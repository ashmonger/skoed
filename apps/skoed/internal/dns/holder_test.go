package dns

import (
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
	if _, err := h.Start(dnsCfg(port), nil); err != nil {
		t.Fatalf("start after rebuild: %v", err)
	}
	if !portBound(t, port) {
		t.Fatalf("port %d not bound after start", port)
	}
}

func TestServerHolderStartUsesLatestPendingConfig(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	bootPort, firstPort, latestPort := freePort(t), freePort(t), freePort(t)

	if err := h.Rebuild(dnsCfg(firstPort), nil); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	if err := h.Rebuild(dnsCfg(latestPort), nil); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	used, err := h.Start(dnsCfg(bootPort), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if used.Listen.Port != latestPort {
		t.Fatalf("start used port %d, want %d", used.Listen.Port, latestPort)
	}
	if !portBound(t, latestPort) {
		t.Fatalf("latest port %d not bound", latestPort)
	}
	if portBound(t, bootPort) || portBound(t, firstPort) {
		t.Fatalf("an older port is bound (boot %d, first %d)", bootPort, firstPort)
	}
}

func TestServerHolderStartTwiceFails(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	port := freePort(t)

	if _, err := h.Start(dnsCfg(port), nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := h.Start(dnsCfg(port), nil); err == nil {
		t.Fatal("second start succeeded, want an error")
	}
}

func TestServerHolderRebuildAfterStartMovesListener(t *testing.T) {
	var h ServerHolder
	defer h.Shutdown()
	oldPort, newPort := freePort(t), freePort(t)

	if _, err := h.Start(dnsCfg(oldPort), nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := h.Rebuild(dnsCfg(oldPort), nil); err != nil {
		t.Fatalf("rebuild with the same listen config: %v", err)
	}
	if err := h.Rebuild(dnsCfg(newPort), nil); err != nil {
		t.Fatalf("rebuild with a new port: %v", err)
	}
	if !portBound(t, newPort) {
		t.Fatalf("new port %d not bound after rebuild", newPort)
	}
}
