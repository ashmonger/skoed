package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/config"
	"github.com/go-chi/chi/v5"
)

// ListProfiles handles GET /api/v1/profiles.
func (h *Handler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	if cfg == nil {
		writeJSON(w, http.StatusOK, []config.Profile{})
		return
	}
	writeJSON(w, http.StatusOK, cfg.Profiles)
}

// GetProfile handles GET /api/v1/profiles/{id}.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cfg := h.app.GetCfg()
	for _, p := range cfg.Profiles {
		if p.ID == id {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	writeError(w, http.StatusNotFound, "profile not found")
}

// CreateProfile handles POST /api/v1/profiles.
func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	var p config.Profile
	if !decodeJSON(w, r, &p) {
		return
	}
	if p.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertProfile(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// UpdateProfile handles PATCH /api/v1/profiles/{id}. Body is a partial
// Profile; only present fields override.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var patch struct {
		Name        *string  `json:"name"`
		Blocklists  []string `json:"blocklists"`
		Allowlist   []string `json:"allowlist"`
		SafeSearch  []string `json:"safesearch"`
		ClientIPs   []string `json:"client_ips"`
		ClientCIDRs []string `json:"client_cidrs"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	cfg := h.app.GetCfg()
	var existing *config.Profile
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == id {
			existing = &cfg.Profiles[i]
			break
		}
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	updated := *existing
	if patch.Name != nil {
		updated.Name = *patch.Name
	}
	if patch.Blocklists != nil {
		updated.Blocklists = patch.Blocklists
	}
	if patch.Allowlist != nil {
		updated.Allowlist = patch.Allowlist
	}
	if patch.SafeSearch != nil {
		updated.SafeSearch = patch.SafeSearch
	}
	if patch.ClientIPs != nil {
		updated.ClientIPs = patch.ClientIPs
	}
	if patch.ClientCIDRs != nil {
		updated.ClientCIDRs = patch.ClientCIDRs
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeleteProfile handles DELETE /api/v1/profiles/{id}.
func (h *Handler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "default" {
		writeError(w, http.StatusConflict, "cannot delete the reserved default profile")
		return
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().DeleteProfile(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
