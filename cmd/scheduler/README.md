# Scheduler

[Project overview](../../README.md) Â| [Configuration reference](../../docs/configuration.md)

Runs entity/outbox retention, private-chat cleanup and orphan-media cleanup jobs.

## Dependencies and startup

PostgreSQL and S3-compatible storage. This entrypoint does not connect to Redis.

Apply the database schema before starting the scheduler. Cron uses UTC by default. Jobs have a 15-minute timeout and skip overlapping executions within one process. Run one scheduler replica unless external coordination prevents duplicate job execution.

## Environment

Set variables in the process environment or a repository-root `.env` file. The table lists defaults and required values; see [Configuration](../../docs/configuration.md) for networking and telemetry options.

| Variable | Default / requirement | Purpose |
|---|---|---|
| `DB_HOST / DB_PORT` | Required | PostgreSQL address; local native `localhost:5434`, Compose `postgres:5432`. |
| `DB_USER / DB_PASSWORD / DB_NAME / DB_SSLMODE` | Required | PostgreSQL credentials, database and SSL mode. |
| `DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS` | 80 / 40 | Connection pool per process. |
| `DB_CONN_MAX_LIFETIME / DB_CONN_MAX_IDLE_TIME` | 30m / 5m | Connection lifetime and idle timeout. |
| `S3_BUCKET_PUBLIC / S3_BUCKET_PRIVATE` | Required | Public and private bucket names. |
| `S3_REGION` | empty | S3 region. |
| `S3_ACCESS_KEY / S3_SECRET_KEY` | empty | Static S3 credentials. |
| `S3_ENDPOINT` | empty | S3 API endpoint; empty uses provider default. |
| `S3_PRESIGN_ENDPOINT` | empty | Browser-accessible endpoint for signed URLs. |
| `S3_PUBLIC_DOMAIN` | empty | Public bucket/CDN URL. |
| `STORAGE_DOWNLOAD_MAX_RETRIES / STORAGE_DOWNLOAD_RETRY_DELAY` | 2 / 250ms | Storage download retry policy. |
| `SOFT_DELETE_RETENTION_DAYS` | `30` | Days before hard deletion. |
| `MEDIA_RETENTION_DAYS` | `7.0` | Orphan-media retention in days; fractional values accepted. |
| `MESSAGE_OUTBOX_RETENTION_DAYS` | `7` | Published/dead-letter outbox retention in days. |
| `ENTITY_CLEANUP_CRON` | `0 2 * * *` | Entity/outbox cleanup schedule. |
| `PRIVATE_CHAT_CLEANUP_CRON` | `30 2 * * *` | Private-chat cleanup schedule. |
| `MEDIA_CLEANUP_CRON` | `0 3 * * *` | Media cleanup schedule. |
| `SCHEDULER_TIMEZONE` | `UTC` | Cron IANA timezone; invalid values fall back to UTC. |
| `APP_ENV` | `development` | Runtime environment label. |
| `OTEL_ENABLED` | `false` | Enable traces and metrics. |
| `OTEL_*` | See shared reference | Exporter, sampling, version and service identity settings. |

## Run

From the repository root, after backing services and schema are ready:

```sh
go run ./cmd/scheduler
```

Build a standalone binary:

```sh
go build -o bin/scheduler ./cmd/scheduler
```

Docker build and standalone run:

```sh
docker build -f build/package/scheduler/Dockerfile -t atoitalk-scheduler .
docker run --env-file .env atoitalk-scheduler
```

For standalone containers, `.env` must contain container-reachable dependency addresses rather than native `localhost` values. The [root quickstart](../../README.md#complete-local-stack) provides a Compose alternative with service DNS wiring.

## Health and operation

No HTTP health endpoint. Monitor job registration, completion/error logs and scheduler telemetry.
