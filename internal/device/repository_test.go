//go:build integration

package device

import (
	"context"
	"errors"
	"testing"

	"github.com/sakshiisiinghh/edvexa-iot/internal/testdb"
)

func TestCreateAndPersist(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	created, err := repo.Create(ctx, CreateInput{DeviceID: "dev-create", Name: "Freezer", Location: "Lab 1"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != StatusActive {
		t.Errorf("Status = %q, want active", created.Status)
	}
	if created.ID == "" {
		t.Error("ID is empty, want a generated UUID")
	}

	got, err := repo.GetByDeviceID(ctx, "dev-create")
	if err != nil {
		t.Fatalf("GetByDeviceID() error = %v", err)
	}
	if got != created {
		t.Errorf("GetByDeviceID() = %+v, want %+v", got, created)
	}
}

// The unique constraint, not a preceding SELECT, is what makes this safe
// under concurrency; this only proves the constraint is wired to ErrDuplicate.
func TestCreateRejectsDuplicateDeviceID(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	in := CreateInput{DeviceID: "dev-dup", Name: "First"}
	if _, err := repo.Create(ctx, in); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}

	in.Name = "Second"
	_, err := repo.Create(ctx, in)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second Create() error = %v, want ErrDuplicate", err)
	}

	got, err := repo.GetByDeviceID(ctx, "dev-dup")
	if err != nil {
		t.Fatalf("GetByDeviceID() error = %v", err)
	}
	if got.Name != "First" {
		t.Errorf("stored name = %q, want %q (the rejected write must not land)", got.Name, "First")
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	_, err := repo.Create(ctx, CreateInput{DeviceID: "", Name: "No ID"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}

	if _, err := repo.GetByDeviceID(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("rejected create left a row behind: GetByDeviceID(\"\") error = %v, want ErrNotFound", err)
	}
}

func TestUpdateChangesOnlyGivenFields(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	original, err := repo.Create(ctx, CreateInput{DeviceID: "dev-update", Name: "Original", Location: "Here"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	newName := "Renamed"
	updated, err := repo.Update(ctx, "dev-update", UpdateInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != newName {
		t.Errorf("Name = %q, want %q", updated.Name, newName)
	}
	if updated.Location != original.Location {
		t.Errorf("Location = %q, want unchanged %q", updated.Location, original.Location)
	}
	if !updated.UpdatedAt.After(original.UpdatedAt) {
		t.Error("UpdatedAt did not advance")
	}
	if !updated.CreatedAt.Equal(original.CreatedAt) {
		t.Error("CreatedAt changed, want unchanged")
	}
}

func TestUpdateUnknownDevice(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	name := "X"
	_, err := repo.Update(context.Background(), "does-not-exist", UpdateInput{Name: &name})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

// Deactivate sets status rather than deleting, so the device and its future
// telemetry foreign-key references survive.
func TestDeactivateLeavesDeviceReadable(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	if _, err := repo.Create(ctx, CreateInput{DeviceID: "dev-deactivate", Name: "X"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	deactivated, err := repo.Deactivate(ctx, "dev-deactivate")
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if deactivated.Status != StatusInactive {
		t.Errorf("Status = %q, want inactive", deactivated.Status)
	}

	got, err := repo.GetByDeviceID(ctx, "dev-deactivate")
	if err != nil {
		t.Fatalf("device became unreadable after deactivation: %v", err)
	}
	if got.Status != StatusInactive {
		t.Errorf("Status = %q, want inactive", got.Status)
	}
}

// Deactivating twice is a valid retry, not an error: an operator should be
// able to call deactivate without first checking current status.
func TestDeactivateIsIdempotent(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	if _, err := repo.Create(ctx, CreateInput{DeviceID: "dev-idempotent", Name: "X"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repo.Deactivate(ctx, "dev-idempotent"); err != nil {
		t.Fatalf("first Deactivate() error = %v", err)
	}
	second, err := repo.Deactivate(ctx, "dev-idempotent")
	if err != nil {
		t.Fatalf("second Deactivate() error = %v, want success", err)
	}
	if second.Status != StatusInactive {
		t.Errorf("Status = %q, want inactive", second.Status)
	}
}

func TestDeactivateUnknownDevice(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	_, err := repo.Deactivate(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Deactivate() error = %v, want ErrNotFound", err)
	}
}

func TestGetByDeviceIDUnknown(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	_, err := repo.GetByDeviceID(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByDeviceID() error = %v, want ErrNotFound", err)
	}
}

func TestListReturnsCreatedDevices(t *testing.T) {
	repo := NewRepository(testdb.Connect(t))
	ctx := context.Background()

	for _, id := range []string{"dev-list-a", "dev-list-b"} {
		if _, err := repo.Create(ctx, CreateInput{DeviceID: id, Name: id}); err != nil {
			t.Fatalf("Create(%q) error = %v", id, err)
		}
	}

	devices, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("List() returned %d devices, want 2", len(devices))
	}
}
