//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakshiisiinghh/edvexa-iot/internal/alert"
	"github.com/sakshiisiinghh/edvexa-iot/internal/device"
	"github.com/sakshiisiinghh/edvexa-iot/internal/telemetry"
	"github.com/sakshiisiinghh/edvexa-iot/internal/testdb"
)

// newIntegrationRouter wires every repository exactly as cmd/api/main.go
// does, including UseAlertEvaluator, so these tests exercise the same
// configuration that actually runs - not a simplified stand-in for it.
func newIntegrationRouter(pool *pgxpool.Pool) (http.Handler, *device.Repository, *alert.Repository) {
	devices := device.NewRepository(pool)
	telemetryRepo := telemetry.NewRepository(pool)
	alerts := alert.NewRepository(pool)
	telemetryRepo.UseAlertEvaluator(alerts)

	r := NewRouter(Deps{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pinger:    pool,
		Devices:   devices,
		Telemetry: telemetryRepo,
		Alerts:    alerts,
	})
	return r, devices, alerts
}

func mustCreateDevice(t *testing.T, repo *device.Repository, deviceID string) device.Device {
	t.Helper()
	d, err := repo.Create(context.Background(), device.CreateInput{DeviceID: deviceID, Name: deviceID})
	if err != nil {
		t.Fatalf("create device %q: %v", deviceID, err)
	}
	return d
}

func eventBody(eventID, deviceID, metric string, value float64, recordedAt time.Time) map[string]any {
	return map[string]any{
		"event_id":    eventID,
		"device_id":   deviceID,
		"metric":      metric,
		"value":       value,
		"recorded_at": recordedAt.Format(time.RFC3339),
	}
}

// doPostJSON takes pre-marshalled bytes rather than calling t.Fatalf itself,
// so it is safe to call from a goroutine other than the test's own - which
// the concurrency tests below do.
func doPostJSON(h http.Handler, path string, data []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return doPostJSON(h, path, data)
}

func countTelemetryRows(t *testing.T, pool *pgxpool.Pool, eventID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM telemetry_events WHERE event_id = $1", eventID).Scan(&n); err != nil {
		t.Fatalf("count rows for %q: %v", eventID, err)
	}
	return n
}

func storedValue(t *testing.T, pool *pgxpool.Pool, eventID string) float64 {
	t.Helper()
	var v float64
	if err := pool.QueryRow(context.Background(),
		"SELECT value FROM telemetry_events WHERE event_id = $1", eventID).Scan(&v); err != nil {
		t.Fatalf("get stored value for %q: %v", eventID, err)
	}
	return v
}

func TestIngestIdempotentOnIdenticalResubmission(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-idem")

	body := eventBody("evt-idem", dev.DeviceID, "temperature", 4.5, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusOK {
		t.Fatalf("second submission status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if n := countTelemetryRows(t, pool, "evt-idem"); n != 1 {
		t.Errorf("rows for evt-idem = %d, want 1", n)
	}
}

// Same event_id, different payload: rejected, and the original is what
// survives - not silently overwritten by the conflicting value.
func TestIngestRejectsConflictingPayload(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-conflict")
	recordedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := postJSON(t, router, "/v1/telemetry", eventBody("evt-conflict", dev.DeviceID, "temperature", 4.5, recordedAt))
	if first.Code != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201; body=%s", first.Code, first.Body)
	}

	second := postJSON(t, router, "/v1/telemetry", eventBody("evt-conflict", dev.DeviceID, "temperature", 99.9, recordedAt))
	if second.Code != http.StatusConflict {
		t.Fatalf("conflicting submission status = %d, want 409; body=%s", second.Code, second.Body)
	}

	if n := countTelemetryRows(t, pool, "evt-conflict"); n != 1 {
		t.Errorf("rows for evt-conflict = %d, want 1", n)
	}
	if v := storedValue(t, pool, "evt-conflict"); v != 4.5 {
		t.Errorf("stored value = %v, want 4.5 (the original; 99.9 must not land)", v)
	}
}

// The idempotency guarantee under concurrency: 50 goroutines submit the same
// event_id with the same payload. Exactly one may create the row; every
// other request must see it as a duplicate, never as an error or a second
// row. This is the test that makes database-level idempotency a proven
// property rather than an assumption read off the code.
func TestIngestConcurrentIdenticalSubmissions(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-concurrent-dup")

	data, err := json.Marshal(eventBody("evt-concurrent-dup", dev.DeviceID, "temperature", 4.5,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	const n = 50
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = doPostJSON(router, "/v1/telemetry", data).Code
		}(i)
	}
	wg.Wait()

	var created, duplicate, other int
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			duplicate++
		default:
			other++
		}
	}
	if created != 1 {
		t.Errorf("created responses = %d, want exactly 1", created)
	}
	if duplicate != n-1 {
		t.Errorf("duplicate (200) responses = %d, want %d", duplicate, n-1)
	}
	if other != 0 {
		t.Errorf("%d responses were neither 201 nor 200", other)
	}
	if rows := countTelemetryRows(t, pool, "evt-concurrent-dup"); rows != 1 {
		t.Errorf("rows = %d, want exactly 1 despite %d concurrent submissions", rows, n)
	}
}

