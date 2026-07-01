package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PauseState is stored in bbolt and replicated via Raft. ResumesAt is the
// wall-clock deadline; if time.Now().Before(ResumesAt) the pause is active.
// ProfileIDs restricts the pause to specific profiles; empty means all profiles.
type PauseState struct {
	ResumesAt  time.Time `yaml:"resumes_at" json:"resumes_at"`
	Reason     string    `yaml:"reason,omitempty" json:"reason,omitempty"`
	ProfileIDs []string  `yaml:"profile_ids,omitempty" json:"profile_ids,omitempty"`
}

const SchemaVersion = 1

// Config is the root configuration structure. It is the single source of truth
// for all skoed settings and is persisted to config.yaml via atomic rename.
type Config struct {
	Version   int                 `yaml:"version"`
	DNS       DNSConfig           `yaml:"dns"`
	Filtering FilteringConfig     `yaml:"filtering"`
	LocalDNS  LocalDNSConfig      `yaml:"local_dns"`
	API       APIConfig           `yaml:"api"`
	QueryLog  QueryLogConfig      `yaml:"query_log"`
	Auth      AuthConfig          `yaml:"auth"`
	Profiles  []Profile           `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Schedules []Schedule          `yaml:"schedules,omitempty" json:"schedules,omitempty"`
	Bindings  []ScheduleBinding   `yaml:"schedule_bindings,omitempty" json:"schedule_bindings,omitempty"`
	Categories []CategoryOverride `yaml:"category_overrides,omitempty" json:"category_overrides,omitempty"`
	Webhooks  []WebhookEndpoint   `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	// M23.5 — built-in DHCP server (leader-owned).
	DHCPServer DHCPServerConfig `yaml:"dhcp_server,omitempty" json:"dhcp_server,omitempty"`
	// M35.5 — named device registry.
	Devices []Device `yaml:"devices,omitempty" json:"devices,omitempty"`
}

// BackupConfig configures the scheduled backup feature (M31).
// Node-local: stored directly in bbolt, not replicated via Raft.
type BackupConfig struct {
	Enabled       bool `yaml:"enabled"        json:"enabled"`
	IntervalHours int  `yaml:"interval_hours" json:"interval_hours"`
	RetainCount   int  `yaml:"retain_count"   json:"retain_count"`
}

// BackupEntry describes a single stored backup archive.
type BackupEntry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
	Encrypted bool      `json:"encrypted"`
}

// DHCPServerConfig is the persisted configuration for the built-in DHCP server.
// Cluster-wide: replicated via Raft, stored in bbolt. The enable flag controls
// whether the Raft leader starts a UDP 67 listener.
type DHCPServerConfig struct {
	Enabled          bool                  `yaml:"enabled"                     json:"enabled"`
	PoolStart        string                `yaml:"pool_start,omitempty"        json:"pool_start,omitempty"`
	PoolEnd          string                `yaml:"pool_end,omitempty"          json:"pool_end,omitempty"`
	Gateway          string                `yaml:"gateway,omitempty"           json:"gateway,omitempty"`
	LeaseTimeSeconds int                   `yaml:"lease_time_seconds,omitempty" json:"lease_time_seconds,omitempty"`
	Domain           string                `yaml:"domain,omitempty"            json:"domain,omitempty"`
	DNSServer        string                `yaml:"dns_server,omitempty"        json:"dns_server,omitempty"`
	StaticAssignments []DHCPStaticAssignment `yaml:"static_assignments,omitempty" json:"static_assignments,omitempty"`
	// M30 — DHCPv6 server config (nested).
	Server6 DHCPv6ServerConfig `yaml:"server6,omitempty" json:"server6,omitempty"`
}

// DHCPStaticAssignment pins a MAC address to a fixed IP and optional hostname.
type DHCPStaticAssignment struct {
	MAC      string `yaml:"mac"               json:"mac"`
	IP       string `yaml:"ip"                json:"ip"`
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
}

