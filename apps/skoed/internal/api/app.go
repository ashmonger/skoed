package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/api/handlers"
	apimw "github.com/skoed/skoed/internal/api/middleware"
	"github.com/skoed/skoed/internal/api/static"
	"github.com/skoed/skoed/internal/api/swaggerui"
	"github.com/skoed/skoed/internal/auth"
	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/dhcp"
	dnsengine "github.com/skoed/skoed/internal/dns"
	"github.com/skoed/skoed/internal/dohresolvers"
	"github.com/skoed/skoed/internal/filter"
	dlog "github.com/skoed/skoed/internal/log"
	"github.com/skoed/skoed/internal/metrics"
	"github.com/skoed/skoed/internal/upgrade"
	"github.com/skoed/skoed/internal/webhook"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// App is the HTTP-facing facade over the cluster. It owns the live
// *cluster.Cluster (Raft+bbolt) and a cached *config.Config snapshot kept in
// sync by a Subscribe callback. All mutations are routed through Raft so
// every node converges on the same state.
type App struct {
	cluster   *cluster.Cluster
	authStore *auth.Store
	queryLog  *dlog.QueryLog
	dhcpMgr   *dhcp.Manager     // optional; nil when DHCP integration is disabled
	dnsCache  *dnsengine.Cache  // M4.7 — long-lived; survives Raft applies
	metrics   *metrics.Metrics  // M5.1 — Prometheus exporter; nil disables /metrics

	upgradeChecker *upgrade.Checker // M5.6 — release-feed cache; nil disables /upgrade/*

	// metricsRequireAuth is node-local config (node.api.metrics.require_auth).
	// Cached at construction; today operators restart the process to flip it,
	// matching how every other node.api.* knob works.
	metricsRequireAuth bool

	// publicLandingEnabled gates the M5.9.5 unauthenticated landing page
	// + /api/v1/_public/test-blocklist endpoint. Default true; flipped
	// off from node.api.public_landing.enabled=false. Cached at boot —
	// operators restart to change it.
	publicLandingEnabled bool

	// publicTester is the per-IP rate-limited handler for
	// /api/v1/_public/test-blocklist. Built once; survives Raft applies.
	publicTester *handlers.PublicTester

	// dohResolverSched is the M6 leader-only refresh scheduler. Nil
	// before SetDohResolverScheduler is called (tests that don't wire
	// the scheduler still get the route registered; ForceRefresh
	// returns 503 in that case).
	dohResolverSched *dohresolvers.Scheduler

	// dohResolverStaleAfter is the threshold beyond which a snapshot's
	// `stale` field is reported true. Defaults to 7 days; node-local
	// (operators set it under node.doh_resolver_db.stale_after_seconds).
	dohResolverStaleAfter time.Duration

	// rebuildDNS is invoked after every committed FSM apply that may have
	// changed DNS-affecting state (settings, local DNS entries). Set by main.go.
	rebuildDNS func(*config.Config) error

	// cfg + filterEng are caches refreshed on every committed apply via the
	// onApply Subscribe callback. cfgMu guards both.
	cfgMu     sync.RWMutex
	cfg       *config.Config
	filterEng *filter.Engine

	// apiTokensByHash is an in-memory lookup table rebuilt on every onApply.
	// Key = hex(sha256(rawToken)); value = pointer into apiTokensByID.
	// tokenMu guards both maps.
	tokenMu        sync.RWMutex
	apiTokensByHash map[string]*cluster.APIToken
	apiTokensByID   map[string]*cluster.APIToken

	// sessions holds short-lived login session tokens (8 h TTL, node-local).
	// Issued by POST /api/v1/auth/login; revoked by DELETE /api/v1/auth/session.
	sessions *sessionStore

	// webhookDispatcher is the M22 push-alert fan-out engine. Nil until
	// SetWebhookDispatcher is called (e.g. in unit tests that don't need
	// webhooks). FireWebhookTest returns an error when nil.
	webhookDispatcher *webhook.Dispatcher
}

// SetDhcpManager wires the optional M3.6 DHCP manager in after App
// construction. main.go calls this after the manager is built so the
// API handlers can reach it via app.GetDhcpMgr().
//
// M6.5 (TS-LeaseRepl): when a cluster is present we also wire the
// leader-only gate and the Raft replicator so the manager polls only
// on the elected leader and pushes its canonical snapshot through Raft.
// Followers run the manager goroutine but the per-tick pollOnce skips
// the connector and waits for ApplyReplicatedSnapshot to refresh the
// in-memory view.
func (a *App) SetDhcpManager(m *dhcp.Manager) {
	a.dhcpMgr = m
	// M6.5 (TS-BlockDyn): wire the origin lookup regardless of cluster
	// mode so block_dynamic_clients works in single-node mode too.
	defer a.wireLeaseOriginLookup()
	if m == nil || a.cluster == nil {
		return
	}
	c := a.cluster
	connectorKind := m.Source()
	m.SetLeaderCheck(c.IsLeader)
	m.SetReplicator(func(leases []dhcp.Lease, pollUnix int64) error {
		return c.ReplicateLeases(cluster.LeasesReplacePayload{
			LeaderNodeID:  c.Node().Node.ID,
			ConnectorKind: connectorKind,
			SourceURL:     a.dhcpSourceURL(),
			PollUnix:      pollUnix,
			Leases:        leases,
		})
	})
	m.SetAnomalyReplicator(func(an dhcp.Anomaly) error {
		return c.ReplicateAnomaly(an)
	})
	// Prime the local view from whatever the cluster already has so a
	// fresh process catches up without waiting for the next leader poll.
	if lp, err := c.CurrentLeaseSnapshot(); err == nil && lp != nil {
		m.ApplyReplicatedSnapshot(lp.Leases, lp.PollUnix)
	}
	if anomalies, err := c.CurrentLeaseAnomalies(); err == nil {
		m.ResetReplicatedAnomalies(anomalies)
	}
	// Start a tiny goroutine that nudges the manager whenever this node
	// becomes leader, so the first replicated snapshot lands within
	// seconds of the election (FS-LeaseReplLeaderFailoverResumesPolling).
	go a.leaderNudgeLoop(m)
}

