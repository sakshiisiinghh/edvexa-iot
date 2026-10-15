package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is the part of *pgxpool.Pool that readiness needs, so the handler can
// be tested without a database.
type Pinger interface {
	Ping(context.Context) error
}

const readyTimeout = 2 * time.Second

// handleHealthz is liveness: it must not touch the database, or a PostgreSQL
// blip would make an orchestrator restart a healthy process.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleReadyz(p Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := p.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unready",
				"error":  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so a write failure here (client gone)
	// cannot be reported to the client or corrected.
	_ = json.NewEncoder(w).Encode(body)
}
