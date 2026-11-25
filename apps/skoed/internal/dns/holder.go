package dns

import (
	"errors"
	"fmt"
	"sync"

	"github.com/skoed/skoed/internal/config"
)

// ServerHolder owns the plain UDP/TCP DNS server of a node.
//
// Replicated changes can be applied before the node finishes its startup
// (a joining node replays the Raft log while it starts). A rebuild that
// arrives before Start keeps neither its configuration nor its handler:
// that handler may have been built before the node's services were
// assigned, so Start builds its own, and the DNS port is bound once, by
// Start. All methods are safe for concurrent use.
type ServerHolder struct {
	mu  sync.Mutex
	srv *Server
}

// BootBuilder returns the configuration and handler the server starts
// with. Start calls it with the holder locked, so a rebuild cannot run
// between the build and the start: a change applied earlier is in the
// state it reads, and a later one is applied by Rebuild after the start.
type BootBuilder func() (config.DNSConfig, *Handler, error)

// Start builds the configuration and handler with build, then starts the
// server. It returns the configuration the server listens with.
func (h *ServerHolder) Start(build BootBuilder) (config.DNSConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		return config.DNSConfig{}, errors.New("dns server already started")
	}
	cfg, handler, err := build()
	if err != nil {
		return cfg, fmt.Errorf("build boot dns handler: %w", err)
	}
	srv := New(cfg, handler)
	if err := srv.Start(); err != nil {
		return cfg, err
	}
	h.srv = srv
	return cfg, nil
}

// Rebuild applies a new configuration and handler. Before Start, it does
// nothing. After Start, it swaps the handler, or restarts the listeners
// when the listen configuration changed.
func (h *ServerHolder) Rebuild(cfg config.DNSConfig, handler *Handler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		return nil
	}
	if !h.srv.ListenCfgChanged(cfg) {
		h.srv.UpdateHandler(handler)
		return nil
	}
	h.srv.Shutdown()
	srv := New(cfg, handler)
	if err := srv.Start(); err != nil {
		// Keep the stopped server so the next rebuild compares against
		// its configuration and tries to start again.
		return fmt.Errorf("start rebuilt dns server: %w", err)
	}
	h.srv = srv
	return nil
}

// Shutdown stops the server if it was started.
func (h *ServerHolder) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		h.srv.Shutdown()
	}
}
