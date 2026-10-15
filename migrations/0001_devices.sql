CREATE TABLE IF NOT EXISTS devices (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id  TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    location   TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Constraints have no IF NOT EXISTS, so they are guarded. See README.md.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'devices_device_id_key') THEN
        ALTER TABLE devices ADD CONSTRAINT devices_device_id_key UNIQUE (device_id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'devices_status_check') THEN
        ALTER TABLE devices ADD CONSTRAINT devices_status_check
            CHECK (status IN ('active', 'inactive'));
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'devices_device_id_not_blank') THEN
        ALTER TABLE devices ADD CONSTRAINT devices_device_id_not_blank
            CHECK (length(btrim(device_id)) > 0);
    END IF;
END $$;
