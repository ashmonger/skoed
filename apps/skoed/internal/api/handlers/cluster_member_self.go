package handlers

import (
	"errors"
	"net/http"

	"github.com/skoed/skoed/internal/cluster"
)

// selfMemberCluster is the part of *cluster.Cluster the members/self
// endpoint uses.
type selfMemberCluster interface {
	ValidateClusterSecret(secret string) bool
	ApplyForwardedSelfMemberUpdate(u cluster.SelfMemberUpdate) error
}

// ClusterInternalSelfMember handles POST /api/v1/cluster/_internal/members/self
// on the leader: a follower sets the peer API address and the capabilities in
// its own member record (TS-EncryptedClusterMesh "Internal API",
// TS-BlocklistContentDistribution §3.3). Authenticated by X-Cluster-Secret,
// which the main listener refuses when mTLS is enabled. On the peer
// listener, the node ID must also match the client certificate, so a node
// can only update its own record.
func (h *Handler) ClusterInternalSelfMember(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	serveSelfMember(w, r, c, func(msg string) { writeLeaderRedirect(w, c, msg) })
}

// serveSelfMember is ClusterInternalSelfMember once the cluster is known.
// notLeader writes the response when c is not the leader.
func serveSelfMember(w http.ResponseWriter, r *http.Request, c selfMemberCluster, notLeader func(msg string)) {
	if !c.ValidateClusterSecret(r.Header.Get("X-Cluster-Secret")) {
		writeError(w, http.StatusUnauthorized, "invalid cluster secret")
		return
	}
	var u cluster.SelfMemberUpdate
	if !decodeJSON(w, r, &u) {
		return
	}
	if u.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
		if cn := r.TLS.VerifiedChains[0][0].Subject.CommonName; cn != u.NodeID {
			writeError(w, http.StatusForbidden, "node_id does not match the client certificate")
			return
		}
	}
	if err := c.ApplyForwardedSelfMemberUpdate(u); err != nil {
		switch {
		case errors.Is(err, cluster.ErrNotLeader):
			notLeader("not the leader")
		case errors.Is(err, cluster.ErrUnknownMember):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
