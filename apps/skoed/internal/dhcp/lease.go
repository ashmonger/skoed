// Package dhcp implements M3.6 read-only DHCP integration.
//
// A Manager polls one configured connector (Kea / dnsmasq / generic
// HTTP-JSON) at a fixed interval, parses lease records into a canonical
// Lease shape, maintains an in-memory snapshot indexed by IP, and tracks
// lease history for anti-spoof anomaly detection.
//
// The package has no Raft / cluster coupling — each node polls its own
// configured source. The recommended deployment is "every skoed node
// points at the same central DHCP server", giving cluster-wide
// consistency without coordinated polling.
package dhcp

import (
	"net"
	"strings"
	"time"
)

// Lease is the canonical record produced by every connector. JSON tags
// match the response shape served by /api/v1/clients/{ip} and the
// internal lease-snapshot endpoint.
//
// M6.5 extensions (TS-LeaseOrigin, TS-Dhcpv6Lease):
//   - Origin / OriginConfidence — observational tags filled in at parse
//     time by each connector. Stay empty when DHCP integration produces
//     no claim (M3.6 wire shape preserved via omitempty).
//   - IPv6Addresses / DUID / IsDualStack — added by the v6 lease-parsing
//     path. IsDualStack is set by the Manager's merge step, never by a
//     connector directly.
type Lease struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`
	Hostname  string    `json:"hostname"`
	ClientID  string    `json:"client_id"`
	Source    string    `json:"source"`
	ExpiresAt time.Time `json:"expires_at"`

	// M6.5 — origin tagging (TS-LeaseOrigin).
	Origin           Origin           `json:"origin,omitempty"`
	OriginConfidence OriginConfidence `json:"origin_confidence,omitempty"`

	// M6.5 — DHCPv6 (TS-Dhcpv6Lease).
	IPv6Addresses []string `json:"ipv6_addresses,omitempty"`
	DUID          string   `json:"duid,omitempty"`
	IsDualStack   bool     `json:"is_dual_stack,omitempty"`
}

// Origin tags how an upstream DHCP source describes a lease's allocation.
// dhcp_static  — operator-reserved (matched against a host reservation)
// dhcp_dynamic — pool-assigned
// router_advertised / manual_admin — reserved values for M7+ SLAAC and
//                                    admin-curated static entries
type Origin string

const (
	OriginDhcpStatic       Origin = "dhcp_static"
	OriginDhcpDynamic      Origin = "dhcp_dynamic"
	OriginRouterAdvertised Origin = "router_advertised"
	OriginManualAdmin      Origin = "manual_admin"
)

// OriginConfidence describes how trustworthy the Origin tag is.
// high     — structured upstream API confirmed it (Kea reservation-get-all,
//             http_json explicit field)
// inferred — parsed from a config file (dnsmasq dhcp-host directives)
// unknown  — upstream didn't say (config unreadable, reservation API down,
//             garbage on the wire) — UI mutes the chip
type OriginConfidence string

const (
	OriginConfidenceHigh     OriginConfidence = "high"
	OriginConfidenceInferred OriginConfidence = "inferred"
	OriginConfidenceUnknown  OriginConfidence = "unknown"
)

// normalize lowercases the MAC and trims whitespace. Always call after
// parsing connector data.
func (l *Lease) normalize() {
	l.IP = strings.TrimSpace(l.IP)
	l.MAC = strings.ToLower(strings.TrimSpace(l.MAC))
	l.Hostname = strings.TrimSpace(l.Hostname)
	if l.Hostname == "*" {
		// dnsmasq writes "*" when the client provided no hostname.
		l.Hostname = ""
	}
	l.ClientID = strings.TrimSpace(l.ClientID)
	if l.ClientID == "*" {
		l.ClientID = ""
	}
}

// IsExpired returns true when ExpiresAt is set and before `now`.
func (l Lease) IsExpired(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(now)
}

// ParseIP returns the lease IP as net.IP, or nil if malformed.
func (l Lease) ParseIP() net.IP { return net.ParseIP(l.IP) }

// AnomalyKind is the discriminator on the anti-spoof event type. New
// kinds must be added to ROADMAP.md and the dhcp-spoof-detection spec.
type AnomalyKind string

const (
	// AnomalyMacChangedForClientID — a previously-known Client-ID is now
	// reported with a different MAC. Strongest spoof signal.
	AnomalyMacChangedForClientID AnomalyKind = "mac_changed_for_client_id"
	// AnomalyClientIDChangedForMac — a previously-known MAC now reports
	// a different Client-ID. Either a device upgrade or an attacker
	// cloning a MAC.
	AnomalyClientIDChangedForMac AnomalyKind = "client_id_changed_for_mac"
	// AnomalyNewDeviceStealsHostname — a brand-new (MAC, Client-ID)
	// pair appears claiming a hostname that's already in use by another
	// known device.
	AnomalyNewDeviceStealsHostname AnomalyKind = "new_device_steals_hostname"

	// M6.5 — layer-3 ARP/NDP cross-check kinds (TS-ArpCheck).
	AnomalyArpMacMismatch  AnomalyKind = "arp_mac_mismatch"
	AnomalyNdpMacMismatch  AnomalyKind = "ndp_mac_mismatch"
	AnomalyGhostLease      AnomalyKind = "ghost_lease"
	AnomalyUnseenByKernel  AnomalyKind = "unseen_by_kernel"
)

// Anomaly is one anti-spoof event. Stored in bbolt with the same
// JSON-tagged shape served by /api/v1/clients/anomalies.
type Anomaly struct {
	ID             string      `json:"id"`
	Kind           AnomalyKind `json:"kind"`
	DetectedAt     time.Time   `json:"detected_at"`
	IP             string      `json:"ip"`
	MAC            string      `json:"mac,omitempty"`
	Hostname       string      `json:"hostname,omitempty"`
	ClientID       string      `json:"client_id,omitempty"`
	PriorMAC       string      `json:"prior_mac,omitempty"`
	PriorClientID  string      `json:"prior_client_id,omitempty"`
	PriorHostname  string      `json:"prior_hostname,omitempty"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at,omitempty"`
}

// AnomalyRetention is the window after which acknowledged anomalies and
// stale active anomalies are evicted. 7 days per spec.
const AnomalyRetention = 7 * 24 * time.Hour
