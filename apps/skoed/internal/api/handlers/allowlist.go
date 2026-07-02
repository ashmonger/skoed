package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/cluster"
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

// GetAllowlistEntries handles GET /api/v1/allowlist/entries — M36 rich format.
func (h *Handler) GetAllowlistEntries(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	entries := cfg.Filtering.AllowlistEntries
	if entries == nil {
		entries = []config.AllowlistEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// addAllowlistRequest is the body accepted by POST /api/v1/allowlist.
// M36 adds optional per-entry metadata fields.
type addAllowlistRequest struct {
	Domain     string  `json:"domain"`
	ExpiresAt  *int64  `json:"expires_at,omitempty"`  // Unix seconds; nil = no expiry
	Note       string  `json:"note,omitempty"`
	ScheduleID string  `json:"schedule_id,omitempty"`
}

// AddAllowlistEntry handles POST /api/v1/allowlist.
func (h *Handler) AddAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	var req addAllowlistRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}

	// When a cluster is available, route through Raft so the entry is
	// replicated. The rich-metadata path (M36) always requires the cluster.
	if cl := h.app.GetCluster(); cl != nil {
		cfg := h.app.GetCfg()
		// Duplicate check across both legacy list and rich entries.
		for _, d := range cfg.Filtering.Allowlist {
			if strings.ToLower(d) == req.Domain {
				writeError(w, http.StatusConflict, "domain already in allowlist")
				return
			}
		}
		payload := cluster.AllowlistAddPayload{
			Domain:     req.Domain,
			ExpiresAt:  req.ExpiresAt,
			Note:       req.Note,
			ScheduleID: req.ScheduleID,
		}
		if err := cl.AddAllowlistEntryRich(payload); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// Standalone / no-cluster path (legacy).
		conflict := false
		if err := h.app.WithWriteLock(func(cfg *config.Config) error {
			for _, d := range cfg.Filtering.Allowlist {
				if strings.ToLower(d) == req.Domain {
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
	}

	// M4.7 — surgical cache invalidation.
	if cache := h.app.GetDNSCache(); cache != nil {
		cache.PurgeDomain(req.Domain)
	}

	entry := config.AllowlistEntry{Domain: req.Domain, Note: req.Note, ScheduleID: req.ScheduleID}
	if req.ExpiresAt != nil {
		t := time.Unix(*req.ExpiresAt, 0).UTC()
		entry.ExpiresAt = &t
	}
	writeJSON(w, http.StatusCreated, entry)
}

// ImportAllowlist handles POST /api/v1/allowlist/import — bulk add (M36).
func (h *Handler) ImportAllowlist(w http.ResponseWriter, r *http.Request) {
	var entries []addAllowlistRequest
	if !decodeJSON(w, r, &entries) {
		return
	}
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	var added, skipped int
	cfg := h.app.GetCfg()
	existing := make(map[string]struct{}, len(cfg.Filtering.Allowlist))
	for _, d := range cfg.Filtering.Allowlist {
		existing[strings.ToLower(d)] = struct{}{}
	}
	for _, e := range cfg.Filtering.AllowlistEntries {
		existing[strings.ToLower(e.Domain)] = struct{}{}
	}
	for _, req := range entries {
		req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
		if req.Domain == "" {
			continue
		}
		if _, dup := existing[req.Domain]; dup {
			skipped++
			continue
		}
		payload := cluster.AllowlistAddPayload{
			Domain:     req.Domain,
			ExpiresAt:  req.ExpiresAt,
			Note:       req.Note,
			ScheduleID: req.ScheduleID,
		}
		if err := cl.AddAllowlistEntryRich(payload); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		existing[req.Domain] = struct{}{}
		added++
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": skipped})
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

// GetProfileAllowlistEntries handles GET /api/v1/profiles/{id}/allowlist/entries — M36 rich format.
func (h *Handler) GetProfileAllowlistEntries(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, p := range cfg.Profiles {
		if p.ID == id {
			entries := p.AllowlistEntries
			if entries == nil {
				entries = []config.AllowlistEntry{}
			}
			writeJSON(w, http.StatusOK, entries)
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
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
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
	// Duplicate check across both plain and rich lists.
	for _, d := range existing.Allowlist {
		if strings.ToLower(d) == req.Domain {
			writeError(w, http.StatusConflict, "domain already in profile allowlist")
			return
		}
	}
	for _, e := range existing.AllowlistEntries {
		if strings.ToLower(e.Domain) == req.Domain {
			writeError(w, http.StatusConflict, "domain already in profile allowlist")
			return
		}
	}
	updated := *existing
	if req.ExpiresAt != nil || req.Note != "" || req.ScheduleID != "" {
		// Rich entry — store in AllowlistEntries so expiry/schedule is honoured.
		entry := config.AllowlistEntry{
			Domain:     req.Domain,
			Note:       req.Note,
			ScheduleID: req.ScheduleID,
		}
		if req.ExpiresAt != nil {
			t := time.Unix(*req.ExpiresAt, 0).UTC()
			entry.ExpiresAt = &t
		}
		updated.AllowlistEntries = append(updated.AllowlistEntries, entry)
	} else {
		// Plain entry — backward-compat list.
		updated.Allowlist = append(updated.Allowlist, req.Domain)
	}
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
		if strings.ToLower(d) == strings.ToLower(domain) {
			updated.Allowlist = append(updated.Allowlist[:i], updated.Allowlist[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		for i, e := range updated.AllowlistEntries {
			if strings.ToLower(e.Domain) == strings.ToLower(domain) {
				updated.AllowlistEntries = append(updated.AllowlistEntries[:i], updated.AllowlistEntries[i+1:]...)
				found = true
				break
			}
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
	// M27 cache-purge fix: evict the stale cached DNS response so the next
	// query for this domain sees the updated (now-blocked) decision immediately.
	if cache := h.app.GetDNSCache(); cache != nil {
		cache.PurgeDomain(domain)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReplaceProfileAllowlist handles PUT /api/v1/profiles/{id}/allowlist.
// Atomically replaces the full allowlist for the named profile.
func (h *Handler) ReplaceProfileAllowlist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")

	var newList []string
	if !decodeJSON(w, r, &newList) {
		return
	}
	for _, d := range newList {
		if d == "" {
			writeError(w, http.StatusBadRequest, "allowlist entries must be non-empty strings")
			return
		}
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
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	// Track which domains were removed so we can purge their cache entries.
	newSet := make(map[string]struct{}, len(newList))
	for _, d := range newList {
		newSet[d] = struct{}{}
	}
	var removed []string
	for _, d := range existing.Allowlist {
		if _, kept := newSet[d]; !kept {
			removed = append(removed, d)
		}
	}

	updated := *existing
	updated.Allowlist = newList

	if err := h.app.GetCluster().UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Purge cache for every domain that was in the old list but not the new one.
	if cache := h.app.GetDNSCache(); cache != nil {
		for _, d := range removed {
			cache.PurgeDomain(d)
		}
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

// ─── Shared allowlists (M36) ──────────────────────────────────────────────────

// ListSharedAllowlists handles GET /api/v1/shared-allowlists.
func (h *Handler) ListSharedAllowlists(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	list := cfg.Filtering.SharedAllowlists
	if list == nil {
		list = []config.SharedAllowlist{}
	}
	writeJSON(w, http.StatusOK, list)
}

// GetSharedAllowlist handles GET /api/v1/shared-allowlists/{id}.
func (h *Handler) GetSharedAllowlist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, sal := range cfg.Filtering.SharedAllowlists {
		if sal.ID == id {
			writeJSON(w, http.StatusOK, sal)
			return
		}
	}
	writeError(w, http.StatusNotFound, "shared allowlist not found")
}

// CreateSharedAllowlist handles POST /api/v1/shared-allowlists.
func (h *Handler) CreateSharedAllowlist(w http.ResponseWriter, r *http.Request) {
	var sal config.SharedAllowlist
	if !decodeJSON(w, r, &sal) {
		return
	}
	if sal.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if sal.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	// Check for duplicate ID.
	cfg := h.app.GetCfg()
	for _, existing := range cfg.Filtering.SharedAllowlists {
		if existing.ID == sal.ID {
			writeError(w, http.StatusConflict, "shared allowlist with this id already exists")
			return
		}
	}
	if err := cl.UpsertSharedAllowlist(sal); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sal)
}

// UpdateSharedAllowlist handles PUT /api/v1/shared-allowlists/{id}.
func (h *Handler) UpdateSharedAllowlist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var sal config.SharedAllowlist
	if !decodeJSON(w, r, &sal) {
		return
	}
	sal.ID = id // enforce path ID
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	cfg := h.app.GetCfg()
	found := false
	for _, existing := range cfg.Filtering.SharedAllowlists {
		if existing.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "shared allowlist not found")
		return
	}
	if err := cl.UpsertSharedAllowlist(sal); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sal)
}

// DeleteSharedAllowlist handles DELETE /api/v1/shared-allowlists/{id}.
func (h *Handler) DeleteSharedAllowlist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	cfg := h.app.GetCfg()
	found := false
	for _, sal := range cfg.Filtering.SharedAllowlists {
		if sal.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "shared allowlist not found")
		return
	}
	if err := cl.DeleteSharedAllowlist(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
