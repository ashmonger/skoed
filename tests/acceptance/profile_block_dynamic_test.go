// Acceptance tests for M6.5 block-dynamic-clients profile rule (TS-BlockDyn).
//
// FSIDs covered (one Go test per FSID):
//   FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients     → TestBlockDynPureBlockDynamicProfileMatchesAllDynamicClients
//   FS-BlockDynMixedCriteriaIsOrNotAnd                             → TestBlockDynMixedCriteriaIsOrNotAnd
//   FS-BlockDynEmptyMatchSetIsFine                                 → TestBlockDynEmptyMatchSetIsFine
//   FS-BlockDynRejectedOnDefaultProfile                            → TestBlockDynRejectedOnDefaultProfile
//   FS-BlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic     → TestBlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic
//   FS-BlockDynUnknownClientIsNotDynamic                           → TestBlockDynUnknownClientIsNotDynamic
//   FS-BlockDynPriorityHigherTierStillWins                         → TestBlockDynPriorityHigherTierStillWins
//   FS-BlockDynProfileApiCrud                                      → TestBlockDynProfileApiCrud
//   FS-BlockDynClientLookupSurfacesMatchedProfile                  → TestBlockDynClientLookupSurfacesMatchedProfile
//   FS-BlockDynUnknownOriginTreatedAsNotDynamic                    → TestBlockDynUnknownOriginTreatedAsNotDynamic
//
// Strategy (per TS-BlockDyn):
//   - The rule only triggers when the resolved Lease.Origin equals the
//     literal string "dhcp_dynamic". The cheapest way to drive that
//     observably from a black-box test is the http_json connector, which
//     honours an explicit `origin` wire field (TS-LeaseOrigin /
//     FS-LeaseOriginHttpJsonHonoursWireField).
//   - Each test serves a tailored JSON payload from an in-process
//     httptest.Server and has skoed poll it via startClusterWithDhcp.
//   - Client-IP spoofing reuses the M3 EDNS0-65500 affordance
//     (dnsQueryAsClient).
//   - Skip-on-404 / skip-on-501 covers the implementation gap: until the
//     M6.5 binary lands, the new POST/PATCH validation, the new profile
//     field, and the lease-origin plumbing return 404/501 and the tests
//     auto-skip with a clear "M6.5 impl pending" reason.

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// blockDynLeaseWire is the JSON shape the http_json connector consumes,
// extended with the M6.5 `origin` field. Mirrors the wire schema in
// FS-LeaseOriginHttpJsonHonoursWireField — kept local to this file so the
// test owns its fixtures without touching the shared Lease struct.
type blockDynLeaseWire struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Hostname  string `json:"hostname,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Origin    string `json:"origin,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// baselineWire mirrors the spec's Background lease cache: two static, two
// dynamic, all expiring in the far future so the connector keeps them.
func baselineWire() []blockDynLeaseWire {
	expFuture := "2287-11-09T11:46:39Z"
	return []blockDynLeaseWire{
		{IP: "192.168.1.10", MAC: "aa:bb:cc:dd:ee:10", Hostname: "home-laptop", ClientID: "id:laptop10", Origin: "dhcp_static", ExpiresAt: expFuture},
		{IP: "192.168.1.42", MAC: "aa:bb:cc:dd:ee:42", Hostname: "kid-tablet", ClientID: "id:tablet42", Origin: "dhcp_static", ExpiresAt: expFuture},
		{IP: "192.168.1.77", MAC: "aa:bb:cc:dd:ee:77", Hostname: "guest-phone", ClientID: "id:guest77", Origin: "dhcp_dynamic", ExpiresAt: expFuture},
		{IP: "192.168.1.88", MAC: "aa:bb:cc:dd:ee:88", Hostname: "iot-thing", ClientID: "id:iot88", Origin: "dhcp_dynamic", ExpiresAt: expFuture},
	}
}

