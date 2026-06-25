package cluster

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
	bolt "go.etcd.io/bbolt"
)

// Bucket names. Paths use slashes in comments only; bbolt buckets are flat.
var (
	bucketMeta              = []byte("meta")
	bucketMembers           = []byte("cluster_members")
	bucketTokens            = []byte("cluster_tokens")
	bucketBlocklists        = []byte("config_blocklists")
	bucketAllowlist         = []byte("config_allowlist")
	bucketLocalDNS          = []byte("config_local_dns")
	bucketSettings          = []byte("config_settings")
	bucketAuth              = []byte("config_auth")
	bucketStats             = []byte("stats") // sub-bucket per node-id
	bucketProfiles          = []byte("config_profiles")
	bucketSchedules         = []byte("config_schedules")
	bucketScheduleBindings  = []byte("config_schedule_bindings")
	bucketCategoryOverrides = []byte("config_category_overrides")
	// M5.2: replicated audit log. Keys are big-endian 8-byte sequence numbers
	// (monotonic, never recycled); values are JSON-encoded AuditEntry rows.
	bucketAudit = []byte("audit")
	// M7: revocable, scoped API bearer tokens. Key = token ID.
	bucketAPITokens = []byte("api_tokens")
	// M8: DNSCrypt v2 keypair. Single key "keys".
	bucketDNSCryptKeys = []byte("dnscrypt_keys")
	// M22: replicated webhook endpoint list. Single key "list".
	bucketWebhooks = []byte("webhooks")
	// M23.5: built-in DHCP server settings (single key "settings") and
	// static assignments (key = lowercase MAC address).
	bucketDhcpServer      = []byte("dhcp_server")
	bucketDhcpStaticAssns = []byte("dhcp_static_assignments")
	// M30: dynamic DHCPv4 lease persistence (key = IPv4 string).
	bucketDhcpServerLeases = []byte("dhcp_server_leases")
	// M30: DHCPv6 server settings (single key "settings"), static assignments
	// (key = DUID string), and dynamic leases (key = IPv6 address string).
	bucketDhcp6Server      = []byte("dhcp6_server")
	bucketDhcp6StaticAssns = []byte("dhcp6_static_assignments")
	bucketDhcp6ServerLeases = []byte("dhcp6_server_leases")
)

// AuditRetention is the cutoff for the lazy trim that runs on every
// CmdAuditAppend. Rows older than this at apply time are deleted in the
// same Raft commit so the bucket never accumulates indefinitely.
const AuditRetention = 90 * 24 * time.Hour

const schemaVersion uint32 = 1

// Store wraps the bbolt database with typed accessors and Apply, the single
// entry point used by the Raft FSM to mutate state. All readers go through
// the typed Get* methods; all writers must route through Apply via Raft.
type Store struct {
	db *bolt.DB
	mu sync.RWMutex // protects the in-memory caches built from bbolt

	// certCache is optionally wired by the Cluster after boot to enable
	// hot-reload of TLS material on CmdCertRotation applies.
	certCache *CertCache
	// nodeID is this node's identifier, used to look up per-node certs in
	// CertRotationPayload.Nodes.
	nodeID string
}

// OpenStore opens (or creates) the bbolt database at path and ensures every
// bucket exists.
func OpenStore(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt: %w", err)
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{
			bucketMeta, bucketMembers, bucketTokens,
			bucketBlocklists, bucketAllowlist, bucketLocalDNS,
			bucketSettings, bucketAuth, bucketStats,
			bucketProfiles, bucketSchedules, bucketScheduleBindings,
			bucketCategoryOverrides,
			bucketAudit,
			bucketAPITokens,
			bucketDNSCryptKeys,
			bucketWebhooks,
			bucketDhcpServer,
			bucketDhcpStaticAssns,
			bucketDhcpServerLeases,
			bucketDhcp6Server,
			bucketDhcp6StaticAssns,
			bucketDhcp6ServerLeases,
		}
		for _, b := range buckets {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", b, err)
			}
		}
		meta := tx.Bucket(bucketMeta)
		if meta.Get([]byte("schema_version")) == nil {
			buf := make([]byte, 4)
			binary.BigEndian.PutUint32(buf, schemaVersion)
			if err := meta.Put([]byte("schema_version"), buf); err != nil {
				return err
			}
		}
		return nil
	})
}

// Close releases the bbolt handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying bbolt handle for tests and the snapshot path.
// Most callers should not need it.
func (s *Store) DB() *bolt.DB { return s.db }

// SetCertCache wires the cert cache and node ID so CmdCertRotation applies
// can hot-reload the leaf certificate without restarting the listener.
func (s *Store) SetCertCache(cc *CertCache, nodeID string) {
	s.certCache = cc
	s.nodeID = nodeID
}

