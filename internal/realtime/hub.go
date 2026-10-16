// Package realtime fans telemetry and alert events out to WebSocket
// subscribers, such as a live dashboard.
package realtime

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// bufferSize bounds how many undelivered messages a subscriber may
// accumulate. A slow dashboard must never be able to apply backpressure to
// request-handling goroutines, so it is intentionally small, and a full
// buffer leads to the subscriber being dropped, not Publish blocking.
const bufferSize = 16

const writeTimeout = 5 * time.Second

// Message is what a subscriber receives. Type names which kind of update
// this is ("telemetry" or "alert"); Data is the JSON-shaped payload. The
// API documentation is where its exact shape is written down for consumers.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type subscriber struct {
	ch chan Message
}

// Hub fans a Message out to every currently-registered subscriber. The zero
// value is not usable; construct with NewHub.
type Hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[*subscriber]struct{})}
}

// Subscribe registers a new listener and returns a channel of messages
// published from this point on, plus a function to stop listening. The
// caller must call that function when done, or this subscriber is never
// removed from the registry. ServeWS is the one built-in consumer; tests
// and any other in-process consumer use Subscribe directly.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	s := &subscriber{ch: make(chan Message, bufferSize)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s.ch, func() { h.unregister(s) }
}

func (h *Hub) unregister(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Publish fans a message out to every subscriber without blocking on any of
// them. A subscriber whose buffer is already full is dropped and its
// channel closed, rather than letting one slow reader apply backpressure to
// the caller - which, for every caller in this codebase, is a
// request-handling goroutine that must not stall on a dashboard that is not
// keeping up.
func (h *Hub) Publish(eventType string, data any) {
	msg := Message{Type: eventType, Data: data}

	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.ch <- msg:
		default:
			close(s.ch)
			delete(h.subs, s)
		}
	}
}

// ServeWS upgrades the request to a WebSocket and streams published
// messages to it until the client disconnects, the connection is dropped
// for being too slow, or a write fails.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote an appropriate HTTP error response.
	}
	defer conn.CloseNow()

	ch, unsubscribe := h.Subscribe()
	defer unsubscribe()

	// This connection only ever sends to the client; it is never read from
	// for data. A WebSocket connection must still be read from continuously
	// for control frames (ping, close) to be handled, so this goroutine's
	// only job is to notice the client going away.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-closed:
			return
		case msg, ok := <-ch:
			if !ok {
				conn.Close(websocket.StatusPolicyViolation, "message buffer full")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), writeTimeout)
			err := wsjson.Write(ctx, conn, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
