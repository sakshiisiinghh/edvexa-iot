# edvexa-iot

A backend-driven IoT device monitoring platform: device management,
idempotent telemetry ingestion under concurrency, threshold-based alerting,
real-time updates over WebSockets, and a minimal dashboard. Go, chi,
PostgreSQL, Docker Compose, and a Vite + React + TypeScript frontend.

Full endpoint reference: [`docs/api.md`](docs/api.md).
Design rationale - why the database, not application code, is the
correctness guarantee: [`docs/architecture.md`](docs/architecture.md).

## Requirements

- Go 1.26.5 or newer (the version pinned in `go.mod`)
- Docker with Compose v2 (`docker compose`, not `docker-compose`)
- Node.js 18+ and npm, for the dashboard

## Run it locally

```sh
cp .env.example .env
make db-up       # starts PostgreSQL, waits until healthy
make migrate     # applies migrations/*.sql; re-running is always a no-op
make run         # starts the API on :8080
curl -i http://localhost:8080/healthz
```

In a second terminal, the dashboard:

```sh
cd web
npm install
npm run dev      # http://localhost:5173, proxies /v1 (including the WebSocket) to :8080
```

**If port 5432 is already in use** - a local PostgreSQL install or another
project - `make db-up` fails with `Bind for 0.0.0.0:5432 failed: port is
already allocated`. Set a free port in `.env` and match it in
`DATABASE_URL`:

```sh
POSTGRES_PORT=5433
DATABASE_URL=postgres://edvexa:edvexa@localhost:5433/edvexa?sslmode=disable
```

The same applies to `HTTP_PORT` if `8080` is already taken - change it in
`.env` and, if you also want the dashboard's dev proxy to follow, update the
target in `web/vite.config.ts` to match.

## Local demo

With the API and dashboard both running (above), register a device, set a
threshold that will be violated, and submit a reading above it:

```sh
curl -s -X POST localhost:8080/v1/devices -H 'Content-Type: application/json' \
  -d '{"device_id":"freezer-1","name":"Freezer A","location":"Lab 1"}'

curl -s -X POST localhost:8080/v1/thresholds -H 'Content-Type: application/json' \
  -d '{"device_id":"freezer-1","metric":"temperature","max_value":8.0}'

curl -s -X POST localhost:8080/v1/telemetry -H 'Content-Type: application/json' \
  -d '{"event_id":"demo-1","device_id":"freezer-1","metric":"temperature","value":15.0,"recorded_at":"2026-01-01T00:00:00Z"}'
```

Open `http://localhost:5173` before submitting telemetry - the reading and
the alert it raises appear live, pushed over the WebSocket connection the
dashboard opens on load. The device list is fetched when the page loads, so
a device registered after that shows up on the next refresh. Acknowledge and
resolve the alert from the dashboard's buttons, or via `curl` against
`/v1/alerts/{id}/acknowledge` and `/v1/alerts/{id}/resolve`.

Readings streaming in, with the 11.2 reading crossing the 8.0 threshold and
raising an alert:

![Dashboard with live telemetry and a new active alert](docs/screenshots/dashboard-live.png)

The same alert after clicking Acknowledge - only Resolve remains:

![Dashboard after acknowledging the alert](docs/screenshots/dashboard-acknowledged.png)

## Configuration

Every variable has a working default except `DATABASE_URL`, which the
service refuses to start without. Invalid values are all reported in one
error, so a misconfigured deployment takes one restart to diagnose rather
than several.

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

Zero and negative values are rejected: `http.Server` treats a zero timeout
as *no* timeout, which would silently remove the protection.

`POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` and `POSTGRES_PORT` are
read by `docker-compose.yml` and default to `edvexa` / `edvexa` / `edvexa` /
`5432`.

## Migrations

Plain SQL files in `migrations/`, applied in order by `make migrate`,
tracked in a `schema_migrations` table. Every migration body is idempotent
(`CREATE TABLE IF NOT EXISTS`, guarded `DO` blocks for constraints), so
**running `make migrate` again is always a no-op** - it prints `skip` for
every already-applied file rather than erroring or reapplying anything.

```sh
make migrate          # apply pending migrations
make migrate-status   # show what has been applied, and what is pending
```

There are no down migrations; rolling back means writing a new forward
migration. See `migrations/README.md` for the idempotency convention every
new migration file must follow, and `docs/architecture.md` for why this is
plain SQL rather than a migration library.

