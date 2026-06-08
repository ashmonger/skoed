package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/config"
	"github.com/go-chi/chi/v5"
)

// ListSchedules handles GET /api/v1/schedules.
func (h *Handler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.app.GetCfg().Schedules)
}

// GetSchedule handles GET /api/v1/schedules/{id}.
func (h *Handler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	for _, s := range h.app.GetCfg().Schedules {
		if s.ID == id {
			writeJSON(w, http.StatusOK, s)
			return
		}
	}
	writeError(w, http.StatusNotFound, "schedule not found")
}

// CreateSchedule handles POST /api/v1/schedules.
func (h *Handler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	var s config.Schedule
	if !decodeJSON(w, r, &s) {
		return
	}
	if s.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertSchedule(s); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// UpdateSchedule handles PATCH /api/v1/schedules/{id}.
func (h *Handler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var patch struct {
		Name    *string             `json:"name"`
		Mode    *string             `json:"mode"`
		Windows []config.TimeWindow `json:"windows"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	cfg := h.app.GetCfg()
	var existing *config.Schedule
	for i := range cfg.Schedules {
		if cfg.Schedules[i].ID == id {
			existing = &cfg.Schedules[i]
			break
		}
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "schedule not found")
		return
	}
	updated := *existing
	if patch.Name != nil {
		updated.Name = *patch.Name
	}
	if patch.Mode != nil {
		updated.Mode = *patch.Mode
	}
	if patch.Windows != nil {
		updated.Windows = patch.Windows
	}
	if err := h.app.GetCluster().UpsertSchedule(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeleteSchedule handles DELETE /api/v1/schedules/{id}.
func (h *Handler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.app.GetCluster().DeleteSchedule(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddScheduleBinding handles POST /api/v1/schedules/{id}/bindings.
func (h *Handler) AddScheduleBinding(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		ProfileID   string `json:"profile_id"`
		BlocklistID string `json:"blocklist_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ProfileID == "" || body.BlocklistID == "" {
		writeError(w, http.StatusBadRequest, "profile_id and blocklist_id are required")
		return
	}
	b := config.ScheduleBinding{ScheduleID: id, ProfileID: body.ProfileID, BlocklistID: body.BlocklistID}
	if err := h.app.GetCluster().UpsertScheduleBinding(b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

// DeleteScheduleBinding handles DELETE /api/v1/schedules/{id}/bindings/{profile}/{blocklist}.
func (h *Handler) DeleteScheduleBinding(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	profile := chi.URLParam(r, "profile")
	blocklist := chi.URLParam(r, "blocklist")
	if err := h.app.GetCluster().DeleteScheduleBinding(id, profile, blocklist); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
