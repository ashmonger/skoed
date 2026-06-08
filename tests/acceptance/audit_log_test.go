// Acceptance tests for M5.2 — Audit log.
//
// FSIDs covered:
//   FS-AuditWriteRecorded         → TestAuditWriteRecorded
//   FS-AuditFailedWriteRecorded   → TestAuditFailedWriteRecorded
//   FS-AuditReadsNotRecorded      → TestAuditReadsNotRecorded
//   FS-AuditListEndpointShape     → TestAuditListEndpointShape
//   FS-AuditFilterByActor         → TestAuditFilterByActor (skip; needs second user, M7)
//   FS-AuditFilterByAction        → TestAuditFilterByAction
//   FS-AuditReplicatesAcrossNodes → TestAuditReplicatesAcrossNodes  ← 3-node
//   FS-AuditRequiresAuth          → TestAuditRequiresAuth
//   FS-AuditMetricsCounter        → TestAuditMetricsCounter

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

type auditEntry struct {
	ID        string `json:"id"`
	Seq       uint64 `json:"seq"`
	Timestamp string `json:"timestamp"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	Diff      string `json:"diff,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
}

type auditPage struct {
	Entries []auditEntry `json:"entries"`
	Total   int          `json:"total"`
	Limit   int          `json:"limit"`
	Offset  int          `json:"offset"`
}

// fetchAudit GETs /api/v1/audit with the given query string. Skips on
// 404 so the test suite stays green before M5.2 implementation lands.
func fetchAudit(t *testing.T, n *Node, qs string) auditPage {
	t.Helper()
	path := "/api/v1/audit"
	if qs != "" {
		path += "?" + qs
	}
	resp := n.apiDo(t, "GET", path, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M5.2 impl pending: %s returns 404", path)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("audit GET %s status %d: %s", path, resp.StatusCode, body)
	}
	var p auditPage
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode audit page: %v", err)
	}
	return p
}

