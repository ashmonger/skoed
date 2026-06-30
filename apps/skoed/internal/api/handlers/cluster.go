package handlers

// HTTP handlers for the /api/v1/cluster/* endpoints (M2).
//
// All handlers degrade gracefully when the binary is running in non-clustered
// (M1) mode: GetCluster() returns nil → we surface 503 so the operator gets a
// clear "this build has no cluster" signal instead of an opaque crash.
//
// Mutation handlers (join, leadership transfer, remove node) MUST run on the
// Raft leader. They detect non-leader state and return 409 with a
// LeaderRedirect body so the caller can retry against the leader.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/cluster"
	"github.com/go-chi/chi/v5"
)

// ─── response shapes ─────────────────────────────────────────────────────────

type leaderRedirectResp struct {
	Error         string `json:"error"`
	LeaderID      string `json:"leader_id,omitempty"`
	LeaderAddress string `json:"leader_address"`
}

type createTokenResp struct {
	Token         string `json:"token"`
	ExpiresAt     string `json:"expires_at"`
	LeaderAddress string `json:"leader_address"`
}

type joinReq struct {
	Token       string `json:"token"`
	NodeID      string `json:"node_id"`
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
}

type joinResp struct {
	ClusterID         string `json:"cluster_id"`
	CommitIndexAtJoin uint64 `json:"commit_index_at_join"`
}

// mtlsBootstrapReq is the POST body for /api/v1/cluster/mtls-bootstrap.
type mtlsBootstrapReq struct {
	Token  string `json:"token"`
	NodeID string `json:"node_id"`
}

// mtlsBootstrapResp ships the cluster CA + a freshly-minted leaf cert
// to the joining node. The token is NOT consumed; the subsequent
// /api/v1/cluster/join call (after Raft is up) does that.
type mtlsBootstrapResp struct {
	CACertPEM   []byte `json:"ca_cert_pem"`
	LeafCertPEM []byte `json:"leaf_cert_pem"`
	LeafKeyPEM  []byte `json:"leaf_key_pem"`
}

type clusterNodeEntry struct {
	NodeID      string `json:"node_id"`
	Role        string `json:"role"` // leader | follower | learner | removed
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
	LastContact string `json:"last_contact"` // RFC3339
	CommitIndex uint64 `json:"commit_index"`
	SyncState   string `json:"sync_state"` // in_sync | behind | unreachable
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

type clusterStatusResp struct {
	ClusterID string             `json:"cluster_id"`
	RaftTerm  uint64             `json:"raft_term"`
	LeaderID  string             `json:"leader_id"`
	Nodes     []clusterNodeEntry `json:"nodes"`
}

type transferLeadershipReq struct {
	TargetNodeID string `json:"target_node_id"`
}

type domainCount struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

type clientCount struct {
	Client string `json:"client"`
	Count  int    `json:"count"`
}

type hourlyAggregateResp struct {
	NodeID     string        `json:"node_id"`
	HourStart  string        `json:"hour_start"` // RFC3339
	Total      int           `json:"total"`
	Blocked    int           `json:"blocked"`
	Forwarded  int           `json:"forwarded"`
	Cached     int           `json:"cached"`
	Local      int           `json:"local"`
	TopDomains []domainCount `json:"top_domains"`
	TopClients []clientCount `json:"top_clients"`
}

type clusterTotals struct {
	Total     int `json:"total"`
	Blocked   int `json:"blocked"`
	Forwarded int `json:"forwarded"`
	Cached    int `json:"cached"`
	Local     int `json:"local"`
}

type clusterStatsResp struct {
	WindowFrom    string                `json:"window_from"`
	WindowTo      string                `json:"window_to"`
	PerNode       []hourlyAggregateResp `json:"per_node"`
	ClusterTotals clusterTotals         `json:"cluster_totals"`
	TopDomains    []domainCount         `json:"top_domains"`
	TopClients    []clientCount         `json:"top_clients"`
}

type mergedQueryEntry struct {
	NodeID      string    `json:"node_id"`
	Timestamp   time.Time `json:"timestamp"`
	Client      string    `json:"client"`
	Domain      string    `json:"domain"`
	QueryType   string    `json:"query_type"`
	Outcome     string    `json:"outcome"`
	BlocklistID string    `json:"blocklist_id,omitempty"`
}

type perNodeFanOut struct {
	NodeID     string `json:"node_id"`
	Status     string `json:"status"` // ok | timeout | error
	EntryCount int    `json:"entry_count"`
	Error      string `json:"error,omitempty"`
}

type clusterQueryLogResp struct {
	Entries []mergedQueryEntry `json:"entries"`
	Total   int                `json:"total"`
	PerNode []perNodeFanOut    `json:"per_node"`
}

// ─── small helpers ───────────────────────────────────────────────────────────

// requireCluster fetches the *cluster.Cluster and surfaces a 503 when the
// binary is running without a cluster wired in. Returns nil on failure so the
// caller can simply `return`.
func (h *Handler) requireCluster(w http.ResponseWriter) *cluster.Cluster {
	c := h.app.GetCluster()
	if c == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not enabled on this node")
		return nil
	}
	return c
}