// wireLeaseOriginLookup wires the block-dynamic-clients lookup into the
// filter engine. Called once after both the DHCP manager and filter engine
// are available. Safe to call multiple times (idempotent).
func (a *App) wireLeaseOriginLookup() {
	mgr := a.dhcpMgr
	if mgr == nil {
		return
	}
	a.cfgMu.RLock()
	eng := a.filterEng
	a.cfgMu.RUnlock()
	if eng == nil {
		return
	}
	eng.SetLeaseOriginLookup(func(ip string) string {
		lease, ok := mgr.LookupByIP(ip)
		if !ok {
			return ""
		}
		// Only fire block_dynamic_clients for leases where we are
		// confident about the origin (FS-BlockDynUnknownOriginTreatedAsNotDynamic).
		// An empty wire field tags as dhcp_dynamic/unknown — that must not
		// trigger the rule.
		if lease.OriginConfidence == dhcp.OriginConfidenceUnknown {
			return ""
		}
		return string(lease.Origin)
	})
}

// dhcpSourceURL returns the node-local DHCP URL (best-effort). Used
// only for the LeasesSource.SourceURL surface; safe to return "" when
// the connector exposes nothing useful.
func (a *App) dhcpSourceURL() string {
	cfg := a.GetCfg()
	if cfg == nil {
		return ""
	}
	// Try node.dhcp.url first (via the YAML mirror exposed in cfg.Node).
	// Fall back to "" — handlers degrade to connector_kind alone.
	return ""
}

// leaderNudgeLoop watches the cluster leader id and nudges the manager
// on every leader-acquire transition. Best-effort polling — there's no
// public "leader changed" event channel on hashicorp/raft.
func (a *App) leaderNudgeLoop(m *dhcp.Manager) {
	wasLeader := a.cluster.IsLeader()
	if wasLeader {
		m.Nudge()
	}
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		isLeader := a.cluster.IsLeader()
		if isLeader && !wasLeader {
			m.Nudge()
		}
		wasLeader = isLeader
	}
}

// GetDhcpMgr returns the configured DHCP manager, or nil when M3.6
// integration is disabled.
func (a *App) GetDhcpMgr() *dhcp.Manager { return a.dhcpMgr }

// SetDNSCache wires the long-lived M4.7 DNS cache into the App so the
// /api/v1/dns/cache/* handlers reach it via GetDNSCache(). main.go
// constructs the cache once at boot and keeps the same pointer across
// every Raft-driven handler rebuild.
func (a *App) SetDNSCache(c *dnsengine.Cache) { a.dnsCache = c }

// GetDNSCache returns the live DNS cache, or nil when caching is
// disabled in config.
func (a *App) GetDNSCache() *dnsengine.Cache { return a.dnsCache }

// SetMetrics wires the M5.1 Prometheus exporter. Pass nil to keep
// /metrics disabled (mainly useful for unit tests).
func (a *App) SetMetrics(m *metrics.Metrics) { a.metrics = m }

// GetMetrics returns the Prometheus exporter, or nil if not wired.
func (a *App) GetMetrics() *metrics.Metrics { return a.metrics }

// ObserveTestDomain is the handlers-package facing shim for the M5.9.7
// skoed_test_domain_requests_total counter. No-op when metrics aren't
// wired so handlers don't have to nil-check.
func (a *App) ObserveTestDomain(surface, verdict string) {
	if a.metrics == nil {
		return
	}
	a.metrics.ObserveTestDomain(surface, verdict)
}

// ObserveFirewallRulesGenerated is the handlers-package shim for the
// M6 skoed_firewall_rules_generated_total counter. No-op when metrics
// aren't wired so handlers don't have to nil-check.
func (a *App) ObserveFirewallRulesGenerated(platform string) {
	if a.metrics == nil {
		return
	}
	a.metrics.ObserveFirewallRulesGenerated(platform)
}

// SetUpgradeChecker wires the M5.6 release-feed checker. Nil disables
// the /api/v1/upgrade/* endpoints' useful behaviour (they still
// respond, but with empty data).
func (a *App) SetUpgradeChecker(c *upgrade.Checker) { a.upgradeChecker = c }

// GetUpgradeChecker returns the upgrade-feed checker as the handlers
// interface. nil-safe: handlers handle the absent-checker case.
func (a *App) GetUpgradeChecker() handlers.UpgradeChecker {
	if a.upgradeChecker == nil {
		return nil
	}
	return a.upgradeChecker
}