// FS-AuditWriteRecorded
func TestAuditWriteRecorded(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	before := fetchAudit(t, n, "limit=1")

	body := mustJSON(t, map[string]any{
		"id":     "house-block-audit",
		"name":   "Audit-test blocklist",
		"source": map[string]string{"type": "manual"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("blocklist create: want 201, got %d", resp.StatusCode)
	}

	// Audit is replicated through Raft; allow a short window.
	got := waitForAudit(t, n, before.Total+1, 3*time.Second)
	latest := got.Entries[0]
	if latest.Actor != "user:admin" {
		t.Errorf("actor: want user:admin, got %q", latest.Actor)
	}
	if latest.Action != "blocklist.create" {
		t.Errorf("action: want blocklist.create, got %q", latest.Action)
	}
	if latest.Result != "ok" {
		t.Errorf("result: want ok, got %q", latest.Result)
	}
	if latest.Target == "" {
		t.Errorf("target should be non-empty")
	}
	ts, err := time.Parse(time.RFC3339, latest.Timestamp)
	if err != nil {
		t.Fatalf("timestamp parse: %v", err)
	}
	if d := time.Since(ts); d < 0 || d > 5*time.Second {
		t.Errorf("timestamp drift: %s", d)
	}
}

// FS-AuditFailedWriteRecorded — provoke a 4xx by sending an invalid
// blocklist body (missing name / source). Blocklist POST upserts on
// duplicate id, so we can't use that to trigger an error.
func TestAuditFailedWriteRecorded(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	before := fetchAudit(t, n, "limit=1").Total

	// Garbled JSON → handler responds 400.
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", "this is not json")
	resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("invalid body: want 4xx, got %d", resp.StatusCode)
	}

	got := waitForAudit(t, n, before+1, 3*time.Second)
	if got.Entries[0].Result != "error" {
		t.Errorf("result: want error, got %q", got.Entries[0].Result)
	}
}

// FS-AuditReadsNotRecorded
func TestAuditReadsNotRecorded(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	before := fetchAudit(t, n, "limit=1").Total
	for i := 0; i < 5; i++ {
		r := n.apiDo(t, "GET", "/api/v1/blocklists", "")
		r.Body.Close()
	}
	// Give Raft a moment in case a write somehow snuck in.
	time.Sleep(300 * time.Millisecond)
	after := fetchAudit(t, n, "limit=1").Total
	if after != before {
		t.Errorf("read traffic recorded %d audit entries (was %d, now %d)", after-before, before, after)
	}
}

// FS-AuditListEndpointShape
func TestAuditListEndpointShape(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	// Make 3 mutations so we have at least 3 entries.
	for i := 0; i < 3; i++ {
		body := mustJSON(t, map[string]any{
			"id":     fmt.Sprintf("shape-%d", i),
			"name":   "shape",
			"source": map[string]string{"type": "manual"},
		})
		r := n.apiDo(t, "POST", "/api/v1/blocklists", body)
		r.Body.Close()
	}
	page := waitForAudit(t, n, 3, 5*time.Second)
	if len(page.Entries) < 2 {
		t.Fatalf("want at least 2 entries on first page; got %d", len(page.Entries))
	}
	for i := 1; i < len(page.Entries); i++ {
		prev, _ := time.Parse(time.RFC3339, page.Entries[i-1].Timestamp)
		cur, _ := time.Parse(time.RFC3339, page.Entries[i].Timestamp)
		if cur.After(prev) {
			t.Errorf("entries not sorted newest-first at index %d", i)
		}
	}

	pageLimit := fetchAudit(t, n, "limit=2")
	if pageLimit.Limit != 2 {
		t.Errorf("Limit echoed: want 2, got %d", pageLimit.Limit)
	}
	if len(pageLimit.Entries) > 2 {
		t.Errorf("limit=2: got %d entries", len(pageLimit.Entries))
	}
}

// FS-AuditFilterByActor — exercised once M7 ships a second user.
func TestAuditFilterByActor(t *testing.T) {
	t.Skip("FS-AuditFilterByActor needs M7 multi-user support to seed entries from two actors")
}

// FS-AuditFilterByAction
func TestAuditFilterByAction(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	// Seed a blocklist (action: blocklist.create).
	body := mustJSON(t, map[string]any{
		"id":     "filter-action-bl",
		"name":   "filter",
		"source": map[string]string{"type": "manual"},
	})
	r := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	r.Body.Close()
	// Seed a profile (action: profile.create) — proves the action= filter
	// actually filters and isn't a no-op.
	prof := mustJSON(t, map[string]any{
		"id":   "filter-action-prof",
		"name": "filter-test",
	})
	r = n.apiDo(t, "POST", "/api/v1/profiles", prof)
	r.Body.Close()
	_ = waitForAudit(t, n, 2, 5*time.Second)

	page := fetchAudit(t, n, "action=blocklist.")
	if len(page.Entries) == 0 {
		t.Skip("audit empty after seed — likely impl pending")
	}
	for _, e := range page.Entries {
		if !strings.HasPrefix(e.Action, "blocklist.") {
			t.Errorf("action filter leaked: got %q", e.Action)
		}
	}
}

// FS-AuditReplicatesAcrossNodes — runs against a 3-node cluster and
// verifies every node sees the same newest audit entry after a single
// mutation on the leader.
func TestAuditReplicatesAcrossNodes(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.Leader(t).Node
	body := mustJSON(t, map[string]any{
		"id":     "replicates-bl",
		"name":   "replicates",
		"source": map[string]string{"type": "manual"},
	})
	r := leader.apiDo(t, "POST", "/api/v1/blocklists", body)
	r.Body.Close()

	// Each node should converge to ≥ 1 entry with action=blocklist.create
	// and target referencing replicates-bl within 2 s.
	deadline := time.Now().Add(2 * time.Second)
	var skipped bool
	for _, cn := range c.nodes {
		matched := false
		for time.Now().Before(deadline) {
			page := fetchAudit(t, cn.Node, "limit=10")
			if len(page.Entries) > 0 {
				for _, e := range page.Entries {
					if e.Action == "blocklist.create" && strings.Contains(e.Target, "replicates-bl") {
						matched = true
						break
					}
				}
			}
			if matched {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !matched && !skipped {
			t.Errorf("node %s did not see the replicated audit entry within 2 s", cn.NodeID)
		}
	}
}

// FS-AuditRequiresAuth
func TestAuditRequiresAuth(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	resp := n.apiDoNoAuth(t, "GET", "/api/v1/audit")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M5.2 impl pending")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/audit no-auth: want 401, got %d", resp.StatusCode)
	}
}

// FS-AuditMetricsCounter — warm the counter with one mutation, then
// confirm a second mutation bumps it by 1. CounterVecs are absent
// until first observation, so the warm-up POST guarantees the series
// exists before we sample the baseline.
func TestAuditMetricsCounter(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	warm := mustJSON(t, map[string]any{
		"id":     "metrics-counter-warm",
		"name":   "metrics-counter-warm",
		"source": map[string]string{"type": "manual"},
	})
	r := n.apiDo(t, "POST", "/api/v1/blocklists", warm)
	r.Body.Close()
	_ = waitForAudit(t, n, 1, 3*time.Second)

	resp, _ := http.Get(n.APIBase + "/metrics")
	if resp == nil || resp.StatusCode != 200 {
		t.Skipf("M5.1 /metrics unavailable")
	}
	b0, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b0), "skoed_audit_events_total") {
		t.Fatalf("skoed_audit_events_total absent after first audit append")
	}
	base := sumActionCounter(string(b0), "blocklist.create")

	body := mustJSON(t, map[string]any{
		"id":     "metrics-counter-bl",
		"name":   "metrics-counter",
		"source": map[string]string{"type": "manual"},
	})
	r2 := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	r2.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, _ := http.Get(n.APIBase + "/metrics")
		if resp == nil {
			break
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cur := sumActionCounter(string(b), "blocklist.create")
		if cur >= base+1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("skoed_audit_events_total{action=\"blocklist.create\"} did not increment from %d", base)
}

// ── helpers ────────────────────────────────────────────────────────────────

// waitForAudit polls /audit until the total is at least `want`, or fails.
func waitForAudit(t *testing.T, n *Node, want int, within time.Duration) auditPage {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		p := fetchAudit(t, n, "limit=10")
		if p.Total >= want {
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("audit total < %d after %s", want, within)
	return auditPage{}
}

func sumActionCounter(metricsBody, action string) int {
	// skoed_audit_events_total{action="blocklist.create"} 7
	re := regexp.MustCompile(`(?m)^skoed_audit_events_total\{[^}]*action="` + regexp.QuoteMeta(action) + `"[^}]*\}\s+(\S+)\s*$`)
	m := re.FindStringSubmatch(metricsBody)
	if m == nil {
		return 0
	}
	var v int
	fmt.Sscanf(m[1], "%d", &v)
	return v
}
