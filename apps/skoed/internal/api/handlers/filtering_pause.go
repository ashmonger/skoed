package handlers

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
)

// FilteringPauseApp is the subset of api.App required by the filtering-pause handlers.
type FilteringPauseApp interface {
	SetGlobalPause(resumesAt time.Time, reason string, profileIDs []string) error
	ClearGlobalPause() error
	SetProfilePause(id string, resumesAt time.Time, reason string, clientIPs []string) error
	ClearProfilePause(id string) error
	GetGlobalPause() *config.PauseState
	GetProfilePause(id string) *config.PauseState
	GetPauseHistory(profileID string) ([]config.PauseHistoryEntry, error)
	PauseMaxSeconds() int
	GetCfg() *config.Config
	GetNewDynamicClients() ([]cluster.NewDynamicClientEntry, error)
	DismissNewDynamicClient(clientIP string) error
}

// pauseRequest is the body accepted by POST .../pause.
type pauseRequest struct {
	DurationSeconds int      `json:"duration_seconds"`
	Reason          string   `json:"reason,omitempty"`
	// ProfileIDs restricts the pause to specific profiles. Absent or empty means all profiles.
	ProfileIDs      []string `json:"profile_ids,omitempty"`
	// ClientIPs (M35) restricts a per-profile pause to specific client IPs only.
	ClientIPs       []string `json:"client_ips,omitempty"`
}

// pauseResponse is the body returned for an active pause.
type pauseResponse struct {
	Active     bool      `json:"active"`
	ResumesAt  time.Time `json:"resumes_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ProfileIDs []string  `json:"profile_ids,omitempty"`
	ClientIPs  []string  `json:"client_ips,omitempty"`
}

func pauseStateResponse(ps *config.PauseState) pauseResponse {
	if ps == nil || !time.Now().Before(ps.ResumesAt) {
		return pauseResponse{Active: false}
	}
	return pauseResponse{Active: true, ResumesAt: ps.ResumesAt, Reason: ps.Reason, ProfileIDs: ps.ProfileIDs, ClientIPs: ps.ClientIPs}
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
	// Validate per-client IPs up front. An unparseable entry that silently
	// dropped through would scope the pause to nobody in the engine (fail
	// closed), but rejecting here gives the caller a clear error instead of a
	// pause that quietly protects no one.
	for _, ip := range req.ClientIPs {
		if net.ParseIP(ip) == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid client_ip: %q", ip))
			return
		}
	}
	resumesAt := time.Now().Add(time.Duration(req.DurationSeconds) * time.Second)
	if err := h.app.SetProfilePause(id, resumesAt, req.Reason, req.ClientIPs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pauseResponse{Active: true, ResumesAt: resumesAt, Reason: req.Reason, ClientIPs: req.ClientIPs})
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

// GetPauseHistory handles GET /api/v1/profiles/{id}/pause/history.
func (h *FilteringPauseHandlers) GetPauseHistory(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	if !profileExistsInCfg(h.app.GetCfg(), id) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	entries, err := h.app.GetPauseHistory(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []config.PauseHistoryEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// GetNewDynamicClients handles GET /api/v1/clients/new-dynamic.
func (h *FilteringPauseHandlers) GetNewDynamicClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.app.GetNewDynamicClients()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if clients == nil {
		clients = []cluster.NewDynamicClientEntry{}
	}
	writeJSON(w, http.StatusOK, clients)
}

// DismissNewDynamicClient handles POST /api/v1/clients/new-dynamic/dismiss.
func (h *FilteringPauseHandlers) DismissNewDynamicClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientIP string `json:"client_ip"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ClientIP == "" {
		writeError(w, http.StatusBadRequest, "client_ip is required")
		return
	}
	if err := h.app.DismissNewDynamicClient(req.ClientIP); err != nil {
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
