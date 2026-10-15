//go:build integration

// Package testdb connects integration tests to a real PostgreSQL database and
// hands them an empty one.
package testdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB is separate from the development database so integration tests never
// truncate data someone was using.
const testDB = "edvexa_test"

// Connect returns a pool to the test database with every application table
// empty. The pool is closed when the test finishes; rows are left behind so a
// failing test can be inspected, and cleared by the next Connect.
func Connect(t *testing.T) *pgxpool.Pool {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(connString(t))
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		cfg.ConnConfig.Database = testDB
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		// Failing rather than skipping: the integration build tag is already
		// the opt-in, so getting here means a database was expected.
		t.Fatalf("cannot reach database %q: %v\nrun: make db-up && make test-integration",
			cfg.ConnConfig.Database, err)
	}

	truncate(t, pool)
	t.Cleanup(pool.Close)
	return pool
}

func connString(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	t.Fatalf("set TEST_DATABASE_URL, or DATABASE_URL to derive the %q database from", testDB)
	return ""
}

// truncate empties every application table. The list comes from the catalogue
// rather than a constant, so tables added by later migrations are covered
// without editing this file.
func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tables *string
	err := pool.QueryRow(ctx, `
		SELECT string_agg(quote_ident(tablename), ', ')
		FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_migrations'`).Scan(&tables)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if tables == nil {
		t.Fatal("no application tables found; run `make migrate` against the test database")
	}

	if _, err := pool.Exec(ctx, "TRUNCATE "+*tables+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
