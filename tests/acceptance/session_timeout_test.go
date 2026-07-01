// Acceptance tests for configurable session timeout (M34.5).
//
// Covers FSIDs:
//
//	FS-SessionTimeoutViewCurrentSetting
//	FS-SessionTimeoutSetCustomDuration
//	FS-SessionTimeoutDefaultApplied
//	FS-SessionTimeoutExpiredSessionRedirectedToLogin
//	FS-SessionTimeoutExistingSessionsUnaffected
//	FS-SessionTimeoutPersistsAcrossRestart
package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// getSessionTimeout reads auth.session_timeout_seconds from GET /api/v1/settings.
func getSessionTimeout(t *testing.T, n *ClusterNode) int {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/settings", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/settings: %d", resp.StatusCode)
	}
	var body struct {
		Auth struct {
			SessionTimeoutSeconds int `json:"session_timeout_seconds"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return body.Auth.SessionTimeoutSeconds
}

// setSessionTimeout PATCHes auth.session_timeout_seconds. Returns the response
// (caller must close the body).
func setSessionTimeout(t *testing.T, n *ClusterNode, seconds int) *http.Response {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"auth": map[string]any{
			"session_timeout_seconds": seconds,
		},
	})
	return n.apiDo(t, "PATCH", "/api/v1/settings", body)
}

// bearerStatus sends GET /api/v1/settings with the given token and returns the
// HTTP status code. /api/v1/settings requires authentication, so an expired
// token returns 401.
func bearerStatus(t *testing.T, n *ClusterNode, token string) int {
	t.Helper()
	resp := n.apiDoBearer(t, "GET", "/api/v1/settings", "", token)
	defer resp.Body.Close()
	return resp.StatusCode
}

// FS-SessionTimeoutDefaultApplied
// FS-SessionTimeoutViewCurrentSetting
func TestSessionTimeoutDefaultApplied(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	got := getSessionTimeout(t, leader)
	const defaultTTL = 28800 // 8 hours
	if got != defaultTTL {
		t.Errorf("default session_timeout_seconds = %d, want %d", got, defaultTTL)
	}
}

// FS-SessionTimeoutSetCustomDuration
func TestSessionTimeoutSetCustomDuration(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	for _, seconds := range []int{1800, 3600, 14400, 28800, 86400, 604800} {
		resp := setSessionTimeout(t, leader, seconds)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("PATCH session_timeout=%d: got %d, want 200", seconds, resp.StatusCode)
			continue
		}
		got := getSessionTimeout(t, leader)
		if got != seconds {
			t.Errorf("after PATCH=%d: GET returned %d", seconds, got)
		}
	}
}

// FS-SessionTimeoutSetCustomDuration — invalid values rejected
func TestSessionTimeoutInvalidValueRejected(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	for _, bad := range []int{0, -1, 604801} {
		resp := setSessionTimeout(t, leader, bad)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PATCH session_timeout=%d: got %d, want 400", bad, resp.StatusCode)
		}
	}
}

// FS-SessionTimeoutExpiredSessionRedirectedToLogin
//
// Uses a 1-second TTL so the test completes quickly. The backend accepts any
// value in [1, 604800]; the UI restricts users to the six approved durations.
func TestSessionTimeoutExpiredSessionReturns401(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	// Set a 1-second TTL.
	resp := setSessionTimeout(t, leader, 1)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=1: %d", resp.StatusCode)
	}

	// Login AFTER the change → new token has a 1-second TTL.
	shortToken := loginSession(t, leader.Node, defaultUsername, defaultPassword)

	// Token is valid immediately after login.
	if status := bearerStatus(t, leader, shortToken); status != http.StatusOK {
		t.Fatalf("token should be valid immediately after login, got %d", status)
	}

	// Wait for expiry.
	time.Sleep(2 * time.Second)

	// Now the token must be rejected.
	if status := bearerStatus(t, leader, shortToken); status != http.StatusUnauthorized {
		t.Errorf("expired token: got %d, want 401", status)
	}
}

// FS-SessionTimeoutExistingSessionsUnaffected
//
// Sessions created BEFORE the timeout change keep their original expiry. We
// verify by creating a session under the default 8-hour TTL, then reducing
// the TTL to 1 second; the original token must still be valid after 2 seconds
// while a token issued under the new 1-second TTL is expired.
func TestSessionTimeoutExistingSessionsUnaffected(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	// Login BEFORE the timeout change (default 8-hour TTL).
	oldToken := loginSession(t, leader.Node, defaultUsername, defaultPassword)

	// Reduce TTL to 1 second.
	resp := setSessionTimeout(t, leader, 1)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=1: %d", resp.StatusCode)
	}

	// Login AFTER the change → short-lived token.
	shortToken := loginSession(t, leader.Node, defaultUsername, defaultPassword)

	// Wait for the short token to expire.
	time.Sleep(2 * time.Second)

	// The old token (issued before the change) must still be valid.
	if status := bearerStatus(t, leader, oldToken); status != http.StatusOK {
		t.Errorf("old token after TTL change: got %d, want 200", status)
	}

	// The new short-lived token must be expired.
	if status := bearerStatus(t, leader, shortToken); status != http.StatusUnauthorized {
		t.Errorf("new short token: got %d, want 401", status)
	}
}

// FS-SessionTimeoutPersistsAcrossRestart
func TestSessionTimeoutPersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)
	leaderIdx := indexOf(c, leader)

	const customTTL = 3600
	resp := setSessionTimeout(t, leader, customTTL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=%d: %d", customTTL, resp.StatusCode)
	}

	// Restart the node (in-memory session store is cleared; config reloads from bbolt).
	c.KillNode(t, leaderIdx)
	c.RestartNode(t, leaderIdx)

	// The restarted node is the only node in the cluster, so it becomes leader again.
	restarted := c.Node(leaderIdx)
	got := getSessionTimeout(t, restarted)
	if got != customTTL {
		t.Errorf("after restart: session_timeout=%d, want %d", got, customTTL)
	}
}

// FS-SessionTimeoutSetCustomDuration + cluster replication
func TestSessionTimeoutClusterReplicated(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	const customTTL = 14400
	resp := setSessionTimeout(t, leader, customTTL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=%d: %d", customTTL, resp.StatusCode)
	}

	c.WaitConverged(t)

	for _, cn := range c.Followers(t) {
		got := getSessionTimeout(t, cn)
		if got != customTTL {
			t.Errorf("follower %s: session_timeout=%d, want %d", cn.NodeID, got, customTTL)
		}
	}
}

// FS-SessionTimeoutExpiredSessionRedirectedToLogin — new session after change
// uses new TTL, not old.
func TestSessionTimeoutNewSessionUsesUpdatedTTL(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	// Set to 1 second.
	resp := setSessionTimeout(t, leader, 1)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=1: %d", resp.StatusCode)
	}

	// New session should use the 1-second TTL.
	token := loginSession(t, leader.Node, defaultUsername, defaultPassword)
	time.Sleep(2 * time.Second)
	if status := bearerStatus(t, leader, token); status != http.StatusUnauthorized {
		t.Errorf("new session after TTL=1s: got %d after 2s wait, want 401", status)
	}

	// Restore to 1 hour; new sessions must last longer.
	resp2 := setSessionTimeout(t, leader, 3600)
	resp2.Body.Close()
	// Re-authenticate (our admin session may have expired under the 1s TTL too).
	longToken := loginSession(t, leader.Node, defaultUsername, defaultPassword)
	time.Sleep(2 * time.Second)
	if status := bearerStatus(t, leader, longToken); status != http.StatusOK {
		t.Errorf("long-lived session after TTL=3600s: got %d after 2s, want 200", status)
	}
}

// FS-SessionTimeoutViewCurrentSetting — settings response includes auth section.
func TestSessionTimeoutInSettingsResponse(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	resp := leader.apiDo(t, "GET", "/api/v1/settings", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/settings: %d", resp.StatusCode)
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if _, ok := raw["auth"]; !ok {
		t.Error("settings response missing 'auth' key")
	}
	got := getSessionTimeout(t, leader)
	if got <= 0 {
		t.Errorf("session_timeout_seconds = %d, want > 0", got)
	}
}

// Ensure the session_timeout_seconds field is persisted in shadow YAML.
func TestSessionTimeoutInShadowYAML(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	const customTTL = 7200
	resp := setSessionTimeout(t, leader, customTTL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH session_timeout=%d: %d", customTTL, resp.StatusCode)
	}

	time.Sleep(2 * time.Second)

	data := readShadowYAML(t, leader)
	expected := fmt.Sprintf("session_timeout_seconds: %d", customTTL)
	if !strings.Contains(string(data), expected) {
		t.Errorf("shadow YAML missing %q:\n%s", expected, string(data))
	}
}
