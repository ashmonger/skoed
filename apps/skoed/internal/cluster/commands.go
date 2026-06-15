package cluster

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/dhcp"
)

// CommandKind enumerates the FSM commands that move replicated state. Every
// mutation that must propagate cluster-wide is encoded as one of these,
// serialised as JSON, and appended to the Raft log.
type CommandKind string

const (
	CmdBlocklistUpsert      CommandKind = "blocklist.upsert"
	CmdBlocklistDelete      CommandKind = "blocklist.delete"
	CmdBlocklistSetEnabled  CommandKind = "blocklist.set_enabled"
	CmdAllowlistAdd         CommandKind = "allowlist.add"
	CmdAllowlistRemove      CommandKind = "allowlist.remove"
	CmdLocalDNSUpsert       CommandKind = "local_dns.upsert"
	CmdLocalDNSDelete       CommandKind = "local_dns.delete"
	CmdSettingsPatch        CommandKind = "settings.patch"
	CmdAuthSetCredentials   CommandKind = "auth.set_credentials"
	CmdTokenCreate          CommandKind = "token.create"
	CmdTokenConsume         CommandKind = "token.consume"
	CmdStatsCommitHour      CommandKind = "stats.commit_hour"
	CmdStatsPrune           CommandKind = "stats.prune"
	CmdConfigImport         CommandKind = "config.import"
	CmdMemberUpsert         CommandKind = "member.upsert"
	CmdMemberRemove         CommandKind = "member.remove"
	CmdClusterSecretSet     CommandKind = "cluster.secret_set"
	CmdProfileUpsert        CommandKind = "profile.upsert"
	CmdProfileDelete        CommandKind = "profile.delete"
	CmdScheduleUpsert       CommandKind = "schedule.upsert"
	CmdScheduleDelete       CommandKind = "schedule.delete"
	CmdScheduleBindingPut   CommandKind = "schedule_binding.upsert"
	CmdScheduleBindingDel   CommandKind = "schedule_binding.delete"
	CmdCategoryOverridePut  CommandKind = "category_override.upsert"
	// M5.2 — append a single audit row + lazy 90-day trim in the same commit.
	CmdAuditAppend          CommandKind = "audit.append"
	// M6 — curated DoH/DoT resolver IP snapshot.
	CmdDohResolverSnapshotReplace CommandKind = "doh_resolver.snapshot_replace"
	CmdDohResolverRefreshFailure  CommandKind = "doh_resolver.refresh_failure"
	// M6.5 — Raft-replicated DHCP lease cache (TS-LeaseRepl).
	CmdLeasesReplace      CommandKind = "leases.replace"
	CmdAnomalyAppend      CommandKind = "dhcp_anomaly.append"
	CmdAnomalyAcknowledge CommandKind = "dhcp_anomaly.acknowledge"
	CmdAnomalySweep       CommandKind = "dhcp_anomaly.sweep"
	// M7 — revocable, scoped API bearer tokens (TS-ApiToken).
	CmdAPITokenUpsert CommandKind = "api_token.upsert"
	CmdAPITokenDelete CommandKind = "api_token.delete"
	// M8 — DNSCrypt v2 keypair rotation (TS-EncryptedDnsExpansion).
	CmdDNSCryptKeysSet CommandKind = "dnscrypt.keys.set"
	// M13 — Filtering pause (TS-FilterPause).
	CmdGlobalPauseSet    CommandKind = "filter.global_pause.set"
	CmdGlobalPauseClear  CommandKind = "filter.global_pause.clear"
	CmdProfilePauseSet   CommandKind = "filter.profile_pause.set"
	CmdProfilePauseClear CommandKind = "filter.profile_pause.clear"
)

// Command is the wire form of a single FSM mutation. Payload is opaque JSON
// — its concrete type depends on Kind. We keep the version field to leave a
// clear upgrade path if the schema ever changes.
type Command struct {
	Kind    CommandKind     `json:"kind"`
	V       int             `json:"v"`
	Payload json.RawMessage `json:"payload"`
}

