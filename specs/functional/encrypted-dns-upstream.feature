Feature: Encrypted DNS Upstream Resolvers
  As a privacy-conscious operator
  I want to forward DNS queries to upstream resolvers over DoT or DoH
  So that my LAN's DNS traffic is not observable on the wire between skoed and the public internet

  Non-goals:
    - Running skoed itself as a DoT/DoH server for clients (that is M4)
    - DNSSEC validation of upstream responses (that is M21)
    - DNSCrypt upstream support (out of scope)
    - Per-query upstream selection based on domain (use multiple profiles or future policy engine)
    - Automatic resolver discovery / bootstrapping from DHCP or OS resolver

  # ── DoT upstream ────────────────────────────────────────────────────────────

  @fsid:FS-DotUpstreamForwards
  Scenario: Queries are forwarded to a DoT upstream
    Given the upstream_resolvers config contains "tls://1.1.1.1:853"
    When a DNS query for "example.com" A arrives
    Then skoed opens a TLS connection to 1.1.1.1:853 and forwards the query
    And the DNS response is returned to the client

  @fsid:FS-DotUpstreamCertVerified
  Scenario: DoT upstream TLS certificate is verified by default
    Given the upstream_resolvers config contains "tls://1.1.1.1:853"
    And no skip_tls_verify flag is set
    When skoed establishes the DoT connection
    Then the server certificate is verified against the system CA bundle
    And if verification fails the upstream is skipped and SERVFAIL is returned after all upstreams are exhausted

  @fsid:FS-DotUpstreamSkipVerify
  Scenario: Operator can disable TLS verification for a DoT upstream
    Given the upstream_resolvers config contains "tls://192.168.1.1:853?skip_verify=true"
    When skoed establishes the DoT connection
    Then the server certificate is not verified
    And the query is forwarded successfully

  # ── DoH upstream ────────────────────────────────────────────────────────────

  @fsid:FS-DohUpstreamForwards
  Scenario: Queries are forwarded to a DoH upstream
    Given the upstream_resolvers config contains "https://cloudflare-dns.com/dns-query"
    When a DNS query for "example.com" A arrives
    Then skoed encodes the query as a DNS wireformat POST to the DoH URL
    And the DNS response is decoded and returned to the client

  @fsid:FS-DohUpstreamGetMethod
  Scenario: DoH upstream supports GET method via query string
    Given the upstream_resolvers config contains "https://dns.google/dns-query?method=get"
    When a DNS query for "example.com" A arrives
    Then skoed encodes the query as base64url and sends a GET request
    And the DNS response is decoded and returned to the client

  @fsid:FS-DohUpstreamCertVerified
  Scenario: DoH upstream HTTPS certificate is verified by default
    Given the upstream_resolvers config contains "https://dns.example.com/dns-query"
    And no skip_tls_verify flag is set
    When skoed makes the DoH HTTP request
    Then the HTTPS certificate is verified against the system CA bundle
    And if verification fails the upstream is skipped

  # ── Mixed and fallback ───────────────────────────────────────────────────────

  @fsid:FS-MixedUpstreamFallback
  Scenario: Operator can mix plain, DoT, and DoH upstreams; fallback is tried in order
    Given the upstream_resolvers config contains:
      | tls://1.1.1.1:853       |
      | https://dns.google/dns-query |
      | 8.8.8.8:53              |
    When the DoT upstream is unreachable
    Then skoed falls back to the DoH upstream
    And if that also fails, falls back to the plain UDP upstream
    And a successful response is returned to the client

  @fsid:FS-AllUpstreamsFail
  Scenario: SERVFAIL is returned when all upstreams are unreachable
    Given all configured upstreams (DoT and DoH) are unreachable
    When a DNS query arrives
    Then skoed returns SERVFAIL to the client

  # ── Configuration and API ────────────────────────────────────────────────────

  @fsid:FS-UpstreamSchemeValidation
  Scenario: Invalid upstream scheme is rejected at config load
    Given the upstream_resolvers config contains "ftp://1.1.1.1"
    When skoed starts or the config is applied via API
    Then an error is returned indicating the scheme is not supported
    And skoed does not start (or the PUT is rejected with 400)

  @fsid:FS-UpstreamConfigPersisted
  Scenario: Encrypted upstream config is persisted and survives restart
    Given an operator sets upstream_resolvers to ["tls://1.1.1.1:853"] via PUT /api/v1/settings
    When skoed is restarted
    Then the upstream_resolvers config still contains "tls://1.1.1.1:853"
    And queries continue to be forwarded over DoT

  @fsid:FS-UpstreamStatusApi
  Scenario: GET /api/v1/settings returns upstream resolver config including scheme
    Given the upstream_resolvers config contains "tls://1.1.1.1:853"
    When an operator calls GET /api/v1/settings
    Then the response includes upstream_resolvers with the full URL including scheme