// writeLeaderRedirect emits a 409 with the current leader's reachable API
// address so the client can retry against the right node.
func writeLeaderRedirect(w http.ResponseWriter, c *cluster.Cluster, msg string) {
	writeJSON(w, http.StatusConflict, leaderRedirectResp{
		Error:         msg,
		LeaderID:      c.LeaderID(),
		LeaderAddress: c.LeaderAPIAddress(),
	})
}

// ─── tokens ──────────────────────────────────────────────────────────────────

// CreateJoinToken handles POST /api/v1/cluster/tokens. Only the leader can
// issue tokens (because issuance writes via Raft); the write-forward
// middleware ensures this handler only runs on the leader.
func (h *Handler) CreateJoinToken(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	issuedBy := "admin"

	res, err := c.CreateJoinToken(issuedBy)
	if err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, createTokenResp{
		Token:         res.Token,
		ExpiresAt:     res.ExpiresAt.UTC().Format(time.RFC3339),
		LeaderAddress: res.LeaderAddress,
	})
}

// ─── node self-join (follower side) ──────────────────────────────────────────

// nodeJoinClusterReq is the body for POST /api/v1/node/join-cluster.
type nodeJoinClusterReq struct {
	Token         string `json:"token"`
	LeaderAddress string `json:"leader_address"`
}

// nodeJoinClusterResp is the body returned on success.
type nodeJoinClusterResp struct {
	ClusterID string `json:"cluster_id"`
}

// NodeJoinCluster handles POST /api/v1/node/join-cluster.
// The caller provides a join payload (token + leader_address) generated by the
// leader's "Generate token" flow. This handler reads the local node's own
// node_id, raft_address, and api_address from its configuration, then forwards
// an enrolment request to the leader on behalf of this node.
//
// This is the follower-side counterpart of ClusterJoin (which is the leader-side
// handler that actually does AddVoter). Together they power the web-UI join flow:
// the administrator pastes the payload on the new node's Cluster page and clicks
// "Join" — no SSH or config-file edit needed.
func (h *Handler) NodeJoinCluster(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	// Reject if this node is already part of a multi-node cluster.
	if len(c.MembersFromRaftConfig()) > 1 {
		writeError(w, http.StatusConflict, "node is already a cluster member")
		return
	}

	var req nodeJoinClusterReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" || req.LeaderAddress == "" {
		writeError(w, http.StatusBadRequest, "token and leader_address are required")
		return
	}

	node := c.Node()
	joinBody, _ := json.Marshal(joinReq{
		Token:       req.Token,
		NodeID:      node.Node.ID,
		RaftAddress: node.Node.RaftAddress,
		APIAddress:  node.Node.APIAddress,
	})

	// Reset local Raft state before calling the leader. The leader's AddVoter
	// waits for the joining node to catch up, so the joining node must be
	// running a clean Raft (no existing log/snapshot) that is ready to accept
	// AppendEntries from the leader.
	if err := c.ResetRaftForJoin(); err != nil {
		writeError(w, http.StatusInternalServerError, "reset raft state: "+err.Error())
		return
	}

	resp, err := http.Post( //nolint:noctx
		strings.TrimRight(req.LeaderAddress, "/")+"/api/v1/cluster/join",
		"application/json",
		strings.NewReader(string(joinBody)),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, "leader unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// Forward token-rejection (403) from the leader as-is.
	if resp.StatusCode == http.StatusForbidden {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(respBody)
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway,
			fmt.Sprintf("leader returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))))
		return
	}

	var joinR joinResp
	_ = json.Unmarshal(respBody, &joinR)
	writeJSON(w, http.StatusOK, nodeJoinClusterResp{ClusterID: joinR.ClusterID})
}

// ─── join ────────────────────────────────────────────────────────────────────

// ClusterJoin handles POST /api/v1/cluster/join. Validates the token, adds
// the new node as a Raft voter, and records it in the replicated members
// bucket. Must be invoked on the leader.
func (h *Handler) ClusterJoin(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	var req joinReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" || req.NodeID == "" || req.RaftAddress == "" {
		writeError(w, http.StatusBadRequest, "token, node_id and raft_address are required")
		return
	}

	res, err := c.EnrollNode(req.Token, req.NodeID, req.RaftAddress, req.APIAddress)
	if err != nil {
		// Token validation failure → 403, as per the OpenAPI contract.
		var je *cluster.JoinError
		if errors.As(err, &je) {
			writeError(w, http.StatusForbidden, je.Error())
			return
		}
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, joinResp{
		ClusterID:         res.ClusterID,
		CommitIndexAtJoin: res.CommitIndexAtJoin,
	})
}

