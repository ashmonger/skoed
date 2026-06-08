// Package firewallrules implements TS-FwRule — paste-ready firewall
// rule generation for blocking outbound traffic to known public
// DoH/DoT resolvers. Five rendering backends share one in-memory plan
// model; the HTTP handler picks the renderer keyed by the platform
// query parameter.
//
// Skoed never touches netfilter, nft, the MikroTik API, the OpnSense
// API, or the UniFi controller. The output is plain text the operator
// pastes into their firewall themselves.
package firewallrules

import (
	"time"
)

// Platform enumerates the supported firewall syntaxes.
type Platform string

const (
	PlatformIptables Platform = "iptables"
	PlatformNftables Platform = "nftables"
	PlatformMikrotik Platform = "mikrotik"
	PlatformOpnsense Platform = "opnsense"
	PlatformUnifi    Platform = "unifi"
)

// SupportedPlatforms is the public enumeration echoed back in the
// 400 "unsupported platform" error envelope.
func SupportedPlatforms() []string {
	return []string{
		string(PlatformIptables),
		string(PlatformNftables),
		string(PlatformMikrotik),
		string(PlatformOpnsense),
		string(PlatformUnifi),
	}
}

// ParsePlatform returns the matching Platform or (_, false) when the
// value is not in the supported enum.
func ParsePlatform(raw string) (Platform, bool) {
	switch Platform(raw) {
	case PlatformIptables, PlatformNftables, PlatformMikrotik, PlatformOpnsense, PlatformUnifi:
		return Platform(raw), true
	}
	return "", false
}

// Scope enumerates the source-set selectors.
type Scope string

const (
	ScopeAll     Scope = "all"
	ScopeSubnet  Scope = "subnet"
	ScopeProfile Scope = "profile"
)

// ParseScope returns the matching Scope or (_, false) when the value
// is not in the supported enum.
func ParseScope(raw string) (Scope, bool) {
	switch Scope(raw) {
	case ScopeAll, ScopeSubnet, ScopeProfile:
		return Scope(raw), true
	}
	return "", false
}

// Action enumerates the rule verdicts.
type Action string

const (
	ActionDrop   Action = "drop"
	ActionReject Action = "reject"
)

// ParseAction returns the matching Action or (_, false). Empty input
// resolves to the default ActionDrop.
func ParseAction(raw string) (Action, bool) {
	if raw == "" {
		return ActionDrop, true
	}
	switch Action(raw) {
	case ActionDrop, ActionReject:
		return Action(raw), true
	}
	return "", false
}

// ResolverEntry is the renderer-facing view of one provider row.
// Mirrors dohresolvers.ResolverEntry but lives here so the package
// stays decoupled from the snapshot store.
type ResolverEntry struct {
	ID   string
	Name string
	IPv4 []string
	IPv6 []string
}

// SnapshotMeta records the provenance fields the leading comment
// block exposes (FS-FwRuleHeaderCarriesSnapshotProvenance).
type SnapshotMeta struct {
	ID            string
	FetchedAt     time.Time
	ResolverCount int
	Stale         bool
}

// Plan is the in-memory description handed to the per-platform
// renderer. Sources empty == "any source" (scope=all).
type Plan struct {
	Scope       Scope
	ScopeLabel  string // human-readable scope tag for the header (e.g. "subnet=10.0.0.0/24")
	Sources     []string
	Resolvers   []ResolverEntry
	Action      Action
	Snapshot    SnapshotMeta
	GeneratedAt time.Time
}

// Renderer is the contract each platform implementation satisfies.
type Renderer interface {
	Render(Plan) string
}
