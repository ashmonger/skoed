package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// Unit tests for the Raft transport rule of TS-EncryptedClusterMesh: the
// dialing node sets ServerName to the peer's node ID, so it verifies the
// peer by node ID rather than by the dialed IP. FS-MtlsClusterFormsWhenEnabled,
// FS-MtlsRejectsUntrustedPeer.

// testStreamLayer starts a TLS stream layer on 127.0.0.3 that presents
// nodeID's leaf and completes the handshake of every accepted connection.
// Leaves carry 127.0.0.1 but not 127.0.0.3 in their IP SANs, so a dial
// verified by IP fails there, as it does across hosts.
// It returns the layer and the number of accepted connections.
func testStreamLayer(t *testing.T, caCert, caKey []byte, nodeID string) (*TLSStreamLayer, *atomic.Int32) {
	t.Helper()
	certPEM, keyPEM := testLeaf(t, caCert, caKey, nodeID)
	cfg, err := BuildClusterTLSConfig(caCert, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	sl, err := NewTLSStreamLayer("127.0.0.3:0", nil, cfg)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer: %v", err)
	}
	t.Cleanup(func() { sl.Close() })
	accepted := &atomic.Int32{}
	go func() {
		for {
			conn, err := sl.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer conn.Close()
				_ = conn.(*tls.Conn).Handshake()
			}()
		}
	}()
	return sl, accepted
}

// testDialer returns a stream layer, presenting nodeID's leaf, whose
// resolver maps peer to peerID and every other address to nothing.
func testDialer(t *testing.T, caCert, caKey []byte, nodeID string, peer raft.ServerAddress, peerID raft.ServerID) *TLSStreamLayer {
	t.Helper()
	certPEM, keyPEM := testLeaf(t, caCert, caKey, nodeID)
	cfg, err := BuildClusterTLSConfig(caCert, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	sl, err := NewTLSStreamLayer("127.0.0.1:0", nil, cfg)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer: %v", err)
	}
	t.Cleanup(func() { sl.Close() })
	sl.SetServerIDResolver(func(address raft.ServerAddress) (raft.ServerID, bool) {
		if address == peer {
			return peerID, true
		}
		return "", false
	})
	return sl
}

func TestMtlsDialVerifiesPeerNodeID(t *testing.T) {
	caCert, caKey := testCA(t)
	peer, _ := testStreamLayer(t, caCert, caKey, "b")
	addr := raft.ServerAddress(peer.Addr().String())

	t.Run("configured node ID matches the leaf", func(t *testing.T) {
		d := testDialer(t, caCert, caKey, "a", addr, "b")
		conn, err := d.Dial(addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial %s as b: %v", addr, err)
		}
		defer conn.Close()
		state := conn.(*tls.Conn).ConnectionState()
		if got := state.PeerCertificates[0].Subject.CommonName; got != "b" {
			t.Fatalf("peer leaf CN = %q, want b", got)
		}
	})

	t.Run("configured node ID differs from the leaf", func(t *testing.T) {
		d := testDialer(t, caCert, caKey, "a", addr, "a")
		conn, err := d.Dial(addr, 5*time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("dial %s as a succeeded against b's leaf", addr)
		}
		var hostErr x509.HostnameError
		if !errors.As(err, &hostErr) {
			t.Fatalf("dial error = %v, want an x509.HostnameError", err)
		}
	})
}

func TestMtlsDialRefusesAddressNotInConfiguration(t *testing.T) {
	caCert, caKey := testCA(t)
	peer, accepted := testStreamLayer(t, caCert, caKey, "b")
	addr := raft.ServerAddress(peer.Addr().String())

	d := testDialer(t, caCert, caKey, "a", "127.0.0.9:7000", "c")
	conn, err := d.Dial(addr, 5*time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("dial %s succeeded with no configured node ID", addr)
	}
	if !strings.Contains(err.Error(), "not in the Raft configuration") {
		t.Fatalf("dial error = %v, want the address to be reported as not in the Raft configuration", err)
	}
	// Give a connection, had one been opened, time to be accepted.
	time.Sleep(100 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("peer accepted %d connections, want none", n)
	}
}

func TestMtlsDialWithoutResolverFails(t *testing.T) {
	caCert, caKey := testCA(t)
	peer, accepted := testStreamLayer(t, caCert, caKey, "b")
	addr := raft.ServerAddress(peer.Addr().String())

	certPEM, keyPEM := testLeaf(t, caCert, caKey, "a")
	cfg, err := BuildClusterTLSConfig(caCert, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewTLSStreamLayer("127.0.0.1:0", nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if conn, err := d.Dial(addr, 5*time.Second); err == nil {
		conn.Close()
		t.Fatalf("dial %s succeeded with no resolver", addr)
	}
	time.Sleep(100 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("peer accepted %d connections, want none", n)
	}
}

func TestServerIDByAddress(t *testing.T) {
	cfg := raft.Configuration{Servers: []raft.Server{
		{ID: "a", Address: "192.168.1.191:7000"},
		{ID: "b", Address: "192.168.1.192:7000"},
	}}
	if id, ok := serverIDByAddress(cfg, "192.168.1.192:7000"); !ok || id != "b" {
		t.Fatalf("serverIDByAddress(b's address) = %q, %v; want b, true", id, ok)
	}
	if id, ok := serverIDByAddress(cfg, "192.168.1.193:7000"); ok {
		t.Fatalf("serverIDByAddress(unknown) = %q, true; want false", id)
	}
}
