// Package cluster owns the M2 replicated state: Raft consensus, the bbolt
// state machine, node enrollment, the shadow YAML writer, and the cluster
// status API surface.
package cluster

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/skoed/skoed/internal/config"
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
	ID          string         `yaml:"id"`
	RaftAddress string         `yaml:"raft_address"`
	APIAddress  string         `yaml:"api_address"`
	DNS         DNSSection     `yaml:"dns"`
	DataDir     string         `yaml:"data_dir"`
	DHCP        DHCPSection    `yaml:"dhcp,omitempty"`
	API         APISection     `yaml:"api,omitempty"`
	Cluster     ClusterSection `yaml:"cluster,omitempty"`
}

// ClusterSection groups node-local cluster behaviour knobs. M5.3 adds
// the encrypted cluster mesh; future milestones can hang more here.
type ClusterSection struct {
	MTLS ClusterMTLSSection `yaml:"mtls,omitempty"`
}

// ClusterMTLSSection enables the M5.3 encrypted cluster mesh. When
// Enabled is true, the bootstrap node generates an ECDSA P-256 cluster
// CA at first boot; joining nodes receive the CA + a freshly-signed
// leaf cert in their join response.
type ClusterMTLSSection struct {
	Enabled bool `yaml:"enabled"`
}

// APISection holds node-local management-API settings. Today the
// substructure is M4.6 TLS + M5.1 metrics + M5.9.5 public landing;
// future M7 token-auth knobs can live here too.
type APISection struct {
	TLS           APITLSSection           `yaml:"tls,omitempty"`
	Metrics       APIMetricsSection       `yaml:"metrics,omitempty"`
	PublicLanding APIPublicLandingSection `yaml:"public_landing,omitempty"`
}

// APIPublicLandingSection gates the M5.9.5 unauthenticated landing
// page + /api/v1/_public/test-blocklist endpoint. Default is on:
// operators who want skoed to be admin-only (no public surface
// beyond /metrics and /health) flip Enabled to false. When the
// `enabled:` key is omitted from YAML, LoadConfig synthesises
// the default (true) so operators don't have to opt in to keep
// the documented behaviour.
type APIPublicLandingSection struct {
	Enabled    *bool `yaml:"enabled,omitempty"`
}

// PublicLandingEnabled returns true when the operator has not
// explicitly turned off the public landing page. nil → default
// true; *false → off; *true → on.
func (s APIPublicLandingSection) PublicLandingEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// APIMetricsSection configures the M5.1 Prometheus /metrics exporter.
// The endpoint is always served; RequireAuth flips it from open (the
// default — operator-internal metrics on LAN deployments) to Basic-auth
// gated (paranoid mode for nodes reachable from untrusted networks).
type APIMetricsSection struct {
	RequireAuth bool `yaml:"require_auth"`
}

// APITLSSection configures the M4.6 HTTPS listener for the management
// API. When Enabled is true, skoed serves the API over HTTPS using the
// same cert mechanism as DoH/DoT (node.dns.tls.cert_file / key_file,
// or the ACME-issued cert when node.dns.tls.acme.enabled).
type APITLSSection struct {
	Enabled bool `yaml:"enabled"`
	// Mode is "single_port" (default) or "dual_port".
	//   single_port: the existing api_address serves HTTPS only; plain
	//                HTTP on the same port returns 308 → https://.
	//   dual_port:   api_address keeps plain HTTP; HTTPSAddress hosts
	//                the HTTPS listener.
	Mode string `yaml:"mode,omitempty"`
	// HTTPSAddress is the host:port of the HTTPS listener when Mode is
	// dual_port. Ignored when Mode is single_port.
	HTTPSAddress string `yaml:"https_address,omitempty"`
	// HSTS adds Strict-Transport-Security on HTTPS responses. Off by
	// default — LAN deployments without DNS rebinding protection
	// shouldn't advertise HSTS.
	HSTS bool `yaml:"hsts,omitempty"`
}

// DHCPSection configures the M3.6 read-only DHCP integration. When
// Enabled is true, skoed polls Kind ("kea"|"dnsmasq"|"http_json") at
// RefreshSeconds intervals and surfaces hostnames / MACs / Client-IDs
// in the query log + dashboards. See ROADMAP.md M3.6.
type DHCPSection struct {
	Enabled        bool   `yaml:"enabled"`
	Kind           string `yaml:"kind,omitempty"`
	URL            string `yaml:"url,omitempty"`
	FilePath       string `yaml:"file_path,omitempty"`
	Username       string `yaml:"username,omitempty"`
	Password       string `yaml:"password,omitempty"`
	RefreshSeconds int    `yaml:"refresh_seconds,omitempty"`
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
// Both CertFile and KeyFile empty = skoed auto-generates a self-signed
// cert under <data_dir>/tls/ on first boot. When ACME is enabled, the
// operator-supplied cert paths are ignored — autocert manages everything.
type TLSSection struct {
	CertFile string       `yaml:"cert_file,omitempty"`
	KeyFile  string       `yaml:"key_file,omitempty"`
	Acme     *AcmeSection `yaml:"acme,omitempty"`
}

// AcmeSection configures the M4 ACME / Let's Encrypt integration. When
// Enabled is true, skoed obtains its DoH+DoT cert via the ACME protocol
// (HTTP-01 challenge by default) instead of using the self-signed
// fallback. Renewal is automatic.
type AcmeSection struct {
	Enabled bool `yaml:"enabled"`
	// Email is the ACME account contact — required by Let's Encrypt.
	Email string `yaml:"email,omitempty"`
	// Domains is the list of FQDNs the cert covers. SNI on DoH/DoT
	// connections MUST match one of these names. Required when enabled.
	Domains []string `yaml:"domains,omitempty"`
	// DirectoryURL overrides the ACME directory endpoint. Empty = Let's
	// Encrypt production. Useful for staging (LE staging URL) or for
	// pointing at Pebble / step-ca / internal CAs.
	DirectoryURL string `yaml:"directory_url,omitempty"`
	// HTTPChallengePort is the port autocert listens on for HTTP-01
	// challenges. Public-facing deployments typically set this to 80
	// (and either run skoed as root or use authbind / setcap). Default
	// 80 when Enabled is true and the field is 0.
	HTTPChallengePort int `yaml:"http_challenge_port,omitempty"`
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
