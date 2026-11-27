// Acceptance tests for DNS overload protection.
//
// Functional spec: specs/functional/dns-overload-protection.feature
// Technical spec:  specs/technical/dns-overload-protection.md
// (TS-DnsOverloadProtection)
//
// FSIDs covered (one Go test per FSID):
//
//	FS-DnsOverloadCapacityConfigurable    → TestDnsOverloadCapacityConfigurable
//	FS-DnsOverloadAnswersUpToCapacity     → TestDnsOverloadAnswersUpToCapacity
//	FS-DnsOverloadRefusesExcess           → TestDnsOverloadRefusesExcess
//	FS-DnsOverloadFilteringKept           → TestDnsOverloadFilteringKept
//	FS-DnsOverloadMemoryBounded           → TestDnsOverloadMemoryBounded
//	FS-DnsOverloadRecoversWhenLoadDrops   → TestDnsOverloadRecoversWhenLoadDrops
//	FS-DnsOverloadNoRefusalBelowCapacity  → TestDnsOverloadNoRefusalBelowCapacity
//	FS-DnsOverloadVisibleToAdministrator  → TestDnsOverloadVisibleToAdministrator
//
// Durations (TS § 10, decision D-3): every scenario runs at C = 200
// queries per second on a 3-node cluster, and the 15-minute runs and the
// 5-minute phases are cut to 1 minute, including the window in which the
// memory of FS-DnsOverloadRecoversWhenLoadDrops must come back (UoR,
// 2026-11-27; the bench run of TS § 9.2 checks the full 5 minutes). The
// other criteria are those of the functional spec.
//
// Background, as the harness sets it up (startOverloadCluster):
//   - a 3-node cluster from the cluster harness, all processes on the test
//     host; the 2 cores / 2 GB per node and the separate load generator
//     host of the Background are not reproduced in CI (TS § 9.2 runs them
//     on the bench);
//   - DNS cache and query log at their defaults (10,000 entries);
//   - an in-process fake upstream that answers every A query at once with
//     overloadUpstreamIP, set through PATCH /api/v1/settings;
//   - a blocklist "ads", block policy NXDOMAIN, holding
//     "tracker.example.com" and 99 other names;
//   - forwarding mode, so the trusted subnets do not apply;
//   - the load generator of dnsload_harness_test.go, over UDP at an even
//     rate, with the 70 / 20 / 10 mix of overloadNames.mix.
//
// The capacity is set through the real API (PATCH /api/v1/settings,
// dns.capacity_qps, TS § 7). "Given the DNS capacity of each node is C"
// is checked with t.Errorf, not t.Fatalf, so that a build that ignores
// the setting still runs the load and the test reports what the node did
// with it.
//
// These tests are not parallel: each one puts a node under load, and two
// at once on one runner would disturb each other's timings.

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const (
	// overloadCapacity is C in CI (TS § 10, decision D-3).
	overloadCapacity = 200
	// overloadRunDuration replaces the 15-minute runs and the 5-minute
	// phases in CI (decision D-3).
	overloadRunDuration = time.Minute
	// overloadUpstreamIP is the address the fake upstream answers with.
	overloadUpstreamIP = "198.51.100.7"
	// overloadBlockedName is the blocked name the scenarios name.
	overloadBlockedName = "tracker.example.com"
	// capacityPropagation is the configuration propagation bound of
	// cluster-config-sync.feature (FS-DnsOverloadCapacityConfigurable).
	capacityPropagation = 5 * time.Second
	// megabyte is the MB of the functional spec. The decimal unit is
	// the stricter reading of "200 MB" and "50 MB".
	megabyte = 1_000_000
)

// overloadNames holds the names of the query mix of the Background.
type overloadNames struct {
	cached  []string // queried once before a run, so they are in the cache
	blocked []string // the domains of the "ads" list
	runID   string   // makes the cache-miss names unique to the test
}

func newOverloadNames() overloadNames {
	n := overloadNames{runID: strconv.FormatInt(time.Now().UnixNano(), 36)}
	for i := range 100 {
		n.cached = append(n.cached, fmt.Sprintf("c%03d.cached.overload.test", i))
	}
	n.blocked = append(n.blocked, overloadBlockedName)
	for i := 1; i < 100; i++ {
		n.blocked = append(n.blocked, fmt.Sprintf("b%02d.ads.overload.test", i))
	}
	return n
}

