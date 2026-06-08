Feature: Firewall rule generator (iptables / nftables / MikroTik / OpnSense / UniFi)
  As an operator who runs skoed as the household resolver
  I want skoed to emit ready-to-paste firewall rules that DROP outbound
  traffic to known public DoH/DoT resolvers
  So I can close the "client just hard-codes 1.1.1.1" bypass without
  hand-curating IP lists for five different firewall syntaxes.

  Background:
    Given skoed has a curated DoH/DoT resolver snapshot containing at least
      | resolver   | ipv4         | ipv6                  |
      | cloudflare | 1.1.1.1      | 2606:4700:4700::1111  |
      | google     | 8.8.8.8      | 2001:4860:4860::8888  |
      | quad9      | 9.9.9.9      | 2620:fe::fe           |
    And the operator is authenticated as admin

  @fsid:FS-FwRuleIptablesSubnetScope
  Scenario: iptables output for a subnet scope is paste-ready
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=subnet&subnet=10.0.0.0/24
    Then the response is 200
    And the Content-Type is "text/plain; charset=utf-8"
    And the body contains a line for each curated resolver IPv4
      | line fragment                                                              |
      | -A FORWARD -s 10.0.0.0/24 -d 1.1.1.1 -j DROP                               |
      | -A FORWARD -s 10.0.0.0/24 -d 8.8.8.8 -j DROP                               |
      | -A FORWARD -s 10.0.0.0/24 -d 9.9.9.9 -j DROP                               |
    And each rule is preceded by a comment naming the resolver
    And the header carries the snapshot fetched_at timestamp

  @fsid:FS-FwRuleNftablesSubnetScope
  Scenario: nftables output uses an inline set and the correct family
    When the admin GETs /api/v1/firewall-rules?platform=nftables&scope=subnet&subnet=10.0.0.0/24
    Then the response is 200
    And the body declares a "table inet skoed_doh_gap"
    And the body contains a chain rule of the form
      "ip saddr 10.0.0.0/24 ip daddr { 1.1.1.1, 8.8.8.8, 9.9.9.9 } drop"
    And the body contains a matching rule for ip6 daddr covering the IPv6 resolvers

  @fsid:FS-FwRuleMikrotikSubnetScope
  Scenario: MikroTik output uses /ip firewall filter add syntax
    When the admin GETs /api/v1/firewall-rules?platform=mikrotik&scope=subnet&subnet=10.0.0.0/24
    Then the response is 200
    And the body contains lines starting with "/ip firewall filter add"
    And each rule has chain=forward, action=drop, src-address=10.0.0.0/24
    And each rule's dst-address matches one curated resolver IPv4
    And a comment="skoed doh-gap: <resolver-name>" is set on every rule

  @fsid:FS-FwRuleOpnsenseSubnetScope
  Scenario: OpnSense output is an importable alias + rule pair
    When the admin GETs /api/v1/firewall-rules?platform=opnsense&scope=subnet&subnet=10.0.0.0/24
    Then the response is 200
    And the body declares an alias named "skoed_doh_resolvers" containing every curated IPv4 and IPv6
    And the body declares a block rule from source 10.0.0.0/24 to that alias
    And the body's header documents how to paste the alias into the OpnSense UI

  @fsid:FS-FwRuleUnifiSubnetScope
  Scenario: UniFi output is a JSON snippet for the gateway firewall API
    When the admin GETs /api/v1/firewall-rules?platform=unifi&scope=subnet&subnet=10.0.0.0/24
    Then the response is 200
    And the Content-Type is "text/plain; charset=utf-8"
    And the body is a valid JSON object describing a UniFi firewall ruleset
    And the JSON contains an action "drop" and a source group covering 10.0.0.0/24
    And the JSON contains a destination address group enumerating every curated resolver IP

  @fsid:FS-FwRuleProfileScope
  Scenario: scope=profile expands to every client IP bound to that profile
    Given a Kids profile bound to client_ips ["10.42.10.50", "10.42.10.51"]
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=profile&profile=kids
    Then the response is 200
    And the body contains rules whose -s is 10.42.10.50
    And the body contains rules whose -s is 10.42.10.51
    And the body contains NO rule whose -s is any other client IP

  @fsid:FS-FwRuleAllScope
  Scenario: scope=all emits rules without a source restriction
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=all
    Then the response is 200
    And the body contains rules with no -s constraint (apply to every source)
    And the body still enumerates every curated resolver IP as -d

  @fsid:FS-FwRuleRejectActionOptIn
  Scenario: action=reject swaps DROP for REJECT (or platform equivalent)
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=subnet&subnet=10.0.0.0/24&action=reject
    Then the response is 200
    And every emitted rule ends with "-j REJECT --reject-with icmp-admin-prohibited"
    And no rule ends with "-j DROP"

  @fsid:FS-FwRuleRejectsUnknownPlatform
  Scenario: unknown platform values are refused
    When the admin GETs /api/v1/firewall-rules?platform=pfsense&scope=all
    Then the response is 400
    And the body's error mentions "unsupported platform"
    And the body's error lists the supported platforms

  @fsid:FS-FwRuleRejectsInvalidSubnet
  Scenario: subnet scope requires a valid CIDR
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=subnet&subnet=not-a-cidr
    Then the response is 400
    And the body's error mentions "invalid subnet"

  @fsid:FS-FwRuleRejectsUnknownProfile
  Scenario: profile scope requires an existing profile
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=profile&profile=does-not-exist
    Then the response is 404
    And the body's error mentions "profile"

  @fsid:FS-FwRuleRequiresAuth
  Scenario: the endpoint refuses unauthenticated callers
    Given no Authorization header
    When a GET /api/v1/firewall-rules?platform=iptables&scope=all fires
    Then the response is 401

  @fsid:FS-FwRuleHeaderCarriesSnapshotProvenance
  Scenario: the output header documents which resolver snapshot was used
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=all
    Then the response is 200
    And the body's leading comment block carries:
      | field             | example                                |
      | snapshot_id       | non-empty                              |
      | snapshot_fetched  | RFC3339 timestamp                      |
      | resolver_count    | matches the curated snapshot size      |
      | generated_at      | RFC3339 timestamp of this request      |
      | scope             | "all" / "subnet=..." / "profile=..."   |

  @fsid:FS-FwRuleStaleSnapshotStillServes
  Scenario: a stale resolver snapshot still serves but is flagged
    Given the curated resolver snapshot is older than 7 days
    When the admin GETs /api/v1/firewall-rules?platform=iptables&scope=all
    Then the response is 200
    And the body's leading comment block contains "WARNING: snapshot is stale"

  @fsid:FS-FwRuleMetricsCounter
  Scenario: Prometheus counter bumps per platform
    When the admin GETs /api/v1/firewall-rules?platform=nftables&scope=all
    And the admin GETs /api/v1/firewall-rules?platform=iptables&scope=all
    Then /metrics shows
      skoed_firewall_rules_generated_total{platform="nftables"} >= 1
    And /metrics shows
      skoed_firewall_rules_generated_total{platform="iptables"} >= 1

  Non-goals:
    - Actually applying rules to any firewall (skoed never touches netfilter, nft,
      the MikroTik API, the OpnSense API, or the UniFi controller — output is
      text only, operator pastes it themselves).
    - DoH-over-HTTPS deep-packet inspection or SNI-based blocking (this milestone
      is IP-list based only; encrypted-ClientHello bypasses are out of scope).
    - Discovering "new" DoH resolvers the curated database does not know about.
    - Per-rule stateful conntrack tuning, NAT rules, or interface selection —
      the generator emits a portable forward-chain DROP/REJECT only.
    - Bulk multi-subnet generation in one call (one scope per request; loop
      in the caller for many).
    - Rule rollback / audit-trail integration with the operator's firewall.
