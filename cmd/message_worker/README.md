# Message worker

[Project overview](../../README.md) Â| [Configuration reference](../../docs/configuration.md)

Claims durable message outbox rows, projects unread/chat state, builds realtime events, publishes to Redis Streams and optionally enqueues per-device push deliveries in the projection transaction.

## Dependencies and startup

PostgreSQL, Redis and S3-compatible storage for attachment URLs.

Apply the database schema through the API before starting this worker. Match `MESSAGE_EVENT_STREAM` with the gateway. Enable `PUSH_ENABLED` here to create push deliveries; this process does not call Firebase.

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
| `MESSAGE_WORKER_BATCH_SIZE` | `100` | Maximum claimed outbox rows per batch. |
| `MESSAGE_WORKER_CONCURRENCY` | `5` | Maximum concurrent chat processing groups. |
| `MESSAGE_WORKER_LEASE_DURATION` | `5m` | Outbox claim lease. |
| `MESSAGE_WORKER_POLL_INTERVAL` | `1s` | Outbox polling interval. |
| `MESSAGE_WORKER_BACKLOG_INTERVAL` | `5s` | Backlog measurement interval. |
| `MESSAGE_WORKER_MAX_ATTEMPTS` | `10` | Retry attempt limit. |
| `MESSAGE_WORKER_RETRY_BASE_DELAY / MESSAGE_WORKER_RETRY_MAX_DELAY` | 1s / 64s | Retry backoff bounds. |
| `MESSAGE_EVENT_STREAM` | `events:messages` | Match the gateway stream. |
| `MESSAGE_EVENT_STREAM_MAX_LEN` | `10000` | Approximate stream trimming length. |
| `MESSAGE_OUTBOX_RETENTION_DAYS` | `7` | Publisher deduplication TTL in days; scheduler uses it for cleanup. |
| `PUSH_ENABLED` | `false` | Enqueue per-device push deliveries during projection. |
| `APP_ENV` | `development` | Runtime environment label. |
| `OTEL_ENABLED` | `false` | Enable traces and metrics. |
| `OTEL_*` | See shared reference | Exporter, sampling, version and service identity settings. |

## Run

From the repository root, after backing services and schema are ready:

```sh
go run ./cmd/message_worker
```

Build a standalone binary:

```sh
go build -o bin/message_worker ./cmd/message_worker
```

Docker build and standalone run:

```sh
docker build -f build/package/message_worker/Dockerfile -t atoitalk-message-worker .
docker run --env-file .env atoitalk-message-worker
```

For standalone containers, `.env` must contain container-reachable dependency addresses rather than native `localhost` values. The [root quickstart](../../README.md#complete-local-stack) provides a Compose alternative with service DNS wiring.

## Health and operation

No HTTP health endpoint. Monitor process lifecycle, outbox backlog, failed/dead-letter rows and worker telemetry.
