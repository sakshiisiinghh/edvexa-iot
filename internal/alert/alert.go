// Package alert configures thresholds and raises alerts when telemetry
// readings cross them.
package alert

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusActive       = "active"
	StatusAcknowledged = "acknowledged"
	StatusResolved     = "resolved"
)

// ErrInvalid wraps every validation failure so a handler can tell bad input
// from a database error without inspecting message text.
var ErrInvalid = errors.New("invalid threshold")

var ErrNotFound = errors.New("alert not found")

// ErrInvalidTransition is distinct from ErrInvalid: that is a malformed
// request body, this is a well-formed request that the alert's current
// status does not allow.
var ErrInvalidTransition = errors.New("alert cannot make that transition from its current status")

// allowedMetrics mirrors migrations/0003_alerts.sql's CHECK constraint, and
// migrations/0002_telemetry.sql's - the same four metrics a reading can
// report. Duplicated rather than imported from internal/telemetry, which
// would create a dependency between the two packages for one constant; the
// three CHECK constraints across the migration files already duplicate this
// list, so this is a fourth copy of an existing cost, not a new one.
var allowedMetrics = map[string]bool{
	"temperature":       true,
	"voltage":           true,
	"battery_level":     true,
	"power_consumption": true,
}

// ThresholdInput is the wire shape of POST /v1/thresholds. DeviceID is
// resolved to an internal key by the caller, not by Validate.
type ThresholdInput struct {
	DeviceID string   `json:"device_id"`
	Metric   string   `json:"metric"`
	MinValue *float64 `json:"min_value"`
	MaxValue *float64 `json:"max_value"`
}

func (in ThresholdInput) Validate() error {
	if strings.TrimSpace(in.DeviceID) == "" {
		return fmt.Errorf("%w: device_id is required", ErrInvalid)
	}
	if !allowedMetrics[in.Metric] {
		return fmt.Errorf("%w: metric must be one of temperature, voltage, battery_level, power_consumption", ErrInvalid)
	}
	if in.MinValue == nil && in.MaxValue == nil {
		return fmt.Errorf("%w: at least one of min_value or max_value is required", ErrInvalid)
	}
	if in.MinValue != nil && in.MaxValue != nil && *in.MinValue >= *in.MaxValue {
		return fmt.Errorf("%w: min_value must be less than max_value", ErrInvalid)
	}
	return nil
}

// Threshold is a configured bound for one device and metric.
type Threshold struct {
	DeviceID string   `json:"device_id"`
	Metric   string   `json:"metric"`
	MinValue *float64 `json:"min_value,omitempty"`
	MaxValue *float64 `json:"max_value,omitempty"`
}

// Alert is a recorded threshold violation.
type Alert struct {
	ID              int64      `json:"id"`
	DeviceID        string     `json:"device_id"`
	Metric          string     `json:"metric"`
	TriggeringValue float64    `json:"triggering_value"`
	Status          string     `json:"status"`
	TriggeredAt     time.Time  `json:"triggered_at"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}
