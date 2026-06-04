package dns

// M4: DoH (RFC 8484) and DoT (RFC 7858) listeners. Both transports flow
// queries through the *same* dns.Handler that serves UDP/TCP, so the
// filter, allowlist, local-DNS, and query-log pipeline applies uniformly.
// Outcomes get a -doh / -dot suffix via the transportTaggedWriter below.

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// transportTag is the context-key-ish marker on a wrapped dns.ResponseWriter
// that lets the query-log handler downstream know which transport delivered
// the message ("doh" / "dot"). UDP/TCP queries use the bare writer and the
// handler reads transport as "".
type transportTaggedWriter struct {
	dns.ResponseWriter
	transport string
	clientIP  string // overrides w.RemoteAddr() — required because DoH uses HTTP, not a UDP/TCP DNS conn
}

// Transport returns the transport tag (e.g. "doh", "dot"). Callers should
// type-assert dns.ResponseWriter to *transportTaggedWriter to read it.
func (w *transportTaggedWriter) Transport() string { return w.transport }

// RemoteAddr overrides the embedded writer when the wrapper carries an
// explicit clientIP — used by DoH so the engine sees the HTTP peer, not
// the synthetic udpStub address.
func (w *transportTaggedWriter) RemoteAddr() net.Addr {
	if w.clientIP != "" {
		if ip, err := netip.ParseAddr(w.clientIP); err == nil {
			return &net.UDPAddr{IP: ip.AsSlice(), Port: 0}
		}
	}
	return w.ResponseWriter.RemoteAddr()
}

// EncryptedServer manages the DoH and DoT listeners. Both share a single
// TLS cert and a single dns.Handler. Start blocks until the listeners are
// bound or until any of them fails to bind.
type EncryptedServer struct {
	handler dns.Handler
	dohPort int
	dotPort int
	cert    tls.Certificate

	httpSrv *http.Server
	dotLn   net.Listener
	mu      sync.Mutex
	stopped bool
}

// NewEncryptedServer builds a server but doesn't bind anything yet.
// Either dohPort or dotPort can be zero to disable that transport.
func NewEncryptedServer(handler dns.Handler, dohPort, dotPort int, certFile, keyFile string) (*EncryptedServer, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}
	return &EncryptedServer{
		handler: handler,
		dohPort: dohPort,
		dotPort: dotPort,
		cert:    cert,
	}, nil
}

// Start opens the configured listeners. Returns an error if any listener
// fails to bind. Already-disabled transports (port == 0) are skipped.
func (s *EncryptedServer) Start() error {
	if s.dohPort > 0 {
		if err := s.startDoH(); err != nil {
			return fmt.Errorf("start DoH: %w", err)
		}
	}
	if s.dotPort > 0 {
		if err := s.startDoT(); err != nil {
			return fmt.Errorf("start DoT: %w", err)
		}
	}
	return nil
}

// Shutdown stops both listeners. Safe to call multiple times.
func (s *EncryptedServer) Shutdown() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	httpSrv := s.httpSrv
	dotLn := s.dotLn
	s.mu.Unlock()

	if httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}
	if dotLn != nil {
		_ = dotLn.Close()
	}
}

// ─── DoH ─────────────────────────────────────────────────────────────────

func (s *EncryptedServer) startDoH() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/dns-query", s.handleDoH)

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.dohPort),
		Handler:           mux,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := tls.Listen("tcp", srv.Addr, tlsCfg)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

