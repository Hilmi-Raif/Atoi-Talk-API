package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type AppConfig struct {
	AppPort               string
	RealtimePort          string
	AppEnv                string
	AppURL                string
	AppCorsAllowedOrigins []string
	RateLimitEnabled      bool
	CaptchaEnabled        bool

	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
	DBMigrate  bool

	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration

	RedisHost     string
	RedisPort     string
	RedisPassword string
	RedisDB       int

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	JWTSecret string
	JWTExp    int

	S3BucketPublic    string
	S3BucketPrivate   string
	S3Region          string
	S3AccessKey       string
	S3SecretKey       string
	S3Endpoint        string
	S3PresignEndpoint string
	S3PublicDomain    string

	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPassword  string
	SMTPFromEmail string
	SMTPFromName  string
	SMTPAsync     bool

	OTPExp              int
	OTPRateLimitSeconds int

	OTPSecret string

	TurnstileSecretKey string

	SoftDeleteRetentionDays int
	MediaRetentionDays      float64

	EntityCleanupCron      string
	PrivateChatCleanupCron string
	MediaCleanupCron       string

	OTelEnabled              bool
	OTelServiceName          string
	OTelServiceVersion       string
	OTelExporterOTLPEndpoint string
	OTelExporterOTLPInsecure bool
	OTelSamplingRatio        float64
	OTelMetricExportInterval time.Duration

	MessageWorkerBatchSize       int
	MessageWorkerConcurrency     int
	MessageWorkerLeaseDuration   time.Duration
	MessageWorkerPollInterval    time.Duration
	MessageWorkerBacklogInterval time.Duration
	MessageEventStream           string
	MessageEventStreamMaxLen     int
	RealtimeInstanceID           string
	RealtimeConsumerGroup        string

	EnablePprof bool
}

