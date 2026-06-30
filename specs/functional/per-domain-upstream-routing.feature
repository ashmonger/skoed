Feature: Per-Domain Upstream Routing
  As an operator running skoed on a network that has both a private DNS resolver
  and public upstream resolvers,
  I want to route specific domain patterns to a dedicated resolver,
  so that internal names resolve correctly without running a separate split-horizon server.

  # Non-goals:
  # - Policy-based routing by client IP (use per-profile upstreams instead)
  # - Automatic failover within a route's resolver list (global fallback behaviour is unchanged)
  # - GUI drag-and-drop route reordering (API and text config only)
  # - DoT/DoH/DoQ connection pooling for route resolvers (deferred post-M32)
  # - Wildcard routes applying to the root zone (match: "*" is rejected)

  Background:
    Given a running skoed node in forwarding mode with admin credentials

  # ─── Route configuration ─────────────────────────────────────────────────────

  @fsid:FS-UpstreamRouteCreate
  Scenario: Operator adds a wildcard route for an internal domain
    When the admin PATCHes /api/v1/settings with dns.upstream_routes containing
      | match             | resolvers       |
      | *.corp.internal   | 10.1.0.1:53     |
    Then GET /api/v1/settings returns dns.upstream_routes with that entry

  @fsid:FS-UpstreamRouteExactMatch
  Scenario: Operator adds an exact-domain route
    When the admin PATCHes /api/v1/settings with dns.upstream_routes containing
      | match             | resolvers       |
      | corp.internal     | 10.1.0.1:53     |
    Then GET /api/v1/settings returns dns.upstream_routes with match: "corp.internal"

  @fsid:FS-UpstreamRouteCIDRMatch
  Scenario: Operator adds a CIDR route for reverse-DNS PTR queries
    When the admin PATCHes /api/v1/settings with dns.upstream_routes containing
      | match             | resolvers       |
      | 10.in-addr.arpa   | 10.1.0.1:53     |
    Then GET /api/v1/settings returns dns.upstream_routes with match: "10.in-addr.arpa"

  @fsid:FS-UpstreamRouteReplace
  Scenario: PATCHing upstream_routes replaces the entire list
    Given dns.upstream_routes has one existing entry
    When the admin PATCHes /api/v1/settings with dns.upstream_routes containing two entries
    Then GET /api/v1/settings returns exactly two upstream_routes entries

  @fsid:FS-UpstreamRouteClear
  Scenario: Operator clears all upstream routes by sending an empty list
    Given dns.upstream_routes has one existing entry
    When the admin PATCHes /api/v1/settings with dns.upstream_routes: []
    Then GET /api/v1/settings returns dns.upstream_routes as an empty list

  # ─── DNS resolution with routes ──────────────────────────────────────────────

  @fsid:FS-UpstreamRouteWildcardResolution
  Scenario: A query matching a wildcard route is sent to the route's resolver
    Given dns.upstream_routes has match: "*.corp.internal" resolvers: ["<fake-corp-resolver>"]
    And the fake corp resolver responds to "host.corp.internal" with 192.168.1.10
    When a client queries for "host.corp.internal" A record
    Then skoed returns 192.168.1.10
    And the query was forwarded to the fake corp resolver, not the global upstream

  @fsid:FS-UpstreamRouteExactResolution
  Scenario: An exact-match route routes only that domain, not subdomains
    Given dns.upstream_routes has match: "corp.internal" resolvers: ["<fake-corp-resolver>"]
    And the fake corp resolver responds to "corp.internal" with 10.1.0.2
    When a client queries for "corp.internal" A record
    Then skoed returns 10.1.0.2
    When a client queries for "host.corp.internal" A record
    Then the query is forwarded to the global upstream, not the fake corp resolver

  @fsid:FS-UpstreamRouteNoMatchFallsThrough
  Scenario: A query that matches no route uses the global upstream list
    Given dns.upstream_routes has match: "*.corp.internal" resolvers: ["10.1.0.1:53"]
    And the global upstream responds to "example.com" with 93.184.216.34
    When a client queries for "example.com" A record
    Then skoed returns 93.184.216.34
    And the query was forwarded to the global upstream

  @fsid:FS-UpstreamRouteTopDownPriority
  Scenario: Routes are evaluated top-down; the first match wins
    Given dns.upstream_routes has two entries in order:
      | match             | resolvers           |
      | *.internal        | 10.1.0.1:53         |
      | *.corp.internal   | 10.2.0.1:53         |
    When a client queries for "host.corp.internal" A record
    Then the query is forwarded to 10.1.0.1:53 (first matching route)
    And 10.2.0.1:53 is not consulted

  @fsid:FS-UpstreamRouteWildcardDepth
  Scenario: A wildcard route matches at any subdomain depth
    Given dns.upstream_routes has match: "*.corp.internal" resolvers: ["<fake-corp-resolver>"]
    When a client queries for "a.b.c.corp.internal" A record
    Then the query is forwarded to the fake corp resolver

  # ─── Cluster replication ─────────────────────────────────────────────────────

  @fsid:FS-UpstreamRouteClusterReplicated
  Scenario: Upstream routes are replicated to all cluster nodes via Raft
    Given a 3-node cluster
    When the admin PATCHes /api/v1/settings on the leader with an upstream_routes entry
    Then GET /api/v1/settings on each follower returns the same upstream_routes entry

  # ─── Input validation ────────────────────────────────────────────────────────

  @fsid:FS-UpstreamRouteInvalidMatchRejected
  Scenario: An invalid match pattern is rejected with 400
    When the admin PATCHes /api/v1/settings with dns.upstream_routes match: "*"
    Then the response status is 400
    And the error message explains that bare wildcard routes are not allowed

  @fsid:FS-UpstreamRouteEmptyResolversRejected
  Scenario: A route with an empty resolver list is rejected with 400
    When the admin PATCHes /api/v1/settings with dns.upstream_routes match: "*.corp.internal" resolvers: []
    Then the response status is 400

  # ─── Upstream discovery ──────────────────────────────────────────────────────

  @fsid:FS-UpstreamDiscovery
  Scenario: The discover-upstreams endpoint returns the system resolver as a suggestion
    Given the system has a nameserver configured in /etc/resolv.conf
    When the admin POSTs to /api/v1/settings/discover-upstreams
    Then the response contains a "suggested_resolvers" list with at least one entry
    And the entries are not automatically applied to the running config
