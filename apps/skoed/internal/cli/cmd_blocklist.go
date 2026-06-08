package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/filter"
	"github.com/spf13/cobra"
)

func newBlocklistCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "blocklist",
		Short: "Blocklist utilities",
	}
	c.AddCommand(newBlocklistTestCmd())
	return c
}

func newBlocklistTestCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "test <url>",
		Short: "Fetch and parse a blocklist URL, print a summary (no daemon required)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				return errExit("URL must start with http:// or https://")
			}
			start := time.Now()
			domains, err := filter.Download(url, format, 30*time.Second)
			elapsed := time.Since(start)
			if err != nil {
				writeln(cmd, StyleDanger.Render("✗")+" "+url)
				writeln(cmd, "  "+StyleMuted.Render(err.Error()))
				return fmt.Errorf("download/parse failed")
			}
			renderTestOK(cmd, url, format, len(domains), elapsed)
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "auto", "hosts|domainlist|askoed|auto")
	return c
}

func renderTestOK(cmd *cobra.Command, url, format string, count int, elapsed time.Duration) {
	writeln(cmd, StyleOK.Render("✓")+" "+StyleAccent.Render(url))
	rows := [][2]string{
		{"format",  fmt.Sprintf("%s%s", format, autoSuffix(format))},
		{"domains", fmt.Sprintf("%d", count)},
		{"elapsed", elapsed.Round(10 * time.Millisecond).String()},
	}
	for _, r := range rows {
		writeln(cmd, "  "+StyleMuted.Render(rpad(r[0], 9))+" "+StyleStrong.Render(r[1]))
	}
}

func autoSuffix(f string) string {
	if f == "auto" {
		return " (auto-detected)"
	}
	return ""
}

func rpad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
