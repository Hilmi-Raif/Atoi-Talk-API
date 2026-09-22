package messageworker

import (
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type OutboxRecord struct {
	ID           uuid.UUID
	LockToken    uuid.UUID
	EventType    string
	MessageID    uuid.UUID
	ChatID       uuid.UUID
	SenderID     uuid.UUID
	TargetUserID uuid.UUID
	Payload      map[string]interface{}
	AttemptCount int
}

type OutboxStore interface {
	Claim(context.Context, int, time.Duration) ([]OutboxRecord, error)
	Project(context.Context, OutboxRecord) error
	BuildEvents(context.Context, OutboxRecord) ([]events.MessageEvent, error)
	MarkPublished(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	MarkFailed(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error
	MarkDeadLetter(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error
}

type StreamPublisher interface {
	Publish(context.Context, events.MessageEvent) (string, error)
}

type WorkerConfig struct {
	BatchSize       int
	Concurrency     int
	LeaseDuration   time.Duration
	PollInterval    time.Duration
	BacklogInterval time.Duration
	MaxAttempts     int
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
}

func ConfigFromAppConfig(cfg *config.AppConfig) WorkerConfig {
	if cfg == nil {
		return WorkerConfig{}
	}
	return WorkerConfig{
		BatchSize:       cfg.MessageWorkerBatchSize,
		Concurrency:     cfg.MessageWorkerConcurrency,
		LeaseDuration:   cfg.MessageWorkerLeaseDuration,
		PollInterval:    cfg.MessageWorkerPollInterval,
		BacklogInterval: cfg.MessageWorkerBacklogInterval,
		MaxAttempts:     cfg.MessageWorkerMaxAttempts,
		RetryBaseDelay:  cfg.MessageWorkerRetryBaseDelay,
		RetryMaxDelay:   cfg.MessageWorkerRetryMaxDelay,
	}
}

type Worker struct {
	store     OutboxStore
	publisher StreamPublisher
	config    WorkerConfig
	metrics   WorkerMetrics
}

func NewWorker(store OutboxStore, publisher StreamPublisher, cfg WorkerConfig) *Worker {
	return NewWorkerWithMetrics(store, publisher, cfg, observability.NewDefaultMessageWorkerMetrics())
}

type WorkerMetrics interface {
	RecordBatch(context.Context, int, int, int, time.Duration)
}

type failureMetrics interface {
	RecordFailure(context.Context, string)
}

func NewWorkerWithMetrics(store OutboxStore, publisher StreamPublisher, cfg WorkerConfig, metrics WorkerMetrics) *Worker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 5 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.BacklogInterval <= 0 {
		cfg.BacklogInterval = 5 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 10
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = time.Second
	}
	if cfg.RetryMaxDelay <= 0 {
		cfg.RetryMaxDelay = 64 * time.Second
	}
	return &Worker{store: store, publisher: publisher, config: cfg, metrics: metrics}
}

func (w *Worker) Run(ctx context.Context, wakeup <-chan struct{}) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	backlogTicker := time.NewTicker(w.config.BacklogInterval)
	defer backlogTicker.Stop()

	for {
		processed, err := w.ProcessBatch(ctx)
		if err == nil && processed == w.config.BatchSize {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-wakeup:
		case <-backlogTicker.C:
			w.recordBacklog(ctx)
		}
	}
}

func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	started := time.Now()
	records, err := w.store.Claim(ctx, w.config.BatchSize, w.config.LeaseDuration)
	if err != nil {
		w.recordFailure(ctx, "claim")
		w.recordBatchMetrics(ctx, 0, 0, 1, started)
		return 0, err
	}

	if len(records) == 0 {
		w.recordBatchMetrics(ctx, 0, 0, 0, started)
		return 0, nil
	}

	concurrency := w.config.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}

	groups := make(map[uuid.UUID][]OutboxRecord)
	groupOrder := make([]uuid.UUID, 0)
	for _, record := range records {
		key := record.ChatID
		if key == uuid.Nil {
			key = record.ID
		}
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], record)
	}

	if concurrency > len(groupOrder) {
		concurrency = len(groupOrder)
	}

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, concurrency)
		processed atomic.Int64
		failed    atomic.Int64
		firstErr  error
		errOnce   sync.Once
	)

	for _, key := range groupOrder {
		groupRecords := groups[key]
		sem <- struct{}{}
		wg.Add(1)
		go func(recs []OutboxRecord) {
			defer func() {
				<-sem
				wg.Done()
			}()
			for _, rec := range recs {
				if err := w.processRecord(ctx, rec); err != nil {
					failed.Add(1)
					errOnce.Do(func() {
						firstErr = err
					})
					return
				}
				processed.Add(1)
			}
		}(groupRecords)
	}
	wg.Wait()

	totalProcessed := int(processed.Load())
	totalFailed := int(failed.Load())
	w.recordBatchMetrics(ctx, len(records), totalProcessed, totalFailed, started)
	if firstErr != nil {
		return totalProcessed, firstErr
	}
	return totalProcessed, nil
}

func (w *Worker) recordBatchMetrics(ctx context.Context, claimed, processed, failed int, started time.Time) {
	if w.metrics != nil {
		w.metrics.RecordBatch(ctx, claimed, processed, failed, time.Since(started))
	}
}

func (w *Worker) recordBacklog(ctx context.Context) {
	store, ok := w.store.(observability.MessageWorkerBacklogStore)
	if !ok || w.metrics == nil {
		return
	}
	backlog, err := store.Backlog(ctx)
	if err != nil {
		return
	}
	if metrics, ok := w.metrics.(interface {
		RecordBacklog(context.Context, observability.MessageWorkerBacklog)
	}); ok {
		metrics.RecordBacklog(ctx, backlog)
	}
}

func (w *Worker) processRecord(ctx context.Context, record OutboxRecord) error {
	stage := "project"
	err := w.store.Project(ctx, record)
	if err == nil {
		stage = "build"
		eventsToPublish, buildErr := w.store.BuildEvents(ctx, record)
		err = buildErr
		if err == nil {
			stage = "publish"
			for _, event := range eventsToPublish {
				if _, err = w.publisher.Publish(ctx, event); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		w.recordFailure(ctx, stage)
		if record.AttemptCount >= w.config.MaxAttempts {
			markErr := w.store.MarkDeadLetter(ctx, record.ID, recordLockToken(record), err, time.Now().UTC())
			if markErr != nil {
				return fmt.Errorf("process outbox record: %w (mark dead letter: %v)", err, markErr)
			}
			return err
		}
		retryDelay := helper.BackoffDelay(record.AttemptCount-1, w.config.RetryBaseDelay, w.config.RetryMaxDelay)
		markErr := w.store.MarkFailed(ctx, record.ID, recordLockToken(record), err, time.Now().UTC().Add(retryDelay))
		if markErr != nil {
			return fmt.Errorf("process outbox record: %w (mark failed: %v)", err, markErr)
		}
		return err
	}

	if err := w.store.MarkPublished(ctx, record.ID, recordLockToken(record), time.Now().UTC()); err != nil {
		w.recordFailure(ctx, "mark")
		return fmt.Errorf("mark outbox record published: %w", err)
	}
	return nil
}

func (w *Worker) recordFailure(ctx context.Context, stage string) {
	if metrics, ok := w.metrics.(failureMetrics); ok {
		metrics.RecordFailure(ctx, stage)
	}
}

func recordLockToken(record OutboxRecord) uuid.UUID {
	return record.LockToken
}
