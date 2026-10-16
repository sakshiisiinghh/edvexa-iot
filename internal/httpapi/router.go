// Package httpapi wires the HTTP routes and middleware.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/sakshiisiinghh/edvexa-iot/internal/device"
)

// Deps are the collaborators the routes need. A struct rather than a growing
// parameter list, since later phases add telemetry, thresholds and alerts.
type Deps struct {
	Logger  *slog.Logger
	Pinger  Pinger
	Devices *device.Repository
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	// requestLogger sits outside Recoverer so a panic is still logged, with the
	// 500 that Recoverer writes.
	r.Use(middleware.RequestID, middleware.RealIP, requestLogger(d.Logger), middleware.Recoverer)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.Pinger))

	dev := deviceHandler{repo: d.Devices, logger: d.Logger}
	r.Post("/v1/devices", dev.create)
	r.Get("/v1/devices", dev.list)
	r.Get("/v1/devices/{deviceID}", dev.get)
	r.Patch("/v1/devices/{deviceID}", dev.update)
	r.Post("/v1/devices/{deviceID}/deactivate", dev.deactivate)

	return r
}

// requestLogger replaces chi's middleware.Logger, which writes to the stdlib
// log package and would bypass slog.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration", time.Since(start),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
