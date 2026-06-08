---
x-tsid: TS-DblockCli
x-fsid-links:
  - FS-CliVersionFlag
  - FS-CliHealth
  - FS-CliStatus
  - FS-CliTokenCreate
  - FS-CliBlocklistTest
  - FS-CliCredentialsFile
  - FS-TuiTopShowsLiveDashboard
  - FS-CliDaemonStillWorks
---

# TS-DblockCli — charm-stack CLI + TUI

## Stack

- [`spf13/cobra`](https://github.com/spf13/cobra) — verb tree.
- [`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss) — styled
  CLI output. Same hex palette as the SPA's Lipgloss theme
  (accent #874BFD, charm-pink #FF06B7, success #20D998, danger #EB4444).
- [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) — TUI
  for `skoed top`.
- [`charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) — table,
  spinner, viewport for the TUI.

## Command tree

```
skoed                                  → equivalent to `skoed daemon` (back-compat)
├── version                             → prints version+commit+go-version
├── daemon [--config FILE]              → existing daemon behaviour
├── health [--api URL]                  → local node health (one-shot)
├── status [--api URL]                  → cluster table (one-shot)
├── top    [--api URL]                  → live TUI dashboard
├── token
│   ├── create [--ttl 15m]              → POST /cluster/tokens
│   └── list                            → (M7) list active tokens
└── blocklist
    └── test <url> [--format FORMAT]    → fetch+parse, no daemon needed
```

The single-binary invocation `skoed --config /etc/skoed/config.yaml`
keeps working because cobra's default subcommand falls through to
`daemon` when no verb is present.

## Auth

The CLI talks to the management API like any other client. Credentials
are read from (in priority order):

1. `--auth user:pass` flag (overrides everything)
2. `SKOED_AUTH=user:pass` env var
3. `~/.skoed/credentials` (YAML):
   ```yaml
   api_url:  http://127.0.0.1:8080
   username: admin
   password: <password>
   ```
4. Interactive prompt (Bubbletea-styled) when running on a TTY.

The credentials file's mode is enforced to 0600 — the CLI refuses to
read world-readable creds (warns + exits 2). This matches the M5.5
postinst's "be opinionated about secret material" stance.

## Styling

A single `internal/cli/style.go` declares the lipgloss colour roles
shared by every subcommand:

```go
var (
    AccentFg = lipgloss.Color("#874BFD") // SPA accent
    PinkFg   = lipgloss.Color("#FF06B7") // brand pink
    OkFg     = lipgloss.Color("#20D998")
    WarnFg   = lipgloss.Color("#FFB23F")
    DangerFg = lipgloss.Color("#EB4444")
    MutedFg  = lipgloss.Color("#7C7C7C")
)
```

Roles:

- Headers: bold + AccentFg.
- Leader rows in tables: AccentFg background + black foreground.
- OK chips: OkFg; degraded: WarnFg; error: DangerFg.
- Box-drawn copy-paste blocks (for `skoed token create`'s output)
  using `lipgloss.RoundedBorder()`.

NO_COLOR env var is honoured (lipgloss does this natively); piped
output strips ANSI (`isatty.IsTerminal`).

## `skoed top` (bubbletea)

Layout (one screen, refreshes every 2 s):

```
┌──────────────── skoed top ──────────────────────────────┐
│ cluster ok  ·  3/3 members  ·  term 4  ·  commit 1247    │
├──── nodes ─────────────────────────────────────────────────┤
│ ● node-1   leader     in_sync   commit 1247                │
│ ○ node-2   follower   in_sync   commit 1247                │
│ ○ node-3   follower   in_sync   commit 1247                │
├──── DNS (last 60s) ────────────────────────────────────────┤
│ blocked   ████████████░░░░░░░  342      (61%)              │
│ forwarded ███░░░░░░░░░░░░░░░░   84      (15%)              │
│ cached    ██████░░░░░░░░░░░░░  140      (24%)              │
├──── top blocked ───────────────────────────────────────────┤
│ doubleclick.net               87                           │
│ googletagmanager.com          54                           │
│ …                                                          │
├──── audit (last 5) ────────────────────────────────────────┤
│ 12:03:21  user:admin  blocklist.create   ok                │
│ …                                                          │
└────────────────────────────────────────────────────────────┘
[q] quit · [r] refresh · [f] filter
```

Implementation: standard bubbletea `Model`/`Update`/`View` with one
`tea.Tick` for the 2 s refresh and one `tea.Cmd` per panel fetch.
Each panel is a separate bubbles component (table for nodes, custom
bar widget for DNS rate, table for top blocked, table for audit).

## `skoed blocklist test`

Runs entirely in-process — `filter.Download(url, format, 30s)` plus
the format-router under `internal/filter/parsers/`. No HTTP to the
daemon, no auth, no SSRF risk (operator's own process, operator's
own network reach).

Output:

```
$ skoed blocklist test https://github.com/StevenBlack/hosts/raw/master/hosts
✓  https://github.com/.../hosts
   format    hosts (auto-detected)
   domains   162,481
   skipped   31 (commented, localhost, invalid IPs)
   elapsed   2.3s
```

Exit codes: 0 success, 1 HTTP/parse failure, 2 bad invocation.

## Layout

```
apps/skoed/
  cmd/
    skoed/
      main.go            (existing, gains cobra root + daemon subcommand)
  internal/
    cli/                 (NEW)
      cmd_root.go        (root command + global flags)
      cmd_daemon.go      (existing daemon flow, wrapped as a subcommand)
      cmd_version.go
      cmd_health.go
      cmd_status.go
      cmd_token.go
      cmd_blocklist.go   (the test subcommand)
      cmd_top.go         (bubbletea TUI)
      credentials.go     (~/.skoed/credentials handling)
      client.go          (auth-aware HTTP client to the management API)
      style.go           (lipgloss palette)
```

## Acceptance tests

`tests/acceptance/cli_test.go`:

- `TestCliVersion` — runs `skoed --version`, asserts the output shape.
- `TestCliHealth` — boots a 1-node cluster, runs `skoed health
  --api <api>`, asserts exit 0 + "ok" in output.
- `TestCliStatus` — 3-node cluster, runs `skoed status --api <api>`,
  asserts leader row is present.
- `TestCliTokenCreate` — runs `skoed token create`, asserts response
  contains a token + leader_address.
- `TestCliBlocklistTest` — `skoed blocklist test http://test-server`
  against an httptest hosts server, asserts the count.
- `TestCliDaemonStillWorks` — runs `skoed --config <path>` (no
  subcommand), asserts the daemon starts (waitReady).

`skoed top` is NOT acceptance-tested (bubbletea TUI testing is
finicky and the value is low). Manual verification + screenshot.