// SetMetricsRequireAuth flips /metrics from open (default) to
// Basic-auth gated. Called by main.go from the node-local
// node.api.metrics.require_auth flag.
func (a *App) SetMetricsRequireAuth(v bool) { a.metricsRequireAuth = v }

// MetricsRequireAuth reports the current value of the metrics auth gate.
func (a *App) MetricsRequireAuth() bool { return a.metricsRequireAuth }

// SetDohResolverScheduler wires the M6 leader-only refresh scheduler.
// Main passes the live *dohresolvers.Scheduler so the POST /refresh
// handler can Nudge() it.
func (a *App) SetDohResolverScheduler(s *dohresolvers.Scheduler) { a.dohResolverSched = s }

// SetDohResolverStaleAfter records the stale-after threshold used by
// the read handlers. Zero falls back to 7 days.
func (a *App) SetDohResolverStaleAfter(d time.Duration) { a.dohResolverStaleAfter = d }

// GetDohResolverSnapshot returns the current replicated snapshot or
// (nil, nil) when no snapshot has ever been written.
func (a *App) GetDohResolverSnapshot() (*dohresolvers.Snapshot, error) {
	if a.cluster == nil {
		return nil, nil
	}
	return a.cluster.CurrentDohSnapshot()
}

// GetDohResolverScheduler returns the live scheduler as the handlers
// interface (nil-safe).
func (a *App) GetDohResolverScheduler() handlers.DohResolverScheduler {
	if a.dohResolverSched == nil {
		return nil
	}
	return a.dohResolverSched
}

// DohResolverStaleAfter returns the configured threshold (default 7d).
func (a *App) DohResolverStaleAfter() time.Duration {
	if a.dohResolverStaleAfter <= 0 {
		return 7 * 24 * time.Hour
	}
	return a.dohResolverStaleAfter
}

// SetPublicLandingEnabled toggles the M5.9.5 unauthenticated landing
// page (/) and its companion endpoint /api/v1/_public/test-blocklist.
// Default is true; main.go calls this from the node-local
// node.api.public_landing.enabled flag. When false, the SPA's
// landing page is replaced by the normal login redirect and the
// endpoint returns 404.
func (a *App) SetPublicLandingEnabled(v bool) { a.publicLandingEnabled = v }

// PublicLandingEnabled reports whether the unauthenticated landing
// surface is active on this node.
func (a *App) PublicLandingEnabled() bool { return a.publicLandingEnabled }

// CheckBasicAuth validates Basic credentials against the auth store.
// Returns true when both auth is configured and credentials are valid.
// Used by the M5.1 metrics handler when the operator opted into
// authenticated /metrics.
func (a *App) CheckBasicAuth(r *http.Request) bool {
	if !a.authStore.IsConfigured() {
		return false
	}
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return a.authStore.Verify(u, p)
}

// NewApp wires up the facade. cluster must be already running (Raft started);
// authStore and queryLog are node-local services. rebuildDNS may be nil if
// no DNS server is managed.
func NewApp(
	c *cluster.Cluster,
	authStore *auth.Store,
	queryLog *dlog.QueryLog,
	rebuildDNS func(*config.Config) error,
) *App {
	a := &App{
		cluster:              c,
		authStore:            authStore,
		queryLog:             queryLog,
		rebuildDNS:           rebuildDNS,
		publicLandingEnabled: true,
		publicTester:         handlers.NewPublicTester(),
		sessions:             newSessionStore(),
	}
	// Prime the cache.
	if snap, err := c.Store().Snapshot(); err == nil {
		a.cfg = snap
		a.filterEng = filter.NewProfiled(snap)
	} else {
		a.cfg = &config.Config{}
		a.filterEng = filter.New(config.FilteringConfig{})
	}
	// Keep the cache fresh after every replicated apply.
	c.Subscribe(a.onApply)
	// Prime the token cache from whatever is already in bbolt.
	a.rebuildTokenCache()
	return a
}

// onApply runs after every committed FSM apply on this node. It refreshes
// the cached config, rebuilds the filter engine, syncs the auth store, and
// fires the DNS rebuild callback. Errors are swallowed so an apply never
// fails because of a downstream rebuild.
func (a *App) onApply() {
	snap, err := a.cluster.Store().Snapshot()
	if err != nil {
		return
	}
	a.cfgMu.Lock()
	a.cfg = snap
	a.filterEng = filter.NewProfiled(snap)
	a.cfgMu.Unlock()

	// M6.5 (TS-BlockDyn): re-wire the lease origin lookup every time the
	// engine is rebuilt so block_dynamic_clients profiles resolve correctly.
	a.wireLeaseOriginLookup()

	// M7 (TS-ApiToken): keep the in-memory token cache in sync with bbolt.
	a.rebuildTokenCache()

	// Sync auth.Store with the replicated credentials so admin can log into
	// any node with the same password.
	if snap.Auth.Username != "" && snap.Auth.PasswordHash != "" {
		a.authStore.SetHashedCredentials(snap.Auth.Username, snap.Auth.PasswordHash)
	}

	if a.rebuildDNS != nil {
		_ = a.rebuildDNS(snap)
	}

	// M6.5 (TS-LeaseRepl): mirror the replicated lease snapshot and the
	// replicated anomaly set into the local manager so every node serves
	// the same /api/v1/clients and /api/v1/leases responses.
	if a.dhcpMgr != nil {
		if lp, err := a.cluster.CurrentLeaseSnapshot(); err == nil && lp != nil {
			a.dhcpMgr.ApplyReplicatedSnapshot(lp.Leases, lp.PollUnix)
		}
		if anomalies, err := a.cluster.CurrentLeaseAnomalies(); err == nil {
			a.dhcpMgr.ResetReplicatedAnomalies(anomalies)
		}
	}
}

