package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/config"
)

// GetAllowlist handles GET /api/v1/allowlist.
func (h *Handler) GetAllowlist(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	list := cfg.Filtering.Allowlist
	if list == nil {
		list = []string{}
	}
	writeJSON(w, http.StatusOK, list)
}

// addAllowlistRequest is the body accepted by POST /api/v1/allowlist.
type addAllowlistRequest struct {
	Domain string `json:"domain"`
}

// AddAllowlistEntry handles POST /api/v1/allowlist.
func (h *Handler) AddAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	var req addAllowlistRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}

	conflict := false
	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for _, d := range cfg.Filtering.Allowlist {
			if d == req.Domain {
				conflict = true
				return nil
			}
		}
		cfg.Filtering.Allowlist = append(cfg.Filtering.Allowlist, req.Domain)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if conflict {
		writeError(w, http.StatusConflict, "domain already in allowlist")
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if err := h.app.RebuildFilter(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild filter: "+err.Error())
		return
	}
	// M4.7 — surgical cache invalidation: drop the cached response for
	// this name so the next query sees the allowlist decision, not the
	// stale upstream answer.
	if cache := h.app.GetDNSCache(); cache != nil {
		cache.PurgeDomain(req.Domain)
	}

	writeJSON(w, http.StatusCreated, map[string]string{"domain": req.Domain})
}

// GetProfileAllowlist handles GET /api/v1/profiles/{id}/allowlist.
func (h *Handler) GetProfileAllowlist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, p := range cfg.Profiles {
		if p.ID == id {
			list := p.Allowlist
			if list == nil {
				list = []string{}
			}
			writeJSON(w, http.StatusOK, list)
			return
		}
	}
	writeError(w, http.StatusNotFound, "profile not found")
}

// AddProfileAllowlistEntry handles POST /api/v1/profiles/{id}/allowlist.
func (h *Handler) AddProfileAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var req addAllowlistRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
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
	for _, d := range existing.Allowlist {
		if d == req.Domain {
			writeError(w, http.StatusConflict, "domain already in profile allowlist")
			return
		}
	}
	updated := *existing
	updated.Allowlist = append(updated.Allowlist, req.Domain)
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cache := h.app.GetDNSCache(); cache != nil {
		cache.PurgeDomain(req.Domain)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"domain": req.Domain})
}

// DeleteProfileAllowlistEntry handles DELETE /api/v1/profiles/{id}/allowlist/{domain}.
func (h *Handler) DeleteProfileAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	domain := urlParam(r, "domain")
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
	found := false
	updated := *existing
	for i, d := range updated.Allowlist {
		if d == domain {
			updated.Allowlist = append(updated.Allowlist[:i], updated.Allowlist[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "domain not found in profile allowlist")
		return
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteAllowlistEntry handles DELETE /api/v1/allowlist/{domain}.
func (h *Handler) DeleteAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	domain := urlParam(r, "domain")
	found := false

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i, d := range cfg.Filtering.Allowlist {
			if d == domain {
				cfg.Filtering.Allowlist = append(
					cfg.Filtering.Allowlist[:i],
					cfg.Filtering.Allowlist[i+1:]...,
				)
				found = true
				return nil
			}
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !found {
		writeError(w, http.StatusNotFound, "domain not found in allowlist")
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if err := h.app.RebuildFilter(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild filter: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
