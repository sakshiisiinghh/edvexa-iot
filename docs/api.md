# API reference

Base URL: `http://localhost:8080` (local). All request and response bodies
are JSON. Every response in this document is the actual shape the handlers
produce, read from the code in `internal/httpapi/`, `internal/device/`,
`internal/telemetry/`, and `internal/alert/` - not inferred from the plan.

Unknown JSON fields in a request body are rejected (`DisallowUnknownFields`),
and request bodies are capped at 1 MiB.

## Health

### `GET /healthz`

Liveness. Never touches the database - a Postgres blip must not make an
orchestrator restart a healthy process.

```
200 OK
{"status":"ok"}
```

### `GET /readyz`

Readiness. Pings the database under a 2s deadline.

```
200 OK
{"status":"ready"}
```
```
503 Service Unavailable
{"status":"unready","error":"<ping error>"}
```

## Devices

A device has a client-supplied `device_id` (external, used in every other
endpoint) and an internal UUID (never exposed in JSON).

| Field | Type | Notes |
|---|---|---|
| `device_id` | string | required, ≤128 chars, non-blank after trimming |
| `name` | string | required, ≤200 chars |
| `location` | string | optional, ≤200 chars, defaults to `""` |
| `status` | string | `"active"` or `"inactive"` |
| `created_at`, `updated_at` | RFC3339 timestamp | |

### `POST /v1/devices`

Request:
```json
{"device_id": "dev-001", "name": "Freezer A", "location": "Lab 1"}
```

| Outcome | Status | Body |
|---|---|---|
| created | `201` | the device |
| missing/too-long field | `422` | `{"error":"invalid device: <reason>"}` |
| `device_id` already registered | `409` | `{"error":"device_id already registered"}` |

The duplicate check is the database's `UNIQUE(device_id)` constraint, not a
preceding `SELECT` - two concurrent creates with the same `device_id` cannot
both succeed.

### `GET /v1/devices`

```
200 OK
{"devices": [ {device}, ... ]}
```

### `GET /v1/devices/{deviceID}`

```
200 OK
{device}
```
```
404 Not Found
{"error":"device not found"}
```

### `PATCH /v1/devices/{deviceID}`

Request (both fields optional, but at least one required):
```json
{"name": "Freezer A2", "location": "Lab 2"}
```

| Outcome | Status |
|---|---|
| updated | `200`, the updated device |
| neither field given | `422` |
| unknown `deviceID` | `404` |

Fields omitted from the body are left unchanged (`COALESCE` in the `UPDATE`,
not a read-modify-write in application code).

### `POST /v1/devices/{deviceID}/deactivate`

Sets `status` to `inactive`. Idempotent: deactivating an already-inactive
device succeeds again rather than erroring.

| Outcome | Status |
|---|---|
| deactivated (or already was) | `200`, the device |
| unknown `deviceID` | `404` |

A deactivated device's telemetry is rejected (see below) but it remains
readable via `GET` - deactivation is not deletion.

## Telemetry

### `POST /v1/telemetry`

Request:
```json
{
  "event_id": "11111111-1111-1111-1111-111111111111",
  "device_id": "dev-001",
  "metric": "temperature",
  "value": 4.5,
  "recorded_at": "2026-10-15T10:00:00Z"
}
```

`metric` must be one of `temperature`, `voltage`, `battery_level`,
`power_consumption`. `recorded_at` must be RFC3339 - this is enforced by
decoding directly into a Go `time.Time`, not by separate validation code.

| Outcome | Condition | Status | Body |
|---|---|---|---|
| created | first time this `event_id` is seen | `201` | the stored event |
| duplicate | same `event_id`, identical payload | `200` | the stored event (the original, not a re-save) |
| conflict | same `event_id`, **different** payload | `409` | `{"error":"event_id <id> was already used with a different payload"}` |
| invalid | missing field, bad metric, or missing `recorded_at` | `422` | `{"error":"invalid telemetry event: <reason>"}` |
| unknown device | `device_id` not registered | `404` | `{"error":"device not found"}` |
| inactive device | device exists but `status != active` | `422` | `{"error":"device <id> is not active (status: inactive)"}` |

Response body (all three success outcomes share this shape):
```json
{"event_id":"...","device_id":"...","metric":"...","value":4.5,"recorded_at":"..."}
```

"Duplicate" vs "conflict" is decided by comparing a SHA-256 hash of the
semantic payload (`device_id`, `metric`, `value`, `recorded_at`, normalised
to UTC) against what is already stored under that `event_id` - the
`UNIQUE(event_id)` constraint alone cannot distinguish a harmless replay from
a different payload reusing the same id.

Only the `created` outcome evaluates thresholds and can raise an alert (see
below) or publish a WebSocket event - a duplicate or conflicting submission
never does either.

### `GET /v1/devices/{deviceID}/telemetry?limit=N`