// mix is the query mix of the Background: of every 10 queries, 7 are for
// cached names, 2 for names never queried before and 1 for a blocked name.
func (n overloadNames) mix(seq int) loadQuery {
	k, r := seq/10, seq%10
	switch {
	case r < 7:
		return loadQuery{Name: n.cached[(k*7+r)%len(n.cached)], Kind: queryCached}
	case r < 9:
		return loadQuery{Name: fmt.Sprintf("m%d.%s.miss.overload.test", seq, n.runID), Kind: queryMiss}
	default:
		return loadQuery{Name: n.blocked[k%len(n.blocked)], Kind: queryBlocked}
	}
}

// overloadCluster is the cluster of the Background.
type overloadCluster struct {
	*Cluster
	names overloadNames
}

// startOverloadCluster starts the cluster of the Background and returns
// once every node forwards to the fake upstream and blocks the "ads" list.
func startOverloadCluster(t *testing.T) *overloadCluster {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA(overloadUpstreamIP))
	c := startCluster(t, 3)
	oc := &overloadCluster{Cluster: c, names: newOverloadNames()}
	leader := c.Leader(t)

	resp := leader.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"mode":               "forwarding",
			"upstream_resolvers": []string{upstream},
		},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = leader.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":           "ads",
		"name":         "ads",
		"enabled":      true,
		"block_policy": "nxdomain",
		"domains":      oc.names.blocked,
		"source":       map[string]any{"type": "inline", "format": "domainlist"},
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create blocklist ads: status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	c.WaitConverged(t)

	probe := fmt.Sprintf("probe.%s.overload.test", oc.names.runID)
	for _, n := range c.nodes {
		waitFor(t, 30*time.Second, func() bool {
			return rcodeOf(n.DNSAddr, overloadBlockedName) == dns.RcodeNameError &&
				rcodeOf(n.DNSAddr, probe) == dns.RcodeSuccess
		}, "node %s does not block %s and forward to the fake upstream", n.NodeID, overloadBlockedName)
	}
	return oc
}

// rcodeOf returns the RCODE of an A query for name, or -1 on no response.
func rcodeOf(server, name string) int {
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	r, _, err := c.Exchange(m, server)
	if err != nil || r == nil {
		return -1
	}
	return r.Rcode
}

// follower returns a follower: "one node" of the scenarios.
func (oc *overloadCluster) follower(t *testing.T) *ClusterNode {
	t.Helper()
	return oc.Followers(t)[0]
}

// warmCache queries every cached name of the mix once on n, so that the
// 70 % of the mix are names in n's DNS cache.
func (oc *overloadCluster) warmCache(t *testing.T, n *ClusterNode) {
	t.Helper()
	for _, name := range oc.names.cached {
		if rc := rcodeOf(n.DNSAddr, name); rc != dns.RcodeSuccess {
			t.Fatalf("warming the cache of %s: %s got RCODE %d, want NOERROR", n.NodeID, name, rc)
		}
	}
}

// capacitySettings is the part of GET /api/v1/settings → dns that holds
// the DNS capacity (TS § 7). A nil field is absent from the response.
type capacitySettings struct {
	CapacityQPS          *int `json:"capacity_qps"`
	CapacityQPSEffective *int `json:"capacity_qps_effective"`
	CapacityQPSDefault   *int `json:"capacity_qps_default"`
}

func (s capacitySettings) String() string {
	f := func(p *int) string {
		if p == nil {
			return "absent"
		}
		return strconv.Itoa(*p)
	}
	return fmt.Sprintf("capacity_qps %s, capacity_qps_effective %s, capacity_qps_default %s",
		f(s.CapacityQPS), f(s.CapacityQPSEffective), f(s.CapacityQPSDefault))
}

