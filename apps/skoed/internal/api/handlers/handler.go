// Package handlers implements the HTTP request handlers for the skoed management API.
// Each handler file covers one resource (blocklists, allowlist, local_dns, settings,
// config, auth). All handlers operate on an AppState interface to decouple them from
// the concrete api.App type and avoid import cycles.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/skoed/skoed/internal/auth"
	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/dhcp"
	dnsengine "github.com/skoed/skoed/internal/dns"
	"github.com/skoed/skoed/internal/filter"
	"github.com/skoed/skoed/internal/log"
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
	CreateSession(rawToken, username string)
	DeleteSession(rawToken string)
	GetFilterEng() *filter.Engine
	GetQueryLog() *log.QueryLog

	// GetCluster returns the *cluster.Cluster owned by the app, or nil when the
	// server is running in M1 (non-clustered) mode.
	GetCluster() *cluster.Cluster

	// GetDhcpMgr returns the M3.6 DHCP manager, or nil when DHCP
	// integration is disabled on this node.
	GetDhcpMgr() *dhcp.Manager

	// GetDhcpServer returns the M23.5 built-in DHCP server, or nil when
	// the feature is not compiled in.
	GetDhcpServer() *dhcp.Server

	// GetDhcpServer6 returns the M30 built-in DHCPv6 server, or nil when
	// the feature is not wired.
	GetDhcpServer6() *dhcp.Server6

	// GetDNSCache returns the live DNS cache used by the current DNS
	// handler. Nil when caching is disabled in config.
	GetDNSCache() *dnsengine.Cache

	// GetUpgradeChecker returns the M5.6 upgrade-feed cache, or nil
	// when node.upgrade.feed_url is unset.
	GetUpgradeChecker() UpgradeChecker

	// ObserveTestDomain bumps the M5.9.7
	// skoed_test_domain_requests_total{surface,verdict} counter.
	// No-op when metrics aren't wired.
	ObserveTestDomain(surface, verdict string)

	// GetCertStatus returns the mTLS certificate expiry status (M20).
	GetCertStatus() cluster.CertsStatus

	// RotateCerts triggers a cluster-wide mTLS certificate rotation (M20).
	RotateCerts(ctx context.Context) error

	// GetTLSRenewConfig returns the cluster-wide TLS auto-renewal settings (M34).
	GetTLSRenewConfig() (config.TLSRenewConfig, error)

	// SetTLSRenewConfig persists TLS auto-renewal settings cluster-wide via Raft (M34).
	SetTLSRenewConfig(cfg config.TLSRenewConfig) error

	// RotateNodeCert rotates the leaf cert for a single named node via Raft (M34).
	RotateNodeCert(ctx context.Context, nodeID string) error

	Dir() string

	// GetBuildVersion returns the (version, commit) injected at link time.
	GetBuildVersion() (string, string)

	// SetCustomRules replaces the cluster-wide custom filtering rules (M30.5).
	SetCustomRules(rules string) error
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

// urlParam returns the chi URL parameter for key, URL-decoded.
// chi does not automatically decode path parameters, so IDs containing
// special characters like ':' (e.g. "cat:adult" → "cat%3Aadult") would
// not match their stored values without this decoding step.
func urlParam(r *http.Request, key string) string {
	v := chi.URLParam(r, key)
	if decoded, err := url.PathUnescape(v); err == nil {
		return decoded
	}
	return v
}

// maxJSONBody bounds the size of a JSON request body. It protects the
// (network-facing, some pre-auth) API from memory-exhaustion via an
// arbitrarily large body. 4 MiB comfortably covers config imports and bulk
// list operations while rejecting abuse.
const maxJSONBody = 4 << 20

// decodeJSON decodes the request body into v, bounded by maxJSONBody. Returns
// false and writes a 400 response if decoding fails (including "body too large").
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// decodeJSONOptional decodes the request body silently (bounded by maxJSONBody);
// errors are ignored. Use when the body is optional (caller checks zero values).
func decodeJSONOptional(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	return json.NewDecoder(r.Body).Decode(v)
}