// Conflict detection under concurrency: 50 goroutines submit the same
// event_id split across two different payloads. Exactly one request may
// create the row; every request carrying the winning payload must see a
// duplicate, and every request carrying the other payload must see a
// conflict - never the reverse, and never a second row.
func TestIngestConcurrentConflictingSubmissions(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-concurrent-conflict")
	recordedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	bodyA, err := json.Marshal(eventBody("evt-concurrent-conflict", dev.DeviceID, "temperature", 1.0, recordedAt))
	if err != nil {
		t.Fatalf("marshal body A: %v", err)
	}
	bodyB, err := json.Marshal(eventBody("evt-concurrent-conflict", dev.DeviceID, "temperature", 2.0, recordedAt))
	if err != nil {
		t.Fatalf("marshal body B: %v", err)
	}

	const n = 50
	codes := make([]int, n)
	values := make([]float64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data, v := bodyA, 1.0
			if i%2 != 0 {
				data, v = bodyB, 2.0
			}
			values[i] = v
			codes[i] = doPostJSON(router, "/v1/telemetry", data).Code
		}(i)
	}
	wg.Wait()

	winner := storedValue(t, pool, "evt-concurrent-conflict")
	if winner != 1.0 && winner != 2.0 {
		t.Fatalf("stored value = %v, want 1 or 2", winner)
	}

	var created, agree, conflict, other int
	matching := 0
	for i, c := range codes {
		if values[i] == winner {
			matching++
		}
		switch {
		case c == http.StatusCreated:
			created++
		case c == http.StatusOK && values[i] == winner:
			agree++
		case c == http.StatusConflict && values[i] != winner:
			conflict++
		default:
			other++
		}
	}
	if created != 1 {
		t.Errorf("created responses = %d, want exactly 1", created)
	}
	if agree != matching-1 {
		t.Errorf("200 responses for the winning payload = %d, want %d", agree, matching-1)
	}
	if conflict != n-matching {
		t.Errorf("409 responses for the losing payload = %d, want %d", conflict, n-matching)
	}
	if other != 0 {
		t.Errorf("%d responses did not match the expected created/duplicate/conflict pattern for their payload", other)
	}
	if rows := countTelemetryRows(t, pool, "evt-concurrent-conflict"); rows != 1 {
		t.Errorf("rows = %d, want exactly 1", rows)
	}
}

func TestIngestRejectsInactiveDevice(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-inactive")
	if _, err := devices.Deactivate(context.Background(), dev.DeviceID); err != nil {
		t.Fatalf("deactivate device: %v", err)
	}

	rec := postJSON(t, router, "/v1/telemetry",
		eventBody("evt-inactive", dev.DeviceID, "temperature", 1.0, time.Now()))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
	}
	if n := countTelemetryRows(t, pool, "evt-inactive"); n != 0 {
		t.Errorf("rows = %d, want 0 (rejected write must not land)", n)
	}
}

func TestIngestRejectsUnknownDevice(t *testing.T) {
	pool := testdb.Connect(t)
	router, _, _ := newIntegrationRouter(pool)

	rec := postJSON(t, router, "/v1/telemetry",
		eventBody("evt-unknown", "does-not-exist", "temperature", 1.0, time.Now()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if n := countTelemetryRows(t, pool, "evt-unknown"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestListByDeviceReturnsRecentReadings(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-list")

	for i, v := range []float64{1, 2, 3} {
		id := fmt.Sprintf("evt-list-%d", i)
		body := eventBody(id, dev.DeviceID, "temperature", v, time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC))
		if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusCreated {
			t.Fatalf("seed submission %d status = %d, want 201; body=%s", i, rec.Code, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/"+dev.DeviceID+"/telemetry", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var decoded struct {
		Readings []telemetry.Reading `json:"readings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Readings) != 3 {
		t.Fatalf("got %d readings, want 3", len(decoded.Readings))
	}
}

func TestListByDeviceUnknownDevice(t *testing.T) {
	pool := testdb.Connect(t)
	router, _, _ := newIntegrationRouter(pool)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/does-not-exist/telemetry", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
