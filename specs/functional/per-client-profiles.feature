Feature: Per-Client Profiles
  As a household administrator
  I want to bind blocklists and allowlists to specific client devices
  So that a child's tablet has stricter filtering than the family TV.

  Background:
    Given a running skoed cluster with two blocklists ("ads" and "social")
    And a default profile that uses only the "ads" blocklist
    And a "kids" profile that uses both "ads" and "social"

  @fsid:FS-ProfileAssignByIp
  Scenario: A device's traffic is matched to its profile by IP
    Given the "kids" profile is assigned to client IP 192.168.1.50
    When 192.168.1.50 queries "facebook.com" via the local DNS port
    Then the response is NXDOMAIN
    And the query log records the query with profile="kids" and blocklist_id="social"

  @fsid:FS-ProfileAssignByCidr
  Scenario: A CIDR range maps multiple clients to one profile
    Given the "kids" profile is assigned to 192.168.10.0/24
    When 192.168.10.7 queries "facebook.com"
    Then the response is NXDOMAIN
    When 192.168.20.5 queries "facebook.com"
    Then the response is a forwarded answer (not in the kids subnet → default profile, social not blocked)

  @fsid:FS-ProfileDefaultFallback
  Scenario: An unassigned client uses the default profile
    Given client IP 192.168.99.99 has no explicit profile assignment
    When it queries "tracker.example.com"
    Then the response is NXDOMAIN (default profile blocks ads)
    When it queries "facebook.com"
    Then the response is forwarded (default profile does not block social)

  @fsid:FS-ProfilePerClientAllowlist
  Scenario: A profile's allowlist overrides its blocklist matches
    Given the "kids" profile allowlists "youtube.com"
    And "youtube.com" is in the "social" blocklist
    When a kids-profile client queries "youtube.com"
    Then the response is forwarded (allowlist wins)

  @fsid:FS-ProfileApiCrud
  Scenario: Admin manages profiles via the management API
    When the admin POSTs a new profile {id, name, blocklists, allowlists, client_ips, client_cidrs}
    Then the profile is created cluster-wide via Raft
    When the admin PATCHes the profile (e.g. adds a blocklist)
    Then the change replicates within 5 seconds
    When the admin DELETEs the profile
    Then the profile is removed; clients previously assigned fall back to default

  @fsid:FS-ProfileSharedClientGroups
  Scenario: A single client matches multiple profiles → union of rules
    Given client IP 192.168.1.50 is in both "kids" (blocks social) and "work" (blocks gaming) profiles
    When the client queries "facebook.com"
    Then the response is NXDOMAIN (blocked by kids)
    When the client queries "steam.com"
    Then the response is NXDOMAIN (blocked by work)

  Non-goals:
    - MAC-address client identification (requires DHCP integration → M3.5 with the firewall recipes)
    - Per-application or per-port profile scoping (out of scope; DNS is layer-7-agnostic)
    - Auto-discovery of devices on the LAN
