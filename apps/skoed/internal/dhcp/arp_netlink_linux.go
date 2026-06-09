//go:build linux

package dhcp

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
)

// testArpProvider is returned when SKOED_TEST_ARP_TABLE is set.
// Format: "ip=mac,state;ip=mac,state" or "" for empty table.
type testArpProvider struct {
	table map[string]NeighEntry
}

func (p *testArpProvider) Dump() (map[string]NeighEntry, error) {
	return p.table, nil
}

// netlinkProvider calls RTM_GETNEIGH for both address families.
type netlinkProvider struct{}

func (p *netlinkProvider) Dump() (map[string]NeighEntry, error) {
	// SKOED_TEST_NETLINK_UNAVAILABLE forces the error path.
	if os.Getenv("SKOED_TEST_NETLINK_UNAVAILABLE") == "1" {
		return nil, fmt.Errorf("operation not permitted (test override)")
	}

	out := map[string]NeighEntry{}
	for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		neighs, err := netlink.NeighList(0, fam)
		if err != nil {
			return nil, err
		}
		for _, n := range neighs {
			if n.IP == nil || n.HardwareAddr == nil {
				continue
			}
			ip := n.IP.String()
			state := nudState(n.State)
			out[ip] = NeighEntry{
				MAC:   strings.ToLower(n.HardwareAddr.String()),
				State: state,
			}
		}
	}
	return out, nil
}

// nudState converts linux NUD_* bitmask to the string enum in TS-ArpCheck.
func nudState(s int) string {
	switch {
	case s&0x02 != 0: // NUD_REACHABLE
		return "reachable"
	case s&0x04 != 0: // NUD_STALE
		return "stale"
	case s&0x08 != 0: // NUD_DELAY
		return "delay"
	case s&0x10 != 0: // NUD_PROBE
		return "probe"
	case s&0x20 != 0: // NUD_FAILED
		return "failed"
	default:
		return "none"
	}
}

// NewNeighborProvider returns the platform-specific provider. When
// SKOED_TEST_ARP_TABLE is set the provider returns the injected table.
// When SKOED_TEST_NETLINK_UNAVAILABLE is set the real provider is
// returned (but Dump() will error, exercising the degradation path).
func NewNeighborProvider() NeighborProvider {
	if v, ok := os.LookupEnv("SKOED_TEST_ARP_TABLE"); ok {
		return &testArpProvider{table: parseTestArpTable(v)}
	}
	return &netlinkProvider{}
}

// parseTestArpTable decodes SKOED_TEST_ARP_TABLE.
// Format: "192.168.1.1=aa:bb:cc:dd:ee:ff,reachable;fd00::1=11:22:33:44:55:66,stale"
// An empty string yields an empty table.
func parseTestArpTable(v string) map[string]NeighEntry {
	out := map[string]NeighEntry{}
	if v == "" {
		return out
	}
	for _, tok := range strings.Split(v, ";") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			continue
		}
		ip := strings.TrimSpace(tok[:eq])
		rest := strings.TrimSpace(tok[eq+1:])
		parts := strings.SplitN(rest, ",", 2)
		mac := strings.ToLower(strings.TrimSpace(parts[0]))
		state := "reachable"
		if len(parts) == 2 {
			state = strings.TrimSpace(parts[1])
		}
		// Normalise IPv6 addresses so lookups match what Go's net.IP.String() returns.
		if parsed := net.ParseIP(ip); parsed != nil {
			ip = parsed.String()
		}
		out[ip] = NeighEntry{MAC: mac, State: state}
	}
	return out
}
