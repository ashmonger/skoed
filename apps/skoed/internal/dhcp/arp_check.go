// arp_check.go — M6.5 ARP/NDP cross-check sweep (TS-ArpCheck).
//
// A background goroutine dumps the kernel's neighbour table once per
// sweep_interval and compares each DHCP lease's MAC with what the
// kernel has recorded for the same IP. Four new anomaly kinds are
// produced:
//
//	arp_mac_mismatch   — IPv4 DHCP MAC ≠ kernel ARP MAC
//	ndp_mac_mismatch   — IPv6 DHCP MAC ≠ kernel NDP MAC
//	ghost_lease        — lease > 6 h old, kernel never saw the IP or MAC
//	unseen_by_kernel   — lease > 30 min old, no current ARP/NDP entry
//
// The sweep is best-effort: it never blocks DHCP polling and degrades
// gracefully when netlink is unavailable (no CAP_NET_ADMIN).
package dhcp

import (
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NeighEntry is one row in the kernel's neighbour table.
type NeighEntry struct {
	MAC   string // lower-cased hex "aa:bb:cc:dd:ee:ff"
	State string // "reachable", "stale", "delay", "probe", "failed", "none"
}

// NeighborProvider abstracts the kernel neighbour table so tests can
// inject a fake table without touching the real kernel.
type NeighborProvider interface {
	// Dump returns the combined IPv4 + IPv6 neighbour table keyed by IP
	// string. An error means the dump failed transiently; the caller
	// decides whether to flag netlink as permanently unavailable.
	Dump() (map[string]NeighEntry, error)
}

// ArpStateEntry is the per-IP view served by GET /api/v1/clients/{ip}/arp-state.
type ArpStateEntry struct {
	IP               string
	MacDhcp          string
	MacKernel        string
	KernelState      string // "reachable"|"stale"|…|"none"|"netlink_unavailable"
	LastObservedUnix int64  // unix epoch of last netlink dump that included this IP
	Anomaly          string // active anomaly kind for this IP, or ""
}

// arpSweeper owns the sweep goroutine and the per-IP neighbour cache
// that the arp-state handler reads from.
type arpSweeper struct {
	mgr      *Manager
	provider NeighborProvider

	mu           sync.RWMutex
	cache        map[string]arpCacheEntry // key: IP string
	macSeen      map[string]time.Time     // key: lower MAC; value: last-seen-in-kernel
	netlinkAvail bool                     // false = permanently disabled this run

	sweepInterval       time.Duration
	ghostThreshold      time.Duration
	unseenGrace         time.Duration
	macSeenRetention    time.Duration
	netlinkReprobeEvery time.Duration
	lastNetlinkAttempt  time.Time

	// running flag prevents overlapping sweeps.
	running atomic.Bool

	// test affordance: offset all historyEntry.FirstSeen by this duration
	// when evaluating ghost_lease / unseen_by_kernel thresholds.
	firstSeenOffset time.Duration
}

type arpCacheEntry struct {
	MacKernel        string
	State            string
	LastObservedUnix int64
}

func newArpSweeper(mgr *Manager, provider NeighborProvider) *arpSweeper {
	s := &arpSweeper{
		mgr:                 mgr,
		provider:            provider,
		cache:               map[string]arpCacheEntry{},
		macSeen:             map[string]time.Time{},
		netlinkAvail:        true,
		sweepInterval:       90 * time.Second,
		ghostThreshold:      6 * time.Hour,
		unseenGrace:         30 * time.Minute,
		macSeenRetention:    24 * time.Hour,
		netlinkReprobeEvery: time.Hour,
	}
	// Test affordance: SKOED_TEST_LEASE_FIRST_SEEN_OFFSET=45m
	if v := os.Getenv("SKOED_TEST_LEASE_FIRST_SEEN_OFFSET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			s.firstSeenOffset = d
		}
	}
	return s
}

