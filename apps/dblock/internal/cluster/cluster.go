package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dblock/dblock/internal/config"
	"github.com/hashicorp/raft"
)

// ApplyTimeout is the default deadline for a single Raft apply.
const ApplyTimeout = 5 * time.Second

// Cluster is the top-level orchestrator owning bbolt + Raft + members + tokens
// and exposing typed mutation methods to the rest of the program. All writes
// route through Raft so every node sees the same state.
type Cluster struct {
	node   *NodeYAML
	store  *Store
	raft   *raftNode
	logger raftLoggerFn

	mu          sync.Mutex
	subscribers []func()

	// applyCounter increments after every committed FSM apply. Tests and the
	// shadow YAML writer use it to detect "something changed".
	applyCounter atomic.Uint64
}

// raftLoggerFn allows the test harness to suppress raft chatter.
type raftLoggerFn func(string, ...any)

// Options control optional knobs at New time. Zero value is fine for prod.
type Options struct {
	// Bootstrap controls the initial Raft state. If true, the node creates a
	// fresh single-node configuration on first boot. Ignored when an existing
	// Raft log is detected.
	Bootstrap bool
	// SuppressRaftLog hides hashicorp/raft's verbose stderr output. Useful in
	// tests; production should leave it enabled.
	SuppressRaftLog bool
}

// New opens the bbolt store, starts the Raft node, and returns a ready
// Cluster. Caller must Close to release file locks.
func New(node *NodeYAML, opts Options) (*Cluster, error) {
	storePath := node.DataPath("cluster.bbolt")
	store, err := OpenStore(storePath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	c := &Cluster{node: node, store: store}

	// Decide bootstrap vs rejoin: only bootstrap if no Raft state exists AND
	// the caller asked us to. A node joining an existing cluster passes
	// Bootstrap=false.
	wantBootstrap := opts.Bootstrap && !hasExistingRaftState(node.Node.DataDir)

	f := newFSM(store, func() {
		c.applyCounter.Add(1)
		c.fireSubscribers()
	})

	var logWriter = raftLogger()
	if opts.SuppressRaftLog {
		logWriter = nil
	}

	rn, err := newRaftNode(raftOptions{
		NodeID:        node.Node.ID,
		BindAddr:      node.Node.RaftAddress,
		AdvertiseAddr: node.Node.RaftAddress,
		DataDir:       node.Node.DataDir,
		Bootstrap:     wantBootstrap,
		Logger:        logWriter,
	}, f)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("start raft: %w", err)
	}

	c.raft = rn

	// If we bootstrapped, register ourselves in the replicated members bucket
	// once Raft elects us leader (which happens immediately for a single-node).
	if wantBootstrap {
		go c.registerSelfAfterLeader()
	}

	return c, nil
}

// Close shuts down Raft and the bbolt store.
func (c *Cluster) Close() error {
	if c.raft != nil {
		_ = c.raft.Shutdown()
	}
	return c.store.Close()
}

// Subscribe registers fn to be called after every committed FSM apply on this
// node (whether the apply originated locally or via replication). Used by the
// filter engine rebuild and the shadow YAML writer.
func (c *Cluster) Subscribe(fn func()) {
	c.mu.Lock()
	c.subscribers = append(c.subscribers, fn)
	c.mu.Unlock()
}

func (c *Cluster) fireSubscribers() {
	c.mu.Lock()
	subs := make([]func(), len(c.subscribers))
	copy(subs, c.subscribers)
	c.mu.Unlock()
	for _, s := range subs {
		s()
	}
}

// ApplyCount returns the number of FSM applies committed on this node since
// process start. Used by the shadow YAML writer to debounce-by-counter.
func (c *Cluster) ApplyCount() uint64 { return c.applyCounter.Load() }

// Store exposes the underlying typed store. Callers should prefer the typed
// methods on Cluster; Store is for read paths and the export/handlers code
// that needs to enumerate buckets directly.
func (c *Cluster) Store() *Store { return c.store }

// Node returns the node's local config.
func (c *Cluster) Node() *NodeYAML { return c.node }

