package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server wraps net/http.Server and binds it to the App's router. M4.6
// adds optional HTTPS — when TLSConfig is non-nil, the listener serves
// TLS. In single-port mode the same socket also detects plain HTTP
// requests on the first byte and answers with a 308 redirect to https://.
// In dual-port mode the plain HTTP server stays on Addr and a second
// HTTPS server is bound to HTTPSAddress.
type Server struct {
	httpServer  *http.Server
	httpsServer *http.Server // dual_port only
	tlsCfg      *tls.Config
	mode        string
	hsts        bool
}

// TLSOptions bundles the M4.6 knobs the boot wires.
type TLSOptions struct {
	Enabled      bool
	Mode         string // "single_port" | "dual_port"
	HTTPSAddress string // dual_port only
	HSTS         bool
	CertFile     string
	KeyFile      string
}

// NewServer creates a Server that listens on the node's API address from
// node.yaml. The API address is node-local and never replicated.
func NewServer(app *App) *Server {
	return NewServerWithTLS(app, TLSOptions{})
}

// NewServerWithTLS is NewServer + optional HTTPS config. When opts.Enabled
// is false the result is identical to NewServer.
func NewServerWithTLS(app *App, opts TLSOptions) *Server {
	addr := app.GetCluster().Node().Node.APIAddress
	handler := app.Router()

	s := &Server{
		mode: opts.Mode,
		hsts: opts.HSTS,
	}

	if opts.Enabled {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err == nil {
			s.tlsCfg = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
				NextProtos:   []string{"h2", "http/1.1"},
			}
		}
		// hstsHandler wraps the router so HTTPS responses can carry the
		// header; it's a no-op when hsts == false.
		handler = s.maybeHSTS(handler)
	}

	// ReadTimeout bounds the time to read the full request (headers + body),
	// preventing slow-body / large-body abuse. WriteTimeout is intentionally
	// unset so long-lived SSE streams (query-log, upgrade log) are not cut off.
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	if opts.Enabled && opts.Mode == "dual_port" && opts.HTTPSAddress != "" {
		s.httpsServer = &http.Server{
			Addr:              opts.HTTPSAddress,
			Handler:           handler,
			TLSConfig:         s.tlsCfg,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
		}
	}
	return s
}

// maybeHSTS adds Strict-Transport-Security to TLS responses when
// configured. Plain-HTTP responses (single_port redirect, dual_port
// HTTP listener) never get it.
func (s *Server) maybeHSTS(next http.Handler) http.Handler {
	if !s.hsts {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=86400; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTP/HTTPS server(s) in background goroutines.
// In single-port HTTPS mode the listener is wrapped with a sniffing
// adapter that 308-redirects plaintext HTTP.
func (s *Server) Start() error {
	errCh := make(chan error, 2)

	// Primary listener on Addr.
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("api server: listen %s: %w", s.httpServer.Addr, err)
	}

	if s.tlsCfg != nil && s.mode != "dual_port" {
		// single_port: peek at the first byte to detect TLS vs plain HTTP.
		ln = &tlsOrRedirectListener{Listener: ln, tlsCfg: s.tlsCfg}
	}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Optional secondary HTTPS listener (dual_port).
	if s.httpsServer != nil {
		go func() {
			if err := s.httpsServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	// Fail-fast window.
	select {
	case err := <-errCh:
		return fmt.Errorf("api server: %w", err)
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// Shutdown gracefully stops both servers.
func (s *Server) Shutdown(ctx context.Context) error {
	var first error
	if err := s.httpServer.Shutdown(ctx); err != nil {
		first = err
	}
	if s.httpsServer != nil {
		if err := s.httpsServer.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ─── single_port TLS-or-redirect listener ───────────────────────────────

// tlsOrRedirectListener wraps a net.Listener. Each accepted connection
// is peeked at: if the first byte is 0x16 (TLS handshake ClientHello),
// the conn is wrapped in tls.Server; otherwise it's handled by a tiny
// HTTP redirector that returns 308 → https:// for any request.
type tlsOrRedirectListener struct {
	net.Listener
	tlsCfg *tls.Config

	mu          sync.Mutex
	redirectCh  chan net.Conn // never closed; redirect goroutine pulls from here
	redirectOnce sync.Once
}

func (l *tlsOrRedirectListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		bc := newPeekableConn(c)
		first, err := bc.Peek(1)
		if err != nil {
			c.Close()
			continue
		}
		if first[0] == 0x16 {
			// TLS handshake — return a tls.Conn so http.Server treats it as HTTPS.
			return tls.Server(bc, l.tlsCfg), nil
		}
		// Plain HTTP — serve a 308 redirect inline and close. We don't
		// return it to http.Server (which would try TLS handshake first).
		go serveRedirect(bc)
	}
}

// peekableConn wraps a net.Conn with a bufio.Reader so we can Peek the
// first byte without consuming it.
type peekableConn struct {
	net.Conn
	br *bufio.Reader
}

func newPeekableConn(c net.Conn) *peekableConn {
	return &peekableConn{Conn: c, br: bufio.NewReader(c)}
}
func (p *peekableConn) Peek(n int) ([]byte, error) { return p.br.Peek(n) }
func (p *peekableConn) Read(b []byte) (int, error) { return p.br.Read(b) }

// serveRedirect reads one HTTP request from c (best-effort), writes a
// 308 redirect with a Location header that swaps http → https on the
// same host:port, and closes the connection.
func serveRedirect(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		return
	}
	host := req.Host
	if host == "" {
		host = c.LocalAddr().String()
	}
	path := req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	target := "https://" + host + path

	body := "Redirecting to " + target + "\r\n"
	resp := strings.Join([]string{
		"HTTP/1.1 308 Permanent Redirect",
		"Location: " + target,
		"Content-Type: text/plain; charset=utf-8",
		"Content-Length: " + fmt.Sprintf("%d", len(body)),
		"Connection: close",
		"", body,
	}, "\r\n")
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte(resp))
}
