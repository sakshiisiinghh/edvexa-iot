CREATE TABLE IF NOT EXISTS alert_thresholds (
    id         UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    device_pk  UUID             NOT NULL REFERENCES devices(id),
    metric     TEXT             NOT NULL,
    min_value  DOUBLE PRECISION,
    max_value  DOUBLE PRECISION,
    created_at TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alerts (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    device_pk        UUID             NOT NULL REFERENCES devices(id),
    metric           TEXT             NOT NULL,
    threshold_id     UUID             NOT NULL REFERENCES alert_thresholds(id),
    triggering_value DOUBLE PRECISION NOT NULL,
    status           TEXT             NOT NULL DEFAULT 'active',
    triggered_at     TIMESTAMPTZ      NOT NULL DEFAULT now(),
    acknowledged_at  TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ
);

-- Constraints have no IF NOT EXISTS, so they are guarded. See README.md.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alert_thresholds_device_pk_metric_key') THEN
        ALTER TABLE alert_thresholds ADD CONSTRAINT alert_thresholds_device_pk_metric_key
            UNIQUE (device_pk, metric);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alert_thresholds_metric_check') THEN
        ALTER TABLE alert_thresholds ADD CONSTRAINT alert_thresholds_metric_check
            CHECK (metric IN ('temperature', 'voltage', 'battery_level', 'power_consumption'));
    END IF;

    -- At least one bound must be set, or the threshold can never be violated.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alert_thresholds_has_a_bound') THEN
        ALTER TABLE alert_thresholds ADD CONSTRAINT alert_thresholds_has_a_bound
            CHECK (min_value IS NOT NULL OR max_value IS NOT NULL);
    END IF;

    -- When both bounds are given, the range they describe must be non-empty.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alert_thresholds_bounds_ordered') THEN
        ALTER TABLE alert_thresholds ADD CONSTRAINT alert_thresholds_bounds_ordered
            CHECK (min_value IS NULL OR max_value IS NULL OR min_value < max_value);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alerts_status_check') THEN
        ALTER TABLE alerts ADD CONSTRAINT alerts_status_check
            CHECK (status IN ('active', 'acknowledged', 'resolved'));
    END IF;
END $$;

-- At most one UNRESOLVED alert per device and metric. "Unresolved" spans both
-- active and acknowledged, since acknowledging a problem is not fixing it -
-- a second alert for the same still-open problem would be noise. This is the
-- guarantee under concurrency: two simultaneous violations race for this
-- index, and the loser's insert must be treated as a no-op, not an error.
CREATE UNIQUE INDEX IF NOT EXISTS alerts_one_unresolved_per_device_metric
    ON alerts (device_pk, metric)
    WHERE status IN ('active', 'acknowledged');

CREATE INDEX IF NOT EXISTS alerts_status_idx ON alerts (status);
