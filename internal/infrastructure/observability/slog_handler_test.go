package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextHandlerWithoutSpan(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewTraceContextHandler(inner)
	logger := slog.New(handler)

	logger.InfoContext(context.Background(), "test message", "user_id", "123")

	var data map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("failed to parse json log: %v", err)
	}

	if data["msg"] != "test message" || data["user_id"] != "123" {
		t.Fatalf("unexpected log data: %+v", data)
	}
	if _, ok := data["trace_id"]; ok {
		t.Fatal("expected no trace_id when span is not present")
	}
}

func TestTraceContextHandlerWithSpan(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewTraceContextHandler(inner)
	logger := slog.New(handler)

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})

	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "traced message", "action", "login")

	var data map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("failed to parse json log: %v", err)
	}

	if data["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected trace_id in log, got: %v", data["trace_id"])
	}
	if data["span_id"] != "00f067aa0ba902b7" {
		t.Fatalf("expected span_id in log, got: %v", data["span_id"])
	}
}

func TestTraceContextHandlerWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, nil)
	handler := NewTraceContextHandler(inner)

	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected handler to be enabled for info level")
	}

	handlerWithAttrs := handler.WithAttrs([]slog.Attr{slog.String("env", "test")})
	handlerWithGroup := handlerWithAttrs.WithGroup("audit")
	logger := slog.New(handlerWithGroup)

	logger.InfoContext(context.Background(), "grouped log", "status", "ok")

	output := buf.String()
	if !strings.Contains(output, "env=test") || !strings.Contains(output, "grouped log") {
		t.Fatalf("unexpected formatted log output: %s", output)
	}
}
