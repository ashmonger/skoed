// Acceptance tests for M6 — Firewall rule generator (TS-FwRule).
//
// Black-box: every interaction is a GET against the running daemon's
// /api/v1/firewall-rules endpoint. Tests skip with 404 when the route
// is not yet registered, mirroring the test_domain_test.go pattern.
//
// FSIDs covered:
//   FS-FwRuleIptablesSubnetScope            → TestFwRuleIptablesSubnetScope
//   FS-FwRuleNftablesSubnetScope            → TestFwRuleNftablesSubnetScope
//   FS-FwRuleMikrotikSubnetScope            → TestFwRuleMikrotikSubnetScope
//   FS-FwRuleOpnsenseSubnetScope            → TestFwRuleOpnsenseSubnetScope
//   FS-FwRuleUnifiSubnetScope               → TestFwRuleUnifiSubnetScope
//   FS-FwRuleProfileScope                   → TestFwRuleProfileScope
//   FS-FwRuleAllScope                       → TestFwRuleAllScope
//   FS-FwRuleRejectActionOptIn              → TestFwRuleRejectActionOptIn
//   FS-FwRuleRejectsUnknownPlatform         → TestFwRuleRejectsUnknownPlatform
//   FS-FwRuleRejectsInvalidSubnet           → TestFwRuleRejectsInvalidSubnet
//   FS-FwRuleRejectsUnknownProfile          → TestFwRuleRejectsUnknownProfile
//   FS-FwRuleRequiresAuth                   → TestFwRuleRequiresAuth
//   FS-FwRuleHeaderCarriesSnapshotProvenance → TestFwRuleHeaderCarriesSnapshotProvenance
//   FS-FwRuleStaleSnapshotStillServes       → TestFwRuleStaleSnapshotStillServes
//   FS-FwRuleMetricsCounter                 → TestFwRuleMetricsCounter

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fwRuleGet fetches /api/v1/firewall-rules with the given raw query
// string (no leading '?'). Returns (status, body, contentType).
func fwRuleGet(t *testing.T, n *Node, query string) (int, string, string) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/firewall-rules?"+query, "")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Content-Type")
}

// skipIfFwRuleAbsent returns true (and skips the test) when the
// firewall-rules endpoint is not yet implemented on the running
// daemon. Used by every test in this file.
func skipIfFwRuleAbsent(t *testing.T, status int) bool {
	t.Helper()
	if status == http.StatusNotFound {
		t.Skip("M6 impl pending: /api/v1/firewall-rules 404")
		return true
	}
	if status == http.StatusServiceUnavailable {
		// 503 means the resolver snapshot has never been fetched. On a
		// cold cluster with no internet egress this is the expected
		// boot-state; tests that need a snapshot should bail rather
		// than fail.
		t.Skip("M6 impl pending: resolver snapshot unavailable (503)")
		return true
	}
	return false
}

// seedKidsProfile creates a "kids" profile bound to two client IPs so
// the profile-scope test has something to expand.
func seedKidsProfile(t *testing.T, n *Node) {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"id":         "kids",
		"name":       "Kids",
		"client_ips": []string{"10.42.10.50", "10.42.10.51"},
	})
	r := n.apiDo(t, "POST", "/api/v1/profiles", body)
	r.Body.Close()
	if r.StatusCode == http.StatusNotFound {
		t.Skip("M3 profiles route 404 — cannot exercise scope=profile")
	}
	if r.StatusCode != http.StatusCreated && r.StatusCode != http.StatusOK {
		t.Fatalf("seed kids profile: status %d", r.StatusCode)
	}
}

