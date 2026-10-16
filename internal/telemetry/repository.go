package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert records a reading, or reports why it was not recorded again. The
// outcome is decided inside one transaction so the fallback SELECT can see a
// competing transaction's commit: under READ COMMITTED each statement in a
// transaction gets a fresh snapshot, so running the SELECT as its own
// statement - rather than folding it into the INSERT as a CTE - is what lets
// it see a row the other transaction committed after this one began. A CTE
// would read the pre-insert snapshot and find nothing.
func (r *Repository) Insert(ctx context.Context, in Event) (Result, error) {
	hash := payloadHash(in)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO telemetry_events (event_id, device_pk, metric, value, recorded_at, payload_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING id`,
		in.EventID, in.DevicePK, in.Metric, in.Value, in.RecordedAt, hash,
	).Scan(&id)

	switch {
	case err == nil:
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit: %w", err)
		}
		return Result{Outcome: OutcomeCreated, Event: in}, nil

	case errors.Is(err, pgx.ErrNoRows):
		var existingHash string
		if err := tx.QueryRow(ctx,
			`SELECT payload_hash FROM telemetry_events WHERE event_id = $1`, in.EventID,
		).Scan(&existingHash); err != nil {
			return Result{}, fmt.Errorf("look up existing event: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit: %w", err)
		}
		if existingHash == hash {
			return Result{Outcome: OutcomeDuplicate, Event: in}, nil
		}
		return Result{Outcome: OutcomeConflict, Event: in}, nil

	default:
		return Result{}, fmt.Errorf("insert telemetry event: %w", err)
	}
}

func (r *Repository) ListByDevice(ctx context.Context, devicePK string, limit int) ([]Reading, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT event_id, metric, value, recorded_at
		FROM telemetry_events
		WHERE device_pk = $1
		ORDER BY recorded_at DESC
		LIMIT $2`,
		devicePK, limit)
	if err != nil {
		return nil, fmt.Errorf("list telemetry: %w", err)
	}
	defer rows.Close()

	readings := []Reading{}
	for rows.Next() {
		var rd Reading
		if err := rows.Scan(&rd.EventID, &rd.Metric, &rd.Value, &rd.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan reading: %w", err)
		}
		readings = append(readings, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list telemetry: %w", err)
	}
	return readings, nil
}
