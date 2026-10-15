// Package httpapi wires the HTTP routes and middleware.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(logger *slog.Logger, p Pinger) http.Handler {
	r := chi.NewRouter()
	// requestLogger sits outside Recoverer so a panic is still logged, with the
	// 500 that Recoverer writes.
	r.Use(middleware.RequestID, middleware.RealIP, requestLogger(logger), middleware.Recoverer)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(p))
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
