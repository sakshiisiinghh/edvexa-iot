//go:build integration

package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sakshiisiinghh/edvexa-iot/internal/realtime"
	"github.com/sakshiisiinghh/edvexa-iot/internal/testdb"
)

// awaitMessage returns the next message on ch, or fails the test if none
// arrives in time. Publish-after-commit means the message exists by the
// time Insert's HTTP call has already returned, so this should never need
// to wait long - a short timeout is a correctness bound, not a tuning knob.
func awaitMessage(t *testing.T, ch <-chan realtime.Message) realtime.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("no message published within the timeout")
		return realtime.Message{}
	}
}

func assertNoMessage(t *testing.T, ch <-chan realtime.Message) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("got an unexpected message: %+v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSuccessfulIngestPublishesExactlyOneTelemetryEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-1")

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-1", dev.DeviceID, "temperature", 4.5, time.Now()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	msg := awaitMessage(t, ch)
	if msg.Type != "telemetry" {
		t.Errorf("message type = %q, want telemetry", msg.Type)
	}

	assertNoMessage(t, ch)
}

// The duplicate-suppression property stated as a requirement for this
// phase: a resubmission that Insert reports as OutcomeDuplicate must not
// publish a second telemetry event, even though the HTTP call succeeds
// (200) both times.
func TestDuplicateSubmissionPublishesNoEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-2")

	body := eventBody("evt-ws-2", dev.DeviceID, "temperature", 4.5, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	if rec := postJSON(t, router, "/v1/telemetry", body); rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	assertNoMessage(t, ch)
}

// A conflicting resubmission is rejected outright, so it must not publish
// either - there is nothing new to announce.
func TestConflictingSubmissionPublishesNoEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-3")
	recordedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-3", dev.DeviceID, "temperature", 4.5, recordedAt)); rec.Code != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	if rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-3", dev.DeviceID, "temperature", 99.9, recordedAt)); rec.Code != http.StatusConflict {
		t.Fatalf("conflicting submission status = %d, want 409; body=%s", rec.Code, rec.Body)
	}

	assertNoMessage(t, ch)
}

// A request that is rejected before Insert is ever called (an inactive
// device) must not publish - this is "publish only after a commit", from
// the other direction: no commit ever happened.
func TestFailedIngestPublishesNoEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-4")
	if _, err := devices.Deactivate(t.Context(), dev.DeviceID); err != nil {
		t.Fatalf("deactivate device: %v", err)
	}

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-4", dev.DeviceID, "temperature", 4.5, time.Now()))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
	}

	assertNoMessage(t, ch)
}

// A threshold violation publishes both a telemetry event and an alert
// event from the same request, and the alert event carries the fields a
// dashboard needs without a further round trip.
func TestThresholdViolationPublishesBothTelemetryAndAlertEvents(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-5")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-5", dev.DeviceID, "temperature", 10.0, time.Now()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	first := awaitMessage(t, ch)
	second := awaitMessage(t, ch)

	types := map[string]bool{first.Type: true, second.Type: true}
	if !types["telemetry"] || !types["alert"] {
		t.Fatalf("got message types %q and %q, want one telemetry and one alert", first.Type, second.Type)
	}

	assertNoMessage(t, ch)
}

// A repeat violation against an already-unresolved alert must not publish
// a second alert-created event - the same duplicate-suppression property
// applied to alerts, proven here rather than assumed from Evaluate's
// return value.
func TestRepeatViolationPublishesNoSecondAlertEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-6")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))

	postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-6a", dev.DeviceID, "temperature", 10.0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	rec := postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-6b", dev.DeviceID, "temperature", 12.0, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("second violating reading status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	msg := awaitMessage(t, ch)
	if msg.Type != "telemetry" {
		t.Errorf("message type = %q, want telemetry", msg.Type)
	}
	// Exactly one more message (the telemetry event); no second alert event.
	assertNoMessage(t, ch)
}

func TestAcknowledgeAndResolvePublishAlertEvents(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-7")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-7", dev.DeviceID, "temperature", 10.0, time.Now()))
	id := singleAlertID(t, pool, dev.ID, "temperature")

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/acknowledge", id)); rec.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if msg := awaitMessage(t, ch); msg.Type != "alert" {
		t.Errorf("message type = %q, want alert", msg.Type)
	}

	if rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id)); rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if msg := awaitMessage(t, ch); msg.Type != "alert" {
		t.Errorf("message type = %q, want alert", msg.Type)
	}

	assertNoMessage(t, ch)
}

// A failed transition (acknowledging an already-resolved alert) must not
// publish - nothing committed.
func TestFailedTransitionPublishesNoEvent(t *testing.T) {
	pool := testdb.Connect(t)
	router, devices, _, hub := newIntegrationRouter(pool)
	dev := mustCreateDevice(t, devices, "dev-ws-8")
	postJSON(t, router, "/v1/thresholds", thresholdBody(dev.DeviceID, "temperature", nil, f64(8.0)))
	postJSON(t, router, "/v1/telemetry", eventBody("evt-ws-8", dev.DeviceID, "temperature", 10.0, time.Now()))
	id := singleAlertID(t, pool, dev.ID, "temperature")
	doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id))

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	rec := doAction(router, fmt.Sprintf("/v1/alerts/%d/resolve", id))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second resolve status = %d, want 409; body=%s", rec.Code, rec.Body)
	}

	assertNoMessage(t, ch)
}
