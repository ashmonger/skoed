// Package metrics owns the Prometheus exporter for skoed (M5.1).
//
// A dedicated *prometheus.Registry holds every series — never
// prometheus.DefaultRegisterer — so we don't accidentally export the
// Go runtime collectors (process_*, go_*) we'd then have to support
// forever. Tests can build a fresh Metrics per node without colliding.
//
// Series catalogue and cardinality budget live in
// specs/technical/prometheus-metrics.md (TS-PrometheusMetrics).
package metrics

import (
	"net/http"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// BuildInfo describes the running binary. Embedded as labels on
// skoed_build_info. Inject from main.go (ldflags-friendly).
type BuildInfo struct {
	Version string
	Commit  string
}

// CacheStatsFunc returns the live cache snapshot, or zero-value when
// caching is disabled. skoed's cache may be reallocated when
// max_entries changes (see main.go rebuildDNS), so we read it through
// a callback rather than holding a stale pointer.
type CacheStatsFunc func() CacheSnapshot

// CacheSnapshot mirrors dnsengine.Stats — duplicated here so the
// metrics package doesn't import the dns package (which would cycle
// once dns starts observing metrics).
type CacheSnapshot struct {
	Size       int
	MaxEntries int
	Hits       uint64
	Misses     uint64
	Evictions  uint64
	Enabled    bool
}

// ClusterStatusFunc returns the live cluster status snapshot.
type ClusterStatusFunc func() ClusterSnapshot

// ClusterSnapshot is the subset of cluster state the exporter needs.
type ClusterSnapshot struct {
	IsLeader         bool
	RaftTerm         uint64
	CommitIndex      uint64
	Members          int
	ReachableMembers int
}

// DhcpStatusFunc returns the live DHCP integration status, or nil
// when DHCP is not configured. When nil, the dhcp_* series are NOT
// registered — operators without DHCP integration see no ghost zeros.
type DhcpStatusFunc func() *DhcpSnapshot

// BlocklistStatusFunc returns one BlocklistSnapshot per blocklist
// known to the cluster, for M5.4 refresh metrics. When nil, the
// per-blocklist series are NOT registered.
type BlocklistStatusFunc func() []BlocklistSnapshot

// BlocklistSnapshot is the subset of blocklist refresh state the
// exporter needs to label per-id metrics.
type BlocklistSnapshot struct {
	ID            string
	LastRefreshAt time.Time // zero ⇒ never refreshed
	Failures      uint64
}

// DohResolverStatusFunc returns the current state of the M6 DoH/DoT
// resolver snapshot. Optional: when nil the doh_resolver_* series are
// not registered (operators without the M6 surface see no ghost zeros).
type DohResolverStatusFunc func() DohResolverSnapshotInfo

// DohResolverSnapshotInfo is the subset of resolver-snapshot state the
// exporter exposes. Counters are absolute since process start; the
// gauge values reflect the current replicated snapshot.
type DohResolverSnapshotInfo struct {
	Successes        uint64
	Failures         uint64
	ResolverCount    int
	LastRefreshUnix  int64 // zero ⇒ never refreshed
}

// DhcpSnapshot is the subset of DHCP state the exporter needs.
//
// M6.5 (TS-LeaseOrigin): OriginCounts maps Origin values
// ("dhcp_static", "dhcp_dynamic", ...) to the number of leases carrying
// them. The collector emits one skoed_dhcp_leases series per
// (source, origin) pair, but only for origins with a non-zero count
// (per FS-LeaseOriginPrometheusGauges).
//
// `Leases` is preserved as the unlabeled total — operators on M3.6
// dashboards continue to graph the legacy series.
type DhcpSnapshot struct {
	Source           string
	Leases           int
	OriginCounts     map[string]int
	AnomaliesOpen    int
	LastPollAgeSecs  float64
	PollErrorsTotal  uint64
}

// Metrics owns the registry and all skoed-specific collectors.
type Metrics struct {
	reg *prometheus.Registry

	// DNS counters — observed from the handler on every query.
	dnsQueriesTotal *prometheus.CounterVec
	dnsQueryDur     *prometheus.HistogramVec

	// Cache counters/gauges are populated via CollectorFunc so we never
	// have to mirror them — the cache itself is the source of truth.
	// Cluster + DHCP work the same way.

	// M5.2 — audit append counter, bumped from the audit middleware on
	// every successful Raft-replicated append.
	auditEventsTotal *prometheus.CounterVec

	// M5.9.7 — test-domain verdict counter. Cardinality bounded at 4
	// series total (2 surfaces × 2 verdicts); domain never appears as
	// a label.
	testDomainTotal *prometheus.CounterVec

	// M6 — firewall-rule-generator request counter. Cardinality bounded
	// at 5 series total (one per supported platform); scope/action are
	// deliberately NOT labels.
	firewallRulesTotal *prometheus.CounterVec

	requireAuth func() bool       // re-read on every request so config edits take effect live
	authOK      func(*http.Request) bool
}

// Options bundles every dependency needed at construction.
type Options struct {
	Build         BuildInfo
	CacheStats    CacheStatsFunc        // required; return Enabled=false when caching is off
	Cluster       ClusterStatusFunc     // required
	Dhcp          DhcpStatusFunc        // optional: nil disables every dhcp_* series
	Blocklists    BlocklistStatusFunc   // optional: nil disables every blocklist_* series
	DohResolvers  DohResolverStatusFunc // optional: nil disables every doh_resolver_* series
	RequireAuth   func() bool           // required; returns the live value of node.api.metrics.require_auth
	AuthOK        func(r *http.Request) bool
}

// New wires up a fresh metrics surface.
func New(opts Options) *Metrics {
	reg := prometheus.NewRegistry()

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "skoed_build_info",
		Help: "Build metadata for the running skoed process. Always 1.",
	}, []string{"version", "commit", "go"})
	buildInfo.WithLabelValues(opts.Build.Version, opts.Build.Commit, runtime.Version()).Set(1)
	reg.MustRegister(buildInfo)

	dnsQueriesTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "skoed_dns_queries_total",
		Help: "Count of DNS queries answered by skoed, by outcome and transport.",
	}, []string{"outcome", "transport"})
	reg.MustRegister(dnsQueriesTotal)

	dnsQueryDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "skoed_dns_query_duration_seconds",
		Help:    "Wall-clock time to answer a DNS query, by outcome.",
		Buckets: []float64{0.001, 0.01, 0.1, 1, 5},
	}, []string{"outcome"})
	reg.MustRegister(dnsQueryDur)

	auditEventsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "skoed_audit_events_total",
		Help: "Cumulative audit-log entries appended through Raft, by action.",
	}, []string{"action"})
	reg.MustRegister(auditEventsTotal)

	testDomainTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "skoed_test_domain_requests_total",
		Help: "Cumulative M5.9.7 \"would this domain be blocked?\" verdicts, by surface and verdict.",
	}, []string{"surface", "verdict"})
	reg.MustRegister(testDomainTotal)

	firewallRulesTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "skoed_firewall_rules_generated_total",
		Help: "Cumulative M6 firewall-rule generator successful renders, by target platform.",
	}, []string{"platform"})
	reg.MustRegister(firewallRulesTotal)

	// Cache — registered as a custom Collector that reads from the
	// CacheStatsFunc on every scrape. Wired only when caching is
	// available; when CacheStats reports Enabled=false the size gauge
	// still surfaces 0 / max_entries 0 so PromQL queries don't go
	// missing-series.
	reg.MustRegister(&cacheCollector{stats: opts.CacheStats})

	// Cluster — always present; single-node nodes report
	// members=1 reachable_members=1 leader=1.
	reg.MustRegister(&clusterCollector{stats: opts.Cluster})

	// DHCP — only registered when the integration is on.
	if opts.Dhcp != nil {
		reg.MustRegister(&dhcpCollector{stats: opts.Dhcp})
	}

	// M5.4 — per-blocklist refresh health.
	if opts.Blocklists != nil {
		reg.MustRegister(&blocklistCollector{stats: opts.Blocklists})
	}

	// M6 — curated DoH/DoT resolver snapshot health.
	if opts.DohResolvers != nil {
		reg.MustRegister(&dohResolverCollector{stats: opts.DohResolvers})
	}

	return &Metrics{
		reg:                reg,
		dnsQueriesTotal:    dnsQueriesTotal,
		dnsQueryDur:        dnsQueryDur,
		auditEventsTotal:   auditEventsTotal,
		testDomainTotal:    testDomainTotal,
		firewallRulesTotal: firewallRulesTotal,
		requireAuth:        opts.RequireAuth,
		authOK:             opts.AuthOK,
	}
}

