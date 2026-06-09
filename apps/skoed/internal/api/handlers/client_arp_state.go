// client_arp_state.go — GET /api/v1/clients/{ip}/arp-state (TS-ArpCheck).
package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// arpStateResponse is the wire shape of GET /api/v1/clients/{ip}/arp-state.
type arpStateResponse struct {
	IP               string `json:"ip"`
	MacDhcp          string `json:"mac_dhcp"`
	MacKernel        string `json:"mac_kernel"`
	KernelState      string `json:"kernel_state"`
	LastObservedUnix int64  `json:"last_observed_unix"`
	Anomaly          string `json:"anomaly,omitempty"`
}

// GetClientArpState implements GET /api/v1/clients/{ip}/arp-state.
//
// FSIDs: FS-ArpCheckArpStateAgreesWithLease, FS-ArpCheckArpMacMismatchFlagsAnomaly,
//        FS-ArpCheckNdpMacMismatchFlagsAnomaly, FS-ArpCheckGhostLeaseLongLivedButNeverInKernel,
//        FS-ArpCheckUnseenByKernelFreshLeaseStaysQuiet, FS-ArpCheckUnseenByKernelAfterGracePeriod,
//        FS-ArpCheckGracefulWhenNetlinkUnavailable, FS-ArpCheckUnknownIpReturns404.
func (h *Handler) GetClientArpState(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	mgr := h.app.GetDhcpMgr()
	if mgr == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no lease for %s", ip))
		return
	}
	entry, ok := mgr.ArpState(ip)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no lease for %s", ip))
		return
	}
	writeJSON(w, http.StatusOK, arpStateResponse{
		IP:               entry.IP,
		MacDhcp:          entry.MacDhcp,
		MacKernel:        entry.MacKernel,
		KernelState:      entry.KernelState,
		LastObservedUnix: entry.LastObservedUnix,
		Anomaly:          entry.Anomaly,
	})
}
