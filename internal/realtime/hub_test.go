package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPublishFansOutToEverySubscriber(t *testing.T) {
	h := NewHub()
	var channels []<-chan Message
	for i := 0; i < 3; i++ {
		ch, unsubscribe := h.Subscribe()
		defer unsubscribe()
		channels = append(channels, ch)
	}

	h.Publish("telemetry", "hello")

	for i, ch := range channels {
		select {
		case msg := <-ch:
			if msg.Type != "telemetry" || msg.Data != "hello" {
				t.Errorf("subscriber %d got %+v, want {telemetry hello}", i, msg)
			}
		default:
			t.Errorf("subscriber %d received nothing", i)
		}
	}
}

// Backpressure must never become the publisher's problem: a subscriber that
// never reads has its buffer filled, and the next Publish must still return
// promptly, with that subscriber dropped rather than blocking the caller -
// which, for every caller in this codebase, is a request-handling goroutine.
func TestBackpressureDropsSlowSubscriberWithoutBlockingPublish(t *testing.T) {
	h := NewHub()
	_, unsubscribe := h.Subscribe()
	defer unsubscribe()

	for i := 0; i < bufferSize; i++ {
		h.Publish("telemetry", i)
	}

	done := make(chan struct{})
	go func() {
		h.Publish("telemetry", "one more than the buffer holds")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}

	h.mu.Lock()
	n := len(h.subs)
	h.mu.Unlock()
	if n != 0 {
		t.Errorf("subscribers still registered = %d, want 0 (the full one should have been dropped)", n)
	}
}

// Waits for cond to become true rather than sampling once, since both the
// registry shrinking and a goroutine actually exiting happen asynchronously
// relative to the client closing its connection.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDisconnectUnregistersAndLeavesNoGoroutineBehind(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(h.ServeWS))
	defer srv.Close()

	baseline := runtime.NumGoroutine()

	conn, _, err := websocket.Dial(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	waitUntil(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.subs) == 1
	})

	conn.Close(websocket.StatusNormalClosure, "")

	waitUntil(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.subs) == 0
	})

	// ServeWS's read-loop goroutine should have exited along with the
	// connection; NumGoroutine can be momentarily stale right after exit, so
	// this also polls rather than sampling once.
	waitUntil(t, func() bool { return runtime.NumGoroutine() <= baseline })
}
