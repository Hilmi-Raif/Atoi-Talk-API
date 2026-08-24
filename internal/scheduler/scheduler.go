package scheduler

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
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
	storageAdapter *adapter.StorageAdapter
}

func New(cfg *config.AppConfig, client *ent.Client, s3Client *s3.Client) *Scheduler {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	storageAdapter := adapter.NewStorageAdapter(cfg, s3Client, httpClient)

	c := cron.New(
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)

	return &Scheduler{
		cfg:            cfg,
		client:         client,
		cron:           c,
		storageAdapter: storageAdapter,
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
		slog.Info("Starting Entity Cleanup Job")
		ctx, cancel := context.WithTimeout(context.Background(), schedulerJobTimeout)
		defer cancel()
		if err := job.RunEntityCleanup(ctx, s.client, s.cfg); err != nil {
			slog.Error("Entity Cleanup Job failed", "error", err)
		} else {
			slog.Info("Entity Cleanup Job completed")
		}
	})
	if err != nil {
		slog.Error("Failed to register Entity Cleanup job", "error", err)
	} else {
		slog.Info("Registered Entity Cleanup Job", "schedule", s.cfg.EntityCleanupCron)
	}

	_, err = s.cron.AddFunc(s.cfg.PrivateChatCleanupCron, func() {
		slog.Info("Starting Private Chat Cleanup Job")
		ctx, cancel := context.WithTimeout(context.Background(), schedulerJobTimeout)
		defer cancel()
		if err := job.RunPrivateChatCleanup(ctx, s.client, s.cfg); err != nil {
			slog.Error("Private Chat Cleanup Job failed", "error", err)
		} else {
			slog.Info("Private Chat Cleanup Job completed")
		}
	})
	if err != nil {
		slog.Error("Failed to register Private Chat Cleanup job", "error", err)
	} else {
		slog.Info("Registered Private Chat Cleanup Job", "schedule", s.cfg.PrivateChatCleanupCron)
	}

	_, err = s.cron.AddFunc(s.cfg.MediaCleanupCron, func() {
		slog.Info("Starting Media Cleanup Job")
		ctx, cancel := context.WithTimeout(context.Background(), schedulerJobTimeout)
		defer cancel()
		if err := job.RunMediaCleanup(ctx, s.client, s.storageAdapter, s.cfg); err != nil {
			slog.Error("Media Cleanup Job failed", "error", err)
		} else {
			slog.Info("Media Cleanup Job completed")
		}
	})
	if err != nil {
		slog.Error("Failed to register Media Cleanup job", "error", err)
	} else {
		slog.Info("Registered Media Cleanup Job", "schedule", s.cfg.MediaCleanupCron)
	}
}