// ============================================================================
// Apply: route a Command to the correct bucket mutation. Called by the FSM.
// ============================================================================

// Apply executes a Command against bbolt in a single transaction.
func (s *Store) Apply(raw []byte) error {
	var cmd Command
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return fmt.Errorf("decode command: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		return s.applyTx(tx, cmd)
	})
}

func (s *Store) applyTx(tx *bolt.Tx, cmd Command) error {
	switch cmd.Kind {
	case CmdBlocklistUpsert:
		var p BlocklistUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Blocklist)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketBlocklists).Put([]byte(p.Blocklist.ID), v)

	case CmdBlocklistDelete:
		var p BlocklistDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketBlocklists).Delete([]byte(p.ID))

	case CmdBlocklistSetEnabled:
		var p BlocklistSetEnabledPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v := tx.Bucket(bucketBlocklists).Get([]byte(p.ID))
		if v == nil {
			return fmt.Errorf("blocklist %q not found", p.ID)
		}
		var bl config.Blocklist
		if err := json.Unmarshal(v, &bl); err != nil {
			return err
		}
		bl.Enabled = p.Enabled
		nv, err := json.Marshal(bl)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketBlocklists).Put([]byte(p.ID), nv)

	case CmdAllowlistAdd:
		var p AllowlistAddPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketAllowlist).Put([]byte(strings.ToLower(p.Domain)), []byte{})

	case CmdAllowlistRemove:
		var p AllowlistRemovePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketAllowlist).Delete([]byte(strings.ToLower(p.Domain)))

	case CmdLocalDNSUpsert:
		var p LocalDNSUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Entry)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketLocalDNS).Put([]byte(p.Entry.ID), v)

	case CmdLocalDNSDelete:
		var p LocalDNSDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketLocalDNS).Delete([]byte(p.ID))

	case CmdSettingsPatch:
		var p SettingsPatchPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applySettingsPatch(tx, p)

	case CmdAuthSetCredentials:
		var p AuthSetCredentialsPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketAuth).Put([]byte("credentials"), v)

	case CmdTokenCreate:
		var p TokenCreatePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketTokens).Put([]byte(p.TokenHash), v)

	case CmdTokenConsume:
		var p TokenConsumePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v := tx.Bucket(bucketTokens).Get([]byte(p.TokenHash))
		if v == nil {
			return fmt.Errorf("token not found")
		}
		var existing TokenCreatePayload
		if err := json.Unmarshal(v, &existing); err != nil {
			return err
		}
		// Mark consumed (don't delete) so a later validation distinguishes
		// "consumed" from "never existed". A pruner sweeps these out later
		// once they're past expiry.
		existing.ConsumedUnix = p.ConsumedAt
		nv, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketTokens).Put([]byte(p.TokenHash), nv)

	case CmdMemberUpsert:
		var p MemberUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketMembers).Put([]byte(p.NodeID), v)

	case CmdMemberRemove:
		var p MemberRemovePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketMembers).Delete([]byte(p.NodeID))

	case CmdStatsCommitHour:
		var p StatsCommitHourPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		nodeBucket, err := tx.Bucket(bucketStats).CreateBucketIfNotExists([]byte(p.NodeID))
		if err != nil {
			return err
		}
		v, err := json.Marshal(p.Aggregate)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, uint64(p.HourUnix))
		return nodeBucket.Put(key, v)

	case CmdStatsPrune:
		var p StatsPrunePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketStats).ForEachBucket(func(nodeID []byte) error {
			b := tx.Bucket(bucketStats).Bucket(nodeID)
			toDelete := [][]byte{}
			if err := b.ForEach(func(k, _ []byte) error {
				if binary.BigEndian.Uint64(k) < uint64(p.BeforeUnix) {
					toDelete = append(toDelete, append([]byte(nil), k...))
				}
				return nil
			}); err != nil {
				return err
			}
			for _, k := range toDelete {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
			return nil
		})

	case CmdClusterSecretSet:
		var p ClusterSecretSetPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketMeta).Put([]byte("cluster_secret"), []byte(p.Secret))

	case CmdProfileUpsert:
		var p ProfileUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Profile)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketProfiles).Put([]byte(p.Profile.ID), v)

	case CmdProfileDelete:
		var p ProfileDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		if p.ID == "default" {
			return fmt.Errorf("cannot delete the reserved default profile")
		}
		return tx.Bucket(bucketProfiles).Delete([]byte(p.ID))

	case CmdScheduleUpsert:
		var p ScheduleUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Schedule)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketSchedules).Put([]byte(p.Schedule.ID), v)

	case CmdScheduleDelete:
		var p ScheduleDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		// Cascade: drop every binding referencing this schedule.
		bindings := tx.Bucket(bucketScheduleBindings)
		toDrop := [][]byte{}
		_ = bindings.ForEach(func(k, _ []byte) error {
			if strings.HasPrefix(string(k), p.ID+":") {
				toDrop = append(toDrop, append([]byte(nil), k...))
			}
			return nil
		})
		for _, k := range toDrop {
			if err := bindings.Delete(k); err != nil {
				return err
			}
		}
		return tx.Bucket(bucketSchedules).Delete([]byte(p.ID))

	case CmdScheduleBindingPut:
		var p ScheduleBindingPutPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		key := bindingKey(p.Binding.ScheduleID, p.Binding.ProfileID, p.Binding.BlocklistID)
		v, err := json.Marshal(p.Binding)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketScheduleBindings).Put(key, v)

	case CmdScheduleBindingDel:
		var p ScheduleBindingDelPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketScheduleBindings).Delete(bindingKey(p.ScheduleID, p.ProfileID, p.BlocklistID))

	case CmdCategoryOverridePut:
		var p CategoryOverridePutPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Override)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketCategoryOverrides).Put([]byte(p.Override.Name), v)

	case CmdConfigImport:
		var p ConfigImportPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return importM1Config(tx, p.Snapshot)

	case CmdAuditAppend:
		var p AuditAppendPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyAuditAppend(tx, p)

	case CmdDohResolverSnapshotReplace:
		var p DohResolverSnapshotReplacePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyDohResolverSnapshotReplaceFromPayload(tx, p)

	case CmdDohResolverRefreshFailure:
		var p DohResolverRefreshFailurePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyDohResolverRefreshFailure(tx, p.AttemptedAt, p.Error)

	case CmdLeasesReplace:
		var p LeasesReplacePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyLeasesReplace(tx, p)

	case CmdAnomalyAppend:
		var p AnomalyAppendPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyAnomalyAppend(tx, p.Anomaly)

	case CmdAnomalyAcknowledge:
		var p AnomalyAckPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyAnomalyAck(tx, p.ID, p.AcknowledgedUnix)

	case CmdAnomalySweep:
		var p AnomalySweepPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return applyAnomalySweep(tx, p.BeforeUnix)

	case CmdAPITokenUpsert:
		var p APITokenUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Token)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketAPITokens).Put([]byte(p.Token.ID), v)

	case CmdAPITokenDelete:
		var p APITokenDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketAPITokens).Delete([]byte(p.ID))

	case CmdDNSCryptKeysSet:
		var k DNSCryptKeys
		if err := json.Unmarshal(cmd.Payload, &k); err != nil {
			return err
		}
		v, err := json.Marshal(k)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDNSCryptKeys).Put([]byte("keys"), v)

	case CmdGlobalPauseSet:
		var p GlobalPauseSetPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketSettings).Put([]byte("global_pause"), v)

	case CmdGlobalPauseClear:
		return tx.Bucket(bucketSettings).Delete([]byte("global_pause"))

	case CmdProfilePauseSet:
		var p ProfilePauseSetPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v := tx.Bucket(bucketProfiles).Get([]byte(p.ProfileID))
		if v == nil {
			return fmt.Errorf("profile %q not found", p.ProfileID)
		}
		var prof config.Profile
		if err := json.Unmarshal(v, &prof); err != nil {
			return err
		}
		prof.Pause = &config.PauseState{ResumesAt: p.ResumesAt, Reason: p.Reason}
		nv, err := json.Marshal(prof)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketProfiles).Put([]byte(p.ProfileID), nv)

	case CmdProfilePauseClear:
		var p ProfilePauseClearPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v := tx.Bucket(bucketProfiles).Get([]byte(p.ProfileID))
		if v == nil {
			return fmt.Errorf("profile %q not found", p.ProfileID)
		}
		var prof config.Profile
		if err := json.Unmarshal(v, &prof); err != nil {
			return err
		}
		prof.Pause = nil
		nv, err := json.Marshal(prof)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketProfiles).Put([]byte(p.ProfileID), nv)

	case CmdCertRotation:
		var p CertRotationPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return s.applyCertRotation(p)

	case CmdWebhooksUpdate:
		var p WebhooksUpdatePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Webhooks)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketWebhooks).Put([]byte("list"), v)

	case CmdDhcpServerSettingsSet:
		var p DhcpServerSettingsSetPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Settings)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDhcpServer).Put([]byte("settings"), v)

	case CmdDhcpStaticAssignmentUpsert:
		var p DhcpStaticAssignmentUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Assignment)
		if err != nil {
			return err
		}
		key := []byte(strings.ToLower(p.Assignment.MAC))
		return tx.Bucket(bucketDhcpStaticAssns).Put(key, v)

	case CmdDhcpStaticAssignmentDelete:
		var p DhcpStaticAssignmentDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketDhcpStaticAssns).Delete([]byte(strings.ToLower(p.MAC)))

	// M30 — DHCPv4 dynamic lease persistence.
	case CmdDhcpServerLeasesUpsert:
		var p DhcpServerLeaseUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDhcpServerLeases).Put([]byte(p.IP), v)

	case CmdDhcpServerLeaseDelete:
		var p DhcpServerLeaseDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketDhcpServerLeases).Delete([]byte(p.IP))

	// M30 — DHCPv6 server settings.
	case CmdDhcp6ServerSettingsSet:
		var p Dhcp6ServerSettingsSetPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Settings)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDhcp6Server).Put([]byte("settings"), v)

	// M30 — DHCPv6 static assignments.
	case CmdDhcp6StaticAssignmentUpsert:
		var p Dhcp6StaticAssignmentUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p.Assignment)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDhcp6StaticAssns).Put([]byte(p.Assignment.DUID), v)

	case CmdDhcp6StaticAssignmentDelete:
		var p Dhcp6StaticAssignmentDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketDhcp6StaticAssns).Delete([]byte(p.DUID))

	// M30 — DHCPv6 dynamic lease persistence.
	case CmdDhcp6ServerLeasesUpsert:
		var p Dhcp6ServerLeaseUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketDhcp6ServerLeases).Put([]byte(p.Address), v)

	case CmdDhcp6ServerLeaseDelete:
		var p Dhcp6ServerLeaseDeletePayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		return tx.Bucket(bucketDhcp6ServerLeases).Delete([]byte(p.Address))
	}
	return fmt.Errorf("unknown command kind %q", cmd.Kind)
}

