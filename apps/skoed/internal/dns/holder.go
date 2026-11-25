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
// arrives before Start only records the latest configuration and handler,
// so the DNS port is bound once, by Start. All methods are safe for
// concurrent use.
type ServerHolder struct {
	mu             sync.Mutex
	srv            *Server
	pendingCfg     *config.DNSConfig
	pendingHandler *Handler
}

// Start builds and starts the server. If Rebuild was called before, the
// configuration and handler it recorded are used instead of cfg and
// handler. It returns the configuration the server listens with.
func (h *ServerHolder) Start(cfg config.DNSConfig, handler *Handler) (config.DNSConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		return cfg, errors.New("dns server already started")
	}
	if h.pendingCfg != nil {
		cfg, handler = *h.pendingCfg, h.pendingHandler
		h.pendingCfg, h.pendingHandler = nil, nil
	}
	srv := New(cfg, handler)
	if err := srv.Start(); err != nil {
		return cfg, err
	}
	h.srv = srv
	return cfg, nil
}

// Rebuild applies a new configuration and handler. Before Start, it only
// records them for Start to use. After Start, it swaps the handler, or
// restarts the listeners when the listen configuration changed.
func (h *ServerHolder) Rebuild(cfg config.DNSConfig, handler *Handler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		h.pendingCfg, h.pendingHandler = &cfg, handler
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