// ClusterMTLSBootstrap handles POST /api/v1/cluster/mtls-bootstrap.
// In mTLS-enabled clusters, joining nodes call this BEFORE bringing up
// their own Raft so they can write the cluster CA + leaf cert to disk
// and start a TLS-wrapped Raft transport. The token is validated but
// NOT consumed — the subsequent /join call consumes it and runs AddVoter.
func (h *Handler) ClusterMTLSBootstrap(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	var req mtlsBootstrapReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" || req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "token and node_id are required")
		return
	}
	ca, leaf, key, err := c.MintLeafForJoin(req.Token, req.NodeID)
	if err != nil {
		var je *cluster.JoinError
		if errors.As(err, &je) {
			writeError(w, http.StatusForbidden, je.Error())
			return
		}
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mtlsBootstrapResp{
		CACertPEM:   ca,
		LeafCertPEM: leaf,
		LeafKeyPEM:  key,
	})
}

// ─── health (cluster-aware admin overview) ──────────────────────────────

// clusterHealthResp is the body of GET /api/v1/cluster/health.
type clusterHealthResp struct {
	Status           string `json:"status"`            // ok | degraded
	NodeID           string `json:"node_id"`
	Role             string `json:"role"`              // leader | follower
	Mode             string `json:"mode"`              // single-node | cluster
	HasLeader        bool   `json:"has_leader"`
	Members          int    `json:"members"`
	ReachableMembers int    `json:"reachable_members"`
	RaftTerm         uint64 `json:"raft_term"`
	CommitIndex      uint64 `json:"commit_index"`
	Version          string `json:"version"`
	Commit           string `json:"commit"`
}