// --- handlers.AppState implementation ---

// GetCfg returns the current snapshot. Callers must not mutate it; the
// returned pointer is shared with concurrent readers.
func (a *App) GetCfg() *config.Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

// WithWriteLock takes a snapshot, runs fn against it, then commits the
// mutated snapshot via Raft as a single ConfigImport command.
//
// The full-snapshot import is heavyweight (rewrites every replicated bucket
// per call) but lets the M2 implementation keep the existing M1 handler API
// surface unchanged. Per-command typed paths are a future optimisation.
func (a *App) WithWriteLock(fn func(*config.Config) error) error {
	snap, err := a.cluster.Store().Snapshot()
	if err != nil {
		return err
	}
	if err := fn(snap); err != nil {
		return err
	}
	return a.cluster.ImportFromM1(*snap)
}

// SaveConfig is a no-op: every write goes through WithWriteLock which
// already committed via Raft. Kept on the interface for handler compatibility.
func (a *App) SaveConfig() error { return nil }

// RebuildFilter is a no-op for the same reason — the filter engine is
// rebuilt automatically by onApply after every committed apply.
func (a *App) RebuildFilter() error { return nil }

// RebuildDNSFromCfg invokes the DNS rebuild callback with the current cached
// config. Used by handlers that change DNS-affecting settings.
func (a *App) RebuildDNSFromCfg() error {
	a.cfgMu.RLock()
	cfg := a.cfg
	fn := a.rebuildDNS
	a.cfgMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(cfg)
}

// GetAuth returns the auth store.
func (a *App) GetAuth() *auth.Store { return a.authStore }

// CreateSession stores a node-local session token issued by POST /api/v1/auth/login.
func (a *App) CreateSession(rawToken, username string) { a.sessions.create(rawToken, username) }

// DeleteSession removes a session token (logout).
func (a *App) DeleteSession(rawToken string) { a.sessions.delete(rawToken) }

// rebuildTokenCache rebuilds the in-memory Bearer token lookup maps from
// bbolt. Called from onApply and NewApp so the cache is always fresh.
func (a *App) rebuildTokenCache() {
	tokens, err := a.cluster.APITokens()
	if err != nil {
		return
	}
	byHash := make(map[string]*cluster.APIToken, len(tokens))
	byID := make(map[string]*cluster.APIToken, len(tokens))
	for i := range tokens {
		t := &tokens[i]
		byHash[t.Hash] = t
		byID[t.ID] = t
	}
	a.tokenMu.Lock()
	a.apiTokensByHash = byHash
	a.apiTokensByID = byID
	a.tokenMu.Unlock()
}

// lookupAPIToken validates a raw Bearer token string and returns the
// corresponding APIToken. The caller must still check IsExpired().
func (a *App) lookupAPIToken(raw string) (*cluster.APIToken, bool) {
	sum := sha256.Sum256([]byte(raw))
	h := hex.EncodeToString(sum[:])
	a.tokenMu.RLock()
	tok, ok := a.apiTokensByHash[h]
	a.tokenMu.RUnlock()
	return tok, ok
}

// LookupAPITokenByID returns a token by its public ID (for handler use).
func (a *App) LookupAPITokenByID(id string) (*cluster.APIToken, bool) {
	a.tokenMu.RLock()
	tok, ok := a.apiTokensByID[id]
	a.tokenMu.RUnlock()
	return tok, ok
}

// GetFilterEng returns the current filter engine.
func (a *App) GetFilterEng() *filter.Engine {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.filterEng
}

// GetQueryLog returns the query log.
func (a *App) GetQueryLog() *dlog.QueryLog { return a.queryLog }

// GetCluster returns the underlying cluster orchestrator. Used by the
// cluster API handlers.
func (a *App) GetCluster() *cluster.Cluster { return a.cluster }

// Dir returns the node's data directory. Used by the legacy export/import
// handlers as a working directory.
func (a *App) Dir() string { return a.cluster.Node().Node.DataDir }

// GetCertStatus returns the current mTLS certificate expiry from the cluster.
// Returns a zero CertsStatus when mTLS is not enabled or cluster is nil.
func (a *App) GetCertStatus() cluster.CertsStatus {
	if a.cluster == nil {
		return cluster.CertsStatus{}
	}
	return a.cluster.CertStatus()
}

// RotateCerts triggers a cluster-wide mTLS certificate rotation via Raft.
func (a *App) RotateCerts(ctx context.Context) error {
	if a.cluster == nil {
		return fmt.Errorf("cluster not enabled")
	}
	return a.cluster.RotateCerts(ctx)
}

// UpdateAuthConfig flushes the local auth.Store state through Raft so every
// node sees the new credentials. Called after first-run setup and password
// change.
func (a *App) UpdateAuthConfig() error {
	exported := a.authStore.Export()
	return a.cluster.SetCredentials(exported.Username, exported.PasswordHash)
}

