package firewallrules

import (
	"strings"
)

// MikrotikRenderer emits one `/ip firewall filter add` per IPv4 and
// one `/ipv6 firewall filter add` per IPv6, each carrying a
// comment="skoed doh-gap: <resolver>". scope=all omits src-address.
// action=reject sets `action=reject reject-with=icmp-admin-prohibited`.
type MikrotikRenderer struct{}

func (MikrotikRenderer) Render(p Plan) string {
	var b strings.Builder
	writeHeader(&b, p, "# ")

	sources := p.Sources
	if len(sources) == 0 {
		sources = []string{""}
	}

	action := "action=drop"
	if p.Action == ActionReject {
		action = "action=reject reject-with=icmp-admin-prohibited"
	}

	for _, r := range p.Resolvers {
		for _, ip := range r.IPv4 {
			for _, src := range sources {
				line := `/ip firewall filter add chain=forward ` + action
				if src != "" {
					line += " src-address=" + src
				}
				line += " dst-address=" + ip
				line += ` comment="skoed doh-gap: ` + strings.ToLower(r.ID) + `"`
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	for _, r := range p.Resolvers {
		for _, ip := range r.IPv6 {
			for _, src := range sources {
				// For v6 rules under subnet/all scope, skip mismatched src.
				if src != "" && p.Scope == ScopeProfile && !strings.Contains(src, ":") {
					continue
				}
				line := `/ipv6 firewall filter add chain=forward ` + action
				if src != "" && strings.Contains(src, ":") {
					line += " src-address=" + src
				}
				line += " dst-address=" + ip
				line += ` comment="skoed doh-gap: ` + strings.ToLower(r.ID) + `"`
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}
