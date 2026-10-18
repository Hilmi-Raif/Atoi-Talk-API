# API

[Project overview](../../README.md) | [Configuration reference](../../docs/configuration.md)

HTTP endpoints, authentication, media upload, chat operations and transactional message/outbox writes.

## Dependencies and startup

PostgreSQL, Redis, S3-compatible storage; SMTP for OTP emails. Google OAuth and Turnstile are needed only when those flows are enabled.

Set `DB_MIGRATE=true` when using API startup to apply the schema. Start workers after migration completes. `PUSH_ENABLED=true` enables device registration; the message worker and push worker must also enable it.

## Environment

Set variables in the process environment or a repository-root `.env` file. The table lists defaults and required values; see [Configuration](../../docs/configuration.md) for networking and telemetry options.

| Variable | Default / requirement | Purpose |
|---|---|---|
| `DB_HOST / DB_PORT` | Required | PostgreSQL address; local native `localhost:5434`, Compose `postgres:5432`. |
| `DB_USER / DB_PASSWORD / DB_NAME / DB_SSLMODE` | Required | PostgreSQL credentials, database and SSL mode. |
| `DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS` | 80 / 40 | Connection pool per process. |
| `DB_CONN_MAX_LIFETIME / DB_CONN_MAX_IDLE_TIME` | 30m / 5m | Connection lifetime and idle timeout. |
| `REDIS_HOST` | `localhost` | Redis host. |
| `REDIS_PORT` | `6379` | Redis port. |
| `REDIS_PASSWORD` | empty | Redis authentication. |
| `REDIS_DB` | `0` | Redis database number; use the same application DB across participating services. |
| `S3_BUCKET_PUBLIC / S3_BUCKET_PRIVATE` | Required | Public and private bucket names. |
| `S3_REGION` | empty | S3 region. |
| `S3_ACCESS_KEY / S3_SECRET_KEY` | empty | Static S3 credentials. |
| `S3_ENDPOINT` | empty | S3 API endpoint; empty uses provider default. |
| `S3_PRESIGN_ENDPOINT` | empty | Browser-accessible endpoint for signed URLs. |
| `S3_PUBLIC_DOMAIN` | empty | Public bucket/CDN URL. |
| `STORAGE_DOWNLOAD_MAX_RETRIES / STORAGE_DOWNLOAD_RETRY_DELAY` | 2 / 250ms | Storage download retry policy. |
| `APP_PORT` | `8080` | HTTP listen port. |
| `APP_URL` | `http://localhost:8080` | API base URL. |
| `APP_CORS_ALLOWED_ORIGINS` | `*` | Comma-separated frontend origins. |
| `JWT_SECRET` | empty | Required for secure JWT signing; match the gateway. |
| `JWT_EXP` | `86400` | JWT lifetime in seconds. |
| `OTP_SECRET` | empty | Required for secure OTP hashing. |
| `OTP_EXP` | `300` | OTP lifetime in seconds. |
| `OTP_RATE_LIMIT_SECONDS` | `60` | OTP request cooldown in seconds. |
| `DB_MIGRATE` | `false` | Apply schema on API startup. |
| `PUSH_ENABLED` | `false` | Enable device registration. |
| `RATE_LIMIT_ENABLED` | `true` | Enable rate limiting. |
| `CAPTCHA_ENABLED` | `true` | Enable Turnstile verification. |
| `TURNSTILE_SECRET_KEY` | empty | Required when CAPTCHA is enabled. |
| `CAPTCHA_MAX_RETRIES / CAPTCHA_RETRY_DELAY` | 2 / 250ms | CAPTCHA provider retries. |
| `GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET / GOOGLE_REDIRECT_URL` | empty | Configure together for Google OAuth. |
| `SMTP_HOST` | empty | SMTP server. |
| `SMTP_PORT` | `587` | SMTP port. |
| `SMTP_USER / SMTP_PASSWORD` | empty | SMTP credentials when needed. |
| `SMTP_FROM_EMAIL / SMTP_FROM_NAME` | empty | OTP email sender. |
| `SMTP_ASYNC` | `true` | Send email asynchronously. |
| `SMTP_MAX_RETRIES / SMTP_RETRY_DELAY` | 1 / 500ms | SMTP retry policy. |
| `ENABLE_PPROF` | `false` | Expose profiling endpoints. |
| `APP_ENV` | `development` | Runtime environment label. |
| `OTEL_ENABLED` | `false` | Enable traces and metrics. |
| `OTEL_*` | See shared reference | Exporter, sampling, version and service identity settings. |

## Run

From the repository root, after backing services and schema are ready:

```sh
go run ./cmd/api
```

Build a standalone binary:

```sh
go build -o bin/api ./cmd/api
```

Docker build and standalone run:

```sh
docker build -f build/package/api/Dockerfile -t atoitalk-api .
docker run --env-file .env -p 8080:8080 atoitalk-api
```

For standalone containers, `.env` must contain container-reachable dependency addresses rather than native `localhost` values. The [root quickstart](../../README.md#complete-local-stack) provides a Compose alternative with service DNS wiring.

## Health and operation

GET `/livez` for liveness; `/readyz` checks PostgreSQL and Redis. `/healthz` provides the health response. Default HTTP port: 8080.

## Notification clients

Endpoint schemas, authentication requirements and response codes are defined in
[Swagger](../../docs/swagger.yaml).

Generate an installation UUID and a cryptographically random device secret once per
installation. Retain both across FCM token refreshes and account switches. Register
again after login, session renewal or token refresh; unregister before logout.
Browser clients require a secure context, an active service worker, Firebase web
configuration and a public VAPID key. Request browser permission through an explicit
user action.

Apply account and chat preferences to foreground alerts as well. Muting suppresses
notifications, not realtime messages or unread counts; disabling account notifications
preserves per-chat preferences. Chat list/detail responses include mute status.
See the [push worker guide](../push_worker/README.md#delivery-semantics) for delivery
limits and preview privacy.
