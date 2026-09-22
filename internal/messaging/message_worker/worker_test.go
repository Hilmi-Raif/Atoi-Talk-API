package messageworker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWorkerConfigFromAppConfig(t *testing.T) {
	cfg := &config.AppConfig{
		MessageWorkerBatchSize:      25,
		MessageWorkerLeaseDuration:  2 * time.Minute,
		MessageWorkerPollInterval:   750 * time.Millisecond,
		MessageWorkerMaxAttempts:    7,
		MessageWorkerRetryBaseDelay: 2 * time.Second,
		MessageWorkerRetryMaxDelay:  2 * time.Minute,
	}

	workerCfg := ConfigFromAppConfig(cfg)
	if workerCfg.BatchSize != 25 || workerCfg.LeaseDuration != 2*time.Minute || workerCfg.PollInterval != 750*time.Millisecond || workerCfg.MaxAttempts != 7 || workerCfg.RetryBaseDelay != 2*time.Second || workerCfg.RetryMaxDelay != 2*time.Minute {
		t.Fatalf("unexpected worker configuration: %+v", workerCfg)
	}
}

func TestWorkerProcessesRecordsInSameChatSequentially(t *testing.T) {
	chatID := uuid.New()
	senderID := uuid.New()
	msgID1 := uuid.New()
	msgID2 := uuid.New()

	rec1 := OutboxRecord{
		ID:        uuid.New(),
		ChatID:    chatID,
		MessageID: msgID1,
		SenderID:  senderID,
		EventType: string(events.EventMessageNew),
	}
	rec2 := OutboxRecord{
		ID:        uuid.New(),
		ChatID:    chatID,
		MessageID: msgID2,
		SenderID:  senderID,
		EventType: string(events.EventMessageDelete),
	}

	var executionOrder []uuid.UUID
	var mu sync.Mutex

	store := &orderTrackingStore{
		records: []OutboxRecord{rec1, rec2},
		onProject: func(r OutboxRecord) {
			mu.Lock()
			executionOrder = append(executionOrder, r.ID)
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		},
	}
	publisher := &fakeStreamPublisher{}

	worker := NewWorker(store, publisher, WorkerConfig{
		BatchSize:   10,
		Concurrency: 4,
	})

	processed, err := worker.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, processed)

	require.Equal(t, []uuid.UUID{rec1.ID, rec2.ID}, executionOrder)
}

type orderTrackingStore struct {
	records   []OutboxRecord
	onProject func(OutboxRecord)
}

func (s *orderTrackingStore) Claim(context.Context, int, time.Duration) ([]OutboxRecord, error) {
	return s.records, nil
}

func (s *orderTrackingStore) Project(_ context.Context, rec OutboxRecord) error {
	if s.onProject != nil {
		s.onProject(rec)
	}
	return nil
}

func (s *orderTrackingStore) BuildEvents(context.Context, OutboxRecord) ([]events.MessageEvent, error) {
	return nil, nil
}

func (s *orderTrackingStore) MarkPublished(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}

func (s *orderTrackingStore) MarkFailed(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}

func (s *orderTrackingStore) MarkDeadLetter(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}

type fakeOutboxStore struct {
	mu            sync.Mutex
	records       []OutboxRecord
	projected     []uuid.UUID
	projectErr    error
	publishedIDs  []uuid.UUID
	publishedLock []uuid.UUID
	failedIDs     []uuid.UUID
	failedLocks   []uuid.UUID
	failedReasons []string
	failedAt      []time.Time
	deadLetterIDs []uuid.UUID
}

type workerLoopStore struct {
	claimCalls chan struct{}
}

func (s *workerLoopStore) Backlog(context.Context) (observability.MessageWorkerBacklog, error) {
	return observability.MessageWorkerBacklog{Pending: 3}, nil
}

func (s *workerLoopStore) Claim(ctx context.Context, _ int, _ time.Duration) ([]OutboxRecord, error) {
	select {
	case s.claimCalls <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, nil
	}
}

func (s *workerLoopStore) Project(context.Context, OutboxRecord) error { return nil }

func (s *workerLoopStore) BuildEvents(context.Context, OutboxRecord) ([]events.MessageEvent, error) {
	return nil, nil
}

func (s *workerLoopStore) MarkPublished(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}

func (s *workerLoopStore) MarkFailed(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}
func (s *workerLoopStore) MarkDeadLetter(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}

