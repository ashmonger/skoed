// Acceptance tests for M32 Per-Domain Upstream Routing.
//
// FSIDs covered:
//   FS-UpstreamRouteCreate               → TestM32RouteCreate
//   FS-UpstreamRouteExactMatch           → TestM32RouteExactMatch
//   FS-UpstreamRouteCIDRMatch            → TestM32RouteCIDRMatch
//   FS-UpstreamRouteReplace              → TestM32RouteReplace
//   FS-UpstreamRouteClear                → TestM32RouteClear
//   FS-UpstreamRouteWildcardResolution   → TestM32RouteWildcardResolution
//   FS-UpstreamRouteExactResolution      → TestM32RouteExactResolution
//   FS-UpstreamRouteNoMatchFallsThrough  → TestM32RouteNoMatchFallsThrough
//   FS-UpstreamRouteTopDownPriority      → TestM32RouteTopDownPriority
//   FS-UpstreamRouteWildcardDepth        → TestM32RouteWildcardDepth
//   FS-UpstreamRouteClusterReplicated    → TestM32RouteClusterReplicated
//   FS-UpstreamRouteInvalidMatchRejected → TestM32RouteInvalidMatchRejected
//   FS-UpstreamRouteEmptyResolversRejected → TestM32RouteEmptyResolversRejected
//   FS-UpstreamDiscovery                 → TestM32UpstreamDiscovery

package acceptance

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

// upstreamRoute mirrors the JSON shape for the API and harness config.
type upstreamRoute struct {
	Match     string   `json:"match"     yaml:"match"`
	Resolvers []string `json:"resolvers" yaml:"resolvers"`
}

// fakeUpstreamReturnsAForDomain answers A queries for a specific domain with
// the given IP. Other queries get NXDOMAIN. Callers distinguish which resolver
// was used by checking the returned IP.
func fakeUpstreamReturnsAForDomain(domain, ip string) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA &&
			r.Question[0].Name == dns.Fqdn(domain) {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: mustParseIP4(ip),
			})
		} else {
			m.Rcode = dns.RcodeNameError
		}
		w.WriteMsg(m) //nolint:errcheck
	}
}

// fakeUpstreamTracked answers every A query with ip and increments a counter
// on each query received. Used to verify a resolver was (or was not) consulted.
func fakeUpstreamTracked(ip string, count *atomic.Int64) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		count.Add(1)
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: mustParseIP4(ip),
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	}
}

// patchUpstreamRoutes replaces the upstream_routes list via PATCH /api/v1/settings.
func patchUpstreamRoutes(t *testing.T, n *Node, routes []upstreamRoute) {
	t.Helper()
	body := map[string]any{
		"dns": map[string]any{
			"upstream_routes": routes,
		},
	}
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH upstream_routes: got %d", resp.StatusCode)
	}
}

