package handlers

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/skoed/skoed/internal/config"
)

var deviceIDRe = regexp.MustCompile(`[^a-z0-9-]`)

// slugify converts a device name into a URL-safe ID.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = deviceIDRe.ReplaceAllString(s, "")
	s = regexp.MustCompile(`-{2,}`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// ListDevices handles GET /api/v1/devices.
func (h *Handler) ListDevices(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	if cfg == nil || len(cfg.Devices) == 0 {
		writeJSON(w, http.StatusOK, []config.Device{})
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, cfg.Devices)
		return
	}
	var out []config.Device
	for _, d := range cfg.Devices {
		if strings.Contains(strings.ToLower(d.Name), q) {
			out = append(out, d)
			continue
		}
		for _, m := range d.MACs {
			if strings.Contains(strings.ToLower(m), q) {
				out = append(out, d)
				break
			}
		}
		for _, ip := range d.IPs {
			if strings.Contains(ip, q) {
				out = append(out, d)
				break
			}
		}
		for _, hn := range d.Hostnames {
			if strings.Contains(strings.ToLower(hn), q) {
				out = append(out, d)
				break
			}
		}
	}
	if out == nil {
		out = []config.Device{}
	}
	writeJSON(w, http.StatusOK, out)
}

// GetDevice handles GET /api/v1/devices/{id}.
func (h *Handler) GetDevice(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	for _, d := range cfg.Devices {
		if d.ID == id {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	writeError(w, http.StatusNotFound, "device not found")
}

// CreateDevice handles POST /api/v1/devices.
func (h *Handler) CreateDevice(w http.ResponseWriter, r *http.Request) {
	var d config.Device
	if !decodeJSON(w, r, &d) {
		return
	}
	if d.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(d.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 64 characters or fewer")
		return
	}
	if d.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profile_id is required")
		return
	}

	d.ID = slugify(d.Name)
	if d.ID == "" {
		writeError(w, http.StatusBadRequest, "name produces an empty slug; use alphanumeric characters")
		return
	}

	cfg := h.app.GetCfg()

	// Reject duplicate name.
	for _, existing := range cfg.Devices {
		if strings.EqualFold(existing.Name, d.Name) {
			writeError(w, http.StatusConflict, "a device with this name already exists")
			return
		}
	}

	// Validate profile_id references an existing profile.
	profileExists := false
	for _, p := range cfg.Profiles {
		if p.ID == d.ProfileID {
			profileExists = true
			break
		}
	}
	if !profileExists {
		writeError(w, http.StatusBadRequest, "profile_id references a non-existent profile")
		return
	}

	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertDevice(d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// UpdateDevice handles PATCH /api/v1/devices/{id}.
func (h *Handler) UpdateDevice(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var patch struct {
		Name      *string  `json:"name"`
		ProfileID *string  `json:"profile_id"`
		MACs      []string `json:"macs"`
		IPs       []string `json:"ips"`
		Hostnames []string `json:"hostnames"`
		ClientIDs []string `json:"client_ids"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}

	cfg := h.app.GetCfg()
	var existing *config.Device
	for i := range cfg.Devices {
		if cfg.Devices[i].ID == id {
			existing = &cfg.Devices[i]
			break
		}
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	updated := *existing
	if patch.Name != nil {
		if len(*patch.Name) > 64 {
			writeError(w, http.StatusBadRequest, "name must be 64 characters or fewer")
			return
		}
		updated.Name = *patch.Name
	}
	if patch.ProfileID != nil {
		profileExists := false
		for _, p := range cfg.Profiles {
			if p.ID == *patch.ProfileID {
				profileExists = true
				break
			}
		}
		if !profileExists {
			writeError(w, http.StatusBadRequest, "profile_id references a non-existent profile")
			return
		}
		updated.ProfileID = *patch.ProfileID
	}
	if patch.MACs != nil {
		updated.MACs = patch.MACs
	}
	if patch.IPs != nil {
		updated.IPs = patch.IPs
	}
	if patch.Hostnames != nil {
		updated.Hostnames = patch.Hostnames
	}
	if patch.ClientIDs != nil {
		updated.ClientIDs = patch.ClientIDs
	}

	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().UpsertDevice(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeleteDevice handles DELETE /api/v1/devices/{id}.
func (h *Handler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	cfg := h.app.GetCfg()
	found := false
	for _, d := range cfg.Devices {
		if d.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	if h.app.GetCluster() == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if err := h.app.GetCluster().DeleteDevice(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