// originStub starts an in-process http_json connector source serving the
// supplied lease list. The pointer-to-slice form lets a test mutate the
// payload between polls (e.g. add a brand-new dynamic lease) without
// restarting the server.
func originStub(t *testing.T, leases *[]blockDynLeaseWire) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(*leases)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startBlockDynCluster spins a single-node cluster wired to an http_json
// connector serving the supplied lease set with a tight refresh interval
// so a payload mutation lands within the test's deadline.
func startBlockDynCluster(t *testing.T, leases *[]blockDynLeaseWire) (*Cluster, *Node) {
	t.Helper()
	srv := originStub(t, leases)
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:           "http_json",
		URL:            srv.URL,
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	// Wait until the first poll lands so subsequent profile rules can
	// observe origin tags. The connector tests use the same idiom but
	// inline; here we centralise it so every block-dynamic test gets a
	// populated cache before issuing DNS queries.
	deadline := time.Now().Add(dhcpTestTimeout)
	for time.Now().Before(deadline) {
		got := fetchLeaseSnapshot(t, n)
		if len(got) >= len(*leases) {
			return c, n
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("connector did not populate %d leases within %s", len(*leases), dhcpTestTimeout)
	return nil, nil
}

// postProfileSkipIfMissing POSTs a profile body and self-skips when the
// route, the body field, or the validation rule isn't yet implemented.
// Returns the response status so individual tests can additionally assert
// on it (e.g. 400 for the default-profile rejection).
func postProfileSkipIfMissing(t *testing.T, n *Node, body string) (int, string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: block_dynamic_clients returns 501")
	}
	return resp.StatusCode, readBody(t, resp)
}

// patchProfileSkipIfMissing PATCHes /api/v1/profiles/{id}. Same skip
// semantics as the POST helper.
func patchProfileSkipIfMissing(t *testing.T, n *Node, id, body string) (int, string) {
	t.Helper()
	resp := n.apiDo(t, "PATCH", "/api/v1/profiles/"+id, body)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: PATCH /api/v1/profiles/%s returns 404", id)
	}
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: PATCH block_dynamic_clients returns 501")
	}
	return resp.StatusCode, readBody(t, resp)
}

// getProfileBlockDynamicClients reads the boolean off GET /profiles/{id}.
// Skips on 404 so the test compiles before the route exists; returns
// (value, exists) once it does.
func getProfileBlockDynamicClients(t *testing.T, n *Node, id string) bool {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/profiles/"+id, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: GET /api/v1/profiles/%s returns 404", id)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /profiles/%s: status %d", id, resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode profile %s: %v", id, err)
	}
	v, _ := got["block_dynamic_clients"].(bool)
	return v
}

// seedBlockDynBlocklists creates "ads" and "social" inline blocklists so
// every test starts from the spec's Background.
func seedBlockDynBlocklists(t *testing.T, n *Node) {
	t.Helper()
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")
	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")
}

// seedDefaultUsesAds installs (or overwrites) the default profile to use
// only the "ads" blocklist, matching the Background's "default profile
// uses only ads" precondition.
func seedDefaultUsesAds(t *testing.T, n *Node) {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"id":         "default",
		"name":       "Default",
		"blocklists": []string{"ads"},
		"allowlist":  []string{},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	resp.Body.Close()
	// 201 = created, 409/200 = already exists / replaced. Anything else
	// is genuinely fatal.
	if resp.StatusCode != 201 && resp.StatusCode != 200 && resp.StatusCode != 409 {
		t.Fatalf("seed default profile: status %d", resp.StatusCode)
	}
}

// FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients
func TestBlockDynPureBlockDynamicProfileMatchesAllDynamicClients(t *testing.T) {
	leases := baselineWire()
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"ads", "social"},
		"block_dynamic_clients": true,
	})
	status, respBody := postProfileSkipIfMissing(t, n, body)
	if status != http.StatusCreated {
		t.Fatalf("create untrusted profile: status %d: %s", status, respBody)
	}

	// 192.168.1.77 is a dynamic-lease client → "untrusted" matches via
	// block_dynamic_clients → social blocks facebook.com.
	rBlocked := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.77")
	if rBlocked.Rcode != dns.RcodeNameError {
		t.Errorf("dynamic client should match untrusted profile; want NXDOMAIN, got %s",
			dns.RcodeToString[rBlocked.Rcode])
	}

	// 192.168.1.10 is a static-lease client → default profile (ads only)
	// applies → facebook.com forwarded (NOERROR is the only stable
	// signal; the test cluster has no real upstream, so we just assert
	// the rule does NOT trigger NXDOMAIN).
	rAllowed := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.10")
	if rAllowed.Rcode == dns.RcodeNameError {
		t.Errorf("static client should fall through to default; got NXDOMAIN")
	}
}

// FS-BlockDynMixedCriteriaIsOrNotAnd
func TestBlockDynMixedCriteriaIsOrNotAnd(t *testing.T) {
	leases := baselineWire()
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
		"client_ips":            []string{"192.168.1.10"},
	})
	status, respBody := postProfileSkipIfMissing(t, n, body)
	if status != http.StatusCreated {
		t.Fatalf("create mixed profile: status %d: %s", status, respBody)
	}

	// 192.168.1.10 (static, in client_ips) → matched by IP arm → NXDOMAIN.
	r1 := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.10")
	if r1.Rcode != dns.RcodeNameError {
		t.Errorf("static client matched by client_ips: want NXDOMAIN, got %s",
			dns.RcodeToString[r1.Rcode])
	}

	// 192.168.1.77 (dynamic, NOT in client_ips) → matched by
	// block_dynamic_clients arm → NXDOMAIN. Proves the rule is OR not AND.
	r2 := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.77")
	if r2.Rcode != dns.RcodeNameError {
		t.Errorf("dynamic client matched by block_dynamic_clients: want NXDOMAIN, got %s",
			dns.RcodeToString[r2.Rcode])
	}

	// 192.168.1.42 (static, NOT in client_ips) → default profile, social
	// not in default → forwarded (no NXDOMAIN).
	r3 := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.42")
	if r3.Rcode == dns.RcodeNameError {
		t.Errorf("static client not in client_ips should NOT match untrusted; got NXDOMAIN")
	}
}

