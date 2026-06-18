package handlers

// HTTP handlers for mTLS certificate status and rotation.
// Routes: GET /api/v1/cluster/certs/status
//         POST /api/v1/cluster/certs/rotate

import (
	"errors"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/cluster"
)

// certStatusResp is the JSON shape for GET /api/v1/cluster/certs/status.
type certStatusResp struct {
	CAExpiresAt time.Time             `json:"ca_expires_at"`
	Nodes       []nodeCertStatusEntry `json:"nodes"`
}

type nodeCertStatusEntry struct {
	NodeID          string    `json:"node_id"`
	CertExpiresAt   time.Time `json:"cert_expires_at"`
	RotationPending bool      `json:"rotation_pending"`
}

// ClusterCertsStatus handles GET /api/v1/cluster/certs/status.
// Returns the CA and per-node certificate expiry. Requires cluster:admin scope.
func (h *Handler) ClusterCertsStatus(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	if !c.MTLSEnabled() {
		writeError(w, http.StatusServiceUnavailable, "mTLS is not enabled on this cluster")
		return
	}
	status := h.app.GetCertStatus()
	resp := certStatusResp{
		CAExpiresAt: status.CAExpiresAt,
		Nodes:       make([]nodeCertStatusEntry, 0, len(status.Nodes)),
	}
	for _, n := range status.Nodes {
		resp.Nodes = append(resp.Nodes, nodeCertStatusEntry{
			NodeID:          n.NodeID,
			CertExpiresAt:   n.CertExpiresAt,
			RotationPending: n.RotationPending,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ClusterCertsRotate handles POST /api/v1/cluster/certs/rotate.
// Triggers a cluster-wide mTLS certificate rotation. Returns 202 on success.
// Must run on the leader (forwarded by write-forward middleware).
func (h *Handler) ClusterCertsRotate(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	if !c.MTLSEnabled() {
		writeError(w, http.StatusServiceUnavailable, "mTLS is not enabled on this cluster")
		return
	}
	if err := h.app.RotateCerts(r.Context()); err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
