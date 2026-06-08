package firewallrules

import (
	"strings"
)

// IptablesRenderer emits a paste-ready blob for iptables / ip6tables.
// IPv4 rules go first, an `# === ip6tables ===` divider precedes the
// IPv6 block. Source restriction is omitted when Plan.Sources is empty
// (scope=all). action=reject swaps the verdict to REJECT --reject-with
// icmp-admin-prohibited (icmp6-adm-prohibited for v6).
type IptablesRenderer struct{}

func (IptablesRenderer) Render(p Plan) string {
	var b strings.Builder
	writeHeader(&b, p, "# ")

	// Determine sources. Empty == one synthetic "" source meaning "no -s clause".
	sources := p.Sources
	if len(sources) == 0 {
		sources = []string{""}
	}

	rejectV4 := "-j REJECT --reject-with icmp-admin-prohibited"
	rejectV6 := "-j REJECT --reject-with icmp6-adm-prohibited"
	dropTarget := "-j DROP"

	// IPv4 block.
	for _, r := range p.Resolvers {
		if len(r.IPv4) == 0 {
			continue
		}
		b.WriteString("# resolver: ")
		b.WriteString(strings.ToLower(r.ID))
		b.WriteString("\n")
		for _, ip := range r.IPv4 {
			for _, src := range sources {
				line := "-A FORWARD"
				if src != "" {
					line += " -s " + src
				}
				line += " -d " + ip
				if p.Action == ActionReject {
					line += " " + rejectV4
				} else {
					line += " " + dropTarget
				}
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}

	// IPv6 block, only if any resolver has an IPv6.
	hasV6 := false
	for _, r := range p.Resolvers {
		if len(r.IPv6) > 0 {
			hasV6 = true
			break
		}
	}
	if hasV6 {
		b.WriteString("# === ip6tables ===\n")
		for _, r := range p.Resolvers {
			if len(r.IPv6) == 0 {
				continue
			}
			b.WriteString("# resolver: ")
			b.WriteString(strings.ToLower(r.ID))
			b.WriteString("\n")
			for _, ip := range r.IPv6 {
				for _, src := range sources {
					line := "-A FORWARD"
					if src != "" {
						line += " -s " + src
					}
					line += " -d " + ip
					if p.Action == ActionReject {
						line += " " + rejectV6
					} else {
						line += " " + dropTarget
					}
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}
