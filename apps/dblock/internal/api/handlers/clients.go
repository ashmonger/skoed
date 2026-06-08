package handlers

import (
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dblock/dblock/internal/dhcp"
)

// GetClient returns the enriched record for one client IP. When no DHCP
// integration is configured OR the client's IP isn't in the lease cache,
// the response echoes the IP and sets source = "none". Anomalies for the
// IP (if any) are included.
//
// FSIDs: FS-ClientLookupReturnsEnrichedRecord, FS-ClientLookupFallsBackToIp,
//        FS-SpoofAnomaliesInResponse.
func (h *Handler) GetClient(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	if net.ParseIP(ip) == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid client IP"})
		return
	}

	type response struct {
		IP        string         `json:"ip"`
		MAC       string         `json:"mac"`
		Hostname  string         `json:"hostname"`
		ClientID  string         `json:"client_id"`
		Source    string         `json:"source"`
		LastSeen  *time.Time     `json:"last_seen,omitempty"`
		Anomalies []dhcp.Anomaly `json:"anomalies,omitempty"`
	}
	out := response{IP: ip, Source: "none"}

	if mgr := h.app.GetDhcpMgr(); mgr != nil {
		if l, ok := mgr.LookupByIP(ip); ok {
			out.MAC = l.MAC
			out.Hostname = l.Hostname
			out.ClientID = l.ClientID
			out.Source = l.Source
			ls := time.Now()
			out.LastSeen = &ls
		}
		if anomalies := mgr.AnomaliesForIP(ip); len(anomalies) > 0 {
			out.Anomalies = anomalies
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ListAnomalies returns all anti-spoof anomalies, newest first (well —
// unordered for now; the UI sorts by detected_at).
//
// FSIDs: FS-SpoofMacChangedForKnownClientId, FS-SpoofClientIdChangedForKnownMac,
//        FS-SpoofNewMacForExistingHostname, FS-SpoofAnomaliesInResponse.
func (h *Handler) ListAnomalies(w http.ResponseWriter, r *http.Request) {
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		writeJSON(w, http.StatusOK, []dhcp.Anomaly{})
		return
	}
	writeJSON(w, http.StatusOK, mgr.Anomalies())
}

// AcknowledgeAnomaly marks one anomaly as acknowledged (operator
// dismissed). The anomaly remains in the list but with AcknowledgedAt
// set, so the Dashboard alert card can filter it out.
//
// FSID: FS-SpoofAcknowledge.
func (h *Handler) AcknowledgeAnomaly(w http.ResponseWriter, r *http.Request) {
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		http.Error(w, "DHCP integration disabled", http.StatusNotFound)
		return
	}
	id := chi.URLParam(r, "id")
	if !mgr.Acknowledge(id, time.Now()) {
		http.Error(w, "anomaly not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
func (h *Handler) LeaseSnapshot(w http.ResponseWriter, r *http.Request) {
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		writeJSON(w, http.StatusOK, []dhcp.Lease{})
		return
	}
	writeJSON(w, http.StatusOK, mgr.Snapshot())
}
