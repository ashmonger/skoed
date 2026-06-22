// Handlers for M23.5 built-in DHCP server.
//
// Routes (all authenticated):
//   GET  /api/v1/dhcp/server/status
//   PUT  /api/v1/settings/dhcp
//   GET  /api/v1/dhcp/leases
//   GET  /api/v1/dhcp/static-assignments
//   POST /api/v1/dhcp/static-assignments
//   DELETE /api/v1/dhcp/static-assignments/{mac}
package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/dhcp"
)

// ─── GET /api/v1/dhcp/server/status ──────────────────────────────────────────

func (h *Handler) DhcpServerStatus(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	cfg, err := cl.GetDhcpServerSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read dhcp settings: "+err.Error())
		return
	}

	srv := h.app.GetDhcpServer()
	poolTotal := poolSize(cfg.PoolStart, cfg.PoolEnd)

	leasesActive := 0
	if cl.IsLeader() {
		if srv != nil {
			leasesActive = len(srv.ActiveLeases())
		}
	} else {
		// Follower: fetch lease count from the leader so the UI shows accurate utilisation.
		if _, body, _, err := cl.ForwardWrite(r.Context(), http.MethodGet, "/api/v1/dhcp/leases", nil, r.Header); err == nil {
			var ls []json.RawMessage
			if json.Unmarshal(body, &ls) == nil {
				leasesActive = len(ls)
			}
		}
	}

	dnsServer := cfg.DNSServer
	if dnsServer == "" {
		dnsServer = h.defaultDNSAddr()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":            cfg.Enabled,
		"is_leader":          cl.IsLeader(),
		"pool_start":         cfg.PoolStart,
		"pool_end":           cfg.PoolEnd,
		"gateway":            cfg.Gateway,
		"lease_time_seconds": cfg.LeaseTimeSeconds,
		"domain":             cfg.Domain,
		"dns_server":         dnsServer,
		"leases_active":      leasesActive,
		"pool_total":         poolTotal,
	})
}

// defaultDNSAddr returns "127.0.0.1" — skoed always listens on 0.0.0.0
// so clients on the same node can reach DNS via loopback.
func (h *Handler) defaultDNSAddr() string {
	return "127.0.0.1"
}

// poolSize computes the number of IPs in the range [start, end] inclusive.
func poolSize(start, end string) int {
	s := net.ParseIP(start).To4()
	e := net.ParseIP(end).To4()
	if s == nil || e == nil {
		return 0
	}
	si := ipToUint32(s)
	ei := ipToUint32(e)
	if ei < si {
		return 0
	}
	return int(ei-si) + 1
}

func ipToUint32(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// ─── PUT /api/v1/settings/dhcp ────────────────────────────────────────────────

type dhcpSettingsUpdateReq struct {
	Enabled          *bool   `json:"enabled"`
	PoolStart        *string `json:"pool_start"`
	PoolEnd          *string `json:"pool_end"`
	Gateway          *string `json:"gateway"`
	LeaseTimeSeconds *int    `json:"lease_time_seconds"`
	Domain           *string `json:"domain"`
	DNSServer        *string `json:"dns_server"`
}

func (h *Handler) PutDhcpSettings(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	var req dhcpSettingsUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	current, err := cl.GetDhcpServerSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read settings: "+err.Error())
		return
	}

	// Merge patch.
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.PoolStart != nil {
		current.PoolStart = *req.PoolStart
	}
	if req.PoolEnd != nil {
		current.PoolEnd = *req.PoolEnd
	}
	if req.Gateway != nil {
		current.Gateway = *req.Gateway
	}
	if req.LeaseTimeSeconds != nil {
		current.LeaseTimeSeconds = *req.LeaseTimeSeconds
	}
	if req.Domain != nil {
		current.Domain = *req.Domain
	}
	if req.DNSServer != nil {
		current.DNSServer = *req.DNSServer
	}

	// Validate: if enabling, require a pool.
	if current.Enabled && (current.PoolStart == "" || current.PoolEnd == "") {
		writeError(w, http.StatusConflict, "pool_start and pool_end are required to enable the DHCP server")
		return
	}

	if err := cl.SetDhcpServerSettings(current); err != nil {
		writeError(w, http.StatusInternalServerError, "apply settings: "+err.Error())
		return
	}

	// Apply to the in-memory server instance if present.
	if srv := h.app.GetDhcpServer(); srv != nil {
		srv.UpdateConfig(current)
		if current.Enabled && cl.IsLeader() && !srv.Running() {
			srv.Start()
		} else if !current.Enabled && srv.Running() {
			srv.Stop()
		}
	}

	h.DhcpServerStatus(w, r)
}

// ─── GET /api/v1/dhcp/leases ─────────────────────────────────────────────────

func (h *Handler) DhcpLeases(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()

	// DHCP in-memory pool lives only on the leader. Followers proxy the request
	// so every node returns complete lease data regardless of current role.
	if cl != nil && !cl.IsLeader() {
		status, body, _, err := cl.ForwardWrite(r.Context(), http.MethodGet, r.URL.Path, nil, r.Header)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write(body) //nolint:errcheck
			return
		}
		// Leader unreachable — fall through and return empty list.
	}

	srv := h.app.GetDhcpServer()
	var leases []dhcp.Lease4
	if srv != nil {
		leases = srv.ActiveLeases()
	}
	if leases == nil {
		leases = []dhcp.Lease4{}
	}
	writeJSON(w, http.StatusOK, leases)
}

// ─── GET /api/v1/dhcp/static-assignments ─────────────────────────────────────

func (h *Handler) ListDhcpStaticAssignments(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	entries, err := cl.GetDhcpStaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []config.DHCPStaticAssignment{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// ─── POST /api/v1/dhcp/static-assignments ────────────────────────────────────

func (h *Handler) CreateDhcpStaticAssignment(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	var a config.DHCPStaticAssignment
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	a.MAC = strings.ToLower(a.MAC)

	if _, err := net.ParseMAC(a.MAC); err != nil {
		writeError(w, http.StatusBadRequest, "invalid MAC address")
		return
	}
	if net.ParseIP(a.IP) == nil {
		writeError(w, http.StatusBadRequest, "invalid IP address")
		return
	}

	// Collision check.
	existing, err := cl.GetDhcpStaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, e := range existing {
		if strings.EqualFold(e.MAC, a.MAC) {
			writeError(w, http.StatusConflict, "a static assignment for this MAC already exists")
			return
		}
	}

	if err := cl.UpsertDhcpStaticAssignment(a); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sync to in-memory server.
	if srv := h.app.GetDhcpServer(); srv != nil {
		cfg, _ := cl.GetDhcpServerSettings()
		srv.UpdateConfig(cfg)
	}

	writeJSON(w, http.StatusCreated, a)
}

// ─── DELETE /api/v1/dhcp/static-assignments/{mac} ────────────────────────────

func (h *Handler) DeleteDhcpStaticAssignment(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	mac := strings.ToLower(chi.URLParam(r, "mac"))
	if _, err := net.ParseMAC(mac); err != nil {
		writeError(w, http.StatusBadRequest, "invalid MAC address")
		return
	}

	// Existence check.
	existing, err := cl.GetDhcpStaticAssignments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for _, e := range existing {
		if strings.EqualFold(e.MAC, mac) {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "static assignment not found")
		return
	}

	if err := cl.DeleteDhcpStaticAssignment(mac); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sync to in-memory server.
	if srv := h.app.GetDhcpServer(); srv != nil {
		cfg, _ := cl.GetDhcpServerSettings()
		srv.UpdateConfig(cfg)
	}

	w.WriteHeader(http.StatusNoContent)
}
