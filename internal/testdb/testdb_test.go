//go:build integration

package testdb

import (
	"context"
	"testing"
)

func TestConnectGivesAMigratedDatabase(t *testing.T) {
	pool := Connect(t)
	ctx := context.Background()

	var applied int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied == 0 {
		t.Error("no migrations recorded in the test database")
	}

	var devices int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM devices").Scan(&devices); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if devices != 0 {
		t.Errorf("devices has %d rows, want 0", devices)
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
