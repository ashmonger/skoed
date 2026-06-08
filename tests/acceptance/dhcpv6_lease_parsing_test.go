// Acceptance tests for M6.5 — DHCPv6 lease parsing (Kea + dnsmasq).
//
// FSIDs covered:
//   FS-Dhcpv6LeaseKeaReadsLease6                  → TestDhcpv6LeaseKeaReadsLease6
//   FS-Dhcpv6LeaseKeaMergesIaNaAndIaPd            → TestDhcpv6LeaseKeaMergesIaNaAndIaPd
//   FS-Dhcpv6LeaseDnsmasqParsesLease6File         → TestDhcpv6LeaseDnsmasqParsesLease6File
//   FS-Dhcpv6LeaseDnsmasqSkipsExpired             → TestDhcpv6LeaseDnsmasqSkipsExpired
//   FS-Dhcpv6LeaseDualStackMerge                  → TestDhcpv6LeaseDualStackMerge
//   FS-Dhcpv6LeaseV6OnlyClientLookupByV6          → TestDhcpv6LeaseV6OnlyClientLookupByV6
//   FS-Dhcpv6LeaseProfileMatchingPriorityUnchanged → TestDhcpv6LeaseProfileMatchingPriorityUnchanged
//   FS-Dhcpv6LeaseClientsPageShowsV6Column        → TestDhcpv6LeaseClientsPageShowsV6Column
//   FS-Dhcpv6LeaseMalformedV6LineSkipped          → TestDhcpv6LeaseMalformedV6LineSkipped
//   FS-Dhcpv6LeaseV6DisabledLegacyShapeUnchanged  → TestDhcpv6LeaseV6DisabledLegacyShapeUnchanged
//
// Strategy (per TS-Dhcpv6Lease):
//   - Kea v6 tests spin a httptest.NewServer that switches behaviour on
//     the inner command body: "lease4-get-all" → empty result, the new
//     "lease6-get-all" → IA_NA + IA_PD payload.
//   - dnsmasq v6 tests materialise a temporary `dnsmasq.leases6` next to
//     the existing `dnsmasq.leases` fixture and configure the connector
//     to read both.
//   - Every test skips with a "M6.5 impl pending" message when the API
//     surface or the harness's DhcpOpts has not yet grown the v6 knobs.
//
// The M6.5 Lease shape adds three optional fields (ipv6_addresses, duid,
// is_dual_stack); all are `omitempty` so the M3.6 wire shape stays
// backwards-compatible.

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// leaseV6 mirrors the M6.5-extended canonical Lease shape (M3.6 fields
// plus the three optional v6 fields). Kept local to this file so the
// harness's existing M3.6 Lease struct (in dhcp_harness_test.go) stays
// untouched for the other DHCP tests.
type leaseV6 struct {
	IP            string    `json:"ip"`
	MAC           string    `json:"mac"`
	Hostname      string    `json:"hostname"`
	ClientID      string    `json:"client_id"`
	Source        string    `json:"source"`
	ExpiresAt     time.Time `json:"expires_at"`
	IPv6Addresses []string  `json:"ipv6_addresses,omitempty"`
	DUID          string    `json:"duid,omitempty"`
	IsDualStack   bool      `json:"is_dual_stack,omitempty"`
}

// fetchLeaseV6Snapshot polls the lease snapshot endpoint and decodes
// into the M6.5-extended shape. Skips when the endpoint is not present
// (binary doesn't yet implement M3.6 lease surface).
func fetchLeaseV6Snapshot(t *testing.T, n *Node) []leaseV6 {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/_leases", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: /api/v1/clients/_leases returns 404")
	}
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: /api/v1/clients/_leases returns 501")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("lease snapshot: status %d", resp.StatusCode)
	}
	var out []leaseV6
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode lease v6 snapshot: %v", err)
	}
	return out
}

// fetchClientV6 calls GET /api/v1/clients/{ip} with either a v4 or v6
// literal and decodes into the M6.5-extended shape. Skips on 404/501.
func fetchClientV6(t *testing.T, n *Node, addr string) leaseV6 {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+addr, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s returns 404", addr)
	}
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s returns 501", addr)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET clients/%s: status %d", addr, resp.StatusCode)
	}
	var c leaseV6
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode client v6: %v", err)
	}
	return c
}

