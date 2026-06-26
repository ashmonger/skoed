Feature: Block Page Enhancements
  As an operator running skoed with the redirect block policy,
  I want per-profile branding, IPv6 redirect, a time-bounded bypass workflow,
  and custom HTML templates,
  so that the block page is operationally useful for managed networks.

  # Non-goals:
  # - Rich media block pages (video, iframe) — plain HTML only
  # - Per-domain different block page — per-profile is the granularity
  # - Rate-limiting the bypass button (operator controls via passcode)
  # - HTTPS / ACME for the block page redirect server (deferred post-M33)

  Background:
    Given a running skoed node with admin credentials
    And the block policy is set to "redirect"

  # ─── Per-profile block page content ─────────────────────────────────────────

  @fsid:FS-BlockPagePerProfileContent
  Scenario: A profile with custom block page content serves its own title and message
    Given a profile "kids" with block page overrides title="Kids Filter" message="Ask a parent"
    And client 192.168.1.50 is assigned to profile "kids"
    When 192.168.1.50 resolves a blocked domain and visits the block page
    Then the block page HTML contains "Kids Filter"
    And the block page HTML contains "Ask a parent"

  @fsid:FS-BlockPageGlobalFallback
  Scenario: A profile without overrides falls back to the global block page content
    Given a profile "adults" with no block page overrides
    And the global block page title is "Access Blocked"
    And client 192.168.1.60 is assigned to profile "adults"
    When 192.168.1.60 visits the block page
    Then the block page HTML contains "Access Blocked"

  @fsid:FS-BlockPageProfileContactEmail
  Scenario: The contact_email override is surfaced on the block page
    Given a profile "corp" with block page override contact_email="it@company.com"
    And client 10.0.0.5 is assigned to profile "corp"
    When 10.0.0.5 visits the block page
    Then the block page HTML contains "it@company.com"

  # ─── IPv6 redirect ───────────────────────────────────────────────────────────

  @fsid:FS-BlockPageIPv6Redirect
  Scenario: An AAAA query for a blocked domain returns the configured IPv6 redirect address
    Given the redirect_address_v6 is set to "fd00::1"
    When a client queries the AAAA record for a blocked domain
    Then the DNS response is NOERROR with answer fd00::1
    And the TTL is the configured block page TTL

  @fsid:FS-BlockPageIPv6NotConfigured
  Scenario: Without redirect_address_v6, AAAA queries for blocked domains return NXDOMAIN
    Given redirect_address_v6 is not configured
    When a client queries the AAAA record for a blocked domain
    Then the DNS response is NXDOMAIN

  # ─── Time-bounded bypass ─────────────────────────────────────────────────────

  @fsid:FS-BlockPageBypassGranted
  Scenario: A client submits the correct bypass passcode and receives a time-bounded allowlist entry
    Given a profile "home" with bypass passcode "letmein" and bypass duration options [5, 30, 120] minutes
    And client 192.168.1.70 is assigned to profile "home" and sees a blocked page
    When 192.168.1.70 POSTs to /api/v1/bypass with passcode "letmein" and duration_minutes=30
    Then the response status is 200
    And an allowlist entry for 192.168.1.70 exists with expiry approximately 30 minutes from now
    And the blocked domain resolves for 192.168.1.70 during that window

  @fsid:FS-BlockPageBypassWrongPasscode
  Scenario: A wrong bypass passcode returns 403 and no allowlist entry is created
    Given a profile "home" with bypass passcode "letmein"
    When a client POSTs to /api/v1/bypass with passcode "wrong" and duration_minutes=30
    Then the response status is 403
    And no new allowlist entry is created

  @fsid:FS-BlockPageBypassExpiry
  Scenario: A bypass allowlist entry expires after the requested duration
    Given a bypass entry was created for 192.168.1.71 with duration_minutes=5
    When 5 minutes elapse
    Then the blocked domain no longer resolves for 192.168.1.71

  @fsid:FS-BlockPageBypassProfileRequired
  Scenario: A bypass request for a profile that has no passcode configured returns 404
    Given a profile "restricted" with no bypass passcode configured
    When a client POSTs to /api/v1/bypass for profile "restricted"
    Then the response status is 404

  # ─── Custom HTML template ────────────────────────────────────────────────────

  @fsid:FS-BlockPageCustomTemplate
  Scenario: An operator uploads a custom HTML template and blocked clients see it rendered
    Given the admin PUTs a custom HTML template "<!DOCTYPE html><html><body>Blocked: {{ .Domain }}</body></html>"
    When a client resolves and visits the block page for domain "ads.example.com"
    Then the block page HTML contains "Blocked: ads.example.com"

  @fsid:FS-BlockPageCustomTemplateVariables
  Scenario: The custom template receives Domain, Profile, and Joke variables
    Given the admin has uploaded a template that renders "{{ .Profile }} blocked {{ .Domain }}"
    And client 192.168.1.80 is assigned to profile "kids"
    When 192.168.1.80 visits the block page for a blocked domain "bad.com"
    Then the block page HTML contains "kids blocked bad.com"

  @fsid:FS-BlockPageCustomTemplateDelete
  Scenario: Deleting the custom template reverts to the built-in template
    Given the admin has uploaded a custom HTML template
    When the admin DELETEs /api/v1/blockpage/template
    And a client visits the block page
    Then the block page HTML uses the built-in default layout
