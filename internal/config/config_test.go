package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// clearEnv blanks every variable Load reads, so a test is not affected by
// whatever the developer has exported.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"APP_ENV", "LOG_LEVEL", "HTTP_PORT", "DATABASE_URL", "DB_MAX_CONNS",
		"DB_CONNECT_TIMEOUT", "READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT",
		"SHUTDOWN_TIMEOUT",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/edvexa")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, want development", cfg.AppEnv)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q, want 8080", cfg.HTTPPort)
	}
	if cfg.DBMaxConns != 10 {
		t.Errorf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
	if cfg.DBConnectTimeout != 5*time.Second {
		t.Errorf("DBConnectTimeout = %v, want 5s", cfg.DBConnectTimeout)
	}
	if cfg.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want 10s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout = %v, want 15s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.Addr() != ":8080" {
		t.Errorf("Addr() = %q, want :8080", cfg.Addr())
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true, want false for development")
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://db:5432/x")
	t.Setenv("DB_MAX_CONNS", "25")
	t.Setenv("READ_TIMEOUT", "1m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.DBMaxConns != 25 {
		t.Errorf("DBMaxConns = %d, want 25", cfg.DBMaxConns)
	}
	if cfg.ReadTimeout != time.Minute {
		t.Errorf("ReadTimeout = %v, want 1m", cfg.ReadTimeout)
	}
	if cfg.Addr() != ":9090" {
		t.Errorf("Addr() = %q, want :9090", cfg.Addr())
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, want true for production")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	clearEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error when DATABASE_URL is unset")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error = %q, want it to name DATABASE_URL", err)
	}
}

// Load must report every bad value at once; fixing a misconfigured deployment
// one restart at a time is the failure mode this guards against.
func TestLoadReportsAllInvalidValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/edvexa")
	t.Setenv("LOG_LEVEL", "chatty")
	t.Setenv("DB_MAX_CONNS", "ten")
	t.Setenv("READ_TIMEOUT", "soon")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	for _, want := range []string{"LOG_LEVEL", "DB_MAX_CONNS", "READ_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// A zero timeout is not a harmless value: http.Server treats it as "no
// timeout", silently removing the protection the timeouts exist to provide.
func TestLoadRejectsNonPositiveValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/edvexa")
	t.Setenv("READ_TIMEOUT", "0s")
	t.Setenv("DB_MAX_CONNS", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error for zero timeout and zero pool size")
	}
	for _, want := range []string{"READ_TIMEOUT", "DB_MAX_CONNS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// An unset variable falls back to its default; only a set-but-unparseable
// value is an error.
func TestLoadTreatsEmptyAsUnset(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/edvexa")
	t.Setenv("READ_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want the 10s default", cfg.ReadTimeout)
	}
}
