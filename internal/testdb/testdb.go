//go:build integration

// Package testdb connects integration tests to a real PostgreSQL database and
// hands them an empty one.
//
// `go test ./...` runs different packages' test binaries as separate,
// concurrent processes by default (the -p flag, which defaults to
// GOMAXPROCS). Every integration-tagged package therefore gets its own
// database, derived from the caller's package directory and provisioned on
// first use, rather than sharing one — so one package's Connect can never
// truncate tables out from under another package's test that is mid-run,
// no matter how the test binaries are scheduled. Within one package, tests
// still run sequentially (nothing here calls t.Parallel), so truncating on
// every Connect remains correct for isolating one test from the next.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB is the database used when TEST_DATABASE_URL is set explicitly - the
// caller has taken responsibility for exactly which database that is. It is
// also the prefix for the per-package names derived from DATABASE_URL.
const testDB = "edvexa_test"

// Connect returns a pool to this package's test database with every
// application table empty. The pool is closed when the test finishes; rows
// are left behind so a failing test can be inspected, and cleared by the
// next Connect.
func Connect(t *testing.T) *pgxpool.Pool {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(connString(t))
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}

	if os.Getenv("TEST_DATABASE_URL") == "" {
		dbName := testDB + "_" + callerPackageName()
		provisionOnce.Do(func() {
			provisionErr = provisionDatabase(dbName, cfg.ConnConfig.Copy())
		})
		if provisionErr != nil {
			t.Fatalf("provision test database %q: %v", dbName, provisionErr)
		}
		cfg.ConnConfig.Database = dbName
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
	t.Fatalf("set TEST_DATABASE_URL, or DATABASE_URL to derive a per-package database from " +
		"(no .env found to load one from either)")
	return ""
}

// Provisioning runs at most once per test process - and every package's
// tests run as their own process - so a single pair of package-level
// variables is enough; there is no second package whose Connect calls could
// land here. The error is captured rather than left to sync.Once's normal
// "only the first caller sees it" behaviour, so every test in the package
// fails with the same clear message instead of a confusing downstream one.
var (
	provisionOnce sync.Once
	provisionErr  error
)

// callerPackageName derives a database-name fragment from the directory of
// whichever test file called Connect. skip=2 from inside this function lands
// on Connect's caller: 0 is callerPackageName itself, 1 is Connect, 2 is the
// test function. This assumes Connect is called directly from a test
// function, which is true everywhere in this repository today; a test
// calling Connect through its own helper would need that helper to report
// its caller's frame instead, or would be misattributed to the helper's
// package.
func callerPackageName() string {
	_, file, _, ok := runtime.Caller(2)
	if !ok {
		return "default"
	}
	return sanitizeIdent(filepath.Base(filepath.Dir(file)))
}

func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

// provisionDatabase creates dbName if it does not exist and applies every
// migration to it directly, so a package's first Connect call is enough to
// make its database usable with no separate migration step. It reuses the
// actual migrations/*.sql files rather than a second copy of the schema, so
// there is one definition of the schema, not two that can drift apart.
//
// There is no schema_migrations bookkeeping here: these are throwaway,
// per-package test databases, every migration file is already required to
// be idempotent (see migrations/README.md), and re-applying a handful of
// `CREATE TABLE IF NOT EXISTS` statements on the rare repeat run costs
// nothing. The tracking table exists for the real deployment target, which
// this is not.
func provisionDatabase(dbName string, baseConfig *pgx.ConnConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	adminConfig := baseConfig.Copy()
	adminConfig.Database = "postgres"
	adminConn, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		return fmt.Errorf("connect to admin database: %w", err)
	}
	defer adminConn.Close(ctx)

	_, err = adminConn.Exec(ctx, `CREATE DATABASE `+quoteIdent(dbName))
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create database %s: %w", dbName, err)
	}

	dbConfig := baseConfig.Copy()
	dbConfig.Database = dbName
	dbConn, err := pgx.ConnectConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}
	defer dbConn.Close(ctx)

	return applyMigrations(ctx, dbConn)
}

func applyMigrations(ctx context.Context, conn *pgx.Conn) error {
	root := findRepoRoot()
	if root == "" {
		return errors.New("could not locate the repository root (no go.mod found above the working directory)")
	}

	dir := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(dir) // sorted by name, matching the Makefile's migrate target
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if _, err := conn.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply %s: %w", e.Name(), err)
		}
	}
	return nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
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

// findRepoRoot walks up from the working directory (a package directory,
// since `go test` runs each package there) looking for go.mod.
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// findRepoEnvFile looks for .env at the repository root only - not above it,
// so this can never read a file outside the repository, such as a stray
// .env in a parent directory.
func findRepoEnvFile() string {
	root := findRepoRoot()
	if root == "" {
		return ""
	}
	if candidate := filepath.Join(root, ".env"); fileExists(candidate) {
		return candidate
	}
	return ""
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
