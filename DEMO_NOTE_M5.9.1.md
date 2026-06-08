# DEMO NOTE — M5.9.1 `dblock` CLI + TUI (charm-stack)

## Scope

Operators stop reaching for `curl -u admin:pwd …`. Every common
operation has a `dblock <verb>` subcommand styled with the same
Lipgloss palette as the Web UI; live cluster overview lives in a
bubbletea TUI (`dblock top`). The existing systemd-driven daemon
invocation (`dblock --config /etc/dblock/config.yaml`) keeps working
unchanged.

### Implemented

- **Cobra command tree** rooted at `dblock`:
  - `dblock --version` (and `dblock version`) — prints
    `dblock <version> (commit=<hex>, go=go1.<x>)`.
  - `dblock daemon [--config FILE]` — explicit daemon mode.
    Subcommand-less `dblock --config …` falls through to `daemon`,
    so the systemd unit shipped in M5.5 keeps working without edits.
  - `dblock health` — local node's cluster health as a styled
    key/value list. Exit 0 when `status=ok`, 1 when `degraded`.
  - `dblock status` — cluster nodes as a Lipgloss table. The
    leader row is highlighted with reverse-video accent.
  - `dblock token create` — issues a join token + prints the
    operator-pasteable `bootstrap:` YAML block inside a rounded-border
    Lipgloss box.
  - `dblock blocklist test <url>` — fetches + parses entirely
    in-process. No daemon required; no auth; no SSRF risk.
    Sets the stage for M5.9.5.
  - `dblock top` — live TUI (bubbletea) cluster + DNS + top-blocked
    dashboard, hot-keys `q` quit / `r` refresh. Auto-refreshes
    every 2 s. `--snapshot` flag renders one frame and exits (used
    by docs + smoke).
- **`internal/cli/style.go`** centralises the Lipgloss palette
  (accent `#874BFD`, pink `#FF06B7`, success `#20D998`, warn `#FFB23F`,
  danger `#EB4444`, muted `#7C7C7C`) — same hexes as
  `web/src/styles/theme/lipgloss.css`, so terminal + browser feel
  like one product.
- **`internal/cli/credentials.go`** resolves credentials in priority
  order: `--auth`, `DBLOCK_AUTH` env, `~/.dblock/credentials`
  (JSON, mode 0600 enforced — refuses world-readable creds with a
  clear error), default. API URL from `--api`, `DBLOCK_API`, file,
  or `http://127.0.0.1:8080`.
- **`internal/cli/client.go`** — auth-aware HTTP client with
  `InsecureSkipVerify` (admin-tools posture; operator's box, operator's
  cert; revisit when public-cert API deployments land).
- **NO_COLOR / non-TTY pipe** strip ANSI automatically (lipgloss
  honors it natively); colour comes back when stdout is a tty.

### Acceptance tests

6 acceptance tests in `tests/acceptance/cli_test.go`:

| FSID                  | Test                       | Topology |
|-----------------------|----------------------------|----------|
| FS-CliVersionFlag     | TestCliVersion             | n/a      |
| FS-CliHealth          | TestCliHealth              | 1 node   |
| FS-CliStatus          | **TestCliStatus**          | **3 nodes** |
| FS-CliTokenCreate     | TestCliTokenCreate         | 1 node   |
| FS-CliBlocklistTest   | TestCliBlocklistTest       | n/a      |
| FS-CliDaemonStillWorks | TestCliDaemonStillWorks   | 1 node   |

All 6 PASS. The TUI dashboard is intentionally NOT acceptance-tested
(bubbletea integration testing is finicky and the value is low;
manual screenshot + the `--snapshot` flag for visual regression).

Full M1→M5.9.1 acceptance suite green in Docker.

### Screenshots & GIFs

