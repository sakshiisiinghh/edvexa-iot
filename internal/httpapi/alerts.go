package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/sakshiisiinghh/edvexa-iot/internal/alert"
	"github.com/sakshiisiinghh/edvexa-iot/internal/realtime"
)

type alertHandler struct {
	alerts *alert.Repository
	hub    *realtime.Hub
	logger *slog.Logger
}

func (h alertHandler) list(w http.ResponseWriter, r *http.Request) {
	alerts, err := h.alerts.ListAlerts(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		internalError(h.logger, w, r, "list alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (h alertHandler) acknowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAlertID(w, r)
	if !ok {
		return
	}
	a, err := h.alerts.Acknowledge(r.Context(), id)
	h.respondTransition(w, r, a, err)
}

func (h alertHandler) resolve(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAlertID(w, r)
	if !ok {
		return
	}
	a, err := h.alerts.Resolve(r.Context(), id)
	h.respondTransition(w, r, a, err)
}

func (h alertHandler) respondTransition(w http.ResponseWriter, r *http.Request, a alert.Alert, err error) {
	switch {
	case errors.Is(err, alert.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, alert.ErrInvalidTransition):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		internalError(h.logger, w, r, "transition alert", err)
	default:
		// Acknowledge/Resolve already returned successfully by this point,
		// which under pgx means their UPDATE committed - so this can never
		// announce a transition that a later rollback undoes.
		if h.hub != nil {
			h.hub.Publish("alert", a)
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func parseAlertID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an integer")
		return 0, false
	}
	return id, true
}
