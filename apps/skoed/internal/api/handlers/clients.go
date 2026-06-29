package handlers

import (
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skoed/skoed/internal/dhcp"
	"github.com/skoed/skoed/internal/filter"
)

// clientResponse is the M3.6 + M6.5 wire shape served by
// GET /api/v1/clients/{ip}. M6.5 (TS-LeaseOrigin, TS-Dhcpv6Lease) adds
// the origin / origin_confidence / ipv6_addresses / duid / is_dual_stack
// fields. All M6.5 fields are omitempty so the M3.6 wire shape survives
// (FS-Dhcpv6LeaseV6DisabledLegacyShapeUnchanged).
type clientResponse struct {
	IP               string         `json:"ip"`
	MAC              string         `json:"mac"`
	Hostname         string         `json:"hostname"`
	ClientID         string         `json:"client_id"`
	Source           string         `json:"source"`
	LastSeen         *time.Time     `json:"last_seen,omitempty"`
	Anomalies        []dhcp.Anomaly `json:"anomalies,omitempty"`
	Origin           string         `json:"origin,omitempty"`
	OriginConfidence string         `json:"origin_confidence,omitempty"`
	IPv6Addresses    []string       `json:"ipv6_addresses,omitempty"`
	DUID             string         `json:"duid,omitempty"`
	IsDualStack      bool           `json:"is_dual_stack,omitempty"`
	// M6.5 (TS-BlockDyn): profiles currently matched for this client IP.
	ProfileIDs []string `json:"profile_ids,omitempty"`
}

