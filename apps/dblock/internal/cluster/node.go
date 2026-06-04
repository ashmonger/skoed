// Package cluster owns the M2 replicated state: Raft consensus, the bbolt
// state machine, node enrollment, the shadow YAML writer, and the cluster
// status API surface.
package cluster

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/dblock/dblock/internal/config"
	"gopkg.in/yaml.v3"
)

// NodeYAML is the node-local view of the on-disk config.yaml. It contains
// the per-host settings (id, addresses, listen ports) plus an optional
// one-shot bootstrap section. Cluster-replicated state (blocklists, settings,
// auth, etc.) is also in the same file but is held in a parallel
// *config.Config returned by LoadConfig.
type NodeYAML struct {
	Node      NodeSection      `yaml:"node"`
	Bootstrap BootstrapSection `yaml:"bootstrap,omitempty"`
}

// NodeSection holds settings that depend on this host: the Raft identity, the
// addresses to bind, and the DNS listen port.
type NodeSection struct {
	ID          string     `yaml:"id"`
	RaftAddress string     `yaml:"raft_address"`
	APIAddress  string     `yaml:"api_address"`
	DNS         DNSSection `yaml:"dns"`
	DataDir     string     `yaml:"data_dir"`
}

// DNSSection / ListenSection mirror the shape used in M1's config.yaml so
// existing M1 binaries can still parse this listen subtree without surprise.
type DNSSection struct {
	Listen ListenSection `yaml:"listen"`
	TLS    TLSSection    `yaml:"tls,omitempty"`
}

// ListenSection is the bind address for the DNS server.
type ListenSection struct {
	Port int  `yaml:"port"`
	IPv4 bool `yaml:"ipv4"`
	IPv6 bool `yaml:"ipv6"`
	// M4: encrypted-DNS listeners. Zero/unset = disabled.
	DoHPort int `yaml:"doh_port,omitempty"`
	DoTPort int `yaml:"dot_port,omitempty"`
}

// TLSSection carries the certificate paths for the M4 DoH/DoT listeners.
// Both fields empty = dblock auto-generates a self-signed cert under
// <data_dir>/tls/ on first boot.
type TLSSection struct {
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
}

// BootstrapSection is consumed exactly once on first boot when no bbolt
// exists. Subsequent boots ignore it.
type BootstrapSection struct {
	LeaderAddress string `yaml:"leader_address,omitempty"`
	Token         string `yaml:"token,omitempty"`
}

// DataPath joins the node's data directory with one or more path components.
func (ny *NodeYAML) DataPath(parts ...string) string {
	return filepath.Join(append([]string{ny.Node.DataDir}, parts...)...)
}

// LoadConfig reads a per-node config.yaml. The file may be either the M2
// "merged" form (contains a top-level `node:` section plus the M1
// cluster-replicated fields) or the legacy M1 form (no `node:` section).
//
// Returns:
//   - the node-local view (always non-nil on success)
//   - the cluster-replicated snapshot iff the file carries one that needs
//     migrating into bbolt on first boot. Zero/empty cluster sections in a
//     merged file yield nil here so we don't pointlessly re-import on every
//     subsequent boot after the shadow writer has touched the file.
//   - an error if the file is unreadable, malformed, or missing fields that
//     can't be defaulted.
//
// LoadConfig is permissive: missing `node:` fields are filled from the M1
// dns/api settings and a hostname-style default ID. raft_address defaults to
// 127.0.0.1:<freeport> for single-node convenience; production deployments
// should always set it explicitly.
func LoadConfig(path string) (*NodeYAML, *config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config.yaml: %w", err)
	}

	// Pass 1: parse the merged form.
	var merged struct {
		NodeYAML  `yaml:",inline"`
		ClusterIn config.Config `yaml:",inline"`
	}
	if err := yaml.Unmarshal(data, &merged); err != nil {
		return nil, nil, fmt.Errorf("parse config.yaml: %w", err)
	}

	ny := &merged.NodeYAML
	cluster := &merged.ClusterIn

	// Synthesise node section if absent (M1 legacy or partial config).
	if ny.Node.ID == "" {
		ny.Node.ID = defaultNodeID()
	}
	if ny.Node.APIAddress == "" {
		port := cluster.API.Port
		if port == 0 {
			port = 8080
		}
		ny.Node.APIAddress = fmt.Sprintf(":%d", port)
	}
	if ny.Node.DNS.Listen.Port == 0 {
		ny.Node.DNS.Listen = ListenSection{
			Port: cluster.DNS.Listen.Port,
			IPv4: cluster.DNS.Listen.IPv4 || (!cluster.DNS.Listen.IPv4 && !cluster.DNS.Listen.IPv6),
			IPv6: cluster.DNS.Listen.IPv6,
		}
		if ny.Node.DNS.Listen.Port == 0 {
			ny.Node.DNS.Listen.Port = 53
		}
	}
	if ny.Node.RaftAddress == "" {
		p, err := pickFreeTCPPort()
		if err != nil {
			return nil, nil, fmt.Errorf("pick raft port: %w", err)
		}
		ny.Node.RaftAddress = fmt.Sprintf("127.0.0.1:%d", p)
	}
	if ny.Node.DataDir == "" {
		ny.Node.DataDir = filepath.Dir(path)
	}
	if !ny.Node.DNS.Listen.IPv4 && !ny.Node.DNS.Listen.IPv6 {
		ny.Node.DNS.Listen.IPv4 = true
	}

	// Decide whether the cluster section carries seed state to migrate.
	if isClusterSectionEmpty(cluster) {
		return ny, nil, nil
	}
	// Strip node-local DNS listen from the seed snapshot before handing it
	// to the importer so it doesn't try to replicate a host-specific port.
	cluster.DNS.Listen = config.ListenConfig{}
	if cluster.Version == 0 {
		cluster.Version = config.SchemaVersion
	}
	return ny, cluster, nil
}

// isClusterSectionEmpty reports whether a parsed cluster section is
// essentially the zero value (no blocklists, no allowlist, no local DNS,
// blank auth). Used to skip pointless re-imports after the shadow writer
// has populated the file with bbolt's current state.
func isClusterSectionEmpty(c *config.Config) bool {
	if len(c.Filtering.Blocklists) > 0 || len(c.Filtering.Allowlist) > 0 {
		return false
	}
	if len(c.LocalDNS.Entries) > 0 {
		return false
	}
	if c.Filtering.BlockPolicy != "" {
		return false
	}
	if c.Auth.Username != "" || c.Auth.PasswordHash != "" {
		return false
	}
	if c.QueryLog.MaxEntries > 0 {
		return false
	}
	if len(c.DNS.UpstreamResolvers) > 0 || c.DNS.Mode != "" {
		return false
	}
	return true
}

// LoadNodeYAML is preserved as a thin wrapper for callers that only need
// the node-local view and want LoadConfig's auto-defaults.
func LoadNodeYAML(path string) (*NodeYAML, error) {
	ny, _, err := LoadConfig(path)
	return ny, err
}

// defaultNodeID returns a hostname-derived identifier with a fallback.
func defaultNodeID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "node-1"
}

// pickFreeTCPPort asks the OS for a free TCP port, closes the listener, and
// returns the port number. There is a small race window before the caller
// re-binds, but it is good enough for single-node test bootstraps.
func pickFreeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
