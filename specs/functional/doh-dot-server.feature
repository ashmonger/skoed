Feature: skoed as a DoH/DoT server
  As a device administrator who can configure encrypted DNS
  I want skoed to serve queries over DoH and DoT
  So that clients which insist on encrypted DNS still get my filter applied

  Background:
    Given a skoed node with the M1 DNS engine working over UDP/TCP
    And the same filter, allowlist, local-DNS, and query-log pipeline that plain DNS uses

  @fsid:FS-DohServerListens
  Scenario: DoH listens on /dns-query
    Given the config sets node.dns.listen.doh_port to a free TCP port
    When the node starts
    Then the node accepts HTTPS on that port
    And POST /dns-query with Content-Type "application/dns-message" returns the binary RFC 8484 response
    And GET /dns-query?dns=<base64url-encoded-query> returns the same response
    And the response Content-Type is "application/dns-message"

  @fsid:FS-DotServerListens
  Scenario: DoT listens on the configured port
    Given the config sets node.dns.listen.dot_port to a free TCP port (typically 853)
    When the node starts
    Then a TLS handshake on that port succeeds with the node's certificate
    And a wire-format DNS query inside the TLS session returns the same answer the UDP listener would

  @fsid:FS-DohAppliesFilter
  Scenario: DoH applies the configured filter
    Given the domain "ads.example.com" is on a blocklist with block_policy=nxdomain
    When a DoH client queries "ads.example.com"
    Then the response is NXDOMAIN (per block_policy)
    And the query is recorded in the query log with outcome "blocked-doh"

  @fsid:FS-DotAppliesFilter
  Scenario: DoT applies the configured filter
    Given the domain "ads.example.com" is on a blocklist with block_policy=nxdomain
    When a DoT client queries "ads.example.com"
    Then the response is NXDOMAIN
    And the query is recorded with outcome "blocked-dot"

  @fsid:FS-DohServesLocalDNS
  Scenario: DoH serves local DNS entries
    Given a local-DNS A record maps "lab.home" to 10.0.0.5
    When a DoH client queries "lab.home"
    Then the response answer is 10.0.0.5
    And the outcome is "local-doh"

  @fsid:FS-DohForwardsUnmatched
  Scenario: DoH forwards queries that match nothing
    Given the domain "example.com" is on no blocklist, no allowlist, no local entry
    When a DoH client queries "example.com"
    Then the upstream answer is returned over the DoH connection
    And the outcome is "forwarded-doh"

  @fsid:FS-DohSelfSignedCert
  Scenario: DoH uses a self-signed certificate by default
    Given no node.dns.tls.cert_file is configured
    When the node starts with doh_port enabled
    Then the node generates a self-signed certificate at first boot
    And reuses that same certificate on subsequent boots
    And the certificate's subject contains the node's hostname or node_id

  @fsid:FS-DohConfiguredCert
  Scenario: Operator-supplied cert+key from disk
    Given node.dns.tls.cert_file and node.dns.tls.key_file point to valid PEM files
    When the node starts
    Then the DoH and DoT listeners use that cert+key
    And no self-signed cert is generated

  @fsid:FS-DohDisabledByDefault
  Scenario: DoH disabled when no port is set
    Given node.dns.listen.doh_port is unset or 0
    When the node starts
    Then no DoH listener is opened
    And the node logs "DoH disabled" (or omits the DoH "listening on" line entirely)

  @fsid:FS-DotDisabledByDefault
  Scenario: DoT disabled when no port is set
    Given node.dns.listen.dot_port is unset or 0
    When the node starts
    Then no DoT listener is opened

  Non-goals:
    - DNSCrypt (rare in modern clients)
    - HTTP/3 (DoH3) — defer
    - ACME / Let's Encrypt auto-renewal (operator runs certbot or supplies a cert)
    - Client-cert authentication on DoH/DoT
    - Rate limiting per-connection (defer to a reverse proxy)
