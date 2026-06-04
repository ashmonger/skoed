Feature: Per-client DoH/DoT status surfacing
  As a household administrator with M3 DoH detection in place
  I want to see, per client, whether DoH/DoT use is suspected
  So that I know which devices need attention (firewall rule, talk to the user)

  Background:
    Given dblock has the M3 DoH category enabled on the default profile
    And the query log captures every blocked DoH probe with blocklist_id="cat:doh"

  @fsid:FS-ClientDohStatusEndpointShape
  Scenario: The endpoint returns the documented shape
    Given the client "192.168.1.42" has 3 blocked DoH probes in the last hour
    And the most recent probe domain was "dns.google" at time T
    When the admin calls GET /api/v1/clients/192.168.1.42/doh-status
    Then the response is 200 with a JSON body containing:
      | field              | type         |
      | client             | string       |
      | using_doh          | boolean      |
      | doh_probes_1h      | integer      |
      | last_doh_query     | string|null  |
      | suspected_provider | string|null  |
    And using_doh is true when doh_probes_1h > 0
    And suspected_provider is derived from the last probe's domain (e.g. "google" from "dns.google")
    And last_doh_query is the ISO-8601 timestamp of the most recent DoH probe

  @fsid:FS-ClientDohStatusNoProbes
  Scenario: A client with no DoH probes returns clean status
    Given the client "192.168.1.99" has no blocked DoH probes in the last hour
    When the admin calls GET /api/v1/clients/192.168.1.99/doh-status
    Then the response is 200
    And using_doh is false
    And doh_probes_1h is 0
    And last_doh_query is null
    And suspected_provider is null

  @fsid:FS-ClientDohStatusUnauthenticated
  Scenario: The endpoint requires authentication
    Given no Authorization header is provided
    When a request is made to GET /api/v1/clients/192.168.1.42/doh-status
    Then the response is 401

  @fsid:FS-ClientDohStatusInvalidIp
  Scenario: A malformed client IP returns a useful error
    When the admin calls GET /api/v1/clients/not-an-ip/doh-status
    Then the response is 400
    And the body explains "invalid client IP"

  @fsid:FS-ClientDohStatusRollingWindow
  Scenario: The 1-hour window is rolling, not calendar-bucketed
    Given the client "192.168.1.42" had 5 probes 90 minutes ago
    And had 2 probes 30 minutes ago
    When the admin calls GET /api/v1/clients/192.168.1.42/doh-status
    Then doh_probes_1h is 2
    And probes older than 60 minutes are not counted

  @fsid:FS-ClientDohStatusSuspectedProvider
  Scenario Outline: Provider inference from probe domain
    Given the most recent DoH probe domain for "192.168.1.42" is <domain>
    When the admin calls GET /api/v1/clients/192.168.1.42/doh-status
    Then suspected_provider is <provider>

    Examples:
      | domain                       | provider     |
      | "dns.google"                 | "google"     |
      | "cloudflare-dns.com"         | "cloudflare" |
      | "mozilla.cloudflare-dns.com" | "cloudflare" |
      | "dns.quad9.net"              | "quad9"      |
      | "dns.adguard.com"            | "adguard"    |
      | "doh.opendns.com"            | "opendns"    |
      | "unknown-resolver.example"   | null         |

  Non-goals:
    - Per-client *aggregate* over arbitrary windows (1h is enough for M3.5)
    - Real-time push (admin polls; UI auto-refresh every 60s is enough)
    - SNI inspection (belongs at the firewall, not in dblock)
    - Automated firewall rule generation (skipped per UoR for M3.5)
    - Resolver-IP database refresh (skipped — same track as firewall)