// applyAuditAppend writes one audit row keyed by the next monotonic
// sequence, then trims rows older than AuditRetention in the same
// transaction. The bucket's `_seq` meta key holds the next sequence.
func applyAuditAppend(tx *bolt.Tx, p AuditAppendPayload) error {
	b := tx.Bucket(bucketAudit)
	if b == nil {
		var err error
		b, err = tx.CreateBucket(bucketAudit)
		if err != nil {
			return err
		}
	}

	// Allocate sequence.
	var seq uint64
	if v := b.Get([]byte("_seq")); v != nil && len(v) == 8 {
		seq = binary.BigEndian.Uint64(v)
	}
	seq++

	row := AuditRow{
		ID:        p.ID,
		Seq:       seq,
		TimeUnix:  p.TimeUnix,
		Actor:     p.Actor,
		Action:    p.Action,
		Target:    p.Target,
		Result:    p.Result,
		Error:     p.Error,
		Diff:      p.Diff,
		NodeID:    p.NodeID,
		RequestID: p.RequestID,
	}
	v, err := json.Marshal(row)
	if err != nil {
		return err
	}
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, seq)
	if err := b.Put(key, v); err != nil {
		return err
	}
	seqBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBuf, seq)
	if err := b.Put([]byte("_seq"), seqBuf); err != nil {
		return err
	}

	// Lazy retention sweep — rows with TimeUnix older than cutoff are
	// dropped in the same Raft commit. No background goroutine.
	cutoff := p.TimeUnix - int64(AuditRetention.Seconds())
	if cutoff <= 0 {
		return nil
	}
	c := b.Cursor()
	var toDelete [][]byte
	for k, val := c.First(); k != nil; k, val = c.Next() {
		if len(k) != 8 {
			continue // skip meta keys like "_seq"
		}
		var r AuditRow
		if err := json.Unmarshal(val, &r); err != nil {
			continue
		}
		if r.TimeUnix >= cutoff {
			break // newer than cutoff; remaining keys are also newer
		}
		toDelete = append(toDelete, append([]byte(nil), k...))
	}
	for _, k := range toDelete {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// AuditRow is the persisted form of one audit log entry. The `Seq`
// field is assigned at apply time so every replica records the same
// sequence for the same Raft log entry.
type AuditRow struct {
	ID        string `json:"id"`
	Seq       uint64 `json:"seq"`
	TimeUnix  int64  `json:"time_unix"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target,omitempty"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	Diff      string `json:"diff,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type storedFilteringSettings struct {
	BlockPolicy     string                  `json:"block_policy"`
	// PauseMaxSeconds: -1 = never explicitly set (first-boot sentinel, Snapshot returns default 86400);
	// 0 = feature explicitly disabled; positive = operator-defined ceiling in seconds.
	PauseMaxSeconds int                     `json:"pause_max_seconds"`
	// M26: block page redirect configuration.
	BlockPage       config.BlockPageConfig  `json:"block_page,omitempty"`
}

func readStoredFiltering(b *bolt.Bucket) storedFilteringSettings {
	var s storedFilteringSettings
	if v := b.Get([]byte("filtering")); v != nil {
		json.Unmarshal(v, &s) //nolint:errcheck — best-effort; zero value is safe
	}
	return s
}

func applySettingsPatch(tx *bolt.Tx, p SettingsPatchPayload) error {
	b := tx.Bucket(bucketSettings)
	if p.DNS != nil {
		v, err := json.Marshal(p.DNS)
		if err != nil {
			return err
		}
		if err := b.Put([]byte("dns"), v); err != nil {
			return err
		}
	}
	if p.Filtering != nil {
		s := readStoredFiltering(b)
		if p.Filtering.BlockPolicy != nil {
			s.BlockPolicy = *p.Filtering.BlockPolicy
		}
		if p.Filtering.PauseMaxSeconds != nil {
			s.PauseMaxSeconds = *p.Filtering.PauseMaxSeconds
		}
		v, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if err := b.Put([]byte("filtering"), v); err != nil {
			return err
		}
	}
	if p.QueryLog != nil {
		v, err := json.Marshal(p.QueryLog)
		if err != nil {
			return err
		}
		if err := b.Put([]byte("query_log"), v); err != nil {
			return err
		}
	}
	return nil
}

func importM1Config(tx *bolt.Tx, c config.Config) error {
	// Remember whether the filtering key has been written before the wipe.
	// On first boot (init() creates buckets but writes no data) it is nil.
	// On subsequent imports (settings patches going through WithWriteLock)
	// it is non-nil, so we can distinguish "first-boot 0 = not configured"
	// from "explicit API call 0 = disabled".
	isFirstBoot := tx.Bucket(bucketSettings).Get([]byte("filtering")) == nil

	// Wipe & rewrite each replicated bucket from the M1 snapshot.
	for _, name := range [][]byte{bucketBlocklists, bucketAllowlist, bucketLocalDNS, bucketSettings, bucketAuth} {
		if err := tx.DeleteBucket(name); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		if _, err := tx.CreateBucket(name); err != nil {
			return err
		}
	}

	// Blocklists.
	for _, bl := range c.Filtering.Blocklists {
		v, err := json.Marshal(bl)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketBlocklists).Put([]byte(bl.ID), v); err != nil {
			return err
		}
	}

	// Allowlist.
	for _, d := range c.Filtering.Allowlist {
		if err := tx.Bucket(bucketAllowlist).Put([]byte(strings.ToLower(d)), []byte{}); err != nil {
			return err
		}
	}

	// Local DNS.
	for _, e := range c.LocalDNS.Entries {
		v, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketLocalDNS).Put([]byte(e.ID), v); err != nil {
			return err
		}
	}

	// Settings — three keys.
	dnsCfg := c.DNS
	dnsCfg.Listen = config.ListenConfig{} // node-local; never replicate
	dv, err := json.Marshal(dnsCfg)
	if err != nil {
		return err
	}
	if err := tx.Bucket(bucketSettings).Put([]byte("dns"), dv); err != nil {
		return err
	}
	// Determine pause ceiling to persist. On first boot with PauseMaxSeconds=0
	// (Go zero value, meaning "not set in config"), use -1 as sentinel so
	// Snapshot() can return the default 86400 instead of treating 0 as "disabled".
	pauseMax := c.Filtering.PauseMaxSeconds
	if isFirstBoot && pauseMax == 0 {
		pauseMax = -1 // sentinel: "not yet explicitly configured"
	}
	fv, err := json.Marshal(storedFilteringSettings{
		BlockPolicy:     c.Filtering.BlockPolicy,
		PauseMaxSeconds: pauseMax,
		BlockPage:       c.Filtering.BlockPage,
	})
	if err != nil {
		return err
	}
	if err := tx.Bucket(bucketSettings).Put([]byte("filtering"), fv); err != nil {
		return err
	}
	qv, err := json.Marshal(c.QueryLog)
	if err != nil {
		return err
	}
	if err := tx.Bucket(bucketSettings).Put([]byte("query_log"), qv); err != nil {
		return err
	}

	// Auth.
	auth := AuthSetCredentialsPayload{
		Username:     c.Auth.Username,
		PasswordHash: c.Auth.PasswordHash,
	}
	av, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	if err := tx.Bucket(bucketAuth).Put([]byte("credentials"), av); err != nil {
		return err
	}
	return nil
}

