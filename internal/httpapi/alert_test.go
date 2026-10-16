//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakshiisiinghh/edvexa-iot/internal/alert"
	"github.com/sakshiisiinghh/edvexa-iot/internal/testdb"
)

func f64(v float64) *float64 { return &v }

func thresholdBody(deviceID, metric string, minValue, maxValue *float64) map[string]any {
	body := map[string]any{"device_id": deviceID, "metric": metric}
	if minValue != nil {
		body["min_value"] = *minValue
	}
	if maxValue != nil {
		body["max_value"] = *maxValue
	}
	return body
}

func doAction(router http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func countAlerts(t *testing.T, pool *pgxpool.Pool, devicePK, metric string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM alerts WHERE device_pk = $1 AND metric = $2", devicePK, metric,
	).Scan(&n); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	return n
}

func countUnresolvedAlerts(t *testing.T, pool *pgxpool.Pool, devicePK, metric string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM alerts WHERE device_pk = $1 AND metric = $2 AND status IN ('active','acknowledged')",
		devicePK, metric,
	).Scan(&n); err != nil {
		t.Fatalf("count unresolved alerts: %v", err)
	}
	return n
}

func singleAlertID(t *testing.T, pool *pgxpool.Pool, devicePK, metric string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		"SELECT id FROM alerts WHERE device_pk = $1 AND metric = $2 AND status IN ('active','acknowledged')",
		devicePK, metric,
	).Scan(&id); err != nil {
		t.Fatalf("get unresolved alert id: %v", err)
	}
	return id
}

func alertStatus(t *testing.T, pool *pgxpool.Pool, devicePK, metric string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		"SELECT status FROM alerts WHERE device_pk = $1 AND metric = $2 AND status IN ('active','acknowledged')",
		devicePK, metric,
	).Scan(&status); err != nil {
		t.Fatalf("get alert status: %v", err)
	}
	return status
}

func countTelemetryForDevice(t *testing.T, pool *pgxpool.Pool, devicePK string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM telemetry_events WHERE device_pk = $1", devicePK,
	).Scan(&n); err != nil {
		t.Fatalf("count telemetry for device: %v", err)
	}
	return n
}

func TestThresholdViolationCreatesOneAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-1")
	if rec := postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0))); rec.Code != http.StatusOK {
		t.Fatalf("create threshold status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-1", dev.DeviceID, "temperature", 10.0, time.Now()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("alerts = %d, want 1", n)
	}
}

func TestIdenticalReplayCreatesNoSecondAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-2")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	body := eventBody("evt-alert-2", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusCreated {
		t.Fatalf("first ingest status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("alerts = %d, want 1 - a duplicate reading must not raise a second alert", n)
	}
}

func TestNewViolationWhileActiveCreatesNoSecondAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-3")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-3a", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))); rec.Code != http.StatusCreated {
		t.Fatalf("first violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-3b", dev.DeviceID, "temperature", 12.0, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC))); rec.Code != http.StatusCreated {
		t.Fatalf("second violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	if n := countTelemetryForDevice(t, pool, dev.ID); n != 2 {
		t.Errorf("telemetry rows = %d, want 2", n)
	}
	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("alerts = %d, want 1", n)
	}
}

func TestNewViolationWhileAcknowledgedCreatesNoSecondAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-4")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-4a", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))); rec.Code != http.StatusCreated {
		t.Fatalf("first violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	id := singleAlertID(t, pool, dev.ID, "temperature")
	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-4b", dev.DeviceID, "temperature", 12.0, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC))); rec.Code != http.StatusCreated {
		t.Fatalf("second violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	if n := countTelemetryForDevice(t, pool, dev.ID); n != 2 {
		t.Errorf("telemetry rows = %d, want 2", n)
	}
	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("alerts = %d, want 1 - this is what the widened predicate buys", n)
	}
	if status := alertStatus(t, pool, dev.ID, "temperature"); status != alert.StatusAcknowledged {
		t.Errorf("alert status = %q, want %q (unchanged by the second violation)", status, alert.StatusAcknowledged)
	}
}

