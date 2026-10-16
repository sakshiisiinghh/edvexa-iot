package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertEvaluator is implemented by internal/alert.Repository. Defined here,
// not there, so neither package needs to import the other: alert's method
// set happens to satisfy this interface structurally, and main.go is the
// only place that wires the two together.
type AlertEvaluator interface {
	// Evaluate runs inside the caller's transaction and reports whether IT
	// created a new alert - false covers both "no threshold was violated"
	// and "another transaction won the race to create one first," which are
	// normal outcomes, not errors; see internal/alert.Repository.Evaluate.
	// created is what lets a caller publish an alert-raised notification
	// only once per alert, not once per reading that happened to violate an
	// already-unresolved one. Any error returned here aborts the whole
	// transaction, including the telemetry row just inserted: alert state
	// and telemetry state are meant to rise and fall together.
	Evaluate(ctx context.Context, tx pgx.Tx, devicePK, metric string, value float64) (alertID int64, created bool, err error)
}

type Repository struct {
	pool      *pgxpool.Pool
	evaluator AlertEvaluator
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// UseAlertEvaluator wires threshold evaluation into Insert, invoked inside
// the same transaction as a successful write. Optional: a Repository with
// no evaluator just never evaluates thresholds, which every test written
// before alerts existed already relies on.
func (r *Repository) UseAlertEvaluator(e AlertEvaluator) {
	r.evaluator = e
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
		// Only a genuine create reaches here - never a duplicate or conflict -
		// so a duplicate reading cannot raise a second alert.
		var alertID int64
		var alertCreated bool
		if r.evaluator != nil {
			var evalErr error
			alertID, alertCreated, evalErr = r.evaluator.Evaluate(ctx, tx, in.DevicePK, in.Metric, in.Value)
			if evalErr != nil {
				return Result{}, fmt.Errorf("evaluate thresholds: %w", evalErr)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit: %w", err)
		}
		return Result{Outcome: OutcomeCreated, Event: in, AlertCreated: alertCreated, AlertID: alertID}, nil

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
