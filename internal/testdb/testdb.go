//go:build integration

// Package testdb connects integration tests to a real PostgreSQL database and
// hands them an empty one.
//
// Every integration-tagged package shares the one edvexa_test database, and
// Connect truncates it on every call. `go test ./...` runs different
// packages' test binaries as separate, concurrent processes by default (the
// -p flag, which defaults to GOMAXPROCS) — so without serialising them, one
// package's Connect can truncate tables out from under another package's
// test that is mid-run. `make test-integration` passes `-p 1` for exactly
// this reason. A bare `go test -tags=integration ./...` across the whole
// repository does not, and can fail intermittently as a result; a bare
// per-package command (`./internal/device/...`) is unaffected, since it is
// only one test binary.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	loadDotEnv()
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	t.Fatalf("set TEST_DATABASE_URL, or DATABASE_URL to derive the %q database from "+
		"(no .env found to load one from either)", testDB)
	return ""
}

var loadDotEnvOnce sync.Once

// loadDotEnv lets a bare `go test -tags=integration ./...` work the same way
// `make test-integration` does, without requiring whatever invokes it — a CI
// runner, an editor's test button — to have sourced .env first. It is a
// fallback: a variable already set in the environment is never overwritten.
func loadDotEnv() {
	loadDotEnvOnce.Do(func() {
		if os.Getenv("DATABASE_URL") != "" || os.Getenv("TEST_DATABASE_URL") != "" {
			return
		}
		path := findRepoEnvFile()
		if path == "" {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			if os.Getenv(key) == "" {
				os.Setenv(key, strings.TrimSpace(value))
			}
		}
	})
}

// findRepoEnvFile walks up from the working directory (a package directory,
// since `go test` runs each package there) looking for .env, stopping at the
// module root — marked by go.mod — so this can never read a file outside the
// repository, such as a stray .env in a parent directory.
func findRepoEnvFile() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if candidate := filepath.Join(dir, ".env"); fileExists(candidate) {
			return candidate
		}
		if fileExists(filepath.Join(dir, "go.mod")) {
			return "" // module root, no .env here; do not search above it
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
