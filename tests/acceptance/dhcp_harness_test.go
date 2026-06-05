// Harness extensions for M3.6 DHCP integration tests.
//
// Defines the canonical Lease shape the connector tests assert on, the
// DhcpOpts knobs the test caller passes, and a placeholder
// startClusterWithDhcp that skips until the production binary + the
// harness's writeConfigYAML learn how to handle the DHCP section.

package acceptance

import (
	"testing"
	"time"
)

// Lease mirrors the canonical lease record produced by every connector.
// JSON tags match the internal snapshot endpoint's documented shape.
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
// connector enabled. Until the production binary supports the DHCP
// section in config.yaml (M3.6 impl), this skips the calling test so
// the test file remains green against the current master.
func startClusterWithDhcp(t *testing.T, opts DhcpOpts) *Cluster {
	t.Helper()
	_ = opts // placeholder until config.yaml support lands
	t.Skipf("M3.6 impl pending: harness does not yet write the DHCP section into config.yaml")
	return nil
}
