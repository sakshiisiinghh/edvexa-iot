# edvexa-iot

IoT device monitoring service. This is the initial scaffold: an HTTP server with
liveness and readiness endpoints backed by PostgreSQL. Device telemetry,
alerting and the frontend are not built yet — see [Not built yet](#not-built-yet).

## Requirements

- Go 1.26.5 or newer (the version pinned in `go.mod`)
- Docker with Compose v2 (`docker compose`, not `docker-compose`)

## Run it locally

```sh
cp .env.example .env
make db-up                            # starts PostgreSQL, waits until healthy
make run                              # starts the API on :8080
curl -i http://localhost:8080/healthz
```

`make db-up` blocks until the container's health check passes, so the API will
not race the database on startup.

**If port 5432 is already in use** — a local PostgreSQL install or another
project — `make db-up` fails with `Bind for 0.0.0.0:5432 failed: port is
already allocated`. Set a free port in `.env` and match it in `DATABASE_URL`:

```sh
POSTGRES_PORT=5433
DATABASE_URL=postgres://edvexa:edvexa@localhost:5433/edvexa?sslmode=disable
```

## Endpoints

| Endpoint | Meaning | Codes |
| --- | --- | --- |
| `GET /healthz` | Liveness. Does not touch PostgreSQL, so a database blip never makes an orchestrator restart a healthy process. | always `200` |
| `GET /readyz` | Readiness. Pings PostgreSQL under a 2s deadline. | `200` ready, `503` unready |

```sh
$ curl -s http://localhost:8080/readyz
{"status":"ready"}

# with PostgreSQL stopped
$ curl -s http://localhost:8080/readyz
{"status":"unready","error":"..."}
```

## Configuration

Every variable has a working default except `DATABASE_URL`, which the service
refuses to start without. Invalid values are all reported in one error, so a
misconfigured deployment takes one restart to diagnose rather than several.

| Variable | Default | Purpose |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` selects JSON logs, anything else text |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `HTTP_PORT` | `8080` | listen port |
| `DATABASE_URL` | *required* | PostgreSQL connection string |
| `DB_MAX_CONNS` | `10` | connection pool ceiling |
| `DB_CONNECT_TIMEOUT` | `5s` | startup ping budget |
| `READ_TIMEOUT` | `10s` | `http.Server.ReadTimeout` |
| `WRITE_TIMEOUT` | `15s` | `http.Server.WriteTimeout` |
| `IDLE_TIMEOUT` | `60s` | `http.Server.IdleTimeout` |
| `SHUTDOWN_TIMEOUT` | `10s` | graceful drain budget |

Zero and negative values are rejected: `http.Server` treats a zero timeout as
*no* timeout, which would silently remove the protection.

`POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` and `POSTGRES_PORT` are read
by `docker-compose.yml` and default to `edvexa` / `edvexa` / `edvexa` / `5432`.

## Make targets

```
make help     List targets
make tidy     Reconcile go.mod and go.sum
make build    Compile the API to bin/api
make run      Run the API
make test     Run all tests
make vet      Report suspicious constructs
make db-up    Start PostgreSQL and block until it is healthy
make db-down  Stop and remove the container, keeping the data volume
make db-logs  Follow PostgreSQL logs
make clean    Remove build output
```

`make db-down` runs `docker compose down` without `-v`, so no target deletes the
`postgres_data` volume. To discard the data you have to pass `-v` yourself.

## Tests

```sh
make test
```

No test needs Docker or a database — run them with PostgreSQL stopped. They
cover the configuration defaults and each validation failure, that `/healthz`
stays `200` even when the pool is erroring, that `/readyz` returns `503` with
the ping error, that the ping is given a deadline, and that a panicking handler
becomes a `500` rather than killing the process.

### Testing strategy

Docker-free unit tests are a property of **this scaffold**, not a project-wide
policy. The readiness handler depends on a one-method `Pinger` interface, which
is enough to prove a handler calls its dependency — and nothing more.

Once telemetry ingestion exists, its **idempotency tests** (replaying a reading
must not duplicate a row, which depends on real unique-index and `ON CONFLICT`
behaviour) and its **concurrency tests** (simultaneous writers, isolation
levels, row locks, deadlock and retry) **must run against a real PostgreSQL
instance**. A fake has no transaction manager and cannot produce serialization
failures or deadlocks, so a mocked version of these tests would pass while the
production behaviour was broken.

Those will be separated behind a `//go:build integration` tag and a
`make test-integration` target that depends on `db-up`, keeping the fast suite
infrastructure-free while the slow suite is honest about needing a database.
Neither exists yet — there are no queries to exercise.

## Layout

```
cmd/api/           entrypoint: wiring, signal handling, graceful shutdown
internal/config/   environment -> Config
internal/db/       pgxpool construction and startup ping
internal/httpapi/  chi router, middleware, health endpoints
```

## Not built yet

Telemetry ingestion, alerting, WebSockets, authentication and the frontend.
There is also no schema or migrations — `/readyz` only pings. These are later
milestones, not oversights.
