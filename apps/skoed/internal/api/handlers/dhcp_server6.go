// Handlers for M30 DHCPv6 server and DHCPv4 lease persistence.
//
// Routes (all authenticated):
//   GET    /api/v1/dhcp/server/status6
//   PUT    /api/v1/settings/dhcp6
//   GET    /api/v1/dhcp/leases6
//   DELETE /api/v1/dhcp/leases6/{address}
//   GET    /api/v1/dhcp/static-assignments6
//   POST   /api/v1/dhcp/static-assignments6
//   DELETE /api/v1/dhcp/static-assignments6/{duid}
package handlers

import (
	"encoding/binary"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/skoed/skoed/internal/config"
)

// ─── GET /api/v1/dhcp/server/status6 ─────────────────────────────────────────

func (h *Handler) DhcpV6ServerStatus(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	cfg, err := cl.GetDhcp6ServerSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read dhcp6 settings: "+err.Error())
		return
	}
	leasesActive := 0
	srv := h.app.GetDhcpServer6()
	if cl.IsLeader() && srv != nil {
		leasesActive = len(srv.ActiveLeases())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       cfg.Enabled,
		"is_leader":     cl.IsLeader(),
		"prefix":        cfg.Prefix,
		"pool_start":    cfg.PoolStart,
		"pool_end":      cfg.PoolEnd,
		"lease_time":    cfg.LeaseTime,
		"search_domain": cfg.SearchDomain,
		"leases_active": leasesActive,
		"pool_total":    poolSize6(cfg.PoolStart, cfg.PoolEnd),
	})
}

// poolSize6 computes the approximate number of addresses in [start, end].
// It compares only the last 4 bytes (sufficient for /96+ pools used in practice).
func poolSize6(start, end string) int {
	s := net.ParseIP(start).To16()
	e := net.ParseIP(end).To16()
	if s == nil || e == nil {
		return 0
	}
	si := binary.BigEndian.Uint32(s[12:16])
	ei := binary.BigEndian.Uint32(e[12:16])
	if ei < si {
		return 0
	}
	return int(ei-si) + 1
}

// ─── PUT /api/v1/settings/dhcp6 ──────────────────────────────────────────────

type dhcpV6SettingsUpdateReq struct {
	Enabled      *bool   `json:"enabled"`
	Prefix       *string `json:"prefix"`
	PoolStart    *string `json:"pool_start"`
	PoolEnd      *string `json:"pool_end"`
	LeaseTime    *int    `json:"lease_time"`
	SearchDomain *string `json:"search_domain"`
}

func (h *Handler) PutDhcpV6Settings(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	var req dhcpV6SettingsUpdateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	current, err := cl.GetDhcp6ServerSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read settings: "+err.Error())
		return
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.Prefix != nil {
		current.Prefix = *req.Prefix
	}
	if req.PoolStart != nil {
		current.PoolStart = *req.PoolStart
	}
	if req.PoolEnd != nil {
		current.PoolEnd = *req.PoolEnd
	}
	if req.LeaseTime != nil {
		current.LeaseTime = *req.LeaseTime
	}
	if req.SearchDomain != nil {
		current.SearchDomain = *req.SearchDomain
	}
	if current.Enabled && (current.PoolStart == "" || current.PoolEnd == "") {
		writeError(w, http.StatusConflict, "pool_start and pool_end are required to enable the DHCPv6 server")
		return
	}
	if err := cl.SetDhcp6ServerSettings(current); err != nil {
		writeError(w, http.StatusInternalServerError, "apply settings: "+err.Error())
		return
	}
	if srv := h.app.GetDhcpServer6(); srv != nil {
		srv.UpdateConfig(current)
		if current.Enabled && cl.IsLeader() && !srv.Running() {
			srv.Start()
		} else if !current.Enabled && srv.Running() {
			srv.Stop()
		}
	}
	h.DhcpV6ServerStatus(w, r)
}

// ─── GET /api/v1/dhcp/leases6 ────────────────────────────────────────────────

func (h *Handler) DhcpV6Leases(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl != nil && !cl.IsLeader() {
		status, body, _, err := cl.ForwardWrite(r.Context(), http.MethodGet, r.URL.Path, nil, r.Header)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write(body) //nolint:errcheck
			return
		}
	}
	srv := h.app.GetDhcpServer6()
	leases := []any{}
	if srv != nil {
		for _, l := range srv.ActiveLeases() {
			leases = append(leases, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": leases})
}

// ─── DELETE /api/v1/dhcp/leases6/{address} ───────────────────────────────────

func (h *Handler) DeleteDhcpV6Lease(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	address := chi.URLParam(r, "address")
	if net.ParseIP(address) == nil {
		writeError(w, http.StatusBadRequest, "invalid IPv6 address")
		return
	}
	if err := cl.DeleteDhcp6Lease(address); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── GET /api/v1/dhcp/static-assignments6 ────────────────────────────────────

func (h *Handler) ListDhcpV6StaticAssignments(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	entries, err := cl.GetDhcp6StaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []config.Dhcp6StaticAssignment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"assignments": entries})
}

// ─── POST /api/v1/dhcp/static-assignments6 ───────────────────────────────────

func (h *Handler) CreateDhcpV6StaticAssignment(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	var a config.Dhcp6StaticAssignment
	if !decodeJSON(w, r, &a) {
		return
	}
	if a.DUID == "" {
		writeError(w, http.StatusBadRequest, "duid is required")
		return
	}
	if net.ParseIP(a.Address) == nil {
		writeError(w, http.StatusBadRequest, "invalid IPv6 address")
		return
	}
	existing, err := cl.GetDhcp6StaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, e := range existing {
		if strings.EqualFold(e.DUID, a.DUID) {
			writeError(w, http.StatusConflict, "a static assignment for this DUID already exists")
			return
		}
	}
	if err := cl.UpsertDhcp6StaticAssignment(a); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if srv := h.app.GetDhcpServer6(); srv != nil {
		cfg, _ := cl.GetDhcp6ServerSettings()
		srv.UpdateConfig(cfg)
	}
	writeJSON(w, http.StatusCreated, a)
}

// ─── DELETE /api/v1/dhcp/static-assignments6/{duid} ──────────────────────────

func (h *Handler) DeleteDhcpV6StaticAssignment(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	duid := chi.URLParam(r, "duid")
	if duid == "" {
		writeError(w, http.StatusBadRequest, "duid is required")
		return
	}
	existing, err := cl.GetDhcp6StaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for _, e := range existing {
		if strings.EqualFold(e.DUID, duid) {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "static assignment not found")
		return
	}
	if err := cl.DeleteDhcp6StaticAssignment(duid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if srv := h.app.GetDhcpServer6(); srv != nil {
		cfg, _ := cl.GetDhcp6ServerSettings()
		srv.UpdateConfig(cfg)
	}
	w.WriteHeader(http.StatusNoContent)
}
