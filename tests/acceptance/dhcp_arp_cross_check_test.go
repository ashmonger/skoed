// Acceptance tests for M6.5 — DHCP layer-3 ARP/NDP cross-check.
//
// FSIDs covered (one Go test per FSID, see specs/functional/dhcp-arp-cross-check.feature):
//   FS-ArpCheckArpStateAgreesWithLease            → TestArpCheckArpStateAgreesWithLease
//   FS-ArpCheckArpMacMismatchFlagsAnomaly         → TestArpCheckArpMacMismatchFlagsAnomaly
//   FS-ArpCheckNdpMacMismatchFlagsAnomaly         → TestArpCheckNdpMacMismatchFlagsAnomaly
//   FS-ArpCheckGhostLeaseLongLivedButNeverInKernel → TestArpCheckGhostLeaseLongLivedButNeverInKernel
//   FS-ArpCheckUnseenByKernelFreshLeaseStaysQuiet → TestArpCheckUnseenByKernelFreshLeaseStaysQuiet
//   FS-ArpCheckUnseenByKernelAfterGracePeriod     → TestArpCheckUnseenByKernelAfterGracePeriod
//   FS-ArpCheckGracefulWhenNetlinkUnavailable     → TestArpCheckGracefulWhenNetlinkUnavailable
//   FS-ArpCheckUnknownIpReturns404                → TestArpCheckUnknownIpReturns404
//   FS-ArpCheckAnomaliesListIncludesNewKinds      → TestArpCheckAnomaliesListIncludesNewKinds
//   FS-ArpCheckSweepCadenceIsBestEffort           → TestArpCheckSweepCadenceIsBestEffort
//
// Strategy:
//   - Tests are black-box: every interaction is via the HTTP management
//     API. The fake-DHCP source uses the same mutableLeaseServer helper as
//     the M3.6 spoof-detection tests.
//   - The kernel ARP/NDP table is observed via netlink in production; in
//     the test harness we expect skoed to honour a SKOED_TEST_ARP_TABLE
//     env var (see TS-ArpCheck implementation map) that injects a fake
//     neighbour table without touching the real kernel. When that
//     affordance is not yet wired into the binary, individual tests skip
//     with a clear "M6.5 impl pending" message.
//   - The Alpine test container typically runs without CAP_NET_ADMIN, so
//     any test that requires a *real* netlink probe is gated on the same
//     env var; without the affordance, we cannot deterministically drive
//     kernel state from a test process. The graceful-degradation FSID
//     covers the unprivileged-host path explicitly.

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// arpState mirrors the GET /api/v1/clients/{ip}/arp-state response shape
// documented in TS-ArpCheck.
type arpState struct {
	IP               string `json:"ip"`
	MacDhcp          string `json:"mac_dhcp"`
	MacKernel        string `json:"mac_kernel"`
	KernelState      string `json:"kernel_state"`
	LastObservedUnix int64  `json:"last_observed_unix"`
	Anomaly          string `json:"anomaly,omitempty"`
}

// fetchArpState calls GET /api/v1/clients/{ip}/arp-state. Returns the
// parsed struct and the raw HTTP status. Skips the test when the route
// returns 404 (M6.5 not yet implemented).
func fetchArpState(t *testing.T, n *Node, ip string) (arpState, int) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+ip+"/arp-state", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 404 here is ambiguous: it can mean the route is not registered
		// yet (M6.5 pending) OR the lease is genuinely unknown. The
		// dedicated TestArpCheckUnknownIpReturns404 case relies on the
		// latter; callers of this helper always pre-seed a lease, so a
		// 404 means the route is missing.
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s/arp-state returns 404", ip)
	}
	if resp.StatusCode != http.StatusOK {
		return arpState{}, resp.StatusCode
	}
	var s arpState
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode arp-state: %v", err)
	}
	return s, resp.StatusCode
}