// DHCPv6ServerConfig is the persisted configuration for the built-in DHCPv6 server.
// M30 — cluster-wide, replicated via Raft. The leader binds UDP 547.
type DHCPv6ServerConfig struct {
	Enabled           bool                    `yaml:"enabled"                      json:"enabled"`
	Prefix            string                  `yaml:"prefix,omitempty"             json:"prefix,omitempty"`
	PoolStart         string                  `yaml:"pool_start,omitempty"         json:"pool_start,omitempty"`
	PoolEnd           string                  `yaml:"pool_end,omitempty"           json:"pool_end,omitempty"`
	LeaseTime         int                     `yaml:"lease_time,omitempty"         json:"lease_time,omitempty"` // seconds
	SearchDomain      string                  `yaml:"search_domain,omitempty"      json:"search_domain,omitempty"`
	StaticAssignments []Dhcp6StaticAssignment `yaml:"static_assignments,omitempty" json:"static_assignments,omitempty"`
}

// Dhcp6StaticAssignment pins a DUID to a fixed IPv6 address.
type Dhcp6StaticAssignment struct {
	DUID     string `yaml:"duid"               json:"duid"`
	Address  string `yaml:"address"            json:"address"`
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
}

// Profile binds blocklists, allowlist entries, SafeSearch providers, and
// client identifiers (IPs and/or CIDRs) into a named rule set. The reserved
// id "default" is applied to any client not matched by an explicit profile.
type Profile struct {
	ID          string   `yaml:"id"           json:"id"`
	Name        string   `yaml:"name"         json:"name"`
	Blocklists  []string `yaml:"blocklists,omitempty"   json:"blocklists,omitempty"`
	Allowlist   []string `yaml:"allowlist,omitempty"    json:"allowlist,omitempty"`
	SafeSearch  []string `yaml:"safesearch,omitempty"   json:"safesearch,omitempty"`
	ClientIPs   []string `yaml:"client_ips,omitempty"   json:"client_ips,omitempty"`
	ClientCIDRs []string `yaml:"client_cidrs,omitempty" json:"client_cidrs,omitempty"`
	// M3.6: per-client stable-identity match keys. Priority on lookup:
	// ClientIDs > ClientMACs > ClientHostnames > ClientIPs/ClientCIDRs.
	ClientIDs       []string `yaml:"client_ids,omitempty"       json:"client_ids,omitempty"`
	ClientMACs      []string `yaml:"client_macs,omitempty"      json:"client_macs,omitempty"`
	ClientHostnames []string `yaml:"client_hostnames,omitempty" json:"client_hostnames,omitempty"`

	// M6.5 (TS-BlockDyn): when true, every client whose DHCP lease
	// Origin is exactly "dhcp_dynamic" matches this profile as part of
	// the tier-4 (IP/CIDR) union. Not allowed on the "default" profile.
	BlockDynamicClients bool `yaml:"block_dynamic_clients,omitempty" json:"block_dynamic_clients,omitempty"`

	// M30 (TS-Dhcpv6DuidProfileMatch): DHCPv6 client DUIDs that map to this profile.
	ClientDUIDs []string `yaml:"client_duids,omitempty" json:"client_duids,omitempty"`

	Pause *PauseState `yaml:"pause,omitempty" json:"pause,omitempty"`

	// M33: per-profile block page content overrides. Nil means use global defaults.
	BlockPage *ProfileBlockPageConfig `yaml:"block_page,omitempty" json:"block_page,omitempty"`
}

