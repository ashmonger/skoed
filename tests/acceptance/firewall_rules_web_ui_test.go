// Acceptance tests for M6 — Web UI "Copy rules" buttons on Clients / Stats pages.
//
// TS-FwRuleUi states the browser is a thin renderer over the M6 sibling
// endpoints (TS-FwRuleGen + TS-DohResolverDb) and introduces NO new HTTP
// routes of its own. Acceptance therefore exercises the API-level
// contracts each SPA action invokes (per the spec's note: "Vue rendering
// is exercised via embedded-SPA presence checks per the M2.6 convention").
// Playwright-style browser tests are out of scope.
//
// FSIDs covered (one Go test per FSID):
//   FS-FwRuleUiClientsRowActionVisible         → TestFwRuleUiClientsRowActionVisible
//   FS-FwRuleUiClientsModalPlatformTabset      → TestFwRuleUiClientsModalPlatformTabset
//   FS-FwRuleUiClientsCopyToClipboard          → TestFwRuleUiClientsCopyToClipboard
//   FS-FwRuleUiStatsSubnetCallout              → TestFwRuleUiStatsSubnetCallout
//   FS-FwRuleUiStatsSubnetPreviewAndCopy       → TestFwRuleUiStatsSubnetPreviewAndCopy
//   FS-FwRuleUiProfileScope                    → TestFwRuleUiProfileScope
//   FS-FwRuleUiKeyboardNavigablePlatformTabs   → TestFwRuleUiKeyboardNavigablePlatformTabs
//   FS-FwRuleUiStaleSnapshotBanner             → TestFwRuleUiStaleSnapshotBanner
//   FS-FwRuleUiEmptyResolverDatabase           → TestFwRuleUiEmptyResolverDatabase
//   FS-FwRuleUiUnauthorizedRedirect            → TestFwRuleUiUnauthorizedRedirect
//
// Helper reuse: fwRuleGet, skipIfFwRuleAbsent, and seedKidsProfile are
// defined in firewall_rule_generator_test.go (same package). The UI
// tests below intentionally piggy-back on those helpers so the two
// suites share one source of truth for the generator endpoint shape.

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// fwRulesPath is the generator endpoint the SPA delegates to for every
// "Copy DoH-gap rules" surface (Clients row action, Profiles row action,
// Stats callout). The UI never invents its own routes.
const fwRulesPath = "/api/v1/firewall-rules"

// FS-FwRuleUiClientsRowActionVisible
// The per-row "Copy DoH-gap rules" action on Clients.vue delegates to
// GET /api/v1/firewall-rules with scope=subnet&subnet=<client-ip>/32.
// The acceptance contract for the UI is that this single-IP request
// returns 200 and a non-empty rule blob — without that the row action
// would have nothing to surface.
func TestFwRuleUiClientsRowActionVisible(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	status, body, _ := fwRuleGet(t, n,
		"platform=iptables&scope=subnet&subnet=10.42.10.50/32")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if strings.TrimSpace(body) == "" {
		t.Errorf("client-row action must return a non-empty rule blob")
	}
	// The blob the SPA shows in the modal must mention the scoped client IP.
	if !strings.Contains(body, "10.42.10.50") {
		t.Errorf("body should reference the scoped client IP 10.42.10.50; got %q", body)
	}
}

// FS-FwRuleUiClientsModalPlatformTabset
// Every tab in the platform tabset (iptables, nftables, mikrotik,
// opnsense, unifi) MUST be a valid platform argument to the generator.
// If any one of them errored, the SPA's tabset would have a dead tab.
func TestFwRuleUiClientsModalPlatformTabset(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	platforms := []string{"iptables", "nftables", "mikrotik", "opnsense", "unifi"}
	previews := make(map[string]string, len(platforms))
	for _, p := range platforms {
		status, body, _ := fwRuleGet(t, n,
			"platform="+p+"&scope=subnet&subnet=10.42.10.50/32")
		if skipIfFwRuleAbsent(t, status) {
			return
		}
		if status != http.StatusOK {
			t.Fatalf("platform=%s: status %d: %s", p, status, body)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("platform=%s: preview must be non-empty", p)
		}
		previews[p] = body
	}
	// Switching tabs MUST reload the preview for the new platform — i.e.
	// the bodies must not all be identical text.
	allSame := true
	first := previews[platforms[0]]
	for _, p := range platforms[1:] {
		if previews[p] != first {
			allSame = false
			break
		}
	}
	if allSame {
		t.Errorf("every platform returned identical text; the tabset would render the same preview on every tab")
	}
}