## Make targets

```
make help            List targets
make tidy             Reconcile go.mod and go.sum
make build            Compile the API to bin/api
make run              Run the API, loading .env if present
make test             Run all unit tests (no database required)
make vet              Report suspicious constructs
make migrate          Apply pending migrations
make migrate-status   Show applied and pending migrations
make test-integration Run integration tests against a real PostgreSQL
make db-up            Start PostgreSQL and block until it is healthy
make db-down          Stop and remove the container, keeping the data volume
make db-logs          Follow PostgreSQL logs
make clean            Remove build output
```

`make db-down` runs `docker compose down` **without** `-v`, so no target in
this project deletes the `postgres_data` volume. To discard the data you
have to pass `-v` to `docker compose down` yourself.

## Tests

Two suites, kept deliberately separate.

```sh
make test              # unit tests - no Docker, no database, runs anywhere
make db-up              # (if not already running)
make test-integration   # integration tests - needs real PostgreSQL
```

### Unit tests (`make test`)

Configuration validation, the health endpoints, and the WebSocket hub's
own behaviour (fan-out to multiple subscribers, a disconnecting client
being cleanly unregistered with no goroutine left behind, a full
subscriber buffer being dropped without blocking the publisher). None of
this touches a database - these pass with Docker stopped.

### Integration tests (`make test-integration`)

Everything that depends on real PostgreSQL transaction semantics: device
CRUD and the `UNIQUE(device_id)` constraint; telemetry idempotency and
conflict detection, including 50-way concurrent submissions under `-race`;
threshold violations raising alerts, including the regression test for a
defect caught in review where two concurrent violations could have lost a
telemetry reading; the full alert lifecycle and its invalid-transition
rejections; and that every WebSocket publish happens only after its
transaction has actually committed - a duplicate submission, a conflicting
submission, an inactive-device rejection, and a failed alert transition
are each asserted to publish nothing.

Each Go package's integration tests run against their **own**, isolated
PostgreSQL database - derived automatically from the package and
provisioned on first use by applying the real migration files directly.
This exists because of a real bug: `go test ./...` runs different
packages as concurrent processes by default, and an earlier design where
every package shared one database let one package's test truncate
another's tables mid-run. See `docs/architecture.md` for the full story.

```sh
go test -tags=integration -race -count=1 ./...
```

is the command that actually proves the above; `make test-integration`
runs the identical thing after making sure `.env` is loaded.

## The dashboard

`web/` is a small Vite + React + TypeScript app: a device table, a
telemetry panel that fills live from the WebSocket feed (nothing is
backfilled on page load - there is no "all recent telemetry across every
device" REST endpoint, only a per-device one), and an active-alerts list
with Acknowledge and Resolve buttons. One WebSocket subscription drives
all three panels. No state-management library, no component framework, no
styling beyond one plain CSS file.

```sh
cd web
npm install
npm run build   # type-checks with tsc, then builds to web/dist/
npm run dev     # dev server on :5173, proxying /v1 (REST and the WebSocket) to :8080
```

## Layout

```
cmd/api/              entrypoint: wiring, signal handling, graceful shutdown
internal/config/      environment -> Config
internal/db/          pgxpool construction and startup ping
internal/device/      device CRUD and status lifecycle
internal/telemetry/   idempotent ingestion, recent-readings read-back
internal/alert/       threshold configuration, violation evaluation, alert lifecycle
internal/realtime/    WebSocket hub (Publish/Subscribe) and the /v1/ws handler
internal/httpapi/     chi router, middleware, HTTP handlers
internal/testdb/      per-package isolated test databases for integration tests
migrations/           plain SQL schema, applied by make migrate
web/                  the dashboard
docs/                 API reference and architecture notes
```

## Known limitations

- **No authentication, no multi-tenancy, no rate limiting.** Not required
  by the assignment; adding any of them would be speculative for a
  single-tenant scaffold.
- **`npm install` currently reports 2 vulnerabilities (1 moderate, 1
  high)** in the dashboard's dependency tree, surfaced by `npm audit`.
  Not remediated here - `npm audit fix --force` pulls in breaking changes
  that were not evaluated as part of this work.
- **No CI configuration.** Both test suites and the build are documented
  above and are what a pipeline would run.