// Raft returns the underlying raftNode. Used by the cluster status handler
// and the membership operations.
func (c *Cluster) Raft() *raftNode { return c.raft }

// IsLeader reports whether this node is the current Raft leader.
func (c *Cluster) IsLeader() bool { return c.raft.IsLeader() }

// LeaderAPIAddress returns the HTTP base URL of the current leader (e.g.
// "http://192.168.1.10:8080") or "" if no leader is yet known. Looks up the
// leader's NodeID from Raft and resolves its api_address from the replicated
// members bucket.
func (c *Cluster) LeaderAPIAddress() string {
	_, id := c.raft.Leader()
	if id == "" {
		return ""
	}
	m, err := c.store.MemberByID(string(id))
	if err != nil || m == nil {
		return ""
	}
	return apiBaseURL(m.APIAddress)
}

// LeaderID returns the current leader's NodeID, or "" if unknown.
func (c *Cluster) LeaderID() string {
	_, id := c.raft.Leader()
	return string(id)
}

// WaitForLeader blocks until any node has been elected leader or timeout.
func (c *Cluster) WaitForLeader(timeout time.Duration) error {
	return c.raft.WaitForLeader(timeout)
}

// ============================================================================
// Typed mutation methods. Each encodes a Command and applies it via Raft.
// Followers MUST forward to the leader first (see middleware/forward.go) —
// these methods assume they're being called on the leader.
// ============================================================================

// ErrNotLeader is returned when a mutation is attempted on a non-leader.
// The forwarding middleware uses this to redirect; callers that hit this
// directly should surface a 503 with LeaderRedirect.
var ErrNotLeader = errors.New("not the raft leader")

func (c *Cluster) requireLeader() error {
	if !c.raft.IsLeader() {
		return ErrNotLeader
	}
	return nil
}

// applyAsLeader is the common path for every typed mutation method: require
// leadership, then Raft-apply the encoded command. A zero timeout falls back
// to ApplyTimeout; callers with unusually large payloads (e.g. ConfigImport)
// can override.
func (c *Cluster) applyAsLeader(kind CommandKind, payload any, timeout time.Duration) error {
	if err := c.requireLeader(); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = ApplyTimeout
	}
	return c.raft.ApplyCommand(kind, payload, timeout)
}

// UpsertBlocklist commits the full blocklist (create or replace).
func (c *Cluster) UpsertBlocklist(bl config.Blocklist) error {
	return c.applyAsLeader(CmdBlocklistUpsert, BlocklistUpsertPayload{Blocklist: bl}, 0)
}

// DeleteBlocklist removes a blocklist by id.
func (c *Cluster) DeleteBlocklist(id string) error {
	return c.applyAsLeader(CmdBlocklistDelete, BlocklistDeletePayload{ID: id}, 0)
}

// SetBlocklistEnabled toggles the enabled flag on an existing blocklist.
func (c *Cluster) SetBlocklistEnabled(id string, enabled bool) error {
	return c.applyAsLeader(CmdBlocklistSetEnabled, BlocklistSetEnabledPayload{ID: id, Enabled: enabled}, 0)
}

// AddAllowlistEntry adds a domain to the allowlist.
func (c *Cluster) AddAllowlistEntry(domain string) error {
	return c.applyAsLeader(CmdAllowlistAdd, AllowlistAddPayload{Domain: domain}, 0)
}

// RemoveAllowlistEntry removes a domain from the allowlist.
func (c *Cluster) RemoveAllowlistEntry(domain string) error {
	return c.applyAsLeader(CmdAllowlistRemove, AllowlistRemovePayload{Domain: domain}, 0)
}

// UpsertLocalDNS commits a local DNS entry.
func (c *Cluster) UpsertLocalDNS(entry config.LocalDNSEntry) error {
	return c.applyAsLeader(CmdLocalDNSUpsert, LocalDNSUpsertPayload{Entry: entry}, 0)
}

// DeleteLocalDNS removes a local DNS entry by id.
func (c *Cluster) DeleteLocalDNS(id string) error {
	return c.applyAsLeader(CmdLocalDNSDelete, LocalDNSDeletePayload{ID: id}, 0)
}

