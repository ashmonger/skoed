// Package dohresolvers owns the M6 curated DoH/DoT resolver IP snapshot
// (TS-DohResolverDb).
//
// One snapshot per cluster. The leader refreshes it from a tracked
// upstream URL (configurable; bundled seed used on cold boot) and
// replicates the result through Raft. Every node converges to the same
// bytes so downstream consumers (TS-FirewallRuleGenerator) can rely on
// a single authoritative view of public DoH/DoT IPs.
package dohresolvers

// ResolverEntry is one provider's row in the snapshot. IPv4 OR IPv6
// MUST be non-empty (per FS-DohResolverDbResolverEntryShape); both
// arrays may be present for dual-stack providers.
type ResolverEntry struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
	SourceURL string   `json:"source_url"`
}

// Snapshot is the persisted+replicated document. Stored as a single
// JSON value under bbolt key doh_resolvers/snapshot.
type Snapshot struct {
	SnapshotID            string          `json:"snapshot_id"`
	SourceURL             string          `json:"source_url"`
	FetchedAt             string          `json:"fetched_at"`              // RFC3339
	LastRefreshAttemptAt  string          `json:"last_refresh_attempt_at"` // RFC3339
	LastRefreshSuccessAt  string          `json:"last_refresh_success_at"` // RFC3339
	LastRefreshError      string          `json:"last_refresh_error"`
	Resolvers             []ResolverEntry `json:"resolvers"`
}
