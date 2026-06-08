package dhcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"strings"
	"sync"
	"time"
)

// Manager polls a Connector at a fixed interval, maintains the lease
// snapshot indexed by IP, and tracks lease history for anti-spoof
// anomaly detection. Safe for concurrent reads from the DNS handler
// and management API.
type Manager struct {
	conn    Connector
	refresh time.Duration

	mu sync.RWMutex
	// byIP is the current snapshot, freshest poll wins.
	byIP map[string]Lease
	// byV6 indexes the same Lease values under each of their IPv6
	// addresses. Populated by M6.5 dual-stack merge; nil-safe pre-merge.
	byV6 map[string]Lease
	// history is the running record of every (Client-ID, MAC, hostname)
	// tuple ever seen, used by the anti-spoof detector. Keyed by
	// arbitrary stable identifier — see indexKey.
	history map[string]historyEntry
	// anomalies are recent anti-spoof events, keyed by ID. Acknowledged
	// ones linger until the AnomalyRetention sweep.
	anomalies map[string]Anomaly

	// M5.1 poll-health bookkeeping. lastPollAt is the wall-clock of the
	// most recent successful Fetch(); pollErrorsTotal counts failed
	// Fetch() calls since process start. Both are surfaced via the
	// /metrics exporter so operators can alert on stale leases.
	lastPollAt      time.Time
	pollErrorsTotal uint64

	cancel context.CancelFunc
	done   chan struct{}
}

type historyEntry struct {
	ClientID  string
	MAC       string
	Hostname  string
	FirstSeen time.Time
	LastSeen  time.Time
}

// NewManager builds the manager but doesn't start polling yet.
func NewManager(conn Connector, refresh time.Duration) *Manager {
	if refresh <= 0 {
		refresh = 60 * time.Second
	}
	return &Manager{
		conn:      conn,
		refresh:   refresh,
		byIP:      map[string]Lease{},
		byV6:      map[string]Lease{},
		history:   map[string]historyEntry{},
		anomalies: map[string]Anomaly{},
	}
}

// Start kicks off the refresh loop in a goroutine. Returns immediately.
// Idempotent: calling Start twice is a programming error.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	go m.loop(ctx)
}

// Shutdown stops the refresh loop. Safe to call multiple times.
func (m *Manager) Shutdown() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel = nil
	}
}

func (m *Manager) loop(ctx context.Context) {
	defer close(m.done)
	// First poll immediately so /api/v1/clients/{ip} returns data fast.
	m.pollOnce()
	t := time.NewTicker(m.refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.pollOnce()
		}
	}
}

func (m *Manager) pollOnce() {
	leases, err := m.conn.Fetch()
	if err != nil {
		m.mu.Lock()
		m.pollErrorsTotal++
		m.mu.Unlock()
		log.Printf("dhcp %s: poll failed: %v (keeping prior snapshot)", m.conn.Source(), err)
		return
	}
	m.apply(leases)
	m.mu.Lock()
	m.lastPollAt = time.Now()
	m.mu.Unlock()
}