// ─── M13: Filtering pause accessors ─────────────────────────────────────────

// SetGlobalPause replicates a global pause deadline through the cluster.
func (a *App) SetGlobalPause(resumesAt time.Time, reason string, profileIDs []string) error {
	return a.cluster.SetGlobalPause(resumesAt, reason, profileIDs)
}

// ClearGlobalPause removes the global pause through the cluster.
func (a *App) ClearGlobalPause() error {
	return a.cluster.ClearGlobalPause()
}

// SetProfilePause replicates a per-profile pause deadline through the cluster.
func (a *App) SetProfilePause(id string, resumesAt time.Time, reason string) error {
	return a.cluster.SetProfilePause(id, resumesAt, reason)
}

// ClearProfilePause removes the per-profile pause through the cluster.
func (a *App) ClearProfilePause(id string) error {
	return a.cluster.ClearProfilePause(id)
}

// GetGlobalPause returns the current global pause state, or nil if inactive.
func (a *App) GetGlobalPause() *config.PauseState {
	return a.cluster.GetGlobalPause()
}

// GetProfilePause returns the current pause state for a profile, or nil if inactive.
func (a *App) GetProfilePause(id string) *config.PauseState {
	return a.cluster.GetProfilePause(id)
}

// PauseMaxSeconds returns the configured pause ceiling. Snapshot() handles the default (86400);
// 0 means the feature is explicitly disabled.
func (a *App) PauseMaxSeconds() int {
	cfg := a.GetCfg()
	if cfg == nil {
		return 86400
	}
	return cfg.Filtering.PauseMaxSeconds
}

// ─── M22: webhook management ─────────────────────────────────────────────────

// SetWebhookDispatcher wires the M22 push-alert dispatcher. Called by main.go
// after the dispatcher is constructed so handlers can reach it via
// FireWebhookTest.
func (a *App) SetWebhookDispatcher(d *webhook.Dispatcher) { a.webhookDispatcher = d }

// GetWebhooks returns the current webhook endpoint list. Never nil — returns
// an empty slice when no webhooks are configured.
func (a *App) GetWebhooks() []config.WebhookEndpoint {
	cfg := a.GetCfg()
	if cfg == nil || cfg.Webhooks == nil {
		return []config.WebhookEndpoint{}
	}
	return cfg.Webhooks
}

// UpdateWebhooks replicates a full webhook endpoint list via Raft (when a
// cluster is present) or updates the in-memory config directly (single-node).
func (a *App) UpdateWebhooks(endpoints []config.WebhookEndpoint) error {
	if a.cluster != nil {
		return a.cluster.UpdateWebhooks(endpoints)
	}
	// Single-node fallback: update config in-memory and persist.
	return a.WithWriteLock(func(cfg *config.Config) error {
		cfg.Webhooks = endpoints
		return nil
	})
}

// FireWebhookTest fires a test event directly to the named endpoint.
// Returns an error when the dispatcher is not wired or the endpoint is not found.
func (a *App) FireWebhookTest(endpointID string) error {
	if a.webhookDispatcher == nil {
		return fmt.Errorf("webhook dispatcher not initialised")
	}
	return a.webhookDispatcher.FireTo(endpointID, webhook.EventTest, map[string]string{"endpoint_id": endpointID})
}

// ============================================================================
// HTTP routing
// ============================================================================

