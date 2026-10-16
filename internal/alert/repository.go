package alert

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

// UpsertThreshold creates or replaces the bounds for a device and metric.
// deviceID is the external identifier, echoed back as given rather than
// re-fetched - the caller already resolved it to devicePK, so no extra
// round trip is needed to report it.
func (r *Repository) UpsertThreshold(ctx context.Context, deviceID, devicePK string, in ThresholdInput) (Threshold, error) {
	var metric string
	var minV, maxV *float64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO alert_thresholds (device_pk, metric, min_value, max_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_pk, metric) DO UPDATE
			SET min_value = EXCLUDED.min_value, max_value = EXCLUDED.max_value, updated_at = now()
		RETURNING metric, min_value, max_value`,
		devicePK, in.Metric, in.MinValue, in.MaxValue,
	).Scan(&metric, &minV, &maxV)
	if err != nil {
		return Threshold{}, fmt.Errorf("upsert threshold: %w", err)
	}
	return Threshold{DeviceID: deviceID, Metric: metric, MinValue: minV, MaxValue: maxV}, nil
}

func (r *Repository) ListThresholds(ctx context.Context) ([]Threshold, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.device_id, t.metric, t.min_value, t.max_value
		FROM alert_thresholds t
		JOIN devices d ON d.id = t.device_pk
		ORDER BY d.device_id, t.metric`)
	if err != nil {
		return nil, fmt.Errorf("list thresholds: %w", err)
	}
	defer rows.Close()

	thresholds := []Threshold{}
	for rows.Next() {
		var th Threshold
		if err := rows.Scan(&th.DeviceID, &th.Metric, &th.MinValue, &th.MaxValue); err != nil {
			return nil, fmt.Errorf("scan threshold: %w", err)
		}
		thresholds = append(thresholds, th)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list thresholds: %w", err)
	}
	return thresholds, nil
}

// scanAlert expects columns in Alert's field order: id, device_id, metric,
// triggering_value, status, triggered_at, acknowledged_at, resolved_at.
// Every query below selects in that order, though under different table
// aliases, so the literal column list is not shareable as one constant.
func scanAlert(row pgx.Row) (Alert, error) {
	var a Alert
	err := row.Scan(&a.ID, &a.DeviceID, &a.Metric, &a.TriggeringValue, &a.Status,
		&a.TriggeredAt, &a.AcknowledgedAt, &a.ResolvedAt)
	return a, err
}

// ListAlerts returns alerts newest first, optionally filtered by status. An
// empty status returns every alert regardless of status.
func (r *Repository) ListAlerts(ctx context.Context, status string) ([]Alert, error) {
	query := `
		SELECT a.id, d.device_id, a.metric, a.triggering_value, a.status, a.triggered_at, a.acknowledged_at, a.resolved_at
		FROM alerts a JOIN devices d ON d.id = a.device_pk`
	args := []any{}
	if status != "" {
		query += ` WHERE a.status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY a.triggered_at DESC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	alerts := []Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	return alerts, nil
}

// Acknowledge transitions an alert from active to acknowledged. The WHERE
// clause makes the check the database's: a row is only updated if it is
// still active, so two concurrent acknowledges cannot both succeed, and
// there is no separate read to race against the write.
func (r *Repository) Acknowledge(ctx context.Context, id int64) (Alert, error) {
	a, err := scanAlert(r.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE alerts SET status = 'acknowledged', acknowledged_at = now()
			WHERE id = $1 AND status = 'active'
			RETURNING id, device_pk, metric, triggering_value, status, triggered_at, acknowledged_at, resolved_at
		)
		SELECT u.id, d.device_id, u.metric, u.triggering_value, u.status, u.triggered_at, u.acknowledged_at, u.resolved_at
		FROM updated u JOIN devices d ON d.id = u.device_pk`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r.transitionError(ctx, id)
	}
	if err != nil {
		return Alert{}, fmt.Errorf("acknowledge alert: %w", err)
	}
	return a, nil
}

// Resolve transitions an alert from active or acknowledged to resolved.
// Both source statuses count as unresolved - the same grouping the partial
// unique index uses - so resolving does not require acknowledging first; a
// condition that clears on its own is resolved the same way one a human
// looked at is.
func (r *Repository) Resolve(ctx context.Context, id int64) (Alert, error) {
	a, err := scanAlert(r.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE alerts SET status = 'resolved', resolved_at = now()
			WHERE id = $1 AND status IN ('active', 'acknowledged')
			RETURNING id, device_pk, metric, triggering_value, status, triggered_at, acknowledged_at, resolved_at
		)
		SELECT u.id, d.device_id, u.metric, u.triggering_value, u.status, u.triggered_at, u.acknowledged_at, u.resolved_at
		FROM updated u JOIN devices d ON d.id = u.device_pk`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r.transitionError(ctx, id)
	}
	if err != nil {
		return Alert{}, fmt.Errorf("resolve alert: %w", err)
	}
	return a, nil
}

// transitionError distinguishes "no such alert" from "that alert exists but
// cannot make this transition right now" - a refinement on top of the
// conditional UPDATE, which only tells us the transition did not happen, not
// why. The existence check does not race with anything: it only decides
// which error to report for an update that has already failed.
func (r *Repository) transitionError(ctx context.Context, id int64) (Alert, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM alerts WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return Alert{}, fmt.Errorf("check alert existence: %w", err)
	}
	if !exists {
		return Alert{}, ErrNotFound
	}
	return Alert{}, ErrInvalidTransition
}

// Evaluate implements telemetry.AlertEvaluator. It runs inside the caller's
// transaction: if no threshold is configured for this device and metric,
// there is nothing to check. If the reading violates the configured bound,
// it inserts an alert - and if another transaction wins the race to do the
// same thing first, ON CONFLICT ... DO NOTHING makes that a normal outcome,
// not an error, so the telemetry write this is evaluating for never gets
// rolled back on account of losing an alert race. The WHERE clause on
// ON CONFLICT must match migrations/0003_alerts.sql's partial unique index
// exactly, or Postgres cannot infer which index the conflict target is.
//
// RETURNING, not Exec, is what distinguishes "this call created the alert"
// from "lost the race": ON CONFLICT DO NOTHING suppresses RETURNING too, so
// a lost race surfaces as pgx.ErrNoRows here exactly as it does in
// telemetry.Repository.Insert's own duplicate-detection query.
func (r *Repository) Evaluate(ctx context.Context, tx pgx.Tx, devicePK, metric string, value float64) (alertID int64, created bool, err error) {
	var thresholdID string
	var minV, maxV *float64
	err = tx.QueryRow(ctx,
		`SELECT id, min_value, max_value FROM alert_thresholds WHERE device_pk = $1 AND metric = $2`,
		devicePK, metric,
	).Scan(&thresholdID, &minV, &maxV)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // no threshold configured for this metric
	}
	if err != nil {
		return 0, false, fmt.Errorf("look up threshold: %w", err)
	}

	violated := (minV != nil && value < *minV) || (maxV != nil && value > *maxV)
	if !violated {
		return 0, false, nil
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO alerts (device_pk, metric, threshold_id, triggering_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_pk, metric) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id`,
		devicePK, metric, thresholdID, value,
	).Scan(&alertID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // lost the race; a normal outcome, not an error
	}
	if err != nil {
		return 0, false, fmt.Errorf("insert alert: %w", err)
	}
	return alertID, true, nil
}
