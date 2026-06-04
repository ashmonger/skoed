Feature: SafeSearch Enforcement
  As a parent
  I want search engines to be locked to their SafeSearch endpoints
  So that explicit content is filtered out at the search layer.

  Background:
    Given dblock can rewrite responses for specific hostnames to point at
    each provider's "SafeSearch" or "restricted-content" edge

  @fsid:FS-SafeSearchGoogle
  Scenario: Google SafeSearch is enforced via CNAME rewrite
    Given the "kids" profile has SafeSearch enabled for Google
    When a kids client queries "www.google.com"
    Then the response contains a CNAME pointing to "forcesafesearch.google.com"
    And subsequent queries to that CNAME return the upstream A/AAAA records

  @fsid:FS-SafeSearchBing
  Scenario: Bing SafeSearch is enforced via CNAME rewrite to "strict.bing.com"
    When a kids client queries "www.bing.com"
    Then the response contains a CNAME to "strict.bing.com"

  @fsid:FS-SafeSearchYoutube
  Scenario: YouTube Restricted Mode is enforced via CNAME rewrite
    When a kids client queries "www.youtube.com"
    Then the response contains a CNAME to "restrict.youtube.com"

  @fsid:FS-SafeSearchDuckDuckGo
  Scenario: DuckDuckGo Safe Search is enforced via CNAME rewrite
    When a kids client queries "duckduckgo.com"
    Then the response contains a CNAME to "safe.duckduckgo.com"

  @fsid:FS-SafeSearchOptInPerProfile
  Scenario: SafeSearch is per-profile, opt-in
    Given the "adults" profile has SafeSearch disabled
    When an adults client queries "www.google.com"
    Then the response is forwarded unchanged (no CNAME injection)

  @fsid:FS-SafeSearchAaaa
  Scenario: SafeSearch rewrites apply to AAAA queries too
    When a kids client issues an AAAA query for "www.google.com"
    Then the response contains a CNAME to "forcesafesearch.google.com"
    And an AAAA answer for that CNAME

  Non-goals:
    - Bing's strict-mode header injection (we can't; it's HTTPS)
    - YouTube's per-account Restricted Mode toggle (login-side; out of scope)
    - DoH client bypass (covered by FS-DohDetectionResolverBlocklist)
