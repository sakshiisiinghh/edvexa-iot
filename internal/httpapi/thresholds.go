package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/sakshiisiinghh/edvexa-iot/internal/alert"
	"github.com/sakshiisiinghh/edvexa-iot/internal/device"
)

type thresholdHandler struct {
	devices    *device.Repository
	thresholds *alert.Repository
	logger     *slog.Logger
}

func (h thresholdHandler) upsert(w http.ResponseWriter, r *http.Request) {
	var in alert.ThresholdInput
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

	th, err := h.thresholds.UpsertThreshold(r.Context(), in.DeviceID, dev.ID, in)
	if err != nil {
		internalError(h.logger, w, r, "upsert threshold", err)
		return
	}
	writeJSON(w, http.StatusOK, th)
}

func (h thresholdHandler) list(w http.ResponseWriter, r *http.Request) {
	thresholds, err := h.thresholds.ListThresholds(r.Context())
	if err != nil {
		internalError(h.logger, w, r, "list thresholds", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": thresholds})
}
