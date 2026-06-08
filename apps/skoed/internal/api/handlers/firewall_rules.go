// firewall_rules.go — M6 TS-FwRule paste-ready firewall rule emitter.
//
// One endpoint, five rendering backends:
//
//   GET /api/v1/firewall-rules?platform=...&scope=...&...
//
// The handler validates input, materialises a firewallrules.Plan from
// the curated DoH/DoT resolver snapshot, dispatches to the per-platform
// renderer, and emits the result as text/plain.
//
// Auth: mounted inside the authenticated group (Basic + audit
// middleware). 401 is delivered by the BasicAuth middleware before
// this handler runs. Audit middleware exempts GETs.
//
// SSRF: no outbound calls — every input is consumed from in-memory
// caches (the resolver snapshot is owned by the M6 scheduler).
//
// Metric: bumps skoed_firewall_rules_generated_total{platform} once
// per successful 200 response; 4xx/5xx do not increment.

package handlers

import (
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/dohresolvers"
	"github.com/skoed/skoed/internal/firewallrules"
)

// AppFirewallRules is the slice of api.App the handler touches.
// Implemented by *api.App.
type AppFirewallRules interface {
	GetCfg() *config.Config
	GetDohResolverSnapshot() (*dohresolvers.Snapshot, error)
	DohResolverStaleAfter() time.Duration
	ObserveFirewallRulesGenerated(platform string)
}

// GenerateFirewallRules handles GET /api/v1/firewall-rules. See
// TS-FwRule for the request shape and validation order.
func GenerateFirewallRules(app AppFirewallRules) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// 1. platform
		platform, ok := firewallrules.ParsePlatform(q.Get("platform"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":                "unsupported platform; expected one of " + strings.Join(firewallrules.SupportedPlatforms(), ", "),
				"supported_platforms":  firewallrules.SupportedPlatforms(),
			})
			return
		}

		// 2. action
		action, ok := firewallrules.ParseAction(q.Get("action"))
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown action; expected drop or reject")
			return
		}

		// 3. scope
		scope, ok := firewallrules.ParseScope(q.Get("scope"))
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown scope; expected all, subnet, or profile")
			return
		}

		// 4 & 5. resolve sources by scope
		var sources []string
		var scopeLabel string
		switch scope {
		case firewallrules.ScopeAll:
			scopeLabel = "all"
		case firewallrules.ScopeSubnet:
			raw := q.Get("subnet")
			if raw == "" {
				writeError(w, http.StatusBadRequest, "invalid subnet: subnet parameter required when scope=subnet")
				return
			}
			pfx, err := netip.ParsePrefix(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid subnet: "+err.Error())
				return
			}
			sources = []string{pfx.String()}
			scopeLabel = "subnet=" + pfx.String()
		case firewallrules.ScopeProfile:
			pid := q.Get("profile")
			if pid == "" {
				writeError(w, http.StatusBadRequest, "profile parameter required when scope=profile")
				return
			}
			cfg := app.GetCfg()
			if cfg == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found: " + pid})
				return
			}
			var match *config.Profile
			for i := range cfg.Profiles {
				if cfg.Profiles[i].ID == pid {
					match = &cfg.Profiles[i]
					break
				}
			}
			if match == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found: " + pid})
				return
			}
			sources = append(sources, match.ClientIPs...)
			sources = append(sources, match.ClientCIDRs...)
			scopeLabel = "profile=" + pid
		}

		// 6. resolver snapshot
		snap, err := app.GetDohResolverSnapshot()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "resolver snapshot unavailable: "+err.Error())
			return
		}
		if snap == nil || snap.SnapshotID == "" || len(snap.Resolvers) == 0 {
			writeError(w, http.StatusServiceUnavailable, "resolver snapshot unavailable; trigger a refresh")
			return
		}

		plan := buildPlan(snap, app.DohResolverStaleAfter(), scope, scopeLabel, sources, action)

		body := firewallrules.Generate(platform, plan)

		// Provenance headers (FS-FwRuleHeaderCarriesSnapshotProvenance).
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Skoed-Snapshot-Id", plan.Snapshot.ID)
		w.Header().Set("X-Skoed-Snapshot-Fetched", plan.Snapshot.FetchedAt.UTC().Format(time.RFC3339))
		if plan.Snapshot.Stale {
			w.Header().Set("X-Skoed-Snapshot-Stale", "true")
		} else {
			w.Header().Set("X-Skoed-Snapshot-Stale", "false")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))

		// Counter only on the 200 path.
		app.ObserveFirewallRulesGenerated(string(platform))
	}
}

// buildPlan materialises the renderer plan from the snapshot and the
// already-validated query parameters. The resolver list is copied into
// the plan to insulate the renderers from snapshot mutation mid-render.
func buildPlan(
	snap *dohresolvers.Snapshot,
	staleAfter time.Duration,
	scope firewallrules.Scope,
	scopeLabel string,
	sources []string,
	action firewallrules.Action,
) firewallrules.Plan {
	entries := make([]firewallrules.ResolverEntry, len(snap.Resolvers))
	for i, e := range snap.Resolvers {
		entries[i] = firewallrules.ResolverEntry{
			ID:   e.ID,
			Name: e.Name,
			IPv4: append([]string(nil), e.IPv4...),
			IPv6: append([]string(nil), e.IPv6...),
		}
	}

	fetched, _ := time.Parse(time.RFC3339, snap.FetchedAt)
	stale := false
	if staleAfter > 0 && !fetched.IsZero() && time.Since(fetched) > staleAfter {
		stale = true
	}

	return firewallrules.Plan{
		Scope:      scope,
		ScopeLabel: scopeLabel,
		Sources:    sources,
		Resolvers:  entries,
		Action:     action,
		Snapshot: firewallrules.SnapshotMeta{
			ID:            snap.SnapshotID,
			FetchedAt:     fetched,
			ResolverCount: len(entries),
			Stale:         stale,
		},
		GeneratedAt: time.Now().UTC(),
	}
}

