package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

func newTopCmd() *cobra.Command {
	var snapshot bool
	c := &cobra.Command{
		Use:   "top",
		Short: "Live cluster + DNS + audit dashboard (bubbletea TUI)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			m := newTopModel(cl)
			if snapshot {
				// One-shot: fetch once, render once, exit. Useful for
				// docs / screenshots / CI smoke; no TTY required.
				msg := m.fetchAll()()
				if f, ok := msg.(fetchedMsg); ok {
					m.health = f.health
					m.status = f.status
					m.stats = f.stats
					m.err = f.err
				}
				fmt.Fprintln(cmd.OutOrStdout(), m.View())
				return nil
			}
			p := tea.NewProgram(m, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}
	c.Flags().BoolVar(&snapshot, "snapshot", false, "render one frame and exit (no TTY needed; for docs / smoke)")
	return c
}

// ─── bubbletea model ───────────────────────────────────────────────────

type topKeyMap struct {
	Refresh key.Binding
	Quit    key.Binding
}

func (k topKeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Refresh, k.Quit} }
func (k topKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Refresh}, {k.Quit}}
}

var topKeys = topKeyMap{
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c", "esc"),
		key.WithHelp("q", "quit"),
	),
}

type tickMsg time.Time

type topModel struct {
	cl      *Client
	help    help.Model
	health  *clusterHealth
	status  *clusterStatus
	stats   *clusterStatsView
	err     string
	width   int
	height  int
	lastTick time.Time
}

type clusterStatsView struct {
	WindowFrom string `json:"window_from"`
	WindowTo   string `json:"window_to"`
	Totals     struct {
		Total     int `json:"total"`
		Blocked   int `json:"blocked"`
		Forwarded int `json:"forwarded"`
		Cached    int `json:"cached"`
		Local     int `json:"local"`
	} `json:"cluster_totals"`
	TopDomains []struct {
		Domain string `json:"domain"`
		Count  int    `json:"count"`
	} `json:"top_domains"`
}

func newTopModel(cl *Client) topModel {
	return topModel{
		cl:   cl,
		help: help.New(),
	}
}

func (m topModel) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.tick())
}

func (m topModel) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type fetchedMsg struct {
	health *clusterHealth
	status *clusterStatus
	stats  *clusterStatsView
	err    string
}

func (m topModel) fetchAll() tea.Cmd {
	return func() tea.Msg {
		var f fetchedMsg
		var h clusterHealth
		if err := m.cl.GetJSON("/api/v1/cluster/health", &h); err != nil {
			f.err = err.Error()
		} else {
			f.health = &h
		}
		var s clusterStatus
		if err := m.cl.GetJSON("/api/v1/cluster/status", &s); err == nil {
			f.status = &s
		}
		var st clusterStatsView
		if err := m.cl.GetJSON("/api/v1/cluster/stats", &st); err == nil {
			f.stats = &st
		}
		return f
	}
}

func (m topModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, topKeys.Quit):
			return m, tea.Quit
		case key.Matches(msg, topKeys.Refresh):
			return m, m.fetchAll()
		}
	case tickMsg:
		m.lastTick = time.Time(msg)
		return m, tea.Batch(m.fetchAll(), m.tick())
	case fetchedMsg:
		m.err = msg.err
		if msg.health != nil {
			m.health = msg.health
		}
		if msg.status != nil {
			m.status = msg.status
		}
		if msg.stats != nil {
			m.stats = msg.stats
		}
		return m, nil
	}
	return m, nil
}

func (m topModel) View() string {
	hdr := StyleHeader.Render("skoed top")
	if m.err != "" {
		return hdr + "\n\n" + StyleDanger.Render("error: ") + m.err + "\n\n" + m.help.View(topKeys)
	}
	if m.health == nil {
		return hdr + "\n\n" + StyleMuted.Render("loading…") + "\n\n" + m.help.View(topKeys)
	}

	// Top strip: cluster status line.
	stat := StyleOK.Render(m.health.Status)
	if m.health.Status != "ok" {
		stat = StyleWarn.Render(m.health.Status)
	}
	line := fmt.Sprintf("%s  ·  %d/%d members  ·  term %d  ·  commit %d",
		stat, m.health.ReachableMembers, m.health.Members,
		m.health.RaftTerm, m.health.CommitIndex,
	)

	panels := []string{hdr, line, "", m.renderNodes(), "", m.renderDNS(), "", m.renderTopBlocked()}
	out := strings.Join(panels, "\n")
	footer := "\n\n" + m.help.View(topKeys)
	return out + footer
}

func (m topModel) renderNodes() string {
	out := StyleAccent.Bold(true).Render("── nodes ──")
	if m.status == nil {
		return out + "\n" + StyleMuted.Render("loading…")
	}
	col := func(w int) lipgloss.Style { return lipgloss.NewStyle().Width(w) }
	for _, n := range m.status.Nodes {
		row := col(14).Render(n.NodeID) +
			col(12).Render(roleLabel(n.Role)) +
			col(14).Render(syncChip(n.SyncState)) +
			col(10).Render(fmt.Sprintf("commit %d", n.CommitIndex))
		if n.Role == "leader" {
			out += "\n" + StyleLeaderRow.Render(row)
		} else {
			out += "\n" + row
		}
	}
	return out
}

func (m topModel) renderDNS() string {
	out := StyleAccent.Bold(true).Render("── DNS (window) ──")
	if m.stats == nil {
		return out + "\n" + StyleMuted.Render("loading…")
	}
	totals := []struct {
		label string
		n     int
		style lipgloss.Style
	}{
		{"blocked", m.stats.Totals.Blocked, StyleDanger},
		{"forwarded", m.stats.Totals.Forwarded, StyleOK},
		{"cached", m.stats.Totals.Cached, StyleAccent},
		{"local", m.stats.Totals.Local, StyleWarn},
	}
	total := m.stats.Totals.Total
	if total == 0 {
		return out + "\n" + StyleMuted.Render("no queries yet")
	}
	for _, t := range totals {
		bar := bar(t.n, total, 20)
		pct := 100.0 * float64(t.n) / float64(total)
		out += fmt.Sprintf("\n%s %s %s",
			lipgloss.NewStyle().Width(10).Render(t.label),
			t.style.Render(bar),
			lipgloss.NewStyle().Width(20).Render(fmt.Sprintf("%d (%.1f%%)", t.n, pct)),
		)
	}
	return out
}

func (m topModel) renderTopBlocked() string {
	out := StyleAccent.Bold(true).Render("── top blocked ──")
	if m.stats == nil || len(m.stats.TopDomains) == 0 {
		return out + "\n" + StyleMuted.Render("no data yet")
	}
	for i, d := range m.stats.TopDomains {
		if i >= 5 {
			break
		}
		out += fmt.Sprintf("\n%s  %s",
			lipgloss.NewStyle().Width(40).Render(d.Domain),
			fmt.Sprintf("%d", d.Count),
		)
	}
	return out
}

func bar(n, total, width int) string {
	if total == 0 {
		return strings.Repeat("░", width)
	}
	filled := n * width / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
