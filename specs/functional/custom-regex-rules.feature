Feature: Custom Filtering Rules (Regex + Exact)
  As a network administrator
  I want to write custom block and allow rules using regex patterns or exact domains
  So that I can express filtering logic that no blocklist URL can capture

  Background:
    Given the skoed node is running and the admin is authenticated
    And no custom rules exist

  Non-goals:
    - Replacing the blocklist system (custom rules are an additional layer, not a substitute)
    - Wildcard glob syntax (regex already covers it; two syntaxes would cause confusion)
    - Per-profile custom rules (profiles use per-profile allowlists; custom rules are cluster-wide)
    - Content or path inspection (rules match the queried domain name only)
    - Regex applied to the query type or client IP (domain matching only)

  # ─── Block rules ────────────────────────────────────────────────────────────

  @fsid:FS-CustomRulesRegexBlock
  Scenario: Regex pattern blocks matching domains
    Given the admin has saved the custom rule "/^ad[0-9]+\.example\.com$/"
    When a client queries "ad42.example.com"
    Then the response is blocked (NXDOMAIN or configured block policy)
    And the query log entry shows outcome "blocked" and source "custom_rule"
    When a client queries "safe.example.com"
    Then the response is forwarded normally

  @fsid:FS-CustomRulesExactBlock
  Scenario: Exact domain line blocks that domain
    Given the admin has saved the custom rule "tracking.bad-actor.net"
    When a client queries "tracking.bad-actor.net"
    Then the response is blocked
    And the query log entry shows source "custom_rule"
    When a client queries "other.bad-actor.net"
    Then the response is forwarded normally

  # ─── Allow rules ────────────────────────────────────────────────────────────

  @fsid:FS-CustomRulesRegexAllow
  Scenario: Regex allow rule overrides a blocklist match
    Given the domain "analytics.partner.com" is matched by an active blocklist
    And the admin has saved the custom allow rule "@@/\.partner\.com$/"
    When a client queries "analytics.partner.com"
    Then the response is forwarded (not blocked)
    And the query log entry shows outcome "allowed" and source "custom_rule"

  @fsid:FS-CustomRulesExactAllow
  Scenario: Exact allow rule overrides a blocklist match
    Given the domain "tracker.example.com" is matched by an active blocklist
    And the admin has saved the custom allow rule "@@tracker.example.com"
    When a client queries "tracker.example.com"
    Then the response is forwarded (not blocked)

  # ─── Priority ordering ───────────────────────────────────────────────────────

  @fsid:FS-CustomRulesPriority
  Scenario: Allow rule takes precedence over a block rule for the same domain
    Given the admin has saved the custom block rule "/\.example\.com$/"
    And the admin has saved the custom allow rule "@@safe.example.com"
    When a client queries "safe.example.com"
    Then the response is forwarded (allow wins)
    When a client queries "ads.example.com"
    Then the response is blocked (block applies)

  @fsid:FS-CustomRulesOverrideBlocklist
  Scenario: Custom block rule applies even when no blocklist matches
    Given no active blocklist covers the domain "homemade.blocker.internal"
    And the admin has saved the custom rule "/blocker\.internal$/"
    When a client queries "anything.blocker.internal"
    Then the response is blocked

  # ─── Rule management ────────────────────────────────────────────────────────

  @fsid:FS-CustomRulesEdit
  Scenario: Admin edits rules in the web UI and changes take effect immediately
    Given the admin navigates to the Custom Rules page
    When the admin types rules into the editor and clicks Save
    Then a "Saved" confirmation appears
    And subsequent DNS queries are evaluated against the new rules without restart
    When the admin clears all rules and clicks Save
    Then no custom rules are active

  @fsid:FS-CustomRulesValidation
  Scenario: Invalid regex is rejected before saving
    Given the admin types the rule "/[unclosed/" in the editor
    When the admin clicks Save
    Then an error message identifies the invalid pattern by line number
    And the previous valid rule set remains active

  # ─── Cluster sync ────────────────────────────────────────────────────────────

  @fsid:FS-CustomRulesClusterSync
  Scenario: Custom rules replicate to all cluster nodes
    Given a 3-node cluster and the admin saves new custom rules on the leader
    Then within 5 seconds all follower nodes apply the same rules to DNS queries
    And a DNS query sent to a follower node is filtered by the new rules
