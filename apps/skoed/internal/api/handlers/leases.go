// Package handlers — M6.5 lease replication endpoints (TS-LeaseRepl).
//
// GET /api/v1/leases         — full replicated lease snapshot + source meta
// GET /api/v1/leases/source  — just the source meta (leader, connector, last poll)
//
// Both endpoints read from the local bbolt snapshot — the same value every
// node carries after Raft commit — so reads are always served locally
// even on followers.
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/skoed/skoed/internal/dhcp"
)

// leasesSourceResponse is the wire shape of the `source` envelope in
// GET /api/v1/leases AND the body of GET /api/v1/leases/source.
type leasesSourceResponse struct {
	ConnectorKind string `json:"connector_kind"`
	LastPollUnix  int64  `json:"last_poll_unix"`
	SourceURL     string `json:"source_url,omitempty"`
	LeaderNodeID  string `json:"leader_node_id"`
}

// leasesResponse is the wire shape of GET /api/v1/leases.
type leasesResponse struct {
	Leases []dhcp.Lease         `json:"leases"`
	Source leasesSourceResponse `json:"source"`
}

// retryAfterSeconds is how long a 503 should ask the client to wait.
const retryAfterSeconds = 5

// GetLeases returns the replicated DHCP lease snapshot.
//
// FSIDs: FS-LeaseReplLeasesEndpointExposesSnapshot,
//        FS-LeaseReplFollowersServeReplicatedSnapshot,
//        FS-LeaseReplEmptyClusterReturns503.
func (h *Handler) GetLeases(w http.ResponseWriter, r *http.Request) {
	c := h.app.GetCluster()
	if c == nil {
		// Non-clustered mode (M1 / unit tests). Fall back to the local
		// manager — same M3.6 behaviour, no leader header.
		mgr := h.app.GetDhcpMgr()
		if mgr == nil {
			writeJSON(w, http.StatusOK, leasesResponse{Leases: []dhcp.Lease{}})
			return
		}
		writeJSON(w, http.StatusOK, leasesResponse{
			Leases: mgr.Snapshot(),
			Source: leasesSourceResponse{
				ConnectorKind: mgr.Source(),
				LastPollUnix:  mgr.LastPollAt().Unix(),
			},
		})
		return
	}

	leaderID := c.LeaderID()
	if leaderID == "" {
		writeNoLeader(w)
		return
	}
	w.Header().Set("x-leader-node-id", leaderID)

	snap, err := c.CurrentLeaseSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body := leasesResponse{
		Leases: []dhcp.Lease{},
		Source: leasesSourceResponse{
			LeaderNodeID: leaderID,
		},
	}
	if snap != nil {
		if snap.Leases != nil {
			body.Leases = snap.Leases
		}
		body.Source.ConnectorKind = snap.ConnectorKind
		body.Source.LastPollUnix = snap.PollUnix
		body.Source.SourceURL = snap.SourceURL
		// Always trust the live Raft leader id over the one stamped at
		// poll time so a recent failover surfaces immediately.
		if snap.LeaderNodeID != "" && leaderID == "" {
			body.Source.LeaderNodeID = snap.LeaderNodeID
		}
	}
	// If we have a local DHCP manager (any node that has the integration
	// enabled), use it to fill in connector_kind when no snapshot has
	// landed yet — operators see the configured connector immediately.
	if body.Source.ConnectorKind == "" {
		if mgr := h.app.GetDhcpMgr(); mgr != nil {
			body.Source.ConnectorKind = mgr.Source()
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// GetLeasesSource returns the source meta (connector kind, last poll
// time, current Raft leader).
//
// FSIDs: FS-LeaseReplSourceEndpointReportsLeader,
//        FS-LeaseReplLastPollUnixAdvances,
//        FS-LeaseReplSourceUnreachableKeepsLastGood.
func (h *Handler) GetLeasesSource(w http.ResponseWriter, r *http.Request) {
	c := h.app.GetCluster()
	if c == nil {
		mgr := h.app.GetDhcpMgr()
		if mgr == nil {
			writeJSON(w, http.StatusOK, leasesSourceResponse{})
			return
		}
		writeJSON(w, http.StatusOK, leasesSourceResponse{
			ConnectorKind: mgr.Source(),
			LastPollUnix:  mgr.LastPollAt().Unix(),
		})
		return
	}

	leaderID := c.LeaderID()
	if leaderID == "" {
		writeNoLeader(w)
		return
	}
	w.Header().Set("x-leader-node-id", leaderID)

	snap, err := c.CurrentLeaseSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := leasesSourceResponse{LeaderNodeID: leaderID}
	if snap != nil {
		out.ConnectorKind = snap.ConnectorKind
		out.LastPollUnix = snap.PollUnix
		out.SourceURL = snap.SourceURL
	}
	if out.ConnectorKind == "" {
		if mgr := h.app.GetDhcpMgr(); mgr != nil {
			out.ConnectorKind = mgr.Source()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// writeNoLeader emits the FS-LeaseReplEmptyClusterReturns503 body.
func writeNoLeader(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":               "no leader",
		"retry_after_seconds": retryAfterSeconds,
	})
}

// (kept for time import compatibility if needed in future)
var _ = time.Now
