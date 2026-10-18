# Push worker

[Project overview](../../README.md) Â| [Configuration reference](../../docs/configuration.md)

Claims durable per-device PostgreSQL deliveries, rechecks eligibility/session/preferences, and sends notifications through FCM HTTP v1 independently of realtime delivery.

## Dependencies and startup

PostgreSQL, Redis for session revocation, and Firebase Application Default Credentials (ADC).

Apply the database schema before starting this worker. Enable `PUSH_ENABLED` on the API, message worker and push worker; otherwise this worker exits without processing deliveries. Delivery is at-least-once, so clients should deduplicate by `event_id`. See [delivery semantics](#delivery-semantics) for expiry and retry behavior.

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
| `S3_BUCKET_PUBLIC / S3_BUCKET_PRIVATE` | Required | Required at startup; no S3 connection is needed. |
| `PUSH_ENABLED` | `false` | Must be true to run delivery; enable on API and message worker too. |
| `FCM_PROJECT_ID` | empty | Firebase project; ADC project metadata is a fallback. |
| `GOOGLE_APPLICATION_CREDENTIALS` | empty | ADC credential file in the runtime filesystem; optional with workload identity. |
| `APP_ENV` | `development` | Runtime environment label. |
| `OTEL_ENABLED` | `false` | Enable traces and metrics. |
| `OTEL_*` | See shared reference | Exporter, sampling, version and service identity settings. |

## Run

From the repository root, after backing services and schema are ready:

```sh
go run ./cmd/push_worker
```

Build a standalone binary:

```sh
go build -o bin/push_worker ./cmd/push_worker
```

Docker build and standalone run:

```sh
docker build -f build/package/push_worker/Dockerfile -t atoitalk-push-worker .
docker run --env-file .env -v /absolute/path/service-account.json:/run/secrets/fcm.json:ro -e GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/fcm.json atoitalk-push-worker
```

For standalone containers, `.env` must contain container-reachable dependency addresses rather than native `localhost` values. The [root quickstart](../../README.md#complete-local-stack) provides a Compose alternative with service DNS wiring.

## Health and operation

No HTTP health endpoint. Monitor process startup, pending/expired deliveries, outcome fields and worker errors. FCM acceptance is not proof of device display.

For the local Compose profile, configure `PUSH_ENABLED=true`, `FCM_PROJECT_ID` and `FCM_CREDENTIALS_FILE` in the Compose env file, then run:

```sh
docker compose --env-file .env -f deployments/local/docker-compose.yaml --profile push up -d --build
```

Keep private credentials outside Git. See the [API guide](../api/README.md#notification-clients) for client lifecycle and [Swagger](../../docs/swagger.yaml) for endpoint contracts.

## Delivery semantics

Message projection atomically enqueues per-device PostgreSQL deliveries. Eligibility
and preferences are checked at enqueue and before sending, including membership,
blocks, hidden history, message deletion, device binding and session revocation.
Payloads include sender/group and message previews plus event/chat/message IDs;
previews may appear on the device lock screen.

Delivery is at-least-once: deduplicate using `event_id`. A crash after FCM acceptance
can produce duplicates, and acceptance does not prove device display. Deliveries
expire one hour after message creation, retry transient failures up to ten attempts,
honor `Retry-After`, and disable tokens reported `UNREGISTERED`.

Skipped notifications are not replayed after unmute; notifications already accepted
by FCM cannot be recalled. Finished rows remain available for inspection; configure
a retention policy appropriate to the deployment.
