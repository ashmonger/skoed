// Acceptance test for a node whose configured Raft address is not
// assigned to any interface when skoed starts.
//
// This test carries the behavior contract in place of a functional
// specification, under the Rule 14 exception recorded in
// decisions/20261127-WaitForUnassignedListenAddress.md. It has no FSID,
// so it does not appear in the traceability matrix.
//
// The behavior it pins:
//
//	TestStartupWaitsForUnassignedAddress
//	    A node whose raft_address is not assigned yet keeps running and
//	    logs that it waits for the address, then gives up after 60 s and
//	    exits non-zero with an error naming the address.
//
// The case where the address is assigned while skoed waits, and startup
// then continues, needs a change to the host's interfaces (root, and a
// network namespace to stay black-box), so it is not covered here. The
// unit tests of the listener retry cover it.
//
// After a reboot of a Proxmox LXC container, systemd started skoed before
// the container's IP was set, although the unit has
// After=network-online.target, and the daemon exited at once:
//
//	start raft: raft transport: listen tcp 10.10.10.21:7000: bind: cannot assign requested address

package acceptance

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// unassignedHost is in TEST-NET-1 (RFC 5737), which no host assigns to an
// interface.
const unassignedHost = "192.0.2.123"

func TestStartupWaitsForUnassignedAddress(t *testing.T) {
	t.Parallel()

	requireUnassigned(t, unassignedHost)

	bin := skoedBinary(t)
	c := &Cluster{t: t, bin: bin}
	start := time.Now()
	cn := c.spawnNode(t, M2NodeConfig{
		NodeID:   "node-1",
		Host:     unassignedHost,
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeRaftPort(t),
	})

	// The node waits for its address instead of exiting.
	waitingLine := fmt.Sprintf("waiting for local address %s to be assigned (listener %s)", unassignedHost, cn.RaftAddr)
	time.Sleep(5 * time.Second)
	if cn.Node.hasExited() {
		t.Fatalf("skoed exited within 5 s instead of waiting for %s%s", unassignedHost, exitReport(cn.Node))
	}
	if out := cn.Node.output.String(); !strings.Contains(out, waitingLine) {
		t.Fatalf("after 5 s, the output does not contain %q:\n%s", waitingLine, out)
	}

	// After 60 s without the address, startup fails.
	select {
	case <-cn.Node.exited:
	case <-time.After(75*time.Second - time.Since(start)):
		t.Fatalf("skoed still running 75 s after start; want an exit after 60 s of waiting:\n%s", cn.Node.output.String())
	}
	elapsed := time.Since(start)
	if code := cn.cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("skoed exited with status 0 after %s; want a non-zero status", elapsed.Round(time.Second))
	}
	if elapsed < 55*time.Second {
		t.Errorf("skoed exited after %s; want about 60 s of waiting", elapsed.Round(time.Second))
	}
	wantErr := fmt.Sprintf("address %s still not assigned after 60s", cn.RaftAddr)
	if out := cn.Node.output.String(); !strings.Contains(out, wantErr) {
		t.Errorf("the output does not contain %q:\n%s", wantErr, out)
	}
}

// requireUnassigned skips the test when host is assigned to an interface
// of the machine running the tests.
func requireUnassigned(t *testing.T, host string) {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("list interface addresses: %v", err)
	}
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.String() == host {
			t.Skipf("%s is assigned to an interface of this host", host)
		}
	}
}