// Encode serialises a typed payload into a Command and then to bytes for the
// Raft log.
func Encode(kind CommandKind, payload any) ([]byte, error) {
	pb, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload for %s: %w", kind, err)
	}
	return json.Marshal(Command{Kind: kind, V: 1, Payload: pb})
}

// Payload shapes — one struct per command kind. All exported so other
// packages can construct them without going through interface{}.

type BlocklistUpsertPayload struct {
	Blocklist config.Blocklist `json:"blocklist"`
}

type BlocklistDeletePayload struct {
	ID string `json:"id"`
}

type BlocklistSetEnabledPayload struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type AllowlistAddPayload struct {
	Domain string `json:"domain"`
}

type AllowlistRemovePayload struct {
	Domain string `json:"domain"`
}

type LocalDNSUpsertPayload struct {
	Entry config.LocalDNSEntry `json:"entry"`
}

type LocalDNSDeletePayload struct {
	ID string `json:"id"`
}

type SettingsPatchPayload struct {
	DNS       *config.DNSConfig      `json:"dns,omitempty"`
	Filtering *FilteringPatch        `json:"filtering,omitempty"`
	QueryLog  *config.QueryLogConfig `json:"query_log,omitempty"`
}

type FilteringPatch struct {
	BlockPolicy     *string `json:"block_policy,omitempty"`
	PauseMaxSeconds *int    `json:"pause_max_seconds,omitempty"`
}

type AuthSetCredentialsPayload struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type TokenCreatePayload struct {
	TokenHash    string `json:"token_hash"`
	ExpiresUnix  int64  `json:"expires_unix"`
	CreatedBy    string `json:"created_by"`
	ConsumedUnix int64  `json:"consumed_unix,omitempty"` // zero = not yet consumed
}

type TokenConsumePayload struct {
	TokenHash  string `json:"token_hash"`
	ConsumedAt int64  `json:"consumed_at"`
}

type StatsCommitHourPayload struct {
	NodeID    string         `json:"node_id"`
	HourUnix  int64          `json:"hour_unix"`
	Aggregate HourAggregate  `json:"aggregate"`
}

type StatsPrunePayload struct {
	BeforeUnix int64 `json:"before_unix"`
}

type ConfigImportPayload struct {
	// Snapshot is a full M1 config used by the M1→M2 migration path. The FSM
	// rewrites every replicated bucket from this single command.
	Snapshot config.Config `json:"snapshot"`
}

type MemberUpsertPayload struct {
	NodeID      string `json:"node_id"`
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
	JoinedUnix  int64  `json:"joined_unix"`
}

type MemberRemovePayload struct {
	NodeID string `json:"node_id"`
}

// ClusterSecretSetPayload carries the cluster-wide shared secret used by
// follower nodes to authenticate internal cluster-to-cluster requests
// (e.g. follower forwarding its hourly aggregate to the leader). Generated
// once at first bootstrap and replicated to every node via Raft.
type ClusterSecretSetPayload struct {
	Secret string `json:"secret"`
}

// ─── M3 payloads ────────────────────────────────────────────────────────────

type ProfileUpsertPayload struct {
	Profile config.Profile `json:"profile"`
}

type ProfileDeletePayload struct {
	ID string `json:"id"`
}

type ScheduleUpsertPayload struct {
	Schedule config.Schedule `json:"schedule"`
}

type ScheduleDeletePayload struct {
	ID string `json:"id"`
}

type ScheduleBindingPutPayload struct {
	Binding config.ScheduleBinding `json:"binding"`
}

type ScheduleBindingDelPayload struct {
	ScheduleID  string `json:"schedule_id"`
	ProfileID   string `json:"profile_id"`
	BlocklistID string `json:"blocklist_id"`
}

type CategoryOverridePutPayload struct {
	Override config.CategoryOverride `json:"override"`
}

