package handlers

import (
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
)

// blocklistResponse is the JSON representation of a blocklist exposed by the API.
type blocklistResponse struct {
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	Enabled                bool                   `json:"enabled"`
	Source                 config.BlocklistSource `json:"source"`
	BlockPolicy            string                 `json:"block_policy,omitempty"`
	DomainCount            int                    `json:"domain_count"`
	LastUpdated            string                 `json:"last_updated,omitempty"`
	Managed                bool                   `json:"managed,omitempty"`
	// M5.4 — automated refresh state.
	// Note: omitempty is intentionally absent so that 0 (disabled) is included in
	// the response, allowing the Vue component to update its local state correctly.
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	LastRefreshAt          string `json:"last_refresh_at,omitempty"`
	LastRefreshStatus      string `json:"last_refresh_status,omitempty"`
	LastRefreshError       string `json:"last_refresh_error,omitempty"`
}

func toBlocklistResponse(bl config.Blocklist) blocklistResponse {
	return blocklistResponse{
		ID:                     bl.ID,
		Name:                   bl.Name,
		Enabled:                bl.Enabled,
		Source:                 bl.Source,
		BlockPolicy:            bl.BlockPolicy,
		DomainCount:            len(bl.Domains),
		LastUpdated:            bl.LastUpdated,
		Managed:                bl.Managed,
		RefreshIntervalSeconds: bl.RefreshIntervalSeconds,
		LastRefreshAt:          bl.LastRefreshAt,
		LastRefreshStatus:      bl.LastRefreshStatus,
		LastRefreshError:       bl.LastRefreshError,
	}
}

// ListBlocklists handles GET /api/v1/blocklists.
func (h *Handler) ListBlocklists(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	result := make([]blocklistResponse, len(cfg.Filtering.Blocklists))
	for i, bl := range cfg.Filtering.Blocklists {
		result[i] = toBlocklistResponse(bl)
	}
	writeJSON(w, http.StatusOK, result)
}

// createBlocklistRequest is the body accepted by POST /api/v1/blocklists.
type createBlocklistRequest struct {
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	Enabled                *bool                  `json:"enabled"` // nil means default=true
	Source                 config.BlocklistSource `json:"source"`
	BlockPolicy            string                 `json:"block_policy"`
	Domains                []string               `json:"domains"`
	RefreshIntervalSeconds int                    `json:"refresh_interval_seconds"`
}

// CreateBlocklist handles POST /api/v1/blocklists.
func (h *Handler) CreateBlocklist(w http.ResponseWriter, r *http.Request) {
	var req createBlocklistRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	var domains []string
	if req.Source.Type == "url" {
		if req.Source.URL == "" {
			writeError(w, http.StatusBadRequest, "source.url is required for type=url")
			return
		}
		var err error
		domains, err = filter.Download(req.Source.URL, req.Source.Format, 30*time.Second)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to download blocklist: "+err.Error())
			return
		}
	} else {
		domains = req.Domains
	}

	id := req.ID
	if id == "" {
		id = newID()
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	bl := config.Blocklist{
		ID:                     id,
		Name:                   req.Name,
		Enabled:                enabled,
		Source:                 req.Source,
		BlockPolicy:            req.BlockPolicy,
		Domains:                domains,
		LastUpdated:            time.Now().UTC().Format(time.RFC3339),
		RefreshIntervalSeconds: req.RefreshIntervalSeconds,
	}

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		cfg.Filtering.Blocklists = append(cfg.Filtering.Blocklists, bl)
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

	writeJSON(w, http.StatusCreated, toBlocklistResponse(bl))
}

// GetBlocklist handles GET /api/v1/blocklists/{id}.
func (h *Handler) GetBlocklist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, bl := range cfg.Filtering.Blocklists {
		if bl.ID == id {
			writeJSON(w, http.StatusOK, toBlocklistResponse(bl))
			return
		}
	}
	writeError(w, http.StatusNotFound, "blocklist not found")
}

