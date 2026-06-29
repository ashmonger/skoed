Feature: Certificate Management
  As an operator running skoed with HTTPS enabled,
  I want TLS certificates to renew automatically and be visible in the UI,
  so that the web UI and management API stay accessible over HTTPS without
  manual certificate intervention.

  # Non-goals:
  # - HSM / TPM key storage — key material stays on disk
  # - External CA integration — self-signed cluster mesh only; ACME is for management API
  # - Multi-domain SAN certificates beyond hostname + IP
  # - Client certificate authentication on the management API
  # - ACME DNS-01 challenge provider (Cloudflare, Route53) — HTTP-01 only in M34
  # - OCSP stapling — deferred post-M34

  Background:
    Given a running skoed node with admin credentials

  # ─── Certificate status visibility ──────────────────────────────────────────

  @fsid:FS-CertStatusApiReturnsExpiry
  Scenario: The cert status endpoint returns expiry dates for all certs
    Given TLS is configured on the management API
    When the admin calls GET /api/v1/cluster/certs/status
    Then the response includes ca_expires_at, and for each node: cert_expires_at and days_until_expiry
    And all expiry dates are valid RFC3339 timestamps

  @fsid:FS-CertStatusShowsAutoRenewConfig
  Scenario: The cert status response reflects the current auto-renew configuration
    Given tls.auto_renew is set to true in settings
    When the admin calls GET /api/v1/cluster/certs/status
    Then the response includes auto_renew: true
    And acme_domains lists the configured domain(s)

  # ─── ACME auto-renewal ───────────────────────────────────────────────────────

  @fsid:FS-AcmeAutoRenewalEnabled
  Scenario: When auto_renew is enabled and a cert is near expiry, renewal is triggered
    Given tls.auto_renew is true and the management API cert expires within 30 days
    When the auto-renewal background job runs
    Then skoed initiates an ACME HTTP-01 challenge for the configured domain
    And on success the new cert is loaded without a server restart

  @fsid:FS-AcmeAutoRenewalSkipsValidCert
  Scenario: Auto-renewal does not trigger when the cert has more than 30 days remaining
    Given tls.auto_renew is true and the management API cert expires in 60 days
    When the auto-renewal background job runs
    Then no ACME challenge is initiated

  @fsid:FS-AcmeAutoRenewalDisabledByDefault
  Scenario: Auto-renewal is disabled by default and requires explicit opt-in
    Given a fresh skoed node with no tls.auto_renew setting
    When the admin calls GET /api/v1/settings
    Then tls.auto_renew is false in the response

  @fsid:FS-AcmeConfigPersisted
  Scenario: ACME settings persist across restarts
    Given the admin PUTs tls.auto_renew=true and tls.acme.domains=["skoed.example.com"] to /api/v1/settings
    When the node restarts
    Then GET /api/v1/settings returns tls.auto_renew=true and the configured domain

  # ─── Per-node cert rotation ──────────────────────────────────────────────────

  @fsid:FS-PerNodeCertRotation
  Scenario: Rotating a single node's cert does not affect other nodes
    Given a 3-node cluster with valid certs on all nodes
    When the admin POSTs to /api/v1/cluster/nodes/skoed-2/rotate-cert
    Then skoed-2's cert_expires_at is updated to a future date
    And skoed-1 and skoed-3 cert_expires_at are unchanged

  @fsid:FS-PerNodeCertRotationUnknownNode
  Scenario: Rotating a cert for an unknown node returns 404
    When the admin POSTs to /api/v1/cluster/nodes/nonexistent/rotate-cert
    Then the response status is 404

  # ─── Settings UI ─────────────────────────────────────────────────────────────

  @fsid:FS-CertStatusVisibleInSettingsUi
  Scenario: The Settings page shows a TLS Certificates section with cert expiry
    Given the admin opens the Settings page
    Then a "TLS Certificates" section is visible
    And it shows the management API cert expiry date and days remaining
    And it shows the cluster mesh CA expiry date

  @fsid:FS-AutoRenewToggleInSettingsUi
  Scenario: The operator can enable auto-renew and set ACME domains from the Settings UI
    Given the admin opens the Settings page
    When they enable the auto-renew toggle and enter "skoed.example.com" as the ACME domain
    And click Save
    Then GET /api/v1/settings returns tls.auto_renew=true and acme_domains=["skoed.example.com"]

  @fsid:FS-RotateNowButtonInSettingsUi
  Scenario: Clicking "Rotate now" for a node triggers per-node cert rotation
    Given the admin opens the Settings page and sees the TLS Certificates section
    When they click "Rotate now" for skoed-1
    Then a POST is sent to /api/v1/cluster/nodes/skoed-1/rotate-cert
    And the UI refreshes to show the updated cert_expires_at for skoed-1
