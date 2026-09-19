package scheduler

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/infrastructure/config"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/scheduler/job"
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/robfig/cron/v3"
)

const schedulerJobTimeout = 15 * time.Minute

type Scheduler struct {
	cfg            *config.AppConfig
	client         *ent.Client
	cron           *cron.Cron
	storageAdapter *objectstorage.StorageAdapter
	jobMetrics     *observability.JobMetrics
}

func New(cfg *config.AppConfig, client *ent.Client, s3Client *s3.Client) *Scheduler {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	storageAdapter := objectstorage.NewStorageAdapter(cfg, s3Client, httpClient)

	c := cron.New(
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)

	return &Scheduler{
		cfg:            cfg,
		client:         client,
		cron:           c,
		storageAdapter: storageAdapter,
		jobMetrics:     observability.NewJobMetrics(),
	}
}

func (s *Scheduler) Start() {
	slog.Info("Starting Scheduler...")

	s.registerJobs()

	s.cron.Start()
	slog.Info("Scheduler started successfully")
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("Scheduler stopped")
}

func (s *Scheduler) registerJobs() {
	_, err := s.cron.AddFunc(s.cfg.EntityCleanupCron, func() {
		_ = s.jobMetrics.RunWithTimeout(context.Background(), "entity_cleanup", schedulerJobTimeout, func(ctx context.Context) error {
			return job.RunEntityCleanup(ctx, s.client, s.cfg)
		})
	})
	if err != nil {
		slog.Error("Failed to register Entity Cleanup job", "error", err)
	} else {
		slog.Info("Registered Entity Cleanup Job", "schedule", s.cfg.EntityCleanupCron)
	}

	_, err = s.cron.AddFunc(s.cfg.PrivateChatCleanupCron, func() {
		_ = s.jobMetrics.RunWithTimeout(context.Background(), "private_chat_cleanup", schedulerJobTimeout, func(ctx context.Context) error {
			return job.RunPrivateChatCleanup(ctx, s.client, s.cfg)
		})
	})
	if err != nil {
		slog.Error("Failed to register Private Chat Cleanup job", "error", err)
	} else {
		slog.Info("Registered Private Chat Cleanup Job", "schedule", s.cfg.PrivateChatCleanupCron)
	}

	_, err = s.cron.AddFunc(s.cfg.MediaCleanupCron, func() {
		_ = s.jobMetrics.RunWithTimeout(context.Background(), "media_cleanup", schedulerJobTimeout, func(ctx context.Context) error {
			return job.RunMediaCleanup(ctx, s.client, s.storageAdapter, s.cfg)
		})
	})
	if err != nil {
		slog.Error("Failed to register Media Cleanup job", "error", err)
	} else {
		slog.Info("Registered Media Cleanup Job", "schedule", s.cfg.MediaCleanupCron)
	}
}
