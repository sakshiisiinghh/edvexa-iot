//go:build integration

package testdb

import (
	"context"
	"testing"
)

// Connect's provisioning applies migrations/*.sql directly rather than
// through the Makefile's schema_migrations bookkeeping, which these
// throwaway per-package databases have no use for - so "migrated" is
// checked here by querying for the tables the migrations actually create,
// not by a tracking row that intentionally does not exist in this path.
func TestConnectGivesAMigratedDatabase(t *testing.T) {
	pool := Connect(t)
	ctx := context.Background()

	for _, table := range []string{"devices", "telemetry_events"} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("%s is not queryable, so the schema was not applied: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows, want 0", table, n)
		}
	}
}

// Connect must clear rows left by earlier work, without depending on the order
// tests happen to run in.
func TestConnectTruncates(t *testing.T) {
	pool := Connect(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		"INSERT INTO devices (device_id, name) VALUES ('testdb-harness', 'Harness')")
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	fresh := Connect(t)

	var n int
	if err := fresh.QueryRow(ctx, "SELECT count(*) FROM devices").Scan(&n); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if n != 0 {
		t.Errorf("devices has %d rows after Connect, want 0", n)
	}
}