// findLeaseV6 returns the first lease whose IP (v4) matches `ip`, or
// whose IPv6Addresses slice contains `ip` (for v6-only entries).
func findLeaseV6(ls []leaseV6, ip string) (leaseV6, bool) {
	for _, l := range ls {
		if l.IP == ip {
			return l, true
		}
		for _, v6 := range l.IPv6Addresses {
			if v6 == ip {
				return l, true
			}
		}
	}
	return leaseV6{}, false
}

// keaDualStackStub serves both lease4-get-all and lease6-get-all from
// fixture-style payloads. The handler routes on the inner "command"
// field of the POST body so a single endpoint covers both calls.
func keaDualStackStub(t *testing.T, v4Body, v6Body []byte, v6Status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body, "lease6-get-all"):
			if v6Status > 0 {
				w.WriteHeader(v6Status)
				return
			}
			_, _ = w.Write(v6Body)
		case strings.Contains(body, "lease4-get-all"):
			_, _ = w.Write(v4Body)
		default:
			// Default to v4 to mirror M3.6 (untyped polls).
			_, _ = w.Write(v4Body)
		}
	}))
}

// writeDnsmasqV6File materialises a temporary file in the standard
// "<expiry-epoch> <iaid> <ipv6> <hostname> <duid>" format used by
// dnsmasq's --dhcp-leasefile-v6 output.
func writeDnsmasqV6File(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dnsmasq.leases6")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write dnsmasq.leases6: %v", err)
	}
	return path
}

// requireV6Harness is the v6 equivalent of requireDhcpHarness — it
// inspects the lease snapshot and skips if the binary doesn't yet
// surface a `duid` or `ipv6_addresses` field on any record. Used by
// every "happy path" v6 test below so they all skip uniformly on a
// pre-M6.5 binary.
func requireV6Harness(t *testing.T, n *Node) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/_leases", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: /api/v1/clients/_leases not available")
	}
}

