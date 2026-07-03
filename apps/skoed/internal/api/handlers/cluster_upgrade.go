package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// upgradeState tracks a rolling upgrade in progress. Process-local; resets on restart.
var (
	upgradeMu    sync.Mutex
	upgradeState rollingUpgradeState
)

type rollingUpgradeState struct {
	InProgress     bool     `json:"in_progress"`
	PendingNodes   []string `json:"pending_nodes"`
	CompletedNodes []string `json:"completed_nodes"`
	FailedNode     *string  `json:"failed_node"`
	Log            []string `json:"log"`
}

// addUpgradeLogLine appends a timestamped log entry to the in-progress upgrade
// state and also writes to the process logger. Safe to call concurrently.
func addUpgradeLogLine(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[rolling-upgrade] %s", msg)
	ts := time.Now().Format("15:04:05")
	upgradeMu.Lock()
	upgradeState.Log = append(upgradeState.Log, ts+" "+msg)
	upgradeMu.Unlock()
}

// ClusterUpgradeStatus handles GET /api/v1/cluster/upgrade/status.
func (h *Handler) ClusterUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	upgradeMu.Lock()
	snap := upgradeState
	// Ensure non-nil slices for clean JSON output.
	if snap.PendingNodes == nil {
		snap.PendingNodes = []string{}
	}
	if snap.CompletedNodes == nil {
		snap.CompletedNodes = []string{}
	}
	if snap.Log == nil {
		snap.Log = []string{}
	}
	upgradeMu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

// ClusterUpgradeLogStream handles GET /api/v1/cluster/upgrade/log.
// Streams upgrade log lines as Server-Sent Events (text/event-stream).
// Replays all buffered lines from the current (or most recent) upgrade run,
// then sends new lines as they are appended. Sends "event: done" when the
// upgrade completes so the client can stop listening.
func (h *Handler) ClusterUpgradeLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sent := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			upgradeMu.Lock()
			newLines := make([]string, len(upgradeState.Log[sent:]))
			copy(newLines, upgradeState.Log[sent:])
			inProgress := upgradeState.InProgress
			total := len(upgradeState.Log)
			upgradeMu.Unlock()

			for _, line := range newLines {
				fmt.Fprintf(w, "data: %s\n\n", line) //nolint:errcheck
				sent++
			}
			if len(newLines) > 0 {
				flusher.Flush()
			}
			// Signal completion once all lines have been sent and upgrade is done.
			if !inProgress && sent >= total && total > 0 {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n") //nolint:errcheck
				flusher.Flush()
				return
			}
		}
	}
}

