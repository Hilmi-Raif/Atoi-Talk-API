# Shared configuration

[Project overview](../README.md)

Service-specific variables and defaults are documented in the [API](../cmd/api/README.md),
[message worker](../cmd/message_worker/README.md), [push worker](../cmd/push_worker/README.md),
[WebSocket gateway](../cmd/websocket_gateway/README.md), and [scheduler](../cmd/scheduler/README.md) guides.
Use [.env.example](../.env.example) for native processes and
[the local deployment template](../deployments/local/.env.example) for Compose.

## Environment loading

All entrypoints load `.env` from the current working directory. Existing process
variables take precedence; there is no automatic per-service env-file selection.
Run repository commands from the repository root.

Example values are not runtime defaults. Empty strings override string defaults.
Duration settings accept Go duration strings such as `250ms`, `5s`, or `5m`;
plain integers mean milliseconds. `JWT_EXP`, `OTP_EXP`, and
`OTP_RATE_LIMIT_SECONDS` use seconds; retention settings ending in `_DAYS` use days.

The shared loader requires non-empty `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`,
`DB_NAME`, `DB_SSLMODE`, `S3_BUCKET_PUBLIC`, and `S3_BUCKET_PRIVATE` for every
process, even when a service does not use object storage or push is disabled.
Only the API applies migrations when `DB_MIGRATE=true`; deploy schema changes
before starting workers. Database connection pool limits apply per process/replica.

## Native versus Compose

| Dependency | Native process using local backing services | Inside local Compose |
|---|---|---|
| PostgreSQL | `localhost:5434` | `postgres:5432` |
| Redis | `localhost:6379` | `redis:6379` |
| S3 operations | `http://localhost:9090` | `http://s3mock:9090` |
| SMTP | `localhost:1025` | `mailpit:1025` |

Compose explicitly maps variables; it does not forward every env-file key.
Its `DB_PORT` controls the published host port, while application containers use
`5432`. Adjust native addresses when published ports change. Browser-facing
`S3_PRESIGN_ENDPOINT` and `S3_PUBLIC_DOMAIN` must be reachable by the browser,
not use container-only DNS names.

Inspect the resolved setup with:

```sh
docker compose --env-file .env -f deployments/local/docker-compose.yaml config
```

The output may contain secrets. Service-specific Compose pool overrides and host
port mappings are listed in the local deployment template.

## Cross-service consistency

- API and gateway must use the same `JWT_SECRET` and appropriate frontend CORS origins.
- API, message worker, push worker, and gateway must use the same application Redis DB.
- `MESSAGE_EVENT_STREAM` must match between message worker and gateway.
- Enable `PUSH_ENABLED` on API, message worker, and push worker for push delivery.
- `MESSAGE_OUTBOX_RETENTION_DAYS` governs scheduler cleanup and publisher deduplication TTL.
- Stored application timestamps use UTC; event/session-revocation timestamps use Unix
  milliseconds. API dates use RFC3339. `SCHEDULER_TIMEZONE` changes cron execution time,
  not stored timestamps.

## Telemetry

These settings apply to all five services. Non-API entrypoints append their service
suffix to `OTEL_SERVICE_NAME`.

| Variable | Default | Purpose |
|---|---|---|
| `OTEL_ENABLED` | `false` | Enable trace and metric export. |
| `OTEL_SERVICE_NAME` | `atoitalk-api` | Base service identity. |
| `OTEL_SERVICE_VERSION` | `0.1.1` | Deployed service version. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC collector address, reachable from the process. |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Use plaintext gRPC rather than TLS. |
| `OTEL_SAMPLING_RATIO` | `0.1` | Trace sampling fraction from 0 to 1. |
| `OTEL_METRIC_EXPORT_INTERVAL` | `5s` | Metric export interval. |

## Credentials and Compose-only settings

Private credentials stay outside the repository and frontend. For Firebase, use
Application Default Credentials: workload identity or `GOOGLE_APPLICATION_CREDENTIALS`
pointing to a file accessible to the process. The push worker can use ADC project
metadata when `FCM_PROJECT_ID` is unset.

For local Compose, `FCM_CREDENTIALS_FILE` is a host path mounted read-only at
`/run/secrets/fcm.json`. The container's `GOOGLE_APPLICATION_CREDENTIALS` points to
that mounted path. Host and container paths are separate filesystem namespaces.
Firebase web configuration and public VAPID keys belong to the client.

`S3_FORCE_PATH_STYLE` and `SMTP_REQUIRE_TLS` are not supported application settings.
The S3 client enables path-style addressing when `S3_ENDPOINT` is set.

## Integration tests

Copy [the integration template](../deployments/integration/.env.example) to
`deployments/integration/.env.test`. Use a dedicated database and Redis DB;
integration tests clear their data. Adjust dependency addresses to the ports
published by the integration Compose stack.