// GetClient returns the enriched record for one client IP. When no DHCP
// integration is configured OR the client's IP isn't in the lease cache,
// the response echoes the IP and sets source = "none". Anomalies for the
// IP (if any) are included.
//
// M6.5 — TS-Dhcpv6Lease: the `ip` path parameter accepts both v4 and v6
// literals; lookup falls back to the v6 index when the v4 map misses.
//
// FSIDs: FS-ClientLookupReturnsEnrichedRecord, FS-ClientLookupFallsBackToIp,
//        FS-SpoofAnomaliesInResponse, FS-LeaseOriginClientLookupExposesFields,
//        FS-LeaseOriginUnknownClientOmitsOrigin, FS-Dhcpv6LeaseV6OnlyClientLookupByV6.
func (h *Handler) GetClient(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	if net.ParseIP(ip) == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid client IP"})
		return
	}

	out := clientResponse{Source: "none"}
	out.IP = ip

	if mgr := h.app.GetDhcpMgr(); mgr != nil {
		if l, ok := mgr.LookupByIP(ip); ok {
			out.MAC = l.MAC
			out.Hostname = l.Hostname
			out.ClientID = l.ClientID
			out.Source = l.Source
			out.Origin = string(l.Origin)
			out.OriginConfidence = string(l.OriginConfidence)
			out.IPv6Addresses = l.IPv6Addresses
			out.DUID = l.DUID
			out.IsDualStack = l.IsDualStack
			// For v6-only leases (Lease.IP empty) the caller asked by v6
			// literal. Per FS-Dhcpv6LeaseV6OnlyClientLookupByV6 the `ip`
			// field MUST be empty in that case — match what the
			// underlying lease says.
			out.IP = l.IP
			ls := time.Now()
			out.LastSeen = &ls
		}
		if anomalies := mgr.AnomaliesForIP(ip); len(anomalies) > 0 {
			out.Anomalies = anomalies
		}
	}
	if eng := h.app.GetFilterEng(); eng != nil {
		parsedIP := net.ParseIP(ip)
		if parsedIP != nil {
			id := filter.ClientIdentity{
				ClientID: out.ClientID,
				MAC:      out.MAC,
				Hostname: out.Hostname,
			}
			out.ProfileIDs = eng.ProfilesMatching(parsedIP, id)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ListClients returns every known DHCP client in the snapshot, sorted by
// IP. Carries the same M6.5 fields as GetClient for SPA badge rendering
// (FS-LeaseOriginClientsListSurfacesBadge, FS-Dhcpv6LeaseClientsPageShowsV6Column).
//
// FSIDs: FS-LeaseOriginClientsListSurfacesBadge,
//        FS-Dhcpv6LeaseClientsPageShowsV6Column.
func (h *Handler) ListClients(w http.ResponseWriter, r *http.Request) {
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		writeJSON(w, http.StatusOK, []clientResponse{})
		return
	}
	leases := mgr.Snapshot()
	out := make([]clientResponse, 0, len(leases))
	for _, l := range leases {
		out = append(out, clientResponse{
			IP:               l.IP,
			MAC:              l.MAC,
			Hostname:         l.Hostname,
			ClientID:         l.ClientID,
			Source:           l.Source,
			Origin:           string(l.Origin),
			OriginConfidence: string(l.OriginConfidence),
			IPv6Addresses:    l.IPv6Addresses,
			DUID:             l.DUID,
			IsDualStack:      l.IsDualStack,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		// v4-first ordering; v6-only rows sort after v4s.
		if (out[i].IP == "") != (out[j].IP == "") {
			return out[i].IP != ""
		}
		if out[i].IP != out[j].IP {
			return out[i].IP < out[j].IP
		}
		// Tie-break on the first v6 address so dual-stack diffing stays stable.
		var a, b string
		if len(out[i].IPv6Addresses) > 0 {
			a = out[i].IPv6Addresses[0]
		}
		if len(out[j].IPv6Addresses) > 0 {
			b = out[j].IPv6Addresses[0]
		}
		return a < b
	})
	writeJSON(w, http.StatusOK, out)
}

// ListAnomalies returns all anti-spoof anomalies, newest first (well —
// unordered for now; the UI sorts by detected_at).
//
// M6.5 (FS-LeaseReplFollowerAnomaliesMatchLeader): when a cluster is
// wired we read from the replicated bbolt bucket so every node returns
// the same set the leader observed. Single-node / unit-test mode falls
// back to the in-memory manager view.
//
// FSIDs: FS-SpoofMacChangedForKnownClientId, FS-SpoofClientIdChangedForKnownMac,
//        FS-SpoofNewMacForExistingHostname, FS-SpoofAnomaliesInResponse,
//        FS-LeaseReplFollowerAnomaliesMatchLeader.
func (h *Handler) ListAnomalies(w http.ResponseWriter, r *http.Request) {
	kindFilter := r.URL.Query().Get("kind")

	var all []dhcp.Anomaly
	if c := h.app.GetCluster(); c != nil {
		anomalies, err := c.CurrentLeaseAnomalies()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if anomalies == nil {
			anomalies = []dhcp.Anomaly{}
		}
		all = anomalies
	} else {
		mgr := h.app.GetDhcpMgr()
		if mgr == nil {
			writeJSON(w, http.StatusOK, []dhcp.Anomaly{})
			return
		}
		all = mgr.Anomalies()
	}

	if kindFilter != "" {
		filtered := all[:0]
		for _, a := range all {
			if string(a.Kind) == kindFilter {
				filtered = append(filtered, a)
			}
		}
		all = filtered
	}
	if all == nil {
		all = []dhcp.Anomaly{}
	}
	writeJSON(w, http.StatusOK, all)
}

// AcknowledgeAnomaly marks one anomaly as acknowledged (operator
// dismissed). The anomaly remains in the list but with AcknowledgedAt
// set, so the Dashboard alert card can filter it out.
//
// M6.5 (TS-LeaseRepl, FS-LeaseReplFollowerWriteForwarded): when a
// cluster is wired the acknowledgement goes through Raft so every node
// converges; the leader-forward middleware ensures this handler only
// runs on the leader. The local manager is used as a fall-back in
// single-node / non-clustered mode for backward compatibility.
//
// FSIDs: FS-SpoofAcknowledge, FS-LeaseReplFollowerWriteForwarded.
func (h *Handler) AcknowledgeAnomaly(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing anomaly id", http.StatusBadRequest)
		return
	}
	if c := h.app.GetCluster(); c != nil {
		if err := c.AcknowledgeAnomaly(id, time.Now()); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		// Spec scenario expects 200, not 204. The body is empty.
		w.WriteHeader(http.StatusOK)
		return
	}
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		http.Error(w, "DHCP integration disabled", http.StatusNotFound)
		return
	}
	if !mgr.Acknowledge(id, time.Now()) {
		http.Error(w, "anomaly not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ExportReservations serves the current lease snapshot as
// operator-pasteable static-reservation syntax for dnsmasq / Kea /
// generic JSON. Empty body when DHCP integration is disabled.
func (h *Handler) ExportReservations(w http.ResponseWriter, r *http.Request) {
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		http.Error(w, "DHCP integration disabled", http.StatusNotFound)
		return
	}
	format := dhcp.ExportFormat(r.URL.Query().Get("format"))
	if format == "" {
		format = dhcp.ExportDnsmasq
	}
	body, err := dhcp.Export(mgr.Snapshot(), format)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch format {
	case dhcp.ExportKea, dhcp.ExportJSON:
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// LeaseSnapshot is the test/debug endpoint exposed for the acceptance
// harness. Returns the raw Lease snapshot (no Anomalies envelope).
// Merges external DHCP manager leases with built-in server leases.
func (h *Handler) LeaseSnapshot(w http.ResponseWriter, r *http.Request) {
	// Built-in DHCP leases live only in-memory on the leader. Proxy to the
	// leader so followers return complete data, same as DhcpLeases does.
	cl := h.app.GetCluster()
	if cl != nil && !cl.IsLeader() {
		status, body, _, err := cl.ForwardWrite(r.Context(), http.MethodGet, r.URL.Path, nil, r.Header)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write(body) //nolint:errcheck
			return
		}
		// Leader unreachable — fall through to local data.
	}

	var leases []dhcp.Lease

	if mgr := h.app.GetDhcpMgr(); mgr != nil {
		leases = mgr.Snapshot()
	}

	if srv := h.app.GetDhcpServer(); srv != nil {
		for _, l4 := range srv.ActiveLeases() {
			leases = append(leases, dhcp.Lease{
				IP:       l4.IP,
				MAC:      l4.MAC,
				Hostname: l4.Hostname,
				Source:   "builtin",
				Origin:   dhcp.Origin(l4.Origin),
			})
		}
	}

	if leases == nil {
		leases = []dhcp.Lease{}
	}
	writeJSON(w, http.StatusOK, leases)
}