// Router returns a chi router with all API routes and middleware registered.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	h := handlers.New(a)

	// Health, first-run setup, and login never require auth.
	r.Get("/api/v1/health", h.Health)
	r.Post("/api/v1/auth/setup", a.forward(h.AuthSetup))
	r.Post("/api/v1/auth/login", h.Login)
	r.Delete("/api/v1/auth/session", h.Logout)

	// M5.1 — Prometheus /metrics. Unauthenticated by default; the
	// metrics package gates on its own RequireAuth callback when
	// node.api.metrics.require_auth is true.
	if a.metrics != nil {
		r.Method(http.MethodGet, "/metrics", a.metrics.Handler())
	}

	// M5.9.5 — public URL tester. Unauthenticated by design; SSRF-guarded
	// + per-IP rate-limited inside the handler. Returns 404 when the
	// operator disabled the landing surface (node.api.public_landing.enabled=false).
	r.Post("/api/v1/_public/test-blocklist", a.publicTesterHandler)

	// M5.9.7 — public domain tester. Same rate-limit bucket as the URL
	// tester (60/h combined). No SSRF surface — the domain is just an
	// in-memory key into the filter engine.
	r.Post("/api/v1/_public/test-domain", a.publicTestDomainHandler)

	// M6 — curated DoH/DoT resolver IP database. Reads are public by
	// design (FS-DohResolverDbReadEndpointPublicOrAuthenticated): the
	// bytes are public provider IPs and the M6 firewall-rule generator
	// renders them on the unauthenticated landing surface. The forced
	// refresh stays admin-only — it triggers an outbound HTTP call.
	r.Get("/api/v1/doh-resolvers", handlers.ListDohResolvers(a))
	r.Get("/api/v1/doh-resolvers/snapshot.json", handlers.SnapshotJSON(a))

	// The /cluster/join endpoint must be served by the leader. Forwarding
	// handles that transparently.
	r.Post("/api/v1/cluster/join", a.forward(h.ClusterJoin))

	// Follower-initiated self-join: the new node calls its own API with the
	// join payload (token + leader_address); the handler calls the leader.
	r.Post("/api/v1/node/join-cluster", h.NodeJoinCluster)

	// M5.3 — pre-Raft mTLS bootstrap. The joining node fetches the cluster
	// CA + a freshly-signed leaf BEFORE it starts its own Raft transport,
	// so the subsequent join's AddVoter can reach a TLS-listening peer.
	// Token is validated but NOT consumed here.
	r.Post("/api/v1/cluster/mtls-bootstrap", a.forward(h.ClusterMTLSBootstrap))

	// Internal cluster-to-cluster channel for follower → leader aggregate
	// forwarding. Authenticated by the replicated cluster secret in the
	// X-Cluster-Secret header — peers do not have admin credentials.
	r.Post("/api/v1/cluster/_internal/aggregates", h.ClusterInternalAggregates)

	// M18 — cluster-internal per-node upgrade trigger. Used by the rolling
	// upgrade goroutine to self-upgrade each peer without WriteForwardMiddleware
	// redirecting the call back to the leader. Authenticated by X-Cluster-Secret.
	r.Post("/api/v1/upgrade/node-start", h.NodeUpgradeStart)

	r.Group(func(r chi.Router) {
		r.Use(a.Auth)
		// M5.2 — audit every authenticated mutating call. Read verbs
		// (GET/HEAD/OPTIONS) are skipped inside the middleware itself.
		r.Use(a.auditMiddleware)
		// M7 — block mutating verbs for read-only Bearer tokens.
		r.Use(a.requireWrite)
		// M10 — forward all mutating requests from followers to the leader so
		// every node is writable without requiring per-route forwarding logic.
		r.Use(WriteForwardMiddleware(a.cluster))

		// M5.2 — audit log read
		r.Get("/api/v1/audit", h.AuditList)

		// Auth
		r.Put("/api/v1/auth/password", a.forward(h.AuthChangePassword))

		// Blocklists — reads served locally; mutations forwarded.
		r.Get("/api/v1/blocklists", h.ListBlocklists)
		r.Post("/api/v1/blocklists", a.forward(h.CreateBlocklist))
		r.Get("/api/v1/blocklists/{id}", h.GetBlocklist)
		r.Patch("/api/v1/blocklists/{id}", a.forward(h.UpdateBlocklist))
		r.Delete("/api/v1/blocklists/{id}", a.forward(h.DeleteBlocklist))
		r.Post("/api/v1/blocklists/{id}/refresh", a.forward(h.RefreshBlocklist))

		// Allowlist
		r.Get("/api/v1/allowlist", h.GetAllowlist)
		r.Post("/api/v1/allowlist", a.forward(h.AddAllowlistEntry))
		r.Delete("/api/v1/allowlist/{domain}", a.forward(h.DeleteAllowlistEntry))

		// Local DNS
		r.Get("/api/v1/local-dns", h.ListLocalDNS)
		r.Post("/api/v1/local-dns", a.forward(h.CreateLocalDNSEntry))
		r.Put("/api/v1/local-dns/{id}", a.forward(h.UpdateLocalDNSEntry))
		r.Delete("/api/v1/local-dns/{id}", a.forward(h.DeleteLocalDNSEntry))

		// Settings
		r.Get("/api/v1/settings", h.GetSettings)
		r.Patch("/api/v1/settings", a.forward(h.UpdateSettings))

		// Query log (per-node read; never forwarded)
		r.Get("/api/v1/query-log", h.GetQueryLog)

		// Config export/import
		r.Get("/api/v1/config/export", h.ExportConfig)
		r.Post("/api/v1/config/import", a.forward(h.ImportConfig))

		// M13 — Filtering pause (global + per-profile)
		ph := handlers.NewFilteringPauseHandlers(a)
		r.Get("/api/v1/filtering/pause", ph.GetGlobalPause)
		r.Post("/api/v1/filtering/pause", a.forward(ph.SetGlobalPause))
		r.Delete("/api/v1/filtering/pause", a.forward(ph.ClearGlobalPause))

		// M3 — Profiles
		r.Get("/api/v1/profiles", h.ListProfiles)
		r.Post("/api/v1/profiles", a.forward(h.CreateProfile))
		r.Get("/api/v1/profiles/{id}", h.GetProfile)
		r.Patch("/api/v1/profiles/{id}", a.forward(h.UpdateProfile))
		r.Delete("/api/v1/profiles/{id}", a.forward(h.DeleteProfile))

		// Per-profile allowlist
		r.Get("/api/v1/profiles/{id}/allowlist", h.GetProfileAllowlist)
		r.Post("/api/v1/profiles/{id}/allowlist", a.forward(h.AddProfileAllowlistEntry))
		r.Delete("/api/v1/profiles/{id}/allowlist/{domain}", a.forward(h.DeleteProfileAllowlistEntry))

		// M13 — Per-profile pause
		r.Get("/api/v1/profiles/{id}/pause", ph.GetProfilePause)
		r.Post("/api/v1/profiles/{id}/pause", a.forward(ph.SetProfilePause))
		r.Delete("/api/v1/profiles/{id}/pause", a.forward(ph.ClearProfilePause))

		// M3 — Schedules
		r.Get("/api/v1/schedules", h.ListSchedules)
		r.Post("/api/v1/schedules", a.forward(h.CreateSchedule))
		r.Get("/api/v1/schedules/{id}", h.GetSchedule)
		r.Patch("/api/v1/schedules/{id}", a.forward(h.UpdateSchedule))
		r.Delete("/api/v1/schedules/{id}", a.forward(h.DeleteSchedule))
		r.Get("/api/v1/schedules/{id}/bindings", h.ListScheduleBindings)
		r.Post("/api/v1/schedules/{id}/bindings", a.forward(h.AddScheduleBinding))
		r.Delete("/api/v1/schedules/{id}/bindings/{profile}/{blocklist}", a.forward(h.DeleteScheduleBinding))

		// M3 — Categories
		r.Get("/api/v1/categories", h.ListCategories)
		r.Get("/api/v1/categories/{name}", h.GetCategory)
		r.Patch("/api/v1/categories/{name}", a.forward(h.UpdateCategory))
		r.Post("/api/v1/categories/{name}/enable", a.forward(h.EnableCategory))
		r.Post("/api/v1/categories/{name}/disable", a.forward(h.DisableCategory))

		// M3.5 — Per-client DoH status (local query-log read; never forwarded)
		r.Get("/api/v1/clients/{ip}/doh-status", h.GetClientDohStatus)

		// M4.7 — DNS cache controls (local node only; never forwarded)
		r.Get("/api/v1/dns/cache/stats", h.GetDNSCacheStats)
		r.Post("/api/v1/dns/cache/purge", h.PurgeDNSCache)

		// M5.6 — in-place upgrade: check is local; start forwards to leader.
		r.Get("/api/v1/upgrade/check", h.UpgradeCheck)
		r.Post("/api/v1/upgrade/start", a.forward(h.UpgradeStart))

		// M18 — rolling cluster upgrade orchestration.
		r.Post("/api/v1/cluster/upgrade/apply", a.forward(h.ClusterUpgradeApply))
		r.Get("/api/v1/cluster/upgrade/status", h.ClusterUpgradeStatus)

		// M5.9.7 — authenticated "would this domain be blocked?" tester.
		// Read-only; doesn't forward to leader (every node has the same
		// replicated filter state). Audit middleware skips it via the
		// auditExempt() prefix check.
		r.Post("/api/v1/test-domain", h.TestDomainAuth)

		// M6 — force a DoH/DoT resolver snapshot refresh. Forwarded to
		// the leader because only the leader's scheduler issues the
		// outbound HTTP fetch.
		r.Post("/api/v1/doh-resolvers/refresh", a.forward(handlers.ForceRefresh(a)))

		// M6 — paste-ready firewall rule generator (TS-FwRule). Read-only;
		// served locally on every node (every node has the replicated
		// resolver snapshot + config cache).
		r.Get("/api/v1/firewall-rules", handlers.GenerateFirewallRules(a))

		// M3.6 — DHCP-enriched client identity + anti-spoof anomalies
		// + reservation export. All node-local reads; never forwarded.
		// M6.5 adds the unparameterised list endpoint that backs the
		// SPA's Clients page badge column (TS-LeaseOrigin).
		r.Get("/api/v1/clients", h.ListClients)
		r.Get("/api/v1/clients/anomalies", h.ListAnomalies)
		// M6.5 (TS-LeaseRepl, FS-LeaseReplFollowerWriteForwarded):
		// anomaly acknowledge is a replicated write; forward to leader.
		r.Post("/api/v1/clients/anomalies/{id}/acknowledge", a.forward(h.AcknowledgeAnomaly))
		r.Get("/api/v1/clients/export-reservations", h.ExportReservations)
		r.Get("/api/v1/clients/_leases", h.LeaseSnapshot) // debug/harness
		r.Get("/api/v1/clients/{ip}", h.GetClient)
		// M6.5 — ARP/NDP cross-check (TS-ArpCheck).
		r.Get("/api/v1/clients/{ip}/arp-state", h.GetClientArpState)

		// M6.5 — Raft-replicated DHCP lease cache (TS-LeaseRepl).
		// Reads are served locally from bbolt on every node; the body
		// carries the current Raft leader id so callers can diagnose
		// failovers without a separate /cluster/status round-trip.
		r.Get("/api/v1/leases", h.GetLeases)
		r.Get("/api/v1/leases/source", h.GetLeasesSource)

		// M22 — webhook endpoint management (TS-Webhooks).
		wh := handlers.NewWebhookHandlers(a)
		r.Get("/api/v1/webhooks", wh.ListWebhooks)
		r.Post("/api/v1/webhooks", a.forward(wh.UpsertWebhook))
		r.Delete("/api/v1/webhooks/{id}", a.forward(wh.DeleteWebhook))
		r.Post("/api/v1/webhooks/{id}/test", wh.TestWebhook)

		// M7 (TS-ApiToken) — bearer token management. Requires cluster:admin scope.
		r.Group(func(r chi.Router) {
			r.Use(a.RequireScope("cluster:admin"))
			tok := handlers.NewTokensAPI(a)
			r.Get("/api/v1/tokens", tok.List)
			r.Post("/api/v1/tokens", a.forward(tok.Create))
			r.Delete("/api/v1/tokens/{id}", a.forward(tok.Delete))
			r.Patch("/api/v1/tokens/{id}", a.forward(tok.Patch))
		})

		// M20 (TS-ClusterSecurityHardening) — mTLS certificate status and rotation.
		r.Group(func(r chi.Router) {
			r.Use(a.RequireScope("cluster:admin"))
			r.Get("/api/v1/cluster/certs/status", h.ClusterCertsStatus)
			r.Post("/api/v1/cluster/certs/rotate", a.forward(h.ClusterCertsRotate))
		})

		// Cluster endpoints — most write paths forwarded.
		r.Post("/api/v1/cluster/tokens", a.forward(h.CreateJoinToken))
		r.Get("/api/v1/cluster/status", h.ClusterStatus)
		r.Get("/api/v1/cluster/self", h.ClusterSelf)
		r.Get("/api/v1/cluster/health", h.ClusterHealth)
		r.Post("/api/v1/cluster/leadership/transfer", a.forward(h.TransferLeadership))
		r.Delete("/api/v1/cluster/nodes/{node_id}", a.forward(h.RemoveNode))
		r.Get("/api/v1/cluster/stats", h.ClusterStats)
		r.Get("/api/v1/cluster/query-log", h.ClusterQueryLog)

	})

	// M4.5 — API documentation browser. Unauthenticated by design: the
	// docs UI is just static assets + a YAML spec the operator already
	// shipped publicly. Try-it-out requests pick up Basic Auth from the
	// browser session. Skip the bundle entirely when api.docs.enabled
	// is explicitly false (operator stripped the docs surface).
	if a.docsEnabled() {
		r.Handle("/api/docs", swaggerui.AssetHandler())
		r.Handle("/api/docs/*", swaggerui.AssetHandler())
		r.Get("/api/openapi.yaml", swaggerui.ServeOpenAPI)
	}

	// Serve the embedded Web UI for everything not matched by /api/v1/*.
	// /assets/* loads built JS/CSS; every other unmatched GET falls back to
	// index.html so the SPA's history router can handle the path. No auth:
	// the SPA renders the login/setup form itself before any API call.
	//
	// M5.9.5 — when the operator disabled the public landing, GET / returns
	// a 302 to /login (preserving the legacy "no landing, login-only"
	// posture) before the SPA shell can paint the public Landing view.
	r.NotFound(a.serveSPAWithLandingGate)

	return r
}