// readCapacitySettings reads the DNS capacity n shows in its DNS settings.
func readCapacitySettings(t *testing.T, n *ClusterNode) capacitySettings {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/settings", "")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
	var body struct {
		DNS capacitySettings `json:"dns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode GET /api/v1/settings from %s: %v", n.NodeID, err)
	}
	return body.DNS
}

// setCapacity sets the DNS capacity to qps through the API and checks that
// every node shows it within capacityPropagation. A failure is reported
// with t.Errorf so that the test goes on with the load.
func (oc *overloadCluster) setCapacity(t *testing.T, qps int) {
	t.Helper()
	via := oc.Leader(t)
	// The 5 s are counted from the request, so they include the PATCH.
	deadline := time.Now().Add(capacityPropagation)
	resp := via.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{"capacity_qps": qps},
	}))
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("PATCH /api/v1/settings dns.capacity_qps=%d: status %d: %s", qps, resp.StatusCode, body)
		return
	}
	for _, n := range oc.nodes {
		for {
			s := readCapacitySettings(t, n)
			if s.CapacityQPS != nil && *s.CapacityQPS == qps &&
				s.CapacityQPSEffective != nil && *s.CapacityQPSEffective == qps {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("%s after the PATCH request, node %s shows %s; want capacity_qps and capacity_qps_effective %d",
					capacityPropagation, n.NodeID, s, qps)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// @fsid:FS-DnsOverloadCapacityConfigurable
//
// A new cluster shows the default DNS capacity on every node
// (capacity_qps 0, capacity_qps_effective equal to capacity_qps_default,
// TS § 7). The capacity is set to 2,000 and every node shows it within
// 5 s. Then 3,000 queries per second go to a follower for 1 minute: from
// the third second, the node answers 1,800–2,040 queries per second on
// average, and at most 122,000 over the minute.
func TestDnsOverloadCapacityConfigurable(t *testing.T) {
	const capacity, rate = 2000, 3000
	oc := startOverloadCluster(t)

	for _, n := range oc.nodes {
		s := readCapacitySettings(t, n)
		if s.CapacityQPS == nil || *s.CapacityQPS != 0 ||
			s.CapacityQPSEffective == nil || s.CapacityQPSDefault == nil ||
			*s.CapacityQPSDefault <= 0 || *s.CapacityQPSEffective != *s.CapacityQPSDefault {
			t.Errorf("new cluster, node %s shows %s; want capacity_qps 0 and capacity_qps_effective equal to a positive capacity_qps_default",
				n.NodeID, s)
		}
	}

	oc.setCapacity(t, capacity)

	node := oc.follower(t)
	oc.warmCache(t, node)
	run := runDNSLoadNow(t, node.DNSAddr, []loadPhase{{Rate: rate, Duration: time.Minute}}, oc.names.mix)
	run.logPerSecond(t)
	run.checkRate(t, 0, time.Minute, rate)

	rest := run.stats(2*time.Second, time.Minute)
	avg := float64(rest.Answered) / 58
	t.Logf("seconds 3 to 60: %s; %.0f answered per second", rest, avg)
	if avg < 0.9*capacity || avg > 1.02*capacity {
		t.Errorf("from the third second, %s answered %.0f queries per second on average; want 1,800 to 2,040", node.NodeID, avg)
	}
	whole := run.all()
	t.Logf("whole minute: %s", whole)
	if limit := 60*capacity + capacity; whole.Answered > limit {
		t.Errorf("over the minute, %s answered %d queries; want at most %d", node.NodeID, whole.Answered, limit)
	}
}

// @fsid:FS-DnsOverloadAnswersUpToCapacity
//
// 2 × C queries per second go to a follower for 1 minute (15 in the
// spec, decision D-3). Excluding the first second, the node answers at
// least 90 % of C queries per second on average over the minute, and
// the 99th percentile latency of the answers is below 50 ms.
func TestDnsOverloadAnswersUpToCapacity(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node)

	run := runDNSLoadNow(t, node.DNSAddr, []loadPhase{{Rate: 2 * overloadCapacity, Duration: overloadRunDuration}}, oc.names.mix)
	run.logPerSecond(t)
	run.checkRate(t, 0, overloadRunDuration, 2*overloadCapacity)

	for m := time.Duration(0); m < overloadRunDuration; m += time.Minute {
		from := max(m, time.Second)
		s := run.stats(from, m+time.Minute)
		perSecond := float64(s.Answered) / (m + time.Minute - from).Seconds()
		t.Logf("minute %d, from %s: %s; %.1f answered per second", m/time.Minute+1, from, s, perSecond)
		if perSecond < 0.9*overloadCapacity {
			t.Errorf("minute %d: %s answered %.1f queries per second; want at least %.0f (90 %% of C)",
				m/time.Minute+1, node.NodeID, perSecond, 0.9*overloadCapacity)
		}
	}
	all := run.all()
	t.Logf("whole run: %s", all)
	if all.P99Answer >= 50*time.Millisecond {
		t.Errorf("99th percentile latency of the answers: %s; want below 50 ms", all.P99Answer)
	}
}

// @fsid:FS-DnsOverloadRefusesExcess
//
// 2 × C queries per second go to a follower for 1 minute (15 in the
// spec, decision D-3). At least 98 % of the queries sent receive an
// answer or a REFUSED response, every other response is REFUSED, and the
// 99.9th percentile response time is below 1 s. The test also checks
// the scenario's title, that queries above the capacity do receive
// REFUSED: the three lines above alone hold for a node that answers
// every query and never refuses.
func TestDnsOverloadRefusesExcess(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node)

	run := runDNSLoadNow(t, node.DNSAddr, []loadPhase{{Rate: 2 * overloadCapacity, Duration: overloadRunDuration}}, oc.names.mix)
	run.logPerSecond(t)
	run.checkRate(t, 0, overloadRunDuration, 2*overloadCapacity)

	all := run.all()
	t.Logf("whole run: %s", all)
	if share := float64(all.Answered+all.Refused) / float64(all.Sent); share < 0.98 {
		t.Errorf("%.2f %% of the queries sent received an answer or REFUSED; want at least 98 %%", 100*share)
	}
	if all.Other != 0 {
		t.Errorf("%d responses are neither an answer nor REFUSED: %v", all.Other, all.OtherRcodes)
	}
	if all.P999Response >= time.Second {
		t.Errorf("99.9th percentile response time: %s; want below 1 s", all.P999Response)
	}
	if all.Refused == 0 {
		t.Errorf("no query received REFUSED at 2 × C (%d queries per second against a capacity of %d)",
			2*overloadCapacity, overloadCapacity)
	}
}

// @fsid:FS-DnsOverloadFilteringKept
//
// While 2 × C queries per second go to a follower, a second client sends
// 1,000 A queries for "tracker.example.com" over 10 s. At least one
// receives NXDOMAIN, every response is NXDOMAIN or REFUSED, and no
// response holds an address.
func TestDnsOverloadFilteringKept(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node)

	// The background load starts first and goes on after the 10 s of
	// tracker queries, so they all arrive while the node is overloaded.
	const lead = 2 * time.Second
	var background *loadRun
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		background = runDNSLoadNow(t, node.DNSAddr,
			[]loadPhase{{Rate: 2 * overloadCapacity, Duration: lead + 12*time.Second}}, oc.names.mix)
	}()
	time.Sleep(lead)
	tracker := runDNSLoadNow(t, node.DNSAddr, []loadPhase{{Rate: 100, Duration: 10 * time.Second}},
		func(int) loadQuery { return loadQuery{Name: overloadBlockedName, Kind: queryBlocked} })
	wg.Wait()
	t.Logf("background: %s", background.all())
	t.Logf("%s queries: %s", overloadBlockedName, tracker.all())
	tracker.checkRate(t, 0, 10*time.Second, 100)

	nxdomain, refused, lost := 0, 0, 0
	for _, q := range tracker.Records {
		if q.Outcome == outcomeLost {
			lost++
			continue
		}
		switch q.Rcode {
		case dns.RcodeNameError:
			nxdomain++
		case dns.RcodeRefused:
			refused++
		default:
			t.Errorf("query for %s sent at %s received %s; want NXDOMAIN or REFUSED",
				overloadBlockedName, q.Sent.Round(time.Millisecond), dns.RcodeToString[q.Rcode])
		}
		if len(q.Addrs) > 0 {
			t.Errorf("query for %s sent at %s received addresses %v", overloadBlockedName, q.Sent.Round(time.Millisecond), q.Addrs)
		}
	}
	t.Logf("%d queries for %s: %d NXDOMAIN, %d REFUSED, %d lost", len(tracker.Records), overloadBlockedName, nxdomain, refused, lost)
	if len(tracker.Records) != 1000 {
		t.Errorf("sent %d queries for %s; want 1,000", len(tracker.Records), overloadBlockedName)
	}
	if nxdomain == 0 {
		t.Errorf("no query for %s received NXDOMAIN", overloadBlockedName)
	}
	if refused == 0 {
		t.Errorf("no query for %s received REFUSED, so the node was not refusing during the 10 s; the run does not test the scenario",
			overloadBlockedName)
	}
}

// baselineRSS returns the median of 5 samples, 1 s apart, of the memory
// of n with no DNS load.
func baselineRSS(t *testing.T, n *ClusterNode) int64 {
	t.Helper()
	var samples []int64
	for i := range 5 {
		if i > 0 {
			time.Sleep(time.Second)
		}
		rss, err := processRSS(n.cmd.Process.Pid)
		if err != nil {
			t.Fatalf("memory of %s: %v", n.NodeID, err)
		}
		samples = append(samples, rss)
	}
	slices.Sort(samples)
	return samples[len(samples)/2]
}

// clusterWatch samples, every second until stopped, the memory of one
// node and the Raft term and leader every node reports.
type clusterWatch struct {
	stop     chan struct{}
	done     chan struct{}
	maxRSS   int64
	rssErr   error
	terms    map[int]bool
	leaders  map[string]bool
	statuses int
}

func watchCluster(c *Cluster, n *ClusterNode) *clusterWatch {
	w := &clusterWatch{stop: make(chan struct{}), done: make(chan struct{}), terms: map[int]bool{}, leaders: map[string]bool{}}
	pid := n.cmd.Process.Pid
	go func() {
		defer close(w.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			if rss, err := processRSS(pid); err != nil {
				if w.rssErr == nil {
					w.rssErr = err
				}
			} else {
				w.maxRSS = max(w.maxRSS, rss)
			}
			for _, cn := range c.nodes {
				if s, ok := statusWithin(cn, 3*time.Second); ok {
					w.statuses++
					w.terms[s.RaftTerm] = true
					if s.LeaderID != "" {
						w.leaders[s.LeaderID] = true
					}
				}
			}
			select {
			case <-w.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

// statusWithin is Cluster.tryStatus with a client timeout, so a node that
// does not answer under load does not stop the sampling.
func statusWithin(n *ClusterNode, timeout time.Duration) (ClusterStatus, bool) {
	req, err := http.NewRequest(http.MethodGet, n.Node.APIBase+"/api/v1/cluster/status", nil)
	if err != nil {
		return ClusterStatus{}, false
	}
	req.Header.Set("Authorization", "Bearer "+n.Node.sessionToken)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return ClusterStatus{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ClusterStatus{}, false
	}
	var s ClusterStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ClusterStatus{}, false
	}
	return s, true
}

func (w *clusterWatch) Stop() {
	close(w.stop)
	<-w.done
}

// @fsid:FS-DnsOverloadMemoryBounded
//
// The memory of the leader (VmRSS) is recorded with its lists loaded and
// no DNS load. 2 × C queries per second then go to the leader for
// 1 minute (15 in the spec, decision D-3). The highest memory sampled
// during the run is at most 200 MB over that level, the leader process
// is still running, and the Raft term and leader every node reports do
// not change: no election takes place.
func TestDnsOverloadMemoryBounded(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	leader := oc.Leader(t)
	oc.warmCache(t, leader)

	before := oc.Status(t, leader)
	base := baselineRSS(t, leader)
	t.Logf("leader %s, term %d, memory with no DNS load: %.1f MB", leader.NodeID, before.RaftTerm, float64(base)/megabyte)

	watch := watchCluster(oc.Cluster, leader)
	run := runDNSLoadNow(t, leader.DNSAddr, []loadPhase{{Rate: 2 * overloadCapacity, Duration: overloadRunDuration}}, oc.names.mix)
	watch.Stop()
	run.logPerSecond(t)
	run.checkRate(t, 0, overloadRunDuration, 2*overloadCapacity)
	t.Logf("whole run: %s", run.all())

	if leader.Node.hasExited() {
		t.Fatalf("leader %s exited during the run%s", leader.NodeID, exitReport(leader.Node))
	}
	if watch.rssErr != nil {
		t.Errorf("memory of %s: %v", leader.NodeID, watch.rssErr)
	}
	growth := watch.maxRSS - base
	t.Logf("highest memory during the run: %.1f MB, %.1f MB over the level with no load",
		float64(watch.maxRSS)/megabyte, float64(growth)/megabyte)
	if growth > 200*megabyte {
		t.Errorf("the memory of leader %s grew by %.1f MB; want at most 200 MB", leader.NodeID, float64(growth)/megabyte)
	}

	after := oc.Status(t, leader)
	if after.LeaderID != leader.NodeID || after.RaftTerm != before.RaftTerm {
		t.Errorf("after the run, leader %q term %d; want leader %q term %d", after.LeaderID, after.RaftTerm, leader.NodeID, before.RaftTerm)
	}
	if len(watch.terms) > 1 || len(watch.leaders) > 1 || !watch.leaders[leader.NodeID] {
		t.Errorf("during the run, the nodes reported terms %v and leaders %v; want only term %d and leader %s",
			watch.terms, watch.leaders, before.RaftTerm, leader.NodeID)
	}
	if watch.statuses == 0 {
		t.Errorf("no node answered GET /api/v1/cluster/status during the run")
	}
}

// @fsid:FS-DnsOverloadRecoversWhenLoadDrops
//
// The memory of a follower is recorded with its lists loaded and no DNS
// load. 2 × C queries per second go to it for 1 minute, then the load
// drops to 25 % of C for 1 minute (5 minutes each in the spec, decision
// D-3). From 5 s after the drop to the end of the run, no query receives
// REFUSED and less than 0.1 % receive no response. At the end of the
// 25 % phase, 1 minute after the drop, the memory is back within 50 MB
// of its level before the load.
//
// The memory window is 1 minute too, not the 5 minutes of the spec: the
// UoR applied D-3 literally on 2026-11-27, and the bench run of TS § 9.2
// checks the full window. On a build where the Go scavenger has not yet
// returned the freed memory 1 minute after the drop (TS § 5), this check
// can fail although the 5-minute criterion holds.
func TestDnsOverloadRecoversWhenLoadDrops(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node)
	base := baselineRSS(t, node)
	t.Logf("node %s, memory with no DNS load: %.1f MB", node.NodeID, float64(base)/megabyte)

	drop := overloadRunDuration
	lowPhase := overloadRunDuration
	lowRate := overloadCapacity / 4
	start := time.Now()
	var run *loadRun
	loadDone := make(chan struct{})
	go func() {
		defer close(loadDone)
		run = runDNSLoad(context.Background(), t, node.DNSAddr, start, []loadPhase{
			{Rate: 2 * overloadCapacity, Duration: drop},
			{Rate: lowRate, Duration: lowPhase},
		}, oc.names.mix)
	}()

	// The memory is read at the end of the 25 % phase, while the load is
	// still at that rate.
	time.Sleep(time.Until(start.Add(drop + lowPhase)))
	rss, rssErr := processRSS(node.cmd.Process.Pid)
	<-loadDone
	run.logPerSecond(t)
	run.checkRate(t, 0, drop, 2*overloadCapacity)
	run.checkRate(t, drop+time.Second, drop+lowPhase, lowRate)

	if node.Node.hasExited() {
		t.Fatalf("node %s exited during the run%s", node.NodeID, exitReport(node.Node))
	}
	overload := run.stats(0, drop)
	t.Logf("overload phase: %s", overload)
	if overload.Refused == 0 {
		t.Errorf("no query received REFUSED before the drop; the run does not test the scenario")
	}
	after := run.stats(drop+5*time.Second, run.Duration+time.Second)
	t.Logf("from 5 s after the drop to the end: %s", after)
	if after.Refused != 0 {
		t.Errorf("%d queries received REFUSED from 5 s after the drop; want none", after.Refused)
	}
	if float64(after.Lost) >= 0.001*float64(after.Sent) {
		t.Errorf("%d of %d queries sent from 5 s after the drop received no response; want less than 0.1 %%", after.Lost, after.Sent)
	}
	switch {
	case rssErr != nil:
		t.Errorf("memory of %s: %v", node.NodeID, rssErr)
	case rss > base+50*megabyte:
		t.Errorf("1 minute after the drop, the memory of %s is %.1f MB, %.1f MB over its level before the load; want within 50 MB",
			node.NodeID, float64(rss)/megabyte, float64(rss-base)/megabyte)
	default:
		t.Logf("1 minute after the drop, the memory of %s is %.1f MB, %.1f MB over its level before the load",
			node.NodeID, float64(rss)/megabyte, float64(rss-base)/megabyte)
	}
}

// @fsid:FS-DnsOverloadNoRefusalBelowCapacity
//
// Every name of the mix receives an answer other than REFUSED with no
// load. Then 75 % of C queries per second go to a follower for 1 minute
// (15 in the spec, decision D-3): no query receives REFUSED and less than
// 0.1 % receive no response.
func TestDnsOverloadNoRefusalBelowCapacity(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node) // checks NOERROR for every cached name
	for _, name := range append(oc.names.blocked, fmt.Sprintf("unloaded.%s.miss.overload.test", oc.names.runID)) {
		if rc := rcodeOf(node.DNSAddr, name); rc == -1 || rc == dns.RcodeRefused {
			t.Fatalf("with no load, %s on %s got RCODE %d; want an answer other than REFUSED", name, node.NodeID, rc)
		}
	}

	rate := overloadCapacity * 3 / 4
	run := runDNSLoadNow(t, node.DNSAddr, []loadPhase{{Rate: rate, Duration: overloadRunDuration}}, oc.names.mix)
	run.logPerSecond(t)
	run.checkRate(t, 0, overloadRunDuration, rate)

	all := run.all()
	t.Logf("whole run: %s", all)
	if all.Refused != 0 {
		t.Errorf("%d queries received REFUSED at 75 %% of C; want none", all.Refused)
	}
	if float64(all.Lost) >= 0.001*float64(all.Sent) {
		t.Errorf("%d of %d queries received no response; want less than 0.1 %%", all.Lost, all.Sent)
	}
}

// overloadLogLine is the log line of TS § 6.2.
var overloadLogLine = regexp.MustCompile(`dns: overloaded: refused (\d+) queries`)

// overloadRefusedTotal returns skoed_dns_overload_refused_total from a
// /metrics body, and false when the series is absent.
func overloadRefusedTotal(body string) (float64, bool) {
	m := regexp.MustCompile(`(?m)^skoed_dns_overload_refused_total(?:\{[^}]*\})?\s+(\S+)\s*$`).FindStringSubmatch(body)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil
}

// logWatch collects, every 500 ms until stopped, the lines of a node's
// output that match re. The harness keeps only the last lines a node
// wrote, so they are collected while the run goes on.
type logWatch struct {
	stop, done chan struct{}
	once       sync.Once
	seen       map[string]bool
	lines      []string
}

// completeLines returns the lines of b that end with a newline. The last,
// unterminated line is left out: it can still grow, and counting it now
// and again once complete would count one line twice.
func completeLines(b *tailBuffer) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	for i, line := range b.lines {
		out[i] = strings.TrimSpace(line)
	}
	return out
}

// watchLog starts a logWatch on n's output. It is stopped at the end of
// the test if the test does not stop it before.
func watchLog(t *testing.T, n *ClusterNode, re *regexp.Regexp) *logWatch {
	w := &logWatch{stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]bool{}}
	// Lines already written before the watch are not counted.
	for _, line := range completeLines(n.Node.output) {
		w.seen[line] = true
	}
	go func() {
		defer close(w.done)
		for {
			for _, line := range completeLines(n.Node.output) {
				if re.MatchString(line) && !w.seen[line] {
					w.seen[line] = true
					w.lines = append(w.lines, line)
				}
			}
			select {
			case <-w.stop:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() { w.Stop() })
	return w
}

// Stop stops the watch and returns the lines collected. It can be called
// more than once.
func (w *logWatch) Stop() []string {
	w.once.Do(func() { close(w.stop) })
	<-w.done
	return w.lines
}

// logTimestamp parses the date and time the standard logger puts at the
// start of a line ("2006/01/02 15:04:05").
func logTimestamp(line string) (time.Time, bool) {
	if len(line) < 19 {
		return time.Time{}, false
	}
	ts, err := time.ParseInLocation("2006/01/02 15:04:05", line[:19], time.Local)
	return ts, err == nil
}

// @fsid:FS-DnsOverloadVisibleToAdministrator
//
// 2 × C queries per second go to a follower for 1 minute. The increase of
// skoed_dns_overload_refused_total on that node is at least the number of
// REFUSED responses the clients received, and exceeds it by at most 2 %
// of the queries sent. The node's log, read 70 s after the run started,
// has at least one line saying it refused queries and how many, and at
// most one per 60-second interval: at most two lines in 70 s, and when
// the lines carry the standard logger's timestamp, at least 59 s apart
// (60 s at the 1-second resolution of the timestamp).
func TestDnsOverloadVisibleToAdministrator(t *testing.T) {
	oc := startOverloadCluster(t)
	oc.setCapacity(t, overloadCapacity)
	node := oc.follower(t)
	oc.warmCache(t, node)

	before, ok := overloadRefusedTotal(fetchMetrics(t, node.Node))
	if !ok {
		t.Errorf("before the run, /metrics of %s has no skoed_dns_overload_refused_total", node.NodeID)
	}

	logs := watchLog(t, node, overloadLogLine)
	start := time.Now()
	run := runDNSLoad(context.Background(), t, node.DNSAddr, start,
		[]loadPhase{{Rate: 2 * overloadCapacity, Duration: time.Minute}}, oc.names.mix)
	run.logPerSecond(t)
	run.checkRate(t, 0, time.Minute, 2*overloadCapacity)
	all := run.all()
	t.Logf("whole run: %s", all)

	after, ok := overloadRefusedTotal(fetchMetrics(t, node.Node))
	if !ok {
		t.Errorf("after the run, /metrics of %s has no skoed_dns_overload_refused_total", node.NodeID)
	} else {
		increase := after - before
		t.Logf("skoed_dns_overload_refused_total: %.0f → %.0f, increase %.0f; REFUSED received %d", before, after, increase, all.Refused)
		if increase < float64(all.Refused) {
			t.Errorf("skoed_dns_overload_refused_total increased by %.0f; want at least the %d REFUSED responses received", increase, all.Refused)
		}
		if limit := float64(all.Refused) + 0.02*float64(all.Sent); increase > limit {
			t.Errorf("skoed_dns_overload_refused_total increased by %.0f; want at most %.0f (REFUSED received + 2 %% of the %d queries sent)",
				increase, limit, all.Sent)
		}
	}
	if all.Refused == 0 {
		t.Errorf("no query received REFUSED at 2 × C")
	}

	time.Sleep(time.Until(start.Add(70 * time.Second)))
	lines := logs.Stop()
	t.Logf("log lines of %s matching %q within 70 s: %q", node.NodeID, overloadLogLine, lines)
	if len(lines) == 0 {
		t.Errorf("70 s after the run started, the log of %s has no line saying it refused queries", node.NodeID)
	}
	if len(lines) > 2 {
		t.Errorf("the log of %s has %d overload lines within 70 s; want at most one per 60-second interval", node.NodeID, len(lines))
	}
	for _, line := range lines {
		if n, err := strconv.Atoi(overloadLogLine.FindStringSubmatch(line)[1]); err != nil || n <= 0 {
			t.Errorf("overload log line does not say how many queries were refused: %q", line)
		}
	}
	for i := 1; i < len(lines); i++ {
		prev, ok1 := logTimestamp(lines[i-1])
		cur, ok2 := logTimestamp(lines[i])
		if ok1 && ok2 && cur.Sub(prev) < 59*time.Second {
			t.Errorf("overload log lines %s apart; want at most one per 60-second interval:\n%s\n%s", cur.Sub(prev), lines[i-1], lines[i])
		}
	}
}
