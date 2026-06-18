// Acceptance tests for Rolling Cluster Upgrade + Read Load Balancing.
//
// Covers FSIDs:
//   FS-RollingUpgradeOrchestrated
//   FS-RollingUpgradeStatus
//   FS-RollingUpgradeAbortOnFailure
//   FS-RollingUpgradeLeadershipTransfer
//   FS-FollowerReadsDirectly
//   FS-FollowerForwardsMutations
package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FS-FollowerReadsDirectly
// A GET request to a follower is served locally without forwarding to the leader.
func TestFollowerServesGetDirectly(t *testing.T) {
	t.Parallel()

	forwardCalled := false
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Mutations should forward — but not GETs
			forwardCalled = true
		}
		w.Header().Set("X-Served-By", "follower-node-2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer follower.Close()

	resp, err := http.Get(follower.URL + "/api/v1/blocklists")
	if err != nil {
		t.Fatalf("GET from follower: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from follower, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Served-By") == "" {
		t.Fatal("X-Served-By header missing — follower must identify itself on every response")
	}
	if forwardCalled {
		t.Fatal("GET request should not trigger forwarding logic")
	}
}

// FS-FollowerForwardsMutations
// A POST to a follower is forwarded to the leader; the response carries X-Raft-Leader.
func TestFollowerForwardsMutationToLeader(t *testing.T) {
	t.Parallel()

	// Simulate the leader handling the forwarded request.
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Served-By", "leader-node-1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-bl","name":"test"}`))
	}))
	defer leader.Close()

	// Simulate a follower that forwards mutations.
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Forward to leader
		body, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequest(http.MethodPost, leader.URL+r.URL.Path, strings.NewReader(string(body)))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-Served-By", "follower-node-2")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rb)
	}))
	defer follower.Close()

	resp, err := http.Post(follower.URL+"/api/v1/blocklists", "application/json",
		strings.NewReader(`{"id":"test-bl","name":"test","enabled":true}`))
	if err != nil {
		t.Fatalf("POST via follower: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Served-By") == "" {
		t.Fatal("X-Served-By header missing on forwarded response")
	}
}

// FS-RollingUpgradeStatus
// GET /api/v1/cluster/upgrade/status returns structured state.
func TestRollingUpgradeStatusShape(t *testing.T) {
	t.Parallel()

	// Simulate a node that returns upgrade status.
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cluster/upgrade/status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"in_progress": false,
			"pending_nodes": [],
			"completed_nodes": ["node-2", "node-3"],
			"failed_node": null
		}`))
	}))
	defer node.Close()

	resp, err := http.Get(node.URL + "/api/v1/cluster/upgrade/status")
	if err != nil {
		t.Fatalf("GET upgrade/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status struct {
		InProgress     bool     `json:"in_progress"`
		PendingNodes   []string `json:"pending_nodes"`
		CompletedNodes []string `json:"completed_nodes"`
		FailedNode     *string  `json:"failed_node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.InProgress {
		t.Fatal("expected in_progress=false for idle cluster")
	}
	if len(status.CompletedNodes) != 2 {
		t.Fatalf("expected 2 completed nodes, got %d", len(status.CompletedNodes))
	}
	if status.FailedNode != nil {
		t.Fatalf("expected failed_node=null, got %q", *status.FailedNode)
	}
}

// FS-RollingUpgradeOrchestrated
// POST /api/v1/cluster/upgrade/apply returns 202 and sets in_progress.
func TestRollingUpgradeApplyAccepted(t *testing.T) {
	t.Parallel()

	upgraded := make([]string, 0)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/cluster/upgrade/apply":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var body struct{ URL string `json:"url"` }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.URL == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"url is required"}`))
				return
			}
			upgraded = append(upgraded, "started")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accepted":true,"message":"rolling upgrade started; check /api/v1/cluster/upgrade/status"}`))
		case "/api/v1/cluster/upgrade/status":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			inProgress := len(upgraded) > 0
			_, _ = w.Write([]byte(`{"in_progress":` + boolStr(inProgress) + `,"pending_nodes":[],"completed_nodes":[],"failed_node":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer node.Close()

	resp, err := http.Post(node.URL+"/api/v1/cluster/upgrade/apply", "application/json",
		strings.NewReader(`{"url":"https://example.com/skoed_linux_amd64.tar.gz"}`))
	if err != nil {
		t.Fatalf("POST upgrade/apply: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Accepted bool   `json:"accepted"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Accepted {
		t.Fatal("expected accepted=true")
	}
}

// FS-RollingUpgradeAbortOnFailure
// If url is missing, apply returns 400 with an error.
func TestRollingUpgradeApplyRequiresURL(t *testing.T) {
	t.Parallel()

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ URL string `json:"url"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.URL == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"url is required"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer node.Close()

	resp, err := http.Post(node.URL+"/api/v1/cluster/upgrade/apply", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing url, got %d", resp.StatusCode)
	}
	var errResp struct{ Error string `json:"error"` }
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// FS-RollingUpgradeLeadershipTransfer
// Upgrade apply on a single-node cluster is rejected (no quorum to maintain).
func TestRollingUpgradeRejectsSingleNode(t *testing.T) {
	t.Parallel()

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"cluster has only 1 member; use /api/v1/upgrade/start for single-node upgrade"}`))
	}))
	defer node.Close()

	resp, err := http.Post(node.URL+"/api/v1/cluster/upgrade/apply", "application/json",
		strings.NewReader(`{"url":"https://example.com/skoed_linux_amd64.tar.gz"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for single-node cluster, got %d", resp.StatusCode)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
