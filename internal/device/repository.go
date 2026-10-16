package device

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is SQLSTATE 23505, spelled out rather than pulled from
// github.com/jackc/pgerrcode to avoid a dependency for one constant.
const uniqueViolation = "23505"

// deviceIDConstraint names migrations/0001_devices.sql's unique constraint.
// Checked explicitly so a 23505 on a different constraint (the id primary
// key, or one added to this table later) is not misreported as a duplicate
// device_id.
const deviceIDConstraint = "devices_device_id_key"

const columns = `id, device_id, name, location, status, created_at, updated_at`

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create registers a device. A repeated device_id is reported as ErrDuplicate
// by the unique constraint rather than by a preceding existence check, which
// two concurrent requests could both pass.
func (r *Repository) Create(ctx context.Context, in CreateInput) (Device, error) {
	if err := in.validate(); err != nil {
		return Device{}, err
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO devices (device_id, name, location)
		VALUES ($1, $2, $3)
		RETURNING `+columns,
		strings.TrimSpace(in.DeviceID), strings.TrimSpace(in.Name), in.Location)

	d, err := scan(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == deviceIDConstraint {
			return Device{}, ErrDuplicate
		}
		return Device{}, fmt.Errorf("create device: %w", err)
	}
	return d, nil
}

func (r *Repository) GetByDeviceID(ctx context.Context, deviceID string) (Device, error) {
	d, err := scan(r.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM devices WHERE device_id = $1`, deviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

func (r *Repository) List(ctx context.Context) ([]Device, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+columns+` FROM devices ORDER BY device_id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	devices := []Device{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return devices, nil
}

// Update applies the fields that were provided. COALESCE leaves the stored
// value in place for a nil argument, so no dynamic SQL is needed.
func (r *Repository) Update(ctx context.Context, deviceID string, in UpdateInput) (Device, error) {
	if err := in.validate(); err != nil {
		return Device{}, err
	}

	d, err := scan(r.pool.QueryRow(ctx, `
		UPDATE devices
		SET name = COALESCE($2, name),
		    location = COALESCE($3, location),
		    updated_at = now()
		WHERE device_id = $1
		RETURNING `+columns,
		deviceID, trimmed(in.Name), in.Location))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("update device: %w", err)
	}
	return d, nil
}

// Deactivate is idempotent: deactivating an already inactive device succeeds
// and returns it unchanged apart from updated_at.
func (r *Repository) Deactivate(ctx context.Context, deviceID string) (Device, error) {
	d, err := scan(r.pool.QueryRow(ctx, `
		UPDATE devices
		SET status = $2, updated_at = now()
		WHERE device_id = $1
		RETURNING `+columns,
		deviceID, StatusInactive))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("deactivate device: %w", err)
	}
	return d, nil
}

func scan(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.DeviceID, &d.Name, &d.Location, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}