// PatchSettings applies a partial settings update.
func (c *Cluster) PatchSettings(p SettingsPatchPayload) error {
	return c.applyAsLeader(CmdSettingsPatch, p, 0)
}

// SetCredentials writes admin credentials (username + bcrypt hash).
func (c *Cluster) SetCredentials(username, passwordHash string) error {
	return c.applyAsLeader(CmdAuthSetCredentials,
		AuthSetCredentialsPayload{Username: username, PasswordHash: passwordHash}, 0)
}

// ImportFromM1 replays a full M1 config snapshot into bbolt as a single
// atomic FSM command. Used only by the migration path; the larger payload
// warrants a longer apply timeout than the default.
func (c *Cluster) ImportFromM1(snapshot config.Config) error {
	return c.applyAsLeader(CmdConfigImport, ConfigImportPayload{Snapshot: snapshot}, 10*time.Second)
}

// CommitHourlyAggregate writes a hourly aggregate to the replicated stats
// bucket. Called by the aggregator goroutine on every node — each node owns
// its own per-hour key under stats/{node_id}/{hour_unix}.
//
// On the leader, the aggregate is applied directly via Raft. On followers,
// the call is forwarded over HTTP to the leader's internal aggregates
// endpoint, authenticated with the replicated cluster secret. This keeps
// every node's stats visible cluster-wide without giving followers raw
// admin credentials.
func (c *Cluster) CommitHourlyAggregate(agg HourAggregate) error {
	if c.raft.IsLeader() {
		return c.applyAggregateLocal(agg)
	}
	return c.forwardAggregate(agg)
}

// applyAggregateLocal commits the aggregate via Raft. Caller must have
// established that this node is the leader (or be the internal endpoint
// handler that already validated the cluster secret and confirmed
// leadership at handler entry).
func (c *Cluster) applyAggregateLocal(agg HourAggregate) error {
	return c.raft.ApplyCommand(CmdStatsCommitHour, StatsCommitHourPayload{
		NodeID:    agg.NodeID,
		HourUnix:  agg.HourStart,
		Aggregate: agg,
	}, ApplyTimeout)
}

// ApplyForwardedAggregate is the entry point used by the internal HTTP
// endpoint after it has validated the cluster secret. It refuses if this
// node is no longer the leader so a stale forward doesn't accidentally
// apply on a former-leader.
func (c *Cluster) ApplyForwardedAggregate(agg HourAggregate) error {
	if err := c.requireLeader(); err != nil {
		return err
	}
	return c.applyAggregateLocal(agg)
}

// forwardAggregate POSTs the aggregate to the current leader's internal
// endpoint, signed with the cluster secret. Returns errors so the
// aggregator's retry loop can back off on transient failures.
func (c *Cluster) forwardAggregate(agg HourAggregate) error {
	leaderURL := c.LeaderAPIAddress()
	if leaderURL == "" {
		return fmt.Errorf("no leader currently elected")
	}
	secret, err := c.store.ClusterSecret()
	if err != nil {
		return fmt.Errorf("read cluster secret: %w", err)
	}
	if secret == "" {
		return fmt.Errorf("cluster secret not yet initialised")
	}

	body, err := json.Marshal(agg)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, leaderURL+"/api/v1/cluster/_internal/aggregates", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Secret", secret)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post aggregate to leader: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("leader rejected aggregate (status %d): %s", resp.StatusCode, string(b))
	}
	return nil
}

// ValidateClusterSecret returns true if the supplied secret matches the
// replicated cluster secret. Used by the internal endpoint handler to
// authenticate peer-to-peer requests without exposing admin credentials.
func (c *Cluster) ValidateClusterSecret(s string) bool {
	if s == "" {
		return false
	}
	stored, err := c.store.ClusterSecret()
	if err != nil || stored == "" {
		return false
	}
	// Constant-time compare avoids leaking the secret length to timing.
	if len(stored) != len(s) {
		return false
	}
	var diff byte
	for i := 0; i < len(s); i++ {
		diff |= s[i] ^ stored[i]
	}
	return diff == 0
}