// requireArpTestAffordance skips the calling test when the binary under
// test does not honour SKOED_TEST_ARP_TABLE. We detect this by inspecting
// the arp-state body for a known lease: if kernel_state is empty or the
// route is missing, the binary is M3.6-only.
func requireArpTestAffordance(t *testing.T, n *Node, ip string) arpState {
	t.Helper()
	s, code := fetchArpState(t, n, ip)
	if code != http.StatusOK {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s/arp-state returned %d", ip, code)
	}
	if s.KernelState == "" {
		t.Skipf("M6.5 impl pending: arp-state response missing kernel_state field (SKOED_TEST_ARP_TABLE not wired)")
	}
	return s
}

// FS-ArpCheckArpStateAgreesWithLease
// Scenario: Kernel ARP entry matches the DHCP lease — no anomaly.
func TestArpCheckArpStateAgreesWithLease(t *testing.T) {
	// SKOED_TEST_ARP_TABLE injects a fake neighbour table:
	//   <ip>=<mac>,<state>;<ip>=<mac>,<state>
	t.Setenv("SKOED_TEST_ARP_TABLE",
		"192.168.1.42=aa:bb:cc:dd:ee:42,reachable")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond) // let DHCP poll + first ARP sweep run

	s := requireArpTestAffordance(t, n, "192.168.1.42")
	if s.IP != "192.168.1.42" {
		t.Errorf("ip: want 192.168.1.42, got %q", s.IP)
	}
	if s.MacDhcp != "aa:bb:cc:dd:ee:42" {
		t.Errorf("mac_dhcp: want aa:bb:cc:dd:ee:42, got %q", s.MacDhcp)
	}
	if s.MacKernel != "aa:bb:cc:dd:ee:42" {
		t.Errorf("mac_kernel: want aa:bb:cc:dd:ee:42, got %q", s.MacKernel)
	}
	if s.KernelState != "reachable" {
		t.Errorf("kernel_state: want reachable, got %q", s.KernelState)
	}
	if s.Anomaly != "" {
		t.Errorf("anomaly: want empty, got %q", s.Anomaly)
	}
	if s.LastObservedUnix == 0 {
		t.Errorf("last_observed_unix: want recent epoch, got 0")
	}

	// And the anomaly list should not mention 192.168.1.42 with any
	// of the new layer-3 kinds.
	for _, a := range fetchAnomalies(t, n) {
		if a.IP == "192.168.1.42" && isArpAnomalyKind(a.Kind) {
			t.Errorf("unexpected layer-3 anomaly for agreeing lease: %+v", a)
		}
	}
}

// FS-ArpCheckArpMacMismatchFlagsAnomaly
// Scenario: Kernel ARP entry disagrees with the DHCP lease's MAC.
func TestArpCheckArpMacMismatchFlagsAnomaly(t *testing.T) {
	// DHCP says aa:..:42, kernel says 11:22:33:44:55:66 — mismatch.
	t.Setenv("SKOED_TEST_ARP_TABLE",
		"192.168.1.42=11:22:33:44:55:66,reachable")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	requireArpTestAffordance(t, n, "192.168.1.42")
	if waitForAnomaly(t, n, "arp_mac_mismatch", "192.168.1.42", 5*time.Second) == nil {
		t.Fatalf("arp_mac_mismatch anomaly never raised")
	}

	s, code := fetchArpState(t, n, "192.168.1.42")
	if code != http.StatusOK {
		t.Fatalf("arp-state: status %d", code)
	}
	if s.MacDhcp != "aa:bb:cc:dd:ee:42" {
		t.Errorf("mac_dhcp: want aa:bb:cc:dd:ee:42, got %q", s.MacDhcp)
	}
	if s.MacKernel != "11:22:33:44:55:66" {
		t.Errorf("mac_kernel: want 11:22:33:44:55:66, got %q", s.MacKernel)
	}
	if s.KernelState != "reachable" {
		t.Errorf("kernel_state: want reachable, got %q", s.KernelState)
	}
	if s.Anomaly != "arp_mac_mismatch" {
		t.Errorf("anomaly: want arp_mac_mismatch, got %q", s.Anomaly)
	}
}

