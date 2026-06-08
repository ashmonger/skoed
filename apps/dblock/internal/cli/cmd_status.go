package cli

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type clusterStatus struct {
	ClusterID string `json:"cluster_id"`
	RaftTerm  uint64 `json:"raft_term"`
	LeaderID  string `json:"leader_id"`
	Nodes     []clusterNodeEntry `json:"nodes"`
}

type clusterNodeEntry struct {
	NodeID      string `json:"node_id"`
	Role        string `json:"role"`
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
	LastContact string `json:"last_contact"`
	CommitIndex uint64 `json:"commit_index"`
	SyncState   string `json:"sync_state"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster nodes as a styled table",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			var s clusterStatus
			if err := cl.GetJSON("/api/v1/cluster/status", &s); err != nil {
				return errExit(err.Error())
			}
			renderStatus(cmd, s)
			return nil
		},
	}
}

func renderStatus(cmd *cobra.Command, s clusterStatus) {
	writeln(cmd, StyleHeader.Render(fmt.Sprintf("dblock cluster — term %d", s.RaftTerm)))
	writeln(cmd, "")

	hdr := lipgloss.NewStyle().Bold(true).Foreground(MutedFg)
	col := func(w int) lipgloss.Style { return lipgloss.NewStyle().Width(w) }

	cw := [5]int{14, 12, 12, 22, 10}
	// header
	writeln(cmd, hdr.Render(
		col(cw[0]).Render("NODE")+
			col(cw[1]).Render("ROLE")+
			col(cw[2]).Render("SYNC")+
			col(cw[3]).Render("API")+
			col(cw[4]).Render("COMMIT"),
	))

	for _, n := range s.Nodes {
		row := col(cw[0]).Render(n.NodeID) +
			col(cw[1]).Render(roleLabel(n.Role)) +
			col(cw[2]).Render(syncChip(n.SyncState)) +
			col(cw[3]).Render(n.APIAddress) +
			col(cw[4]).Render(fmt.Sprintf("%d", n.CommitIndex))
		if n.Role == "leader" {
			writeln(cmd, StyleLeaderRow.Render(row))
		} else {
			writeln(cmd, row)
		}
	}
}

func roleLabel(r string) string {
	switch r {
	case "leader":
		return "leader"
	case "follower":
		return "follower"
	case "learner":
		return "learner"
	}
	return r
}

func syncChip(state string) string {
	switch state {
	case "in_sync":
		return StyleOK.Render("in_sync")
	case "behind":
		return StyleWarn.Render("behind")
	case "unreachable":
		return StyleDanger.Render("unreachable")
	}
	return StyleMuted.Render(state)
}
