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
	CmdSharedAllowlistUpsert CommandKind = "shared_allowlist.upsert" // M36
	CmdSharedAllowlistDelete CommandKind = "shared_allowlist.delete" // M36
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
	// M20 — mTLS certificate rotation (TS-ClusterSecurityHardening).
	CmdCertRotation CommandKind = "cert_rotation"
	// M22 — replicated webhook endpoint list.
	CmdWebhooksUpdate CommandKind = "webhooks.update"
	// M23.5 — built-in DHCP server settings and static assignments.
	CmdDhcpServerSettingsSet        CommandKind = "dhcp_server.settings.set"
	CmdDhcpStaticAssignmentUpsert   CommandKind = "dhcp_server.static.upsert"
	CmdDhcpStaticAssignmentDelete   CommandKind = "dhcp_server.static.delete"
	// M30 — DHCPv4 dynamic lease persistence.
	CmdDhcpServerLeasesUpsert CommandKind = "dhcp_server.leases.upsert"
	CmdDhcpServerLeaseDelete  CommandKind = "dhcp_server.leases.delete"
	// M30.5 — cluster-wide custom filtering rules (TS-CustomRules).
	CmdCustomRulesSet CommandKind = "custom_rules.set"
	// M30 — DHCPv6 server settings, static assignments, and lease persistence.
	CmdDhcp6ServerSettingsSet      CommandKind = "dhcp6_server.settings.set"
	CmdDhcp6StaticAssignmentUpsert CommandKind = "dhcp6_server.static.upsert"
	CmdDhcp6StaticAssignmentDelete CommandKind = "dhcp6_server.static.delete"
	CmdDhcp6ServerLeasesUpsert     CommandKind = "dhcp6_server.leases.upsert"
	CmdDhcp6ServerLeaseDelete      CommandKind = "dhcp6_server.leases.delete"
	// M34 — TLS auto-renewal config + per-node cert rotation.
	CmdTLSRenewConfigSet CommandKind = "tls_renew.config.set"
	CmdRotateNodeCert    CommandKind = "cert.node.rotate"
	// M35.5 — named device registry.
	CmdDeviceUpsert CommandKind = "device.upsert"
	CmdDeviceDelete CommandKind = "device.delete"
	// M35 — pause history append and new-dynamic-client dismiss.
	CmdPauseHistoryAppend        CommandKind = "pause_history.append"
	CmdNewDynamicClientDismiss   CommandKind = "new_dynamic_client.dismiss"
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
	// Domain is always required.
	Domain string `json:"domain"`
	// M36: optional per-entry metadata.
	ExpiresAt  *int64 `json:"expires_at,omitempty"`  // Unix seconds; nil = no expiry
	Note       string `json:"note,omitempty"`
	ScheduleID string `json:"schedule_id,omitempty"`
	// ProfileID, if set, targets a per-profile allowlist; empty = global allowlist.
	ProfileID string `json:"profile_id,omitempty"`
}

type AllowlistRemovePayload struct {
	Domain    string `json:"domain"`
	ProfileID string `json:"profile_id,omitempty"` // empty = global allowlist
}

// SharedAllowlistUpsertPayload carries a full SharedAllowlist for Raft replication (M36).
type SharedAllowlistUpsertPayload struct {
	List config.SharedAllowlist `json:"list"`
}

// SharedAllowlistDeletePayload removes a shared allowlist by ID (M36).
type SharedAllowlistDeletePayload struct {
	ID string `json:"id"`
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
	Username              string `json:"username"`
	PasswordHash          string `json:"password_hash"`
	SessionTimeoutSeconds int    `json:"session_timeout_seconds,omitempty"`
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
	StartedAt time.Time `json:"started_at"`
	ResumesAt time.Time `json:"resumes_at"`
	Reason    string    `json:"reason,omitempty"`
	ClientIPs []string  `json:"client_ips,omitempty"`
}

type ProfilePauseClearPayload struct {
	ProfileID string    `json:"profile_id"`
	EndedAt   time.Time `json:"ended_at"`
}

// ─── M20 payloads (TS-ClusterSecurityHardening) ─────────────────────────────

// NodeCerts holds the new leaf cert+key PEMs for a single node.
type NodeCerts struct {
	CertPEM []byte `json:"cert_pem"`
	KeyPEM  []byte `json:"key_pem"`
}

// CertRotationPayload carries a new CA and per-node leaf certs for
// cluster-wide mTLS certificate rotation. Keyed by node ID.
type CertRotationPayload struct {
	CACertPEM []byte               `json:"ca_cert_pem"`
	Nodes     map[string]NodeCerts `json:"nodes"`
}