// updateBlocklistRequest is the body accepted by PATCH /api/v1/blocklists/{id}.
type updateBlocklistRequest struct {
	Name                   *string                `json:"name"`
	Enabled                *bool                  `json:"enabled"`
	BlockPolicy            *string                `json:"block_policy"`
	Source                 *updateBlocklistSource `json:"source"`
	RefreshIntervalSeconds *int                   `json:"refresh_interval_seconds"`
}

type updateBlocklistSource struct {
	URL    string `json:"url"`
	Format string `json:"format"`
}

// UpdateBlocklist handles PATCH /api/v1/blocklists/{id}.
func (h *Handler) UpdateBlocklist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")

	var req updateBlocklistRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var updated config.Blocklist
	found := false

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i := range cfg.Filtering.Blocklists {
			if cfg.Filtering.Blocklists[i].ID == id {
				if req.Name != nil {
					cfg.Filtering.Blocklists[i].Name = *req.Name
				}
				if req.Enabled != nil {
					cfg.Filtering.Blocklists[i].Enabled = *req.Enabled
				}
				if req.BlockPolicy != nil {
					cfg.Filtering.Blocklists[i].BlockPolicy = *req.BlockPolicy
				}
				if req.Source != nil && req.Source.URL != "" {
					cfg.Filtering.Blocklists[i].Source.Type = "url"
					cfg.Filtering.Blocklists[i].Source.URL = req.Source.URL
					if req.Source.Format != "" {
						cfg.Filtering.Blocklists[i].Source.Format = req.Source.Format
					}
					// Invalidate cached domains — they'll be repopulated on next refresh.
					cfg.Filtering.Blocklists[i].Domains = nil
					cfg.Filtering.Blocklists[i].LastUpdated = ""
				}
				if req.RefreshIntervalSeconds != nil {
					if *req.RefreshIntervalSeconds < 0 {
						*req.RefreshIntervalSeconds = 0
					}
					cfg.Filtering.Blocklists[i].RefreshIntervalSeconds = *req.RefreshIntervalSeconds
				}
				updated = cfg.Filtering.Blocklists[i]
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
		writeError(w, http.StatusNotFound, "blocklist not found")
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

	writeJSON(w, http.StatusOK, toBlocklistResponse(updated))
}

// DeleteBlocklist handles DELETE /api/v1/blocklists/{id}.
func (h *Handler) DeleteBlocklist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	found := false

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i, bl := range cfg.Filtering.Blocklists {
			if bl.ID == id {
				cfg.Filtering.Blocklists = append(
					cfg.Filtering.Blocklists[:i],
					cfg.Filtering.Blocklists[i+1:]...,
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
		writeError(w, http.StatusNotFound, "blocklist not found")
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

// RefreshBlocklist handles POST /api/v1/blocklists/{id}/refresh.
// Only URL-type blocklists can be refreshed.
func (h *Handler) RefreshBlocklist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()

	var sourceURL, sourceFormat string
	found := false
	for _, bl := range cfg.Filtering.Blocklists {
		if bl.ID == id {
			if bl.Source.Type != "url" {
				writeError(w, http.StatusBadRequest, "only url-type blocklists can be refreshed")
				return
			}
			sourceURL = bl.Source.URL
			sourceFormat = bl.Source.Format
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "blocklist not found")
		return
	}

	domains, err := filter.Download(sourceURL, sourceFormat, 30*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to download blocklist: "+err.Error())
		return
	}

	lastUpdated := time.Now().UTC().Format(time.RFC3339)
	var updated config.Blocklist

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i := range cfg.Filtering.Blocklists {
			if cfg.Filtering.Blocklists[i].ID == id {
				cfg.Filtering.Blocklists[i].Domains = domains
				cfg.Filtering.Blocklists[i].LastUpdated = lastUpdated
				updated = cfg.Filtering.Blocklists[i]
				return nil
			}
		}
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

	writeJSON(w, http.StatusOK, toBlocklistResponse(updated))
}
