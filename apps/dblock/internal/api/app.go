package api

import (
	"net/http"
	"sync"

	"github.com/dblock/dblock/internal/api/handlers"
	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/filter"
	"github.com/dblock/dblock/internal/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// App is the central state holder for the HTTP management API.
// All config mutations go through its methods to ensure thread safety.
type App struct {
	dir        string         // config directory (for Save, Export, Import)
	cfg        *config.Config // current live config
	authStore  *auth.Store
	filterEng  *filter.Engine
	queryLog   *log.QueryLog
	rebuildDNS func(*config.Config) error // called after config changes that affect DNS
	mu         sync.RWMutex              // protects cfg, filterEng (not queryLog — it has its own lock)
}

// NewApp creates an App. rebuildDNS may be nil if no DNS server is managed.
func NewApp(
	dir string,
	cfg *config.Config,
	authStore *auth.Store,
	filterEng *filter.Engine,
	queryLog *log.QueryLog,
	rebuildDNS func(*config.Config) error,
) *App {
	return &App{
		dir:        dir,
		cfg:        cfg,
		authStore:  authStore,
		filterEng:  filterEng,
		queryLog:   queryLog,
		rebuildDNS: rebuildDNS,
	}
}

// --- handlers.AppState implementation ---

// GetCfg returns the current config. The caller must hold the read lock.
func (a *App) GetCfg() *config.Config {
	return a.cfg
}

// WithWriteLock acquires the write lock, calls fn with the current config,
// and releases the lock. fn should mutate *config.Config directly.
func (a *App) WithWriteLock(fn func(*config.Config) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fn(a.cfg)
}

// SaveConfig writes the current config to disk. The write lock must be held
// by the caller (or the caller must ensure no concurrent mutations).
func (a *App) SaveConfig() error {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	return config.Save(a.dir, cfg)
}

// RebuildFilter rebuilds the filter engine from the current config.
// It acquires the write lock.
func (a *App) RebuildFilter() error {
	a.mu.Lock()
	a.filterEng = filter.New(a.cfg.Filtering)
	a.mu.Unlock()
	return nil
}

// RebuildDNSFromCfg calls the registered DNS rebuild callback.
func (a *App) RebuildDNSFromCfg() error {
	a.mu.RLock()
	cfg := a.cfg
	fn := a.rebuildDNS
	a.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(cfg)
}

// GetAuth returns the auth store.
func (a *App) GetAuth() *auth.Store {
	return a.authStore
}

// GetFilterEng returns the current filter engine.
func (a *App) GetFilterEng() *filter.Engine {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.filterEng
}

// GetQueryLog returns the query log.
func (a *App) GetQueryLog() *log.QueryLog {
	return a.queryLog
}

// Dir returns the config directory.
func (a *App) Dir() string {
	return a.dir
}

// UpdateAuthConfig stores the auth config into the main config and persists it.
// This is used after SetPassword / ChangePassword.
func (a *App) UpdateAuthConfig() error {
	exported := a.authStore.Export()
	a.mu.Lock()
	a.cfg.Auth = exported
	a.mu.Unlock()
	return a.SaveConfig()
}

// saveConfig is a convenience method for internal use that saves under write lock.
func (a *App) saveConfig() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return config.Save(a.dir, a.cfg)
}

// Router returns a chi router with all API routes and middleware registered.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	h := handlers.New(a)

	// Health endpoint — no auth required.
	r.Get("/api/v1/health", h.Health)

	// Setup endpoint — always public so first-run credentials can be set.
	r.Post("/api/v1/auth/setup", h.AuthSetup)

	// All other routes require authentication.
	r.Group(func(r chi.Router) {
		r.Use(a.BasicAuth)

		// Auth
		r.Put("/api/v1/auth/password", h.AuthChangePassword)

		// Blocklists
		r.Get("/api/v1/blocklists", h.ListBlocklists)
		r.Post("/api/v1/blocklists", h.CreateBlocklist)
		r.Get("/api/v1/blocklists/{id}", h.GetBlocklist)
		r.Patch("/api/v1/blocklists/{id}", h.UpdateBlocklist)
		r.Delete("/api/v1/blocklists/{id}", h.DeleteBlocklist)
		r.Post("/api/v1/blocklists/{id}/refresh", h.RefreshBlocklist)

		// Allowlist
		r.Get("/api/v1/allowlist", h.GetAllowlist)
		r.Post("/api/v1/allowlist", h.AddAllowlistEntry)
		r.Delete("/api/v1/allowlist/{domain}", h.DeleteAllowlistEntry)

		// Local DNS
		r.Get("/api/v1/local-dns", h.ListLocalDNS)
		r.Post("/api/v1/local-dns", h.CreateLocalDNSEntry)
		r.Put("/api/v1/local-dns/{id}", h.UpdateLocalDNSEntry)
		r.Delete("/api/v1/local-dns/{id}", h.DeleteLocalDNSEntry)

		// Settings
		r.Get("/api/v1/settings", h.GetSettings)
		r.Patch("/api/v1/settings", h.UpdateSettings)

		// Query log
		r.Get("/api/v1/query-log", h.GetQueryLog)

		// Config export/import
		r.Get("/api/v1/config/export", h.ExportConfig)
		r.Post("/api/v1/config/import", h.ImportConfig)

		// Catch-all: web UI placeholder (auth challenge for unauthenticated requests).
		r.Get("/*", http.NotFound)
	})

	return r
}