// Device groups multiple network identifiers (MACs, IPs, hostnames, client-ids)
// that represent a single physical or virtual machine. It binds them to one
// profile; the device registry is evaluated before all profile selectors.
type Device struct {
	ID        string   `yaml:"id"                  json:"id"`
	Name      string   `yaml:"name"                json:"name"`
	ProfileID string   `yaml:"profile_id"          json:"profile_id"`
	MACs      []string `yaml:"macs,omitempty"      json:"macs,omitempty"`
	IPs       []string `yaml:"ips,omitempty"       json:"ips,omitempty"`
	Hostnames []string `yaml:"hostnames,omitempty" json:"hostnames,omitempty"`
	ClientIDs []string `yaml:"client_ids,omitempty" json:"client_ids,omitempty"`
}

// Schedule defines time-of-day / day-of-week windows that gate when a
// schedule-binding's blocklist applies. Times use 24h "HH:MM" in the node's
// local timezone (configurable via node.yaml's optional `node.timezone`).
type Schedule struct {
	ID      string       `yaml:"id"      json:"id"`
	Name    string       `yaml:"name"    json:"name"`
	Mode    string       `yaml:"mode"    json:"mode"` // "block_only_inside" | "allow_only_inside"
	Windows []TimeWindow `yaml:"windows" json:"windows"`
}

// TimeWindow is one weekly recurring interval. `End < Start` wraps midnight.
type TimeWindow struct {
	Days  []string `yaml:"days"  json:"days"`
	Start string   `yaml:"start" json:"start"`
	End   string   `yaml:"end"   json:"end"`
}

// ScheduleBinding attaches one schedule to one (profile, blocklist) pair.
type ScheduleBinding struct {
	ScheduleID  string `yaml:"schedule_id"  json:"schedule_id"`
	ProfileID   string `yaml:"profile_id"   json:"profile_id"`
	BlocklistID string `yaml:"blocklist_id" json:"blocklist_id"`
}