// FS-FwRuleUiClientsCopyToClipboard
// The "Copy" button copies the exact text currently rendered in the
// preview <pre>. The clipboard write is browser-side, so the acceptance
// invariant is: the API returns Content-Type text/plain and a stable
// body that the SPA can hand verbatim to
// navigator.clipboard.writeText.
func TestFwRuleUiClientsCopyToClipboard(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	status, body, ctype := fwRuleGet(t, n,
		"platform=nftables&scope=subnet&subnet=10.42.10.50/32")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if !strings.HasPrefix(ctype, "text/plain") {
		t.Errorf("Content-Type must start with text/plain so the SPA can copy it verbatim; got %q", ctype)
	}
	if strings.TrimSpace(body) == "" {
		t.Errorf("preview body must be non-empty for Copy to be enabled")
	}
}

// FS-FwRuleUiStatsSubnetCallout
// The Stats.vue "Closing the DoH gap" callout is a subnet-scoped
// generator widget. Its existence is meaningful only if the generator
// accepts arbitrary CIDRs the operator types into the picker and the
// platform tabset's alternate tabs are each independently reachable.
func TestFwRuleUiStatsSubnetCallout(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// The callout's default subnet (per the spec's mock-up) is 10.0.0.0/24.
	status, body, _ := fwRuleGet(t, n,
		"platform=iptables&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, "10.0.0.0/24") {
		t.Errorf("Stats callout preview must reference the chosen subnet; got %q", body)
	}
	// The callout's platform tabset reuses the same five tabs as the
	// Clients modal; sanity-check at least one alternate tab loads
	// against the same subnet so the callout's tab switch is meaningful.
	status2, body2, _ := fwRuleGet(t, n,
		"platform=mikrotik&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status2) {
		return
	}
	if status2 != http.StatusOK {
		t.Fatalf("mikrotik callout: status %d", status2)
	}
	if strings.TrimSpace(body2) == "" {
		t.Errorf("mikrotik callout preview must be non-empty")
	}
}

// FS-FwRuleUiStatsSubnetPreviewAndCopy
// When the Stats callout shows subnet 10.0.0.0/24 and the operator
// picks the "iptables" tab, the preview the SPA renders is exactly
// what the API returns for that scope+platform combination — and the
// Copy button must therefore be able to hand that text to the
// clipboard verbatim.
func TestFwRuleUiStatsSubnetPreviewAndCopy(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	status, body, _ := fwRuleGet(t, n,
		"platform=iptables&scope=subnet&subnet=10.0.0.0/24")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if !strings.Contains(body, "10.0.0.0/24") {
		t.Errorf("preview should reference the subnet; got %q", body)
	}
	// The iptables format documented in TS-FwRuleGen prefixes rules with
	// "-A FORWARD". Without that the SPA's "Copy" copies a blob that
	// won't apply on the operator's edge router.
	if !strings.Contains(body, "-A FORWARD") {
		t.Errorf("iptables preview must contain '-A FORWARD' rules; got %q", body)
	}
}

// FS-FwRuleUiProfileScope
// The modal opened from a profile context calls the generator with
// scope=profile&profile=<id>. The generated text MUST reference every
// IP currently bound to the profile so the operator sees them in the
// preview before pressing Copy.
func TestFwRuleUiProfileScope(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedKidsProfile(t, n)

	status, body, _ := fwRuleGet(t, n,
		"platform=mikrotik&scope=profile&profile=kids")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, ip := range []string{"10.42.10.50", "10.42.10.51"} {
		if !strings.Contains(body, ip) {
			t.Errorf("profile-scope preview must reference %s; got %q", ip, body)
		}
	}
}