// ============================================================================
// Read paths — typed getters used by the API and the DNS engine.
// ============================================================================

// Snapshot returns a fully-populated config.Config built from bbolt. This is
// the form consumed by the existing M1 filter/local-dns engines (via the
// existing config types) and exported as the shadow YAML.
func (s *Store) Snapshot() (*config.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := &config.Config{}
	err := s.db.View(func(tx *bolt.Tx) error {
		// Blocklists.
		if err := tx.Bucket(bucketBlocklists).ForEach(func(_, v []byte) error {
			var bl config.Blocklist
			if err := json.Unmarshal(v, &bl); err != nil {
				return err
			}
			out.Filtering.Blocklists = append(out.Filtering.Blocklists, bl)
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(out.Filtering.Blocklists, func(i, j int) bool {
			return out.Filtering.Blocklists[i].ID < out.Filtering.Blocklists[j].ID
		})

		// Allowlist.
		if err := tx.Bucket(bucketAllowlist).ForEach(func(k, _ []byte) error {
			out.Filtering.Allowlist = append(out.Filtering.Allowlist, string(k))
			return nil
		}); err != nil {
			return err
		}
		sort.Strings(out.Filtering.Allowlist)

		// Local DNS.
		if err := tx.Bucket(bucketLocalDNS).ForEach(func(_, v []byte) error {
			var e config.LocalDNSEntry
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			out.LocalDNS.Entries = append(out.LocalDNS.Entries, e)
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(out.LocalDNS.Entries, func(i, j int) bool {
			return out.LocalDNS.Entries[i].ID < out.LocalDNS.Entries[j].ID
		})

		// Settings.
		sb := tx.Bucket(bucketSettings)
		if v := sb.Get([]byte("dns")); v != nil {
			if err := json.Unmarshal(v, &out.DNS); err != nil {
				return err
			}
		}
		out.Filtering.PauseMaxSeconds = 86400 // default: not yet explicitly configured
		if v := sb.Get([]byte("filtering")); v != nil {
			var s storedFilteringSettings
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			out.Filtering.BlockPolicy = s.BlockPolicy
			// -1 is the sentinel written on first boot meaning "not configured";
			// any other value (including 0 = disabled) was set explicitly.
			if s.PauseMaxSeconds >= 0 {
				out.Filtering.PauseMaxSeconds = s.PauseMaxSeconds
			}
			out.Filtering.BlockPage = s.BlockPage
		}
		if v := sb.Get([]byte("query_log")); v != nil {
			if err := json.Unmarshal(v, &out.QueryLog); err != nil {
				return err
			}
		}
		if v := sb.Get([]byte("global_pause")); v != nil {
			var gp GlobalPauseSetPayload
			if err := json.Unmarshal(v, &gp); err != nil {
				return err
			}
			out.Filtering.GlobalPause = &config.PauseState{ResumesAt: gp.ResumesAt, Reason: gp.Reason, ProfileIDs: gp.ProfileIDs}
		}

		// Auth.
		if v := tx.Bucket(bucketAuth).Get([]byte("credentials")); v != nil {
			var a AuthSetCredentialsPayload
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out.Auth.Username = a.Username
			out.Auth.PasswordHash = a.PasswordHash
		}

		// Profiles.
		if err := tx.Bucket(bucketProfiles).ForEach(func(_, v []byte) error {
			var p config.Profile
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			out.Profiles = append(out.Profiles, p)
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(out.Profiles, func(i, j int) bool { return out.Profiles[i].ID < out.Profiles[j].ID })

		// Schedules.
		if err := tx.Bucket(bucketSchedules).ForEach(func(_, v []byte) error {
			var s config.Schedule
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			out.Schedules = append(out.Schedules, s)
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(out.Schedules, func(i, j int) bool { return out.Schedules[i].ID < out.Schedules[j].ID })

		// Schedule bindings.
		if err := tx.Bucket(bucketScheduleBindings).ForEach(func(_, v []byte) error {
			var b config.ScheduleBinding
			if err := json.Unmarshal(v, &b); err != nil {
				return err
			}
			out.Bindings = append(out.Bindings, b)
			return nil
		}); err != nil {
			return err
		}

		// Category overrides.
		if err := tx.Bucket(bucketCategoryOverrides).ForEach(func(_, v []byte) error {
			var c config.CategoryOverride
			if err := json.Unmarshal(v, &c); err != nil {
				return err
			}
			out.Categories = append(out.Categories, c)
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(out.Categories, func(i, j int) bool { return out.Categories[i].Name < out.Categories[j].Name })

		// Webhooks.
		if v := tx.Bucket(bucketWebhooks).Get([]byte("list")); v != nil {
			if err := json.Unmarshal(v, &out.Webhooks); err != nil {
				return err
			}
		}

		// M23.5 — DHCP server settings.
		if v := tx.Bucket(bucketDhcpServer).Get([]byte("settings")); v != nil {
			if err := json.Unmarshal(v, &out.DHCPServer); err != nil {
				return err
			}
		}
		// M23.5 — DHCP static assignments (embedded in DHCPServer config).
		if err := tx.Bucket(bucketDhcpStaticAssns).ForEach(func(_, v []byte) error {
			var a config.DHCPStaticAssignment
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out.DHCPServer.StaticAssignments = append(out.DHCPServer.StaticAssignments, a)
			return nil
		}); err != nil {
			return err
		}

		out.Version = config.SchemaVersion
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Member is the view of a cluster member from the replicated members bucket.
type Member struct {
	NodeID      string `json:"node_id"`
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
	JoinedUnix  int64  `json:"joined_unix"`
}

// Members returns the current replicated member list.
func (s *Store) Members() ([]Member, error) {
	var out []Member
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMembers).ForEach(func(_, v []byte) error {
			var m Member
			if err := json.Unmarshal(v, &m); err != nil {
				return err
			}
			out = append(out, m)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, err
}

// MemberByID returns the named member, or nil if absent.
func (s *Store) MemberByID(id string) (*Member, error) {
	var m *Member
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketMembers).Get([]byte(id))
		if v == nil {
			return nil
		}
		m = &Member{}
		return json.Unmarshal(v, m)
	})
	return m, err
}

// TokenInfo describes a stored join token (without the plaintext — that's
// returned to the caller once and never persisted).
type TokenInfo struct {
	TokenHash    string
	ExpiresUnix  int64
	CreatedBy    string
	ConsumedUnix int64 // zero when the token has never been consumed
}

// Token looks up a token by its hash. Returns (nil, nil) when not found.
func (s *Store) Token(hash string) (*TokenInfo, error) {
	var out *TokenInfo
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketTokens).Get([]byte(hash))
		if v == nil {
			return nil
		}
		var p TokenCreatePayload
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		out = &TokenInfo{
			TokenHash:    p.TokenHash,
			ExpiresUnix:  p.ExpiresUnix,
			CreatedBy:    p.CreatedBy,
			ConsumedUnix: p.ConsumedUnix,
		}
		return nil
	})
	return out, err
}

// ClusterSecret returns the replicated cluster-wide secret, or "" if not yet
// initialised (which happens briefly during the first bootstrap before
// EnsureClusterSecret writes it).
func (s *Store) ClusterSecret() (string, error) {
	var out string
	err := s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketMeta).Get([]byte("cluster_secret")); v != nil {
			out = string(v)
		}
		return nil
	})
	return out, err
}

// bindingKey is the canonical composite key for a schedule_binding in
// bbolt. Format: "<schedule_id>:<profile_id>:<blocklist_id>". The schedule
// prefix lets CmdScheduleDelete cascade by prefix-scan.
func bindingKey(scheduleID, profileID, blocklistID string) []byte {
	return []byte(scheduleID + ":" + profileID + ":" + blocklistID)
}

// AggregatesIter calls fn for every hourly aggregate currently stored. The
// callback receives a copy that the caller owns.
// AuditQuery filters a page of audit rows. Zero/empty filters are treated as
// "match all". Limit ≤ 0 falls back to 50; values over 500 are clamped.
type AuditQuery struct {
	Actor        string // exact match
	ActionPrefix string // prefix match on action
	Result       string // "ok" | "error" | "" = any
	Limit        int
	Offset       int
}

// AuditList returns rows newest-first, plus the total count of rows
// matching the filter (before limit/offset are applied).
func (s *Store) AuditList(q AuditQuery) (rows []AuditRow, total int, err error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAudit)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		// Walk newest-first: reverse cursor over the sequence keys.
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if len(k) != 8 {
				continue // skip meta keys
			}
			var r AuditRow
			if err := json.Unmarshal(v, &r); err != nil {
				continue
			}
			if q.Actor != "" && r.Actor != q.Actor {
				continue
			}
			if q.ActionPrefix != "" && !strings.HasPrefix(r.Action, q.ActionPrefix) {
				continue
			}
			if q.Result != "" && r.Result != q.Result {
				continue
			}
			total++
			if total <= q.Offset {
				continue
			}
			if len(rows) >= q.Limit {
				continue
			}
			rows = append(rows, r)
		}
		return nil
	})
	return rows, total, err
}

func (s *Store) AggregatesIter(fn func(HourAggregate) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketStats).ForEachBucket(func(nodeID []byte) error {
			b := tx.Bucket(bucketStats).Bucket(nodeID)
			return b.ForEach(func(_, v []byte) error {
				var agg HourAggregate
				if err := json.Unmarshal(v, &agg); err != nil {
					return err
				}
				return fn(agg)
			})
		})
	})
}