// docsEnabled returns true when the operator has not explicitly turned
// off the API documentation browser. Default is on.
func (a *App) docsEnabled() bool {
	cfg := a.GetCfg()
	if cfg == nil {
		return true
	}
	return !cfg.API.Docs.Disabled
}

// publicTesterHandler gates the M5.9.5 public test endpoint on the
// node-local public_landing toggle and forwards to the rate-limited
// tester. Returns 404 when the operator disabled the landing surface.
func (a *App) publicTesterHandler(w http.ResponseWriter, r *http.Request) {
	if !a.publicLandingEnabled {
		http.NotFound(w, r)
		return
	}
	a.publicTester.Handle(w, r)
}

// publicTestDomainHandler is the M5.9.7 guest entry. Reuses the
// publicTester's per-IP token bucket so the combined budget across
// all public test endpoints stays at 60/h.
func (a *App) publicTestDomainHandler(w http.ResponseWriter, r *http.Request) {
	if !a.publicLandingEnabled {
		http.NotFound(w, r)
		return
	}
	a.publicTester.HandleTestDomain(a, w, r)
}

// serveSPAWithLandingGate wraps serveSPA. When the operator disabled
// the M5.9.5 public landing page, a GET / is redirected to /login so
// the legacy "admin-only, no public surface" posture is preserved
// without re-shipping the SPA. Every other path falls through to the
// regular SPA fallback (login/setup pages are still SPA-rendered).
func (a *App) serveSPAWithLandingGate(w http.ResponseWriter, r *http.Request) {
	if !a.publicLandingEnabled && r.Method == http.MethodGet && r.URL.Path == "/" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	serveSPA(w, r)
}