// CategoryOverride lets an operator point a built-in category at a custom
// upstream URL. Empty fields fall back to the catalog defaults.
type CategoryOverride struct {
	Name   string `yaml:"name"           json:"name"`
	URL    string `yaml:"url,omitempty"   json:"url,omitempty"`
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

// WebhookEndpoint describes a single outbound webhook receiver.
type WebhookEndpoint struct {
	ID      string   `yaml:"id"      json:"id"`
	URL     string   `yaml:"url"     json:"url"`
	Secret  string   `yaml:"secret"  json:"secret"`
	Events  []string `yaml:"events"  json:"events"`
	Enabled bool     `yaml:"enabled" json:"enabled"`
}

// UpstreamRoute sends queries matching Match to the given Resolvers before
// consulting the global UpstreamResolvers list. Routes are evaluated top-down;
// the first match wins.
//
// Match syntax:
//   - "*.suffix"  — any subdomain of suffix at any depth
//   - "exact"     — the exact domain only (no subdomains)
type UpstreamRoute struct {
	Match     string   `yaml:"match"     json:"match"`
	Resolvers []string `yaml:"resolvers" json:"resolvers"`
}

type DNSConfig struct {
	Listen            ListenConfig    `yaml:"listen"                       json:"listen"`
	Mode              string          `yaml:"mode"                         json:"mode"`
	DNSSECMode        string          `yaml:"dnssec_mode,omitempty"        json:"dnssec_mode,omitempty"` // "transparent" (default) | "validate"
	UpstreamResolvers []string        `yaml:"upstream_resolvers,omitempty" json:"upstream_resolvers,omitempty"`
	UpstreamRoutes    []UpstreamRoute `yaml:"upstream_routes,omitempty"    json:"upstream_routes,omitempty"`
	UpstreamTimeout   int             `yaml:"upstream_timeout_seconds"     json:"upstream_timeout_seconds"`
	TrustedSubnets    []string        `yaml:"trusted_subnets,omitempty"    json:"trusted_subnets,omitempty"`
	Cache             CacheConfig     `yaml:"cache"                        json:"cache"`
}

type ListenConfig struct {
	Port int  `yaml:"port" json:"port"`
	IPv4 bool `yaml:"ipv4" json:"ipv4"`
	IPv6 bool `yaml:"ipv6" json:"ipv6"`
	// M4: encrypted DNS listeners. Zero/unset = disabled.
	DoHPort int `yaml:"doh_port,omitempty" json:"doh_port,omitempty"`
	DoTPort int `yaml:"dot_port,omitempty" json:"dot_port,omitempty"`
}

// TLSConfig carries the certificate paths used by the DoH and DoT listeners.
// When CertFile/KeyFile are empty, skoed generates a self-signed cert on
// first boot under <data_dir>/tls/ and reuses it afterwards.
type TLSConfig struct {
	CertFile string `yaml:"cert_file,omitempty" json:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"  json:"key_file,omitempty"`
}

// TLSRenewConfig holds the cluster-replicated TLS auto-renewal settings (M34).
// Stored in bbolt via Raft; controls the ACME background renewal job on every node.
type TLSRenewConfig struct {
	AutoRenew            bool           `yaml:"auto_renew"              json:"auto_renew"`
	RenewalThresholdDays int            `yaml:"renewal_threshold_days"  json:"renewal_threshold_days"`
	ACME                 ACMERenewConfig `yaml:"acme"                   json:"acme"`
}

// ACMERenewConfig holds ACME-specific fields within TLSRenewConfig.
type ACMERenewConfig struct {
	Domains []string `yaml:"domains" json:"domains"`
	Email   string   `yaml:"email"   json:"email"`
}

type CacheConfig struct {
	Enabled    bool `yaml:"enabled"     json:"enabled"`
	MaxEntries int  `yaml:"max_entries" json:"max_entries"`
}

// BlockPageConfig holds the settings for the M26 redirect block page.
type BlockPageConfig struct {
	IP           string `yaml:"ip,omitempty"                  json:"ip,omitempty"`
	Port         int    `yaml:"port,omitempty"                json:"port,omitempty"`
	Title        string `yaml:"title,omitempty"               json:"title,omitempty"`
	Message      string `yaml:"message,omitempty"             json:"message,omitempty"`
	ContactEmail string `yaml:"contact_email,omitempty"       json:"contact_email,omitempty"`
	// M33: optional IPv6 redirect address. When set, AAAA queries for blocked
	// domains under the "redirect" policy return this address instead of SERVFAIL.
	RedirectAddressV6 string `yaml:"redirect_address_v6,omitempty" json:"redirect_address_v6,omitempty"`
}

// ProfileBlockPageConfig holds per-profile block page content overrides (M33).
// Fields present here take precedence over the global BlockPageConfig values.
type ProfileBlockPageConfig struct {
	Title          string `yaml:"title,omitempty"           json:"title,omitempty"`
	Message        string `yaml:"message,omitempty"         json:"message,omitempty"`
	ContactEmail   string `yaml:"contact_email,omitempty"   json:"contact_email,omitempty"`
	LogoURL        string `yaml:"logo_url,omitempty"        json:"logo_url,omitempty"`
	// BypassPasscode is the secret string a client must supply to POST /api/v1/bypass
	// in order to receive a time-bounded pause on their profile's filtering.
	BypassPasscode string `yaml:"bypass_passcode,omitempty" json:"bypass_passcode,omitempty"`
}

type FilteringConfig struct {
	BlockPolicy     string          `yaml:"block_policy"` // "nxdomain" | "null" | "nodata" | "redirect"
	Blocklists      []Blocklist     `yaml:"blocklists,omitempty"`
	Allowlist       []string        `yaml:"allowlist,omitempty"`
	PauseMaxSeconds int             `yaml:"pause_max_seconds,omitempty" json:"pause_max_seconds,omitempty"` // 0 = feature disabled; absent = 86400
	GlobalPause     *PauseState     `yaml:"global_pause,omitempty"      json:"global_pause,omitempty"`
	BlockPage       BlockPageConfig `yaml:"block_page,omitempty"        json:"block_page,omitempty"`
	// M30.5 — cluster-wide custom filtering rules (AdGuard Home syntax).
	// /regex/ = block, @@/regex/ = allow, domain = exact block, @@domain = exact allow.
	CustomRules     string          `yaml:"custom_rules,omitempty"      json:"custom_rules,omitempty"`
}

// Blocklist describes a named set of domain rules.
type Blocklist struct {
	ID          string          `yaml:"id"           json:"id"`
	Name        string          `yaml:"name"         json:"name"`
	Enabled     bool            `yaml:"enabled"      json:"enabled"`
	Source      BlocklistSource `yaml:"source"       json:"source"`
	BlockPolicy string          `yaml:"block_policy,omitempty" json:"block_policy,omitempty"` // "" = inherit global
	Domains     []string        `yaml:"domains,omitempty"      json:"domains,omitempty"`
	LastUpdated string          `yaml:"last_updated,omitempty" json:"last_updated,omitempty"`
	// Managed marks a blocklist as owned by a category (cat:*). The UI uses
	// this to forbid manual editing of the domain list — refreshes pull
	// from the catalog URL instead.
	Managed bool `yaml:"managed,omitempty" json:"managed,omitempty"`

	// M5.4 — automated refresh state. RefreshIntervalSeconds=0 means
	// "don't auto-refresh"; absent means "inherit cluster default".
	// LastRefresh* are populated by the scheduler on each tick.
	RefreshIntervalSeconds int    `yaml:"refresh_interval_seconds,omitempty" json:"refresh_interval_seconds,omitempty"`
	LastRefreshAt          string `yaml:"last_refresh_at,omitempty"          json:"last_refresh_at,omitempty"`
	LastRefreshStatus      string `yaml:"last_refresh_status,omitempty"      json:"last_refresh_status,omitempty"`
	LastRefreshError       string `yaml:"last_refresh_error,omitempty"       json:"last_refresh_error,omitempty"`
}

type BlocklistSource struct {
	Type   string `yaml:"type"            json:"type"`
	URL    string `yaml:"url,omitempty"   json:"url,omitempty"`
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

type LocalDNSConfig struct {
	Entries []LocalDNSEntry `yaml:"entries,omitempty"`
}

// LocalDNSEntry is a manually configured DNS record served by skoed.
type LocalDNSEntry struct {
	ID       string `yaml:"id"       json:"id"`
	Hostname string `yaml:"hostname" json:"hostname"`
	Type     string `yaml:"type"     json:"type"`
	Value    string `yaml:"value"    json:"value"`
	TTL      int    `yaml:"ttl"      json:"ttl"`
}

type APIConfig struct {
	Port int             `yaml:"port"`
	Docs APIDocsConfig   `yaml:"docs,omitempty"`
}

// APIDocsConfig gates the M4.5 API Documentation Browser. Default is on
// — Disabled=true strips both /api/docs and /api/openapi.yaml from the
// route table.
type APIDocsConfig struct {
	Disabled bool `yaml:"disabled,omitempty"`
}

type QueryLogConfig struct {
	MaxEntries             int `yaml:"max_entries"               json:"max_entries"`
	AggregateRetentionDays int `yaml:"aggregate_retention_days"  json:"aggregate_retention_days"`
}

type AuthConfig struct {
	Username             string `yaml:"username"`
	PasswordHash         string `yaml:"password_hash"`          // bcrypt hash; empty = first-run pending
	SessionTimeoutSeconds int   `yaml:"session_timeout_seconds,omitempty" json:"session_timeout_seconds,omitempty"`
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
		c.DNS.Mode = "recursive"
	}
	if c.DNS.UpstreamTimeout == 0 {
		c.DNS.UpstreamTimeout = 3
	}
	// When forwarding mode is selected with no upstreams, fall back to
	// privacy-first resolvers: Quad9 (non-profit, no-log, DNSSEC) and
	// DNS0.eu (European, GDPR, no-log). Intentionally excludes Google
	// and Cloudflare.
	if c.DNS.Mode == "forwarding" && len(c.DNS.UpstreamResolvers) == 0 {
		c.DNS.UpstreamResolvers = []string{"9.9.9.9", "149.112.112.112", "193.110.81.0", "185.253.5.0"}
	}
	if !c.DNS.Cache.Enabled && c.DNS.Cache.MaxEntries == 0 {
		c.DNS.Cache.Enabled = true
		c.DNS.Cache.MaxEntries = 10000
	}
	if c.Filtering.BlockPolicy == "" {
		c.Filtering.BlockPolicy = "nxdomain"
	}
	if c.Filtering.BlockPage.Port == 0 {
		c.Filtering.BlockPage.Port = 8053
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
	case "nxdomain", "null", "nodata", "redirect":
	default:
		return fmt.Errorf("filtering.block_policy must be nxdomain, null, nodata, or redirect; got %q", c.Filtering.BlockPolicy)
	}
	if c.DNS.Listen.Port < 1 || c.DNS.Listen.Port > 65535 {
		return fmt.Errorf("dns.listen.port must be 1–65535")
	}
	if c.API.Port < 1 || c.API.Port > 65535 {
		return fmt.Errorf("api.port must be 1–65535")
	}
	for i, u := range c.DNS.UpstreamResolvers {
		if _, err := NormaliseUpstream(u); err != nil {
			return fmt.Errorf("dns.upstream_resolvers[%d]: %w", i, err)
		}
	}
	for i, r := range c.DNS.UpstreamRoutes {
		if err := ValidateUpstreamRoute(r); err != nil {
			return fmt.Errorf("dns.upstream_routes[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateUpstreamRoute checks that a single UpstreamRoute is well-formed.
func ValidateUpstreamRoute(r UpstreamRoute) error {
	if r.Match == "" {
		return fmt.Errorf("match must not be empty")
	}
	if r.Match == "*" {
		return fmt.Errorf("bare wildcard match %q is not allowed — use *.suffix to restrict the scope", r.Match)
	}
	if len(r.Resolvers) == 0 {
		return fmt.Errorf("resolvers must not be empty")
	}
	for i, u := range r.Resolvers {
		if _, err := NormaliseUpstream(u); err != nil {
			return fmt.Errorf("resolvers[%d]: %w", i, err)
		}
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

// NormaliseUpstream validates and normalises a single upstream resolver address.
// Supported forms:
//   - Plain host or host:port   → appends :53 if no port
//   - tls://host[:port][?...]   → appends :853 if no port
//   - https://host/path[?...]   → returned unchanged
//
// Any other scheme returns an error. Exported for use by the settings handler.
func NormaliseUpstream(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if idx := strings.Index(s, "://"); idx >= 0 {
		scheme := s[:idx]
		switch scheme {
		case "tls":
			return normaliseTLSUpstream(s), nil
		case "https":
			return s, nil
		default:
			return "", fmt.Errorf("unsupported scheme %q (supported: tls://, https://, or plain host:port)", scheme)
		}
	}
	// Plain host[:port].
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s, nil
	}
	return s + ":53", nil
}

// normaliseUpstream is the unexported shim used by Defaults (no error path needed
// there because Validate catches bad schemes separately).
func normaliseUpstream(s string) string {
	n, _ := NormaliseUpstream(s)
	if n == "" {
		return s // unsupported scheme: leave as-is so Validate can report it
	}
	return n
}

// normaliseTLSUpstream appends :853 to a tls:// URL if the host has no port.
func normaliseTLSUpstream(s string) string {
	// s has the form "tls://host[:port][?query]"
	rest := s[len("tls://"):]
	hostPart := rest
	queryPart := ""
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		hostPart = rest[:q]
		queryPart = rest[q:]
	}
	if _, _, err := net.SplitHostPort(hostPart); err != nil {
		return "tls://" + hostPart + ":853" + queryPart
	}
	return s
}