Recorded with [`charmbracelet/vhs`](https://github.com/charmbracelet/vhs) —
`.tape` files committed alongside the outputs so anyone can re-record
on demand. Theme: Catppuccin Mocha (closest to the SPA's Lipgloss dark).

- `docs/screenshots/m5.9.1-dblock-cli.gif` — animated walkthrough of
  `--version → health → status → token create` (~8 s).
- `docs/screenshots/m5.9.1-dblock-top.gif` — animated `dblock top` TUI
  showing the live dashboard, hot-key refresh (`r`), and quit (`q`).
- `docs/screenshots/m5.9.1-dblock-cli.png` — static composite of the
  same CLI verbs (kept as a fallback for markdown renderers that
  don't animate GIFs).
- `docs/screenshots/m5.9.1-dblock-top.png` — static `dblock top
  --snapshot` frame.

Re-record with `cd docs/screenshots && vhs m5.9.1-cli.tape && vhs
m5.9.1-top.tape` (requires a running dblock daemon on the configured
port — see the tape files for the alias setup).

### Not implemented (deferred / non-goals)

- **Shell completion** — cobra ships `dblock completion bash|zsh|fish`
  for free; the binary exposes it but `make completions` for /etc/
  install is M5.9.1.1 follow-up.
- **JSON output mode** — operators wanting JSON use the API directly.
- **Curses YAML editor** — operators edit the file or use the Web UI.
- **Interactive credentials prompt** — for v1, set `~/.dblock/credentials`
  or `DBLOCK_AUTH`. A bubbles/huh-style first-run prompt is M5.9.1.2.

### Files added

```
apps/dblock/internal/cli/
  style.go         — Lipgloss palette + shared styles
  credentials.go   — auth resolution chain
  client.go        — auth-aware HTTP client
  root.go          — cobra root + subcommand wire-up
  cmd_health.go    — dblock health
  cmd_status.go    — dblock status
  cmd_token.go     — dblock token create
  cmd_blocklist.go — dblock blocklist test <url>
  cmd_top.go       — dblock top (bubbletea TUI + --snapshot)

apps/dblock/cmd/dblock/main.go  (refactored: runDaemon body + cli.Execute wrapper)

tests/acceptance/cli_test.go    (6 FSIDs, includes 3-node TestCliStatus)
specs/functional/dblock-cli.feature
specs/technical/dblock-cli.md

web/shoot-cli-tui.mjs    (TUI screenshot helper)
web/shoot-cli-verbs.mjs  (CLI verbs composite screenshot helper)
```

## Demo

```sh
# Version line.
$ dblock --version
dblock 0.5.0 (commit=8bc61d4, go=go1.24.0)

# Cluster health (one-shot).
$ dblock health --api http://127.0.0.1:8080
dblock cluster health

status        ok
node          dblock-1
role          ● leader
mode          single-node
members       1 / 1 reachable
raft term     2
commit index  17

# Cluster table.
$ dblock status --api http://127.0.0.1:8080
dblock cluster — term 4

NODE          ROLE        SYNC        API                   COMMIT
dblock-1      leader      in_sync     192.168.1.10:8080     1247
dblock-2      follower    in_sync     192.168.1.11:8080     1247
dblock-3      follower    in_sync     192.168.1.12:8080     1247

# Join token in copy-paste-friendly box.
$ dblock token create
Cluster join token
Paste this `bootstrap:` block into the joining node's /etc/dblock/config.yaml,
then `systemctl restart dblock`.

╭──────────────────────────────────────────────────────────────────────────────╮
│  bootstrap:                                                                  │
│    leader_address: http://192.168.1.10:8080                                  │
│    token:          a12ca440858e7449e7f5933d6493fb40091377c48c2ec8c7ab60c9312added7e │
╰──────────────────────────────────────────────────────────────────────────────╯

Token expires at: 2026-06-08T13:01:14Z. Single-use.

# URL tester — runs in-process, no daemon, no auth.
$ dblock blocklist test https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
✓ https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
  format    auto (auto-detected)
  domains   162,481
  elapsed   2.3s

# Live dashboard.
$ dblock top
[fullscreen TUI with cluster strip + nodes + DNS bars + top-blocked]
```

## Next

M5.9.2 — `make dev` for SPA hot-reload.
