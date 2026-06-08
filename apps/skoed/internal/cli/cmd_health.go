package cli

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type clusterHealth struct {
	Status            string `json:"status"`
	NodeID            string `json:"node_id"`
	Role              string `json:"role"`
	Mode              string `json:"mode"`
	HasLeader         bool   `json:"has_leader"`
	Members           int    `json:"members"`
	ReachableMembers  int    `json:"reachable_members"`
	RaftTerm          uint64 `json:"raft_term"`
	CommitIndex       uint64 `json:"commit_index"`
}

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show cluster health (one-shot)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			var h clusterHealth
			if err := cl.GetJSON("/api/v1/cluster/health", &h); err != nil {
				return errExit(err.Error())
			}
			renderHealth(cmd, h)
			if h.Status != "ok" {
				return fmt.Errorf("status: %s", h.Status)
			}
			return nil
		},
	}
}

func renderHealth(cmd *cobra.Command, h clusterHealth) {
	statusStyle := StyleOK
	if h.Status != "ok" {
		statusStyle = StyleWarn
	}
	writeln(cmd, StyleHeader.Render("skoed cluster health"))
	writeln(cmd, "")

	rows := [][2]string{
		{"status",            statusStyle.Render(h.Status)},
		{"node",              StyleStrong.Render(h.NodeID)},
		{"role",              roleChip(h.Role)},
		{"mode",              h.Mode},
		{"members",           fmt.Sprintf("%d / %d reachable", h.ReachableMembers, h.Members)},
		{"raft term",         fmt.Sprintf("%d", h.RaftTerm)},
		{"commit index",      fmt.Sprintf("%d", h.CommitIndex)},
	}
	keyW := lipgloss.NewStyle().Width(14).Foreground(MutedFg)
	for _, r := range rows {
		writeln(cmd, keyW.Render(r[0])+r[1])
	}
}

func roleChip(role string) string {
	switch role {
	case "leader":
		return StylePink.Render("● leader")
	case "follower":
		return StyleAccent.Render("○ follower")
	}
	return StyleMuted.Render("· " + role)
}