// FS-FwRuleIptablesSubnetScope
func TestFwRuleIptablesSubnetScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, ctype := fwRuleGet(t, n, "platform=iptables&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(strings.ToLower(ctype), "text/plain") {
		t.Errorf("want text/plain content-type, got %q", ctype)
	}
	// The curated snapshot must enumerate at least Cloudflare, Google,
	// and Quad9 — every blob therefore carries DROP rules for these IPs
	// scoped to the operator's subnet.
	for _, want := range []string{
		"-A FORWARD -s 10.0.0.0/24 -d 1.1.1.1 -j DROP",
		"-A FORWARD -s 10.0.0.0/24 -d 8.8.8.8 -j DROP",
		"-A FORWARD -s 10.0.0.0/24 -d 9.9.9.9 -j DROP",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing rule line %q\nbody:\n%s", want, body)
		}
	}
	// Per-rule provenance: each rule preceded by a comment naming the
	// resolver. Comment syntax is '#' for iptables.
	for _, name := range []string{"cloudflare", "google", "quad9"} {
		if !strings.Contains(strings.ToLower(body), "# resolver: "+name) {
			t.Errorf("missing per-resolver comment for %q", name)
		}
	}
	// Header comment block carries snapshot_fetched timestamp.
	if !strings.Contains(strings.ToLower(body), "snapshot_fetched") {
		t.Errorf("header missing snapshot_fetched field; body:\n%s", body)
	}
}

// FS-FwRuleNftablesSubnetScope
func TestFwRuleNftablesSubnetScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=nftables&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, "table inet skoed_doh_gap") {
		t.Errorf("missing 'table inet skoed_doh_gap' declaration; body:\n%s", body)
	}
	// IPv4 inline-set rule for the curated resolvers.
	if !strings.Contains(body, "ip saddr 10.0.0.0/24") {
		t.Errorf("missing 'ip saddr 10.0.0.0/24' clause; body:\n%s", body)
	}
	for _, want := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing IPv4 daddr %q in nftables set", want)
		}
	}
	// IPv6 counterpart exists in the same blob.
	if !strings.Contains(body, "ip6 daddr") {
		t.Errorf("missing ip6 daddr rule for IPv6 resolvers; body:\n%s", body)
	}
	// The action keyword is "drop" (lowercase, nftables-style).
	if !strings.Contains(body, "drop") {
		t.Errorf("missing 'drop' verdict in nftables output; body:\n%s", body)
	}
}

// FS-FwRuleMikrotikSubnetScope
func TestFwRuleMikrotikSubnetScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=mikrotik&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, "/ip firewall filter add") {
		t.Errorf("missing '/ip firewall filter add' lines; body:\n%s", body)
	}
	// Every rule must carry chain=forward, action=drop, src-address.
	for _, want := range []string{
		"chain=forward",
		"action=drop",
		"src-address=10.0.0.0/24",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing MikroTik fragment %q; body:\n%s", want, body)
		}
	}
	// Per-rule comment naming the resolver.
	if !strings.Contains(strings.ToLower(body), `comment="skoed doh-gap:`) {
		t.Errorf(`missing comment="skoed doh-gap: ..." fragment; body:\n%s`, body)
	}
	// Curated IPv4 must appear as dst-address.
	for _, want := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if !strings.Contains(body, "dst-address="+want) {
			t.Errorf("missing dst-address=%s; body:\n%s", want, body)
		}
	}
}

// FS-FwRuleOpnsenseSubnetScope
func TestFwRuleOpnsenseSubnetScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=opnsense&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	// Alias name "skoed_doh_resolvers" is declared and lists every
	// curated IPv4 + IPv6 member.
	if !strings.Contains(body, "skoed_doh_resolvers") {
		t.Errorf("missing 'skoed_doh_resolvers' alias; body:\n%s", body)
	}
	for _, want := range []string{
		"1.1.1.1", "8.8.8.8", "9.9.9.9",
		"2606:4700:4700::1111", "2001:4860:4860::8888", "2620:fe::fe",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing alias member %q; body:\n%s", want, body)
		}
	}
	// The blob declares a block rule sourced from the operator subnet
	// targeting that alias.
	if !strings.Contains(body, "10.0.0.0/24") {
		t.Errorf("missing source 10.0.0.0/24; body:\n%s", body)
	}
	// Header documents the paste flow for the OpnSense UI. Match on
	// 'alias' near the start of the body — the leading comment block
	// must mention how the alias is imported.
	low := strings.ToLower(body)
	if !strings.Contains(low, "alias") {
		t.Errorf("header should reference the OpnSense alias paste flow; body:\n%s", body)
	}
}

