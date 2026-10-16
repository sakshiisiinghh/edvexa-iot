package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fakePinger struct {
	err    error
	gotCtx context.Context
}

func (f *fakePinger) Ping(ctx context.Context) error {
	f.gotCtx = ctx
	return f.err
}

func newTestRouter(p Pinger) http.Handler {
	return NewRouter(Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pinger: p,
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return body
}

// Liveness must not depend on the database, so it stays 200 even when the
// pool is failing.
func TestHealthzIgnoresDatabase(t *testing.T) {
	p := &fakePinger{err: errors.New("database is down")}
	rec := get(t, newTestRouter(p), "/healthz")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := decodeStatus(t, rec)["status"]; got != "ok" {
		t.Errorf("status field = %q, want ok", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if p.gotCtx != nil {
		t.Error("healthz pinged the database; it must not")
	}
}

func TestReadyzWhenDatabaseUp(t *testing.T) {
	rec := get(t, newTestRouter(&fakePinger{}), "/readyz")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := decodeStatus(t, rec)["status"]; got != "ready" {
		t.Errorf("status field = %q, want ready", got)
	}
}

func TestReadyzWhenDatabaseDown(t *testing.T) {
	rec := get(t, newTestRouter(&fakePinger{err: errors.New("connection refused")}), "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	body := decodeStatus(t, rec)
	if body["status"] != "unready" {
		t.Errorf("status field = %q, want unready", body["status"])
	}
	if body["error"] != "connection refused" {
		t.Errorf("error field = %q, want the ping failure", body["error"])
	}
}

// Without a deadline a hung database would hold the readiness probe open until
// the client gave up, which defeats the point of the probe.
func TestReadyzBoundsThePing(t *testing.T) {
	p := &fakePinger{}
	get(t, newTestRouter(p), "/readyz")

	if p.gotCtx == nil {
		t.Fatal("readyz did not ping the database")
	}
	if _, ok := p.gotCtx.Deadline(); !ok {
		t.Error("ping context has no deadline")
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	if rec := get(t, newTestRouter(&fakePinger{}), "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Recoverer must be mounted inside the request logger: a panicking handler
// becomes a 500 and the process survives.
func TestPanicBecomes500(t *testing.T) {
	h := newTestRouter(&fakePinger{})
	h.(chi.Router).Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	if rec := get(t, h, "/panic"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