// EnsureClusterSecret generates and replicates a fresh cluster secret if
// the meta bucket doesn't already contain one. Idempotent. Called from
// main.go after bootstrap so the secret is available before the aggregator
// starts forwarding.
func (c *Cluster) EnsureClusterSecret() error {
	if !c.raft.IsLeader() {
		// Followers wait for the leader's Raft entry to replicate; no-op.
		return nil
	}
	existing, err := c.store.ClusterSecret()
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}
	secret, err := GenerateToken() // 32-byte hex; reused for convenience
	if err != nil {
		return err
	}
	return c.raft.ApplyCommand(CmdClusterSecretSet, ClusterSecretSetPayload{Secret: secret}, ApplyTimeout)
}

// PruneAggregatesBefore deletes hourly aggregates older than the given unix
// timestamp.
func (c *Cluster) PruneAggregatesBefore(beforeUnix int64) error {
	return c.applyAsLeader(CmdStatsPrune, StatsPrunePayload{BeforeUnix: beforeUnix}, 0)
}

// ─── M3 typed mutation methods ──────────────────────────────────────────

// UpsertProfile creates or replaces a profile.
func (c *Cluster) UpsertProfile(p config.Profile) error {
	return c.applyAsLeader(CmdProfileUpsert, ProfileUpsertPayload{Profile: p}, 0)
}

// DeleteProfile removes a profile by id. Cannot delete "default".
func (c *Cluster) DeleteProfile(id string) error {
	return c.applyAsLeader(CmdProfileDelete, ProfileDeletePayload{ID: id}, 0)
}

// UpsertSchedule creates or replaces a schedule.
func (c *Cluster) UpsertSchedule(s config.Schedule) error {
	return c.applyAsLeader(CmdScheduleUpsert, ScheduleUpsertPayload{Schedule: s}, 0)
}

// DeleteSchedule removes a schedule by id; cascades to its bindings.
func (c *Cluster) DeleteSchedule(id string) error {
	return c.applyAsLeader(CmdScheduleDelete, ScheduleDeletePayload{ID: id}, 0)
}

// UpsertScheduleBinding attaches one schedule to a (profile, blocklist) pair.
func (c *Cluster) UpsertScheduleBinding(b config.ScheduleBinding) error {
	return c.applyAsLeader(CmdScheduleBindingPut, ScheduleBindingPutPayload{Binding: b}, 0)
}

// DeleteScheduleBinding detaches a schedule from a (profile, blocklist) pair.
func (c *Cluster) DeleteScheduleBinding(scheduleID, profileID, blocklistID string) error {
	return c.applyAsLeader(CmdScheduleBindingDel, ScheduleBindingDelPayload{
		ScheduleID:  scheduleID,
		ProfileID:   profileID,
		BlocklistID: blocklistID,
	}, 0)
}

// UpsertCategoryOverride records an operator's URL/format override for a
// named built-in category.
func (c *Cluster) UpsertCategoryOverride(o config.CategoryOverride) error {
	return c.applyAsLeader(CmdCategoryOverridePut, CategoryOverridePutPayload{Override: o}, 0)
}

// EnsureDefaultProfile creates the reserved "default" profile if missing.
// Idempotent. Called from main.go on bootstrap so a fresh cluster always
// has a fallback profile for unassigned clients.
func (c *Cluster) EnsureDefaultProfile() error {
	if !c.raft.IsLeader() {
		return nil
	}
	snap, err := c.store.Snapshot()
	if err != nil {
		return err
	}
	for _, p := range snap.Profiles {
		if p.ID == "default" {
			return nil
		}
	}
	return c.UpsertProfile(config.Profile{
		ID:   "default",
		Name: "Default",
	})
}