// FS-BlockDynEmptyMatchSetIsFine
func TestBlockDynEmptyMatchSetIsFine(t *testing.T) {
	// Start with only static leases — the rule has zero matches at
	// boot, which the spec says is fine.
	leases := []blockDynLeaseWire{
		{IP: "192.168.1.10", MAC: "aa:bb:cc:dd:ee:10", Hostname: "home-laptop", ClientID: "id:laptop10", Origin: "dhcp_static", ExpiresAt: "2287-11-09T11:46:39Z"},
		{IP: "192.168.1.42", MAC: "aa:bb:cc:dd:ee:42", Hostname: "kid-tablet", ClientID: "id:tablet42", Origin: "dhcp_static", ExpiresAt: "2287-11-09T11:46:39Z"},
	}
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	status, respBody := postProfileSkipIfMissing(t, n, body)
	if status != http.StatusCreated {
		t.Fatalf("create empty-match profile: status %d: %s", status, respBody)
	}

	// GET /profiles/untrusted should round-trip block_dynamic_clients=true.
	if !getProfileBlockDynamicClients(t, n, "untrusted") {
		t.Errorf("GET /profiles/untrusted: block_dynamic_clients should be true")
	}

	// No current client matches the rule — the static client 192.168.1.10
	// must NOT be pinned to "untrusted".
	rStatic := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.10")
	if rStatic.Rcode == dns.RcodeNameError {
		t.Errorf("static client must not match untrusted on empty match set; got NXDOMAIN")
	}

	// Now a brand-new device shows up with a dynamic lease — the rule
	// must catch it on the next poll.
	leases = append(leases, blockDynLeaseWire{
		IP: "192.168.1.99", MAC: "aa:bb:cc:dd:ee:99",
		Hostname: "new-device", ClientID: "id:new99",
		Origin: "dhcp_dynamic", ExpiresAt: "2287-11-09T11:46:39Z",
	})
	// Wait for refresh (RefreshSeconds=1; allow up to dhcpTestTimeout).
	deadline := time.Now().Add(dhcpTestTimeout)
	for time.Now().Before(deadline) {
		got := fetchLeaseSnapshot(t, n)
		if findLeaseByIP(got, "192.168.1.99").IP != "" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	rDynamic := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.99")
	if rDynamic.Rcode != dns.RcodeNameError {
		t.Errorf("brand-new dynamic client should match untrusted; want NXDOMAIN, got %s",
			dns.RcodeToString[rDynamic.Rcode])
	}
}

// FS-BlockDynRejectedOnDefaultProfile
func TestBlockDynRejectedOnDefaultProfile(t *testing.T) {
	leases := baselineWire()
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	// PATCH /profiles/default with block_dynamic_clients=true must 400.
	status, body := patchProfileSkipIfMissing(t, n, "default", `{"block_dynamic_clients":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PATCH default with block_dynamic_clients=true: want 400, got %d: %s",
			status, body)
	}
	low := strings.ToLower(body)
	if !strings.Contains(low, "default profile") {
		t.Errorf("400 body should mention \"default profile\"; got %q", body)
	}
	if !strings.Contains(low, "block_dynamic_clients") {
		t.Errorf("400 body should mention \"block_dynamic_clients\"; got %q", body)
	}

	// GET /profiles/default must still report block_dynamic_clients=false.
	if got := getProfileBlockDynamicClients(t, n, "default"); got {
		t.Errorf("GET /profiles/default after rejected PATCH: block_dynamic_clients=true, want false")
	}
}

// FS-BlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic
func TestBlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic(t *testing.T) {
	leases := baselineWire()
	leases = append(leases,
		blockDynLeaseWire{IP: "192.168.2.1", MAC: "aa:bb:cc:dd:ee:01", Hostname: "ra-host", Origin: "router_advertised", ExpiresAt: "2287-11-09T11:46:39Z"},
		blockDynLeaseWire{IP: "192.168.2.5", MAC: "aa:bb:cc:dd:ee:05", Hostname: "manual-host", Origin: "manual_admin", ExpiresAt: "2287-11-09T11:46:39Z"},
	)
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	status, respBody := postProfileSkipIfMissing(t, n, body)
	if status != http.StatusCreated {
		t.Fatalf("create untrusted profile: status %d: %s", status, respBody)
	}

	// router_advertised is NOT dynamic → default applies → facebook.com
	// must NOT be NXDOMAIN.
	rRA := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.2.1")
	if rRA.Rcode == dns.RcodeNameError {
		t.Errorf("router_advertised should not match block_dynamic_clients; got NXDOMAIN")
	}

	// manual_admin is NOT dynamic.
	rMA := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.2.5")
	if rMA.Rcode == dns.RcodeNameError {
		t.Errorf("manual_admin should not match block_dynamic_clients; got NXDOMAIN")
	}

	// dhcp_dynamic DOES match.
	rDyn := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.77")
	if rDyn.Rcode != dns.RcodeNameError {
		t.Errorf("dhcp_dynamic should match block_dynamic_clients; want NXDOMAIN, got %s",
			dns.RcodeToString[rDyn.Rcode])
	}
}

// FS-BlockDynUnknownClientIsNotDynamic
func TestBlockDynUnknownClientIsNotDynamic(t *testing.T) {
	leases := baselineWire()
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	status, respBody := postProfileSkipIfMissing(t, n, body)
	if status != http.StatusCreated {
		t.Fatalf("create untrusted profile: status %d: %s", status, respBody)
	}

	// 10.99.99.99 has no lease at all — must fall through to default,
	// not match untrusted.
	r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "10.99.99.99")
	if r.Rcode == dns.RcodeNameError {
		t.Errorf("unknown client (no lease) should NOT match block_dynamic_clients; got NXDOMAIN")
	}
}

// FS-BlockDynPriorityHigherTierStillWins
func TestBlockDynPriorityHigherTierStillWins(t *testing.T) {
	// 192.168.1.200 has Client-ID id:tablet42 AND origin=dhcp_dynamic —
	// Client-ID match (tier 1) must win, untrusted (tier 4) must NOT
	// apply. Trusted-tablet only blocks ads; untrusted blocks social.
	leases := []blockDynLeaseWire{
		{IP: "192.168.1.200", MAC: "aa:bb:cc:dd:ee:42", Hostname: "kid-tablet", ClientID: "id:tablet42", Origin: "dhcp_dynamic", ExpiresAt: "2287-11-09T11:46:39Z"},
	}
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	// trusted-tablet: pinned by Client-ID, blocks "ads", does NOT set
	// block_dynamic_clients.
	bodyA := mustJSON(t, map[string]any{
		"id":         "trusted-tablet",
		"name":       "Trusted tablet",
		"blocklists": []string{"ads"},
		"client_ids": []string{"id:tablet42"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", bodyA)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create trusted-tablet: status %d", resp.StatusCode)
	}

	// untrusted: pure block_dynamic_clients, blocks "social".
	bodyB := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	if status, _ := postProfileSkipIfMissing(t, n, bodyB); status != http.StatusCreated {
		t.Fatalf("create untrusted: status %d", status)
	}

	// Client-ID wins: doubleclick.net (in "ads" via trusted-tablet)
	// must be NXDOMAIN.
	rAds := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.1.200")
	if rAds.Rcode != dns.RcodeNameError {
		t.Errorf("Client-ID match must apply trusted-tablet → block ads; want NXDOMAIN, got %s",
			dns.RcodeToString[rAds.Rcode])
	}

	// untrusted MUST NOT apply (tier 4 never consulted when tier 1
	// matched). facebook.com is in "social" — only untrusted blocks
	// it — must NOT be NXDOMAIN.
	rSocial := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.200")
	if rSocial.Rcode == dns.RcodeNameError {
		t.Errorf("Client-ID priority lost: untrusted should not apply; got NXDOMAIN for facebook.com")
	}
}

// FS-BlockDynProfileApiCrud
func TestBlockDynProfileApiCrud(t *testing.T) {
	leases := baselineWire()
	c, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	// POST creates with block_dynamic_clients=true.
	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	if status, _ := postProfileSkipIfMissing(t, n, body); status != http.StatusCreated {
		t.Fatalf("POST untrusted: status %d", status)
	}

	// GET round-trip.
	if !getProfileBlockDynamicClients(t, n, "untrusted") {
		t.Errorf("after POST: block_dynamic_clients should be true")
	}

	// Pre-PATCH: dynamic client matches untrusted → NXDOMAIN.
	rBefore := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.77")
	if rBefore.Rcode != dns.RcodeNameError {
		t.Errorf("pre-PATCH: dynamic client should match untrusted; want NXDOMAIN, got %s",
			dns.RcodeToString[rBefore.Rcode])
	}

	// PATCH block_dynamic_clients=false. Replication is local on a
	// single-node cluster but the spec lets the test wait up to 5s
	// regardless.
	if status, b := patchProfileSkipIfMissing(t, n, "untrusted", `{"block_dynamic_clients":false}`); status != http.StatusOK {
		t.Fatalf("PATCH untrusted: status %d: %s", status, b)
	}
	// Allow replication / engine rebuild — poll the engine via DNS
	// rather than the API to verify the rule is actually disabled.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.77")
		if r.Rcode != dns.RcodeNameError {
			return // success: rule no longer applies
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("after PATCH block_dynamic_clients=false within 5s: dynamic client still pinned to untrusted")
	_ = c // keep variable used for future multi-node assertions
}

// FS-BlockDynClientLookupSurfacesMatchedProfile
func TestBlockDynClientLookupSurfacesMatchedProfile(t *testing.T) {
	leases := baselineWire()
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	if status, _ := postProfileSkipIfMissing(t, n, body); status != http.StatusCreated {
		t.Fatalf("create untrusted: status %d", status)
	}

	// GET /api/v1/clients/192.168.1.77 — dynamic, must list "untrusted"
	// in profile_ids and report origin = "dhcp_dynamic".
	got77 := fetchClientWithOrigin(t, n, "192.168.1.77")
	if !containsString(got77.ProfileIDs, "untrusted") {
		t.Errorf("dynamic client profile_ids: want \"untrusted\" in %v", got77.ProfileIDs)
	}
	if got77.Origin != "dhcp_dynamic" {
		t.Errorf("dynamic client origin: want \"dhcp_dynamic\", got %q", got77.Origin)
	}

	// GET /api/v1/clients/192.168.1.10 — static, must NOT list
	// "untrusted" and must report origin = "dhcp_static".
	got10 := fetchClientWithOrigin(t, n, "192.168.1.10")
	if containsString(got10.ProfileIDs, "untrusted") {
		t.Errorf("static client profile_ids must NOT include untrusted; got %v", got10.ProfileIDs)
	}
	if got10.Origin != "dhcp_static" {
		t.Errorf("static client origin: want \"dhcp_static\", got %q", got10.Origin)
	}
}

// FS-BlockDynUnknownOriginTreatedAsNotDynamic
func TestBlockDynUnknownOriginTreatedAsNotDynamic(t *testing.T) {
	// One lease with origin="" — connector could not determine it.
	leases := []blockDynLeaseWire{
		{IP: "192.168.3.3", MAC: "aa:bb:cc:dd:ee:33", Hostname: "unknown-origin-host", ClientID: "id:unknown33", Origin: "", ExpiresAt: "2287-11-09T11:46:39Z"},
	}
	_, n := startBlockDynCluster(t, &leases)
	seedBlockDynBlocklists(t, n)
	seedDefaultUsesAds(t, n)

	body := mustJSON(t, map[string]any{
		"id":                    "untrusted",
		"name":                  "Untrusted",
		"blocklists":            []string{"social"},
		"block_dynamic_clients": true,
	})
	if status, _ := postProfileSkipIfMissing(t, n, body); status != http.StatusCreated {
		t.Fatalf("create untrusted: status %d", status)
	}

	// Conservative default: origin="" must NOT trigger the rule.
	r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.3.3")
	if r.Rcode == dns.RcodeNameError {
		t.Errorf("unknown origin should NOT match block_dynamic_clients; got NXDOMAIN")
	}
}

// ─── helpers exposed by this file ──────────────────────────────────────────

// clientWithOrigin extends the M3.6 enrichedClient shape with the M6.5
// origin / profile_ids fields. Kept local because the shared
// enrichedClient struct in dhcp_client_identity_test.go intentionally
// does not yet know about M6.5 fields.
type clientWithOrigin struct {
	IP         string   `json:"ip"`
	MAC        string   `json:"mac"`
	Hostname   string   `json:"hostname"`
	ClientID   string   `json:"client_id"`
	Source     string   `json:"source"`
	Origin     string   `json:"origin"`
	ProfileIDs []string `json:"profile_ids"`
}

// fetchClientWithOrigin reads GET /api/v1/clients/{ip} and decodes the
// M6.5-extended shape. Skips when the endpoint is missing.
func fetchClientWithOrigin(t *testing.T, n *Node, ip string) clientWithOrigin {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+ip, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s returns 404", ip)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /clients/%s: status %d", ip, resp.StatusCode)
	}
	var out clientWithOrigin
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode client %s: %v", ip, err)
	}
	if out.Origin == "" && out.IP == ip {
		// origin field absent on the wire → M6.5 plumbing pending.
		// Skip so the test compiles cleanly until the impl lands.
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s omits \"origin\" field (got body %+v)",
			ip, out)
	}
	return out
}

// containsString is a tiny []string membership check kept inline so
// the test file has zero new dependencies.
func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// keep fmt imported even if a future edit drops the only formatted use
// above (matches the harness's pattern in cluster_harness_test.go).
var _ = fmt.Sprintf
