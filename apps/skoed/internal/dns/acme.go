package dns

// M4 ACME / Let's Encrypt integration for the DoH and DoT listeners.
//
// Strategy: wrap golang.org/x/crypto/acme/autocert.Manager. autocert
// handles directory discovery, account registration, ordering, HTTP-01
// challenge serving, cert caching, and lazy renewal. skoed provides:
//
//   - The disk cache directory (under <data_dir>/tls/acme-cache/)
//   - The HTTP-01 challenge HTTP server (port from config)
//   - A GetCertificate function that EncryptedServer plugs into its
//     tls.Config.GetCertificate
//   - A self-signed fallback when ACME hasn't yet issued a cert (so
//     the node still accepts DoH/DoT during the very first boot).

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// AcmeConfig is the subset of the per-node ACME settings the dns package
// needs. Decoupled from cluster.AcmeSection so callers don't drag the
// whole cluster package in.
type AcmeConfig struct {
	Enabled           bool
	Email             string
	Domains           []string
	DirectoryURL      string
	HTTPChallengePort int
	CacheDir          string
	// FallbackCertFile / FallbackKeyFile are the self-signed cert paths
	// used as a fallback while ACME hasn't yet issued the first cert
	// (or when ACME is unreachable). EncryptedServer's
	// EnsureSelfSignedCert generates these.
	FallbackCertFile string
	FallbackKeyFile  string
}

// AcmeManager wraps autocert.Manager + the HTTP-01 challenge server +
// the self-signed fallback. Call Start to begin serving challenges; the
// returned GetCertificate is what plugs into tls.Config.
type AcmeManager struct {
	cfg         AcmeConfig
	mgr         *autocert.Manager
	httpServer  *http.Server
	fallback    *tls.Certificate
	httpAddr    string
}

// NewAcmeManager builds the manager but doesn't start listeners.
// Returns a useful error if Domains is empty.
func NewAcmeManager(cfg AcmeConfig) (*AcmeManager, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("acme not enabled")
	}
	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("acme.domains must list at least one FQDN")
	}
	if cfg.CacheDir == "" {
		return nil, fmt.Errorf("acme.cache_dir is required")
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create acme cache dir: %w", err)
	}

	m := &autocert.Manager{
		Cache:      autocert.DirCache(cfg.CacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domains...),
		Email:      cfg.Email,
	}
	if cfg.DirectoryURL != "" {
		m.Client = &acme.Client{DirectoryURL: cfg.DirectoryURL}
	}

	a := &AcmeManager{cfg: cfg, mgr: m}

	// Load the self-signed fallback so GetCertificate always has
	// something to return even if autocert hasn't issued yet.
	if cfg.FallbackCertFile != "" && cfg.FallbackKeyFile != "" {
		c, err := tls.LoadX509KeyPair(cfg.FallbackCertFile, cfg.FallbackKeyFile)
		if err == nil {
			a.fallback = &c
		}
	}

	return a, nil
}

// Start binds the HTTP-01 challenge listener. Returns an error only if
// the listener can't bind; the ACME flow itself is lazy (it runs on the
// first GetCertificate call that misses the cache).
func (a *AcmeManager) Start() error {
	port := a.cfg.HTTPChallengePort
	if port == 0 {
		port = 80
	}
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen acme http-01 :%d: %w", port, err)
	}
	a.httpAddr = ln.Addr().String()

	// autocert.Manager.HTTPHandler returns a handler that serves
	// /.well-known/acme-challenge/* and 404s everything else. The arg
	// is a fallback for non-challenge paths — we use http.NotFoundHandler.
	srv := &http.Server{
		Handler:           a.mgr.HTTPHandler(http.NotFoundHandler()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.httpServer = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// Addr returns the bound address of the HTTP-01 listener (host:port).
// Useful for tests that need to know which port was auto-picked when
// HTTPChallengePort is 0 in config (the production code uses 80 by
// default — autopick is a test-harness convenience).
func (a *AcmeManager) Addr() string { return a.httpAddr }

// Shutdown stops the HTTP-01 server. Safe to call multiple times.
func (a *AcmeManager) Shutdown() {
	if a.httpServer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = a.httpServer.Shutdown(ctx)
	a.httpServer = nil
}

// GetCertificate is the function EncryptedServer plugs into its
// tls.Config.GetCertificate. Tries autocert first; on any failure
// (unreachable directory, rate-limited, host policy reject, etc.)
// returns the self-signed fallback so the listener still serves
// something. Errors are logged but not surfaced to clients.
func (a *AcmeManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c, err := a.mgr.GetCertificate(hello)
	if err == nil {
		return c, nil
	}
	log.Printf("acme: GetCertificate(%q) failed, using self-signed fallback: %v",
		hello.ServerName, err)
	if a.fallback != nil {
		return a.fallback, nil
	}
	return nil, err
}
