package config

import (
	"os"
	"os/exec"
	"testing"
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
	if len(cfg.AppCorsAllowedOrigins) != 2 || len(cfg.TrustedProxyCIDRs) != 2 || cfg.RedisDB != 0 {
		t.Fatalf("unexpected list/default configuration: %+v", cfg)
	}
	if cfg.MediaRetentionDays != 3.5 || cfg.SoftDeleteRetentionDays != 14 || cfg.SMTPPort != 1025 {
		t.Fatalf("unexpected scheduler/mail configuration: %+v", cfg)
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
		"TRUSTED_PROXY_CIDRS":        "10.0.0.0/8, 192.168.0.0/16",
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