Most recent readings for one device, newest first.

| Query param | Default | Max |
|---|---|---|
| `limit` | 50 | 500 (silently capped, not rejected) |

```
200 OK
{"readings": [{"event_id":"...","metric":"...","value":4.5,"recorded_at":"..."}, ...]}
```

| Error | Status |
|---|---|
| `limit` not a positive integer | `400` |
| unknown `deviceID` | `404` |

## Thresholds

A threshold needs at least one bound; when both are given, `min_value` must
be strictly less than `max_value`.

| Field | Type | Notes |
|---|---|---|
| `device_id` | string | required |
| `metric` | string | required, same four-value whitelist as telemetry |
| `min_value` | number \| null | optional |
| `max_value` | number \| null | optional |

### `POST /v1/thresholds`

Request:
```json
{"device_id": "dev-001", "metric": "temperature", "max_value": 8.0}
```

This is an **upsert**: posting again for the same `device_id` + `metric`
replaces the bounds rather than erroring.

| Outcome | Status |
|---|---|
| created or replaced | `200`, the stored threshold |
| missing `device_id`/`metric`, bad metric, no bound given, or `min_value >= max_value` | `422` |
| unknown device | `404` |

### `GET /v1/thresholds`

```
200 OK
{"thresholds": [{"device_id":"...","metric":"...","min_value":null,"max_value":8.0}, ...]}
```

`min_value`/`max_value` are omitted from the JSON (not `null`) when unset,
via `omitempty`.

## Alerts

| Field | Type | Notes |
|---|---|---|
| `id` | number | |
| `device_id` | string | |
| `metric` | string | |
| `triggering_value` | number | the reading that caused it |
| `status` | string | `active` → `acknowledged` → `resolved` |
| `triggered_at` | RFC3339 | |
| `acknowledged_at`, `resolved_at` | RFC3339 \| omitted | present only once that transition has happened |

An alert is created automatically by a telemetry submission that violates a
configured threshold - there is no `POST /v1/alerts`. At most one
**unresolved** alert (`active` or `acknowledged`) exists per device and
metric at a time; a repeat violation while one is already unresolved stores
the reading but raises no second alert.

### `GET /v1/alerts?status=`

| Query param | Behaviour |
|---|---|
| *(omitted)* | every alert, any status |
| `status=active` \| `acknowledged` \| `resolved` | filtered to that status |

```
200 OK
{"alerts": [{alert}, ...]}
```
Ordered newest-first by `triggered_at`.

### `POST /v1/alerts/{id}/acknowledge`

Valid only from `active`.

| Outcome | Status |
|---|---|
| acknowledged | `200`, the updated alert |
| `{id}` is not an integer | `400` |
| no alert with that id | `404` |
| alert exists but is not `active` (already acknowledged, or resolved) | `409` |

### `POST /v1/alerts/{id}/resolve`

Valid from `active` **or** `acknowledged` - resolving does not require
acknowledging first.

| Outcome | Status |
|---|---|
| resolved | `200`, the updated alert |
| `{id}` is not an integer | `400` |
| no alert with that id | `404` |
| alert is already `resolved` | `409` |

Both transitions are a single conditional `UPDATE ... WHERE id = $1 AND
status = $2`; the database decides success or failure, not a preceding read.

## Real-time updates

### `GET /v1/ws`

Upgrades to a WebSocket. The server only ever sends; it does not expect the
client to send data frames. Every message:

```json
{"type": "telemetry" | "alert", "data": { ... }}
```

| `type` | `data` shape | Published when |
|---|---|---|
| `telemetry` | the same shape as a `POST /v1/telemetry` response | a reading is newly created (never for a duplicate or conflict) |
| `alert` (creation) | `{"id","device_id","metric","triggering_value","status":"active"}` | a new alert is raised by the reading that just published |
| `alert` (lifecycle) | the full alert object (see above) | `acknowledge` or `resolve` succeeds |

Every publish happens **after** the underlying database transaction has
already committed - a request whose write is rolled back, rejected, or
recognised as a duplicate never publishes anything. A subscriber that falls
behind (16 unread messages) is disconnected rather than allowed to slow down
ingestion.

## Status code summary

| Code | Meaning in this API |
|---|---|
| `200` | read succeeded; or a write succeeded without creating a new resource (duplicate telemetry, threshold upsert, alert transition) |
| `201` | a new resource was created (device, telemetry event) |
| `400` | malformed request the handler cannot parse (bad JSON, non-integer path id, bad `limit`) |
| `404` | the referenced resource (device or alert) does not exist |
| `409` | the request is well-formed but conflicts with existing state (duplicate `device_id`, conflicting telemetry payload, invalid alert transition) |
| `422` | the request body failed field-level validation (bad metric, missing bound, inactive device) |
| `500` | unexpected internal error; logged server-side, never detailed to the client |
| `503` | `/readyz` only: the database is unreachable |
