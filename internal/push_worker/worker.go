package pushworker

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/pushdevice"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/fcm"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"sync"
	"time"
)

type Worker struct {
	Store       *Store
	Sender      fcm.Sender
	Redis       redis.UniversalClient
	Concurrency int
}

func (w *Worker) Run(ctx context.Context) error {
	n := w.Concurrency
	if n <= 0 {
		n = 4
	}
	n = min(n, 32)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.run(ctx) }()
	}
	wg.Wait()
	return nil
}
func (w *Worker) run(ctx context.Context) {
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := w.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "Push delivery processing failed", "stage", "claim_or_finish", "error_type", fmt.Sprintf("%T", err))
			}
		}
		if !worked || err != nil {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}
}
func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	row, err := w.Store.Claim(ctx)
	if err != nil || row == nil {
		return false, err
	}
	origin, originErr := w.Store.Client.MessageOutbox.Get(ctx, row.EventID)
	if originErr != nil && !ent.IsNotFound(originErr) {
		return true, w.retry(ctx, row, originErr)
	}
	if origin != nil {
		parent, _ := origin.Payload[observability.TraceParentKey].(string)
		ctx = observability.WithTraceParent(ctx, parent)
	}
	ctx, span := observability.StartServiceSpan(ctx, "push_worker.deliver")
	defer span.End()
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	device, err := w.Store.Client.PushDevice.Get(sendCtx, row.DeviceID)
	if ent.IsNotFound(err) {
		return true, w.finish(ctx, row, "skipped", "device_missing", nil, 0)
	}
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	if device.DisabledAt != nil || device.BindingID != row.BindingID || device.UserID != row.UserID || !device.ExpiresAt.After(time.Now().UTC()) || !row.ExpiresAt.After(time.Now().UTC()) {
		return true, w.finish(ctx, row, "skipped", "binding_inactive_or_expired", nil, 0)
	}
	valid, err := w.sessionValid(sendCtx, device)
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	if !valid {
		return true, w.finish(ctx, row, "skipped", "session_revoked", nil, 0)
	}
	msg, err := w.Store.Client.Message.Get(sendCtx, row.MessageID)
	if ent.IsNotFound(err) {
		return true, w.finish(ctx, row, "skipped", "message_missing", nil, 0)
	}
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	preferences, err := NotificationRecipients(sendCtx, w.Store.Client, row.ChatID, []uuid.UUID{row.UserID})
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	if !preferences[row.UserID] {
		return true, w.finish(ctx, row, "skipped", "notifications_muted", nil, 0)
	}
	eligible, err := Eligible(sendCtx, w.Store.Client, msg, row.UserID)
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	if !eligible {
		return true, w.finish(ctx, row, "skipped", "recipient_ineligible", nil, 0)
	}
	title, body, err := notificationPreview(sendCtx, w.Store.Client, msg)
	if err != nil {
		return true, w.retry(ctx, row, err)
	}
	err = w.Sender.Send(sendCtx, device.Token, map[string]string{"event_id": row.EventID.String(), "chat_id": row.ChatID.String(), "message_id": row.MessageID.String(), "title": title, "body": body})
	if err == nil {
		return true, w.finish(ctx, row, "sent", "provider_accepted", nil, 0)
	}
	var failure *fcm.SendError
	if errors.As(err, &failure) {
		if failure.Invalid {
			_, disableErr := w.Store.Client.PushDevice.Update().Where(pushdevice.ID(device.ID), pushdevice.BindingID(device.BindingID), pushdevice.Token(device.Token)).SetDisabledAt(time.Now().UTC()).Save(ctx)
			if disableErr != nil {
				return true, w.retry(ctx, row, disableErr)
			}
			return true, w.finish(ctx, row, "invalid_token", "provider_unregistered", err, 0)
		}
		if !failure.Retry {
			return true, w.finish(ctx, row, "dead_letter", "provider_rejected", err, 0)
		}
	}
	return true, w.retry(ctx, row, err)
}
func (w *Worker) retry(ctx context.Context, row *ent.PushDelivery, cause error) error {
	if row.AttemptCount >= 10 || !row.ExpiresAt.After(time.Now().UTC()) {
		return w.finish(ctx, row, "dead_letter", "attempts_or_expiry_exhausted", cause, 0)
	}
	delay := helper.BackoffDelay(row.AttemptCount-1, time.Second, 5*time.Minute)
	var failure *fcm.SendError
	if errors.As(cause, &failure) && failure.After > delay {
		delay = failure.After
	}
	return w.finish(ctx, row, "pending", "transient_failure", cause, delay)
}
func (w *Worker) sessionValid(ctx context.Context, device *ent.PushDevice) (bool, error) {
	if w.Redis == nil {
		return false, fmt.Errorf("push session store unavailable")
	}
	logout, err := w.Redis.Exists(ctx, "push_logout:"+device.SessionHash).Result()
	if err != nil || logout > 0 {
		return false, err
	}
	marker, err := w.Redis.Get(ctx, "revoked_user:"+device.UserID.String()).Result()
	if err == redis.Nil {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	revoked, err := helper.IsSessionRevoked(marker, device.IssuedAt)
	return !revoked, err
}

func (w *Worker) finish(ctx context.Context, row *ent.PushDelivery, outcome, reason string, cause error, delay time.Duration) error {
	err := w.Store.Finish(ctx, row, outcome, cause, delay)
	level := slog.LevelDebug
	if outcome == "pending" {
		level = slog.LevelWarn
	}
	if outcome == "dead_letter" || err != nil {
		level = slog.LevelError
	}
	attrs := []any{"delivery_id", row.ID, "event_id", row.EventID, "message_id", row.MessageID, "attempt", row.AttemptCount, "stage", "finish", "outcome", outcome, "reason", reason}
	if delay > 0 {
		attrs = append(attrs, "retry_at", time.Now().UTC().Add(delay))
	}
	var provider *fcm.SendError
	if errors.As(cause, &provider) {
		attrs = append(attrs, "provider_status", provider.Status)
	}
	if cause != nil {
		attrs = append(attrs, "error_type", fmt.Sprintf("%T", cause))
		observability.RecordError(trace.SpanFromContext(ctx), errors.New("push delivery attempt failed"))
	}
	if err != nil {
		attrs = append(attrs, "error_type", fmt.Sprintf("%T", err), "finish_failed", true)
	}
	slog.Log(ctx, level, "Push delivery outcome", attrs...)
	return err
}
