Feature: "Would this domain be blocked?" tester
  As an operator (or a curious household member)
  I want to ask skoed whether a given domain would be blocked
  And see WHY for the authenticated case
  So I can sanity-check the cluster from outside the DNS path itself.

  Background:
    Given skoed is configured with at least one blocklist that blocks "doubleclick.net"
    And the default profile inherits that blocklist

  @fsid:FS-TestDomainGuestVerdictBlocked
  Scenario: Guest endpoint reports BLOCKED for a known-blocked domain
    Given no Authorization header
    When a POST /api/v1/_public/test-domain with body {"domain":"doubleclick.net"} fires
    Then the response is 200
    And the body has shape {"would_block": true, "reason": "blocklist"}

  @fsid:FS-TestDomainGuestVerdictAllowed
  Scenario: Guest endpoint reports ALLOWED for a domain not on any blocklist
    Given no Authorization header
    When a POST /api/v1/_public/test-domain with body {"domain":"github.com"} fires
    Then the response is 200
    And the body has "would_block": false
    And the reason is "forwarded"

  @fsid:FS-TestDomainGuestRefusesInvalidInput
  Scenario: Guest endpoint refuses suspicious inputs
    Given no Authorization header
    When a POST /api/v1/_public/test-domain with body {"domain":"10.0.0.5"} fires
    Then the response is 400
    And the body's error mentions "literal IP"

  @fsid:FS-TestDomainGuestRateLimited
  Scenario: Guest endpoint rate-limits abusive callers
    Given no Authorization header
    When 30 POSTs to /api/v1/_public/test-domain fire in a tight loop from one source IP
    Then at least one response is 429

  @fsid:FS-TestDomainGuestDisabledWithPublicLanding
  Scenario: Guest endpoint is gated by node.api.public_landing.enabled
    Given node.api.public_landing.enabled = false
    When a POST /api/v1/_public/test-domain fires
    Then the response is 404

  @fsid:FS-TestDomainAuthRequiresAuth
  Scenario: Authenticated endpoint refuses unauthenticated requests
    Given no Authorization header
    When a POST /api/v1/test-domain fires
    Then the response is 401

  @fsid:FS-TestDomainAuthReturnsFullChain
  Scenario: Authenticated endpoint returns the full reasoning chain
    Given a Kids profile bound to client_ip 10.42.10.50 with the doubleclick-blocking blocklist
    When the admin POSTs /api/v1/test-domain with body
      {"domain":"doubleclick.net","client_ip":"10.42.10.50"}
    Then the response is 200
    And the body carries:
      | field                | value         |
      | would_block          | true          |
      | reason               | "blocklist"   |
      | matched_profile_id   | "kids"        |
      | matched_blocklist_id | non-empty     |
      | block_policy         | "nxdomain"    |

  @fsid:FS-TestDomainAuthLocalDnsTakesPriority
  Scenario: Local-DNS entry short-circuits the verdict
    Given a local DNS entry "nas.lab" → 10.42.10.20 exists
    When the admin POSTs /api/v1/test-domain with {"domain":"nas.lab"}
    Then would_block = false
    And reason = "local-dns"
    And local_dns_answer = "10.42.10.20"

  @fsid:FS-TestDomainAuthAllowlistOverridesBlocklist
  Scenario: An allowlist entry beats a blocklist hit
    Given "doubleclick.net" is also on the allowlist
    When the admin POSTs /api/v1/test-domain with {"domain":"doubleclick.net","client_ip":"10.42.10.50"}
    Then would_block = false
    And reason = "allowlist"

  @fsid:FS-TestDomainAuthFiresOnSameEvaluatorAsRealQueries
  Scenario: The verdict is identical to what a real DNS query would get
    Given the cluster has any blocklist + profile + schedule configuration
    When the admin POSTs /api/v1/test-domain for domain D and client_ip C
    And a real DNS query for D from source IP C arrives at the same instant
    Then the test endpoint's verdict matches the real query's outcome
    (Single source of truth: same filter.Engine.EvaluateForClientID call)

  @fsid:FS-TestDomainCliVerb
  Scenario: skoed domain test <domain> works from the CLI
    Given valid credentials
    When `skoed domain test doubleclick.net --client 10.42.10.50` runs
    Then exit code is 0
    And stdout includes "blocked" and the matched blocklist id

  @fsid:FS-TestDomainMetricsCounter
  Scenario: Prometheus counter bumps per verdict + surface
    When a guest POSTs /api/v1/_public/test-domain that returns blocked
    Then /metrics shows
      skoed_test_domain_requests_total{surface="guest",verdict="block"} >= 1
    And a corresponding "allow" verdict increments
      skoed_test_domain_requests_total{surface="guest",verdict="allow"}

  Non-goals:
    - dig-style DNS-RR composition (verdict + rationale only, no answer RRs)
    - Per-rule diff explainer ("matched line 8 of hagezi-pro source")
    - Recursive what-if ("what if I added this to the allowlist?")
    - Bulk-mode (one domain per call; loop in the caller for many)
