Feature: ACME / Let's Encrypt TLS certificates for DoH and DoT
  As an operator running skoed on a publicly-reachable host
  I want skoed to obtain and renew its TLS certificate automatically via ACME
  So that DoH and DoT clients trust the cert without operator-supplied PEMs

  Background:
    Given skoed supports DoH and DoT via the M4 EncryptedServer
    And the operator has a public DNS name pointing at the node's address

  @fsid:FS-AcmeEnabledFromConfig
  Scenario: ACME is enabled via node.dns.tls.acme.enabled
    Given the config sets node.dns.tls.acme.enabled = true
    And node.dns.tls.acme.email = "ops@example.com"
    And node.dns.tls.acme.domains = ["dns.example.com"]
    When the node starts
    Then the EncryptedServer uses an autocert-issued certificate (not the self-signed fallback)
    And the certificate's Subject CommonName or SAN list contains "dns.example.com"

  @fsid:FS-AcmeCustomDirectory
  Scenario: Operator overrides the ACME directory URL (e.g. for staging)
    Given the config sets node.dns.tls.acme.directory_url to a non-empty URL
    When the node starts and acquires its first certificate
    Then the ACME client uses that directory URL instead of the Let's Encrypt production default

  @fsid:FS-AcmeChallengeListener
  Scenario: The HTTP-01 challenge listener responds on a configurable port
    Given node.dns.tls.acme.http_challenge_port = 8081
    When the node starts
    Then port 8081 accepts plain HTTP
    And GET /.well-known/acme-challenge/<token> on that port returns 200 with the challenge response when the ACME flow has staged one
    And every other path on that port returns 404

  @fsid:FS-AcmeCacheReuse
  Scenario: Cert is cached on disk and reused across restarts
    Given the node has previously obtained an ACME certificate
    And the cached cert is still valid (>= 30 days until expiry)
    When the node restarts
    Then the same certificate is served — no new ACME order is created
    And the cache path is <data_dir>/tls/acme-cache/

  @fsid:FS-AcmeFallsBackOnFailure
  Scenario: ACME failure does not block startup
    Given node.dns.tls.acme.enabled = true
    And the ACME directory is unreachable
    When the node starts
    Then the EncryptedServer still binds DoH / DoT
    And subsequent DoH connections succeed (using the cached cert if present, otherwise the self-signed cert)
    And the operator sees an error log line referencing the ACME failure

  @fsid:FS-AcmeDisabledByDefault
  Scenario: Without ACME config, the node uses self-signed (M4 default)
    Given node.dns.tls.acme is absent or has enabled=false
    When the node starts with doh_port > 0
    Then the self-signed cert at <data_dir>/tls/cert.pem is used
    And no ACME ports are opened

  Non-goals:
    - DNS-01 challenge (defer; the operator can use a sidecar like lego + a writable cert path)
    - Wildcard certs (require DNS-01)
    - Multiple-issuer support (Let's Encrypt or any other ACME-compliant CA, but not both at once)
    - Per-node automatic renewal scheduling (autocert renews lazily on cert load — good enough)
    - ACME-EAB (External Account Binding) — defer to a future track
