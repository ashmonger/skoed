package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type tokenResp struct {
	Token         string `json:"token"`
	ExpiresAt     string `json:"expires_at"`
	LeaderAddress string `json:"leader_address"`
}

func newTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "Cluster join tokens",
	}
	c.AddCommand(&cobra.Command{
		Use:   "create",
		Short: "Issue a single-use join token (operator pastes it into a joining node's config.yaml)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			var r tokenResp
			if err := cl.PostJSON("/api/v1/cluster/tokens", nil, &r); err != nil {
				return errExit(err.Error())
			}
			renderToken(cmd, r, cl.APIURL())
			return nil
		},
	})
	return c
}

func renderToken(cmd *cobra.Command, r tokenResp, fallbackURL string) {
	leader := r.LeaderAddress
	if leader == "" {
		leader = fallbackURL
	}
	writeln(cmd, StyleHeader.Render("Cluster join token"))
	writeln(cmd, StyleMuted.Render("Paste this `bootstrap:` block into the joining node's /etc/dblock/config.yaml,"))
	writeln(cmd, StyleMuted.Render("then `systemctl restart dblock`."))
	writeln(cmd, "")

	body := strings.Join([]string{
		"bootstrap:",
		fmt.Sprintf("  leader_address: %s", leader),
		fmt.Sprintf("  token:          %s", r.Token),
	}, "\n")
	writeln(cmd, StyleBox.Render(body))

	writeln(cmd, "")
	writeln(cmd, StyleMuted.Render(fmt.Sprintf("Token expires at: %s. Single-use.", r.ExpiresAt)))
}