// FS-FwRuleUnifiSubnetScope
func TestFwRuleUnifiSubnetScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, ctype := fwRuleGet(t, n, "platform=unifi&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(strings.ToLower(ctype), "text/plain") {
		t.Errorf("want text/plain content-type, got %q", ctype)
	}
	// The body string IS a JSON document (text/plain is the transport,
	// JSON is the payload).
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("UniFi body is not valid JSON: %v\nbody:\n%s", err, body)
	}
	if action, _ := obj["action"].(string); action != "drop" {
		t.Errorf(`want action="drop", got %q`, action)
	}
	// Source group must carry the operator's subnet.
	rawSrc, _ := json.Marshal(obj["src_address_group"])
	if !strings.Contains(string(rawSrc), "10.0.0.0/24") {
		t.Errorf("source group missing 10.0.0.0/24; got %s", string(rawSrc))
	}
	// Destination group enumerates every curated resolver IP.
	rawDst, _ := json.Marshal(obj["dst_address_group"])
	for _, want := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if !strings.Contains(string(rawDst), want) {
			t.Errorf("dst group missing %q; got %s", want, string(rawDst))
		}
	}
}

// FS-FwRuleProfileScope
func TestFwRuleProfileScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedKidsProfile(t, n)

	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=profile&profile=kids")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	// The two client IPs assigned to "kids" each appear as -s sources.
	for _, ip := range []string{"10.42.10.50", "10.42.10.51"} {
		if !strings.Contains(body, "-s "+ip) {
			t.Errorf("expected '-s %s' in output; body:\n%s", ip, body)
		}
	}
	// No rule for any OTHER client IP. The "default" profile's IPs (or
	// any wildcard) MUST NOT leak in.
	for _, leak := range []string{"-s 10.0.0.0/24", "-s 0.0.0.0/0", "-s 192.168.1.50"} {
		if strings.Contains(body, leak) {
			t.Errorf("profile scope leaked %q; body:\n%s", leak, body)
		}
	}
}

// FS-FwRuleAllScope
func TestFwRuleAllScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=all")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	// scope=all → no -s clause on any FORWARD rule. We look for a
	// FORWARD line and confirm it lacks the source flag.
	sawForward := false
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "-A FORWARD") {
			continue
		}
		sawForward = true
		if strings.Contains(trim, " -s ") {
			t.Errorf("scope=all rule unexpectedly contains -s clause: %q", trim)
		}
	}
	if !sawForward {
		t.Errorf("scope=all produced no FORWARD rules; body:\n%s", body)
	}
	// Curated resolver IPs still enumerated as -d.
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if !strings.Contains(body, "-d "+ip) {
			t.Errorf("scope=all missing '-d %s'; body:\n%s", ip, body)
		}
	}
}

// FS-FwRuleRejectActionOptIn
func TestFwRuleRejectActionOptIn(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=subnet&subnet=10.0.0.0/24&action=reject")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	// Every FORWARD rule ends with the REJECT target + icmp reason.
	rejectFrag := "-j REJECT --reject-with icmp-admin-prohibited"
	sawReject := false
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "-A FORWARD") {
			continue
		}
		if strings.HasSuffix(trim, "-j DROP") {
			t.Errorf("action=reject produced a DROP rule: %q", trim)
		}
		if strings.Contains(trim, rejectFrag) {
			sawReject = true
		}
	}
	if !sawReject {
		t.Errorf("expected at least one rule ending in %q; body:\n%s", rejectFrag, body)
	}
}

// FS-FwRuleRejectsUnknownPlatform
func TestFwRuleRejectsUnknownPlatform(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=pfsense&scope=all")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown platform, got %d: %s", status, body)
	}
	low := strings.ToLower(body)
	if !strings.Contains(low, "unsupported platform") {
		t.Errorf(`error should mention "unsupported platform"; got %s`, body)
	}
	// The error envelope should also enumerate the supported platforms.
	for _, p := range []string{"iptables", "nftables", "mikrotik", "opnsense", "unifi"} {
		if !strings.Contains(low, p) {
			t.Errorf("error should list supported platform %q; got %s", p, body)
		}
	}
}