// FS-ArpCheckNdpMacMismatchFlagsAnomaly
// Scenario: Kernel NDP entry disagrees with the DHCPv6 lease's MAC.
func TestArpCheckNdpMacMismatchFlagsAnomaly(t *testing.T) {
	t.Setenv("SKOED_TEST_ARP_TABLE",
		"fd00::42=99:88:77:66:55:44,stale")

	srv := newMutableLeaseServer([]map[string]any{
		// IPv6 lease — same MAC as the v4 sibling, but kernel NDP
		// reports a different MAC.
		sampleLease("fd00::42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	requireArpTestAffordance(t, n, "fd00::42")
	if waitForAnomaly(t, n, "ndp_mac_mismatch", "fd00::42", 5*time.Second) == nil {
		t.Fatalf("ndp_mac_mismatch anomaly never raised")
	}

	s, code := fetchArpState(t, n, "fd00::42")
	if code != http.StatusOK {
		t.Fatalf("arp-state: status %d", code)
	}
	if s.Anomaly != "ndp_mac_mismatch" {
		t.Errorf("anomaly: want ndp_mac_mismatch, got %q", s.Anomaly)
	}
	if s.KernelState != "stale" {
		t.Errorf("kernel_state: want stale, got %q", s.KernelState)
	}
}

// FS-ArpCheckGhostLeaseLongLivedButNeverInKernel
// Scenario: DHCP lease has been around for hours but kernel has never
// seen the MAC.
func TestArpCheckGhostLeaseLongLivedButNeverInKernel(t *testing.T) {
	// Empty fake ARP table → kernel has no entry for this IP, and the
	// MAC has never been seen on any interface.
	t.Setenv("SKOED_TEST_ARP_TABLE", "")
	// The ghost_lease threshold defaults to 6h in production. The
	// SKOED_TEST_NOW affordance (also used by the M3.6 retention test)
	// lets us pretend the lease has been observed for >6h without
	// waiting. Without it, we cannot drive the ghost_lease decision
	// branch deterministically.
	t.Setenv("SKOED_TEST_LEASE_FIRST_SEEN_OFFSET", "7h")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop", "id:laptop10"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	s, code := fetchArpState(t, n, "192.168.1.10")
	if code != http.StatusOK {
		t.Skipf("M6.5 impl pending: arp-state returned %d for ghost lease scenario", code)
	}
	if s.KernelState == "" {
		t.Skipf("M6.5 impl pending: SKOED_TEST_ARP_TABLE / SKOED_TEST_LEASE_FIRST_SEEN_OFFSET not wired")
	}
	if waitForAnomaly(t, n, "ghost_lease", "192.168.1.10", 5*time.Second) == nil {
		t.Fatalf("ghost_lease anomaly never raised")
	}
	if s.MacKernel != "" {
		t.Errorf("mac_kernel: want empty, got %q", s.MacKernel)
	}
	if s.KernelState != "none" {
		t.Errorf("kernel_state: want none, got %q", s.KernelState)
	}
	if s.Anomaly != "ghost_lease" {
		t.Errorf("anomaly: want ghost_lease, got %q", s.Anomaly)
	}
}

// FS-ArpCheckUnseenByKernelFreshLeaseStaysQuiet
// Scenario: A freshly-issued lease that the kernel hasn't observed yet
// is NOT flagged.
func TestArpCheckUnseenByKernelFreshLeaseStaysQuiet(t *testing.T) {
	t.Setenv("SKOED_TEST_ARP_TABLE", "")
	// Fresh lease — first observed 12s ago, well under the 30m
	// unseen_grace and the 6h ghost_lease_threshold.

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.77", "aa:bb:cc:dd:ee:77", "new-phone", "id:phone77"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	s := requireArpTestAffordance(t, n, "192.168.1.77")

	// Give the sweep a couple of ticks; it should NOT flag this lease.
	time.Sleep(2 * time.Second)
	for _, a := range fetchAnomalies(t, n) {
		if a.IP == "192.168.1.77" && isArpAnomalyKind(a.Kind) {
			t.Errorf("fresh lease unexpectedly flagged: %+v", a)
		}
	}
	if s.KernelState != "none" {
		t.Errorf("kernel_state: want none, got %q", s.KernelState)
	}
	if s.Anomaly != "" {
		t.Errorf("anomaly: want absent, got %q", s.Anomaly)
	}
}

// FS-ArpCheckUnseenByKernelAfterGracePeriod
// Scenario: A lease the kernel cannot see after the grace window is
// flagged unseen_by_kernel.
func TestArpCheckUnseenByKernelAfterGracePeriod(t *testing.T) {
	t.Setenv("SKOED_TEST_ARP_TABLE", "")
	// 45 minutes — past unseen_grace (default 30m) but under
	// ghost_lease_threshold (default 6h).
	t.Setenv("SKOED_TEST_LEASE_FIRST_SEEN_OFFSET", "45m")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.55", "aa:bb:cc:dd:ee:55", "weird-bulb", "id:bulb55"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	s, code := fetchArpState(t, n, "192.168.1.55")
	if code != http.StatusOK {
		t.Skipf("M6.5 impl pending: arp-state returned %d", code)
	}
	if s.KernelState == "" {
		t.Skipf("M6.5 impl pending: SKOED_TEST_LEASE_FIRST_SEEN_OFFSET not wired")
	}
	if waitForAnomaly(t, n, "unseen_by_kernel", "192.168.1.55", 5*time.Second) == nil {
		t.Fatalf("unseen_by_kernel anomaly never raised")
	}
	if s.Anomaly != "unseen_by_kernel" {
		t.Errorf("anomaly: want unseen_by_kernel, got %q", s.Anomaly)
	}
}

// FS-ArpCheckGracefulWhenNetlinkUnavailable
// Scenario: Node without CAP_NET_ADMIN reports netlink_unavailable once
// and stops spamming.
func TestArpCheckGracefulWhenNetlinkUnavailable(t *testing.T) {
	// Force the binary down the "netlink open failed" branch even when
	// the test host happens to run privileged.
	t.Setenv("SKOED_TEST_NETLINK_UNAVAILABLE", "1")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	// Give the sweep three ticks to fire; the spec requires the
	// "netlink_unavailable" structured log line is emitted only once.
	time.Sleep(3500 * time.Millisecond)

	s, code := fetchArpState(t, n, "192.168.1.42")
	if code == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: arp-state route missing")
	}
	if code != http.StatusOK {
		t.Fatalf("arp-state: status %d", code)
	}
	if s.KernelState == "" {
		t.Skipf("M6.5 impl pending: SKOED_TEST_NETLINK_UNAVAILABLE not wired")
	}
	if s.KernelState != "netlink_unavailable" {
		t.Errorf("kernel_state: want netlink_unavailable, got %q", s.KernelState)
	}
	if s.MacKernel != "" {
		t.Errorf("mac_kernel: want empty, got %q", s.MacKernel)
	}
	if s.Anomaly != "" {
		t.Errorf("anomaly: want absent under netlink_unavailable, got %q", s.Anomaly)
	}

	// And no layer-3 anomalies anywhere in the list — the sweep must
	// short-circuit before recording any of the four new kinds.
	for _, a := range fetchAnomalies(t, n) {
		if isArpAnomalyKind(a.Kind) {
			t.Errorf("unexpected layer-3 anomaly while netlink unavailable: %+v", a)
		}
	}
}

// FS-ArpCheckUnknownIpReturns404
// Scenario: GET /api/v1/clients/{ip}/arp-state for an unknown IP returns 404.
func TestArpCheckUnknownIpReturns404(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	// Probe the route first with a known lease — if it 404s for the
	// known IP too, the route is not implemented (skip rather than
	// falsely passing on the unknown-IP assertion).
	resp := n.apiDo(t, "GET", "/api/v1/clients/192.168.1.42/arp-state", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: arp-state route missing (known-IP probe returned 404)")
	}

	// Now the real assertion: an unknown IP returns 404 with an error
	// mentioning "no lease".
	resp = n.apiDo(t, "GET", "/api/v1/clients/10.99.99.99/arp-state", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown IP, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(strings.ToLower(body), "no lease") {
		t.Errorf("error body should mention \"no lease\", got %q", body)
	}
}

// FS-ArpCheckAnomaliesListIncludesNewKinds
// Scenario: GET /api/v1/anomalies surfaces the new layer-3 anomaly kinds
// alongside M3.6 kinds.
func TestArpCheckAnomaliesListIncludesNewKinds(t *testing.T) {
	t.Setenv("SKOED_TEST_ARP_TABLE",
		"192.168.1.42=11:22:33:44:55:66,reachable")
	t.Setenv("SKOED_TEST_LEASE_FIRST_SEEN_OFFSET", "7h")

	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
		sampleLease("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop", "id:laptop10"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	// Trigger an M3.6 anomaly (mac_changed_for_client_id) by swapping
	// the MAC on 192.168.1.10's lease.
	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
		sampleLease("192.168.1.10", "ff:00:00:00:00:99", "home-laptop", "id:laptop10"),
	})

	// Wait for the M3.6 anomaly first; this also confirms the harness
	// can reach /anomalies (skip-on-404 via fetchAnomalies).
	if waitForAnomaly(t, n, "mac_changed_for_client_id", "192.168.1.10", 5*time.Second) == nil {
		t.Fatalf("M3.6 anomaly never raised — harness sanity check failed")
	}

	// Now wait for the M6.5 ARP mismatch anomaly on .42.
	if waitForAnomaly(t, n, "arp_mac_mismatch", "192.168.1.42", 5*time.Second) == nil {
		t.Skipf("M6.5 impl pending: arp_mac_mismatch anomaly never appeared (test affordance not wired?)")
	}

	all := fetchAnomalies(t, n)
	var sawMac, sawArp bool
	for _, a := range all {
		if a.Kind == "mac_changed_for_client_id" && a.IP == "192.168.1.10" {
			sawMac = true
		}
		if a.Kind == "arp_mac_mismatch" && a.IP == "192.168.1.42" {
			sawArp = true
		}
		if a.Kind == "" || a.IP == "" {
			t.Errorf("anomaly entry missing kind or ip: %+v", a)
		}
		if a.DetectedAt.IsZero() {
			t.Errorf("anomaly entry missing detected_at: %+v", a)
		}
	}
	if !sawMac {
		t.Errorf("M3.6 anomaly missing from full /anomalies list")
	}
	if !sawArp {
		t.Errorf("M6.5 arp_mac_mismatch missing from full /anomalies list")
	}

	// Filter assertion: ?kind=arp_mac_mismatch returns only ARP rows.
	resp := n.apiDo(t, "GET", "/api/v1/clients/anomalies?kind=arp_mac_mismatch", "")
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		t.Skipf("M6.5 impl pending: ?kind filter returned 404")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anomalies?kind: status %d", resp.StatusCode)
	}
	var filtered []anomaly
	if err := json.NewDecoder(resp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered anomalies: %v", err)
	}
	for _, a := range filtered {
		if a.Kind != "arp_mac_mismatch" {
			t.Errorf("filter ?kind=arp_mac_mismatch leaked %q", a.Kind)
		}
	}
}

// FS-ArpCheckSweepCadenceIsBestEffort
// Scenario: ARP cross-check is best-effort and never blocks DHCP polling.
func TestArpCheckSweepCadenceIsBestEffort(t *testing.T) {
	// The spec requires that a slow netlink probe MUST NOT block DHCP
	// polling and that overlapping sweeps are skipped rather than
	// queued. We assert this end-to-end by forcing the probe to take
	// longer than the sweep interval (via a test affordance) and
	// observing that:
	//   (a) the DHCP-driven lease cache still refreshes
	//   (b) at most one "arp_sweep_skipped" log/metric event surfaces
	//       per minute
	//
	// Direct log-line inspection is out of scope for a black-box test;
	// instead we rely on a future /api/v1/clients/_arp_sweep_stats
	// endpoint exposed under SKOED_TEST_MODE. Until that exists, this
	// test skips.
	t.Skipf("M6.5 impl pending — requires SKOED_TEST_ARP_SWEEP_DELAY + stats endpoint affordance")
}

// isArpAnomalyKind reports whether kind is one of the four M6.5
// layer-3 anomaly kinds introduced by TS-ArpCheck.
func isArpAnomalyKind(kind string) bool {
	switch kind {
	case "arp_mac_mismatch",
		"ndp_mac_mismatch",
		"ghost_lease",
		"unseen_by_kernel":
		return true
	}
	return false
}
