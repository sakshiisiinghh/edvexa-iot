package telemetry

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalid wraps every validation failure so a handler can tell bad input
// from a database error without inspecting message text.
var ErrInvalid = errors.New("invalid telemetry event")

// allowedMetrics mirrors migrations/0002_telemetry.sql's CHECK constraint.
// Validating here too means a bad metric gets a clean 422 instead of
// surfacing as a raw constraint-violation error.
var allowedMetrics = map[string]bool{
	"temperature":       true,
	"voltage":           true,
	"battery_level":     true,
	"power_consumption": true,
}

// IngestInput is the wire shape of POST /v1/telemetry. DeviceID is resolved
// to an internal key by the caller, not by Validate - that lookup needs a
// database round trip and belongs with the handler, not with field checks.
type IngestInput struct {
	EventID    string    `json:"event_id"`
	DeviceID   string    `json:"device_id"`
	Metric     string    `json:"metric"`
	Value      float64   `json:"value"`
	RecordedAt time.Time `json:"recorded_at"`
}

func (in IngestInput) Validate() error {
	if strings.TrimSpace(in.EventID) == "" {
		return fmt.Errorf("%w: event_id is required", ErrInvalid)
	}
	if strings.TrimSpace(in.DeviceID) == "" {
		return fmt.Errorf("%w: device_id is required", ErrInvalid)
	}
	if !allowedMetrics[in.Metric] {
		return fmt.Errorf("%w: metric must be one of temperature, voltage, battery_level, power_consumption", ErrInvalid)
	}
	if in.RecordedAt.IsZero() {
		return fmt.Errorf("%w: recorded_at is required", ErrInvalid)
	}
	return nil
}

// Reading is a stored event as returned to a caller listing a device's
// history. It omits DevicePK and the payload hash, neither of which a
// caller outside this package has a use for.
type Reading struct {
	EventID    string    `json:"event_id"`
	Metric     string    `json:"metric"`
	Value      float64   `json:"value"`
	RecordedAt time.Time `json:"recorded_at"`
}