// WebhooksUpdatePayload replaces the full webhook endpoint list atomically.
type WebhooksUpdatePayload struct {
	Webhooks []config.WebhookEndpoint `json:"webhooks"`
}

// DhcpServerSettingsSetPayload carries the full DHCPServerConfig (minus
// StaticAssignments, which have their own commands).
type DhcpServerSettingsSetPayload struct {
	Settings config.DHCPServerConfig `json:"settings"`
}

// DhcpStaticAssignmentUpsertPayload adds or replaces a static assignment.
type DhcpStaticAssignmentUpsertPayload struct {
	Assignment config.DHCPStaticAssignment `json:"assignment"`
}

// DhcpStaticAssignmentDeletePayload removes the assignment for the given MAC.
type DhcpStaticAssignmentDeletePayload struct {
	MAC string `json:"mac"`
}

// ─── M30 payloads ─────────────────────────────────────────────────────────────

// DhcpServerLeaseUpsertPayload persists one DHCPv4 dynamic lease via Raft.
type DhcpServerLeaseUpsertPayload struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Hostname  string `json:"hostname"`
	ExpiresAt int64  `json:"expires_at"` // Unix seconds
}

// DhcpServerLeaseDeletePayload removes one DHCPv4 dynamic lease from bbolt.
type DhcpServerLeaseDeletePayload struct {
	IP string `json:"ip"`
}

// Dhcp6ServerSettingsSetPayload carries the full DHCPv6 server configuration.
type Dhcp6ServerSettingsSetPayload struct {
	Settings config.DHCPv6ServerConfig `json:"settings"`
}

// Dhcp6StaticAssignmentUpsertPayload adds or replaces a DHCPv6 static DUID→address entry.
type Dhcp6StaticAssignmentUpsertPayload struct {
	Assignment config.Dhcp6StaticAssignment `json:"assignment"`
}

// Dhcp6StaticAssignmentDeletePayload removes the DHCPv6 static assignment for a DUID.
type Dhcp6StaticAssignmentDeletePayload struct {
	DUID string `json:"duid"`
}

// Dhcp6ServerLeaseUpsertPayload persists one DHCPv6 dynamic lease via Raft.
type Dhcp6ServerLeaseUpsertPayload struct {
	Address   string `json:"address"`
	DUID      string `json:"duid"`
	Hostname  string `json:"hostname"`
	ProfileID string `json:"profile_id,omitempty"`
	ExpiresAt int64  `json:"expires_at"` // Unix seconds
}

// Dhcp6ServerLeaseDeletePayload removes one DHCPv6 dynamic lease from bbolt.
type Dhcp6ServerLeaseDeletePayload struct {
	Address string `json:"address"`
}

// ─── M30.5 payloads (TS-CustomRules) ─────────────────────────────────────────

// CustomRulesSetPayload replaces the full cluster-wide custom rules text.
type CustomRulesSetPayload struct {
	Rules string `json:"rules"`
}

// ─── M34 payloads (TS-CertificateManagement) ─────────────────────────────────

// TLSRenewConfigSetPayload replaces the cluster-wide TLS auto-renewal settings.
type TLSRenewConfigSetPayload struct {
	Config config.TLSRenewConfig `json:"config"`
}

// RotateNodeCertPayload carries a new leaf cert for a single node.
// The CA cert is included so each node can update its CA trust store.
type RotateNodeCertPayload struct {
	CACertPEM []byte `json:"ca_cert_pem"`
	NodeID    string `json:"node_id"`
	CertPEM   []byte `json:"cert_pem"`
	KeyPEM    []byte `json:"key_pem"`
}

// ─── M35.5 payloads (TS-DeviceRegistry) ──────────────────────────────────────

type DeviceUpsertPayload struct {
	Device config.Device `json:"device"`
}

type DeviceDeletePayload struct {
	ID string `json:"id"`
}

// ─── M35 payloads (TS-FilteringPauseEnhancements) ────────────────────────────

// PauseHistoryAppendPayload appends one history entry for a profile.
type PauseHistoryAppendPayload struct {
	ProfileID string                   `json:"profile_id"`
	Entry     config.PauseHistoryEntry `json:"entry"`
}

// NewDynamicClientDismissPayload dismisses an IP from the new-dynamic alert
// list. DismissedAt is captured by the leader (never read inside FSM Apply) so
// the replicated tombstone timestamp is identical on every node.
type NewDynamicClientDismissPayload struct {
	ClientIP    string    `json:"client_ip"`
	DismissedAt time.Time `json:"dismissed_at"`
}
