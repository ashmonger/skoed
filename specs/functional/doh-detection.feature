Feature: DoH/DoT Detection and Layer-2 Blocking
  As an administrator
  I want clients that try to bypass skoed by switching to public DoH/DoT
  resolvers to be detected and (where the hostname approach can stop them)
  blocked at the DNS layer.

  Background:
    Given a freshly bootstrapped skoed cluster
    And the default `doh` category is enabled on the default profile

  @fsid:FS-DohDetectionResolverBlocklist
  Scenario: Default DoH-resolver hostnames are NXDOMAIN-blocked out of the box
    When any client queries "cloudflare-dns.com", "dns.google", "dns.quad9.net",
        "dns.adguard.com", "dns.nextdns.io", "mozilla.cloudflare-dns.com",
        "doh.opendns.com", "dns.controld.com", "chrome.cloudflare-dns.com"
    Then every one of those queries returns NXDOMAIN
    And every entry appears in the query log with category="doh-probe"

  @fsid:FS-DohDetectionFirefoxCanary
  Scenario: Firefox's "use-application-dns.net" canary always returns NXDOMAIN
    Given a Firefox client that probes "use-application-dns.net" before
      enabling DoH-by-default
    When the canary query reaches skoed
    Then skoed returns NXDOMAIN regardless of any profile/allowlist setting
    And the log entry's category is "doh-canary"
    And the entry is NEVER allowlist-overridable (operators cannot accidentally
      undo Firefox's auto-disable safety net)

  @fsid:FS-DohDetectionDdrProbe
  Scenario: RFC 9462 DDR probes are logged and never answered usefully
    Given a client issues a DDR SVCB query for "_dns.resolver.arpa"
    When the query reaches skoed
    Then the response is NODATA (no SVCB record)
    And the entry appears in the query log with category="ddr-probe"

  @fsid:FS-DohDetectionTaggedInQueryLog
  Scenario: Every query log entry carries a category field
    Given a default profile and one regular blocklist
    When a mix of normal, blocked, doh-probe, and ddr-probe queries are issued
    Then `/api/v1/query-log` returns each entry with a `category` field
    And valid values are: "", "doh-probe", "doh-canary", "ddr-probe"
    And the `/api/v1/cluster/stats` aggregate breaks down counts per category

  @fsid:FS-DohDetectionPerClientUiSurfacing
  Scenario: An admin sees per-client DoH attempts in the UI dashboard
    Given client 192.168.1.50 has made multiple doh-probe queries today
    When the admin opens the Stats view in the Web UI
    Then a "DoH attempts today" section lists each client and their probe count
    And a per-client link drills down to the matching query-log entries

  @fsid:FS-DohDetectionCategoryDisableable
  Scenario: An operator can disable the default DoH category for a profile
    Given the admin wants their `adults` profile to NOT block DoH hostnames
    When the admin POSTs /api/v1/categories/doh/disable {profile_id: "adults"}
    Then queries for known DoH hostnames from an adults-profile client are
      forwarded (not blocked) AND still logged with category="doh-probe"
    But Firefox canary remains NXDOMAIN regardless of profile (safety override)

  Non-goals:
    - Blocking DoH clients pinned to hardcoded IPs (firewall, M3.5)
    - SNI / TLS-ClientHello inspection (firewall, M3.5)
    - Serving DoH/DoT ourselves (M4)