// serveSPA serves files from the embedded SPA dist FS. Asset paths
// (/assets/* or known static files) hit the FS directly; any other path
// returns index.html so the Vue Router can take over.
func serveSPA(w http.ResponseWriter, r *http.Request) {
	if !static.HasIndex() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	// Decline anything API-shaped — guards against future API paths slipping
	// through if a route is removed.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean == "" {
		clean = "index.html"
	}

	fsys := static.FS()
	if f, err := fsys.Open(clean); err == nil {
		_ = f.Close()
		http.ServeFileFS(w, r, fsys, clean)
		return
	}

	// SPA route: serve index.html so the client-side router renders.
	http.ServeFileFS(w, r, fsys, "index.html")
}

// forward wraps an HTTP handler func with the forward-to-leader middleware.
func (a *App) forward(fn http.HandlerFunc) http.HandlerFunc {
	return apimw.LeaderForward(clusterAdapter{a.cluster}, fn).ServeHTTP
}

// clusterAdapter implements apimw.Cluster on top of *cluster.Cluster without
// pulling the concrete type into the middleware package.
type clusterAdapter struct{ c *cluster.Cluster }

func (a clusterAdapter) IsLeader() bool           { return a.c.IsLeader() }
func (a clusterAdapter) LeaderAPIAddress() string { return a.c.LeaderAPIAddress() }
func (a clusterAdapter) LeaderID() string         { return a.c.LeaderID() }