// ─── M20: mTLS certificate rotation (TS-ClusterSecurityHardening) ────────────

// applyCertRotation writes the new CA cert and this node's leaf cert+key to
// disk, then hot-reloads the cert cache if one is wired. Called inside applyTx.
func (s *Store) applyCertRotation(p CertRotationPayload) error {
	if s.nodeID == "" {
		// nodeID not wired (unit-test path without a full cluster); skip
		// the disk write but don't fail the FSM apply.
		return nil
	}
	nc, ok := p.Nodes[s.nodeID]
	if !ok {
		// This node's certs are not in the payload — skip silently.
		// Could happen if the member list changed between generation and apply.
		return nil
	}
	// db.Path() returns the bbolt file; derive data_dir from it.
	dataDir := pathDir(s.db.Path())
	paths := MtlsPaths(dataDir)

	if err := os.WriteFile(paths.CACertFile, p.CACertPEM, 0644); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(paths.NodeCert, nc.CertPEM, 0644); err != nil {
		return fmt.Errorf("write node cert: %w", err)
	}
	if err := os.WriteFile(paths.NodeKey, nc.KeyPEM, 0600); err != nil {
		return fmt.Errorf("write node key: %w", err)
	}

	if s.certCache != nil {
		if err := s.certCache.Update(p.CACertPEM, nc.CertPEM, nc.KeyPEM); err != nil {
			// Log but don't fail the FSM apply — the certs are already on disk.
			// On next restart the correct certs will be loaded.
			_ = err
		}
	}
	return nil
}

