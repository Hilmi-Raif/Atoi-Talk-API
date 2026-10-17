package scheduler

import (
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/infrastructure/config"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
)

func TestSchedulerUsesConfiguredTimezone(t *testing.T) {
	scheduler := New(&config.AppConfig{SchedulerTimezone: "Asia/Jakarta"}, nil, nil)

	if scheduler.cron.Location().String() != "Asia/Jakarta" {
		t.Fatalf("expected Asia/Jakarta scheduler timezone, got %s", scheduler.cron.Location())
	}
}

func TestSchedulerFallsBackToUTCForInvalidTimezone(t *testing.T) {
	scheduler := New(&config.AppConfig{SchedulerTimezone: "invalid/timezone"}, nil, nil)

	if scheduler.cron.Location() != time.UTC {
		t.Fatalf("expected UTC fallback, got %s", scheduler.cron.Location())
	}
}

func TestSchedulerStartAndStopRegistersJobs(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:scheduler-test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	scheduler := New(&config.AppConfig{
		EntityCleanupCron:      "@every 1h",
		PrivateChatCleanupCron: "@every 1h",
		MediaCleanupCron:       "@every 1h",
	}, client, nil)

	scheduler.Start()
	for _, entry := range scheduler.cron.Entries() {
		entry.Job.Run()
	}
	scheduler.Stop()
}

func TestSchedulerStartAndStopHandlesJobErrors(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:scheduler-err-test?mode=memory&cache=shared&_fk=1")
	_ = client.Close()

	scheduler := New(&config.AppConfig{
		EntityCleanupCron:      "@every 1h",
		PrivateChatCleanupCron: "@every 1h",
		MediaCleanupCron:       "@every 1h",
	}, client, nil)

	scheduler.Start()
	for _, entry := range scheduler.cron.Entries() {
		entry.Job.Run()
	}
	scheduler.Stop()
}

func TestSchedulerStartAndStopContinuesWhenJobScheduleIsInvalid(t *testing.T) {
	scheduler := New(&config.AppConfig{
		EntityCleanupCron:      "not-a-cron",
		PrivateChatCleanupCron: "@every 1h",
		MediaCleanupCron:       "also-not-a-cron",
	}, nil, nil)

	scheduler.Start()
	scheduler.Stop()
}
