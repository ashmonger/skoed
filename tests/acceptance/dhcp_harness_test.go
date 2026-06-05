// Harness extensions for M3.6 DHCP integration tests.
//
// Defines the canonical Lease shape the connector tests assert on, the
// DhcpOpts knobs the test caller passes, and startClusterWithDhcp which
// spins a single-node cluster wired to a DHCP source.

package acceptance

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Lease mirrors the canonical lease record produced by every connector.
type Lease struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`
	Hostname  string    `json:"hostname"`
	ClientID  string    `json:"client_id"`
	Source    string    `json:"source"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DhcpOpts is the per-node DHCP connector config the harness writes
// under node.dhcp.* in the spawned node's config.yaml.
type DhcpOpts struct {
	Kind           string // "kea" | "dnsmasq" | "http_json"
	URL            string // Kea control-agent URL OR http_json endpoint
	FilePath       string // dnsmasq lease file (when Kind == "dnsmasq")
	Username       string // optional Basic Auth for Kea / http_json
	Password       string
	RefreshSeconds int // 0 → default
}

// startClusterWithDhcp spins a single-node cluster with the given DHCP
// connector enabled. Sets Node.LeaseSnapshotURL so connector tests can
// poll /api/v1/clients/_leases.
//
// Always enables DBLOCK_TEST_MODE so callers can use EDNS0 LOCAL option
// 65500 to drive query-log entries from synthetic client IPs (the
// hostname-enrichment tests need this to verify the lease lookup
// happens against the right IP).
func startClusterWithDhcp(t *testing.T, opts DhcpOpts) *Cluster {
	t.Helper()
	t.Setenv("DBLOCK_TEST_MODE", "1")
	bin := dblockBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("dblock binary not found at %s (set DBLOCK_BINARY to override)", bin)
	}
	c := &Cluster{t: t, bin: bin}
	cfg := M2NodeConfig{
		NodeID:   "node-1",
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeTCPPort(t),
		DHCP:     &opts,
	}
	cn := c.spawnNode(t, cfg)
	cn.Node.LeaseSnapshotURL = fmt.Sprintf("http://127.0.0.1:%d/api/v1/clients/_leases", cfg.APIPort)
	c.nodes = append(c.nodes, cn)
	waitReady(t, cn.Node)
	setupAuth(t, c.nodes[0].Node)
	return c
}
