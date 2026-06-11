package handlers

import (
	"net"
	"net/http"
	"strings"

	"github.com/skoed/skoed/internal/config"
)

// localDNSEntryRequest is the body accepted by POST and PUT local-dns endpoints.
type localDNSEntryRequest struct {
	Hostname string `json:"hostname"`
	Type     string `json:"type"`  // "A" | "AAAA" | "CNAME"
	Value    string `json:"value"` // IP for A/AAAA; target FQDN for CNAME
	IP       string `json:"ip"`    // shorthand: auto-detects A vs AAAA, sets Type and Value
	TTL      int    `json:"ttl"`
}

// validateLocalDNSEntry checks the type and value fields and returns an error message
// if invalid, or an empty string if valid.
func validateLocalDNSEntry(req localDNSEntryRequest) string {
	if req.Hostname == "" {
		return "hostname is required"
	}
	switch req.Type {
	case "A":
		ip := net.ParseIP(req.Value)
		if ip == nil || ip.To4() == nil {
			return "value must be a valid IPv4 address for type A"
		}
	case "AAAA":
		ip := net.ParseIP(req.Value)
		if ip == nil || ip.To4() != nil {
			return "value must be a valid IPv6 address for type AAAA"
		}
	case "CNAME":
		if req.Value == "" {
			return "value must be a non-empty FQDN for type CNAME"
		}
	default:
		return "type must be A, AAAA, or CNAME"
	}
	if req.Value == "" {
		return "value is required"
	}
	return ""
}

// ListLocalDNS handles GET /api/v1/local-dns.
func (h *Handler) ListLocalDNS(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	entries := cfg.LocalDNS.Entries
	if entries == nil {
		entries = []config.LocalDNSEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// CreateLocalDNSEntry handles POST /api/v1/local-dns.
func (h *Handler) CreateLocalDNSEntry(w http.ResponseWriter, r *http.Request) {
	var req localDNSEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Normalise type to uppercase and resolve ip shorthand.
	req.Type = strings.ToUpper(req.Type)
	if req.IP != "" && req.Value == "" {
		req.Value = req.IP
		if net.ParseIP(req.IP).To4() != nil {
			req.Type = "A"
		} else {
			req.Type = "AAAA"
		}
	}

	if msg := validateLocalDNSEntry(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.TTL == 0 {
		req.TTL = 300
	}

	entry := config.LocalDNSEntry{
		ID:       newID(),
		Hostname: req.Hostname,
		Type:     req.Type,
		Value:    req.Value,
		TTL:      req.TTL,
	}

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		cfg.LocalDNS.Entries = append(cfg.LocalDNS.Entries, entry)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if err := h.app.RebuildDNSFromCfg(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild dns: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, entry)
}

// UpdateLocalDNSEntry handles PUT /api/v1/local-dns/{id}.
func (h *Handler) UpdateLocalDNSEntry(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")

	var req localDNSEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Type = strings.ToUpper(req.Type)
	if req.IP != "" && req.Value == "" {
		req.Value = req.IP
		if net.ParseIP(req.IP).To4() != nil {
			req.Type = "A"
		} else {
			req.Type = "AAAA"
		}
	}

	if msg := validateLocalDNSEntry(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.TTL == 0 {
		req.TTL = 300
	}

	found := false
	var updated config.LocalDNSEntry

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i := range cfg.LocalDNS.Entries {
			if cfg.LocalDNS.Entries[i].ID == id {
				cfg.LocalDNS.Entries[i].Hostname = req.Hostname
				cfg.LocalDNS.Entries[i].Type = req.Type
				cfg.LocalDNS.Entries[i].Value = req.Value
				cfg.LocalDNS.Entries[i].TTL = req.TTL
				updated = cfg.LocalDNS.Entries[i]
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
		writeError(w, http.StatusNotFound, "local DNS entry not found")
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if err := h.app.RebuildDNSFromCfg(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild dns: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// DeleteLocalDNSEntry handles DELETE /api/v1/local-dns/{id}.
func (h *Handler) DeleteLocalDNSEntry(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	found := false

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		for i, e := range cfg.LocalDNS.Entries {
			if e.ID == id {
				cfg.LocalDNS.Entries = append(
					cfg.LocalDNS.Entries[:i],
					cfg.LocalDNS.Entries[i+1:]...,
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
		writeError(w, http.StatusNotFound, "local DNS entry not found")
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if err := h.app.RebuildDNSFromCfg(); err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild dns: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
