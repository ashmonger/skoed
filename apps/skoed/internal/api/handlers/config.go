package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/config"
)

// ExportConfig handles GET /api/v1/config/export.
// It streams the config as a gzip-compressed tar archive.
func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="skoed-config.tar.gz"`)

	if err := config.Export(cfg, w); err != nil {
		// Headers already sent; we can't change the status code at this point.
		_, _ = w.Write([]byte("\nexport error: " + err.Error()))
	}
}

// ImportConfig handles POST /api/v1/config/import.
// Expects a multipart form with an "archive" file field containing a tar.gz archive.
func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeError(w, http.StatusBadRequest, "archive field missing or invalid: "+err.Error())
		return
	}
	defer file.Close()

	newCfg, err := config.Import(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "import failed: "+err.Error())
		return
	}

	// Apply the new config atomically, preserving node-local settings that
	// must not be overwritten by an imported archive (listen ports, API port,
	// and admin credentials — credentials are a per-node secret; importing a
	// backup must never lock the current admin out of the node).
	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		newCfg.DNS.Listen = cfg.DNS.Listen
		newCfg.API.Port = cfg.API.Port
		newCfg.Auth = cfg.Auth
		*cfg = *newCfg
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	// Rebuild filter engine and DNS server from the new config.
	if err := h.app.RebuildFilter(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild filter: "+err.Error())
		return
	}
	if err := h.app.RebuildDNSFromCfg(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild dns: "+err.Error())
		return
	}

	h.app.GetQueryLog().SetMaxEntries(newCfg.QueryLog.MaxEntries)

	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}
