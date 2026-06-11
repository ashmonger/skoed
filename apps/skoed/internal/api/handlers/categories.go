package handlers

import (
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
	"github.com/skoed/skoed/internal/filter/categories"
)

// categoryResponse is the JSON shape returned by GET /api/v1/categories.
// `url` is the *effective* URL — the operator override when set, else the
// catalog's DefaultURL. UIs that want to compare can show both.
type categoryResponse struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	DefaultURL         string   `json:"default_url"`
	URL                string   `json:"url"`
	Format             string   `json:"format"`
	EnabledForProfiles []string `json:"enabled_for_profiles"`
}

// ListCategories handles GET /api/v1/categories.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	out := make([]categoryResponse, 0, len(categories.Catalog))
	for _, name := range categories.Names() {
		out = append(out, buildCategoryView(cfg, name))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetCategory handles GET /api/v1/categories/{name}.
func (h *Handler) GetCategory(w http.ResponseWriter, r *http.Request) {
	name := urlParam(r, "name")
	if _, ok := categories.Catalog[name]; !ok {
		writeError(w, http.StatusNotFound, "unknown category")
		return
	}
	writeJSON(w, http.StatusOK, buildCategoryView(h.app.GetCfg(), name))
}

func buildCategoryView(cfg *config.Config, name string) categoryResponse {
	cat := categories.Catalog[name]
	row := categoryResponse{
		Name:               name,
		Description:        cat.Description,
		DefaultURL:         cat.DefaultURL,
		URL:                cat.DefaultURL,
		Format:             cat.Format,
		EnabledForProfiles: profilesWithCategory(cfg, name),
	}
	for _, o := range cfg.Categories {
		if o.Name == name {
			if o.URL != "" {
				row.URL = o.URL
			}
			if o.Format != "" {
				row.Format = o.Format
			}
			break
		}
	}
	return row
}

// UpdateCategory handles PATCH /api/v1/categories/{name}. Operator overrides
// for the catalog's default URL and parser format.
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	name := urlParam(r, "name")
	if _, ok := categories.Catalog[name]; !ok {
		writeError(w, http.StatusNotFound, "unknown category")
		return
	}
	var body struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	o := config.CategoryOverride{Name: name, URL: body.URL, Format: body.Format}
	if err := h.app.GetCluster().UpsertCategoryOverride(o); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// EnableCategory handles POST /api/v1/categories/{name}/enable.
// Body: {profile_id}.
func (h *Handler) EnableCategory(w http.ResponseWriter, r *http.Request) {
	name := urlParam(r, "name")
	cat, ok := categories.Catalog[name]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown category")
		return
	}
	var body struct {
		ProfileID string `json:"profile_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profile_id is required")
		return
	}
	cfg := h.app.GetCfg()
	cluster := h.app.GetCluster()
	if cluster == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	// Ensure the managed blocklist exists. Sourced from the bundled list
	// when present (DoH), otherwise from the catalog/override URL.
	blID := categories.BlocklistID(name)
	hasBL := false
	for _, bl := range cfg.Filtering.Blocklists {
		if bl.ID == blID {
			hasBL = true
			break
		}
	}
	if !hasBL {
		src := config.BlocklistSource{Type: "url", URL: cat.DefaultURL, Format: cat.Format}
		var domains []string
		if len(cat.Bundled) > 0 {
			src = config.BlocklistSource{Type: "inline", Format: cat.Format}
			domains = cat.Bundled
		}
		// Operator override takes priority over catalog default for non-bundled
		// categories.
		for _, o := range cfg.Categories {
			if o.Name == name && o.URL != "" {
				src.URL = o.URL
				if o.Format != "" {
					src.Format = o.Format
				}
			}
		}
		// For URL-based categories download domains immediately so the blocklist
		// is usable right after enablement without a separate manual refresh.
		if src.Type == "url" && src.URL != "" {
			if downloaded, err := filter.Download(src.URL, src.Format, 30*time.Second); err == nil {
				domains = downloaded
			}
		}
		bl := config.Blocklist{
			ID:          blID,
			Name:        cat.Description,
			Enabled:     true,
			Source:      src,
			Domains:     domains,
			LastUpdated: time.Now().UTC().Format(time.RFC3339),
			Managed:     true,
		}
		if err := cluster.UpsertBlocklist(bl); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// Attach to the named profile (create on the fly if missing).
	prof := findProfile(cfg, body.ProfileID)
	if prof == nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	for _, b := range prof.Blocklists {
		if b == blID {
			writeJSON(w, http.StatusOK, map[string]any{"profile_id": body.ProfileID, "blocklist_id": blID, "added": false})
			return
		}
	}
	updated := *prof
	updated.Blocklists = append(updated.Blocklists, blID)
	if err := cluster.UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"profile_id": body.ProfileID, "blocklist_id": blID, "added": true})
}

// DisableCategory handles POST /api/v1/categories/{name}/disable.
// Body: {profile_id}.
func (h *Handler) DisableCategory(w http.ResponseWriter, r *http.Request) {
	name := urlParam(r, "name")
	if _, ok := categories.Catalog[name]; !ok {
		writeError(w, http.StatusNotFound, "unknown category")
		return
	}
	var body struct {
		ProfileID string `json:"profile_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg := h.app.GetCfg()
	prof := findProfile(cfg, body.ProfileID)
	if prof == nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	blID := categories.BlocklistID(name)
	updated := *prof
	updated.Blocklists = removeString(updated.Blocklists, blID)
	if err := h.app.GetCluster().UpsertProfile(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func profilesWithCategory(cfg *config.Config, name string) []string {
	blID := categories.BlocklistID(name)
	out := []string{}
	for _, p := range cfg.Profiles {
		for _, b := range p.Blocklists {
			if b == blID {
				out = append(out, p.ID)
				break
			}
		}
	}
	return out
}

func findProfile(cfg *config.Config, id string) *config.Profile {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == id {
			return &cfg.Profiles[i]
		}
	}
	return nil
}

func removeString(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
