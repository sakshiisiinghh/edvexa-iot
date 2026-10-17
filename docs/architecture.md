# Architecture

## Components

```
cmd/api/main.go        wiring only: config, logger, signal handling, graceful shutdown
internal/config        environment -> Config, with defaults and validation
internal/db            pgxpool construction and startup ping
internal/device        device CRUD, status lifecycle
internal/telemetry     idempotent ingestion, read-back of recent readings
internal/alert         threshold configuration, violation evaluation, alert lifecycle
internal/realtime      WebSocket hub: Publish/Subscribe, the /v1/ws handler
internal/httpapi       chi router, middleware, HTTP handlers for all of the above
internal/testdb        per-package isolated test databases for integration tests
web/                   Vite + React + TypeScript dashboard
migrations/            plain SQL, applied by `make migrate` or self-provisioned by testdb
```

`internal/device`, `internal/telemetry`, and `internal/alert` know nothing
about HTTP or about each other, with one exception described below
(telemetry depends on an interface alert happens to satisfy). `internal/httpapi`
is the only package that imports all three - it is the orchestration layer,
not the business logic.

## Request flow: ingesting a reading

```
POST /v1/telemetry
  -> httpapi.telemetryHandler.ingest
       decode + validate (metric whitelist, RFC3339 recorded_at)
       device.Repository.GetByDeviceID   -- 404 if unknown, 422 if inactive
       telemetry.Repository.Insert       -- see below
       Hub.Publish (only if Insert reports a genuine create)
       write the HTTP response
```

`Insert` runs entirely inside one `pgx` transaction:

1. `INSERT INTO telemetry_events (...) ON CONFLICT (event_id) DO NOTHING
   RETURNING id`.
2. If a row came back, the write is new. If a threshold evaluator is wired
   in, it runs **inside this same transaction** (see below), then the
   transaction commits.
3. If no row came back (`pgx.ErrNoRows`), something with this `event_id`
   already exists. A **separate** `SELECT payload_hash ... WHERE event_id =
   $1` - not a CTE folded into the insert - decides duplicate vs conflict by
   comparing hashes, then commits.

The handler only publishes a WebSocket message, and only computes whether to
also publish an alert-created message, after `Insert` has already returned
successfully - which under `pgx` means the transaction already committed.
There is no path where a message is published for a write that later rolled
back, because publishing never happens before the commit it depends on.

## Why the database is the guarantee, not application code

Two correctness properties are asked for by the assignment: idempotency
under duplicate submissions, and correctness under concurrent ingestion.
Neither is implemented as a check in Go before a write; both are properties
of a `UNIQUE` constraint plus `ON CONFLICT`.

**Why not "check if it exists, then insert"?** That is the standard
race: two concurrent requests for the same `event_id` can both run the
"does it exist?" check before either has inserted, both see "no", and both
insert. No amount of careful Go code closes that window - only the database
can, because only the database serialises concurrent writers to the same
row. `UNIQUE (event_id)` is what actually enforces "at most once": the
*losing* concurrent request's `INSERT` is rejected by Postgres itself, not
by a race-prone pre-check.

**Why `ON CONFLICT DO NOTHING` plus a separate `SELECT`, not a plain
`INSERT` that errors?** Because requirement 3 needs three outcomes, not two:
an identical resubmission must succeed quietly, but a resubmission with a
*different* payload under the same `event_id` must be rejected. A unique
violation alone cannot tell those apart - it only knows "a row with this key
exists," not "does it match." `payload_hash` is what makes the distinction
possible, computed over the fields the client actually controls (`device_id`,
`metric`, `value`, `recorded_at`), explicitly excluding `event_id` (the
lookup key, not part of what is being compared) and the internal device
primary key (a resolution detail, not something submitted).