type transientWorkerLoopStore struct {
	claimCalls chan struct{}
	claims     int
}

func (s *transientWorkerLoopStore) Backlog(context.Context) (observability.MessageWorkerBacklog, error) {
	return observability.MessageWorkerBacklog{Pending: 1}, nil
}

func (s *transientWorkerLoopStore) Claim(ctx context.Context, _ int, _ time.Duration) ([]OutboxRecord, error) {
	s.claims++
	select {
	case s.claimCalls <- struct{}{}:
	default:
	}
	if s.claims == 1 {
		return nil, errors.New("temporary claim failure")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, nil
	}
}

func (s *transientWorkerLoopStore) Project(context.Context, OutboxRecord) error { return nil }

func (s *transientWorkerLoopStore) BuildEvents(context.Context, OutboxRecord) ([]events.MessageEvent, error) {
	return nil, nil
}

func (s *transientWorkerLoopStore) MarkPublished(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}

func (s *transientWorkerLoopStore) MarkFailed(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}
func (s *transientWorkerLoopStore) MarkDeadLetter(context.Context, uuid.UUID, uuid.UUID, error, time.Time) error {
	return nil
}
func (s *fakeOutboxStore) Project(_ context.Context, record OutboxRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projectErr != nil {
		return s.projectErr
	}
	s.projected = append(s.projected, record.ID)
	return nil
}

func (s *fakeOutboxStore) Claim(context.Context, int, time.Duration) ([]OutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := append([]OutboxRecord(nil), s.records...)
	s.records = nil
	return records, nil
}

func (s *fakeOutboxStore) BuildEvents(_ context.Context, record OutboxRecord) ([]events.MessageEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	targets := []uuid.UUID{record.TargetUserID}
	if record.TargetUserID == uuid.Nil {
		targets = []uuid.UUID{uuid.New(), uuid.New()}
	}

	built := make([]events.MessageEvent, 0, len(targets))
	for _, target := range targets {
		eventID := record.ID
		if len(targets) > 1 {
			eventID = uuid.NewSHA1(uuid.Nil, []byte(record.ID.String()+":"+target.String()))
		}
		built = append(built, events.MessageEvent{
			ID:           eventID,
			OutboxID:     record.ID,
			Type:         record.EventType,
			MessageID:    record.MessageID,
			ChatID:       record.ChatID,
			SenderID:     record.SenderID,
			TargetUserID: target,
			Payload:      []byte(`{"type":"message.new"}`),
		})
	}
	return built, nil
}

func (s *fakeOutboxStore) MarkPublished(_ context.Context, id, lockToken uuid.UUID, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishedIDs = append(s.publishedIDs, id)
	s.publishedLock = append(s.publishedLock, lockToken)
	return nil
}

func (s *fakeOutboxStore) MarkFailed(_ context.Context, id, lockToken uuid.UUID, err error, availableAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedIDs = append(s.failedIDs, id)
	s.failedLocks = append(s.failedLocks, lockToken)
	s.failedReasons = append(s.failedReasons, err.Error())
	s.failedAt = append(s.failedAt, availableAt)
	return nil
}

func (s *fakeOutboxStore) MarkDeadLetter(_ context.Context, id, _ uuid.UUID, _ error, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadLetterIDs = append(s.deadLetterIDs, id)
	return nil
}

type fakeStreamPublisher struct {
	mu          sync.Mutex
	events      []events.MessageEvent
	err         error
	failOnCall  int
	publishCall int
}

func (p *fakeStreamPublisher) Publish(_ context.Context, event events.MessageEvent) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishCall++
	if p.failOnCall > 0 && p.publishCall == p.failOnCall {
		return "", errors.New("redis unavailable")
	}
	if p.err != nil {
		return "", p.err
	}
	p.events = append(p.events, event)
	return "stream-id", nil
}

type fakeWorkerMetrics struct {
	mu        sync.Mutex
	claimed   int
	processed int
	failed    int
	duration  time.Duration
	backlog   observability.MessageWorkerBacklog
	stages    []string
}

func (m *fakeWorkerMetrics) RecordBatch(_ context.Context, claimed, processed, failed int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimed = claimed
	m.processed = processed
	m.failed = failed
	m.duration = duration
}

func (m *fakeWorkerMetrics) RecordBacklog(_ context.Context, backlog observability.MessageWorkerBacklog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backlog = backlog
}

