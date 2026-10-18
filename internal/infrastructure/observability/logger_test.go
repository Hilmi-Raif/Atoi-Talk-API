package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"testing"
)

func TestLoggerSourceContextAndRedaction(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, "production", "push-worker", "1.0")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	logger.With("token", "secret-device-token").ErrorContext(ctx, "Delivery failed", slog.Group("request", "authorization", "Bearer secret-jwt", "url", "/ws?token=secret-query&limit=2"), "error", errors.New("connect postgres://user:secret-password@localhost/db?password=secret-query"))
	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	require.Equal(t, "push-worker", record["service"])
	require.Equal(t, sc.TraceID().String(), record["trace_id"])
	require.Contains(t, record["source"].(map[string]any)["function"], "TestLoggerSourceContextAndRedaction")
	require.NotContains(t, output.String(), "secret-")
	require.Contains(t, output.String(), "REDACTED")
}

func TestTraceParentRoundTripAndInvalidInput(t *testing.T) {
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{3}, SpanID: trace.SpanID{4}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	parent := TraceParent(ctx)
	require.NotEmpty(t, parent)
	restored := trace.SpanContextFromContext(WithTraceParent(context.Background(), parent))
	require.Equal(t, sc.TraceID(), restored.TraceID())
	require.Equal(t, sc.SpanID(), restored.SpanID())
	require.False(t, trace.SpanContextFromContext(WithTraceParent(context.Background(), "invalid")).IsValid())
}
