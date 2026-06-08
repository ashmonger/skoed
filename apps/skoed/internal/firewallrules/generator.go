package firewallrules

import (
	"strconv"
	"strings"
	"time"
)

// rendererFor returns the renderer for the platform. The platform
// MUST already be validated (ParsePlatform); a zero-value Renderer
// would otherwise be returned for unknown platforms.
func rendererFor(p Platform) Renderer {
	switch p {
	case PlatformIptables:
		return IptablesRenderer{}
	case PlatformNftables:
		return NftablesRenderer{}
	case PlatformMikrotik:
		return MikrotikRenderer{}
	case PlatformOpnsense:
		return OpnsenseRenderer{}
	case PlatformUnifi:
		return UnifiRenderer{}
	}
	return nil
}

// Generate dispatches a Plan to the platform's renderer.
func Generate(p Platform, plan Plan) string {
	r := rendererFor(p)
	if r == nil {
		return ""
	}
	return r.Render(plan)
}

// writeHeader plants the leading comment block shared by every
// non-UniFi renderer. The UniFi renderer embeds the same fields in
// its JSON payload under `_provenance` because JSON has no native
// comment syntax. linePrefix is the comment marker including a
// trailing space (e.g. "# ").
func writeHeader(b *strings.Builder, p Plan, linePrefix string) {
	b.WriteString(linePrefix)
	b.WriteString("skoed firewall-rule generator\n")
	b.WriteString(linePrefix)
	b.WriteString("snapshot_id:       ")
	b.WriteString(p.Snapshot.ID)
	b.WriteString("\n")
	b.WriteString(linePrefix)
	b.WriteString("snapshot_fetched:  ")
	b.WriteString(p.Snapshot.FetchedAt.UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString(linePrefix)
	b.WriteString("resolver_count:    ")
	b.WriteString(strconv.Itoa(p.Snapshot.ResolverCount))
	b.WriteString("\n")
	b.WriteString(linePrefix)
	b.WriteString("generated_at:      ")
	b.WriteString(p.GeneratedAt.UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString(linePrefix)
	b.WriteString("scope:             ")
	b.WriteString(p.ScopeLabel)
	b.WriteString("\n")
	if p.Snapshot.Stale {
		b.WriteString(linePrefix)
		b.WriteString("WARNING: snapshot is stale (older than 7d)\n")
	}
	b.WriteString(linePrefix)
	b.WriteString("\n")
}