// EnsureDohCategoryOnDefaultProfile creates the cat:doh blocklist (seeded
// from the bundled DoH-resolver list) and ensures the default profile's
// blocklists include it. Idempotent.
//
// We avoid importing internal/filter/categories here (cluster ↔ filter is
// the wrong direction). The bundled DoH domain list is passed in by main.go
// from the categories package — keeps the dependency arrow pointing the
// right way.
func (c *Cluster) EnsureDohCategoryOnDefaultProfile(bundledDoH []string) error {
	if !c.raft.IsLeader() {
		return nil
	}
	snap, err := c.store.Snapshot()
	if err != nil {
		return err
	}
	const dohID = "cat:doh"

	// Find or create the blocklist.
	var existing *config.Blocklist
	for i := range snap.Filtering.Blocklists {
		if snap.Filtering.Blocklists[i].ID == dohID {
			existing = &snap.Filtering.Blocklists[i]
			break
		}
	}
	if existing == nil {
		bl := config.Blocklist{
			ID:      dohID,
			Name:    "DoH/DoT resolvers (bundled)",
			Enabled: true,
			Source:  config.BlocklistSource{Type: "inline", Format: "domainlist"},
			Domains: bundledDoH,
			Managed: true,
		}
		if err := c.UpsertBlocklist(bl); err != nil {
			return err
		}
	}

	// Attach to default profile.
	for _, p := range snap.Profiles {
		if p.ID != "default" {
			continue
		}
		for _, b := range p.Blocklists {
			if b == dohID {
				return nil // already attached
			}
		}
		p.Blocklists = append(p.Blocklists, dohID)
		return c.UpsertProfile(p)
	}
	// Default profile not present yet (EnsureDefaultProfile not yet run).
	return nil
}

// ============================================================================
// Token issuance & enrollment. Tokens are stored hashed in the replicated
// tokens bucket so any node can validate one regardless of which node issued
// it. The plaintext token is returned to the caller exactly once.
// ============================================================================

// TokenIssueResult is the response to a CreateJoinToken call.
type TokenIssueResult struct {
	Token         string
	ExpiresAt     time.Time
	LeaderAddress string
}

// CreateJoinToken generates a fresh single-use join token and commits its
// hash via Raft. Returns the plaintext token (shown to caller once) plus
// metadata. TTL respects DBLOCK_TEST_TOKEN_TTL_SECONDS when DBLOCK_TEST_MODE=1.
func (c *Cluster) CreateJoinToken(issuedBy string) (*TokenIssueResult, error) {
	if err := c.requireLeader(); err != nil {
		return nil, err
	}
	plaintext, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	ttl := resolveTokenTTL()
	expiresAt := time.Now().Add(ttl)
	hash := HashToken(plaintext)
	if err := c.raft.ApplyCommand(CmdTokenCreate, TokenCreatePayload{
		TokenHash:   hash,
		ExpiresUnix: expiresAt.Unix(),
		CreatedBy:   issuedBy,
	}, ApplyTimeout); err != nil {
		return nil, err
	}
	return &TokenIssueResult{
		Token:         plaintext,
		ExpiresAt:     expiresAt,
		LeaderAddress: apiBaseURL(c.node.Node.APIAddress),
	}, nil
}

// JoinResult describes a successful enrollment.
type JoinResult struct {
	ClusterID         string
	CommitIndexAtJoin uint64
}

// EnrollNode is called on the leader by a joining node's HTTP request.
// Validates the token, consumes it via Raft, then issues AddVoter. Returns
// when the new node has caught up (or after timeout). On token failure
// returns the typed JoinError so the handler can map to 403.
func (c *Cluster) EnrollNode(token, nodeID, raftAddress, apiAddress string) (*JoinResult, error) {
	if err := c.requireLeader(); err != nil {
		return nil, err
	}
	hash := HashToken(token)
	ti, err := c.store.Token(hash)
	if err != nil {
		return nil, err
	}
	if ti == nil {
		return nil, &JoinError{Reason: "invalid token"}
	}
	if ti.ConsumedUnix != 0 {
		return nil, &JoinError{Reason: "token already consumed"}
	}
	if time.Now().Unix() > ti.ExpiresUnix {
		return nil, &JoinError{Reason: "token expired"}
	}

	// Consume the token first so a concurrent join sees it gone.
	if err := c.raft.ApplyCommand(CmdTokenConsume, TokenConsumePayload{
		TokenHash:  hash,
		ConsumedAt: time.Now().Unix(),
	}, ApplyTimeout); err != nil {
		return nil, err
	}

	// Add the new server to Raft configuration.
	if err := c.raft.AddVoter(nodeID, raftAddress, 10*time.Second); err != nil {
		return nil, fmt.Errorf("add voter: %w", err)
	}

	// Record the member in the replicated members bucket so any node can map
	// node_id → api_address.
	if err := c.raft.ApplyCommand(CmdMemberUpsert, MemberUpsertPayload{
		NodeID:      nodeID,
		RaftAddress: raftAddress,
		APIAddress:  apiAddress,
		JoinedUnix:  time.Now().Unix(),
	}, ApplyTimeout); err != nil {
		return nil, err
	}

	return &JoinResult{
		ClusterID:         "",
		CommitIndexAtJoin: c.raft.CommitIndex(),
	}, nil
}