func (s *EncryptedServer) handleDoH(w http.ResponseWriter, r *http.Request) {
	var wireQuery []byte
	switch r.Method {
	case http.MethodPost:
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "expected Content-Type: application/dns-message", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 65535))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		wireQuery = body
	case http.MethodGet:
		enc := r.URL.Query().Get("dns")
		if enc == "" {
			http.Error(w, "missing 'dns' query param", http.StatusBadRequest)
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(enc)
		if err != nil {
			http.Error(w, "invalid base64url: "+err.Error(), http.StatusBadRequest)
			return
		}
		wireQuery = decoded
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(wireQuery); err != nil {
		http.Error(w, "malformed DNS message", http.StatusBadRequest)
		return
	}

	tw := &dohResponseWriter{
		transport: "doh",
		clientIP:  remoteHostNoPort(r.RemoteAddr),
		done:      make(chan struct{}, 1),
	}
	s.handler.ServeDNS(tw, msg)

	// Wait briefly for the handler to write — it MUST be synchronous, but
	// we add a safety timeout in case a future change introduces async.
	select {
	case <-tw.done:
	case <-time.After(5 * time.Second):
		http.Error(w, "DNS handler timeout", http.StatusGatewayTimeout)
		return
	}

	resp := tw.responseWire
	if resp == nil {
		http.Error(w, "DNS handler did not produce a response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/dns-message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// dohResponseWriter is a dns.ResponseWriter that captures the wire-format
// response into memory so the HTTP handler can send it back as the body.
type dohResponseWriter struct {
	transport    string
	clientIP     string
	responseWire []byte
	done         chan struct{}
}

func (d *dohResponseWriter) WriteMsg(m *dns.Msg) error {
	out, err := m.Pack()
	if err != nil {
		return err
	}
	d.responseWire = out
	select {
	case d.done <- struct{}{}:
	default:
	}
	return nil
}

func (d *dohResponseWriter) Write(b []byte) (int, error) {
	d.responseWire = append([]byte(nil), b...)
	select {
	case d.done <- struct{}{}:
	default:
	}
	return len(b), nil
}

// LocalAddr / RemoteAddr / etc. — implement enough of dns.ResponseWriter
// so the engine's logging picks up the client IP.
func (d *dohResponseWriter) LocalAddr() net.Addr { return &net.IPAddr{IP: net.IPv4zero} }
func (d *dohResponseWriter) RemoteAddr() net.Addr {
	if ip := net.ParseIP(d.clientIP); ip != nil {
		return &net.UDPAddr{IP: ip, Port: 0}
	}
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}
func (d *dohResponseWriter) Close() error              { return nil }
func (d *dohResponseWriter) TsigStatus() error         { return nil }
func (d *dohResponseWriter) TsigTimersOnly(bool)       {}
func (d *dohResponseWriter) Hijack()                   {}
func (d *dohResponseWriter) Transport() string         { return d.transport }
func (d *dohResponseWriter) Network() string           { return "tcp" }

// remoteHostNoPort returns the host portion of "host:port", or the input
// unchanged if it can't be split.
func remoteHostNoPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// ─── DoT ─────────────────────────────────────────────────────────────────

func (s *EncryptedServer) startDoT() error {
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
	}
	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", s.dotPort), tlsCfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.dotLn = ln
	s.mu.Unlock()

	go s.acceptDoT(ln)
	return nil
}

func (s *EncryptedServer) acceptDoT(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Transient errors: pause briefly to avoid a hot loop.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		go s.handleDoTConn(conn)
	}
}

func (s *EncryptedServer) handleDoTConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	// RFC 7858: 2-byte length prefix + DNS wire message. One TLS
	// connection can carry multiple back-to-back queries (RFC 7766).
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		l := int(hdr[0])<<8 | int(hdr[1])
		if l <= 0 || l > 65535 {
			return
		}
		body := make([]byte, l)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}

		msg := new(dns.Msg)
		if err := msg.Unpack(body); err != nil {
			return
		}

		// Build a sync writer that frames the response.
		clientIP := remoteHostNoPort(conn.RemoteAddr().String())
		dw := &dotResponseWriter{
			conn:      conn,
			transport: "dot",
			clientIP:  clientIP,
		}
		s.handler.ServeDNS(dw, msg)
		if dw.writeErr != nil {
			return
		}
		// Reset deadline for the next query on this connection.
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
}

// dotResponseWriter writes RFC 7858-framed responses back over the TLS
// connection. Synchronous — WriteMsg returns after the framed bytes are
// flushed.
type dotResponseWriter struct {
	conn      net.Conn
	transport string
	clientIP  string
	writeErr  error
}

func (d *dotResponseWriter) WriteMsg(m *dns.Msg) error {
	out, err := m.Pack()
	if err != nil {
		d.writeErr = err
		return err
	}
	hdr := []byte{byte(len(out) >> 8), byte(len(out))}
	if _, err := d.conn.Write(append(hdr, out...)); err != nil {
		d.writeErr = err
		return err
	}
	return nil
}

func (d *dotResponseWriter) Write(b []byte) (int, error) {
	hdr := []byte{byte(len(b) >> 8), byte(len(b))}
	if _, err := d.conn.Write(append(hdr, b...)); err != nil {
		d.writeErr = err
		return 0, err
	}
	return len(b), nil
}

func (d *dotResponseWriter) LocalAddr() net.Addr { return d.conn.LocalAddr() }
func (d *dotResponseWriter) RemoteAddr() net.Addr {
	// Honour the client IP we parsed at conn-accept time so logs match.
	if ip := net.ParseIP(d.clientIP); ip != nil {
		return &net.TCPAddr{IP: ip, Port: 0}
	}
	return d.conn.RemoteAddr()
}
func (d *dotResponseWriter) Close() error        { return d.conn.Close() }
func (d *dotResponseWriter) TsigStatus() error   { return nil }
func (d *dotResponseWriter) TsigTimersOnly(bool) {}
func (d *dotResponseWriter) Hijack()             {}
func (d *dotResponseWriter) Transport() string   { return d.transport }
func (d *dotResponseWriter) Network() string     { return "tcp" }

// transportFromWriter returns "doh", "dot", or "" depending on the type
// of dns.ResponseWriter the handler received. Plain UDP/TCP returns "".
// Used by the DNS handler to suffix query-log outcomes.
func transportFromWriter(w dns.ResponseWriter) string {
	switch v := w.(type) {
	case *dohResponseWriter:
		return v.transport
	case *dotResponseWriter:
		return v.transport
	case *transportTaggedWriter:
		return v.transport
	}
	return ""
}
