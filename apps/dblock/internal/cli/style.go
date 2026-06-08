// Package cli is the cobra-based CLI surface for dblock (M5.9.1).
//
// One root command plus subcommands (daemon, version, health, status,
// top, token, blocklist). Output is lipgloss-styled with the same
// Lipgloss-dark palette the Web UI uses so the visual identity stays
// consistent. NO_COLOR env / non-TTY pipes are honored natively by
// lipgloss.
package cli

import "github.com/charmbracelet/lipgloss"

// Palette — mirrors web/src/styles/theme/lipgloss.css.
var (
	AccentFg  = lipgloss.Color("#874BFD")
	PinkFg    = lipgloss.Color("#FF06B7")
	OkFg      = lipgloss.Color("#20D998")
	WarnFg    = lipgloss.Color("#FFB23F")
	DangerFg  = lipgloss.Color("#EB4444")
	MutedFg   = lipgloss.Color("#7C7C7C")
	StrongFg  = lipgloss.Color("#FFFFFF")
	BgSubtle  = lipgloss.Color("#1A1A23")
)

// Shared styles.
var (
	StyleHeader = lipgloss.NewStyle().Bold(true).Foreground(AccentFg)
	StyleOK     = lipgloss.NewStyle().Foreground(OkFg).Bold(true)
	StyleWarn   = lipgloss.NewStyle().Foreground(WarnFg).Bold(true)
	StyleDanger = lipgloss.NewStyle().Foreground(DangerFg).Bold(true)
	StyleMuted  = lipgloss.NewStyle().Foreground(MutedFg)
	StyleStrong = lipgloss.NewStyle().Foreground(StrongFg).Bold(true)
	StylePink   = lipgloss.NewStyle().Foreground(PinkFg).Bold(true)
	StyleAccent = lipgloss.NewStyle().Foreground(AccentFg)

	StyleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(AccentFg).
			Padding(0, 2)

	StyleLeaderRow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(AccentFg).
			Bold(true)
)
