package firewallrules

import (
	"strings"
)

// OpnsenseRenderer emits two artefacts: an importable alias listing
// every curated IPv4 + IPv6 member, and a rule descriptor in the
// "add rule" form fields. The leading comment block documents the
// paste flow into the OpnSense UI.
type OpnsenseRenderer struct{}

func (OpnsenseRenderer) Render(p Plan) string {
	var b strings.Builder
	writeHeader(&b, p, "# ")
	b.WriteString("# How to paste:\n")
	b.WriteString("# 1. Open Firewall > Aliases > Import in the OpnSense UI.\n")
	b.WriteString("# 2. Paste the 'skoed_doh_resolvers' alias block below; save.\n")
	b.WriteString("# 3. Open Firewall > Rules > <interface> and add a new rule\n")
	b.WriteString("#    using the fields under '# rule descriptor' below.\n")
	b.WriteString("\n")

	// Alias block.
	b.WriteString("# --- alias ---\n")
	b.WriteString("skoed_doh_resolvers\n")
	for _, r := range p.Resolvers {
		for _, ip := range r.IPv4 {
			b.WriteString("  ")
			b.WriteString(ip)
			b.WriteString("\n")
		}
	}
	for _, r := range p.Resolvers {
		for _, ip := range r.IPv6 {
			b.WriteString("  ")
			b.WriteString(ip)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	// Rule descriptor.
	action := "Block"
	if p.Action == ActionReject {
		action = "Reject"
	}
	b.WriteString("# --- rule descriptor ---\n")
	b.WriteString("Action:           ")
	b.WriteString(action)
	b.WriteString("\n")
	b.WriteString("Interface:        LAN  (operator picks)\n")
	b.WriteString("Direction:        out\n")

	src := "any"
	if len(p.Sources) > 0 {
		src = strings.Join(p.Sources, ", ")
	}
	b.WriteString("Source:           ")
	b.WriteString(src)
	b.WriteString("\n")
	b.WriteString("Destination:      Single host or alias: skoed_doh_resolvers\n")
	return b.String()
}