func (m *fakeWorkerMetrics) RecordFailure(_ context.Context, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stages = append(m.stages, stage)
}

func TestWorkerProcessesBatchConcurrently(t *testing.T) {
	records := make([]OutboxRecord, 10)
	for i := range records {
		records[i] = OutboxRecord{
			ID:           uuid.New(),
			LockToken:    uuid.New(),
			EventType:    string(events.EventMessageNew),
			TargetUserID: uuid.New(),
		}
	}
	store := &fakeOutboxStore{records: records}
	publisher := &fakeStreamPublisher{}
	worker := NewWorker(store, publisher, WorkerConfig{
		BatchSize:     10,
		Concurrency:   5,
		LeaseDuration: time.Minute,
	})

	processed, err := worker.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10, processed)
	require.Len(t, publisher.events, 10)
	require.Len(t, store.publishedIDs, 10)
}
func TestWorkerRecordsBatchMetrics(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), LockToken: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	metrics := &fakeWorkerMetrics{}
	worker := NewWorkerWithMetrics(store, &fakeStreamPublisher{}, WorkerConfig{BatchSize: 1}, metrics)

	processed, err := worker.ProcessBatch(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, metrics.claimed)
	require.Equal(t, 1, metrics.processed)
	require.Zero(t, metrics.failed)
	require.GreaterOrEqual(t, metrics.duration, time.Duration(0))
}

func TestWorkerRecordsFailureStage(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), LockToken: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	metrics := &fakeWorkerMetrics{}
	worker := NewWorkerWithMetrics(store, &fakeStreamPublisher{err: errors.New("redis unavailable")}, WorkerConfig{BatchSize: 1}, metrics)

	_, err := worker.ProcessBatch(context.Background())

	require.Error(t, err)
	require.Equal(t, []string{"publish"}, metrics.stages)
}

func TestWorkerRecordsOutboxBacklog(t *testing.T) {
	store := &workerLoopStore{claimCalls: make(chan struct{}, 1)}
	metrics := &fakeWorkerMetrics{}
	worker := NewWorkerWithMetrics(store, &fakeStreamPublisher{}, WorkerConfig{BatchSize: 1}, metrics)

	worker.recordBacklog(context.Background())

	require.Equal(t, observability.MessageWorkerBacklog{Pending: 3}, metrics.backlog)
}

func TestWorkerPublishesClaimedOutboxAndMarksItPublished(t *testing.T) {
	record := OutboxRecord{
		ID:           uuid.New(),
		LockToken:    uuid.New(),
		EventType:    string(events.EventMessageNew),
		MessageID:    uuid.New(),
		ChatID:       uuid.New(),
		SenderID:     uuid.New(),
		TargetUserID: uuid.New(),
	}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	publisher := &fakeStreamPublisher{}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 10, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, publisher.events, 1)
	require.Equal(t, record.ID, publisher.events[0].ID)
	require.Equal(t, record.ID, publisher.events[0].OutboxID)
	require.Equal(t, []uuid.UUID{record.ID}, store.publishedIDs)
	require.Equal(t, []uuid.UUID{record.LockToken}, store.publishedLock)
	require.Empty(t, store.failedIDs)
}

func TestWorkerProjectsBeforePublishing(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	publisher := &fakeStreamPublisher{}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 1, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, []uuid.UUID{record.ID}, store.projected)
	require.Len(t, publisher.events, 2)
}

func TestWorkerRetriesWhenProjectionFailsWithoutPublishing(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}, projectErr: errors.New("projection unavailable")}
	publisher := &fakeStreamPublisher{}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 1, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())

	require.Error(t, err)
	require.Zero(t, processed)
	require.Empty(t, publisher.events)
	require.Equal(t, []uuid.UUID{record.ID}, store.failedIDs)
}

func TestWorkerMarksPublishFailureForRetry(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew), AttemptCount: 9}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	publisher := &fakeStreamPublisher{err: errors.New("redis unavailable")}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 1, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())
	require.Error(t, err)
	require.Equal(t, 0, processed)
	require.Equal(t, []uuid.UUID{record.ID}, store.failedIDs)
	require.Equal(t, "redis unavailable", store.failedReasons[0])
	require.WithinDuration(t, time.Now().UTC().Add(64*time.Second), store.failedAt[0], time.Second)
	require.Empty(t, store.publishedIDs)
}

