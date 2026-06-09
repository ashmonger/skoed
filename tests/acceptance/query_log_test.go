// Acceptance tests for the query log feature.
//
// FSIDs covered:
//   FS-QueryLogRecordsEntry, FS-QueryLogOutcomeBlocked, FS-QueryLogOutcomeLocal,
//   FS-QueryLogBrowseAll, FS-QueryLogFilterByClient, FS-QueryLogFilterByOutcome,
//   FS-QueryLogRetentionBound, FS-QueryLogRetentionConfigurable

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// getLog calls GET /api/v1/query-log with an optional query string (e.g. "?client=127.0.0.1")
// and returns the entries slice from the response body.
func getLog(t *testing.T, n *Node, query string) []map[string]any {
	t.Helper()
	path := "/api/v1/query-log"
	if query != "" {
		path += query
	}
	resp := n.apiDo(t, "GET", path, "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var result struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode query-log response: %v", err)
	}
	return result.Entries
}

// findLogEntry returns the first entry in entries where domain and outcome both match,
// or nil if no such entry exists.
func findLogEntry(entries []map[string]any, domain, outcome string) map[string]any {
	for _, e := range entries {
		d, _ := e["domain"].(string)
		o, _ := e["outcome"].(string)
		if d == domain && o == outcome {
			return e
		}
	}
	return nil
}

// waitForLog polls GET /api/v1/query-log until an entry matching domain and outcome
// appears, or until maxWait elapses. It returns the matching entry or fails the test.
func waitForLog(t *testing.T, n *Node, domain, outcome string, maxWait time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		entries := getLog(t, n, "")
		if entry := findLogEntry(entries, domain, outcome); entry != nil {
			return entry
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("query log entry for domain=%q outcome=%q not found within %s", domain, outcome, maxWait)
	return nil
}

// FS-QueryLogRecordsEntry
// After a DNS query, GET /api/v1/query-log contains an entry with the correct
// client, domain, query type, and outcome.
func TestQueryLogRecordsEntry(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	entry := waitForLog(t, n, "example.com", "forwarded", 3*time.Second)

	if entry["client"] == nil || entry["client"] == "" {
		t.Fatalf("log entry missing client field: %v", entry)
	}
	if qt, _ := entry["query_type"].(string); qt != "A" {
		t.Fatalf("expected query_type=A, got %q", qt)
	}
	if entry["timestamp"] == nil || entry["timestamp"] == "" {
		t.Fatalf("log entry missing timestamp field: %v", entry)
	}
}

// FS-QueryLogOutcomeBlocked
// A blocked query appears in the log with outcome="blocked" and the blocklist ID.
func TestQueryLogOutcomeBlocked(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)

	entry := waitForLog(t, n, "ads.example.com", "blocked", 3*time.Second)

	blocklistID, _ := entry["blocklist_id"].(string)
	if blocklistID == "" {
		t.Fatalf("expected blocklist_id to be set on blocked entry, got: %v", entry)
	}
	if blocklistID != "ads" {
		t.Fatalf("expected blocklist_id=ads, got %q", blocklistID)
	}
}

// FS-QueryLogOutcomeLocal
// A query resolved from a local DNS entry appears with outcome="local".
func TestQueryLogOutcomeLocal(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Add a local A record via the API.
	resp := n.apiDo(t, "POST", "/api/v1/local-dns", mustJSON(t, map[string]any{
		"hostname": "nas.home",
		"type":     "A",
		"value":    "192.168.1.50",
		"ttl":      300,
	}))
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)

	waitForLog(t, n, "nas.home", "local", 3*time.Second)
}

// FS-QueryLogBrowseAll
// The log is returned in reverse chronological order and each entry includes
// the required fields: timestamp, client, domain, query_type, outcome.
func TestQueryLogBrowseAll(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	domains := []string{
		"first.example.com",
		"second.example.com",
		"third.example.com",
	}
	for _, d := range domains {
		dnsQuery(t, n.DNSAddr, d, dns.TypeA)
	}

	// Wait for the last domain to appear.
	waitForLog(t, n, "third.example.com", "forwarded", 3*time.Second)

	entries := getLog(t, n, "")
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 log entries, got %d", len(entries))
	}

	// Verify required fields are present on every entry.
	for i, e := range entries {
		for _, field := range []string{"timestamp", "client", "domain", "query_type", "outcome"} {
			v, ok := e[field]
			if !ok || v == nil || v == "" {
				t.Fatalf("entry[%d] missing required field %q: %v", i, field, e)
			}
		}
	}

	// Verify reverse chronological order: entries are most-recent first.
	// Find the positions of the three domains we queried.
	posFirst, posSecond, posThird := -1, -1, -1
	for i, e := range entries {
		d, _ := e["domain"].(string)
		switch d {
		case "first.example.com":
			if posFirst == -1 {
				posFirst = i
			}
		case "second.example.com":
			if posSecond == -1 {
				posSecond = i
			}
		case "third.example.com":
			if posThird == -1 {
				posThird = i
			}
		}
	}
	if posFirst == -1 || posSecond == -1 || posThird == -1 {
		t.Fatalf("could not find all three test domains in log (first=%d second=%d third=%d)",
			posFirst, posSecond, posThird)
	}
	// Reverse chronological means third < second < first in index (lower index = newer).
	if !(posThird < posSecond && posSecond < posFirst) {
		t.Fatalf("log not in reverse chronological order: posThird=%d posSecond=%d posFirst=%d",
			posThird, posSecond, posFirst)
	}
}