**Why a separate `SELECT` statement rather than one combined query?** Under
`READ COMMITTED` (Postgres's default, unchanged here), each *statement*
within a transaction takes its own snapshot - but a single statement, such
as a CTE that both inserts and selects in one go, still only sees the
snapshot as of when that one statement began. If transaction A's insert
loses the `ON CONFLICT` race to transaction B, A's own combined
insert-and-select would still be looking at the pre-B snapshot and find
nothing. Running the lookup as its own, later statement is what lets it see
the row B just committed.

## Thresholds and alerts: the same pattern, with a sharper edge

Alert creation shares the ingest transaction via a small interface:

```go
// internal/telemetry/repository.go
type AlertEvaluator interface {
    Evaluate(ctx context.Context, tx pgx.Tx, devicePK, metric string, value float64) (alertID int64, created bool, err error)
}
```

`internal/alert.Repository` implements this method set but **imports
nothing from `internal/telemetry`**, and `internal/telemetry` imports
nothing from `internal/alert` either - the interface is satisfied
structurally. `cmd/api/main.go` is the only place that knows both exist and
wires them together with `telemetryRepo.UseAlertEvaluator(alertRepo)`. This
keeps the dependency one-directional without either package needing to
depend on the other's types.

Sharing the transaction is deliberate, not incidental: it is what makes two
things true by construction rather than by a check someone has to remember
to write:

- **A duplicate reading cannot raise a second alert**, because `Evaluate`
  is only ever called on the `OutcomeCreated` branch of `Insert` - a
  duplicate or conflicting submission never reaches it.
- **An alert cannot outlive a reading that gets rolled back**, because they
  commit or roll back together.

The sharper edge is what happens when two readings violate the same
threshold at the same time. `alerts` has a **partial** unique index:

```sql
CREATE UNIQUE INDEX alerts_one_unresolved_per_device_metric
    ON alerts (device_pk, metric)
    WHERE status IN ('active', 'acknowledged');
```

Partial, and spanning both `active` and `acknowledged`, because
acknowledging a problem is not fixing it - a second alert for a problem
someone has only acknowledged would be noise. An earlier version of this
design inserted the alert with a plain `INSERT` and let a unique violation
propagate as an error. That is a real defect, caught in plan review before
it shipped: a unique violation aborts the **whole transaction**, which would
have rolled back the telemetry reading along with the alert that lost the
race - one of two concurrent, perfectly valid readings would simply
disappear. The fix is the same idiom used for telemetry idempotency:
`INSERT ... ON CONFLICT (device_pk, metric) WHERE status IN ('active',
'acknowledged') DO NOTHING RETURNING id`. The `WHERE` clause on
`ON CONFLICT` has to match the partial index's predicate exactly, or
Postgres cannot infer which index the conflict target even is. Losing the
race now returns `pgx.ErrNoRows` from the `RETURNING` clause - handled
identically to how `telemetry.Repository.Insert` already detects a lost
`event_id` race - and is treated as `created = false`, not an error. Both
readings persist either way; only one alert is ever created.
`TestConcurrentViolatingReadingsCreateExactlyOneAlert` is the regression
test for this specific defect.

Alert status transitions use the same database-as-arbiter idiom once more,
at smaller scale: `acknowledge` and `resolve` are each a single conditional
`UPDATE alerts SET status = ... WHERE id = $1 AND status = <required
current status>`. Zero rows affected means the transition was invalid; which
specific error to report (unknown alert vs. wrong status) is decided by a
follow-up existence check that runs only after the `UPDATE` has already
failed, so it never introduces a second race of its own.

## Real-time updates

`internal/realtime.Hub` is a subscriber registry behind a mutex:
`Subscribe` returns a receive channel and an unsubscribe function;
`Publish` fans a message out to every subscriber without blocking - a
subscriber whose 16-message buffer is already full is dropped and its
channel closed, rather than letting one slow dashboard apply backpressure to
an ingestion request. `GET /v1/ws` is `Hub.ServeWS`, which upgrades the
connection and streams `Subscribe`'s channel to the client as JSON frames
until the client disconnects, falls behind, or a write fails. Because a
WebSocket connection must be read from continuously for the library to
process control frames (ping, close) even when the server never expects
inbound data, `ServeWS` runs a small background goroutine whose only job is
to notice the client going away.

`httpapi.Deps.Hub` may be `nil`; every call site checks before publishing,
mirroring the same optional-collaborator pattern already used for
`AlertEvaluator`. This is what let every test written before this feature
existed keep passing unchanged - they simply never trigger a publish.

## Delivery choices and their costs

- **Migrations are plain SQL, applied by `make migrate` against the
  development database, or self-provisioned per Go package into an
  isolated throwaway database by `internal/testdb` for integration tests.**
  No migration library; every migration body must be idempotent
  (`CREATE TABLE IF NOT EXISTS`, guarded `DO` blocks for constraints, which
  have no `IF NOT EXISTS`). The cost is a convention enforced by review, not
  by tooling - the concrete need that would justify adopting a migration
  library later.
- **Each Go test package gets its own Postgres database**, derived from the
  caller's source directory and provisioned on first use by executing the
  real migration files directly. This was a real fix, not a design up
  front: a shared single test database let two packages' test processes
  (which `go test ./...` runs concurrently by default) truncate each
  other's tables mid-test, producing exactly the kind of intermittent
  failure a flaky-test report describes. Per-package isolation removed the
  shared state rather than serialising around it.
- **No authentication, no multi-tenancy, no rate limiting.** Not required
  by the assignment, and adding any of them would be speculative for a
  scaffold with one tenant.
