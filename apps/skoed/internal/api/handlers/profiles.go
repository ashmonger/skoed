package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/config"
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
	id := urlParam(r, "id")
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
	// M6.5 (TS-BlockDyn): block_dynamic_clients is not allowed on the
	// default profile — it must not create an implicit global block rule.
	if p.ID == "default" && p.BlockDynamicClients {
		writeError(w, http.StatusBadRequest, "the default profile cannot set block_dynamic_clients — create a dedicated profile (e.g. \"untrusted\") for this rule instead")
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
	id := urlParam(r, "id")
	var patch struct {
		Name                *string                         `json:"name"`
		Blocklists          []string                        `json:"blocklists"`
		Allowlist           []string                        `json:"allowlist"`
		SafeSearch          []string                        `json:"safesearch"`
		ClientIPs           []string                        `json:"client_ips"`
		ClientCIDRs         []string                        `json:"client_cidrs"`
		BlockDynamicClients *bool                           `json:"block_dynamic_clients"`
		// M33: per-profile block page overrides.
		BlockPage           *config.ProfileBlockPageConfig  `json:"block_page"`
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
	if patch.BlockDynamicClients != nil {
		// M6.5 (TS-BlockDyn): reject block_dynamic_clients=true on "default".
		if id == "default" && *patch.BlockDynamicClients {
			writeError(w, http.StatusBadRequest, "the default profile cannot set block_dynamic_clients — create a dedicated profile (e.g. \"untrusted\") for this rule instead")
			return
		}
		updated.BlockDynamicClients = *patch.BlockDynamicClients
	}
	if patch.BlockPage != nil {
		updated.BlockPage = patch.BlockPage
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
	id := urlParam(r, "id")
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