// apply replaces the snapshot and runs anti-spoof detection against
// the prior history. Exported (lowercase via apply) for tests via the
// package-level Apply wrapper.
func (m *Manager) apply(leases []Lease) {
	now := time.Now()
	merged := mergeDualStack(leases)
	newByIP := make(map[string]Lease, len(merged))
	newByV6 := map[string]Lease{}
	for _, l := range merged {
		if l.IP != "" {
			newByIP[l.IP] = l
		} else if len(l.IPv6Addresses) > 0 {
			// v6-only lease — index under the first (lex-smallest) v6 address.
			newByIP[l.IPv6Addresses[0]] = l
		}
		for _, v6 := range l.IPv6Addresses {
			newByV6[v6] = l
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Anti-spoof: compare each NEW v4 lease against history before updating.
	// v6-only and IA_PD-style records are skipped (M3.6 anti-spoof is v4-keyed).
	for _, l := range merged {
		if l.IP == "" {
			continue
		}
		m.detectAnomalies(l, now)
		key := historyKey(l)
		h := m.history[key]
		if h.FirstSeen.IsZero() {
			h.FirstSeen = now
		}
		h.LastSeen = now
		h.ClientID = l.ClientID
		h.MAC = l.MAC
		h.Hostname = l.Hostname
		m.history[key] = h
	}
	m.byIP = newByIP
	m.byV6 = newByV6
	m.sweepAnomalies(now)
}

// mergeDualStack collapses one client's v4 + v6 records into a single
// Lease per TS-Dhcpv6Lease. Heuristic ladder (first hit wins):
//  1. DUID-LLT/LL ends in a 6-byte MAC equal to an existing v4 Lease.MAC
//  2. Case-insensitive non-empty hostname equality
// No match → emit the v6 record stand-alone.
func mergeDualStack(leases []Lease) []Lease {
	var v4 []*Lease
	var v6 []*Lease
	for i := range leases {
		l := leases[i]
		// A "v4 record" is any lease that carries an IPv4 IP. A "v6
		// record" is any lease that carries v6 addresses but no IPv4 IP.
		switch {
		case l.IP != "" && len(l.IPv6Addresses) == 0:
			v4 = append(v4, &leases[i])
		case l.IP == "" && len(l.IPv6Addresses) > 0:
			v6 = append(v6, &leases[i])
		default:
			// Already-merged or oddball record (e.g. v4 + v6 from one
			// http_json payload). Keep it as-is.
			v4 = append(v4, &leases[i])
		}
	}

	for _, vsix := range v6 {
		match := findV4Match(v4, *vsix)
		if match == nil {
			continue
		}
		// Absorb v6 addresses into the v4 record.
		seen := map[string]struct{}{}
		for _, a := range match.IPv6Addresses {
			seen[a] = struct{}{}
		}
		for _, a := range vsix.IPv6Addresses {
			if _, ok := seen[a]; !ok {
				match.IPv6Addresses = append(match.IPv6Addresses, a)
				seen[a] = struct{}{}
			}
		}
		sortStrings(match.IPv6Addresses)
		if match.DUID == "" {
			match.DUID = vsix.DUID
		}
		match.IsDualStack = true
		vsix.IP = "MERGED" // mark for filtering below
	}

	out := make([]Lease, 0, len(leases))
	for _, p := range v4 {
		out = append(out, *p)
	}
	for _, p := range v6 {
		if p.IP == "MERGED" {
			continue
		}
		// Stable ordering of stand-alone v6 records too.
		sortStrings(p.IPv6Addresses)
		out = append(out, *p)
	}
	return out
}

// findV4Match returns the first v4 record matching the heuristic ladder,
// or nil when no match is found.
func findV4Match(v4 []*Lease, vsix Lease) *Lease {
	// 1. DUID-LLT/LL MAC suffix == existing v4 MAC.
	if mac := macFromDUID(vsix.DUID); mac != "" {
		for _, p := range v4 {
			if p.MAC != "" && p.MAC == mac {
				return p
			}
		}
	}
	// 2. Hostname equality (case-insensitive, non-empty).
	if vsix.Hostname != "" {
		want := strings.ToLower(vsix.Hostname)
		for _, p := range v4 {
			if p.Hostname != "" && strings.ToLower(p.Hostname) == want {
				return p
			}
		}
	}
	return nil
}

// macFromDUID extracts the trailing MAC from DUID-LL / DUID-LLT
// formatted as colon-separated hex bytes. Returns "" when the DUID isn't
// in a recognised LL/LLT shape.
//
// DUID-LL  : 00:03:<hw-type:2>:<MAC:6>
// DUID-LLT : 00:01:<hw-type:2>:<time:4>:<MAC:6>
func macFromDUID(duid string) string {
	parts := strings.Split(duid, ":")
	if len(parts) < 6 {
		return ""
	}
	macParts := parts[len(parts)-6:]
	for _, p := range macParts {
		if len(p) != 2 {
			return ""
		}
	}
	return strings.ToLower(strings.Join(macParts, ":"))
}

// detectAnomalies compares an incoming lease against history and records
// any spoof-shaped event. Called with m.mu held.
func (m *Manager) detectAnomalies(l Lease, now time.Time) {
	// 1. Same Client-ID known with a different MAC?
	if l.ClientID != "" {
		for _, h := range m.history {
			if h.ClientID == l.ClientID && h.MAC != "" && h.MAC != l.MAC {
				m.recordAnomaly(Anomaly{
					Kind:          AnomalyMacChangedForClientID,
					IP:            l.IP,
					MAC:           l.MAC,
					Hostname:      l.Hostname,
					ClientID:      l.ClientID,
					PriorMAC:      h.MAC,
					PriorClientID: h.ClientID,
					PriorHostname: h.Hostname,
					DetectedAt:    now,
				})
				break // one anomaly per detection is enough
			}
		}
	}

	// 2. Same MAC known with a different Client-ID?
	if l.MAC != "" {
		for _, h := range m.history {
			if h.MAC == l.MAC && h.ClientID != "" && l.ClientID != "" && h.ClientID != l.ClientID {
				m.recordAnomaly(Anomaly{
					Kind:          AnomalyClientIDChangedForMac,
					IP:            l.IP,
					MAC:           l.MAC,
					Hostname:      l.Hostname,
					ClientID:      l.ClientID,
					PriorMAC:      h.MAC,
					PriorClientID: h.ClientID,
					PriorHostname: h.Hostname,
					DetectedAt:    now,
				})
				break
			}
		}
	}

	// 3. Brand-new (MAC, Client-ID) pair claiming an existing hostname?
	if l.Hostname != "" {
		// "Brand-new" = no history entry matching this MAC or Client-ID.
		isNewIdentity := true
		for _, h := range m.history {
			if (l.MAC != "" && h.MAC == l.MAC) || (l.ClientID != "" && h.ClientID == l.ClientID) {
				isNewIdentity = false
				break
			}
		}
		if isNewIdentity {
			for _, h := range m.history {
				if h.Hostname == l.Hostname {
					m.recordAnomaly(Anomaly{
						Kind:          AnomalyNewDeviceStealsHostname,
						IP:            l.IP,
						MAC:           l.MAC,
						Hostname:      l.Hostname,
						ClientID:      l.ClientID,
						PriorMAC:      h.MAC,
						PriorClientID: h.ClientID,
						PriorHostname: h.Hostname,
						DetectedAt:    now,
					})
					break
				}
			}
		}
	}
}

// recordAnomaly inserts a unique anomaly into m.anomalies. Called with
// m.mu held. Dedups within the retention window: if an anomaly of the
// same kind+IP+MAC already exists and is unacknowledged, this one is
// dropped to avoid noise from a stable spoof state.
func (m *Manager) recordAnomaly(a Anomaly) {
	for _, existing := range m.anomalies {
		if existing.Kind == a.Kind && existing.IP == a.IP && existing.MAC == a.MAC &&
			existing.AcknowledgedAt == nil {
			return // already known
		}
	}
	a.ID = newAnomalyID()
	m.anomalies[a.ID] = a
	log.Printf("dhcp anomaly: kind=%s ip=%s mac=%s client_id=%s prior_mac=%s prior_client_id=%s",
		a.Kind, a.IP, a.MAC, a.ClientID, a.PriorMAC, a.PriorClientID)
}

// sweepAnomalies evicts entries older than AnomalyRetention. Called
// with m.mu held.
func (m *Manager) sweepAnomalies(now time.Time) {
	cutoff := now.Add(-AnomalyRetention)
	for id, a := range m.anomalies {
		if a.DetectedAt.Before(cutoff) {
			delete(m.anomalies, id)
		}
	}
}

// historyKey is the stable identifier used to dedup the history table.
// Prefer Client-ID; fall back to MAC; fall back to hostname+IP.
func historyKey(l Lease) string {
	if l.ClientID != "" {
		return "id:" + l.ClientID
	}
	if l.MAC != "" {
		return "mac:" + l.MAC
	}
	return "ipname:" + l.IP + "|" + l.Hostname
}

func newAnomalyID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ANOM-" + strings.ToUpper(hex.EncodeToString(b))
}

// ─── Read API (DNS handler / management API call this) ─────────────

// LookupByIP returns the lease for the given IP literal (v4 OR v6), or
// (Lease{}, false) when not in the current snapshot.
func (m *Manager) LookupByIP(ip string) (Lease, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if l, ok := m.byIP[ip]; ok {
		return l, true
	}
	if l, ok := m.byV6[ip]; ok {
		return l, true
	}
	return Lease{}, false
}

// Snapshot returns a copy of every lease currently in cache. Safe to
// hand to JSON encoders.
func (m *Manager) Snapshot() []Lease {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Lease, 0, len(m.byIP))
	for _, l := range m.byIP {
		out = append(out, l)
	}
	return out
}

