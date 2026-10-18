package scheduler

import (
	"AtoiTalkAPI/internal/infrastructure/observability"
	"bytes"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"log/slog"
	"testing"
)

func TestCronLoggerUsesSharedHandler(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(observability.NewLogger(&output, "production", "scheduler", "test"))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cronLogger{}.Error(errors.New("scheduler failure"), "Job failed", "job", "cleanup")
	var entry map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &entry))
	require.Equal(t, "scheduler", entry["service"])
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "cleanup", entry["job"])
	require.Contains(t, entry["source"].(map[string]any)["function"], "cronLogger.Error")
}
