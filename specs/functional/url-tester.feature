Feature: URL tester (CLI + public landing page)
  As an operator evaluating dblock for the first time
  I want to sanity-check a blocklist URL before committing to install
  And the dblock daemon to expose that same tester at / without auth
  So I can answer "does this list parse?" in 30 seconds — locally via CLI
  or remotely against a running dblock that someone else set up.

  Background:
    Given dblock can be reached on the public LAN interface
    And the operator's CLI binary is available at $PATH/dblock
    And the daemon's node config has `node.api.public_landing.enabled` set
      (default: true)

  @fsid:FS-UrlTesterCliSubcommand
  Scenario: `dblock blocklist test <url>` already works (M5.9.1)
    Given a hosts-format blocklist URL is reachable
    When the operator runs `dblock blocklist test https://example.com/hosts.txt`
    Then exit code is 0
    And stdout reports a domain count and the detected format
    And no daemon is required — the parse runs in-process
    And no auth is exchanged

  @fsid:FS-UrlTesterPublicLandingShown
  Scenario: GET / serves an unauthenticated URL-tester landing page
    Given the public landing page is enabled (default)
    When an unauthenticated browser opens GET /
    Then the response is the SPA index document (no redirect to /login)
    And the page contains a URL-tester form
    And the page contains a top-right "Login" affordance

  @fsid:FS-UrlTesterLoginButtonLeadsToAdminUi
  Scenario: The "Login" button on the landing leads to the admin login screen
    Given the operator is on the landing page at /
    When they click the "Login" affordance
    Then the browser navigates to /login
    And the existing admin login form renders

  @fsid:FS-UrlTesterPublicEndpointReturnsCountAndFormat
  Scenario: POST /api/v1/_public/test-blocklist parses a valid public URL
    Given the public landing page is enabled
    And a hosts-format blocklist with N domains is reachable on the public internet
    When an unauthenticated client POSTs {"url":"<url>", "format":"auto"}
      to /api/v1/_public/test-blocklist
    Then the HTTP response is 200
    And the body is {"ok":true, "count":N, "format":"…", "elapsed_ms":<duration>}

  @fsid:FS-UrlTesterRefusesPrivateAddress
  Scenario: The endpoint refuses URLs resolving to non-public addresses
    Given the public landing page is enabled
    When an unauthenticated client POSTs a URL whose host resolves to
      any of 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8,
      169.254.0.0/16, ::1, or fc00::/7
      to /api/v1/_public/test-blocklist
    Then the HTTP response is 403
    And the body contains an explanation referencing the refused address
    And no outbound fetch is attempted by the dblock daemon

  @fsid:FS-UrlTesterRateLimited
  Scenario: The endpoint rate-limits a single source IP
    Given the public landing page is enabled
    When the same source IP issues 10 POSTs to /api/v1/_public/test-blocklist
      within one second
    Then at least one response carries HTTP 429
    And the 429 body explains the limit
    And other source IPs are not affected

  @fsid:FS-UrlTesterOperatorCanDisable
  Scenario: Operator can disable the public landing entirely
    Given node.api.public_landing.enabled is set to false
    When an unauthenticated browser opens GET /
    Then the response is a redirect to /login (preserving the pre-M5.9.5 posture)
    And POST /api/v1/_public/test-blocklist returns HTTP 404

  Non-goals:
    - "Try it on dblock.io" hosted demo — dblock stays private-network for v1.
    - Authenticated tester from the public surface (admin already has the
      Create Blocklist modal — this exists solely for the unauth landing).
    - Sharing test results between browsers / persistence across requests.
    - Per-account rate-limit budgets — only per-source-IP, in-process.