// clusterUpgradeApplyRequest is the body for POST /api/v1/cluster/upgrade/apply.
type clusterUpgradeApplyRequest struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// ClusterUpgradeApply handles POST /api/v1/cluster/upgrade/apply.
// Must be called on the leader (forwarded by WriteForwardMiddleware).
func (h *Handler) ClusterUpgradeApply(w http.ResponseWriter, r *http.Request) {
	var req clusterUpgradeApplyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.SHA256 == "" {
		writeError(w, http.StatusBadRequest, "sha256 is required; a rolling upgrade must supply the artifact checksum")
		return
	}

	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	members, err := cl.Store().Members()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get members: "+err.Error())
		return
	}
	if len(members) < 2 {
		writeError(w, http.StatusUnprocessableEntity,
			"cluster has only 1 member; use /api/v1/upgrade/start for single-node upgrade")
		return
	}

	upgradeMu.Lock()
	if upgradeState.InProgress {
		upgradeMu.Unlock()
		writeError(w, http.StatusConflict, "a rolling upgrade is already in progress")
		return
	}
	selfID := cl.NodeID()
	// Build peer list: all members except self, sorted for determinism.
	peers := make([]struct{ id, apiAddr string }, 0, len(members)-1)
	for _, m := range members {
		if m.NodeID == selfID {
			continue
		}
		// Resolve wildcard bind address (0.0.0.0) to the peer's actual IP
		// using its Raft address as fallback. Without this, connecting to
		// 0.0.0.0:port on Linux routes to loopback and the leader upgrades
		// itself instead of the peer.
		peers = append(peers, struct{ id, apiAddr string }{m.NodeID, resolvePeerAddr(m.APIAddress, m.RaftAddress)})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].id < peers[j].id })

	pending := make([]string, len(peers))
	for i, p := range peers {
		pending[i] = p.id
	}
	upgradeState = rollingUpgradeState{
		InProgress:     true,
		PendingNodes:   pending,
		CompletedNodes: []string{},
		Log:            []string{},
	}
	upgradeMu.Unlock()

	clusterSecret := cl.ClusterSecret()
	selfAPIAddr := cl.Node().Node.APIAddress

	go func() {
		addUpgradeLogLine("rolling upgrade started — %d peer(s) to upgrade before self", len(peers))
		completedFirst := ""
		for _, peer := range peers {
			addUpgradeLogLine("upgrading %s (%s)…", peer.id, peer.apiAddr)
			if err := upgradeNode(peer.apiAddr, req.URL, req.SHA256, clusterSecret); err != nil {
				addUpgradeLogLine("FAILED %s: %v", peer.id, err)
				node := peer.id
				upgradeMu.Lock()
				upgradeState.InProgress = false
				upgradeState.FailedNode = &node
				upgradeState.PendingNodes = removePeer(upgradeState.PendingNodes, node)
				upgradeMu.Unlock()
				return
			}
			addUpgradeLogLine("OK %s — node healthy", peer.id)
			if completedFirst == "" {
				completedFirst = peer.id
			}
			upgradeMu.Lock()
			upgradeState.PendingNodes = removePeer(upgradeState.PendingNodes, peer.id)
			upgradeState.CompletedNodes = append(upgradeState.CompletedNodes, peer.id)
			upgradeMu.Unlock()
		}

		// Transfer leadership so this node (about to upgrade itself) steps down gracefully.
		if completedFirst != "" {
			addUpgradeLogLine("transferring leadership to %s before self-upgrade…", completedFirst)
			_ = cl.TransferLeadership(completedFirst)
			// Wait briefly for election to stabilise before self-upgrading.
			time.Sleep(2 * time.Second)
		}

		// Upgrade self last: post to own API endpoint. In test mode UpgradeStart
		// suppresses os.Exit so the goroutine completes normally.
		addUpgradeLogLine("upgrading self (%s)…", selfID)
		_ = upgradeNode(selfAPIAddr, req.URL, req.SHA256, clusterSecret)
		addUpgradeLogLine("OK %s — upgrade complete", selfID)

		upgradeMu.Lock()
		upgradeState.InProgress = false
		upgradeState.CompletedNodes = append(upgradeState.CompletedNodes, selfID)
		upgradeMu.Unlock()
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"message":  "rolling upgrade started; check /api/v1/cluster/upgrade/status",
	})
}

// upgradeNode posts to /api/v1/upgrade/start on the target node, waits for it
// to return 200 and become healthy again (up to upgradeNodeTimeout).
func upgradeNode(apiAddr, tarURL, sha256, clusterSecret string) error {
	const upgradeNodeTimeout = 120 * time.Second
	const pollInterval = 3 * time.Second

	baseURL := "http://" + apiAddr
	body, _ := json.Marshal(map[string]string{"url": tarURL, "sha256": sha256})

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, baseURL+"/api/v1/upgrade/node-start", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Secret", clusterSecret)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call upgrade/node-start on %s: %w", apiAddr, err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upgrade/node-start on %s returned %d: %s", apiAddr, resp.StatusCode, string(bodyBytes))
	}

	// Wait for node to come back healthy.
	deadline := time.Now().Add(upgradeNodeTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		hreq, _ := http.NewRequestWithContext(context.Background(),
			http.MethodGet, baseURL+"/api/v1/health", nil)
		hresp, herr := client.Do(hreq)
		if herr == nil && hresp.StatusCode == http.StatusOK {
			io.Copy(io.Discard, hresp.Body) //nolint:errcheck
			hresp.Body.Close()
			return nil
		}
		if hresp != nil {
			io.Copy(io.Discard, hresp.Body) //nolint:errcheck
			hresp.Body.Close()
		}
	}
	return fmt.Errorf("node %s did not become healthy within %s", apiAddr, upgradeNodeTimeout)
}

func removePeer(list []string, id string) []string {
	out := list[:0]
	for _, v := range list {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// resolvePeerAddr returns a connectable host:port for a cluster peer.
// When the peer's API address is bound to 0.0.0.0 or ::, the host is
// replaced with the IP extracted from the peer's Raft address.
func resolvePeerAddr(apiAddr, raftAddr string) string {
	host, port, err := net.SplitHostPort(apiAddr)
	if err != nil {
		return apiAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if raftHost, _, rerr := net.SplitHostPort(raftAddr); rerr == nil && raftHost != "" {
			return net.JoinHostPort(raftHost, port)
		}
	}
	return apiAddr
}