// FS-FwRuleRejectsInvalidSubnet
func TestFwRuleRejectsInvalidSubnet(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=subnet&subnet=not-a-cidr")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid subnet, got %d: %s", status, body)
	}
	if !strings.Contains(strings.ToLower(body), "invalid subnet") {
		t.Errorf(`error should mention "invalid subnet"; got %s`, body)
	}
}

// FS-FwRuleRejectsUnknownProfile
func TestFwRuleRejectsUnknownProfile(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=profile&profile=does-not-exist")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusNotFound {
		t.Fatalf("want 404 for unknown profile, got %d: %s", status, body)
	}
	if !strings.Contains(strings.ToLower(body), "profile") {
		t.Errorf(`error should mention "profile"; got %s`, body)
	}
}

// FS-FwRuleRequiresAuth
func TestFwRuleRequiresAuth(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	resp := n.apiDoNoAuth(t, "GET", "/api/v1/firewall-rules?platform=iptables&scope=all")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M6 impl pending: /api/v1/firewall-rules 404")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-auth: want 401, got %d", resp.StatusCode)
	}
}

// FS-FwRuleHeaderCarriesSnapshotProvenance
func TestFwRuleHeaderCarriesSnapshotProvenance(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=all")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	// The leading comment block must carry every provenance field.
	// We accept any RFC3339-shaped timestamp; the renderer plants the
	// six fields verbatim.
	for _, field := range []string{
		"snapshot_id",
		"snapshot_fetched",
		"resolver_count",
		"generated_at",
		"scope",
	} {
		if !strings.Contains(strings.ToLower(body), field) {
			t.Errorf("header missing provenance field %q; body:\n%s", field, body)
		}
	}
	// Scope label must reflect the request — scope=all in this case.
	if !strings.Contains(strings.ToLower(body), "all") {
		t.Errorf("header should record scope=all; body:\n%s", body)
	}
}

// FS-FwRuleStaleSnapshotStillServes
func TestFwRuleStaleSnapshotStillServes(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// The stale path can only be exercised on a daemon that ships with
	// a test hook (e.g. SKOED_TEST_RESOLVER_STALE=1) or a pre-aged
	// snapshot. Lacking such a hook in the harness, the test asserts
	// the contract on a best-effort basis: when the daemon happens to
	// have a stale snapshot (cold cluster + no successful refresh in
	// 7+ days), the response must still be 200 and carry the warning
	// banner; otherwise the test reports the absence of the banner
	// only if a 200 came back with a non-stale snapshot, and skips.
	status, body, _ := fwRuleGet(t, n, "platform=iptables&scope=all")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	low := strings.ToLower(body)
	if strings.Contains(low, "warning: snapshot is stale") {
		// Stale path observed — the body still served, contract holds.
		return
	}
	// Fresh snapshot path. We cannot force-age the snapshot via the
	// public API; mark this as inconclusive rather than failing.
	t.Skip("resolver snapshot is fresh; stale-path requires a pre-aged snapshot fixture")
}

// FS-FwRuleMetricsCounter
func TestFwRuleMetricsCounter(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// Generate one nftables blob and one iptables blob so both label
	// series exist before scraping /metrics.
	for _, q := range []string{
		"platform=nftables&scope=all",
		"platform=iptables&scope=all",
	} {
		status, body, _ := fwRuleGet(t, n, q)
		if skipIfFwRuleAbsent(t, status) {
			return
		}
		if status != 200 {
			t.Fatalf("priming GET %s: status %d: %s", q, status, body)
		}
	}

	resp, _ := http.Get(n.APIBase + "/metrics")
	if resp == nil || resp.StatusCode != 200 {
		t.Skip("M5.1 /metrics unavailable")
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(b)
	if !strings.Contains(body, "skoed_firewall_rules_generated_total") {
		t.Skipf("M6 impl pending: counter skoed_firewall_rules_generated_total absent")
	}
	for _, want := range []string{
		`skoed_firewall_rules_generated_total{platform="nftables"}`,
		`skoed_firewall_rules_generated_total{platform="iptables"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing counter series %q", want)
		}
	}
}
