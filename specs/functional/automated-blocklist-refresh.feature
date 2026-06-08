Feature: Automated Blocklist Refresh
  As an operator who configures URL-source blocklists
  I want them to refresh on a schedule without manual `POST /refresh`
  And a clear signal in the UI + metrics when a refresh starts failing
  So a stale blocklist doesn't quietly let through the things the
  operator thought they were blocking.

  Background:
    Given a 3-node skoed cluster
    And each blocklist has an optional refresh_interval_seconds field (default cluster-wide)

  @fsid:FS-AutoRefreshLeaderOnly
  Scenario: Only the leader runs the refresh worker
    Given a URL-sourced blocklist with refresh_interval_seconds = 5
    When the cluster runs for 12 seconds
    Then the leader's audit log shows at least 1 "blocklist.refresh" action
    And neither follower's audit log shows a "blocklist.refresh" action
    (refresh is leader-only because the result is Raft-replicated)

  @fsid:FS-AutoRefreshUpdatesAllNodes
  Scenario: A successful refresh replicates to every node
    Given a URL-sourced blocklist with refresh_interval_seconds = 5
    And the URL serves a domain list that triples in size between polls
    When the leader's refresh worker fires
    Then every node's GET /api/v1/blocklists shows the new domain_count
    And every node's last_refresh_at / last_refresh_status fields are identical

  @fsid:FS-AutoRefreshStatusFields
  Scenario: Blocklist exposes last_refresh_at / status / error
    Given a blocklist that has been refreshed once successfully
    When the admin GETs /api/v1/blocklists/<id>
    Then the response carries:
      | field                | type    | shape                         |
      | last_refresh_at      | string  | RFC3339                       |
      | last_refresh_status  | string  | "ok" \| "error" \| "unchanged" |
      | last_refresh_error   | string  | "" when status == "ok"        |
      | refresh_interval_seconds | int | 0 = cluster default            |

  @fsid:FS-AutoRefreshFailureRecorded
  Scenario: A failed refresh records the error, prior contents survive
    Given a URL-sourced blocklist with a working URL that has 1000 domains
    And the URL then starts returning HTTP 500
    When the leader's next refresh fires
    Then the blocklist still serves the prior 1000 domains
    And GET /api/v1/blocklists/<id> shows last_refresh_status = "error"
    And the error message names the HTTP status

  @fsid:FS-AutoRefreshStaleAlert
  Scenario: Dashboard surfaces stale blocklists
    Given a blocklist with refresh_interval_seconds = 5
    And the blocklist hasn't refreshed in > 2× the interval
    When the admin opens the Dashboard
    Then a warning card lists the stale blocklist with its name and last_refresh_at

  @fsid:FS-AutoRefreshDisabledWhenZero
  Scenario: refresh_interval_seconds = 0 means "don't auto-refresh"
    Given a URL-sourced blocklist with refresh_interval_seconds = 0
    When the cluster runs for 30 seconds
    Then the blocklist's last_refresh_at is unchanged
    (manual POST /refresh still works)

  @fsid:FS-AutoRefreshMetrics
  Scenario: Prometheus exposes refresh health
    Given a refreshed blocklist
    When an HTTP GET hits /metrics
    Then the body contains:
      | series                                                |
      | skoed_blocklist_last_refresh_seconds{id="<id>"}      |
      | skoed_blocklist_refresh_failures_total{id="<id>"}    |

  Non-goals:
    - Per-rule deltas (UI shows count delta only)
    - Push-style refresh hooks
    - Multi-source merge (one URL per blocklist)
    - GPG signature verification (deferred to M5.4.1)
    - Backoff on consecutive failures (constant interval for v1; operators
      raise the interval if they want fewer retries)