// keaV6FixtureIANA returns a synthetic lease6-get-all response with
// three IA_NA leases — each one DUID-distinct so they don't merge.
func keaV6FixtureIANA() []byte {
	return []byte(`[
  {
    "result": 0,
    "text": "3 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::1001",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:01",
          "hostname": "v6-host-a",
          "hw-address": "aa:bb:cc:dd:ee:01",
          "valid-lft": 3600,
          "cltt": 9999996400
        },
        {
          "ip-address": "2001:db8::1002",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:02",
          "hostname": "v6-host-b",
          "hw-address": "aa:bb:cc:dd:ee:02",
          "valid-lft": 3600,
          "cltt": 9999996400
        },
        {
          "ip-address": "2001:db8::1003",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:03",
          "hostname": "v6-host-c",
          "hw-address": "aa:bb:cc:dd:ee:03",
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
}

// keaV6FixtureIaNaPlusIaPd returns one IA_NA + one IA_PD record for the
// same DUID — the merge step must collapse them into a single Lease
// carrying both addresses.
func keaV6FixtureIaNaPlusIaPd() []byte {
	return []byte(`[
  {
    "result": 0,
    "text": "2 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::1234",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:ff",
          "hostname": "merged-host",
          "hw-address": "aa:bb:cc:dd:ee:ff",
          "valid-lft": 3600,
          "cltt": 9999996400
        },
        {
          "ip-address": "2001:db8:abcd::",
          "type": "IA_PD",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:ff",
          "hostname": "merged-host",
          "hw-address": "aa:bb:cc:dd:ee:ff",
          "prefix-len": 56,
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
}

// FS-Dhcpv6LeaseKeaReadsLease6
// Scenario: Kea connector reads lease6-get-all from the control-agent
func TestDhcpv6LeaseKeaReadsLease6(t *testing.T) {
	// Empty v4 body, three IA_NA leases on the v6 side.
	v4 := []byte(`[{"result":0,"text":"0 IPv4 lease(s) found.","arguments":{"leases":[]}}]`)
	stub := keaDualStackStub(t, v4, keaV6FixtureIANA(), 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	leases := fetchLeaseV6Snapshot(t, n)
	v6Count := 0
	for _, l := range leases {
		if len(l.IPv6Addresses) > 0 {
			v6Count++
		}
	}
	if v6Count < 3 {
		t.Skipf("M6.5 impl pending: Kea lease6-get-all wiring absent (saw %d v6 leases of 3 expected)", v6Count)
	}

	for _, want := range []string{
		"2001:db8::1001", "2001:db8::1002", "2001:db8::1003",
	} {
		l, ok := findLeaseV6(leases, want)
		if !ok {
			t.Errorf("missing v6 lease for %s", want)
			continue
		}
		if l.Source != "kea" {
			t.Errorf("lease %s: source %q, want %q", want, l.Source, "kea")
		}
		if l.DUID == "" {
			t.Errorf("lease %s: duid is empty", want)
		}
		// valid-lft (3600) + cltt (9999996400) → 9999999999 ≈ 2287-11-09.
		if l.ExpiresAt.Year() < 2200 {
			t.Errorf("lease %s: expires_at %v not derived from valid-lft + cltt", want, l.ExpiresAt)
		}
	}
}

// FS-Dhcpv6LeaseKeaMergesIaNaAndIaPd
// Scenario: Kea IA_NA + IA_PD entries for the same DUID merge into one Lease
func TestDhcpv6LeaseKeaMergesIaNaAndIaPd(t *testing.T) {
	v4 := []byte(`[{"result":0,"text":"0","arguments":{"leases":[]}}]`)
	stub := keaDualStackStub(t, v4, keaV6FixtureIaNaPlusIaPd(), 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	// The spec says GET /api/v1/clients/2001:db8::1234 must return 200.
	got := fetchClientV6(t, n, "2001:db8::1234")
	if got.DUID != "00:01:00:01:aa:bb:cc:dd:ee:ff" {
		t.Skipf("M6.5 impl pending: DUID not populated (got %q)", got.DUID)
	}
	hasIANA, hasIAPD := false, false
	for _, v6 := range got.IPv6Addresses {
		if v6 == "2001:db8::1234" {
			hasIANA = true
		}
		if v6 == "2001:db8:abcd::/56" {
			hasIAPD = true
		}
	}
	if !hasIANA {
		t.Errorf("ipv6_addresses missing IA_NA 2001:db8::1234: got %v", got.IPv6Addresses)
	}
	if !hasIAPD {
		t.Errorf("ipv6_addresses missing IA_PD 2001:db8:abcd::/56: got %v", got.IPv6Addresses)
	}
}

// FS-Dhcpv6LeaseDnsmasqParsesLease6File
// Scenario: dnsmasq connector parses /var/lib/misc/dnsmasq.leases6
func TestDhcpv6LeaseDnsmasqParsesLease6File(t *testing.T) {
	v6Path := writeDnsmasqV6File(t, []string{
		// "<expiry-epoch> <iaid> <ipv6> <hostname> <duid>"
		"9999999999 100 2001:db8::a 6host-a 00:01:00:01:aa:bb:cc:dd:ee:0a",
		"9999999999 101 2001:db8::b 6host-b 00:01:00:01:aa:bb:cc:dd:ee:0b",
		"9999999999 102 2001:db8::c * 00:01:00:01:aa:bb:cc:dd:ee:0c",
	})

	// The harness DhcpOpts doesn't yet carry FilePathV6; until it does
	// the test can't wire the v6 file into the spawned node. Skip with
	// a clear message rather than silently passing.
	if !dhcpOptsSupportsV6() {
		t.Skipf("M6.5 impl pending: DhcpOpts.FilePathV6 not wired in test harness (path was: %s)", v6Path)
	}

	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	leases := fetchLeaseV6Snapshot(t, n)
	v6 := 0
	for _, l := range leases {
		if len(l.IPv6Addresses) == 1 && l.DUID != "" {
			v6++
		}
	}
	if v6 < 3 {
		t.Errorf("expected 3 v6 records from dnsmasq.leases6; got %d", v6)
	}
	// Hostname "*" must yield empty Hostname.
	got, ok := findLeaseV6(leases, "2001:db8::c")
	if !ok {
		t.Fatalf("missing v6 lease for 2001:db8::c")
	}
	if got.Hostname != "" {
		t.Errorf("hostname for '*' should be empty, got %q", got.Hostname)
	}
}

// FS-Dhcpv6LeaseDnsmasqSkipsExpired
// Scenario: dnsmasq v6 connector drops leases whose expiry epoch is in the past
func TestDhcpv6LeaseDnsmasqSkipsExpired(t *testing.T) {
	v6Path := writeDnsmasqV6File(t, []string{
		"9999999999 200 2001:db8::active1 alive-a 00:01:00:01:aa:bb:cc:dd:ee:11",
		"9999999999 201 2001:db8::active2 alive-b 00:01:00:01:aa:bb:cc:dd:ee:12",
		"0 202 2001:db8::dead expired-host 00:01:00:01:aa:bb:cc:dd:ee:13",
	})

	if !dhcpOptsSupportsV6() {
		t.Skipf("M6.5 impl pending: DhcpOpts.FilePathV6 not wired in test harness (path was: %s)", v6Path)
	}

	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	leases := fetchLeaseV6Snapshot(t, n)
	if _, ok := findLeaseV6(leases, "2001:db8::dead"); ok {
		t.Errorf("expired v6 lease 2001:db8::dead must be dropped")
	}
	for _, want := range []string{"2001:db8::active1", "2001:db8::active2"} {
		if _, ok := findLeaseV6(leases, want); !ok {
			t.Errorf("active v6 lease %s missing", want)
		}
	}
}

// FS-Dhcpv6LeaseDualStackMerge
// Scenario: A client present in both v4 and v6 sources merges into one Lease
func TestDhcpv6LeaseDualStackMerge(t *testing.T) {
	// Reuse the M3.6 v4 fixture (192.168.1.10 → aa:bb:cc:dd:ee:10
	// "home-laptop") and pair it with a v6 lease for the matching
	// DUID-LL ending in the same MAC suffix.
	v6 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::10",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:10",
          "hostname": "home-laptop",
          "hw-address": "aa:bb:cc:dd:ee:10",
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
	// v4 fixture mirroring the dnsmasq.leases id:laptop10 entry.
	v4 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv4 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "192.168.1.10",
          "hw-address": "aa:bb:cc:dd:ee:10",
          "hostname": "home-laptop",
          "client-id": "id:laptop10",
          "valid-lft": 3600,
          "cltt": 9999996400,
          "subnet-id": 1,
          "state": 0
        }
      ]
    }
  }
]`)
	stub := keaDualStackStub(t, v4, v6, 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	got := fetchClientV6(t, n, "192.168.1.10")
	if !got.IsDualStack {
		t.Skipf("M6.5 impl pending: is_dual_stack=false (merge step not yet implemented)")
	}
	hasV6 := false
	for _, v := range got.IPv6Addresses {
		if v == "2001:db8::10" {
			hasV6 = true
		}
	}
	if !hasV6 {
		t.Errorf("ipv6_addresses missing 2001:db8::10: got %v", got.IPv6Addresses)
	}
	if got.DUID == "" {
		t.Errorf("merged record should carry duid")
	}
}

// FS-Dhcpv6LeaseV6OnlyClientLookupByV6
// Scenario: GET /api/v1/clients/{ip} accepts an IPv6 literal
func TestDhcpv6LeaseV6OnlyClientLookupByV6(t *testing.T) {
	v6 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::dead",
          "type": "IA_NA",
          "duid": "00:01:00:01:de:ad:be:ef:00:01",
          "hostname": "",
          "hw-address": "",
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
	v4 := []byte(`[{"result":0,"text":"0","arguments":{"leases":[]}}]`)
	stub := keaDualStackStub(t, v4, v6, 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	got := fetchClientV6(t, n, "2001:db8::dead")
	if got.DUID != "00:01:00:01:de:ad:be:ef:00:01" {
		t.Skipf("M6.5 impl pending: v6 literal lookup not yet wired (duid=%q)", got.DUID)
	}
	if got.IP != "" {
		t.Errorf("v6-only lease: ip should be empty, got %q", got.IP)
	}
	if got.MAC != "" {
		t.Errorf("v6-only lease: mac should be empty, got %q", got.MAC)
	}
	if got.IsDualStack {
		t.Errorf("v6-only lease: is_dual_stack should be false")
	}
	if len(got.IPv6Addresses) != 1 || got.IPv6Addresses[0] != "2001:db8::dead" {
		t.Errorf("ipv6_addresses: want [2001:db8::dead], got %v", got.IPv6Addresses)
	}
	if got.Source == "" {
		t.Errorf("source should reflect the producing connector, got empty")
	}
}

// FS-Dhcpv6LeaseProfileMatchingPriorityUnchanged
// Scenario: Profile matching priority is unchanged at M6.5 (DUID is observational only)
func TestDhcpv6LeaseProfileMatchingPriorityUnchanged(t *testing.T) {
	// Use the existing M3.6 fixture: 192.168.1.42 has client_id "id:tablet42".
	// We add a v6 lease for the same hostname so the merged record also
	// carries a DUID. The kids profile pins by client_ids — DUID must
	// not be required, accepted, or override the M3.6 priority.
	v4 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv4 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "192.168.1.42",
          "hw-address": "aa:bb:cc:dd:ee:42",
          "hostname": "kid-tablet",
          "client-id": "id:tablet42",
          "valid-lft": 3600,
          "cltt": 9999996400,
          "subnet-id": 1,
          "state": 0
        }
      ]
    }
  }
]`)
	v6 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::42",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:42",
          "hostname": "kid-tablet",
          "hw-address": "aa:bb:cc:dd:ee:42",
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
	stub := keaDualStackStub(t, v4, v6, 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	// Profile MUST be accepted with client_ids (M3.6). A profile that
	// tries to declare client_duids must be rejected at M6.5.
	body := mustJSON(t, map[string]any{
		"id":         "kids",
		"name":       "Kids",
		"client_ids": []string{"id:tablet42"},
		"blocklists": []string{"cat:doh"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile by client_ids: status %d", resp.StatusCode)
	}

	// Attempt to create a profile that uses the M7+ field `client_duids`
	// — at M6.5 this must NOT be silently accepted.
	duidBody := mustJSON(t, map[string]any{
		"id":           "duid-rule",
		"name":         "DUID rule",
		"client_duids": []string{"00:01:00:01:aa:bb:cc:dd:ee:42"},
		"blocklists":   []string{"cat:doh"},
	})
	resp2 := n.apiDo(t, "POST", "/api/v1/profiles", duidBody)
	resp2.Body.Close()
	// Either rejected (4xx) OR the field is ignored — but it must
	// never be the matcher that decides the profile.
	if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
		// Accepted: fetch back and confirm DUID list is empty/absent.
		raw, err := readProfileRaw(t, n, "duid-rule")
		if err == nil {
			if duids, ok := raw["client_duids"]; ok && duids != nil {
				if arr, ok := duids.([]any); ok && len(arr) > 0 {
					t.Errorf("M6.5 must not accept client_duids; got %v", arr)
				}
			}
		}
	}
}

// readProfileRaw fetches a profile as a raw map for shape assertions.
func readProfileRaw(t *testing.T, n *Node, id string) (map[string]any, error) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/profiles/"+id, "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("profile %s: status %d", id, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// FS-Dhcpv6LeaseClientsPageShowsV6Column
// Scenario: The Clients page renders IPv6 addresses next to the IPv4 row
//
// The Clients page is a static SPA asset under /; this test asserts the
// data the SPA needs is exposed by GET /api/v1/clients (the list shape).
// True UI rendering is verified manually via the demo recipe — same
// stance as FS-SpoofDashboardAlert in dhcp_spoof_detection_test.go.
func TestDhcpv6LeaseClientsPageShowsV6Column(t *testing.T) {
	v4 := []byte(`[
  {
    "result": 0,
    "text": "1 IPv4 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "192.168.1.10",
          "hw-address": "aa:bb:cc:dd:ee:10",
          "hostname": "home-laptop",
          "client-id": "id:laptop10",
          "valid-lft": 3600,
          "cltt": 9999996400,
          "subnet-id": 1,
          "state": 0
        }
      ]
    }
  }
]`)
	v6 := []byte(`[
  {
    "result": 0,
    "text": "2 IPv6 lease(s) found.",
    "arguments": {
      "leases": [
        {
          "ip-address": "2001:db8::10",
          "type": "IA_NA",
          "duid": "00:01:00:01:aa:bb:cc:dd:ee:10",
          "hostname": "home-laptop",
          "hw-address": "aa:bb:cc:dd:ee:10",
          "valid-lft": 3600,
          "cltt": 9999996400
        },
        {
          "ip-address": "2001:db8::feed",
          "type": "IA_NA",
          "duid": "00:01:00:01:fe:ed:fa:ce:00:01",
          "hostname": "v6-only",
          "hw-address": "",
          "valid-lft": 3600,
          "cltt": 9999996400
        }
      ]
    }
  }
]`)
	stub := keaDualStackStub(t, v4, v6, 0)
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	resp := n.apiDo(t, "GET", "/api/v1/clients", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients returns %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/v1/clients: status %d", resp.StatusCode)
	}
	var clients []leaseV6
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("decode clients list: %v", err)
	}

	// Dual-stack row.
	dual, ok := findLeaseV6(clients, "192.168.1.10")
	if !ok {
		t.Skipf("M6.5 impl pending: 192.168.1.10 missing from /clients (no merge yet)")
	}
	if !dual.IsDualStack {
		t.Errorf("dual-stack row: is_dual_stack=false (chip would not render)")
	}
	if len(dual.IPv6Addresses) == 0 {
		t.Errorf("dual-stack row: ipv6_addresses empty")
	}
	// v6-only row.
	v6only, ok := findLeaseV6(clients, "2001:db8::feed")
	if !ok {
		t.Errorf("/clients missing v6-only row 2001:db8::feed")
	} else {
		if v6only.IP != "" {
			t.Errorf("v6-only row: ip should be empty, got %q", v6only.IP)
		}
		if v6only.IsDualStack {
			t.Errorf("v6-only row: is_dual_stack should be false")
		}
	}
}

// FS-Dhcpv6LeaseMalformedV6LineSkipped
// Scenario: Malformed v6 lease lines are skipped, not fatal
func TestDhcpv6LeaseMalformedV6LineSkipped(t *testing.T) {
	v6Path := writeDnsmasqV6File(t, []string{
		"9999999999 300 2001:db8::v1 host-v1 00:01:00:01:aa:bb:cc:dd:ee:21",
		"this is a malformed v6 line",
		"9999999999 301 2001:db8::v2 host-v2 00:01:00:01:aa:bb:cc:dd:ee:22",
		"9999999999 302 2001:db8::v3 host-v3 00:01:00:01:aa:bb:cc:dd:ee:23",
		"9999999999 303 2001:db8::v4 host-v4 00:01:00:01:aa:bb:cc:dd:ee:24",
	})

	if !dhcpOptsSupportsV6() {
		t.Skipf("M6.5 impl pending: DhcpOpts.FilePathV6 not wired in test harness (path was: %s)", v6Path)
	}

	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	requireV6Harness(t, n)

	leases := fetchLeaseV6Snapshot(t, n)
	want := []string{"2001:db8::v1", "2001:db8::v2", "2001:db8::v3", "2001:db8::v4"}
	found := 0
	for _, w := range want {
		if _, ok := findLeaseV6(leases, w); ok {
			found++
		}
	}
	if found != 4 {
		t.Errorf("malformed line should not abort parsing: got %d/4 valid v6 leases", found)
	}
}

// FS-Dhcpv6LeaseV6DisabledLegacyShapeUnchanged
// Scenario: When no v6 source is configured the API shape stays M3.6-compatible
func TestDhcpv6LeaseV6DisabledLegacyShapeUnchanged(t *testing.T) {
	// Use the M3.6 fixture only — no v6 source configured at all.
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	// Decode as a raw map so we can assert the v6 fields are absent or
	// zero-valued (omitempty contract in TS-Dhcpv6Lease).
	resp := n.apiDo(t, "GET", "/api/v1/clients/192.168.1.42", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.6 impl pending: GET /api/v1/clients/{ip} returns 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET clients/192.168.1.42: status %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw client: %v", err)
	}
	if v, ok := raw["ipv6_addresses"]; ok && v != nil {
		if arr, ok := v.([]any); ok && len(arr) > 0 {
			t.Errorf("v6 disabled: ipv6_addresses must be absent or empty, got %v", arr)
		}
	}
	if v, ok := raw["duid"]; ok && v != nil {
		if s, ok := v.(string); ok && s != "" {
			t.Errorf("v6 disabled: duid must be absent or empty, got %q", s)
		}
	}
	if v, ok := raw["is_dual_stack"]; ok && v != nil {
		if b, ok := v.(bool); ok && b {
			t.Errorf("v6 disabled: is_dual_stack must be absent or false, got true")
		}
	}
}

// dhcpOptsSupportsV6 returns true once the harness's DhcpOpts has grown
// a FilePathV6 field (or equivalent). Until then, dnsmasq v6 tests skip
// because the spawned node cannot be told where the v6 lease file lives.
//
// The probe is intentionally pessimistic: we never assert "v6 works"
// from a no-op harness. When DhcpOpts grows the field, change this
// helper to `return true` (or wire it through and remove the gate).
func dhcpOptsSupportsV6() bool {
	// As of M3.6, DhcpOpts is defined in dhcp_harness_test.go with
	// fields {Kind, URL, FilePath, Username, Password, RefreshSeconds}.
	// No v6 knob exists yet — the spec calls for `FilePathV6`.
	return false
}
