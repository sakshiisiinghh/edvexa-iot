// Package device stores and validates monitored devices.
package device

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

const (
	maxDeviceIDLen = 128
	maxNameLen     = 200
	maxLocationLen = 200
)

var (
	ErrNotFound  = errors.New("device not found")
	ErrDuplicate = errors.New("device_id already registered")
	// ErrInvalid wraps every validation failure so a handler can tell bad
	// input from a database error without inspecting message text.
	ErrInvalid = errors.New("invalid device")
)

// Device is a monitored device. ID is the internal key telemetry references;
// DeviceID is the identifier the device itself reports.
type Device struct {
	ID        string    `json:"-"`
	DeviceID  string    `json:"device_id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (d Device) Active() bool { return d.Status == StatusActive }

type CreateInput struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (in CreateInput) validate() error {
	if err := required("device_id", in.DeviceID, maxDeviceIDLen); err != nil {
		return err
	}
	if err := required("name", in.Name, maxNameLen); err != nil {
		return err
	}
	return within("location", in.Location, maxLocationLen)
}

// UpdateInput uses pointers so an omitted field is distinguishable from one
// explicitly set to empty, which is what PATCH has to express.
type UpdateInput struct {
	Name     *string `json:"name"`
	Location *string `json:"location"`
}

func (in UpdateInput) validate() error {
	if in.Name == nil && in.Location == nil {
		return fmt.Errorf("%w: provide at least one of name or location", ErrInvalid)
	}
	if in.Name != nil {
		if err := required("name", *in.Name, maxNameLen); err != nil {
			return err
		}
	}
	if in.Location != nil {
		return within("location", *in.Location, maxLocationLen)
	}
	return nil
}

func required(field, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}
	return within(field, value, max)
}

func within(field, value string, max int) error {
	if len(value) > max {
		return fmt.Errorf("%w: %s must be at most %d characters", ErrInvalid, field, max)
	}
	return nil
}