// pathDir returns the directory component of a file path.
func pathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

// WebhookEndpoints returns the current replicated webhook endpoint list.
// Returns an empty slice (never nil) when no webhooks have been configured.
func (s *Store) WebhookEndpoints() ([]config.WebhookEndpoint, error) {
	var out []config.WebhookEndpoint
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketWebhooks).Get([]byte("list"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &out)
	})
	if out == nil {
		out = []config.WebhookEndpoint{}
	}
	return out, err
}

// DhcpServerSettings returns the cluster-wide DHCP server configuration.
func (s *Store) DhcpServerSettings() (config.DHCPServerConfig, error) {
	var out config.DHCPServerConfig
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketDhcpServer).Get([]byte("settings"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &out)
	})
	return out, err
}

// DhcpStaticAssignments returns all static MAC→IP assignments.
func (s *Store) DhcpStaticAssignments() ([]config.DHCPStaticAssignment, error) {
	var out []config.DHCPStaticAssignment
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDhcpStaticAssns).ForEach(func(_, v []byte) error {
			var a config.DHCPStaticAssignment
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out = append(out, a)
			return nil
		})
	})
	if out == nil {
		out = []config.DHCPStaticAssignment{}
	}
	return out, err
}

// ─── M30: DHCPv4 dynamic lease persistence ───────────────────────────────────