// OriginCounts returns the count of leases per Origin value in the
// current snapshot. Entries with zero count are omitted so the metric
// exporter can honour the "no series for zero leases" rule from
// FS-LeaseOriginPrometheusGauges.
func (m *Manager) OriginCounts() map[Origin]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[Origin]int{}
	for _, l := range m.byIP {
		if l.Origin == "" {
			continue
		}
		out[l.Origin]++
	}
	return out
}

// Anomalies returns a copy of the current anomaly set, oldest first.
func (m *Manager) Anomalies() []Anomaly {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Anomaly, 0, len(m.anomalies))
	for _, a := range m.anomalies {
		out = append(out, a)
	}
	return out
}

// AnomaliesForIP returns the (acknowledged or not) anomalies whose IP
// matches.
func (m *Manager) AnomaliesForIP(ip string) []Anomaly {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Anomaly
	for _, a := range m.anomalies {
		if a.IP == ip {
			out = append(out, a)
		}
	}
	return out
}

// Acknowledge sets AcknowledgedAt on an anomaly. Returns ok=false when
// the id doesn't exist.
func (m *Manager) Acknowledge(id string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.anomalies[id]
	if !ok {
		return false
	}
	a.AcknowledgedAt = &now
	m.anomalies[id] = a
	return true
}

// ApplySync is a test affordance: drive the cache from a synthetic
// lease slice without spinning the polling goroutine. Used by package
// unit tests; the acceptance tests use the real Connector path.
func (m *Manager) ApplySync(leases []Lease) {
	m.apply(leases)
}

// Source returns the connector's source label ("kea" / "dnsmasq" /
// "http_json"). Exposed for the M5.1 /metrics exporter so the
// skoed_dhcp_* series can carry an accurate `source` label.
func (m *Manager) Source() string {
	if m.conn == nil {
		return ""
	}
	return m.conn.Source()
}

// LastPollAt returns the wall-clock of the most recent successful
// poll. Zero when no poll has yet succeeded (the very first poll is
// not yet in).
func (m *Manager) LastPollAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastPollAt
}

// PollErrorsTotal returns the cumulative number of failed Fetch()
// calls since process start.
func (m *Manager) PollErrorsTotal() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pollErrorsTotal
}
