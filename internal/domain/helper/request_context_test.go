package helper

import (
	"context"
	"testing"
)

func TestRequestFingerprintContext(t *testing.T) {
	ctx := WithClientFingerprint(context.Background(), "  fingerprint ")
	if got := ClientFingerprintFromContext(ctx); got != "fingerprint" {
		t.Fatalf("unexpected fingerprint: %q", got)
	}
	if got := ClientFingerprintFromContext(WithClientFingerprint(context.Background(), "  ")); got != "" {
		t.Fatalf("expected empty fingerprint, got %q", got)
	}
	if got := ClientFingerprintFromContext(context.TODO()); got != "" {
		t.Fatalf("expected empty fingerprint for nil context, got %q", got)
	}
}
