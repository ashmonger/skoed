package dns

// M8: DNSCrypt v2 server. Wraps github.com/ameshkov/dnscrypt/v2 and bridges
// it to the miekg/dns.Handler pipeline so all the same filter, allowlist,
// local-DNS, and query-log paths that serve UDP/TCP/DoH/DoT apply here too.
// Outcomes are tagged with transport "dnscrypt".

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	dnscrypt "github.com/ameshkov/dnscrypt/v2"
	mdns "github.com/miekg/dns"
)

// DNSCryptServer wraps an ameshkov/dnscrypt Server and exposes the
// same Start/Shutdown/UpdateHandler API as EncryptedServer.
type DNSCryptServer struct {
	port    int
	mu      sync.Mutex
	wrapper *swappableHandler
	srv     *dnscrypt.Server
	udpConn *net.UDPConn
	stopped bool
}

// NewDNSCryptServer constructs a DNSCryptServer from a serialised
// dnscrypt.ResolverConfig (the JSON stored in the cluster bbolt bucket).
//
// configJSON is the cluster.DNSCryptKeys.Config field — JSON-marshalled
// dnscrypt.ResolverConfig (PascalCase field names). port is the UDP port.
func NewDNSCryptServer(handler mdns.Handler, port int, configJSON string) (*DNSCryptServer, error) {
	var rc dnscrypt.ResolverConfig
	if err := json.Unmarshal([]byte(configJSON), &rc); err != nil {
		return nil, fmt.Errorf("parse DNSCrypt resolver config: %w", err)
	}

	cert, err := rc.CreateCert()
	if err != nil {
		return nil, fmt.Errorf("create DNSCrypt cert: %w", err)
	}

	wrapper := &swappableHandler{current: handler}
	srv := &dnscrypt.Server{
		ProviderName: rc.ProviderName,
		ResolverCert: cert,
		Handler:      &dnscryptHandlerBridge{wrapper: wrapper},
	}

	return &DNSCryptServer{
		port:    port,
		wrapper: wrapper,
		srv:     srv,
	}, nil
}

// Start opens the UDP listener. Returns an error if binding fails.
func (d *DNSCryptServer) Start() error {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: d.port})
	if err != nil {
		return fmt.Errorf("bind DNSCrypt UDP :%d: %w", d.port, err)
	}
	d.mu.Lock()
	d.udpConn = conn
	d.mu.Unlock()
	go func() {
		// ServeUDP blocks until Shutdown closes the conn.
		_ = d.srv.ServeUDP(conn)
	}()
	return nil
}

// Shutdown stops the DNSCrypt listener gracefully.
func (d *DNSCryptServer) Shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = d.srv.Shutdown(ctx)
}

// UpdateHandler atomically swaps the dns.Handler. Called on every Raft apply
// just like EncryptedServer.UpdateHandler.
func (d *DNSCryptServer) UpdateHandler(h mdns.Handler) {
	d.wrapper.swap(h)
}

// ─── Stamp ────────────────────────────────────────────────────────────────

// DNSCryptStamp generates the sdns:// URI for this server given its
// advertised address (e.g. "1.2.3.4:5443"). Returns "" on error.
func DNSCryptStamp(configJSON, addr string) string {
	var rc dnscrypt.ResolverConfig
	if err := json.Unmarshal([]byte(configJSON), &rc); err != nil {
		return ""
	}
	stamp, err := rc.CreateStamp(addr)
	if err != nil {
		return ""
	}
	return stamp.String()
}

// ─── Bridge ──────────────────────────────────────────────────────────────

// dnscryptHandlerBridge implements dnscrypt.Handler — called by the ameshkov
// server for each decrypted query — and routes through our miekg/dns pipeline.
type dnscryptHandlerBridge struct {
	wrapper *swappableHandler
}

func (b *dnscryptHandlerBridge) ServeDNS(rw dnscrypt.ResponseWriter, r *mdns.Msg) error {
	b.wrapper.ServeDNS(&dnscryptResponseWriter{rw: rw}, r)
	return nil
}

// dnscryptResponseWriter wraps dnscrypt.ResponseWriter to satisfy the
// miekg/dns.ResponseWriter interface so our existing handler pipeline can
// write answers without knowing about the encrypted transport.
type dnscryptResponseWriter struct {
	rw dnscrypt.ResponseWriter
}

func (w *dnscryptResponseWriter) WriteMsg(m *mdns.Msg) error { return w.rw.WriteMsg(m) }
func (w *dnscryptResponseWriter) Write(b []byte) (int, error) {
	// Unpack so the dnscrypt layer can re-encrypt properly.
	m := new(mdns.Msg)
	if err := m.Unpack(b); err != nil {
		return 0, err
	}
	return len(b), w.rw.WriteMsg(m)
}
func (w *dnscryptResponseWriter) LocalAddr() net.Addr  { return w.rw.LocalAddr() }
func (w *dnscryptResponseWriter) RemoteAddr() net.Addr { return w.rw.RemoteAddr() }
func (w *dnscryptResponseWriter) Close() error         { return nil }
func (w *dnscryptResponseWriter) TsigStatus() error    { return nil }
func (w *dnscryptResponseWriter) TsigTimersOnly(bool)  {}
func (w *dnscryptResponseWriter) Hijack()              {}
func (w *dnscryptResponseWriter) Transport() string    { return "dnscrypt" }
func (w *dnscryptResponseWriter) Network() string      { return "udp" }
