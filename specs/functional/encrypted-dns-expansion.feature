Feature: Encrypted DNS Expansion — DoH3 and DNSCrypt v2
  As a device administrator who wants full encrypted-DNS coverage
  I want skoed to serve DNS over HTTP/3 (DoH3) and DNSCrypt v2
  So that clients that prefer those transports still get my filter applied

  Background:
    Given a skoed node with M4 DoH/DoT already working
    And the same filter, allowlist, local-DNS, and query-log pipeline applies to all transports

  # ─── DoH3 (HTTP/3 over QUIC) ────────────────────────────────────────────────

  @fsid:FS-Doh3ServerListens
  Scenario: DoH3 listens on the configured UDP port
    Given the config sets node.dns.listen.doh3_port to a free UDP port
    When the node starts
    Then the node accepts QUIC connections on that port with HTTP/3
    And POST /dns-query with Content-Type "application/dns-message" returns the binary RFC 8484 response over HTTP/3
    And GET /dns-query?dns=<base64url-encoded-query> returns the same response over HTTP/3
    And the response Content-Type is "application/dns-message"

  @fsid:FS-Doh3UsesTlsOneTwoThree
  Scenario: DoH3 requires TLS 1.3 via QUIC
    Given node.dns.listen.doh3_port is set
    When a client attempts to connect without TLS or with TLS < 1.3
    Then the QUIC handshake is rejected
    And the node reuses the same TLS certificate already serving DoH/DoT

  @fsid:FS-Doh3AppliesFilter
  Scenario: DoH3 applies the configured filter
    Given "ads.example.com" is on a blocklist with block_policy=nxdomain
    When a DoH3 client queries "ads.example.com"
    Then the response is NXDOMAIN
    And the query is recorded in the query log with transport "doh3" and outcome "blocked-doh3"

  @fsid:FS-Doh3ServesLocalDns
  Scenario: DoH3 serves local DNS entries
    Given a local-DNS A record maps "lab.home" to 10.0.0.5
    When a DoH3 client queries "lab.home"
    Then the response answer is 10.0.0.5
    And the outcome is "local-doh3"

  @fsid:FS-Doh3ForwardsUnmatched
  Scenario: DoH3 forwards queries that match nothing
    Given "example.com" is not in any blocklist, allowlist, or local DNS
    When a DoH3 client queries "example.com"
    Then the upstream answer is returned
    And the outcome is "forwarded-doh3"

  @fsid:FS-Doh3DisabledByDefault
  Scenario: DoH3 is disabled when no port is configured
    Given node.dns.listen.doh3_port is absent or zero
    When the node starts
    Then no UDP listener for DoH3 is bound
    And DNS over HTTP/1.1 and DoT continue to work normally

  @fsid:FS-Doh3IndependentEnable
  Scenario: DoH3 can be toggled without restarting DoH or DoT
    Given the node is running with all three transports enabled
    When an operator sets node.dns.listen.doh3_port to 0 and the node restarts
    Then DoH3 connections are refused
    And DoH and DoT continue to accept queries

  # ─── DNSCrypt v2 ─────────────────────────────────────────────────────────────

  @fsid:FS-DnscryptServerListens
  Scenario: DNSCrypt v2 server listens on the configured port
    Given the config sets node.dns.listen.dnscrypt_port to a free UDP port
    When the node starts
    Then the node accepts DNSCrypt v2 queries on that port
    And a DNSCrypt-aware client can resolve a domain through it

  @fsid:FS-DnscryptAppliesFilter
  Scenario: DNSCrypt applies the configured filter
    Given "ads.example.com" is on a blocklist with block_policy=nxdomain
    When a DNSCrypt client queries "ads.example.com"
    Then the response is NXDOMAIN
    And the query is recorded with transport "dnscrypt" and outcome "blocked-dnscrypt"

  @fsid:FS-DnscryptServesLocalDns
  Scenario: DNSCrypt serves local DNS entries
    Given a local-DNS A record maps "nas.home" to 10.0.0.10
    When a DNSCrypt client queries "nas.home"
    Then the response answer is 10.0.0.10
    And the outcome is "local-dnscrypt"

  @fsid:FS-DnscryptForwardsUnmatched
  Scenario: DNSCrypt forwards queries that match nothing
    Given "example.com" is not in any blocklist, allowlist, or local DNS
    When a DNSCrypt client queries "example.com"
    Then the upstream answer is returned
    And the outcome is "forwarded-dnscrypt"

  @fsid:FS-DnscryptStampPublished
  Scenario: Server publishes its sdns:// stamp via the API
    Given node.dns.listen.dnscrypt_port is set
    When an operator calls GET /api/v1/settings
    Then the response includes a dnscrypt_stamp field with a valid sdns:// URI
    And the stamp encodes the server's IP, port, and public key fingerprint

  @fsid:FS-DnscryptKeyRotation
  Scenario: DNSCrypt server certificate rotates on schedule
    Given node.dns.dnscrypt.cert_ttl_hours is set to 24 (default)
    When the certificate TTL expires
    Then a new server keypair is generated
    And new clients authenticate with the new keypair
    And in-flight queries are served through the transition without rejection

  @fsid:FS-DnscryptKeyReplicatedViaRaft
  Scenario: DNSCrypt keypair is replicated across the cluster
    Given a 3-node cluster with DNSCrypt enabled
    When the leader generates or rotates the DNSCrypt keypair
    Then all nodes converge on the same keypair within one Raft apply cycle
    And any node can serve DNSCrypt queries that clients resolve against the stamp

  @fsid:FS-DnscryptDisabledByDefault
  Scenario: DNSCrypt is disabled when no port is configured
    Given node.dns.listen.dnscrypt_port is absent or zero
    When the node starts
    Then no UDP listener for DNSCrypt is bound
    And all other DNS transports continue to work normally

  @fsid:FS-DnscryptIndependentEnable
  Scenario: DNSCrypt can be toggled without affecting other transports
    Given the node is running with both DNSCrypt and DoH enabled
    When node.dns.listen.dnscrypt_port is set to 0 and the node restarts
    Then DNSCrypt connections are refused
    And DoH, DoT, and plain DNS continue to accept queries

  # ─── Non-goals ───────────────────────────────────────────────────────────────

  # Non-goals for M8:
  # - ODoH (Oblivious DoH) — niche protocol; deferred to post-M8
  # - Anonymized DNSCrypt relays — requires relay-node infrastructure
  # - WebTransport or DoQ over non-standard ports — DoH3 covers the QUIC use case
  # - Per-token / per-client transport restrictions
  # - HTTP/3 for the management API (only the DNS path runs over QUIC)
