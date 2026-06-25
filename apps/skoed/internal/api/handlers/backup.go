// Handlers for M31 Backup Hardening.
//
// Routes (all authenticated):
//   POST /api/v1/config/export             — export with optional passphrase
//   POST /api/v1/config/import             — import with optional passphrase
//   PUT  /api/v1/settings/backup           — configure scheduled backup
//   GET  /api/v1/config/backups            — list stored backups
//   GET  /api/v1/config/backups/{id}/download — download a stored backup
//   POST /api/v1/config/backups/trigger    — force one backup cycle
//   POST /api/v1/config/diff              — diff two archives
package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/skoed/skoed/internal/config"
)

// BackupSchedulerIface is the subset of *config.BackupScheduler needed here.
type BackupSchedulerIface interface {
	TriggerOnce() (bool, error)
}

// BackupHandlers bundles HTTP handlers for the M31 backup feature.
type BackupHandlers struct {
	app       AppState
	scheduler BackupSchedulerIface
}

// NewBackupHandlers creates a BackupHandlers.
func NewBackupHandlers(app AppState, scheduler BackupSchedulerIface) *BackupHandlers {
	return &BackupHandlers{app: app, scheduler: scheduler}
}

// ExportConfigPost handles POST /api/v1/config/export.
// Accepts an optional JSON body {"passphrase":"..."}.
// When passphrase is supplied the response is age-encrypted (application/octet-stream).
func (h *Handler) ExportConfigPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

	cfg := h.app.GetCfg()
	if req.Passphrase == "" {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="skoed-config.tar.gz"`)
		if err := config.Export(cfg, w); err != nil {
			_, _ = w.Write([]byte("\nexport error: " + err.Error()))
		}
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="skoed-config.age"`)
	if err := config.ExportWithPassphrase(cfg, w, req.Passphrase); err != nil {
		_, _ = w.Write([]byte("\nexport error: " + err.Error()))
	}
}

// ImportConfigWithPassphrase handles POST /api/v1/config/import.
// Accepts multipart form with "archive" file and optional "passphrase" text field.
func (h *Handler) ImportConfigWithPassphrase(w http.ResponseWriter, r *http.Request) {
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

	passphrase := r.FormValue("passphrase")
	newCfg, importErr := config.ImportWithPassphrase(file, passphrase)
	if importErr != nil {
		status := http.StatusBadRequest
		if strings.Contains(importErr.Error(), "invalid passphrase") ||
			strings.Contains(importErr.Error(), "corrupted archive") {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, importErr.Error())
		return
	}

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

// ─── BackupHandlers methods ──────────────────────────────────────────────────

// PutBackupSettings handles PUT /api/v1/settings/backup.
func (h *BackupHandlers) PutBackupSettings(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	var req config.BackupConfig
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := cl.SetBackupConfig(req); err != nil {
		writeError(w, http.StatusInternalServerError, "set backup config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// ListBackups handles GET /api/v1/config/backups.
func (h *BackupHandlers) ListBackups(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	entries, err := cl.Store().BackupEntries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list backups: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"backups": entries})
}

// DownloadBackup handles GET /api/v1/config/backups/{id}/download.
func (h *BackupHandlers) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	backupDir := filepath.Join(h.app.Dir(), "backups")
	for _, ext := range []string{".tar.gz", ".age"} {
		path := filepath.Join(backupDir, id+ext)
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "open backup: "+err.Error())
			return
		}
		defer f.Close()
		contentType := "application/gzip"
		if ext == ".age" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+ext+`"`)
		io.Copy(w, f) //nolint:errcheck
		return
	}
	writeError(w, http.StatusNotFound, "backup not found")
}

// TriggerBackup handles POST /api/v1/config/backups/trigger.
func (h *BackupHandlers) TriggerBackup(w http.ResponseWriter, r *http.Request) {
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "backup scheduler not available")
		return
	}
	created, err := h.scheduler.TriggerOnce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "trigger backup: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"created": created})
}

// DiffBackups handles POST /api/v1/config/diff.
// Accepts multipart form with "archive_a" and "archive_b" file fields.
func (h *BackupHandlers) DiffBackups(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}
	dataA, err := readFormFile(r, "archive_a")
	if err != nil {
		writeError(w, http.StatusBadRequest, "archive_a: "+err.Error())
		return
	}
	dataB, err := readFormFile(r, "archive_b")
	if err != nil {
		writeError(w, http.StatusBadRequest, "archive_b: "+err.Error())
		return
	}
	diff, err := config.DiffArchives(bytes.NewReader(dataA), bytes.NewReader(dataB))
	if err != nil {
		writeError(w, http.StatusBadRequest, "diff failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// readFormFile reads an entire multipart file field into memory.
func readFormFile(r *http.Request, field string) ([]byte, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