func TestWorkerUsesBaseDelayForFirstRetry(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew), AttemptCount: 1}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	worker := NewWorker(store, &fakeStreamPublisher{err: errors.New("redis unavailable")}, WorkerConfig{
		BatchSize:      1,
		MaxAttempts:    10,
		RetryBaseDelay: 2 * time.Second,
		RetryMaxDelay:  time.Minute,
	})

	_, err := worker.ProcessBatch(context.Background())
	require.Error(t, err)
	require.WithinDuration(t, time.Now().UTC().Add(2*time.Second), store.failedAt[0], time.Second)
}

func TestWorkerDeadLettersRecordAtMaximumAttempt(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew), AttemptCount: 10}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	worker := NewWorker(store, &fakeStreamPublisher{err: errors.New("redis unavailable")}, WorkerConfig{
		BatchSize:   1,
		MaxAttempts: 10,
	})

	_, err := worker.ProcessBatch(context.Background())
	require.Error(t, err)
	require.Equal(t, []uuid.UUID{record.ID}, store.deadLetterIDs)
	require.Empty(t, store.failedIDs)
}

func TestWorkerPublishesAllFanoutEventsBeforeMarkingOutboxPublished(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	publisher := &fakeStreamPublisher{}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 1, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, publisher.events, 2)
	require.Equal(t, []uuid.UUID{record.ID}, store.publishedIDs)
}

func TestWorkerRetriesOutboxWhenSecondFanoutPublishFails(t *testing.T) {
	record := OutboxRecord{ID: uuid.New(), EventType: string(events.EventMessageNew)}
	store := &fakeOutboxStore{records: []OutboxRecord{record}}
	publisher := &fakeStreamPublisher{failOnCall: 2}
	worker := NewWorker(store, publisher, WorkerConfig{BatchSize: 1, LeaseDuration: time.Minute})

	processed, err := worker.ProcessBatch(context.Background())

	require.Error(t, err)
	require.Zero(t, processed)
	require.Len(t, publisher.events, 1)
	require.Empty(t, store.publishedIDs)
	require.Equal(t, []uuid.UUID{record.ID}, store.failedIDs)
	require.Equal(t, []uuid.UUID{record.LockToken}, store.failedLocks)
}

func TestWorkerRunPollsUntilContextCancellation(t *testing.T) {
	store := &workerLoopStore{claimCalls: make(chan struct{}, 1)}
	worker := NewWorker(store, &fakeStreamPublisher{}, WorkerConfig{
		BatchSize:     1,
		LeaseDuration: time.Minute,
		PollInterval:  time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, nil) }()

	select {
	case <-store.claimCalls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("worker did not poll outbox")
	}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestWorkerRunContinuesAfterTransientClaimFailure(t *testing.T) {
	store := &transientWorkerLoopStore{claimCalls: make(chan struct{}, 4)}
	worker := NewWorker(store, &fakeStreamPublisher{}, WorkerConfig{
		BatchSize:     1,
		LeaseDuration: time.Minute,
		PollInterval:  time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, nil) }()

	select {
	case <-store.claimCalls:
	case <-time.After(time.Second):
		t.Fatal("worker did not perform the initial poll")
	}
	select {
	case <-store.claimCalls:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker did not poll after the transient failure")
	}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

type continuousDrainStore struct {
	fakeOutboxStore
	claimCount int
	claimCalls chan struct{}
}

func (s *continuousDrainStore) Claim(_ context.Context, batchSize int, _ time.Duration) ([]OutboxRecord, error) {
	s.claimCount++
	select {
	case s.claimCalls <- struct{}{}:
	default:
	}
	if s.claimCount <= 2 {
		return []OutboxRecord{{ID: uuid.New(), EventType: string(events.EventMessageNew)}}, nil
	}
	return nil, nil
}

func TestWorkerRunDrainsContinuouslyWhenBatchIsFull(t *testing.T) {
	store := &continuousDrainStore{claimCalls: make(chan struct{}, 10)}
	worker := NewWorker(store, &fakeStreamPublisher{}, WorkerConfig{
		BatchSize:     1,
		LeaseDuration: time.Minute,
		PollInterval:  time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, nil) }()

	for i := 0; i < 2; i++ {
		select {
		case <-store.claimCalls:
		case <-time.After(time.Second):
			t.Fatalf("worker did not drain immediately on call %d", i+1)
		}
	}
	cancel()
	<-done
}
