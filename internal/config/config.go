// Package config loads the service's configuration from environment
// variables. Every value has a usable default except DATABASE_URL, which the
// service refuses to start without: guessing a connection string would let a
// misconfigured deployment look healthy until its first query.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config is the fully resolved configuration for one process. It is read once
// at startup and then treated as immutable.
type Config struct {
	AppEnv   string
	LogLevel slog.Level

	HTTPPort        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	DatabaseURL string
	// DBMaxConns is int32 to match pgxpool.Config.MaxConns exactly, so the db
	// package can assign it without a conversion.
	DBMaxConns       int32
	DBConnectTimeout time.Duration
}

// Addr is the address the HTTP server listens on.
func (c Config) Addr() string { return ":" + c.HTTPPort }

// IsProduction reports whether logs should be machine-readable.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// Load reads the configuration from the environment. It reports every invalid
// value at once rather than failing on the first, so a misconfigured
// deployment takes one restart to diagnose instead of several.
func Load() (Config, error) {
	var l loader

	cfg := Config{
		AppEnv:   l.str("APP_ENV", "development"),
		LogLevel: l.level("LOG_LEVEL", slog.LevelInfo),

		HTTPPort:        l.str("HTTP_PORT", "8080"),
		ReadTimeout:     l.duration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    l.duration("WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:     l.duration("IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout: l.duration("SHUTDOWN_TIMEOUT", 10*time.Second),

		DatabaseURL:      l.required("DATABASE_URL"),
		DBMaxConns:       l.int32("DB_MAX_CONNS", 10),
		DBConnectTimeout: l.duration("DB_CONNECT_TIMEOUT", 5*time.Second),
	}

	if err := errors.Join(l.errs...); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// loader reads environment variables, collecting failures instead of
// returning them one at a time. An unset variable always falls back to the
// default; only a set-but-unparseable value is an error.
type loader struct {
	errs []error
}

func (l *loader) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (l *loader) required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		l.errs = append(l.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (l *loader) int32(key string, def int32) int32 {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %q is not an integer", key, raw))
		return def
	}
	if n <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s: must be positive, got %d", key, n))
		return def
	}
	return int32(n)
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %q is not a duration (want e.g. 5s)", key, raw))
		return def
	}
	if d <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s: must be positive, got %s", key, d))
		return def
	}
	return d
}

func (l *loader) level(key string, def slog.Level) slog.Level {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %q is not a level (want debug, info, warn or error)", key, raw))
		return def
	}
	return lvl
}