// ObserveTestDomain bumps the M5.9.7 verdict counter.
//
//	surface ∈ {"guest","auth"}
//	verdict ∈ {"block","allow"}
func (m *Metrics) ObserveTestDomain(surface, verdict string) {
	if m == nil || m.testDomainTotal == nil {
		return
	}
	m.testDomainTotal.WithLabelValues(surface, verdict).Inc()
}

// ObserveFirewallRulesGenerated bumps the M6 firewall-rule generator
// counter for one successful render.
//
//	platform ∈ {"iptables","nftables","mikrotik","opnsense","unifi"}
func (m *Metrics) ObserveFirewallRulesGenerated(platform string) {
	if m == nil || m.firewallRulesTotal == nil {
		return
	}
	m.firewallRulesTotal.WithLabelValues(platform).Inc()
}

// ObserveAudit bumps the audit-event counter for the given action.
// Called by the audit middleware after a successful Raft append.
func (m *Metrics) ObserveAudit(action string) {
	if m == nil || m.auditEventsTotal == nil {
		return
	}
	m.auditEventsTotal.WithLabelValues(action).Inc()
}

// ObserveQuery records one DNS-handler exit. outcome is the dlog.Outcome
// string ("forwarded", "blocked", "cached", "local", optionally suffixed
// with "-doh" or "-dot" when the query came in over an encrypted
// transport). We split that suffix back into a separate transport label
// so PromQL can aggregate either way.
func (m *Metrics) ObserveQuery(outcome string, duration time.Duration) {
	o, t := splitOutcomeTransport(outcome)
	m.dnsQueriesTotal.WithLabelValues(o, t).Inc()
	m.dnsQueryDur.WithLabelValues(o).Observe(duration.Seconds())
}

