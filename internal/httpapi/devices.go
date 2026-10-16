package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sakshiisiinghh/edvexa-iot/internal/device"
)

// maxBodyBytes caps request bodies. ReadTimeout bounds how long a client may
// take, but not how much it may send.
const maxBodyBytes = 1 << 20

type deviceHandler struct {
	repo   *device.Repository
	logger *slog.Logger
}

func (h deviceHandler) create(w http.ResponseWriter, r *http.Request) {
	var in device.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}

	d, err := h.repo.Create(r.Context(), in)
	switch {
	case errors.Is(err, device.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, device.ErrDuplicate):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		h.internal(r, w, "create device", err)
	default:
		writeJSON(w, http.StatusCreated, d)
	}
}

func (h deviceHandler) list(w http.ResponseWriter, r *http.Request) {
	devices, err := h.repo.List(r.Context())
	if err != nil {
		h.internal(r, w, "list devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (h deviceHandler) get(w http.ResponseWriter, r *http.Request) {
	d, err := h.repo.GetByDeviceID(r.Context(), chi.URLParam(r, "deviceID"))
	switch {
	case errors.Is(err, device.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		h.internal(r, w, "get device", err)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

func (h deviceHandler) update(w http.ResponseWriter, r *http.Request) {
	var in device.UpdateInput
	if !decodeJSON(w, r, &in) {
		return
	}

	d, err := h.repo.Update(r.Context(), chi.URLParam(r, "deviceID"), in)
	switch {
	case errors.Is(err, device.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, device.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		h.internal(r, w, "update device", err)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

func (h deviceHandler) deactivate(w http.ResponseWriter, r *http.Request) {
	d, err := h.repo.Deactivate(r.Context(), chi.URLParam(r, "deviceID"))
	switch {
	case errors.Is(err, device.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		h.internal(r, w, "deactivate device", err)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

// internal logs the cause and tells the client nothing about it.
func (h deviceHandler) internal(r *http.Request, w http.ResponseWriter, op string, err error) {
	h.logger.ErrorContext(r.Context(), op+" failed", "error", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
