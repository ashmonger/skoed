package dhcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ExportFormat is the discriminator on which static-reservation syntax
// to emit. Supported values: "dnsmasq", "kea", "json".
type ExportFormat string

const (
	ExportDnsmasq ExportFormat = "dnsmasq"
	ExportKea     ExportFormat = "kea"
	ExportJSON    ExportFormat = "json"
)

// Export renders the lease snapshot as static-reservation syntax in the
// chosen format. Leases without a MAC are skipped — most DHCP servers
// pin reservations by MAC, and a reservation without a MAC isn't useful.
//
// The output is sorted by IP for stable diffs.
func Export(leases []Lease, format ExportFormat) ([]byte, error) {
	usable := make([]Lease, 0, len(leases))
	for _, l := range leases {
		if l.MAC == "" {
			continue
		}
		usable = append(usable, l)
	}
	sort.Slice(usable, func(i, j int) bool { return usable[i].IP < usable[j].IP })

	switch format {
	case ExportDnsmasq:
		return exportDnsmasq(usable), nil
	case ExportKea:
		return exportKea(usable)
	case ExportJSON:
		return exportJSON(usable)
	default:
		return nil, fmt.Errorf("unknown export format: %q (want dnsmasq|kea|json)", format)
	}
}

// dnsmasq dhcp-host syntax. One line per device.
//
//   dhcp-host=<mac>[,<ip>][,<hostname>][,<lease-time>]
func exportDnsmasq(ls []Lease) []byte {
	var b strings.Builder
	b.WriteString("# dnsmasq static reservations exported from dblock\n")
	b.WriteString("# Drop into /etc/dnsmasq.d/dblock-reservations.conf and reload dnsmasq.\n")
	for _, l := range ls {
		b.WriteString("dhcp-host=")
		b.WriteString(l.MAC)
		b.WriteString(",")
		b.WriteString(l.IP)
		if l.Hostname != "" {
			b.WriteString(",")
			b.WriteString(l.Hostname)
		}
		b.WriteString(",infinite\n")
	}
	return []byte(b.String())
}

// Kea host-reservation JSON. Emits an array suitable for splicing into
// the relevant subnet's `reservations` field.
type keaReservation struct {
	HWAddress string `json:"hw-address"`
	IPAddress string `json:"ip-address"`
	Hostname  string `json:"hostname,omitempty"`
	ClientID  string `json:"client-id,omitempty"`
}

func exportKea(ls []Lease) ([]byte, error) {
	out := make([]keaReservation, 0, len(ls))
	for _, l := range ls {
		out = append(out, keaReservation{
			HWAddress: l.MAC,
			IPAddress: l.IP,
			Hostname:  l.Hostname,
			ClientID:  l.ClientID,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// Generic JSON. Same shape as the Lease struct minus the source /
// expires_at, since reservations are static.
func exportJSON(ls []Lease) ([]byte, error) {
	type genericReservation struct {
		IP       string `json:"ip"`
		MAC      string `json:"mac"`
		Hostname string `json:"hostname,omitempty"`
		ClientID string `json:"client_id,omitempty"`
	}
	out := make([]genericReservation, 0, len(ls))
	for _, l := range ls {
		out = append(out, genericReservation{
			IP:       l.IP,
			MAC:      l.MAC,
			Hostname: l.Hostname,
			ClientID: l.ClientID,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}
