package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// testDomainAuthResp mirrors the API's full authenticated response.
type testDomainAuthResp struct {
	Domain             string `json:"domain"`
	ClientIP           string `json:"client_ip"`
	WouldBlock         bool   `json:"would_block"`
	Reason             string `json:"reason"`
	MatchedProfileID   string `json:"matched_profile_id"`
	MatchedBlocklistID string `json:"matched_blocklist_id"`
	BlockPolicy        string `json:"block_policy"`
	LocalDNSAnswer     string `json:"local_dns_answer"`
	SafeSearchRewrite  string `json:"safesearch_rewrite"`
	EvaluatedAt        string `json:"evaluated_at"`
	Error              string `json:"error"`
}

// testDomainGuestResp mirrors the public endpoint's stripped response.
type testDomainGuestResp struct {
	WouldBlock bool   `json:"would_block"`
	Reason     string `json:"reason"`
	Error      string `json:"error"`
}

func newDomainCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "domain",
		Short: "Per-domain debugging tools",
	}
	c.AddCommand(newDomainTestCmd())
	return c
}

func newDomainTestCmd() *cobra.Command {
	var clientIP, profileID string
	var forcePublic bool
	c := &cobra.Command{
		Use:   "test <domain>",
		Short: "Ask the cluster whether a domain would be blocked",
		Long: `Test whether a domain would be blocked by the current rules.

When credentials are available (--auth, $SKOED_AUTH, or
~/.skoed/credentials), uses the authenticated /api/v1/test-domain
endpoint which returns the full reasoning chain (matched profile,
matched blocklist, block policy, local-DNS / SafeSearch / allowlist
overrides).

Without credentials, or with --public, falls back to the
unauthenticated /api/v1/_public/test-domain endpoint which only
reports {would_block, reason} against the default profile.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			cl, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			useAuth := !forcePublic && cl.creds.Username != ""
			if useAuth {
				body := map[string]string{"domain": domain}
				if clientIP != "" {
					body["client_ip"] = clientIP
				}
				if profileID != "" {
					body["profile_id"] = profileID
				}
				var resp testDomainAuthResp
				if err := cl.PostJSON("/api/v1/test-domain", body, &resp); err != nil {
					return errExit(err.Error())
				}
				renderAuthVerdict(cmd, resp)
				return nil
			}
			// Guest fallback.
			var resp testDomainGuestResp
			if err := cl.PostJSON("/api/v1/_public/test-domain",
				map[string]string{"domain": domain}, &resp); err != nil {
				return errExit(err.Error())
			}
			renderGuestVerdict(cmd, domain, resp)
			return nil
		},
	}
	c.Flags().StringVar(&clientIP, "client", "", "test as if the query came from this client IP (auth endpoint)")
	c.Flags().StringVar(&profileID, "profile", "", "override profile resolution (auth endpoint)")
	c.Flags().BoolVar(&forcePublic, "public", false, "force the unauthenticated endpoint even if credentials are configured")
	return c
}

func renderAuthVerdict(cmd *cobra.Command, r testDomainAuthResp) {
	verdict := StyleOK.Render("✓ allowed")
	if r.WouldBlock {
		verdict = StyleDanger.Render("✗ blocked")
	}
	writeln(cmd, fmt.Sprintf("%s  %s", verdict, StyleAccent.Render(r.Domain)))
	writeln(cmd, "")

	keyW := lipgloss.NewStyle().Width(20).Foreground(MutedFg)
	row := func(k, v string) {
		if v == "" {
			return
		}
		writeln(cmd, keyW.Render(k)+v)
	}
	row("reason", reasonChip(r.Reason))
	row("matched profile", StyleStrong.Render(r.MatchedProfileID))
	row("matched blocklist", StyleStrong.Render(r.MatchedBlocklistID))
	row("block policy", r.BlockPolicy)
	row("local-DNS answer", r.LocalDNSAnswer)
	row("SafeSearch rewrite", r.SafeSearchRewrite)
	if r.ClientIP != "" {
		row("client IP", r.ClientIP)
	}
}

func renderGuestVerdict(cmd *cobra.Command, domain string, r testDomainGuestResp) {
	verdict := StyleOK.Render("✓ allowed")
	if r.WouldBlock {
		verdict = StyleDanger.Render("✗ blocked")
	}
	writeln(cmd, fmt.Sprintf("%s  %s", verdict, StyleAccent.Render(domain)))
	writeln(cmd, StyleMuted.Render("  reason  ")+reasonChip(r.Reason))
	writeln(cmd, StyleMuted.Render("  (guest endpoint — log in for the full chain)"))
}

func reasonChip(reason string) string {
	switch strings.ToLower(reason) {
	case "blocklist":
		return StyleDanger.Render(reason)
	case "allowlist":
		return StyleOK.Render(reason)
	case "local-dns":
		return StylePink.Render(reason)
	case "safesearch":
		return StyleWarn.Render(reason)
	case "forwarded":
		return StyleAccent.Render(reason)
	}
	return StyleMuted.Render(reason)
}

// silence unused-import warning until errExit moves into root.go's
// exported surface; the helper lives in root.go.
var _ = http.StatusOK
