package helper

import (
	"context"
	"strings"
)

type requestContextKey string

const clientFingerprintContextKey requestContextKey = "client_fingerprint"

func WithClientFingerprint(ctx context.Context, fingerprint string) context.Context {
	cleaned := strings.TrimSpace(fingerprint)
	if cleaned == "" {
		return ctx
	}

	return context.WithValue(ctx, clientFingerprintContextKey, cleaned)
}

func ClientFingerprintFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	value, _ := ctx.Value(clientFingerprintContextKey).(string)
	return strings.TrimSpace(value)
}
