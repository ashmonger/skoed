Feature: Custom Block Page
  As a network administrator
  I want blocked domains to redirect to a human-readable block page
  So that users understand why a site is unavailable

  Non-goals:
    - HTTPS for the block page HTTP server
    - Per-domain or per-profile block page content
    - IPv6 redirect (AAAA queries return SERVFAIL under redirect policy)
    - Bypass or allow workflow from the block page
    - Custom HTML templates; only title, message, and contact_email are configurable

  @fsid:FS-BlockPageRedirectReturnsIP
  Scenario: redirect policy returns block_page_ip for blocked A query
    Given the node is configured with block_policy "redirect"
    And block_page.ip is set to "203.0.113.1"
    And a domain "blocked.example.com" is on an active blocklist
    When a DNS A query is sent for "blocked.example.com"
    Then the response has RCODE NOERROR
    And the answer section contains an A record pointing to "203.0.113.1"

  @fsid:FS-BlockPageRedirectServfailAAAA
  Scenario: redirect policy returns SERVFAIL for blocked AAAA query
    Given the node is configured with block_policy "redirect"
    And block_page.ip is set to "203.0.113.1"
    And a domain "blocked.example.com" is on an active blocklist
    When a DNS AAAA query is sent for "blocked.example.com"
    Then the response has RCODE SERVFAIL

  @fsid:FS-BlockPageNonRedirectUnaffected
  Scenario: non-redirect policies are unaffected by block_page config
    Given the node is configured with block_policy "nxdomain"
    And a domain "blocked.example.com" is on an active blocklist
    When a DNS A query is sent for "blocked.example.com"
    Then the response has RCODE NXDOMAIN

  @fsid:FS-BlockPageHttpServerResponds
  Scenario: block page HTTP server responds with HTML on GET /
    Given the node is configured with block_policy "redirect"
    And block_page.port is set to a valid port
    When an HTTP GET request is sent to the block page port at path "/"
    Then the response status is 200
    And the content-type is "text/html"
    And the body contains a self-contained HTML page

  @fsid:FS-BlockPageConfigGet
  Scenario: GET /api/v1/blockpage returns current block page config
    Given the node is running
    When GET /api/v1/blockpage is called
    Then the response status is 200
    And the body contains the current block_page configuration fields

  @fsid:FS-BlockPageConfigPatch
  Scenario: PATCH /api/v1/blockpage updates block page config and persists via Raft
    Given the node is running
    When PATCH /api/v1/blockpage is sent with title "Access Denied" and message "This site is blocked"
    Then the response status is 200
    And GET /api/v1/blockpage returns the updated title and message

  @fsid:FS-BlockPageTitleInResponse
  Scenario: block page HTML contains the configured title
    Given the node is configured with block_policy "redirect"
    And PATCH /api/v1/blockpage sets title to "My Custom Title"
    When an HTTP GET request is sent to the block page port at path "/"
    Then the response body contains "My Custom Title"
