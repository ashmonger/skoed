package dns

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/skoed/skoed/internal/config"
)

// freePort returns a TCP port that is free on 0.0.0.0 at call time.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func dnsCfg(port int) config.DNSConfig {
	return config.DNSConfig{Listen: config.ListenConfig{Port: port, IPv4: true}}
}

// portBound reports whether port is already bound on 0.0.0.0 over TCP.
func portBound(t *testing.T, port int) bool {
	t.Helper()
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// boot returns a BootBuilder that starts with cfg and handler.
func boot(cfg config.DNSConfig, handler *Handler) BootBuilder {
	return func() (config.DNSConfig, *Handler, error) { return cfg, handler, nil }
}

// currentHandler returns the handler the started server answers with.
func currentHandler(h *ServerHolder) *Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.srv.wrapper.mu.RLock()
	defer h.srv.wrapper.mu.RUnlock()
	handler, _ := h.srv.wrapper.current.(*Handler)
	return handler
}

func TestServerHolderRebuildBeforeStartDoesNotBind(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	port := freePort(t)

	if err := h.Rebuild(dnsCfg(port), nil); err != nil {
		t.Fatalf("rebuild before start: %v", err)
	}
	if portBound(t, port) {
		t.Fatalf("port %d bound by a rebuild before start", port)
	}
	if _, err := h.Start(boot(dnsCfg(port), nil)); err != nil {
		t.Fatalf("start after rebuild: %v", err)
	}
	if !portBound(t, port) {
		t.Fatalf("port %d not bound after start", port)
	}
}

func TestServerHolderStartIgnoresPreBootRebuild(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	rebuildPort, bootPort := freePort(t), freePort(t)
	rebuildHandler, bootHandler := &Handler{}, &Handler{}

	if err := h.Rebuild(dnsCfg(rebuildPort), rebuildHandler); err != nil {
		t.Fatalf("rebuild before start: %v", err)
	}
	used, err := h.Start(boot(dnsCfg(bootPort), bootHandler))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if used.Listen.Port != bootPort {
		t.Fatalf("start used port %d, want %d", used.Listen.Port, bootPort)
	}
	if got := currentHandler(&h); got != bootHandler {
		t.Fatalf("server answers with %p, want the boot handler %p (rebuild handler %p)", got, bootHandler, rebuildHandler)
	}
	if portBound(t, rebuildPort) {
		t.Fatalf("pre-boot rebuild port %d is bound", rebuildPort)
	}
}

func TestServerHolderStartBuildErrorDoesNotBind(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	port := freePort(t)
	failing := func() (config.DNSConfig, *Handler, error) {
		return dnsCfg(port), nil, errors.New("snapshot failed")
	}

	if _, err := h.Start(failing); err == nil {
		t.Fatal("start with a failing builder succeeded, want an error")
	}
	if portBound(t, port) {
		t.Fatalf("port %d bound after a failed build", port)
	}
	if _, err := h.Start(boot(dnsCfg(port), nil)); err != nil {
		t.Fatalf("start after a failed build: %v", err)
	}
}

func TestServerHolderStartTwiceFails(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	port := freePort(t)

	if _, err := h.Start(boot(dnsCfg(port), nil)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := h.Start(boot(dnsCfg(port), nil)); err == nil {
		t.Fatal("second start succeeded, want an error")
	}
}

func TestServerHolderRebuildAfterStartMovesListener(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	oldPort, newPort := freePort(t), freePort(t)
	newHandler := &Handler{}

	if _, err := h.Start(boot(dnsCfg(oldPort), nil)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := h.Rebuild(dnsCfg(oldPort), nil); err != nil {
		t.Fatalf("rebuild with the same listen config: %v", err)
	}
	if err := h.Rebuild(dnsCfg(newPort), newHandler); err != nil {
		t.Fatalf("rebuild with a new port: %v", err)
	}
	if !portBound(t, newPort) {
		t.Fatalf("new port %d not bound after rebuild", newPort)
	}
	if got := currentHandler(&h); got != newHandler {
		t.Fatalf("server answers with %p, want the rebuilt handler %p", got, newHandler)
	}
}
