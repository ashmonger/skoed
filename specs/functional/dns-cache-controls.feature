Feature: DNS Cache Controls
  As an operator who just blocked a domain that's still cached
  I want to purge the DNS cache on demand
  And see basic cache health from outside the process
  So unrelated config changes stop bulldozing the whole cache

  Background:
    Given the DNS cache is enabled (settings.dns.cache.enabled = true)

  @fsid:FS-CachePurgeAll
  Scenario: Operator purges the whole cache
    Given the cache holds several entries from prior queries
    When the admin POSTs /api/v1/dns/cache/purge with no query params
    Then the response is 200 with a JSON body `{"purged": <count>}`
    And subsequent /api/v1/dns/cache/stats returns size = 0

  @fsid:FS-CachePurgeOneDomain
  Scenario: Operator purges one domain
    Given the cache holds entries for example.com (A) AND github.com (A)
    When the admin POSTs /api/v1/dns/cache/purge?domain=example.com
    Then the cached example.com entries are gone
    And github.com is still cached

  @fsid:FS-CacheStatsEndpoint
  Scenario: The stats endpoint exposes cache health
    When the admin requests /api/v1/dns/cache/stats
    Then the response is 200 with JSON containing:
      | field        | type    |
      | size         | integer |
      | max_entries  | integer |
      | hits         | integer |
      | misses       | integer |
      | evictions    | integer |

  @fsid:FS-CacheSurvivesConfigChange
  Scenario: Adding an unrelated config entry no longer wipes the cache
    Given a domain example.com is cached
    When the admin adds a local DNS entry for "unrelated.lab"
    Then example.com is still cached (size unchanged)
    And only entries matching the changed scope are invalidated

  @fsid:FS-CacheInvalidatesOnAllowlistAdd
  Scenario: Adding a domain to the allowlist invalidates that name in cache
    Given example.com is blocked AND its cached negative entry exists
    When the admin adds example.com to the allowlist
    Then the cache entry for example.com is dropped
    And other cached entries are untouched

  @fsid:FS-CacheRequiresAuth
  Scenario: Purge and stats require authentication
    Given no Authorization header
    When a request hits /api/v1/dns/cache/purge OR /api/v1/dns/cache/stats
    Then the response is 401

  Non-goals:
    - Persistent cache across restarts (the cache is by design ephemeral)
    - Per-client cache namespaces (defer until anyone asks)
    - Negative-result cache (NXDOMAIN caching) — separate scope decision
    - Cache pre-warming on boot