// The regression test for the defect caught in plan review: losing the race
// to create an alert must never roll back the telemetry write it was
// evaluating for. Both readings must persist, and exactly one alert exists.
func TestConcurrentViolatingReadingsCreateExactlyOneAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-concurrent")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	dataA, err := json.Marshal(eventBody("evt-alert-concurrent-a", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("marshal body A: %v", err)
	}
	dataB, err := json.Marshal(eventBody("evt-alert-concurrent-b", dev.DeviceID, "temperature", 12.0, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("marshal body B: %v", err)
	}

	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); codes[0] = doPostJSON(router, "/v1/telemetry", dataA).Code }()
	go func() { defer wg.Done(); codes[1] = doPostJSON(router, "/v1/telemetry", dataB).Code }()
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusCreated {
			t.Errorf("response %d status = %d, want 201", i, c)
		}
	}
	if n := countTelemetryForDevice(t, pool, dev.ID); n != 2 {
		t.Errorf("telemetry rows = %d, want 2 - a reading must not be lost to the alert race", n)
	}
	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("alerts = %d, want exactly 1 despite 2 concurrent violations", n)
	}
}

func TestAcknowledgeThenResolveAllowsNewAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-6")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-6a", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	id := singleAlertID(t, pool, dev.ID, "temperature")

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id)); rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if n := countUnresolvedAlerts(t, pool, dev.ID, "temperature"); n != 0 {
		t.Errorf("unresolved alerts after resolve = %d, want 0", n)
	}

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-6b", dev.DeviceID, "temperature", 12.0, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC))); rec.Code != http.StatusCreated {
		t.Fatalf("new violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 2 {
		t.Errorf("total alerts = %d, want 2 (one resolved, one new)", n)
	}
	if n := countUnresolvedAlerts(t, pool, dev.ID, "temperature"); n != 1 {
		t.Errorf("unresolved alerts = %d, want 1 (the new one)", n)
	}
}

func TestResolveAlreadyResolvedConflicts(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-7")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-7", dev.DeviceID, "temperature", 10.0, time.Now()))
	id := singleAlertID(t, pool, dev.ID, "temperature")

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id)); rec.Code != http.StatusOK {
		t.Fatalf("first resolve status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id)); rec.Code != http.StatusConflict {
		t.Fatalf("second resolve status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

func TestAcknowledgeResolvedConflicts(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-8")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-8", dev.DeviceID, "temperature", 10.0, time.Now()))
	id := singleAlertID(t, pool, dev.ID, "temperature")

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id)); rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusConflict {
		t.Fatalf("acknowledge-after-resolve status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

func TestAcknowledgeTwiceConflicts(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-9")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-9", dev.DeviceID, "temperature", 10.0, time.Now()))
	id := singleAlertID(t, pool, dev.ID, "temperature")

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusOK {
		t.Fatalf("first acknowledge status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusConflict {
		t.Fatalf("second acknowledge status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

func TestAcknowledgeUnknownAlertNotFound(t *testing.T) {
	pool := testdb.Connect(t)
	router, _, _, _ := newIntegrationRouter(pool)

	rec := doAction(router, "/v1/alerts/999999/acknowledge")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

func TestThresholdRejectsMissingBounds(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-threshold-missing")

	rec := postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
	}
}

func TestThresholdRejectsInvertedBounds(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-threshold-inverted")

	rec := postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "voltage", f64(12.0), f64(5.0)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
	}
}

func TestReadingWithinBoundsCreatesNoAlert(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-within")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-within", dev.DeviceID, "temperature", 4.5, time.Now()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	if n := countAlerts(t, pool, dev.ID, "temperature"); n != 0 {
		t.Errorf("alerts = %d, want 0", n)
	}
}

func TestListAlertsFiltersByStatus(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-alert-list")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-alert-list", dev.DeviceID, "temperature", 10.0, time.Now()))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/alerts?status=active", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var active struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(active.Alerts) != 1 {
		t.Fatalf("active alerts = %d, want 1", len(active.Alerts))
	}

	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/v1/alerts?status=resolved", nil))
	var resolved struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resolved.Alerts) != 0 {
		t.Fatalf("resolved alerts = %d, want 0", len(resolved.Alerts))
	}
}

func TestListThresholdsReturnsUpserted(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, _ := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-threshold-list")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/thresholds", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var decoded struct {
		Thresholds []alert.Threshold `json:"thresholds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Thresholds) != 1 {
		t.Fatalf("thresholds = %d, want 1", len(decoded.Thresholds))
	}
}