// JoinError signals a token validation failure that the handler should map
// to HTTP 403 with the reason in the body.
type JoinError struct{ Reason string }

func (e *JoinError) Error() string { return e.Reason }

// RemoveMember removes a node from the Raft configuration and the replicated
// members bucket.
func (c *Cluster) RemoveMember(nodeID string) error {
	if err := c.requireLeader(); err != nil {
		return err
	}
	if nodeID == c.node.Node.ID {
		return fmt.Errorf("cannot remove the current leader; transfer leadership first")
	}
	if err := c.raft.RemoveServer(nodeID, 10*time.Second); err != nil {
		return fmt.Errorf("raft remove server: %w", err)
	}
	return c.raft.ApplyCommand(CmdMemberRemove, MemberRemovePayload{NodeID: nodeID}, ApplyTimeout)
}

// TransferLeadership requests a Raft leadership transfer.
func (c *Cluster) TransferLeadership(targetNodeID string) error {
	if err := c.requireLeader(); err != nil {
		return err
	}
	return c.raft.LeadershipTransfer(targetNodeID, 10*time.Second)
}

// registerSelfAfterLeader records this node in the members bucket once it's
// the leader. Runs as a single goroutine spawned at bootstrap.
func (c *Cluster) registerSelfAfterLeader() {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c.raft.IsLeader() {
			// Best-effort; if it fails we'll retry on next leader transition.
			_ = c.raft.ApplyCommand(CmdMemberUpsert, MemberUpsertPayload{
				NodeID:      c.node.Node.ID,
				RaftAddress: c.node.Node.RaftAddress,
				APIAddress:  c.node.Node.APIAddress,
				JoinedUnix:  time.Now().Unix(),
			}, ApplyTimeout)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ============================================================================
// Helpers
// ============================================================================

// resolveTokenTTL respects DBLOCK_TEST_TOKEN_TTL_SECONDS only when
// DBLOCK_TEST_MODE=1; otherwise returns DefaultTokenTTL.
func resolveTokenTTL() time.Duration {
	if os.Getenv("DBLOCK_TEST_MODE") != "1" {
		return DefaultTokenTTL
	}
	v := os.Getenv("DBLOCK_TEST_TOKEN_TTL_SECONDS")
	if v == "" {
		return DefaultTokenTTL
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return DefaultTokenTTL
	}
	return time.Duration(n) * time.Second
}

// apiBaseURL turns a "host:port" listen address into a reachable HTTP base
// URL. Listen addresses bound to 0.0.0.0 are rewritten to 127.0.0.1 for
// loopback testing.
func apiBaseURL(listenAddr string) string {
	host := listenAddr
	port := ""
	for i := len(listenAddr) - 1; i >= 0; i-- {
		if listenAddr[i] == ':' {
			host = listenAddr[:i]
			port = listenAddr[i+1:]
			break
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if port == "" {
		return "http://" + host
	}
	return "http://" + host + ":" + port
}

// MembersFromRaftConfig returns the node IDs currently in the Raft
// configuration, regardless of whether they've registered themselves in the
// members bucket yet. Used by the cluster status handler so newly-added
// nodes appear in the listing even before their first CmdMemberUpsert.
func (c *Cluster) MembersFromRaftConfig() []raft.Server {
	return c.raft.Configuration().Servers
}
