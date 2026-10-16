CREATE TABLE IF NOT EXISTS telemetry_events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id     TEXT             NOT NULL,
    device_pk    UUID             NOT NULL REFERENCES devices(id),
    metric       TEXT             NOT NULL,
    value        DOUBLE PRECISION NOT NULL,
    recorded_at  TIMESTAMPTZ      NOT NULL,
    payload_hash TEXT             NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);

-- Constraints have no IF NOT EXISTS, so they are guarded. See README.md.
-- This UNIQUE constraint is the idempotency guarantee: a repeated event_id
-- is rejected by the database regardless of how many requests race to insert
-- it. payload_hash exists because the constraint alone cannot distinguish an
-- identical resubmission from a conflicting one with the same event_id - that
-- comparison happens in internal/telemetry once the row already exists.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'telemetry_events_event_id_key') THEN
        ALTER TABLE telemetry_events ADD CONSTRAINT telemetry_events_event_id_key UNIQUE (event_id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'telemetry_events_metric_check') THEN
        ALTER TABLE telemetry_events ADD CONSTRAINT telemetry_events_metric_check
            CHECK (metric IN ('temperature', 'voltage', 'battery_level', 'power_consumption'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS telemetry_events_device_pk_recorded_at_idx
    ON telemetry_events (device_pk, recorded_at DESC);
