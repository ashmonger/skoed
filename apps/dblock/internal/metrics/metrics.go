// Package metrics owns the Prometheus exporter for dblock (M5.1).
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
// dblock_build_info. Inject from main.go (ldflags-friendly).
type BuildInfo struct {
	Version string
	Commit  string
}

// CacheStatsFunc returns the live cache snapshot, or zero-value when
// caching is disabled. dblock's cache may be reallocated when
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

// DhcpSnapshot is the subset of DHCP state the exporter needs.
type DhcpSnapshot struct {
	Source            string
	Leases            int
	AnomaliesOpen     int
	LastPollAgeSecs   float64
	PollErrorsTotal   uint64
}

// Metrics owns the registry and all dblock-specific collectors.
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

	requireAuth func() bool       // re-read on every request so config edits take effect live
	authOK      func(*http.Request) bool
}

// Options bundles every dependency needed at construction.
type Options struct {
	Build       BuildInfo
	CacheStats  CacheStatsFunc      // required; return Enabled=false when caching is off
	Cluster     ClusterStatusFunc   // required
	Dhcp        DhcpStatusFunc      // optional: nil disables every dhcp_* series
	Blocklists  BlocklistStatusFunc // optional: nil disables every blocklist_* series
	RequireAuth func() bool         // required; returns the live value of node.api.metrics.require_auth
	AuthOK      func(r *http.Request) bool
}

// New wires up a fresh metrics surface.
func New(opts Options) *Metrics {
	reg := prometheus.NewRegistry()

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dblock_build_info",
		Help: "Build metadata for the running dblock process. Always 1.",
	}, []string{"version", "commit", "go"})
	buildInfo.WithLabelValues(opts.Build.Version, opts.Build.Commit, runtime.Version()).Set(1)
	reg.MustRegister(buildInfo)

	dnsQueriesTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dblock_dns_queries_total",
		Help: "Count of DNS queries answered by dblock, by outcome and transport.",
	}, []string{"outcome", "transport"})
	reg.MustRegister(dnsQueriesTotal)

	dnsQueryDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dblock_dns_query_duration_seconds",
		Help:    "Wall-clock time to answer a DNS query, by outcome.",
		Buckets: []float64{0.001, 0.01, 0.1, 1, 5},
	}, []string{"outcome"})
	reg.MustRegister(dnsQueryDur)

	auditEventsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dblock_audit_events_total",
		Help: "Cumulative audit-log entries appended through Raft, by action.",
	}, []string{"action"})
	reg.MustRegister(auditEventsTotal)

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

	return &Metrics{
		reg:              reg,
		dnsQueriesTotal:  dnsQueriesTotal,
		dnsQueryDur:      dnsQueryDur,
		auditEventsTotal: auditEventsTotal,
		requireAuth:      opts.RequireAuth,
		authOK:           opts.AuthOK,
	}
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
				w.Header().Set("WWW-Authenticate", `Basic realm="dblock-metrics"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
		}
		prom.ServeHTTP(w, r)
	})
}

// ─── Collectors ─────────────────────────────────────────────────────────────

var (
	descCacheSize       = prometheus.NewDesc("dblock_dns_cache_size", "Current number of entries in the DNS response cache.", nil, nil)
	descCacheMaxEntries = prometheus.NewDesc("dblock_dns_cache_max_entries", "Configured maximum entries in the DNS response cache (0 = unbounded or disabled).", nil, nil)
	descCacheHits       = prometheus.NewDesc("dblock_dns_cache_hits_total", "Cumulative DNS cache hits since process start.", nil, nil)
	descCacheMisses     = prometheus.NewDesc("dblock_dns_cache_misses_total", "Cumulative DNS cache misses since process start.", nil, nil)
	descCacheEvictions  = prometheus.NewDesc("dblock_dns_cache_evictions_total", "Cumulative LRU evictions from the DNS cache since process start.", nil, nil)
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
	descRole        = prometheus.NewDesc("dblock_cluster_node_role", "1 if this node currently holds the labelled role, else 0.", []string{"role"}, nil)
	descRaftTerm    = prometheus.NewDesc("dblock_cluster_raft_term", "Latest Raft term observed by this node.", nil, nil)
	descCommitIndex = prometheus.NewDesc("dblock_cluster_commit_index", "Latest committed Raft log index on this node.", nil, nil)
	descMembers     = prometheus.NewDesc("dblock_cluster_members", "Number of nodes in the current Raft configuration.", nil, nil)
	descReachable   = prometheus.NewDesc("dblock_cluster_reachable_members", "Number of cluster members reachable from this node (always >= 1 since the local node counts).", nil, nil)
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
	descDhcpLeases      = prometheus.NewDesc("dblock_dhcp_leases", "Current count of leases known to the DHCP integration, by source.", []string{"source"}, nil)
	descDhcpAnomalies   = prometheus.NewDesc("dblock_dhcp_anomalies_open", "Number of unacknowledged anti-spoof anomalies.", nil, nil)
	descDhcpLastPollAge = prometheus.NewDesc("dblock_dhcp_last_poll_age_seconds", "Seconds since the DHCP source was last successfully polled, by source.", []string{"source"}, nil)
	descDhcpPollErrors  = prometheus.NewDesc("dblock_dhcp_poll_errors_total", "Cumulative DHCP source poll failures since process start, by source.", []string{"source"}, nil)
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
	ch <- prometheus.MustNewConstMetric(descDhcpLeases, prometheus.GaugeValue, float64(s.Leases), src)
	ch <- prometheus.MustNewConstMetric(descDhcpAnomalies, prometheus.GaugeValue, float64(s.AnomaliesOpen))
	ch <- prometheus.MustNewConstMetric(descDhcpLastPollAge, prometheus.GaugeValue, s.LastPollAgeSecs, src)
	ch <- prometheus.MustNewConstMetric(descDhcpPollErrors, prometheus.CounterValue, float64(s.PollErrorsTotal), src)
}

var (
	descBlocklistLastRefresh = prometheus.NewDesc("dblock_blocklist_last_refresh_seconds", "Unix timestamp of the last successful or failed refresh attempt, per blocklist id.", []string{"id"}, nil)
	descBlocklistFailures    = prometheus.NewDesc("dblock_blocklist_refresh_failures_total", "Cumulative refresh failures since process start, per blocklist id.", []string{"id"}, nil)
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
