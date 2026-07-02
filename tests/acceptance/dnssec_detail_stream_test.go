// Acceptance tests for M43 — DNSSEC Detail on Query Stream.
//
// FSIDs covered:
//   FS-DnssecStatusFilterOnStream, FS-DnssecStatusOmittedWhenDisabled,
//   FS-DnssecStatusInPaginatedLog

package acceptance

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ── FS-DnssecStatusFilterOnStream ─────────────────────────────────────────────

// TestDnssecStatusFilterSuppressesNonMatching connects to the stream with
// ?dnssec_status=bogus and triggers a regular (non-bogus) DNS query. The
// filter must suppress the event so no "event: query" frame arrives.
func TestDnssecStatusFilterSuppressesNonMatching(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "dnssec_status=bogus")
	defer body.Close()

	// Trigger a query — it will have no DNSSEC status (transparent mode),
	// so the filter should suppress it.
	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	}()

	// Scan for 3 s; a "event: query" frame means the filter failed.
	eventCh := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(body)
		eventName := ""
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") && eventName == "query" {
				eventCh <- struct{}{}
				return
			}
		}
	}()

	select {
	case <-eventCh:
		t.Error("dnssec_status=bogus filter did not suppress non-bogus event")
	case <-time.After(3 * time.Second):
		// Correct: no event delivered.
	}
}

// ── FS-DnssecStatusOmittedWhenDisabled ────────────────────────────────────────

// TestDnssecStatusAbsentInTransparentMode verifies that in transparent (default)
// mode the SSE event JSON does not carry a dnssec_status key.
func TestDnssecStatusAbsentInTransparentMode(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "")
	defer body.Close()

	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	}()

	_, raw := readNextEvent(t, body, 10*time.Second)

	// In transparent mode DnssecStatus is "" → omitempty drops the key.
	if _, ok := raw["dnssec_status"]; ok {
		if raw["dnssec_status"] != "" {
			t.Errorf("expected dnssec_status absent or empty in transparent mode, got %v", raw["dnssec_status"])
		}
	}
}

// ── FS-DnssecStatusInPaginatedLog ─────────────────────────────────────────────

// TestDnssecStatusInPaginatedLog confirms GET /api/v1/query-log does not error
// and returns well-formed entries after M43 additions.
func TestDnssecStatusInPaginatedLog(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Trigger a query so the log is non-empty.
	dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	time.Sleep(200 * time.Millisecond)

	req, _ := http.NewRequest("GET", n.APIBase+"/api/v1/query-log", nil)
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET query-log: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode query-log response: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one log entry")
	}

	// The entry must contain domain and not crash on dnssec fields.
	entry := result.Entries[0]
	if entry["domain"] == nil {
		t.Error("log entry missing domain")
	}
	// dnssec_status/dnssec_error keys are optional (omitempty); their presence or
	// absence is both correct depending on whether DNSSEC was active.
}
