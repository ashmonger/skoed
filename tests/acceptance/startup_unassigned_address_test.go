// Acceptance test for a node whose configured Raft address is not
// assigned to any interface when skoed starts.
//
// This test carries the behavior contract in place of a functional
// specification, under the Rule 14 exception recorded in
// decisions/20261127-WaitForUnassignedListenAddress.md. It has no FSID
// until the retroactive functional spec is written, so it does not
// appear in the traceability matrix.
//
// The behavior it pins, for the plain and the mTLS Raft transport:
//
//	TestStartupWaitsForUnassignedAddress
//	    A node whose raft_address is not assigned yet keeps running and
//	    logs once that it waits for the address, then gives up after
//	    60 s and exits non-zero with an error naming the address.
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
//	tls listen 10.10.10.14:7000: bind: cannot assign requested address

package acceptance

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// unassignedHost is in TEST-NET-1 (RFC 5737), which no host assigns to an
// interface.
const unassignedHost = "192.0.2.123"

const (
	// waitingLineTimeout bounds the wait for the "waiting for local
	// address" line. It is counted from the spawn, so it includes the
	// process start-up, which is slow on a loaded CI runner.
	waitingLineTimeout = 30 * time.Second
	// The contract gives up after 60 s of waiting. The window around it
	// is counted from the moment the waiting line is seen, which comes
	// after the first failed bind, so the exit can come a little before
	// 60 s; the upper bound leaves room for a loaded runner.
	minWaitBeforeExit = 55 * time.Second
	maxWaitBeforeExit = 75 * time.Second
)

func TestStartupWaitsForUnassignedAddress(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mtls bool
	}{
		{name: "plain", mtls: false},
		{name: "mtls", mtls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testStartupWaitsForUnassignedAddress(t, tc.mtls)
		})
	}
}

func testStartupWaitsForUnassignedAddress(t *testing.T, mtls bool) {
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}
	requireUnassigned(t, unassignedHost)

	c := &Cluster{t: t, bin: bin, mtls: mtls}
	cn := c.spawnNode(t, M2NodeConfig{
		NodeID:             "node-1",
		Host:               unassignedHost,
		DNSPort:            freeUDPPort(t),
		APIPort:            freeTCPPort(t),
		RaftPort:           freeRaftPort(t),
		ClusterMTLSEnabled: mtls,
	})

	// The node waits for its address instead of exiting.
	waitingLine := fmt.Sprintf("waiting for local address %s to be assigned (listener %s)", unassignedHost, cn.RaftAddr)
	deadline := time.Now().Add(waitingLineTimeout)
	for !strings.Contains(cn.Node.output.String(), waitingLine) {
		if cn.Node.hasExited() {
			t.Fatalf("skoed exited instead of waiting for %s%s", unassignedHost, exitReport(cn.Node))
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s, the output does not contain %q:\n%s", waitingLineTimeout, waitingLine, cn.Node.output.String())
		}
		time.Sleep(readyPollInterval)
	}
	waitStart := time.Now()

	// After 60 s without the address, startup fails.
	select {
	case <-cn.Node.exited:
	case <-time.After(maxWaitBeforeExit):
		t.Fatalf("skoed still running %s after it started waiting; want an exit after 60 s:\n%s", maxWaitBeforeExit, cn.Node.output.String())
	}
	waited := time.Since(waitStart)
	out := cn.Node.output.String()
	if code := cn.cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("skoed exited with status 0 after waiting %s; want a non-zero status", waited.Round(time.Second))
	}
	if waited < minWaitBeforeExit {
		t.Errorf("skoed exited %s after it started waiting; want about 60 s", waited.Round(time.Second))
	}
	if n := strings.Count(out, waitingLine); n != 1 {
		t.Errorf("the output contains the waiting line %d times; want once:\n%s", n, out)
	}
	wantErr := fmt.Sprintf("address %s still not assigned after 60s", cn.RaftAddr)
	if !strings.Contains(out, wantErr) {
		t.Errorf("the output does not contain %q:\n%s", wantErr, out)
	}
}

// requireUnassigned skips the test unless binding host fails with
// EADDRNOTAVAIL on the machine running the tests. A bind can succeed on
// an address assigned to no interface, e.g. with ip_nonlocal_bind or a
// local route.
func requireUnassigned(t *testing.T, host string) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err == nil {
		l.Close()
		t.Skipf("%s can be bound on this host", host)
	}
	if !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Skipf("binding %s fails with %v, not EADDRNOTAVAIL", host, err)
	}
}