// FS-QueryLogFilterByClient
// GET /api/v1/query-log?client=127.0.0.1 returns only entries for that client.
func TestQueryLogFilterByClient(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	dnsQuery(t, n.DNSAddr, "filter-client.example.com", dns.TypeA)
	waitForLog(t, n, "filter-client.example.com", "forwarded", 3*time.Second)

	// The harness binds the DNS listener to 127.0.0.1; queries originate from 127.0.0.1.
	entries := getLog(t, n, "?client=127.0.0.1")
	if len(entries) == 0 {
		t.Fatal("expected at least one entry for client=127.0.0.1, got none")
	}
	for i, e := range entries {
		client, _ := e["client"].(string)
		if client != "127.0.0.1" {
			t.Fatalf("entry[%d] has client=%q, expected 127.0.0.1", i, client)
		}
	}
}

// FS-QueryLogFilterByOutcome
// GET /api/v1/query-log?outcome=blocked returns only blocked entries.
func TestQueryLogFilterByOutcome(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"blocked-outcome.example.com"}, "")

	// Issue both a blocked and a forwarded query.
	dnsQuery(t, n.DNSAddr, "blocked-outcome.example.com", dns.TypeA)
	dnsQuery(t, n.DNSAddr, "safe-outcome.example.com", dns.TypeA)

	waitForLog(t, n, "blocked-outcome.example.com", "blocked", 3*time.Second)

	entries := getLog(t, n, "?outcome=blocked")
	if len(entries) == 0 {
		t.Fatal("expected at least one blocked entry, got none")
	}
	for i, e := range entries {
		outcome, _ := e["outcome"].(string)
		if outcome != "blocked" {
			t.Fatalf("entry[%d] has outcome=%q, expected blocked", i, outcome)
		}
	}
}

// FS-QueryLogRetentionBound
// When max_entries is set to N and more than N queries are made, the log
// contains at most N entries.
func TestQueryLogRetentionBound(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	const maxEntries = 5

	// Set max_entries=5 before making queries.
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"query_log": map[string]any{
			"max_entries": maxEntries,
		},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Make 10 queries — more than the configured limit.
	for i := 0; i < 10; i++ {
		dnsQuery(t, n.DNSAddr, fmt.Sprintf("retention%d.example.com", i), dns.TypeA)
	}

	// Wait until the last query appears or the log is at capacity.
	deadline := time.Now().Add(5 * time.Second)
	var total int
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/query-log", "")
		assertStatus(t, resp, http.StatusOK)
		var result struct {
			Entries []map[string]any `json:"entries"`
			Total   int              `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			t.Fatalf("decode query-log response: %v", err)
		}
		resp.Body.Close()
		total = result.Total
		if total > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if total > maxEntries {
		t.Fatalf("expected log total <= %d after retention bound, got %d", maxEntries, total)
	}
}

// FS-QueryLogRetentionConfigurable
// PATCH /api/v1/settings with query_log.max_entries takes effect.
func TestQueryLogRetentionConfigurable(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	const newMax = 5000

	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"query_log": map[string]any{
			"max_entries": newMax,
		},
	}))
	assertStatus(t, resp, http.StatusOK)

	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		resp.Body.Close()
		t.Fatalf("decode settings response: %v", err)
	}
	resp.Body.Close()

	// Verify the returned settings reflect the new value.
	ql, ok := settings["query_log"].(map[string]any)
	if !ok {
		t.Fatalf("settings response missing query_log object: %v", settings)
	}
	got, ok := ql["max_entries"].(float64)
	if !ok {
		t.Fatalf("query_log.max_entries not numeric: %v", ql["max_entries"])
	}
	if int(got) != newMax {
		t.Fatalf("expected query_log.max_entries=%d, got %d", newMax, int(got))
	}

	// Confirm the setting persists via GET /api/v1/settings.
	getRespSettings := n.apiDo(t, "GET", "/api/v1/settings", "")
	assertStatus(t, getRespSettings, http.StatusOK)
	var getSettings map[string]any
	if err := json.NewDecoder(getRespSettings.Body).Decode(&getSettings); err != nil {
		getRespSettings.Body.Close()
		t.Fatalf("decode GET settings response: %v", err)
	}
	getRespSettings.Body.Close()

	ql2, ok := getSettings["query_log"].(map[string]any)
	if !ok {
		t.Fatalf("GET settings response missing query_log object: %v", getSettings)
	}
	got2, ok := ql2["max_entries"].(float64)
	if !ok {
		t.Fatalf("GET query_log.max_entries not numeric: %v", ql2["max_entries"])
	}
	if int(got2) != newMax {
		t.Fatalf("expected persisted query_log.max_entries=%d, got %d", newMax, int(got2))
	}
}
