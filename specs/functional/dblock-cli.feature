Feature: skoed CLI + TUI
  As an operator who lives in the terminal
  I want `skoed <verb>` subcommands for every common operation
  And a live TUI dashboard for "what's happening right now"
  So I stop reaching for `curl -u admin:pwd …` every time.

  Background:
    Given the skoed binary is on PATH
    And ~/.skoed/credentials may hold {api_url, username, password}

  @fsid:FS-CliVersionFlag
  Scenario: skoed --version prints version + commit + go-version
    When `skoed --version` runs
    Then exit code is 0
    And stdout matches: skoed <semver> (commit=<hex>, go=go1.<x>)

  @fsid:FS-CliHealth
  Scenario: skoed health reports the local node's health
    Given a running skoed node
    When `skoed health` runs
    Then stdout shows: node id, role, mode, members, reachable_members,
      raft_term, commit_index — color-coded (green ok, yellow degraded)
    And exit code is 0 when status=ok, 1 when degraded

  @fsid:FS-CliStatus
  Scenario: skoed status shows the cluster as a styled table
    Given a 3-node cluster
    When `skoed status` runs
    Then stdout is a lipgloss-rendered table:
      | NODE | ROLE | SYNC | COMMIT |
      | node-1 | leader   | in_sync | 17 |
      | node-2 | follower | in_sync | 17 |
      | node-3 | follower | in_sync | 17 |
    And the LEADER row is highlighted

  @fsid:FS-CliTokenCreate
  Scenario: skoed token create issues a join token
    Given the operator is authenticated (creds file or --auth)
    When `skoed token create` runs against the leader
    Then stdout shows the bootstrap snippet (leader_address, token, expires_at)
      in a copy-paste-friendly box
    And exit code is 0

  @fsid:FS-CliBlocklistTest
  Scenario: skoed blocklist test <url> fetches + parses without a daemon
    When `skoed blocklist test https://example.com/hosts.txt` runs
    Then stdout reports: count of domains, detected format, parse warnings
    And exit code is 0 on success, 1 on HTTP/parse failure
    (No auth, no daemon, no SSRF risk — runs in operator's process)

  @fsid:FS-CliCredentialsFile
  Scenario: Credentials are read from ~/.skoed/credentials
    Given ~/.skoed/credentials contains api_url + username + password
    When any authenticated subcommand runs
    Then it uses those credentials without prompting
    And the file's mode is enforced to 0600 (refuses to read world-readable creds)

  @fsid:FS-TuiTopShowsLiveDashboard
  Scenario: skoed top renders a live TUI dashboard
    Given a running skoed cluster
    When `skoed top` runs
    Then a bubbletea TUI renders:
      | cluster status (leader/term/commit) |
      | DNS query rate (last 60s)            |
      | top blocked domains (top 10)         |
      | audit-log tail (last 5)              |
    And pressing `q` exits cleanly with code 0
    And pressing `r` forces an immediate refresh

  @fsid:FS-CliDaemonStillWorks
  Scenario: Existing skoed --config still works as the default
    Given an existing systemd unit running `skoed --config /etc/skoed/config.yaml`
    When the unit starts on the new binary
    Then the daemon starts normally (subcommand-less invocation defaults to `daemon`)

  Non-goals:
    - Full curses editor for config (operators edit YAML or use the Web UI)
    - Shell completion (M5.9.1.1 follow-up via `skoed completion`)
    - JSON output mode (operators wanting JSON use the API directly)
    - Windows / macOS support (Linux-only tool; same posture as the daemon)