// FS-FwRuleUiKeyboardNavigablePlatformTabs
// The keyboard model (ArrowRight, Home, etc.) is browser-side and
// cannot be exercised here. The acceptance invariant the API owes the
// UI is: every key press that moves focus to another tab must be
// backed by a generator call that returns a non-empty preview without
// extra round-trip arguments (the spec caches by (scope, platform,
// action) — so a vanilla GET per platform MUST succeed). This test
// walks the five platforms in order and confirms each one is
// independently fetchable, mirroring the sequence ArrowRight produces,
// then re-fetches iptables to model the Home-key return.
func TestFwRuleUiKeyboardNavigablePlatformTabs(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	order := []string{"iptables", "nftables", "mikrotik", "opnsense", "unifi"}
	for _, p := range order {
		status, body, _ := fwRuleGet(t, n,
			"platform="+p+"&scope=subnet&subnet=10.42.10.50/32")
		if skipIfFwRuleAbsent(t, status) {
			return
		}
		if status != http.StatusOK {
			t.Errorf("platform=%s: status %d (%s)", p, status, body)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("platform=%s: empty body breaks the tabset keyboard flow", p)
		}
	}
	// Pressing Home returns focus to the first tab — i.e. iptables MUST
	// also be reachable after the full forward walk (idempotent GET).
	status, body, _ := fwRuleGet(t, n,
		"platform=iptables&scope=subnet&subnet=10.42.10.50/32")
	if status != http.StatusOK {
		t.Errorf("Home-key tab (iptables): status %d (%s)", status, body)
	}
}

// FS-FwRuleUiStaleSnapshotBanner
// The stale-snapshot banner is sourced from the leading comment block
// of the generator response (TS-FwRuleUi). The acceptance invariant is
// that the generator emits a leading header block at all so the SPA
// has somewhere to read fetched_at from. A snapshot fresher than 7d
// must therefore still produce a parseable header (whether or not the
// stale marker is present).
func TestFwRuleUiStaleSnapshotBanner(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	status, body, _ := fwRuleGet(t, n,
		"platform=iptables&scope=subnet&subnet=10.42.10.50/32")
	if skipIfFwRuleAbsent(t, status) {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	// The spec asks the SPA to parse "the first ~10 lines of the body"
	// for `snapshot_fetched`. The body must therefore start with a
	// header block so the parser has something to anchor on.
	lines := strings.SplitN(body, "\n", 11)
	if len(lines) < 2 {
		t.Errorf("body has no leading header block (got %d lines)", len(lines))
	}
	headerEnd := len(lines)
	if headerEnd > 10 {
		headerEnd = 10
	}
	header := strings.ToLower(strings.Join(lines[:headerEnd], "\n"))
	if !strings.Contains(header, "snapshot") && !strings.Contains(header, "fetched") {
		t.Errorf("header block must carry snapshot/fetched metadata for the SPA's stale banner; got %q", header)
	}
}

// FS-FwRuleUiEmptyResolverDatabase
// When the resolver database has never been refreshed, the spec says
// the modal renders an empty-state and disables the Copy button. The
// API contract that drives this is: the generator MUST NOT 500 when
// the snapshot is empty — it must return a benign response (empty body
// or a 503 the SPA can map to the empty-state) instead of leaking an
// internal error.
func TestFwRuleUiEmptyResolverDatabase(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp := n.apiDo(t, "GET",
		fwRulesPath+"?platform=iptables&scope=subnet&subnet=10.42.10.50/32", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M6 impl pending")
	}
	// Brand-new test cluster: no scheduled refresh has run yet, so the
	// snapshot table may be empty. Whatever the backend chooses to do,
	// 200 (with empty/header-only body) or 503 are the only sane
	// outcomes — never 500.
	if resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("empty-resolver-db path must not 5xx (got %d)", resp.StatusCode)
	}
}

// FS-FwRuleUiUnauthorizedRedirect
// The generator endpoint is auth-gated (per TS-FwRuleUi posture and the
// sibling FS-FwRuleRequiresAuth). An unauthenticated GET MUST receive
// 401 so the SPA's global axios interceptor can bounce to /login. A
// successful body would mean the "Copy DoH-gap rules" action is
// reachable without a session.
func TestFwRuleUiUnauthorizedRedirect(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp := n.apiDoNoAuth(t, "GET",
		fwRulesPath+"?platform=iptables&scope=subnet&subnet=10.42.10.50/32")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M6 impl pending")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-auth GET %s: want 401, got %d", fwRulesPath, resp.StatusCode)
	}
}
