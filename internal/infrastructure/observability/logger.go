package observability

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var credentialQuery = regexp.MustCompile(`(?i)([?&]|^)(access_token|refresh_token|token|code|password|secret|api_key)=([^&\s]*)`)
var credentialURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

func NewLogger(output io.Writer, environment, service, version string) *slog.Logger {
	options := &slog.HandlerOptions{AddSource: true, Level: slog.LevelInfo, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		key := strings.ToLower(a.Key)
		switch key {
		case "authorization", "cookie", "set-cookie", "token", "access_token", "refresh_token", "fcm_token", "password", "secret", "otp", "device_secret", "credential", "credentials", "body", "payload":
			return slog.String(a.Key, "[REDACTED]")
		}
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(err.Error())
		}
		if a.Value.Kind() == slog.KindString {
			value := credentialQuery.ReplaceAllString(a.Value.String(), "${1}${2}=[REDACTED]")
			a.Value = slog.StringValue(credentialURL.ReplaceAllString(value, "${1}[REDACTED]@"))
		}
		if a.Key == slog.SourceKey {
			if source, ok := a.Value.Any().(*slog.Source); ok {
				copy := *source
				copy.File = strings.ReplaceAll(copy.File, "\\", "/")
				if i := strings.Index(copy.File, "/internal/"); i >= 0 {
					copy.File = copy.File[i+1:]
				} else if i = strings.Index(copy.File, "/cmd/"); i >= 0 {
					copy.File = copy.File[i+1:]
				}
				a.Value = slog.AnyValue(&copy)
			}
		}
		return a
	}}
	var handler slog.Handler
	if environment == "production" {
		handler = slog.NewJSONHandler(output, options)
	} else {
		handler = slog.NewTextHandler(output, options)
	}
	return slog.New(NewTraceContextHandler(handler)).With("service", service, "version", version, "environment", environment)
}
