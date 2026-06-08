package firewallrules

import (
	"strings"
)

// NftablesRenderer emits a single `table inet skoed_doh_gap` with one
// forward chain. IPv4 and IPv6 resolvers are emitted as inline sets;
// scope=profile fans out one rule per source IP since nftables sets
// can't mix families. action=reject swaps `drop` for
// `reject with icmpx admin-prohibited`.
type NftablesRenderer struct{}

func (NftablesRenderer) Render(p Plan) string {
	var b strings.Builder
	writeHeader(&b, p, "# ")

	verdict := "drop"
	if p.Action == ActionReject {
		verdict = "reject with icmpx admin-prohibited"
	}

	// Collect the resolver IPs per family.
	var v4s, v6s []string
	for _, r := range p.Resolvers {
		v4s = append(v4s, r.IPv4...)
		v6s = append(v6s, r.IPv6...)
	}

	sources := p.Sources
	if len(sources) == 0 {
		sources = []string{""}
	}

	b.WriteString("table inet skoed_doh_gap {\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority 0;\n")

	if len(v4s) > 0 {
		v4set := "{ " + strings.Join(v4s, ", ") + " }"
		for _, src := range sources {
			line := "    "
			if src != "" {
				line += "ip saddr " + src + " "
			}
			line += "ip daddr " + v4set + " " + verdict
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(v6s) > 0 {
		v6set := "{ " + strings.Join(v6s, ", ") + " }"
		for _, src := range sources {
			line := "    "
			if src != "" {
				// nftables: a v4 saddr can't filter v6 daddr in the same
				// rule, so for subnet scope we still emit the ip6 rule
				// unscoped (the operator's subnet is v4 only here).
				// For profile scope, each source string is one client IP;
				// emit only when it parses as v6.
				if strings.Contains(src, ":") {
					line += "ip6 saddr " + src + " "
				} else if p.Scope == ScopeProfile {
					// Skip v4 sources on the v6 rule under profile scope.
					continue
				}
			}
			line += "ip6 daddr " + v6set + " " + verdict
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}
