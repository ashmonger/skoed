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
	// M50 — URL lists only: the replicated content version, this node's
	// copy, and whether they match.
	ContentSHA256      string `json:"content_sha256,omitempty"`
	LocalContentSHA256 string `json:"local_content_sha256,omitempty"`
	ContentStatus      string `json:"content_status,omitempty"`
}

// Values of blocklistResponse.ContentStatus.
const (
	contentCurrent   = "current"
	contentOutOfDate = "out_of_date"
	contentMissing   = "missing"
)

// toBlocklistResponse builds the API view of bl on this node. For a URL
// list, domain_count is the count of this node's copy; for a URL list
// written by a previous version (domains replicated) and for an inline
// list, it is the number of replicated domains.
func (h *Handler) toBlocklistResponse(bl config.Blocklist) blocklistResponse {
	resp := blocklistResponse{
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
	if bl.Source.Type != blocklistSourceURL {
		return resp
	}
	if bl.Content != nil {
		resp.ContentSHA256 = bl.Content.SHA256
	}
	if cache := h.app.BlocklistContent(); cache != nil {
		if e, ok := cache.Get(bl.ID); ok {
			resp.LocalContentSHA256 = e.SHA256
			if bl.Content != nil {
				resp.DomainCount = e.Count
			}
		}
	}
	switch {
	case resp.LocalContentSHA256 == "":
		resp.ContentStatus = contentMissing
	case resp.LocalContentSHA256 == resp.ContentSHA256:
		resp.ContentStatus = contentCurrent
	default:
		resp.ContentStatus = contentOutOfDate
	}
	return resp
}

// ListBlocklists handles GET /api/v1/blocklists.
func (h *Handler) ListBlocklists(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	result := make([]blocklistResponse, len(cfg.Filtering.Blocklists))
	for i, bl := range cfg.Filtering.Blocklists {
		result[i] = h.toBlocklistResponse(bl)
	}
	writeJSON(w, http.StatusOK, result)
}

// Source types a blocklist can be created with. Any other value leaves the
// daemon with no way to load domains, so the create is refused rather than
// committed as an enabled list that matches nothing.
const (
	blocklistSourceURL    = "url"
	blocklistSourceInline = "inline"
)

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

	id := req.ID
	if id == "" {
		id = newID()
	}

	// Resolve the domains the list will hold. Every branch either produces a
	// non-empty list or refuses the create: a blocklist committed enabled with
	// no domains reads as active through the API and the web UI while
	// filtering nothing.
	var domains []string
	var content *config.BlocklistContent
	switch req.Source.Type {
	case blocklistSourceURL:
		if req.Source.URL == "" {
			writeError(w, http.StatusBadRequest, "source.url is required for source.type=url")
			return
		}
		var err error
		if ref := h.app.BlocklistRefresher(); ref != nil && ref.UsesContent() {
			// M50: the leader keeps the domains in its own copy and
			// replicates only their version; followers copy the file.
			content, err = ref.FetchContent(id, req.Source.URL, req.Source.Format)
		} else {
			domains, err = filter.Download(req.Source.URL, req.Source.Format, 30*time.Second)
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to download blocklist: "+err.Error())
			return
		}
	case blocklistSourceInline:
		if len(req.Domains) == 0 {
			writeError(w, http.StatusBadRequest, "domains is required for source.type=inline")
			return
		}
		// Store the domains the engine will load rather than the entries as
		// sent: the engine drops blank lines and comments when it reads a
		// blocklist, so an entry of "  " or "# ads.example" counts as one
		// domain in the response and as none in the filter. Parsing here
		// keeps domain_count and the engine's set in agreement, and leaves
		// nothing usable to refuse.
		domains = filter.ParseStoredDomains(req.Domains, req.Source.Format)
		if len(domains) == 0 {
			writeError(w, http.StatusBadRequest,
				"domains contains no usable domain for source.type=inline (blank lines and comments are ignored)")
			return
		}
	case "":
		writeError(w, http.StatusBadRequest, "source.type is required (url or inline)")
		return
	default:
		writeError(w, http.StatusBadRequest, "source.type must be url or inline")
		return
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
	if content != nil {
		bl.Content = content
		bl.LastUpdated = content.FetchedAt
		bl.LastRefreshAt = content.FetchedAt
		bl.LastRefreshStatus = "ok"
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

	writeJSON(w, http.StatusCreated, h.toBlocklistResponse(bl))
}

// GetBlocklist handles GET /api/v1/blocklists/{id}.
func (h *Handler) GetBlocklist(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, bl := range cfg.Filtering.Blocklists {
		if bl.ID == id {
			writeJSON(w, http.StatusOK, h.toBlocklistResponse(bl))
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
					bl := &cfg.Filtering.Blocklists[i]
					before := bl.Source
					bl.Source.Type = blocklistSourceURL
					bl.Source.URL = req.Source.URL
					if req.Source.Format != "" {
						bl.Source.Format = req.Source.Format
					}
					// A new URL or format invalidates the downloaded domains
					// and their version, so the scheduler downloads the list
					// again on its next tick.
					if bl.Source != before {
						bl.Domains = nil
						bl.Content = nil
						bl.LastUpdated = ""
					}
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

	writeJSON(w, http.StatusOK, h.toBlocklistResponse(updated))
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

	found := false
	for _, bl := range cfg.Filtering.Blocklists {
		if bl.ID == id {
			if bl.Source.Type != blocklistSourceURL {
				writeError(w, http.StatusBadRequest, "only url-type blocklists can be refreshed")
				return
			}
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "blocklist not found")
		return
	}

	// The request reached the leader through the forward middleware. The
	// download runs synchronously and records LastRefresh* like the
	// scheduler (TS-BlocklistContentDistribution §4).
	ref := h.app.BlocklistRefresher()
	if ref == nil {
		writeError(w, http.StatusServiceUnavailable, "blocklist refresh is not available on this node")
		return
	}
	if err := ref.RefreshNow(id); err != nil {
		writeError(w, http.StatusBadGateway, "failed to download blocklist: "+err.Error())
		return
	}

	for _, bl := range h.app.GetCfg().Filtering.Blocklists {
		if bl.ID == id {
			writeJSON(w, http.StatusOK, h.toBlocklistResponse(bl))
			return
		}
	}
	writeError(w, http.StatusNotFound, "blocklist not found")
}
