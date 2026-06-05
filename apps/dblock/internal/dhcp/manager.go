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
	conn     Connector
	refresh  time.Duration

	mu sync.RWMutex
	// byIP is the current snapshot, freshest poll wins.
	byIP map[string]Lease
	// history is the running record of every (Client-ID, MAC, hostname)
	// tuple ever seen, used by the anti-spoof detector. Keyed by
	// arbitrary stable identifier — see indexKey.
	history map[string]historyEntry
	// anomalies are recent anti-spoof events, keyed by ID. Acknowledged
	// ones linger until the AnomalyRetention sweep.
	anomalies map[string]Anomaly

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
		log.Printf("dhcp %s: poll failed: %v (keeping prior snapshot)", m.conn.Source(), err)
		return
	}
	m.apply(leases)
}

// apply replaces the snapshot and runs anti-spoof detection against
// the prior history. Exported (lowercase via apply) for tests via the
// package-level Apply wrapper.
func (m *Manager) apply(leases []Lease) {
	now := time.Now()
	newByIP := make(map[string]Lease, len(leases))
	// Build the new snapshot first so the anti-spoof scan sees every
	// new lease in the same batch.
	for _, l := range leases {
		newByIP[l.IP] = l
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Anti-spoof: compare each NEW lease against history before updating.
	for _, l := range leases {
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
	m.sweepAnomalies(now)
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

// LookupByIP returns the lease for the given IP, or (Lease{}, false)
// when not in the current snapshot.
func (m *Manager) LookupByIP(ip string) (Lease, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.byIP[ip]
	return l, ok
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
