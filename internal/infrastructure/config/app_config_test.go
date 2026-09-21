package config

import (
	"database/sql"
	"os"
	"os/exec"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestEnvironmentHelpers(t *testing.T) {
	t.Setenv("TEST_STRING", "value")
	if got := getEnv("TEST_STRING", "fallback"); got != "value" {
		t.Fatalf("unexpected string env: %q", got)
	}
	if got := getEnv("MISSING_STRING", "fallback"); got != "fallback" {
		t.Fatalf("unexpected fallback string: %q", got)
	}

	t.Setenv("TEST_INT", "42")
	if got := getEnvAsInt("TEST_INT", 1); got != 42 || getEnvAsInt("MISSING_INT", 7) != 7 {
		t.Fatalf("unexpected integer environment values")
	}
	t.Setenv("TEST_INT", "invalid")
	if got := getEnvAsInt("TEST_INT", 7); got != 7 {
		t.Fatalf("invalid integer should use fallback: %d", got)
	}

	t.Setenv("TEST_BOOL", "true")
	if got := getEnvAsBool("TEST_BOOL", false); !got || getEnvAsBool("MISSING_BOOL", true) != true {
		t.Fatalf("unexpected boolean environment values")
	}
	t.Setenv("TEST_BOOL", "invalid")
	if got := getEnvAsBool("TEST_BOOL", true); !got {
		t.Fatal("invalid boolean should use fallback")
	}

	t.Setenv("TEST_FLOAT", "2.5")
	if got := getEnvAsFloat("TEST_FLOAT", 1); got != 2.5 || getEnvAsFloat("MISSING_FLOAT", 1.5) != 1.5 {
		t.Fatalf("unexpected float environment values")
	}
	t.Setenv("TEST_FLOAT", "invalid")
	if got := getEnvAsFloat("TEST_FLOAT", 1.5); got != 1.5 {
		t.Fatal("invalid float should use fallback")
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV(" one, ,two,, three "); len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Fatalf("unexpected CSV result: %#v", got)
	}
	if got := splitCSV("  "); got != nil {
		t.Fatalf("expected nil for empty CSV, got %#v", got)
	}
}

func TestDBConnectionString(t *testing.T) {
	cfg := &AppConfig{
		DBUser: "user", DBPassword: "pass", DBHost: "localhost",
		DBPort: "5432", DBName: "database", DBSSLMode: "disable",
	}
	if got := cfg.DBConnectionString(); got != "postgres://user:pass@localhost:5432/database?sslmode=disable" {
		t.Fatalf("unexpected connection string: %q", got)
	}
}

func TestGetEnvReadsEmptyValueAsPresent(t *testing.T) {
	t.Setenv("EMPTY_VALUE", "")
	value, ok := os.LookupEnv("EMPTY_VALUE")
	if !ok || value != "" || getEnv("EMPTY_VALUE", "fallback") != "" {
		t.Fatal("getEnv should preserve an explicitly empty environment value")
	}
}

func TestLoadAppConfigBuildsCompleteConfiguration(t *testing.T) {
	setCompleteAppConfigEnvironment(t)

	cfg := LoadAppConfig()
	if cfg.AppPort != "8080" || cfg.AppEnv != "test" || cfg.DBMigrate || cfg.JWTExp != 3600 || cfg.OTPExp != 300 {
		t.Fatalf("unexpected core configuration: %+v", cfg)
	}
	if len(cfg.AppCorsAllowedOrigins) != 2 || !cfg.RateLimitEnabled || cfg.RedisDB != 0 {
		t.Fatalf("unexpected list/default configuration: %+v", cfg)
	}
	if cfg.MediaRetentionDays != 3.5 || cfg.SoftDeleteRetentionDays != 14 || cfg.SMTPPort != 1025 || cfg.S3PresignEndpoint != "http://localhost:9090" {
		t.Fatalf("unexpected scheduler/mail configuration: %+v", cfg)
	}
}

func TestAppConfigOTelFields(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_SERVICE_NAME", "atoitalk-test")
	t.Setenv("OTEL_SERVICE_VERSION", "1.0.0")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_SAMPLING_RATIO", "0.5")
	t.Setenv("ENABLE_PPROF", "true")

	cfg := LoadAppConfig()
	if !cfg.OTelEnabled || cfg.OTelServiceName != "atoitalk-test" || cfg.OTelServiceVersion != "1.0.0" || cfg.OTelExporterOTLPEndpoint != "localhost:4317" || !cfg.OTelExporterOTLPInsecure || cfg.OTelSamplingRatio != 0.5 || !cfg.EnablePprof {
		t.Fatalf("unexpected otel configuration: %+v", cfg)
	}
}

func TestAppConfigDefaultsToProductionSafeTraceSampling(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	previous, existed := os.LookupEnv("OTEL_SAMPLING_RATIO")
	requireRestore := func() {
		if existed {
			_ = os.Setenv("OTEL_SAMPLING_RATIO", previous)
			return
		}
		_ = os.Unsetenv("OTEL_SAMPLING_RATIO")
	}
	defer requireRestore()
	_ = os.Unsetenv("OTEL_SAMPLING_RATIO")

	cfg := LoadAppConfig()
	if cfg.OTelSamplingRatio != 0.1 {
		t.Fatalf("expected default trace sampling ratio 0.1, got %v", cfg.OTelSamplingRatio)
	}
}

func TestLoadAppConfigDisablesRateLimitFromEnvironment(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("RATE_LIMIT_ENABLED", "false")

	cfg := LoadAppConfig()
	if cfg.RateLimitEnabled {
		t.Fatal("expected rate limiting to be disabled")
	}
}

func TestLoadAppConfigDisablesCaptchaFromEnvironment(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("CAPTCHA_ENABLED", "false")

	cfg := LoadAppConfig()
	if cfg.CaptchaEnabled {
		t.Fatal("expected captcha to be disabled")
	}
}

func TestLoadAppConfigReadsDatabasePoolConfiguration(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("DB_MAX_OPEN_CONNS", "30")
	t.Setenv("DB_MAX_IDLE_CONNS", "10")
	t.Setenv("DB_CONN_MAX_LIFETIME", "30m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "5m")

	cfg := LoadAppConfig()
	if cfg.DBMaxOpenConns != 30 || cfg.DBMaxIdleConns != 10 || cfg.DBConnMaxLifetime != 30*time.Minute || cfg.DBConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("unexpected database pool configuration: %+v", cfg)
	}
}

func TestLoadAppConfigReadsMessageWorkerConfiguration(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("MESSAGE_WORKER_BATCH_SIZE", "25")
	t.Setenv("MESSAGE_WORKER_CONCURRENCY", "8")
	t.Setenv("MESSAGE_WORKER_LEASE_DURATION", "2m")
	t.Setenv("MESSAGE_WORKER_POLL_INTERVAL", "750ms")
	t.Setenv("MESSAGE_WORKER_BACKLOG_INTERVAL", "5s")
	t.Setenv("MESSAGE_EVENT_STREAM", "events:test")
	t.Setenv("MESSAGE_EVENT_STREAM_MAX_LEN", "5000")

	cfg := LoadAppConfig()
	if cfg.MessageWorkerBatchSize != 25 || cfg.MessageWorkerConcurrency != 8 || cfg.MessageWorkerLeaseDuration != 2*time.Minute || cfg.MessageWorkerPollInterval != 750*time.Millisecond || cfg.MessageWorkerBacklogInterval != 5*time.Second || cfg.MessageEventStream != "events:test" || cfg.MessageEventStreamMaxLen != 5000 {
		t.Fatalf("unexpected message worker configuration: %+v", cfg)
	}
}

func TestLoadAppConfigReadsRealtimeConfiguration(t *testing.T) {
	setCompleteAppConfigEnvironment(t)
	t.Setenv("REALTIME_PORT", "8081")
	t.Setenv("REALTIME_INSTANCE_ID", "realtime-test-1")
	t.Setenv("REALTIME_CONSUMER_GROUP", "realtime-test-group")

	cfg := LoadAppConfig()
	if cfg.RealtimePort != "8081" || cfg.RealtimeInstanceID != "realtime-test-1" || cfg.RealtimeConsumerGroup != "realtime-test-group" {
		t.Fatalf("unexpected realtime configuration: %+v", cfg)
	}
}

func TestGetEnvAsDurationUsesFallbackForInvalidValue(t *testing.T) {
	t.Setenv("TEST_DURATION", "invalid")
	if got := getEnvAsDuration("TEST_DURATION", 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("unexpected duration fallback: %s", got)
	}
}

func TestConfigureDBPoolClampsInvalidValues(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	configureDBPool(db, &AppConfig{
		DBMaxOpenConns:    0,
		DBMaxIdleConns:    20,
		DBConnMaxLifetime: 0,
		DBConnMaxIdleTime: 0,
	})

	stats := db.Stats()
	if stats.MaxOpenConnections != 80 {
		t.Fatalf("unexpected max open connections: %d", stats.MaxOpenConnections)
	}
}

func TestAppConfigFatalValidation(t *testing.T) {
	for _, name := range []string{"missing-required", "empty-required", "invalid-bool", "invalid-int", "invalid-jwt-exp", "invalid-otp-exp"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestAppConfigProcessHelper$")
			cmd.Env = append(os.Environ(), "APP_CONFIG_SUBPROCESS=1", "APP_CONFIG_CASE="+name)
			if err := cmd.Run(); err == nil {
				t.Fatalf("expected subprocess to exit unsuccessfully for %s", name)
			}
		})
	}
}

