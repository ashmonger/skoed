package handlers

import (
	"net/http"

	"github.com/dblock/dblock/internal/config"
	"github.com/go-chi/chi/v5"
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

	writeJSON(w, http.StatusCreated, map[string]string{"domain": req.Domain})
}

// DeleteAllowlistEntry handles DELETE /api/v1/allowlist/{domain}.
func (h *Handler) DeleteAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
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
