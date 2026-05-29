package dns

import (
	"fmt"
	"sync"

	"github.com/dblock/dblock/internal/config"
	"github.com/miekg/dns"
)

// swappableHandler wraps a Handler and allows it to be atomically replaced
// without restarting the underlying DNS listeners.
type swappableHandler struct {
	mu      sync.RWMutex
	current dns.Handler
}

func (s *swappableHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	s.mu.RLock()
	h := s.current
	s.mu.RUnlock()
	h.ServeDNS(w, r)
}

func (s *swappableHandler) swap(h dns.Handler) {
	s.mu.Lock()
	s.current = h
	s.mu.Unlock()
}

// Server manages the UDP and TCP DNS listeners.
type Server struct {
	cfg     config.DNSConfig
	wrapper *swappableHandler
	servers []*dns.Server
}

// New creates a Server but does not start any listeners.
func New(cfg config.DNSConfig, handler *Handler) *Server {
	return &Server{
		cfg:     cfg,
		wrapper: &swappableHandler{current: handler},
	}
}

// UpdateHandler replaces the DNS query handler without restarting listeners.
// Safe to call while the server is running.
func (s *Server) UpdateHandler(h *Handler) {
	s.wrapper.swap(h)
}

// ListenCfgChanged reports whether the listen configuration in newCfg differs
// from the current server's configuration (port or address-family flags).
func (s *Server) ListenCfgChanged(newCfg config.DNSConfig) bool {
	return newCfg.Listen.Port != s.cfg.Listen.Port ||
		newCfg.Listen.IPv4 != s.cfg.Listen.IPv4 ||
		newCfg.Listen.IPv6 != s.cfg.Listen.IPv6
}

// Start begins listening on all enabled address families and protocols.
// It blocks until all listeners are ready, then returns. Call Shutdown to stop.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Listen.Port)
	v4addr := fmt.Sprintf("0.0.0.0:%d", s.cfg.Listen.Port)
	v6addr := fmt.Sprintf("[::]:%d", s.cfg.Listen.Port)

	type spec struct {
		net  string
		addr string
	}

	var specs []spec
	if s.cfg.Listen.IPv4 {
		specs = append(specs,
			spec{"udp", v4addr},
			spec{"tcp", v4addr},
		)
	}
	if s.cfg.Listen.IPv6 {
		specs = append(specs,
			spec{"udp6", v6addr},
			spec{"tcp6", v6addr},
		)
	}
	if len(specs) == 0 {
		specs = append(specs,
			spec{"udp", addr},
			spec{"tcp", addr},
		)
	}

	ready := make(chan struct{}, len(specs))
	errCh := make(chan error, len(specs))

	var mu sync.Mutex
	started := make([]*dns.Server, 0, len(specs))

	for _, sp := range specs {
		srv := &dns.Server{
			Addr:    sp.addr,
			Net:     sp.net,
			Handler: s.wrapper,
			NotifyStartedFunc: func() {
				ready <- struct{}{}
			},
		}

		mu.Lock()
		started = append(started, srv)
		mu.Unlock()

		go func(srv *dns.Server) {
			if err := srv.ListenAndServe(); err != nil {
				errCh <- err
			}
		}(srv)
	}

	readyCount := 0
	for readyCount < len(specs) {
		select {
		case err := <-errCh:
			mu.Lock()
			for _, srv := range started {
				_ = srv.Shutdown()
			}
			mu.Unlock()
			return err
		case <-ready:
			readyCount++
		}
	}

	mu.Lock()
	s.servers = started
	mu.Unlock()

	return nil
}

// Shutdown gracefully stops all listeners.
func (s *Server) Shutdown() {
	for _, srv := range s.servers {
		_ = srv.Shutdown()
	}
}
