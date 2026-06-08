// Package handlers implements the HTTP request handlers for the dblock management API.
// Each handler file covers one resource (blocklists, allowlist, local_dns, settings,
// config, auth). All handlers operate on an AppState interface to decouple them from
// the concrete api.App type and avoid import cycles.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/cluster"
	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/dhcp"
	dnsengine "github.com/dblock/dblock/internal/dns"
	"github.com/dblock/dblock/internal/filter"
	"github.com/dblock/dblock/internal/log"
)

// AppState is the subset of api.App that handlers need.
// It is satisfied by *api.App.
type AppState interface {
	// GetCfg returns the current config pointer (read-only use; caller must not mutate).
	GetCfg() *config.Config

	// WithWriteLock acquires the write lock and calls fn with the live config.
	// fn may mutate the config. The lock is released after fn returns.
	WithWriteLock(fn func(*config.Config) error) error

	// SaveConfig persists the current in-memory config to disk.
	SaveConfig() error

	// RebuildFilter rebuilds the filter engine from the current config.
	RebuildFilter() error

	// RebuildDNSFromCfg calls the DNS rebuild callback with the current config.
	RebuildDNSFromCfg() error

	// UpdateAuthConfig persists the current auth store state into cfg and saves.
	UpdateAuthConfig() error

	GetAuth() *auth.Store
	GetFilterEng() *filter.Engine
	GetQueryLog() *log.QueryLog

	// GetCluster returns the *cluster.Cluster owned by the app, or nil when the
	// server is running in M1 (non-clustered) mode.
	GetCluster() *cluster.Cluster

	// GetDhcpMgr returns the M3.6 DHCP manager, or nil when DHCP
	// integration is disabled on this node.
	GetDhcpMgr() *dhcp.Manager

	// GetDNSCache returns the live DNS cache used by the current DNS
	// handler. Nil when caching is disabled in config.
	GetDNSCache() *dnsengine.Cache

	Dir() string
}

// Handler groups all HTTP handlers and holds a reference to the application state.
type Handler struct {
	app AppState
}

// New creates a Handler for the given app state.
func New(app AppState) *Handler {
	return &Handler{app: app}
}

// writeJSON encodes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON decodes the request body into v. Returns false and writes a 400
// response if decoding fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}
