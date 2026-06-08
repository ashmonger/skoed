Feature: Curated DoH/DoT resolver IP database
  As an operator closing the DoH gap on my network
  I want skoed to maintain a fresh, cluster-wide list of public DoH/DoT resolver IPs
  So that downstream tools (firewall rule generator, dashboards) can rely on a single
  authoritative snapshot that every node agrees on.

  Background:
    Given skoed is running as a 3-node cluster
    And the curated resolver feed is reachable at the configured upstream URL

  @fsid:FS-DohResolverDbListSnapshotShape
  Scenario: GET /api/v1/doh-resolvers returns the current snapshot
    Given a snapshot has been fetched at least once
    When the admin GETs /api/v1/doh-resolvers
    Then the response is 200
    And the body has shape
      | field         | value                                     |
      | snapshot_id   | non-empty                                 |
      | fetched_at    | RFC3339 timestamp                         |
      | source_url    | non-empty                                 |
      | stale         | false                                     |
      | resolvers     | array of {id, name, ipv4[], ipv6[], source_url} |
    And the resolver list includes well-known providers
      | name        |
      | Cloudflare  |
      | Google      |
      | Quad9       |
      | NextDNS     |
      | AdGuard     |
      | Mullvad     |
      | Apple       |

  @fsid:FS-DohResolverDbSnapshotJsonExport
  Scenario: GET /api/v1/doh-resolvers/snapshot.json returns the raw export
    Given a snapshot has been fetched at least once
    When the admin GETs /api/v1/doh-resolvers/snapshot.json
    Then the response is 200
    And the Content-Type is "application/json"
    And the body is the raw snapshot document with snapshot_id and resolvers[]

  @fsid:FS-DohResolverDbAdminForceRefresh
  Scenario: POST /api/v1/doh-resolvers/refresh forces an immediate refresh
    Given a snapshot fetched at time T
    When the admin POSTs /api/v1/doh-resolvers/refresh with an empty body
    Then the response is 202
    And within 5 seconds GET /api/v1/doh-resolvers returns a snapshot with fetched_at > T

  @fsid:FS-DohResolverDbRefreshRequiresAuth
  Scenario: Force-refresh refuses unauthenticated callers
    Given no Authorization header
    When a POST /api/v1/doh-resolvers/refresh fires
    Then the response is 401

  @fsid:FS-DohResolverDbScheduledDailyRefresh
  Scenario: The leader refreshes the snapshot once per day automatically
    Given a freshly elected leader at time T0
    When 24 hours have elapsed without any manual refresh
    Then the snapshot's fetched_at advances past T0 without operator action
    And the new snapshot is visible on every follower via GET /api/v1/doh-resolvers

  @fsid:FS-DohResolverDbLeaderOnlyScheduler
  Scenario: Only the leader runs the refresh job
    Given a 3-node cluster
    When the scheduled refresh tick fires on all nodes simultaneously
    Then exactly one HTTP request to the upstream feed is observed
    And it originates from the current leader

  @fsid:FS-DohResolverDbReplicatedAcrossNodes
  Scenario: A refreshed snapshot is identical on every node
    Given the leader has just persisted a new snapshot with snapshot_id S
    When the admin GETs /api/v1/doh-resolvers from each follower
    Then every node returns snapshot_id = S
    And the resolvers[] arrays are byte-for-byte equal

  @fsid:FS-DohResolverDbStaleFlagAfterSevenDays
  Scenario: Snapshots older than 7 days are served with stale=true
    Given the last successful fetch was 8 days ago
    When the admin GETs /api/v1/doh-resolvers
    Then the response is 200
    And the body still contains the resolvers[] from that snapshot
    And the body's "stale" field is true

  @fsid:FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot
  Scenario: Upstream fetch failures do not wipe the existing snapshot
    Given a healthy snapshot S exists
    And the upstream feed starts returning HTTP 500 on every request
    When the scheduled refresh runs and fails
    Then GET /api/v1/doh-resolvers still returns snapshot S
    And no resolvers[] entries are dropped

  @fsid:FS-DohResolverDbRefreshRetriesWithBackoff
  Scenario: Transient upstream errors trigger backoff retries, not silent failure
    Given the upstream feed returns HTTP 503 on the first 2 attempts and 200 on the third
    When the leader runs a refresh cycle
    Then the snapshot is eventually updated within the retry window
    And the operator-visible "last_refresh_error" field is cleared on success

  @fsid:FS-DohResolverDbReadEndpointPublicOrAuthenticated
  Scenario: Read endpoints are reachable without admin credentials
    Given no Authorization header
    When a GET /api/v1/doh-resolvers fires
    Then the response is 200
    And the body has shape {snapshot_id, fetched_at, stale, resolvers[]}

  @fsid:FS-DohResolverDbResolverEntryShape
  Scenario: Each resolver entry carries IPv4, IPv6, and provenance fields
    When the admin GETs /api/v1/doh-resolvers
    Then every entry in resolvers[] has
      | field      | constraint                                   |
      | id         | stable slug (e.g. "cloudflare", "quad9")     |
      | name       | human-readable provider name                 |
      | ipv4       | array of valid IPv4 strings (may be empty)   |
      | ipv6       | array of valid IPv6 strings (may be empty)   |
      | source_url | URL of the upstream record for this provider |
    And no entry has both ipv4 and ipv6 empty

  @fsid:FS-DohResolverDbMetricsCounters
  Scenario: Prometheus counters expose refresh outcomes
    When the leader completes one successful refresh and one failed refresh
    Then /metrics shows
      skoed_doh_resolver_refresh_total{outcome="success"} >= 1
    And /metrics shows
      skoed_doh_resolver_refresh_total{outcome="failure"} >= 1
    And /metrics exposes
      skoed_doh_resolver_count gauge with the size of the current snapshot

  Non-goals:
    - Discovering DoH/DoT resolvers automatically by probing the public internet
      (the list is curated, refreshed from one tracked upstream URL)
    - Per-tenant or per-profile resolver lists (the snapshot is cluster-global)
    - Blocking DoH/DoT traffic in skoed itself (this database only feeds the
      firewall-rule generator; skoed never touches packet filters)
    - Resolver health-checking or latency measurements (out of scope; this is
      an IP inventory, not a monitoring system)
    - Historical snapshot retention or diffing across versions (only the current
      snapshot is queryable; prior versions are overwritten on refresh)
    - User-editable resolver entries via the API (operators who need custom
      entries should fork the upstream feed; runtime mutation would defeat the
      "single source of truth" property)
