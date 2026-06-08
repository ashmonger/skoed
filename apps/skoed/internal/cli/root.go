package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// Build metadata wired from main via SetBuildInfo.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

// SetBuildInfo lets main inject the linker-supplied version/commit.
func SetBuildInfo(version, commit string) {
	buildVersion = version
	buildCommit = commit
}

// DaemonFn is the existing daemon entry point. Wired by main.go so
// `skoed` (no subcommand) and `skoed daemon` both fall through to it.
type DaemonFn func(cfgPath string) error

// Execute builds the cobra tree and runs it. Returns the cobra error
// (or nil); main.go handles the os.Exit so we don't double-fault on
// test harnesses that import this package.
func Execute(daemon DaemonFn) error {
	root := &cobra.Command{
		Use:           "skoed",
		Short:         "skoed — self-hosted DNS filtering with multi-node sync",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Back-compat: when invoked as `skoed --config /etc/skoed/config.yaml`
		// with no subcommand, fall through to the daemon. cobra calls
		// RunE on the root command when no subcommand matches.
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := cmd.Flags().GetString("config")
			return daemon(cfg)
		},
	}

	// Global flags. --config is daemon-only but lives on root so the
	// systemd unit (`skoed --config /etc/skoed/config.yaml`) keeps
	// working without an explicit `daemon` subcommand.
	root.PersistentFlags().String("api", "", "management API base URL (default http://127.0.0.1:8080 or $SKOED_API)")
	root.PersistentFlags().String("auth", "", "credentials as user:pass (overrides ~/.skoed/credentials)")
	root.Flags().String("config", "config.yaml", "path to config.yaml (daemon mode)")

	// --version on the root prints the version line and exits.
	root.SetVersionTemplate(versionLine() + "\n")
	root.Version = buildVersion // populates cobra's version field; we still set the template above

	// Subcommands.
	root.AddCommand(newDaemonCmd(daemon))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newHealthCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newTokenCmd())
	root.AddCommand(newBlocklistCmd())
	root.AddCommand(newTopCmd())

	return root.Execute()
}

func versionLine() string {
	return fmt.Sprintf("skoed %s (commit=%s, go=%s)", buildVersion, buildCommit, runtime.Version())
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and Go runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return nil
		},
	}
}

func newDaemonCmd(daemon DaemonFn) *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Run the skoed daemon (default when no subcommand is given)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := cmd.Flags().GetString("config")
			return daemon(cfg)
		},
	}
	c.Flags().String("config", "config.yaml", "path to config.yaml")
	return c
}

// resolveClient is a small helper used by every authenticated
// subcommand to build a Client from --auth / --api / env / file.
func resolveClient(cmd *cobra.Command) (*Client, error) {
	authFlag, _ := cmd.Flags().GetString("auth")
	apiFlag, _ := cmd.Flags().GetString("api")
	creds, err := LoadCredentials(authFlag, apiFlag)
	if err != nil {
		return nil, err
	}
	return NewClient(creds), nil
}

// errExit writes msg in danger style to stderr and returns it as an
// error so cobra's SilenceErrors leaves the styling clean.
func errExit(msg string) error { return fmt.Errorf("%s", StyleDanger.Render(msg)) }

// stdout helper that respects the cobra OutOrStdout (testing-friendly).
func writeln(cmd *cobra.Command, s string) {
	fmt.Fprintln(cmd.OutOrStdout(), s)
}

var _ = os.Exit // future: per-subcommand exit codes