// getUpstreamRoutes reads dns.upstream_routes from GET /api/v1/settings.
func getUpstreamRoutes(t *testing.T, n *Node) []upstreamRoute {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/settings", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET settings: %d", resp.StatusCode)
	}
	var body struct {
		DNS struct {
			UpstreamRoutes []upstreamRoute `json:"upstream_routes"`
		} `json:"dns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return body.DNS.UpstreamRoutes
}

// ─── Configuration CRUD ──────────────────────────────────────────────────────

// TestM32RouteCreate: FS-UpstreamRouteCreate
func TestM32RouteCreate(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{"10.1.0.1:53"}},
	})

	routes := getUpstreamRoutes(t, n)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Match != "*.corp.internal" {
		t.Errorf("match: got %q, want %q", routes[0].Match, "*.corp.internal")
	}
	if len(routes[0].Resolvers) != 1 || routes[0].Resolvers[0] != "10.1.0.1:53" {
		t.Errorf("resolvers: got %v", routes[0].Resolvers)
	}
}

// TestM32RouteExactMatch: FS-UpstreamRouteExactMatch
func TestM32RouteExactMatch(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "corp.internal", Resolvers: []string{"10.1.0.1:53"}},
	})

	routes := getUpstreamRoutes(t, n)
	if len(routes) != 1 || routes[0].Match != "corp.internal" {
		t.Fatalf("got routes: %+v", routes)
	}
}

// TestM32RouteCIDRMatch: FS-UpstreamRouteCIDRMatch
func TestM32RouteCIDRMatch(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "10.in-addr.arpa", Resolvers: []string{"10.1.0.1:53"}},
	})

	routes := getUpstreamRoutes(t, n)
	if len(routes) != 1 || routes[0].Match != "10.in-addr.arpa" {
		t.Fatalf("got routes: %+v", routes)
	}
}

// TestM32RouteReplace: FS-UpstreamRouteReplace
func TestM32RouteReplace(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.old.internal", Resolvers: []string{"10.0.0.1:53"}},
	})
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.a.internal", Resolvers: []string{"10.1.0.1:53"}},
		{Match: "*.b.internal", Resolvers: []string{"10.2.0.1:53"}},
	})

	routes := getUpstreamRoutes(t, n)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes after replace, got %d: %+v", len(routes), routes)
	}
}

// TestM32RouteClear: FS-UpstreamRouteClear
func TestM32RouteClear(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{"10.1.0.1:53"}},
	})
	patchUpstreamRoutes(t, n, []upstreamRoute{})

	routes := getUpstreamRoutes(t, n)
	if len(routes) != 0 {
		t.Fatalf("expected empty routes after clear, got %d", len(routes))
	}
}

// ─── DNS resolution ───────────────────────────────────────────────────────────

// TestM32RouteWildcardResolution: FS-UpstreamRouteWildcardResolution
// Queries matching *.corp.internal go to the corp resolver; the answer IP
// distinguishes which resolver was used.
func TestM32RouteWildcardResolution(t *testing.T) {
	corpUpstream := startFakeUpstream(t, fakeUpstreamReturnsAForDomain("host.corp.internal", "192.168.1.10"))
	globalUpstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))

	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{globalUpstream}})
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{corpUpstream}},
	})

	r := dnsQuery(t, n.DNSAddr, "host.corp.internal", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	if len(r.Answer) == 0 {
		t.Fatal("no answer")
	}
	if a, ok := r.Answer[0].(*dns.A); !ok || a.A.String() != "192.168.1.10" {
		t.Errorf("expected 192.168.1.10 from corp resolver, got %v", r.Answer[0])
	}
}

// TestM32RouteExactResolution: FS-UpstreamRouteExactResolution
// Exact match routes only the exact domain, not subdomains.
func TestM32RouteExactResolution(t *testing.T) {
	var corpHits, globalHits atomic.Int64
	corpUpstream := startFakeUpstream(t, fakeUpstreamTracked("10.1.0.2", &corpHits))
	globalUpstream := startFakeUpstream(t, fakeUpstreamTracked("9.9.9.9", &globalHits))

	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{globalUpstream}})
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "corp.internal", Resolvers: []string{corpUpstream}},
	})

	// Exact domain → corp resolver
	r := dnsQuery(t, n.DNSAddr, "corp.internal", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	if a, ok := r.Answer[0].(*dns.A); !ok || a.A.String() != "10.1.0.2" {
		t.Errorf("corp.internal: expected 10.1.0.2, got %v", r.Answer[0])
	}

	// Subdomain → global upstream (not the exact-match route)
	dnsQuery(t, n.DNSAddr, "host.corp.internal", dns.TypeA)
	if globalHits.Load() == 0 {
		t.Error("host.corp.internal should have gone to global upstream, not corp resolver")
	}
}

// TestM32RouteNoMatchFallsThrough: FS-UpstreamRouteNoMatchFallsThrough
func TestM32RouteNoMatchFallsThrough(t *testing.T) {
	var globalHits atomic.Int64
	corpUpstream := startFakeUpstream(t, fakeUpstreamReturnsA("192.168.1.1"))
	globalUpstream := startFakeUpstream(t, fakeUpstreamTracked("93.184.216.34", &globalHits))

	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{globalUpstream}})
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{corpUpstream}},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	if globalHits.Load() == 0 {
		t.Error("example.com should have been forwarded to the global upstream")
	}
	if a, ok := r.Answer[0].(*dns.A); !ok || a.A.String() != "93.184.216.34" {
		t.Errorf("expected global upstream answer, got %v", r.Answer[0])
	}
}

// TestM32RouteTopDownPriority: FS-UpstreamRouteTopDownPriority
// First matching route wins; second more-specific route is never consulted.
func TestM32RouteTopDownPriority(t *testing.T) {
	var firstHits, secondHits atomic.Int64
	first := startFakeUpstream(t, fakeUpstreamTracked("10.1.0.1", &firstHits))
	second := startFakeUpstream(t, fakeUpstreamTracked("10.2.0.1", &secondHits))
	global := startFakeUpstream(t, fakeUpstreamReturnsA("1.1.1.1"))

	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{global}})
	// *.internal is broader and listed first → wins for *.corp.internal too.
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.internal", Resolvers: []string{first}},
		{Match: "*.corp.internal", Resolvers: []string{second}},
	})

	dnsQuery(t, n.DNSAddr, "host.corp.internal", dns.TypeA)

	if firstHits.Load() == 0 {
		t.Error("first route should have been consulted")
	}
	if secondHits.Load() != 0 {
		t.Error("second route should NOT have been consulted (first route already matched)")
	}
}

// TestM32RouteWildcardDepth: FS-UpstreamRouteWildcardDepth
// *.corp.internal matches a.b.c.corp.internal (any depth).
func TestM32RouteWildcardDepth(t *testing.T) {
	var corpHits atomic.Int64
	corpUpstream := startFakeUpstream(t, fakeUpstreamTracked("192.168.1.10", &corpHits))
	global := startFakeUpstream(t, fakeUpstreamReturnsA("1.1.1.1"))

	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{global}})
	patchUpstreamRoutes(t, n, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{corpUpstream}},
	})

	dnsQuery(t, n.DNSAddr, "a.b.c.corp.internal", dns.TypeA)
	if corpHits.Load() == 0 {
		t.Error("deep subdomain a.b.c.corp.internal should have matched *.corp.internal route")
	}
}

// ─── Cluster replication ──────────────────────────────────────────────────────

// TestM32RouteClusterReplicated: FS-UpstreamRouteClusterReplicated
func TestM32RouteClusterReplicated(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.Leader(t)

	patchUpstreamRoutes(t, leader.Node, []upstreamRoute{
		{Match: "*.corp.internal", Resolvers: []string{"10.1.0.1:53"}},
	})

	c.WaitConverged(t)

	for _, cn := range c.Followers(t) {
		routes := getUpstreamRoutes(t, cn.Node)
		if len(routes) != 1 || routes[0].Match != "*.corp.internal" {
			t.Errorf("follower %s: routes not replicated, got %+v", cn.NodeID, routes)
		}
	}
}

// ─── Validation ──────────────────────────────────────────────────────────────

// TestM32RouteInvalidMatchRejected: FS-UpstreamRouteInvalidMatchRejected
func TestM32RouteInvalidMatchRejected(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	body := map[string]any{
		"dns": map[string]any{
			"upstream_routes": []map[string]any{
				{"match": "*", "resolvers": []string{"10.1.0.1:53"}},
			},
		},
	}
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bare wildcard match: expected 400, got %d", resp.StatusCode)
	}
}

// TestM32RouteEmptyResolversRejected: FS-UpstreamRouteEmptyResolversRejected
func TestM32RouteEmptyResolversRejected(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	body := map[string]any{
		"dns": map[string]any{
			"upstream_routes": []map[string]any{
				{"match": "*.corp.internal", "resolvers": []string{}},
			},
		},
	}
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty resolvers: expected 400, got %d", resp.StatusCode)
	}
}

// ─── Upstream discovery ──────────────────────────────────────────────────────

// TestM32UpstreamDiscovery: FS-UpstreamDiscovery
func TestM32UpstreamDiscovery(t *testing.T) {
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{"127.0.0.1:1"}})

	resp := n.apiDo(t, "POST", "/api/v1/settings/discover-upstreams", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover-upstreams: expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		SuggestedResolvers []string `json:"suggested_resolvers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	// The test environment always has at least one nameserver in /etc/resolv.conf.
	if len(body.SuggestedResolvers) == 0 {
		t.Error("discover-upstreams returned no suggested_resolvers")
	}
	// Verify settings were NOT modified automatically.
	routes := getUpstreamRoutes(t, n)
	if len(routes) != 0 {
		t.Error("discover-upstreams should not modify upstream_routes automatically")
	}
}
