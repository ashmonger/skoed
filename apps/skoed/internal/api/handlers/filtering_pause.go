package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/config"
)

// FilteringPauseApp is the subset of api.App required by the filtering-pause handlers.
type FilteringPauseApp interface {
	SetGlobalPause(resumesAt time.Time, reason string, profileIDs []string) error
	ClearGlobalPause() error
	SetProfilePause(id string, resumesAt time.Time, reason string) error
	ClearProfilePause(id string) error
	GetGlobalPause() *config.PauseState
	GetProfilePause(id string) *config.PauseState
	PauseMaxSeconds() int
	GetCfg() *config.Config
}

// pauseRequest is the body accepted by POST .../pause.
type pauseRequest struct {
	DurationSeconds int      `json:"duration_seconds"`
	Reason          string   `json:"reason,omitempty"`
	// ProfileIDs restricts the pause to specific profiles. Absent or empty means all profiles.
	ProfileIDs      []string `json:"profile_ids,omitempty"`
}

// pauseResponse is the body returned for an active pause.
type pauseResponse struct {
	Active     bool      `json:"active"`
	ResumesAt  time.Time `json:"resumes_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ProfileIDs []string  `json:"profile_ids,omitempty"`
}

func pauseStateResponse(ps *config.PauseState) pauseResponse {
	if ps == nil || !time.Now().Before(ps.ResumesAt) {
		return pauseResponse{Active: false}
	}
	return pauseResponse{Active: true, ResumesAt: ps.ResumesAt, Reason: ps.Reason, ProfileIDs: ps.ProfileIDs}
}

func validatePauseDuration(w http.ResponseWriter, n, maxSeconds int) bool {
	if maxSeconds == 0 {
		writeError(w, http.StatusBadRequest, "filtering pause feature is disabled")
		return false
	}
	if n <= 0 || n > maxSeconds {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("duration exceeds maximum (%d seconds)", maxSeconds))
		return false
	}
	return true
}

// FilteringPauseHandlers groups the M13 filtering-pause HTTP handlers.
type FilteringPauseHandlers struct {
	app FilteringPauseApp
}

// NewFilteringPauseHandlers constructs the handler set.
func NewFilteringPauseHandlers(app FilteringPauseApp) *FilteringPauseHandlers {
	return &FilteringPauseHandlers{app: app}
}

// GetGlobalPause handles GET /api/v1/filtering/pause.
func (h *FilteringPauseHandlers) GetGlobalPause(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, pauseStateResponse(h.app.GetGlobalPause()))
}

// SetGlobalPause handles POST /api/v1/filtering/pause.
func (h *FilteringPauseHandlers) SetGlobalPause(w http.ResponseWriter, r *http.Request) {
	var req pauseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validatePauseDuration(w, req.DurationSeconds, h.app.PauseMaxSeconds()) {
		return
	}
	// Validate that all supplied profile IDs exist.
	if len(req.ProfileIDs) > 0 {
		cfg := h.app.GetCfg()
		for _, pid := range req.ProfileIDs {
			if !profileExistsInCfg(cfg, pid) && pid != "default" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("profile not found: %s", pid))
				return
			}
		}
	}
	resumesAt := time.Now().Add(time.Duration(req.DurationSeconds) * time.Second)
	if err := h.app.SetGlobalPause(resumesAt, req.Reason, req.ProfileIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pauseResponse{Active: true, ResumesAt: resumesAt, Reason: req.Reason, ProfileIDs: req.ProfileIDs})
}

// ClearGlobalPause handles DELETE /api/v1/filtering/pause.
func (h *FilteringPauseHandlers) ClearGlobalPause(w http.ResponseWriter, r *http.Request) {
	ps := h.app.GetGlobalPause()
	if ps == nil || !time.Now().Before(ps.ResumesAt) {
		writeError(w, http.StatusNotFound, "no active global pause")
		return
	}
	if err := h.app.ClearGlobalPause(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetProfilePause handles GET /api/v1/profiles/{id}/pause.
func (h *FilteringPauseHandlers) GetProfilePause(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	if !profileExistsInCfg(h.app.GetCfg(), id) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	writeJSON(w, http.StatusOK, pauseStateResponse(h.app.GetProfilePause(id)))
}

// SetProfilePause handles POST /api/v1/profiles/{id}/pause.
func (h *FilteringPauseHandlers) SetProfilePause(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	if !profileExistsInCfg(h.app.GetCfg(), id) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	var req pauseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validatePauseDuration(w, req.DurationSeconds, h.app.PauseMaxSeconds()) {
		return
	}
	resumesAt := time.Now().Add(time.Duration(req.DurationSeconds) * time.Second)
	if err := h.app.SetProfilePause(id, resumesAt, req.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pauseResponse{Active: true, ResumesAt: resumesAt, Reason: req.Reason})
}

// ClearProfilePause handles DELETE /api/v1/profiles/{id}/pause.
func (h *FilteringPauseHandlers) ClearProfilePause(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	if !profileExistsInCfg(h.app.GetCfg(), id) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	ps := h.app.GetProfilePause(id)
	if ps == nil || !time.Now().Before(ps.ResumesAt) {
		writeError(w, http.StatusNotFound, "no active profile pause")
		return
	}
	if err := h.app.ClearProfilePause(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func profileExistsInCfg(cfg *config.Config, id string) bool {
	if cfg == nil {
		return false
	}
	for _, p := range cfg.Profiles {
		if p.ID == id {
			return true
		}
	}
	return false
}
