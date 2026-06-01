package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const SchemaVersion = 1

// Config is the root configuration structure. It is the single source of truth
// for all dblock settings and is persisted to config.yaml via atomic rename.
type Config struct {
	Version   int             `yaml:"version"`
	DNS       DNSConfig       `yaml:"dns"`
	Filtering FilteringConfig `yaml:"filtering"`
	LocalDNS  LocalDNSConfig  `yaml:"local_dns"`
	API       APIConfig       `yaml:"api"`
	QueryLog  QueryLogConfig  `yaml:"query_log"`
	Auth      AuthConfig      `yaml:"auth"`
}

type DNSConfig struct {
	Listen            ListenConfig `yaml:"listen"                      json:"listen"`
	Mode              string       `yaml:"mode"                        json:"mode"`
	UpstreamResolvers []string     `yaml:"upstream_resolvers,omitempty" json:"upstream_resolvers,omitempty"`
	UpstreamTimeout   int          `yaml:"upstream_timeout_seconds"    json:"upstream_timeout_seconds"`
	TrustedSubnets    []string     `yaml:"trusted_subnets,omitempty"   json:"trusted_subnets,omitempty"`
	Cache             CacheConfig  `yaml:"cache"                       json:"cache"`
}

type ListenConfig struct {
	Port int  `yaml:"port" json:"port"`
	IPv4 bool `yaml:"ipv4" json:"ipv4"`
	IPv6 bool `yaml:"ipv6" json:"ipv6"`
}

type CacheConfig struct {
	Enabled    bool `yaml:"enabled"     json:"enabled"`
	MaxEntries int  `yaml:"max_entries" json:"max_entries"`
}

type FilteringConfig struct {
	BlockPolicy string      `yaml:"block_policy"` // "nxdomain" | "null" | "nodata"
	Blocklists  []Blocklist `yaml:"blocklists,omitempty"`
	Allowlist   []string    `yaml:"allowlist,omitempty"`
}

// Blocklist describes a named set of domain rules.
type Blocklist struct {
	ID          string          `yaml:"id"`
	Name        string          `yaml:"name"`
	Enabled     bool            `yaml:"enabled"`
	Source      BlocklistSource `yaml:"source"`
	BlockPolicy string          `yaml:"block_policy,omitempty"` // "" = inherit global
	Domains     []string        `yaml:"domains,omitempty"`
	LastUpdated string          `yaml:"last_updated,omitempty"`
}

type BlocklistSource struct {
	Type   string `yaml:"type"            json:"type"`
	URL    string `yaml:"url,omitempty"   json:"url,omitempty"`
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

type LocalDNSConfig struct {
	Entries []LocalDNSEntry `yaml:"entries,omitempty"`
}

// LocalDNSEntry is a manually configured DNS record served by dblock.
type LocalDNSEntry struct {
	ID       string `yaml:"id"       json:"id"`
	Hostname string `yaml:"hostname" json:"hostname"`
	Type     string `yaml:"type"     json:"type"`
	Value    string `yaml:"value"    json:"value"`
	TTL      int    `yaml:"ttl"      json:"ttl"`
}

type APIConfig struct {
	Port int `yaml:"port"`
}

type QueryLogConfig struct {
	MaxEntries             int `yaml:"max_entries"               json:"max_entries"`
	AggregateRetentionDays int `yaml:"aggregate_retention_days"  json:"aggregate_retention_days"`
}

type AuthConfig struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"` // bcrypt hash; empty = first-run pending
}

// Defaults fills in zero values with sensible defaults.
func (c *Config) Defaults() {
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	if c.DNS.Listen.Port == 0 {
		c.DNS.Listen.Port = 53
	}
	if !c.DNS.Listen.IPv4 && !c.DNS.Listen.IPv6 {
		c.DNS.Listen.IPv4 = true
		c.DNS.Listen.IPv6 = true
	}
	if c.DNS.Mode == "" {
		c.DNS.Mode = "forwarding"
	}
	if c.DNS.UpstreamTimeout == 0 {
		c.DNS.UpstreamTimeout = 3
	}
	if !c.DNS.Cache.Enabled && c.DNS.Cache.MaxEntries == 0 {
		c.DNS.Cache.Enabled = true
		c.DNS.Cache.MaxEntries = 10000
	}
	if c.Filtering.BlockPolicy == "" {
		c.Filtering.BlockPolicy = "nxdomain"
	}
	if c.API.Port == 0 {
		c.API.Port = 8080
	}
	if c.QueryLog.MaxEntries == 0 {
		c.QueryLog.MaxEntries = 10000
	}
	if c.QueryLog.AggregateRetentionDays == 0 {
		c.QueryLog.AggregateRetentionDays = 30
	}
	for i := range c.DNS.UpstreamResolvers {
		c.DNS.UpstreamResolvers[i] = normaliseUpstream(c.DNS.UpstreamResolvers[i])
	}
	for i := range c.LocalDNS.Entries {
		if c.LocalDNS.Entries[i].TTL == 0 {
			c.LocalDNS.Entries[i].TTL = 300
		}
	}
}

// Validate returns an error if the configuration is semantically invalid.
func (c *Config) Validate() error {
	if c.Version != SchemaVersion {
		return fmt.Errorf("unsupported config version %d (expected %d)", c.Version, SchemaVersion)
	}
	if c.DNS.Mode != "forwarding" && c.DNS.Mode != "recursive" {
		return fmt.Errorf("dns.mode must be 'forwarding' or 'recursive', got %q", c.DNS.Mode)
	}
	switch c.Filtering.BlockPolicy {
	case "nxdomain", "null", "nodata":
	default:
		return fmt.Errorf("filtering.block_policy must be nxdomain, null, or nodata; got %q", c.Filtering.BlockPolicy)
	}
	if c.DNS.Listen.Port < 1 || c.DNS.Listen.Port > 65535 {
		return fmt.Errorf("dns.listen.port must be 1–65535")
	}
	if c.API.Port < 1 || c.API.Port > 65535 {
		return fmt.Errorf("api.port must be 1–65535")
	}
	return nil
}

// Load reads and parses config.yaml from dir, applies defaults, and validates.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.Defaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &c, nil
}

// Save atomically writes the config to config.yaml in dir.
func Save(dir string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := filepath.Join(dir, "config.yaml.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.yaml")); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// normaliseUpstream appends :53 if no port is specified.
func normaliseUpstream(s string) string {
	if s == "" {
		return s
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return s + ":53"
}
