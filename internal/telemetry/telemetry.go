// Package telemetry stores ingested device readings and guarantees that a
// client-supplied event ID is processed at most once per distinct payload.
package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Event is a single reading submitted for ingestion. DevicePK is the
// internal key the caller has already resolved from DeviceID; Insert does
// not look it up, so an unknown or inactive device is rejected before
// Insert is ever called.
type Event struct {
	EventID    string
	DeviceID   string // external identifier, as submitted - part of the hashed payload
	DevicePK   string // internal devices.id, used for the foreign key
	Metric     string
	Value      float64
	RecordedAt time.Time
}

// payloadHash covers exactly what a resubmission must match to count as a
// duplicate rather than a conflict: not EventID, since that is the key being
// looked up by, and not DevicePK, since that is a resolution detail rather
// than something the client submitted.
//
// RecordedAt is normalised to UTC before hashing so the same instant written
// with a different but equivalent timezone offset still hashes identically -
// otherwise a client consistent about the moment but inconsistent about how
// it writes the offset would be misreported as a conflict.
func payloadHash(e Event) string {
	// Error is impossible: every field is a string, float64 or a time
	// formatted to a string, none of which json.Marshal can fail on.
	canonical, _ := json.Marshal(map[string]any{
		"device_id":   e.DeviceID,
		"metric":      e.Metric,
		"value":       e.Value,
		"recorded_at": e.RecordedAt.UTC().Format(time.RFC3339Nano),
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Outcome is what Insert actually did, distinguishing a fresh write from the
// two ways a repeated event_id can resolve.
type Outcome int

const (
	OutcomeCreated Outcome = iota
	OutcomeDuplicate
	OutcomeConflict
)

// Result carries the outcome plus the event that was submitted - what a
// later caller needs to publish a notification. Insert itself does not
// publish, so a message is never sent for a write that did not commit.
type Result struct {
	Outcome Outcome
	Event   Event
}
