package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/sakshiisiinghh/edvexa-iot/internal/alert"
	"github.com/sakshiisiinghh/edvexa-iot/internal/device"
	"github.com/sakshiisiinghh/edvexa-iot/internal/realtime"
	"github.com/sakshiisiinghh/edvexa-iot/internal/telemetry"
)

const (
	defaultTelemetryLimit = 50
	maxTelemetryLimit     = 500
)

type telemetryHandler struct {
	devices   *device.Repository
	telemetry *telemetry.Repository
	hub       *realtime.Hub
	logger    *slog.Logger
}

func (h telemetryHandler) ingest(w http.ResponseWriter, r *http.Request) {
	var in telemetry.IngestInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	dev, err := h.devices.GetByDeviceID(r.Context(), in.DeviceID)
	switch {
	case errors.Is(err, device.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		internalError(h.logger, w, r, "resolve device", err)
		return
	}
	// Deactivation has to mean something: a deactivated device's readings are
	// rejected rather than silently accepted, distinct from an unknown
	// device, which is 404.
	if !dev.Active() {
		writeError(w, http.StatusUnprocessableEntity,
			"device "+dev.DeviceID+" is not active (status: "+dev.Status+")")
		return
	}

	result, err := h.telemetry.Insert(r.Context(), telemetry.Event{
		EventID:    in.EventID,
		DeviceID:   in.DeviceID,
		DevicePK:   dev.ID,
		Metric:     in.Metric,
		Value:      in.Value,
		RecordedAt: in.RecordedAt,
	})
	if err != nil {
		internalError(h.logger, w, r, "ingest telemetry", err)
		return
	}

	switch result.Outcome {
	case telemetry.OutcomeCreated:
		// Insert has already returned successfully, which only happens after
		// its transaction committed - so publishing here can never announce a
		// reading that a later rollback undoes. Only this outcome publishes:
		// a duplicate resubmission must not emit a second telemetry event.
		if h.hub != nil {
			h.hub.Publish("telemetry", eventResponse(result.Event))
			if result.AlertCreated {
				h.hub.Publish("alert", map[string]any{
					"id":               result.AlertID,
					"device_id":        in.DeviceID,
					"metric":           in.Metric,
					"triggering_value": in.Value,
					"status":           alert.StatusActive,
				})
			}
		}
		writeJSON(w, http.StatusCreated, eventResponse(result.Event))
	case telemetry.OutcomeDuplicate:
		writeJSON(w, http.StatusOK, eventResponse(result.Event))
	case telemetry.OutcomeConflict:
		writeError(w, http.StatusConflict,
			"event_id "+in.EventID+" was already used with a different payload")
	}
}

func (h telemetryHandler) listByDevice(w http.ResponseWriter, r *http.Request) {
	dev, err := h.devices.GetByDeviceID(r.Context(), chi.URLParam(r, "deviceID"))
	switch {
	case errors.Is(err, device.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		internalError(h.logger, w, r, "resolve device", err)
		return
	}

	limit := defaultTelemetryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxTelemetryLimit)
	}

	readings, err := h.telemetry.ListByDevice(r.Context(), dev.ID, limit)
	if err != nil {
		internalError(h.logger, w, r, "list telemetry", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"readings": readings})
}

func eventResponse(e telemetry.Event) map[string]any {
	return map[string]any{
		"event_id":    e.EventID,
		"device_id":   e.DeviceID,
		"metric":      e.Metric,
		"value":       e.Value,
		"recorded_at": e.RecordedAt,
	}
}