// ClusterHealth handles GET /api/v1/cluster/health — an authenticated
// cluster-aware health summary. Distinct from the unauthenticated /health
// liveness probe used by load balancers: /cluster/health probes every peer
// and reports the cluster's quorum / reachability state.
func (h *Handler) ClusterHealth(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	servers := c.MembersFromRaftConfig()
	leaderID := c.LeaderID()
	localID := c.Node().Node.ID

	version, commit := h.app.GetBuildVersion()
	resp := clusterHealthResp{
		NodeID:      localID,
		Role:        "follower",
		HasLeader:   leaderID != "",
		Members:     len(servers),
		RaftTerm:    c.Raft().CurrentTerm(),
		CommitIndex: c.Raft().CommitIndex(),
		Version:     version,
		Commit:      commit,
	}
	if localID == leaderID {
		resp.Role = "leader"
	}
	if resp.Members <= 1 {
		resp.Mode = "single-node"
	} else {
		resp.Mode = "cluster"
	}

	// Count reachable peers. The local node always counts as reachable.
	reachable := 1
	if resp.Members > 1 {
		for _, r := range probeAllPeers(c, localID, c.ClusterSecret()) {
			if r.alive {
				reachable++
			}
		}
	}
	resp.ReachableMembers = reachable

	// Status is "ok" iff there's a leader and every member responded.
	if resp.HasLeader && resp.ReachableMembers == resp.Members {
		resp.Status = "ok"
	} else {
		resp.Status = "degraded"
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─── internal aggregate ingestion (follower → leader) ───────────────────

// ClusterInternalAggregates handles POST /api/v1/cluster/_internal/aggregates.
// Followers POST their hourly HourAggregate here so the leader can apply it
// via Raft and replicate it to every node. Auth is the shared cluster
// secret in the X-Cluster-Secret header — replicated to every member at
// bootstrap, so peers can talk to each other without admin credentials.
func (h *Handler) ClusterInternalAggregates(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	if !c.ValidateClusterSecret(r.Header.Get("X-Cluster-Secret")) {
		writeError(w, http.StatusUnauthorized, "invalid cluster secret")
		return
	}
	var agg cluster.HourAggregate
	if !decodeJSON(w, r, &agg) {
		return
	}
	if agg.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if err := c.ApplyForwardedAggregate(agg); err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── self (lightweight local view, used by peer probes) ───────────────────

// clusterSelfResp is the shape returned by GET /api/v1/cluster/self. It
// reports only this node's local Raft state, so peer-to-peer probes never
// recurse back into the full ClusterStatus fan-out.
type clusterSelfResp struct {
	NodeID      string `json:"node_id"`
	Role        string `json:"role"`
	RaftTerm    uint64 `json:"raft_term"`
	CommitIndex uint64 `json:"commit_index"`
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

// ClusterSelf handles GET /api/v1/cluster/self. It is intentionally minimal:
// the data it returns is a strict subset of /cluster/status restricted to
// the local node, so it is safe to expose to peer probes without auth
// being a barrier between cluster members. (Auth is still required for
// admin probes — peers forward the admin's Authorization header.)
func (h *Handler) ClusterSelf(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	role := "follower"
	if c.Node().Node.ID == c.LeaderID() {
		role = "leader"
	}
	version, commit := h.app.GetBuildVersion()
	writeJSON(w, http.StatusOK, clusterSelfResp{
		NodeID:      c.Node().Node.ID,
		Role:        role,
		RaftTerm:    c.Raft().CurrentTerm(),
		CommitIndex: c.Raft().CommitIndex(),
		Version:     version,
		Commit:      commit,
	})
}

// ─── status ──────────────────────────────────────────────────────────────────

// ClusterStatus handles GET /api/v1/cluster/status. Built from a combination
// of the Raft configuration (authoritative member list), the replicated
// members bucket (api_address mapping) and Raft Stats (per-peer last contact).
//
// Limitation (M2): hashicorp/raft does not expose per-peer commit_index in
// Stats(). We report this node's commit index for every entry and rely on the
// sync_state field to flag laggers. A future milestone can switch to a survey
// API that polls each peer for an accurate per-peer view.
func (h *Handler) ClusterStatus(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	servers := c.MembersFromRaftConfig()
	leaderID := c.LeaderID()
	localCommit := c.Raft().CommitIndex()
	localID := c.Node().Node.ID
	localVersion, localGitCommit := h.app.GetBuildVersion()

	out := clusterStatusResp{
		ClusterID: "",
		RaftTerm:  c.Raft().CurrentTerm(),
		LeaderID:  leaderID,
		Nodes:     make([]clusterNodeEntry, 0, len(servers)),
	}

	now := time.Now().UTC()

	// Resolve api_address for every peer before issuing probes — we need
	// somewhere to send the GET.
	type peerView struct {
		id, apiAddr, raftAddr string
	}
	peers := make([]peerView, 0, len(servers))
	for _, s := range servers {
		id := string(s.ID)
		pv := peerView{id: id, raftAddr: string(s.Address)}
		if m, err := c.Store().MemberByID(id); err == nil && m != nil {
			pv.apiAddr = m.APIAddress
			if pv.raftAddr == "" {
				pv.raftAddr = m.RaftAddress
			}
		}
		peers = append(peers, pv)
	}

	// Probe every peer (except self) in parallel to learn liveness and
	// commit_index. hashicorp/raft doesn't expose per-peer commit_index in
	// Stats(), so we ask each peer directly via /cluster/self.
	// Use the cluster secret for inter-node auth — session tokens are
	// node-local and would be rejected by the target node.
	results := probeAllPeers(c, localID, c.ClusterSecret())

	for _, p := range peers {
		entry := clusterNodeEntry{
			NodeID:      p.id,
			RaftAddress: p.raftAddr,
			APIAddress:  p.apiAddr,
			CommitIndex: localCommit,
			LastContact: now.UTC().Format(time.RFC3339),
		}

		if p.id == leaderID {
			entry.Role = "leader"
		} else {
			entry.Role = "follower"
		}

		switch {
		case p.id == localID:
			entry.SyncState = "in_sync"
			entry.Version = localVersion
			entry.Commit = localGitCommit
		default:
			r := results[p.id]
			if !r.alive {
				// We don't know the unreachable peer's commit; signal that
				// by zeroing the field rather than echoing the local value.
				entry.CommitIndex = 0
				entry.SyncState = "unreachable"
			} else {
				entry.CommitIndex = r.commitIndex
				entry.Version = r.version
				entry.Commit = r.commit
				if entry.CommitIndex < localCommit {
					entry.SyncState = "behind"
				} else {
					entry.SyncState = "in_sync"
				}
			}
		}

		out.Nodes = append(out.Nodes, entry)
	}

	// Stable ordering keeps the test fixtures deterministic.
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].NodeID < out.Nodes[j].NodeID })

	writeJSON(w, http.StatusOK, out)
}

// peerProbeResult is what one probeAllPeers entry yields. alive is false
// when the peer didn't respond within the probe timeout or had no known
// api_address; commitIndex is 0 in that case.
type peerProbeResult struct {
	alive       bool
	commitIndex uint64
	version     string
	commit      string
}

// probeAllPeers fan-outs probePeer to every cluster member except localID
// using the shared HTTP timeout. Uses the cluster secret for inter-node auth
// so that node-local session tokens do not need to be forwarded. Missing
// api_address counts as not-alive but is still keyed in the returned map so
// the caller can iterate the full member list uniformly. Both ClusterStatus
// and ClusterHealth use this; keeping the pattern in one place avoids drift
// between the two views.
func probeAllPeers(c *cluster.Cluster, localID, clusterSecret string) map[string]peerProbeResult {
	servers := c.MembersFromRaftConfig()
	out := make(map[string]peerProbeResult, len(servers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 800 * time.Millisecond}
	for _, s := range servers {
		id := string(s.ID)
		if id == localID {
			continue
		}
		m, _ := c.Store().MemberByID(id)
		if m == nil || m.APIAddress == "" {
			mu.Lock()
			out[id] = peerProbeResult{}
			mu.Unlock()
			continue
		}
		apiAddr := m.APIAddress
		raftHost, _, _ := net.SplitHostPort(m.RaftAddress)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := probePeer(client, apiAddr, clusterSecret, raftHost)
			mu.Lock()
			out[id] = peerProbeResult{alive: r.alive, commitIndex: r.commitIndex, version: r.version, commit: r.commit}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// probePeer GETs the peer's /api/v1/cluster/self endpoint, a deliberately
// minimal handler that returns only the local node's view (no fan-out, no
// recursion). The peer's authoritative commit_index is returned alongside
// the liveness signal so the caller can compute behind / in_sync accurately.
// clusterSecret is sent as X-Cluster-Secret so the peer accepts the probe
// without a user session — session tokens are node-local and would be rejected.
func probePeer(client *http.Client, apiAddr, clusterSecret string, raftHostFallback ...string) (out struct {
	alive       bool
	commitIndex uint64
	version     string
	commit      string
}) {
	base := apiBase(apiAddr, raftHostFallback...)
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/cluster/self", nil)
	if err != nil {
		return
	}
	if clusterSecret != "" {
		req.Header.Set("X-Cluster-Secret", clusterSecret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var body clusterSelfResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	out.alive = true
	out.commitIndex = body.CommitIndex
	out.version = body.Version
	out.commit = body.Commit
	return
}

// ─── leadership transfer ─────────────────────────────────────────────────────

// TransferLeadership handles POST /api/v1/cluster/leadership/transfer.
// Returns 204 on success, 409 when this node is not the leader OR the target
// is not in the cluster, 504 when the transfer does not complete in time.
func (h *Handler) TransferLeadership(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	var req transferLeadershipReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TargetNodeID == "" {
		writeError(w, http.StatusBadRequest, "target_node_id is required")
		return
	}

	// Validate the target is currently in the Raft configuration so we can
	// surface a clean 409 instead of an opaque raft error.
	if !nodeInConfig(c, req.TargetNodeID) {
		writeLeaderRedirect(w, c, "target node not in cluster")
		return
	}

	err := c.TransferLeadership(req.TargetNodeID)
	if err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		// hashicorp/raft surfaces deadline-style errors as plain errors. Map
		// anything that looks like a timeout to 504; everything else to 500.
		if isTimeoutErr(err) {
			writeError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "timeout") || strings.Contains(s, "deadline") || strings.Contains(s, "did not complete")
}

func nodeInConfig(c *cluster.Cluster, id string) bool {
	for _, s := range c.MembersFromRaftConfig() {
		if string(s.ID) == id {
			return true
		}
	}
	return false
}

// ─── remove node ─────────────────────────────────────────────────────────────

// RemoveNode handles DELETE /api/v1/cluster/nodes/{node_id}. Refuses to
// remove the leader (admin must transfer first) and refuses to remove a node
// that is not in the cluster.
func (h *Handler) RemoveNode(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	nodeID := chi.URLParam(r, "node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	if nodeID == c.LeaderID() {
		writeError(w, http.StatusConflict, "cannot remove the current leader; transfer leadership first")
		return
	}

	if !nodeInConfig(c, nodeID) {
		writeError(w, http.StatusNotFound, "node not in cluster")
		return
	}

	if err := c.RemoveMember(nodeID); err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			writeLeaderRedirect(w, c, "not the leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─── stats ───────────────────────────────────────────────────────────────────

// ClusterStats handles GET /api/v1/cluster/stats. Served from the local bbolt
// replica so the call succeeds even when the leader is unreachable.
func (h *Handler) ClusterStats(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	q := r.URL.Query()
	from, to, err := parseStatsWindow(q.Get("from"), q.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	topN := 10
	if v := q.Get("top_n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			topN = n
		}
	}

	perNode := []hourlyAggregateResp{}
	totals := clusterTotals{}
	domainTotals := map[string]int{}
	clientTotals := map[string]int{}

	err = c.Store().AggregatesIter(func(agg cluster.HourAggregate) error {
		hourStart := time.Unix(agg.HourStart, 0).UTC()
		if !from.IsZero() && hourStart.Before(from) {
			return nil
		}
		if !to.IsZero() && hourStart.After(to) {
			return nil
		}

		entry := hourlyAggregateResp{
			NodeID:     agg.NodeID,
			HourStart:  hourStart.Format(time.RFC3339),
			Total:      agg.Total,
			Blocked:    agg.Blocked,
			Forwarded:  agg.Forwarded,
			Cached:     agg.Cached,
			Local:      agg.Local,
			TopDomains: make([]domainCount, 0, len(agg.TopDomains)),
			TopClients: make([]clientCount, 0, len(agg.TopClients)),
		}
		for _, d := range agg.TopDomains {
			entry.TopDomains = append(entry.TopDomains, domainCount{Domain: d.Name, Count: d.Count})
			domainTotals[d.Name] += d.Count
		}
		for _, cl := range agg.TopClients {
			entry.TopClients = append(entry.TopClients, clientCount{Client: cl.Name, Count: cl.Count})
			clientTotals[cl.Name] += cl.Count
		}

		totals.Total += agg.Total
		totals.Blocked += agg.Blocked
		totals.Forwarded += agg.Forwarded
		totals.Cached += agg.Cached
		totals.Local += agg.Local

		perNode = append(perNode, entry)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Stable ordering: by node_id then hour_start so identical inputs always
	// produce identical responses (important for the acceptance test diffs).
	sort.Slice(perNode, func(i, j int) bool {
		if perNode[i].NodeID != perNode[j].NodeID {
			return perNode[i].NodeID < perNode[j].NodeID
		}
		return perNode[i].HourStart < perNode[j].HourStart
	})

	resp := clusterStatsResp{
		WindowFrom:    formatWindow(from),
		WindowTo:      formatWindow(to),
		PerNode:       perNode,
		ClusterTotals: totals,
		TopDomains:    topDomains(domainTotals, topN),
		TopClients:    topClients(clientTotals, topN),
	}

	writeJSON(w, http.StatusOK, resp)
}

func parseStatsWindow(fromStr, toStr string) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	if fromStr != "" {
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return from, to, fmt.Errorf("invalid from: %w", err)
		}
		from = from.UTC()
	}
	if toStr != "" {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			return from, to, fmt.Errorf("invalid to: %w", err)
		}
		to = to.UTC()
	}
	return from, to, nil
}

// formatWindow returns the empty string when t is zero so the JSON consumer
// can distinguish "unbounded window" from "1970-01-01".
func formatWindow(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func topDomains(counts map[string]int, n int) []domainCount {
	out := make([]domainCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, domainCount{Domain: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Domain < out[j].Domain
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func topClients(counts map[string]int, n int) []clientCount {
	out := make([]clientCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, clientCount{Client: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Client < out[j].Client
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ─── cluster query-log fan-out ───────────────────────────────────────────────

// ClusterQueryLog handles GET /api/v1/cluster/query-log. Fans out to every
// known member's /api/v1/query-log in parallel; merges entries sorted by
// timestamp descending and reports per-node status (ok / timeout / error).
//
// Unreachable nodes do NOT fail the call — they show up in per_node with
// status "timeout" or "error". This matches FS-QueryLogAggregatesFanOutPartialFailure.
func (h *Handler) ClusterQueryLog(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}

	q := r.URL.Query()
	client := q.Get("client")
	outcome := q.Get("outcome")
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	timeoutMs := 2000
	if v := q.Get("timeout_ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeoutMs = n
		}
	}

	members, err := c.Store().Members()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build the query string we will forward. We deliberately do not forward
	// the timeout_ms (it's a per-node deadline, not a downstream parameter).
	fwdQuery := buildFwdQuery(client, outcome, limit, offset)
	// Use the cluster secret for inter-node requests — session tokens are
	// node-local and would be rejected by the target node's auth middleware.
	clusterSecret := c.ClusterSecret()
	localID := c.Node().Node.ID

	type fanResult struct {
		entries []mergedQueryEntry
		status  perNodeFanOut
	}

	results := make(chan fanResult, len(members))
	var wg sync.WaitGroup
	httpClient := &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}

	for _, m := range members {
		wg.Add(1)
		go func(m cluster.Member) {
			defer wg.Done()

			// Local short-circuit — calling ourselves over HTTP would deadlock
			// during the inflight request and add network overhead for no
			// reason; we read the query log directly instead.
			if m.NodeID == localID {
				results <- localFanOut(h, m.NodeID, client, outcome, limit, offset)
				return
			}

			results <- remoteFanOut(httpClient, m, fwdQuery, clusterSecret)
		}(m)
	}

	wg.Wait()
	close(results)

	var merged []mergedQueryEntry
	perNode := []perNodeFanOut{}
	for res := range results {
		merged = append(merged, res.entries...)
		perNode = append(perNode, res.status)
	}

	// Stable ordering: timestamp desc, then node_id asc to break ties so the
	// response is deterministic for tests.
	sort.Slice(merged, func(i, j int) bool {
		if !merged[i].Timestamp.Equal(merged[j].Timestamp) {
			return merged[i].Timestamp.After(merged[j].Timestamp)
		}
		return merged[i].NodeID < merged[j].NodeID
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}

	sort.Slice(perNode, func(i, j int) bool { return perNode[i].NodeID < perNode[j].NodeID })

	writeJSON(w, http.StatusOK, clusterQueryLogResp{
		Entries: merged,
		Total:   len(merged),
		PerNode: perNode,
	})
}

func buildFwdQuery(client, outcome string, limit, offset int) string {
	parts := []string{}
	if client != "" {
		parts = append(parts, "client="+client)
	}
	if outcome != "" {
		parts = append(parts, "outcome="+outcome)
	}
	parts = append(parts, "limit="+strconv.Itoa(limit))
	parts = append(parts, "offset="+strconv.Itoa(offset))
	return strings.Join(parts, "&")
}

// localFanOut reads the local query log directly so we don't have to dial
// ourselves over HTTP (which would block on a single-threaded test server).
func localFanOut(h *Handler, nodeID, client, outcome string, limit, offset int) struct {
	entries []mergedQueryEntry
	status  perNodeFanOut
} {
	type wrapped struct {
		entries []mergedQueryEntry
		status  perNodeFanOut
	}
	ql := h.app.GetQueryLog()
	if ql == nil {
		return wrapped{
			status: perNodeFanOut{NodeID: nodeID, Status: "error", Error: "local query log unavailable"},
		}
	}
	entries, _ := ql.Query(client, outcome, limit, offset)
	out := make([]mergedQueryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, mergedQueryEntry{
			NodeID:      nodeID,
			Timestamp:   e.Timestamp,
			Client:      e.Client,
			Domain:      e.Domain,
			QueryType:   e.QueryType,
			Outcome:     string(e.Outcome),
			BlocklistID: e.BlocklistID,
		})
	}
	return wrapped{
		entries: out,
		status:  perNodeFanOut{NodeID: nodeID, Status: "ok", EntryCount: len(out)},
	}
}

// remoteFanOut issues the per-node HTTP request and classifies the result.
// clusterSecret is sent as X-Cluster-Secret — session tokens are node-local
// and would be rejected by the target node's auth middleware.
func remoteFanOut(client *http.Client, m cluster.Member, query, clusterSecret string) struct {
	entries []mergedQueryEntry
	status  perNodeFanOut
} {
	type wrapped struct {
		entries []mergedQueryEntry
		status  perNodeFanOut
	}
	raftHost, _, _ := net.SplitHostPort(m.RaftAddress)
	base := apiBase(m.APIAddress, raftHost)
	url := base + "/api/v1/query-log"
	if query != "" {
		url += "?" + query
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return wrapped{status: perNodeFanOut{NodeID: m.NodeID, Status: "error", Error: err.Error()}}
	}
	if clusterSecret != "" {
		req.Header.Set("X-Cluster-Secret", clusterSecret)
	}

	resp, err := client.Do(req)
	if err != nil {
		status := "error"
		if isTimeoutErr(err) {
			status = "timeout"
		}
		return wrapped{status: perNodeFanOut{NodeID: m.NodeID, Status: status, Error: err.Error()}}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return wrapped{status: perNodeFanOut{
			NodeID: m.NodeID,
			Status: "error",
			Error:  fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}}
	}

	// We use the in-process queryLogResponse shape declared in query_log.go.
	var body queryLogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return wrapped{status: perNodeFanOut{NodeID: m.NodeID, Status: "error", Error: err.Error()}}
	}

	out := make([]mergedQueryEntry, 0, len(body.Entries))
	for _, e := range body.Entries {
		out = append(out, mergedQueryEntry{
			NodeID:      m.NodeID,
			Timestamp:   e.Timestamp,
			Client:      e.Client,
			Domain:      e.Domain,
			QueryType:   e.QueryType,
			Outcome:     e.Outcome,
			BlocklistID: e.BlocklistID,
		})
	}
	return wrapped{
		entries: out,
		status:  perNodeFanOut{NodeID: m.NodeID, Status: "ok", EntryCount: len(out)},
	}
}

// apiBase rewrites a "host:port" or "0.0.0.0:port" listen address into a
// reachable HTTP base URL. When the listen address is bound to 0.0.0.0 or ::,
// the first non-empty fallbackHost is used instead of 127.0.0.1.
func apiBase(listenAddr string, fallbackHost ...string) string {
	if strings.HasPrefix(listenAddr, "http://") || strings.HasPrefix(listenAddr, "https://") {
		return strings.TrimRight(listenAddr, "/")
	}
	host, port := listenAddr, ""
	if idx := strings.LastIndex(listenAddr, ":"); idx >= 0 {
		host = listenAddr[:idx]
		port = listenAddr[idx+1:]
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
		for _, fb := range fallbackHost {
			if fb != "" {
				host = fb
				break
			}
		}
	}
	if port == "" {
		return "http://" + host
	}
	return "http://" + host + ":" + port
}
