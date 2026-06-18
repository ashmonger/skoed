package acceptance

// Acceptance tests for M20: mTLS Certificate Rotation.
// FSIDs covered:
//   FS-CertStatusExposesCertExpiry
//   FS-CertRotateTriggeredByAdmin
//   FS-CertRotateRollingMaintainsQuorum
//   FS-CertRotateRequiresClusterAdminScope

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// certStatusResp mirrors the JSON returned by GET /api/v1/cluster/certs/status.
type certStatusResp struct {
	CAExpiresAt time.Time             `json:"ca_expires_at"`
	Nodes       []certNodeStatusEntry `json:"nodes"`
}

type certNodeStatusEntry struct {
	NodeID          string    `json:"node_id"`
	CertExpiresAt   time.Time `json:"cert_expires_at"`
	RotationPending bool      `json:"rotation_pending"`
}

// TestCertStatusReturnsExpiry verifies FS-CertStatusExposesCertExpiry:
// GET /api/v1/cluster/certs/status returns CA and per-node cert expiry.
func TestCertStatusReturnsExpiry(t *testing.T) {
	c := startClusterMTLS(t, 1)
	leader := c.Leader(t)

	resp := leader.apiDo(t, "GET", "/api/v1/cluster/certs/status", "")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)

	var status certStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode certs/status: %v", err)
	}

	now := time.Now()
	if status.CAExpiresAt.IsZero() || status.CAExpiresAt.Before(now) {
		t.Errorf("ca_expires_at should be a future time, got %v", status.CAExpiresAt)
	}
	if len(status.Nodes) == 0 {
		t.Fatal("expected at least one node in certs/status response")
	}
	for _, n := range status.Nodes {
		if n.NodeID == "" {
			t.Error("node entry missing node_id")
		}
		if n.CertExpiresAt.IsZero() || n.CertExpiresAt.Before(now) {
			t.Errorf("node %s: cert_expires_at should be future, got %v", n.NodeID, n.CertExpiresAt)
		}
	}
}

// TestCertRotateAccepted verifies FS-CertRotateTriggeredByAdmin:
// POST /api/v1/cluster/certs/rotate returns 202 and rotation completes.
func TestCertRotateAccepted(t *testing.T) {
	c := startClusterMTLS(t, 1)
	leader := c.Leader(t)

	resp := leader.apiDo(t, "POST", "/api/v1/cluster/certs/rotate", "")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusAccepted)

	// Poll until rotation_pending=false for all nodes (up to 10s).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r := leader.apiDo(t, "GET", "/api/v1/cluster/certs/status", "")
		if r.StatusCode == http.StatusOK {
			var status certStatusResp
			if err := json.NewDecoder(r.Body).Decode(&status); err == nil {
				allDone := true
				for _, n := range status.Nodes {
					if n.RotationPending {
						allDone = false
						break
					}
				}
				r.Body.Close()
				if allDone {
					return
				}
			} else {
				r.Body.Close()
			}
		} else {
			r.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Final check with assertion.
	r := leader.apiDo(t, "GET", "/api/v1/cluster/certs/status", "")
	defer r.Body.Close()
	assertStatus(t, r, http.StatusOK)
	var status certStatusResp
	if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
		t.Fatalf("decode final status: %v", err)
	}
	for _, n := range status.Nodes {
		if n.RotationPending {
			t.Errorf("node %s still has rotation_pending=true after 10s", n.NodeID)
		}
	}
}

// TestCertRotateMaintainsQuorum verifies FS-CertRotateRollingMaintainsQuorum:
// rotating certs on a 3-node cluster never drops below 2 reachable members.
func TestCertRotateMaintainsQuorum(t *testing.T) {
	c := startClusterMTLS(t, 3)
	leader := c.Leader(t)

	// Trigger rotation.
	rotResp := leader.apiDo(t, "POST", "/api/v1/cluster/certs/rotate", "")
	defer rotResp.Body.Close()
	assertStatus(t, rotResp, http.StatusAccepted)

	// Poll cluster health while rotation is in progress.
	type healthResp struct {
		Status           string `json:"status"`
		ReachableMembers int    `json:"reachable_members"`
		Members          int    `json:"members"`
	}
	minReachable := 3
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		hr := leader.apiDo(t, "GET", "/api/v1/cluster/health", "")
		if hr.StatusCode == http.StatusOK {
			var h healthResp
			if err := json.NewDecoder(hr.Body).Decode(&h); err == nil {
				if h.ReachableMembers < minReachable {
					minReachable = h.ReachableMembers
				}
				hr.Body.Close()
				if h.Status == "ok" {
					break
				}
			} else {
				hr.Body.Close()
			}
		} else {
			hr.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Quorum = majority. For 3 nodes, quorum = 2.
	if minReachable < 2 {
		t.Errorf("cluster dropped below quorum during rotation: min reachable = %d", minReachable)
	}

	// After rotation, cluster should be fully healthy.
	hr := leader.apiDo(t, "GET", "/api/v1/cluster/health", "")
	defer hr.Body.Close()
	assertStatus(t, hr, http.StatusOK)
	var h healthResp
	if err := json.NewDecoder(hr.Body).Decode(&h); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if h.Status != "ok" {
		t.Errorf("cluster health after rotation: want ok, got %s", h.Status)
	}
	if h.ReachableMembers != 3 {
		t.Errorf("expected 3 reachable members after rotation, got %d", h.ReachableMembers)
	}
}

// TestCertRotateRequiresAdmin verifies FS-CertRotateRequiresClusterAdminScope:
// a read-only bearer token gets 403 on POST /api/v1/cluster/certs/rotate.
func TestCertRotateRequiresAdmin(t *testing.T) {
	c := startClusterMTLS(t, 1)
	leader := c.Leader(t)

	// Mint a read-only bearer token via the admin session.
	mintBody := `{"label":"readonly","scopes":["read"]}`
	mintResp := leader.apiDo(t, "POST", "/api/v1/tokens", mintBody)
	defer mintResp.Body.Close()
	assertStatus(t, mintResp, http.StatusCreated)

	var minted struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(mintResp.Body).Decode(&minted); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	if minted.Token == "" {
		t.Fatal("minted token is empty")
	}

	// Use the read-only token to call POST /certs/rotate directly.
	req, err := http.NewRequest(http.MethodPost, leader.APIBase+"/api/v1/cluster/certs/rotate", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+minted.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusForbidden)
}