// AuditAppendPayload is the Raft-replicated form of a single audit row.
// Fields mirror specs/technical/audit-log.md exactly. Seq is filled in
// at Apply time so every node assigns the same sequence value to the
// same Raft log entry.
type AuditAppendPayload struct {
	ID         string `json:"id"`
	TimeUnix   int64  `json:"time_unix"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	Target     string `json:"target,omitempty"`
	Result     string `json:"result"` // "ok" | "error"
	Error      string `json:"error,omitempty"`
	Diff       string `json:"diff,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

// DohResolverSnapshotReplacePayload carries the full replicated snapshot
// document. The shape mirrors dohresolvers.Snapshot 1:1 (duplicated here
// so this file does not import the dohresolvers package; the dedicated
// doh_resolvers.go apply helpers do that conversion at the bbolt edge).
type DohResolverSnapshotReplacePayload struct {
	SnapshotID           string                    `json:"snapshot_id"`
	SourceURL            string                    `json:"source_url"`
	FetchedAt            string                    `json:"fetched_at"`
	LastRefreshAttemptAt string                    `json:"last_refresh_attempt_at"`
	LastRefreshSuccessAt string                    `json:"last_refresh_success_at"`
	LastRefreshError     string                    `json:"last_refresh_error"`
	Resolvers            []DohResolverEntryPayload `json:"resolvers"`
}

// DohResolverRefreshFailurePayload carries only the failure metadata —
// the snapshot blob itself is left untouched.
type DohResolverRefreshFailurePayload struct {
	AttemptedAt string `json:"attempted_at"`
	Error       string `json:"error"`
}

// DohResolverEntryPayload mirrors dohresolvers.ResolverEntry.
type DohResolverEntryPayload struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
	SourceURL string   `json:"source_url"`
}

// ─── M6.5 payloads (TS-LeaseRepl) ───────────────────────────────────────────

// LeasesReplacePayload carries the full canonical lease snapshot from the
// leader's most recent poll. Followers overwrite their `dhcp/snapshot`
// bucket atomically.
type LeasesReplacePayload struct {
	LeaderNodeID  string       `json:"leader_node_id"`
	ConnectorKind string       `json:"connector_kind"`
	SourceURL     string       `json:"source_url,omitempty"`
	PollUnix      int64        `json:"poll_unix"`
	Leases        []dhcp.Lease `json:"leases"`
}

// AnomalyAppendPayload replicates one anti-spoof anomaly to every node.
type AnomalyAppendPayload struct {
	Anomaly dhcp.Anomaly `json:"anomaly"`
}

// AnomalyAckPayload marks a replicated anomaly acknowledged.
type AnomalyAckPayload struct {
	ID               string `json:"id"`
	AcknowledgedUnix int64  `json:"acknowledged_unix"`
}

// AnomalySweepPayload deletes anomalies older than BeforeUnix.
type AnomalySweepPayload struct {
	BeforeUnix int64 `json:"before_unix"`
}

// HourAggregate is the per-node hourly aggregate written by the
// stats-commit-hour FSM command and replicated to every node.
type HourAggregate struct {
	NodeID      string         `json:"node_id"`
	HourStart   int64          `json:"hour_start"`
	Total       int            `json:"total"`
	Blocked     int            `json:"blocked"`
	Forwarded   int            `json:"forwarded"`
	Cached      int            `json:"cached"`
	Local       int            `json:"local"`
	TopDomains  []NameCount    `json:"top_domains"`
	TopClients  []NameCount    `json:"top_clients"`
}

// NameCount is a (label, count) pair used inside HourAggregate for top-N
// domain/client breakdowns.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ─── M13 payloads (TS-FilterPause) ──────────────────────────────────────────

type GlobalPauseSetPayload struct {
	ResumesAt  time.Time `json:"resumes_at"`
	Reason     string    `json:"reason,omitempty"`
	ProfileIDs []string  `json:"profile_ids,omitempty"`
}

type ProfilePauseSetPayload struct {
	ProfileID string    `json:"profile_id"`
	ResumesAt time.Time `json:"resumes_at"`
	Reason    string    `json:"reason,omitempty"`
}

type ProfilePauseClearPayload struct {
	ProfileID string `json:"profile_id"`
}