func (s *arpSweeper) run(stop <-chan struct{}) {
	// First sweep happens quickly after start so tests don't wait 90 s.
	timer := time.NewTimer(1500 * time.Millisecond)
	tick := time.NewTicker(s.sweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
			s.sweep()
		case <-tick.C:
			s.sweep()
		}
	}
}

func (s *arpSweeper) sweep() {
	if !s.running.CompareAndSwap(false, true) {
		log.Printf("arp_sweep_skipped=true reason=overlap")
		return
	}
	defer s.running.Store(false)

	// If netlink has been permanently flagged unavailable, try a reprobe
	// every netlinkReprobeEvery to pick up newly-granted capabilities.
	s.mu.Lock()
	avail := s.netlinkAvail
	lastAttempt := s.lastNetlinkAttempt
	s.mu.Unlock()

	if !avail {
		if time.Since(lastAttempt) < s.netlinkReprobeEvery {
			// Short-circuit; keep the cache's "netlink_unavailable" state.
			return
		}
	}

	s.mu.Lock()
	s.lastNetlinkAttempt = time.Now()
	s.mu.Unlock()

	table, err := s.provider.Dump()
	now := time.Now()

	if err != nil {
		// Check if this is a permanent (capability) failure.
		if isPermError(err) {
			s.mu.Lock()
			if s.netlinkAvail {
				log.Printf("event=netlink_unavailable cap_net_admin=false error=%v", err)
			}
			s.netlinkAvail = false
			// Overwrite cache entries with netlink_unavailable so the
			// arp-state handler surfaces the right kernel_state.
			for k, e := range s.cache {
				e.State = "netlink_unavailable"
				e.MacKernel = ""
				s.cache[k] = e
			}
			s.mu.Unlock()
			return
		}
		log.Printf("event=arp_sweep_error error=%v", err)
		return
	}

	// Successful dump — mark netlink as available.
	s.mu.Lock()
	s.netlinkAvail = true
	s.mu.Unlock()

	// Build an updated cache and extend macSeen.
	newCache := make(map[string]arpCacheEntry, len(table))
	nowUnix := now.Unix()

	s.mu.Lock()
	for ip, e := range table {
		newCache[ip] = arpCacheEntry{
			MacKernel:        e.MAC,
			State:            e.State,
			LastObservedUnix: nowUnix,
		}
		if e.MAC != "" {
			s.macSeen[e.MAC] = now
		}
	}
	// For IPs in the old cache that are no longer in the table, set
	// State="none" (preserve the last-observed unix).
	for ip, old := range s.cache {
		if _, present := newCache[ip]; !present {
			old.State = "none"
			old.MacKernel = ""
			// Keep the LastObservedUnix at its prior value — it
			// records when we last actually saw this IP, not "now".
			newCache[ip] = old
		}
	}
	// Evict stale macSeen entries.
	for mac, t := range s.macSeen {
		if now.Sub(t) > s.macSeenRetention {
			delete(s.macSeen, mac)
		}
	}
	s.cache = newCache
	s.mu.Unlock()

	// Run the cross-check decision tree over the current lease snapshot.
	s.mgr.mu.RLock()
	leases := make([]Lease, 0, len(s.mgr.byIP))
	for _, l := range s.mgr.byIP {
		leases = append(leases, l)
	}
	history := s.mgr.history
	s.mgr.mu.RUnlock()

	for _, lease := range leases {
		s.crossCheck(lease, table, history, now)
	}
}