// DhcpServerLeases returns all non-expired persisted DHCPv4 dynamic leases.
func (s *Store) DhcpServerLeases() ([]DhcpServerLeaseUpsertPayload, error) {
	var out []DhcpServerLeaseUpsertPayload
	now := time.Now().Unix()
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDhcpServerLeases).ForEach(func(_, v []byte) error {
			var p DhcpServerLeaseUpsertPayload
			if err := json.Unmarshal(v, &p); err != nil {
				return nil // skip malformed
			}
			if p.ExpiresAt > 0 && p.ExpiresAt < now {
				return nil // skip expired
			}
			out = append(out, p)
			return nil
		})
	})
	if out == nil {
		out = []DhcpServerLeaseUpsertPayload{}
	}
	return out, err
}

// ─── M30: DHCPv6 server settings and persistence ─────────────────────────────

// Dhcp6ServerSettings returns the cluster-wide DHCPv6 server configuration.
func (s *Store) Dhcp6ServerSettings() (config.DHCPv6ServerConfig, error) {
	var out config.DHCPv6ServerConfig
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketDhcp6Server).Get([]byte("settings"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &out)
	})
	return out, err
}

// Dhcp6StaticAssignments returns all static DUID→address assignments.
func (s *Store) Dhcp6StaticAssignments() ([]config.Dhcp6StaticAssignment, error) {
	var out []config.Dhcp6StaticAssignment
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDhcp6StaticAssns).ForEach(func(_, v []byte) error {
			var a config.Dhcp6StaticAssignment
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out = append(out, a)
			return nil
		})
	})
	if out == nil {
		out = []config.Dhcp6StaticAssignment{}
	}
	return out, err
}

// Dhcp6ServerLeases returns all non-expired persisted DHCPv6 dynamic leases.
func (s *Store) Dhcp6ServerLeases() ([]Dhcp6ServerLeaseUpsertPayload, error) {
	var out []Dhcp6ServerLeaseUpsertPayload
	now := time.Now().Unix()
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDhcp6ServerLeases).ForEach(func(_, v []byte) error {
			var p Dhcp6ServerLeaseUpsertPayload
			if err := json.Unmarshal(v, &p); err != nil {
				return nil // skip malformed
			}
			if p.ExpiresAt > 0 && p.ExpiresAt < now {
				return nil // skip expired
			}
			out = append(out, p)
			return nil
		})
	})
	if out == nil {
		out = []Dhcp6ServerLeaseUpsertPayload{}
	}
	return out, err
}
