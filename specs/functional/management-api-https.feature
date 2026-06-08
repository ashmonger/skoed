Feature: HTTPS for the Management API
  As an operator running skoed on a publicly-reachable host
  I want the management API and Web UI to be reachable over HTTPS
  using the same cert skoed already manages for DoH/DoT
  So I don't need a reverse proxy in front of skoed just for TLS

  Background:
    Given a skoed node with the M1 management API on node.api_address
    And the M4 DoH/DoT cert mechanism (ACME or operator-supplied PEMs)
    And node.api.tls.enabled = false by default — the API stays plain HTTP

  @fsid:FS-MgmtApiHttpsListens
  Scenario: When api.tls.enabled, the API listener is HTTPS
    Given node.api.tls.enabled = true
    And node.dns.tls.cert_file + key_file point at a valid PEM pair
    When the node starts
    Then the API listener accepts only TLS connections on its port
    And plain HTTP connections are refused (or 308-redirected — see single_port)
    And the served cert matches the configured cert_file

  @fsid:FS-MgmtApiHttpsReusesAcmeCert
  Scenario: HTTPS reuses the ACME-managed cert when ACME is enabled
    Given node.dns.tls.acme.enabled = true with valid domains
    And node.api.tls.enabled = true
    When the node starts and ACME issues the cert
    Then the API listener serves the same cert as DoH/DoT
    And no separate cert is required for the API

  @fsid:FS-MgmtApiHttpsDualPort
  Scenario: Dual-port mode keeps plain HTTP for LAN scripts
    Given node.api.tls.enabled = true
    And node.api.tls.mode = "dual_port"
    And node.api.tls.https_address is set to a separate host:port
    When the node starts
    Then the original api_address still accepts plain HTTP
    And the new https_address accepts HTTPS
    And both listeners route to the same management API

  @fsid:FS-MgmtApiHttpsSinglePortRedirect
  Scenario: Single-port mode 308-redirects HTTP to HTTPS
    Given node.api.tls.enabled = true
    And node.api.tls.mode = "single_port" (default)
    When a plain HTTP request lands on api_address
    Then the response is 308 Permanent Redirect
    And the Location header points at https:// with the same host:port

  @fsid:FS-MgmtApiHttpsHSTSOptional
  Scenario: HSTS header is opt-in
    Given node.api.tls.enabled = true and node.api.tls.hsts = false (default)
    When a TLS request returns a response
    Then the response does NOT include Strict-Transport-Security
    And when hsts = true the header is present with max-age >= 86400

  @fsid:FS-MgmtApiHttpsDisabledByDefault
  Scenario: Without api.tls config the listener stays plain HTTP (no behaviour change)
    Given node.api.tls is absent or enabled = false
    When the node starts
    Then api_address serves plain HTTP exactly as it did pre-M4.6
    And no HTTPS listener is opened

  Non-goals:
    - A separate cert from DoH/DoT (one cert, one renewal)
    - mTLS / client certificates on the management API
    - Per-route TLS policies
    - HTTP/3 / QUIC for the management API