// splitOutcomeTransport turns "forwarded-doh" into ("forwarded","doh").
// Plain UDP/TCP queries arrive with no suffix; they're tagged as "udp".
func splitOutcomeTransport(raw string) (outcome, transport string) {
	for _, suffix := range []string{"-doh", "-dot"} {
		if n := len(raw) - len(suffix); n > 0 && raw[n:] == suffix {
			return raw[:n], suffix[1:]
		}
	}
	return raw, "udp"
}

// Handler returns the /metrics HTTP handler. When RequireAuth() is
// false (the default), the handler answers any request; when true,
// AuthOK gates the response.
func (m *Metrics) Handler() http.Handler {
	prom := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.requireAuth != nil && m.requireAuth() {
			if m.authOK == nil || !m.authOK(r) {
				w.Header().Set("WWW-Authenticate", `Basic realm="skoed-metrics"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
		}
		prom.ServeHTTP(w, r)
	})
}

// ─── Collectors ─────────────────────────────────────────────────────────────

var (
	descCacheSize       = prometheus.NewDesc("skoed_dns_cache_size", "Current number of entries in the DNS response cache.", nil, nil)
	descCacheMaxEntries = prometheus.NewDesc("skoed_dns_cache_max_entries", "Configured maximum entries in the DNS response cache (0 = unbounded or disabled).", nil, nil)
	descCacheHits       = prometheus.NewDesc("skoed_dns_cache_hits_total", "Cumulative DNS cache hits since process start.", nil, nil)
	descCacheMisses     = prometheus.NewDesc("skoed_dns_cache_misses_total", "Cumulative DNS cache misses since process start.", nil, nil)
	descCacheEvictions  = prometheus.NewDesc("skoed_dns_cache_evictions_total", "Cumulative LRU evictions from the DNS cache since process start.", nil, nil)
)

type cacheCollector struct{ stats CacheStatsFunc }

func (c *cacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descCacheSize
	ch <- descCacheMaxEntries
	ch <- descCacheHits
	ch <- descCacheMisses
	ch <- descCacheEvictions
}

func (c *cacheCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	ch <- prometheus.MustNewConstMetric(descCacheSize, prometheus.GaugeValue, float64(s.Size))
	ch <- prometheus.MustNewConstMetric(descCacheMaxEntries, prometheus.GaugeValue, float64(s.MaxEntries))
	ch <- prometheus.MustNewConstMetric(descCacheHits, prometheus.CounterValue, float64(s.Hits))
	ch <- prometheus.MustNewConstMetric(descCacheMisses, prometheus.CounterValue, float64(s.Misses))
	ch <- prometheus.MustNewConstMetric(descCacheEvictions, prometheus.CounterValue, float64(s.Evictions))
}

var (
	descRole        = prometheus.NewDesc("skoed_cluster_node_role", "1 if this node currently holds the labelled role, else 0.", []string{"role"}, nil)
	descRaftTerm    = prometheus.NewDesc("skoed_cluster_raft_term", "Latest Raft term observed by this node.", nil, nil)
	descCommitIndex = prometheus.NewDesc("skoed_cluster_commit_index", "Latest committed Raft log index on this node.", nil, nil)
	descMembers     = prometheus.NewDesc("skoed_cluster_members", "Number of nodes in the current Raft configuration.", nil, nil)
	descReachable   = prometheus.NewDesc("skoed_cluster_reachable_members", "Number of cluster members reachable from this node (always >= 1 since the local node counts).", nil, nil)
)

type clusterCollector struct{ stats ClusterStatusFunc }

func (c *clusterCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descRole
	ch <- descRaftTerm
	ch <- descCommitIndex
	ch <- descMembers
	ch <- descReachable
}

func (c *clusterCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	leader, follower := 0.0, 1.0
	if s.IsLeader {
		leader, follower = 1.0, 0.0
	}
	ch <- prometheus.MustNewConstMetric(descRole, prometheus.GaugeValue, leader, "leader")
	ch <- prometheus.MustNewConstMetric(descRole, prometheus.GaugeValue, follower, "follower")
	ch <- prometheus.MustNewConstMetric(descRaftTerm, prometheus.GaugeValue, float64(s.RaftTerm))
	ch <- prometheus.MustNewConstMetric(descCommitIndex, prometheus.GaugeValue, float64(s.CommitIndex))
	ch <- prometheus.MustNewConstMetric(descMembers, prometheus.GaugeValue, float64(s.Members))
	ch <- prometheus.MustNewConstMetric(descReachable, prometheus.GaugeValue, float64(s.ReachableMembers))
}

var (
	descDhcpLeases       = prometheus.NewDesc("skoed_dhcp_leases", "Current count of leases known to the DHCP integration, by source and origin. The `origin` label is empty when DHCP integration is not yet producing origin tags.", []string{"source", "origin"}, nil)
	descDhcpAnomalies    = prometheus.NewDesc("skoed_dhcp_anomalies_open", "Number of unacknowledged anti-spoof anomalies.", nil, nil)
	descDhcpLastPollAge  = prometheus.NewDesc("skoed_dhcp_last_poll_age_seconds", "Seconds since the DHCP source was last successfully polled, by source.", []string{"source"}, nil)
	descDhcpPollErrors   = prometheus.NewDesc("skoed_dhcp_poll_errors_total", "Cumulative DHCP source poll failures since process start, by source.", []string{"source"}, nil)
)

type dhcpCollector struct{ stats DhcpStatusFunc }

func (c *dhcpCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descDhcpLeases
	ch <- descDhcpAnomalies
	ch <- descDhcpLastPollAge
	ch <- descDhcpPollErrors
}

func (c *dhcpCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	if s == nil {
		return
	}
	src := s.Source
	if src == "" {
		src = "unknown"
	}
	// M6.5 — TS-LeaseOrigin: emit one series per (source, origin) that
	// actually appears in the snapshot. When no origin tags are present
	// yet (M3.6-only DHCP source), fall back to the legacy unlabelled
	// total under `origin=""` so existing dashboards keep working.
	emitted := 0
	for origin, n := range s.OriginCounts {
		if n == 0 || origin == "" {
			continue
		}
		ch <- prometheus.MustNewConstMetric(descDhcpLeases, prometheus.GaugeValue, float64(n), src, origin)
		emitted++
	}
	if emitted == 0 {
		ch <- prometheus.MustNewConstMetric(descDhcpLeases, prometheus.GaugeValue, float64(s.Leases), src, "")
	}
	ch <- prometheus.MustNewConstMetric(descDhcpAnomalies, prometheus.GaugeValue, float64(s.AnomaliesOpen))
	ch <- prometheus.MustNewConstMetric(descDhcpLastPollAge, prometheus.GaugeValue, s.LastPollAgeSecs, src)
	ch <- prometheus.MustNewConstMetric(descDhcpPollErrors, prometheus.CounterValue, float64(s.PollErrorsTotal), src)
}

var (
	descBlocklistLastRefresh = prometheus.NewDesc("skoed_blocklist_last_refresh_seconds", "Unix timestamp of the last successful or failed refresh attempt, per blocklist id.", []string{"id"}, nil)
	descBlocklistFailures    = prometheus.NewDesc("skoed_blocklist_refresh_failures_total", "Cumulative refresh failures since process start, per blocklist id.", []string{"id"}, nil)
)

type blocklistCollector struct{ stats BlocklistStatusFunc }

func (b *blocklistCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descBlocklistLastRefresh
	ch <- descBlocklistFailures
}

func (b *blocklistCollector) Collect(ch chan<- prometheus.Metric) {
	for _, bl := range b.stats() {
		var ts float64
		if !bl.LastRefreshAt.IsZero() {
			ts = float64(bl.LastRefreshAt.Unix())
		}
		ch <- prometheus.MustNewConstMetric(descBlocklistLastRefresh, prometheus.GaugeValue, ts, bl.ID)
		ch <- prometheus.MustNewConstMetric(descBlocklistFailures, prometheus.CounterValue, float64(bl.Failures), bl.ID)
	}
}

var (
	descDohResolverRefreshTotal       = prometheus.NewDesc("skoed_doh_resolver_refresh_total", "Cumulative DoH/DoT resolver snapshot refresh attempts since process start, by outcome.", []string{"outcome"}, nil)
	descDohResolverCount              = prometheus.NewDesc("skoed_doh_resolver_count", "Number of resolver entries in the current snapshot.", nil, nil)
	descDohResolverLastRefreshSeconds = prometheus.NewDesc("skoed_doh_resolver_last_refresh_timestamp_seconds", "Unix timestamp of the last successful resolver-snapshot refresh.", nil, nil)
)

type dohResolverCollector struct{ stats DohResolverStatusFunc }

func (d *dohResolverCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descDohResolverRefreshTotal
	ch <- descDohResolverCount
	ch <- descDohResolverLastRefreshSeconds
}

func (d *dohResolverCollector) Collect(ch chan<- prometheus.Metric) {
	s := d.stats()
	// Always emit both outcome series so PromQL queries don't go
	// missing-series on a fresh cluster (cardinality bound = 2).
	ch <- prometheus.MustNewConstMetric(descDohResolverRefreshTotal, prometheus.CounterValue, float64(s.Successes), "success")
	ch <- prometheus.MustNewConstMetric(descDohResolverRefreshTotal, prometheus.CounterValue, float64(s.Failures), "failure")
	ch <- prometheus.MustNewConstMetric(descDohResolverCount, prometheus.GaugeValue, float64(s.ResolverCount))
	ch <- prometheus.MustNewConstMetric(descDohResolverLastRefreshSeconds, prometheus.GaugeValue, float64(s.LastRefreshUnix))
}