func (s *arpSweeper) crossCheck(lease Lease, table map[string]NeighEntry, history map[string]historyEntry, now time.Time) {
	// Netlink unavailable → record nothing, set cache entry.
	s.mu.RLock()
	avail := s.netlinkAvail
	s.mu.RUnlock()
	if !avail {
		return
	}

	ip := lease.IP
	macDhcp := normMAC(lease.MAC)

	entry, inTable := table[ip]

	if !inTable {
		// IP not in kernel neighbour table at all.
		// Determine first-seen age (with test affordance).
		var firstSeen time.Time
		if h, ok := history[ip]; ok {
			firstSeen = h.FirstSeen
		} else {
			firstSeen = now // brand new — no history yet
		}
		if !firstSeen.IsZero() && s.firstSeenOffset > 0 {
			firstSeen = firstSeen.Add(-s.firstSeenOffset)
		}
		age := now.Sub(firstSeen)

		s.mu.RLock()
		macEverSeen := s.macSeen[macDhcp]
		s.mu.RUnlock()

		if age > s.ghostThreshold && macEverSeen.IsZero() {
			s.mgr.mu.Lock()
			s.mgr.recordAnomaly(Anomaly{
				Kind:       AnomalyGhostLease,
				IP:         ip,
				MAC:        macDhcp,
				DetectedAt: now,
			})
			s.mgr.mu.Unlock()
		} else if age > s.unseenGrace {
			s.mgr.mu.Lock()
			s.mgr.recordAnomaly(Anomaly{
				Kind:       AnomalyUnseenByKernel,
				IP:         ip,
				MAC:        macDhcp,
				DetectedAt: now,
			})
			s.mgr.mu.Unlock()
		}
		return
	}

	macKernel := normMAC(entry.MAC)
	if macKernel != "" && macKernel != macDhcp {
		kind := AnomalyArpMacMismatch
		if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() == nil {
			kind = AnomalyNdpMacMismatch
		}
		s.mgr.mu.Lock()
		s.mgr.recordAnomaly(Anomaly{
			Kind:       kind,
			IP:         ip,
			MAC:        macDhcp,
			PriorMAC:   macKernel,
			DetectedAt: now,
		})
		s.mgr.mu.Unlock()
	}
}

// ArpState returns the current ARP cross-check state for one IP as seen
// by the last sweep. Returns (entry, true) when the IP is known (i.e., a
// lease exists in the manager snapshot), (zero, false) when unknown.
func (s *arpSweeper) ArpState(ip string) (ArpStateEntry, bool) {
	// Lease must exist.
	s.mgr.mu.RLock()
	lease, hasLease := s.mgr.byIP[ip]
	anomalyKind := s.mgr.activeArpAnomalyKind(ip)
	avail := true // re-read under arpSweeper lock below
	s.mgr.mu.RUnlock()
	if !hasLease {
		return ArpStateEntry{}, false
	}

	s.mu.RLock()
	avail = s.netlinkAvail
	cached, hasCached := s.cache[ip]
	s.mu.RUnlock()

	entry := ArpStateEntry{
		IP:      ip,
		MacDhcp: normMAC(lease.MAC),
		Anomaly: string(anomalyKind),
	}

	if !avail {
		entry.KernelState = "netlink_unavailable"
		return entry, true
	}

	if hasCached {
		entry.MacKernel = cached.MacKernel
		entry.KernelState = cached.State
		entry.LastObservedUnix = cached.LastObservedUnix
	} else {
		entry.KernelState = "none"
	}
	return entry, true
}

// activeArpAnomalyKind returns the active (unacknowledged) M6.5 anomaly
// kind for an IP, or "". Caller must hold m.mu.RLock.
func (m *Manager) activeArpAnomalyKind(ip string) AnomalyKind {
	for _, a := range m.anomalies {
		if a.IP != ip || a.AcknowledgedAt != nil {
			continue
		}
		switch a.Kind {
		case AnomalyArpMacMismatch, AnomalyNdpMacMismatch,
			AnomalyGhostLease, AnomalyUnseenByKernel:
			return a.Kind
		}
	}
	return ""
}

func normMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

// isPermError reports whether an error from Dump() indicates a permanent
// capability problem (EPERM/EACCES) vs a transient failure.
func isPermError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "eperm") ||
		strings.Contains(msg, "eacces")
}
