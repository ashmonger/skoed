Feature: Blocklist management
  As a network administrator
  I want to add, remove, enable, disable, and refresh blocklists
  So that I can control which domain sets are used for filtering

  Non-goals:
    - This feature does not cover per-client profiles (Milestone 3)
    - This feature does not cover automated scheduled refresh (Milestone 4)
    - This feature does not cover allowlist management (see allowlist-management.feature)

  Supported blocklist source formats:
    - Hosts file (e.g., "0.0.0.0 ads.example.com")
    - Domain list (one domain per line)
    - AdBlock/ABP syntax (e.g., "||ads.example.com^")

  @fsid:FS-BlocklistAddFromUrl
  Scenario: Admin adds a blocklist from a URL
    When the admin adds a blocklist named "ads" with source URL "https://example.com/ads.txt"
    Then skoed downloads the list from the URL
    And parses the domains from the supported format
    And the blocklist "ads" is active with the parsed domains

  @fsid:FS-BlocklistAddManual
  Scenario: Admin adds a blocklist with manually entered domains
    When the admin creates a blocklist named "custom" with domains ["malware.example.com", "spyware.example.org"]
    Then the blocklist "custom" is active
    And queries for "malware.example.com" are blocked

  @fsid:FS-BlocklistRemove
  Scenario: Admin removes a blocklist
    Given a blocklist "ads" is active
    When the admin removes the blocklist "ads"
    Then the blocklist "ads" no longer exists
    And queries for domains that were in "ads" are no longer blocked by that list

  @fsid:FS-BlocklistDisable
  Scenario: Admin disables a blocklist without removing it
    Given a blocklist "ads" is active containing "ads.example.com"
    When the admin disables the blocklist "ads"
    Then the blocklist "ads" is retained but inactive
    And queries for "ads.example.com" are no longer blocked

  @fsid:FS-BlocklistEnable
  Scenario: Admin re-enables a disabled blocklist
    Given a blocklist "ads" is disabled
    When the admin enables the blocklist "ads"
    Then the blocklist "ads" is active
    And queries for domains in "ads" are blocked again

  @fsid:FS-BlocklistRefresh
  Scenario: Admin manually refreshes a URL-based blocklist
    Given a blocklist "ads" was added from a URL
    When the admin triggers a manual refresh of the blocklist "ads"
    Then skoed downloads the latest version from the source URL
    And replaces the domain set with the newly parsed domains

  @fsid:FS-BlocklistParseHostsFormat
  Scenario: Blocklist in hosts file format is parsed correctly
    When the admin adds a blocklist with content "0.0.0.0 ads.example.com\n127.0.0.1 tracker.example.org"
    Then the blocklist contains "ads.example.com" and "tracker.example.org"
    And does not contain "0.0.0.0" or "127.0.0.1" as blocked domains

  @fsid:FS-BlocklistParseAskoedFormat
  Scenario: Blocklist in AdBlock/ABP format is parsed correctly
    When the admin adds a blocklist with content "||ads.example.com^\n! comment line\n||tracker.example.org^"
    Then the blocklist contains "ads.example.com" and "tracker.example.org"
    And comment lines are ignored

  @fsid:FS-BlocklistWildcardEntry
  Scenario: Blocklist entry with wildcard matches apex and all subdomains
    When the admin creates a blocklist "custom" with entry "*.ads.example.com"
    Then a query for "ads.example.com" is blocked
    And a query for "sub.ads.example.com" is blocked
    And a query for "deep.sub.ads.example.com" is blocked
    And a query for "other.example.com" is not blocked by this entry

  @fsid:FS-BlocklistStats
  Scenario: Admin views blocklist statistics
    Given a blocklist "ads" is active with 5000 domains
    When the admin views the blocklist list
    Then the "ads" blocklist shows a domain count of 5000
    And shows its source URL and last-updated timestamp