func LoadAppConfig() *AppConfig {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found, reading from system environment variables")
	}

	cfg := &AppConfig{
		AppPort:               getEnv("APP_PORT", "8080"),
		RealtimePort:          getEnv("REALTIME_PORT", "8081"),
		AppEnv:                getEnv("APP_ENV", "development"),
		AppURL:                getEnv("APP_URL", "http://localhost:8080"),
		AppCorsAllowedOrigins: strings.Split(getEnv("APP_CORS_ALLOWED_ORIGINS", "*"), ","),
		RateLimitEnabled:      getEnvAsBool("RATE_LIMIT_ENABLED", true),
		CaptchaEnabled:        getEnvAsBool("CAPTCHA_ENABLED", true),

		DBHost:     mustGetEnv("DB_HOST"),
		DBPort:     mustGetEnv("DB_PORT"),
		DBUser:     mustGetEnv("DB_USER"),
		DBPassword: mustGetEnv("DB_PASSWORD"),
		DBName:     mustGetEnv("DB_NAME"),
		DBSSLMode:  mustGetEnv("DB_SSLMODE"),
		DBMigrate:  getEnvAsBool("DB_MIGRATE", false),

		DBMaxOpenConns:    getEnvAsInt("DB_MAX_OPEN_CONNS", 80),
		DBMaxIdleConns:    getEnvAsInt("DB_MAX_IDLE_CONNS", 40),
		DBConnMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBConnMaxIdleTime: getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),

		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvAsInt("REDIS_DB", 0),

		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", ""),

		JWTSecret: getEnv("JWT_SECRET", ""),
		JWTExp:    getEnvAsInt("JWT_EXP", 86400),

		S3BucketPublic:    mustGetEnv("S3_BUCKET_PUBLIC"),
		S3BucketPrivate:   mustGetEnv("S3_BUCKET_PRIVATE"),
		S3Region:          getEnv("S3_REGION", ""),
		S3AccessKey:       getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:       getEnv("S3_SECRET_KEY", ""),
		S3Endpoint:        getEnv("S3_ENDPOINT", ""),
		S3PresignEndpoint: getEnv("S3_PRESIGN_ENDPOINT", ""),
		S3PublicDomain:    getEnv("S3_PUBLIC_DOMAIN", ""),

		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnvAsInt("SMTP_PORT", 587),
		SMTPUser:      getEnv("SMTP_USER", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
		SMTPFromEmail: getEnv("SMTP_FROM_EMAIL", ""),
		SMTPFromName:  getEnv("SMTP_FROM_NAME", ""),
		SMTPAsync:     getEnvAsBool("SMTP_ASYNC", true),

		OTPExp:              getEnvAsInt("OTP_EXP", 300),
		OTPRateLimitSeconds: getEnvAsInt("OTP_RATE_LIMIT_SECONDS", 60),
		OTPSecret:           getEnv("OTP_SECRET", ""),

		TurnstileSecretKey: getEnv("TURNSTILE_SECRET_KEY", ""),

		SoftDeleteRetentionDays: getEnvAsInt("SOFT_DELETE_RETENTION_DAYS", 30),
		MediaRetentionDays:      getEnvAsFloat("MEDIA_RETENTION_DAYS", 7.0),

		EntityCleanupCron:      getEnv("ENTITY_CLEANUP_CRON", "0 2 * * *"),
		PrivateChatCleanupCron: getEnv("PRIVATE_CHAT_CLEANUP_CRON", "30 2 * * *"),
		MediaCleanupCron:       getEnv("MEDIA_CLEANUP_CRON", "0 3 * * *"),

		OTelEnabled:              getEnvAsBool("OTEL_ENABLED", false),
		OTelServiceName:          getEnv("OTEL_SERVICE_NAME", "atoitalk-api"),
		OTelServiceVersion:       getEnv("OTEL_SERVICE_VERSION", "0.1.1"),
		OTelExporterOTLPEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTelExporterOTLPInsecure: getEnvAsBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		OTelSamplingRatio:        getEnvAsFloat("OTEL_SAMPLING_RATIO", 0.1),
		OTelMetricExportInterval: getEnvAsDuration("OTEL_METRIC_EXPORT_INTERVAL", 5*time.Second),

		MessageWorkerBatchSize:       getEnvAsInt("MESSAGE_WORKER_BATCH_SIZE", 100),
		MessageWorkerConcurrency:     getEnvAsInt("MESSAGE_WORKER_CONCURRENCY", 5),
		MessageWorkerLeaseDuration:   getEnvAsDuration("MESSAGE_WORKER_LEASE_DURATION", 5*time.Minute),
		MessageWorkerPollInterval:    getEnvAsDuration("MESSAGE_WORKER_POLL_INTERVAL", time.Second),
		MessageWorkerBacklogInterval: getEnvAsDuration("MESSAGE_WORKER_BACKLOG_INTERVAL", 5*time.Second),
		MessageEventStream:           getEnv("MESSAGE_EVENT_STREAM", "events:messages"),
		MessageEventStreamMaxLen:     getEnvAsInt("MESSAGE_EVENT_STREAM_MAX_LEN", 10000),
		RealtimeInstanceID:           getEnv("REALTIME_INSTANCE_ID", "realtime-1"),
		RealtimeConsumerGroup:        getEnv("REALTIME_CONSUMER_GROUP", ""),

		EnablePprof: getEnvAsBool("ENABLE_PPROF", false),
	}

	if cfg.JWTExp <= 0 {
		slog.Error("JWT_EXP must be greater than 0", "value", cfg.JWTExp)
		os.Exit(1)
	}
	if cfg.OTPExp <= 0 {
		slog.Error("OTP_EXP must be greater than 0", "value", cfg.OTPExp)
		os.Exit(1)
	}

	return cfg
}

func (c *AppConfig) DBConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

func mustGetEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		slog.Error("Environment variable is required but not set", "key", key)
		os.Exit(1)
	}
	return value
}

func mustGetEnvAsBool(key string) bool {
	valStr := mustGetEnv(key)
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		slog.Error("Environment variable must be a boolean (true/false)", "key", key, "value", valStr)
		os.Exit(1)
	}
	return val
}

func mustGetEnvAsInt(key string) int {
	valStr := mustGetEnv(key)
	val, err := strconv.Atoi(valStr)
	if err != nil {
		slog.Error("Environment variable must be an integer", "key", key, "value", valStr)
		os.Exit(1)
	}
	return val
}

func getEnvAsFloat(key string, fallback float64) float64 {
	valStr, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		slog.Warn("Environment variable must be a float, using fallback", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return val
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	valStr, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		slog.Warn("Environment variable must be an integer, using fallback", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return val
}

func getEnvAsBool(key string, fallback bool) bool {
	valStr, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		slog.Warn("Environment variable must be a boolean, using fallback", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return val
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration
	}
	if ms, err := strconv.Atoi(value); err == nil {
		return time.Duration(ms) * time.Millisecond
	}
	slog.Warn("Environment variable must be a duration, using fallback", "key", key, "value", value, "fallback", fallback)
	return fallback
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}