func TestAppConfigProcessHelper(t *testing.T) {
	if os.Getenv("APP_CONFIG_SUBPROCESS") != "1" {
		return
	}

	switch os.Getenv("APP_CONFIG_CASE") {
	case "missing-required":
		_ = mustGetEnv("APP_CONFIG_MISSING_REQUIRED")
	case "empty-required":
		t.Setenv("APP_CONFIG_EMPTY_REQUIRED", "")
		_ = mustGetEnv("APP_CONFIG_EMPTY_REQUIRED")
	case "invalid-bool":
		t.Setenv("APP_CONFIG_INVALID_BOOL", "invalid")
		_ = mustGetEnvAsBool("APP_CONFIG_INVALID_BOOL")
	case "invalid-int":
		t.Setenv("APP_CONFIG_INVALID_INT", "invalid")
		_ = mustGetEnvAsInt("APP_CONFIG_INVALID_INT")
	case "invalid-jwt-exp":
		setCompleteAppConfigEnvironment(t)
		t.Setenv("JWT_EXP", "0")
		LoadAppConfig()
	case "invalid-otp-exp":
		setCompleteAppConfigEnvironment(t)
		t.Setenv("OTP_EXP", "0")
		LoadAppConfig()
	}
}

func setCompleteAppConfigEnvironment(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"APP_PORT":                   "8080",
		"APP_ENV":                    "test",
		"APP_URL":                    "http://localhost:8080",
		"APP_CORS_ALLOWED_ORIGINS":   "http://localhost:3000, http://localhost:3001",
		"RATE_LIMIT_ENABLED":         "true",
		"DB_HOST":                    "localhost",
		"DB_PORT":                    "5432",
		"DB_USER":                    "postgres",
		"DB_PASSWORD":                "postgres",
		"DB_NAME":                    "test",
		"DB_SSLMODE":                 "disable",
		"DB_MIGRATE":                 "false",
		"GOOGLE_CLIENT_ID":           "client",
		"GOOGLE_CLIENT_SECRET":       "secret",
		"GOOGLE_REDIRECT_URL":        "http://localhost/callback",
		"JWT_SECRET":                 "jwt-secret",
		"JWT_EXP":                    "3600",
		"S3_BUCKET_PUBLIC":           "public",
		"S3_BUCKET_PRIVATE":          "private",
		"S3_REGION":                  "us-east-1",
		"S3_ACCESS_KEY":              "access",
		"S3_SECRET_KEY":              "secret",
		"S3_ENDPOINT":                "http://localhost:9090",
		"S3_PRESIGN_ENDPOINT":        "http://localhost:9090",
		"S3_PUBLIC_DOMAIN":           "http://localhost:9090/public",
		"SMTP_HOST":                  "localhost",
		"SMTP_PORT":                  "1025",
		"SMTP_FROM_EMAIL":            "test@example.com",
		"SMTP_FROM_NAME":             "Test",
		"SMTP_ASYNC":                 "false",
		"OTP_EXP":                    "300",
		"OTP_RATE_LIMIT_SECONDS":     "2",
		"OTP_SECRET":                 "otp-secret",
		"TURNSTILE_SECRET_KEY":       "turnstile",
		"SOFT_DELETE_RETENTION_DAYS": "14",
		"MEDIA_RETENTION_DAYS":       "3.5",
		"ENTITY_CLEANUP_CRON":        "0 1 * * *",
		"PRIVATE_CHAT_CLEANUP_CRON":  "30 1 * * *",
		"MEDIA_CLEANUP_CRON":         "0 2 * * *",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}
