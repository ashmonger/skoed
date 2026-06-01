package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/dblock/dblock/internal/config"
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
	BlockPolicy *string `json:"block_policy,omitempty"`
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
