package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/dblock/dblock/internal/api/handlers"
	apimw "github.com/dblock/dblock/internal/api/middleware"
	"github.com/dblock/dblock/internal/api/static"
	"github.com/dblock/dblock/internal/api/swaggerui"
	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/cluster"
	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/dhcp"
	dnsengine "github.com/dblock/dblock/internal/dns"
	"github.com/dblock/dblock/internal/filter"
	dlog "github.com/dblock/dblock/internal/log"
	"github.com/dblock/dblock/internal/metrics"
	"github.com/dblock/dblock/internal/upgrade"
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

	// rebuildDNS is invoked after every committed FSM apply that may have
	// changed DNS-affecting state (settings, local DNS entries). Set by main.go.
	rebuildDNS func(*config.Config) error

	// cfg + filterEng are caches refreshed on every committed apply via the
	// onApply Subscribe callback. cfgMu guards both.
	cfgMu     sync.RWMutex
	cfg       *config.Config
	filterEng *filter.Engine
}

// SetDhcpManager wires the optional M3.6 DHCP manager in after App
// construction. main.go calls this after the manager is built so the
// API handlers can reach it via app.GetDhcpMgr().
func (a *App) SetDhcpManager(m *dhcp.Manager) { a.dhcpMgr = m }

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
		cluster:    c,
		authStore:  authStore,
		queryLog:   queryLog,
		rebuildDNS: rebuildDNS,
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

	// Sync auth.Store with the replicated credentials so admin can log into
	// any node with the same password.
	if snap.Auth.Username != "" && snap.Auth.PasswordHash != "" {
		a.authStore.SetHashedCredentials(snap.Auth.Username, snap.Auth.PasswordHash)
	}

	if a.rebuildDNS != nil {
		_ = a.rebuildDNS(snap)
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

// UpdateAuthConfig flushes the local auth.Store state through Raft so every
// node sees the new credentials. Called after first-run setup and password
// change.
func (a *App) UpdateAuthConfig() error {
	exported := a.authStore.Export()
	return a.cluster.SetCredentials(exported.Username, exported.PasswordHash)
}

// ============================================================================
// HTTP routing
// ============================================================================

// Router returns a chi router with all API routes and middleware registered.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	h := handlers.New(a)

	// Health and first-run setup never require auth.
	r.Get("/api/v1/health", h.Health)
	r.Post("/api/v1/auth/setup", a.forward(h.AuthSetup))

	// M5.1 — Prometheus /metrics. Unauthenticated by default; the
	// metrics package gates on its own RequireAuth callback when
	// node.api.metrics.require_auth is true.
	if a.metrics != nil {
		r.Method(http.MethodGet, "/metrics", a.metrics.Handler())
	}

	// The /cluster/join endpoint must be served by the leader. Forwarding
	// handles that transparently.
	r.Post("/api/v1/cluster/join", a.forward(h.ClusterJoin))

	// M5.3 — pre-Raft mTLS bootstrap. The joining node fetches the cluster
	// CA + a freshly-signed leaf BEFORE it starts its own Raft transport,
	// so the subsequent join's AddVoter can reach a TLS-listening peer.
	// Token is validated but NOT consumed here.
	r.Post("/api/v1/cluster/mtls-bootstrap", a.forward(h.ClusterMTLSBootstrap))

	// Internal cluster-to-cluster channel for follower → leader aggregate
	// forwarding. Authenticated by the replicated cluster secret in the
	// X-Cluster-Secret header — peers do not have admin credentials.
	r.Post("/api/v1/cluster/_internal/aggregates", h.ClusterInternalAggregates)

	r.Group(func(r chi.Router) {
		r.Use(a.BasicAuth)
		// M5.2 — audit every authenticated mutating call. Read verbs
		// (GET/HEAD/OPTIONS) are skipped inside the middleware itself.
		r.Use(a.auditMiddleware)

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

		// M3 — Profiles
		r.Get("/api/v1/profiles", h.ListProfiles)
		r.Post("/api/v1/profiles", a.forward(h.CreateProfile))
		r.Get("/api/v1/profiles/{id}", h.GetProfile)
		r.Patch("/api/v1/profiles/{id}", a.forward(h.UpdateProfile))
		r.Delete("/api/v1/profiles/{id}", a.forward(h.DeleteProfile))

		// M3 — Schedules
		r.Get("/api/v1/schedules", h.ListSchedules)
		r.Post("/api/v1/schedules", a.forward(h.CreateSchedule))
		r.Get("/api/v1/schedules/{id}", h.GetSchedule)
		r.Patch("/api/v1/schedules/{id}", a.forward(h.UpdateSchedule))
		r.Delete("/api/v1/schedules/{id}", a.forward(h.DeleteSchedule))
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

		// M3.6 — DHCP-enriched client identity + anti-spoof anomalies
		// + reservation export. All node-local reads; never forwarded.
		r.Get("/api/v1/clients/anomalies", h.ListAnomalies)
		r.Post("/api/v1/clients/anomalies/{id}/acknowledge", h.AcknowledgeAnomaly)
		r.Get("/api/v1/clients/export-reservations", h.ExportReservations)
		r.Get("/api/v1/clients/_leases", h.LeaseSnapshot) // debug/harness
		r.Get("/api/v1/clients/{ip}", h.GetClient)

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
	r.NotFound(serveSPA)

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
