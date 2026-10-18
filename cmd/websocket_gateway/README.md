# WebSocket gateway

[Project overview](../../README.md) Â| [Configuration reference](../../docs/configuration.md)

Authenticates socket connections and delivers realtime events, presence and typing updates.

## Dependencies and startup

PostgreSQL and Redis.

Apply the database schema before starting the gateway. Use the API JWT secret and frontend origins. Assign distinct instance IDs to live replicas; consumer groups append the instance ID to their prefix. Clients must resynchronize history after reconnecting because socket delivery is best-effort.

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
| `REALTIME_PORT` | `8081` | HTTP/WebSocket port. |
| `REALTIME_INSTANCE_ID` | empty | Unique per live gateway; empty resolves hostname or UUID. |
| `REALTIME_CONSUMER_GROUP` | empty | Group prefix; empty resolves realtime, instance ID appended. |
| `MESSAGE_EVENT_STREAM` | `events:messages` | Match the message-worker stream. |
| `JWT_SECRET` | empty | Match the API signing secret. |
| `APP_CORS_ALLOWED_ORIGINS` | `*` | Allowed frontend WebSocket origins. |
| `APP_ENV` | `development` | Runtime environment label. |
| `OTEL_ENABLED` | `false` | Enable traces and metrics. |
| `OTEL_*` | See shared reference | Exporter, sampling, version and service identity settings. |

## Run

From the repository root, after backing services and schema are ready:

```sh
go run ./cmd/websocket_gateway
```

Build a standalone binary:

```sh
go build -o bin/websocket_gateway ./cmd/websocket_gateway
```

Docker build and standalone run:

```sh
docker build -f build/package/websocket_gateway/Dockerfile -t atoitalk-websocket-gateway .
docker run --env-file .env -p 8081:8081 atoitalk-websocket-gateway
```

For standalone containers, `.env` must contain container-reachable dependency addresses rather than native `localhost` values. The [root quickstart](../../README.md#complete-local-stack) provides a Compose alternative with service DNS wiring.

## Health and operation

GET `/healthz` on port 8081 by default. This endpoint reports HTTP liveness, not a PostgreSQL/Redis readiness probe.
