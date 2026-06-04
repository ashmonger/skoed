Feature: Category-Based Blocking
  As an administrator
  I want to subscribe to curated domain categories instead of hand-managing
  thousands of blocklist entries
  So that I can enable "adult content" or "gambling" with one click.

  Background:
    Given the dblock binary ships an embedded `category-catalog` mapping each
    category name to a default upstream blocklist URL
    And the catalog covers: adult, gambling, social, gaming, streaming, doh

  @fsid:FS-CategoryCatalogListed
  Scenario: The catalog is queryable via the API
    When the admin GETs /api/v1/categories
    Then the response lists every available category with name, description,
         default URL, suggested format, and an `enabled_for_profiles[]`
         array showing which profiles currently subscribe to it

  @fsid:FS-CategoryEnableAddsBlocklist
  Scenario: Enabling a category on a profile creates a managed blocklist
    When the admin POSTs to /api/v1/categories/adult/enable {profile_id: "kids"}
    Then a new blocklist is created with id=`cat:adult` and source.url=catalog default
    And the "kids" profile's blocklists include `cat:adult`
    And `GET /api/v1/blocklists/cat:adult` shows `managed: true`

  @fsid:FS-CategoryDisableRemovesAssociation
  Scenario: Disabling a category removes the link but keeps the blocklist
    When the admin POSTs to /api/v1/categories/adult/disable {profile_id: "kids"}
    Then the "kids" profile no longer references `cat:adult`
    But the `cat:adult` blocklist itself stays (other profiles may still use it)
    When NO profile references the category any longer
    Then `cat:adult` is automatically pruned on the next stats.prune tick

  @fsid:FS-CategoryRefreshRespectsManaged
  Scenario: Managed blocklists refresh from the catalog's default URL
    Given `cat:adult` was created from the catalog
    When the admin clicks Refresh in the UI (or POSTs .../refresh)
    Then the domain set is re-downloaded from the catalog's default URL
    And the operator-facing UI surfaces the catalog name, not just the raw URL

  @fsid:FS-CategoryOverrideUrl
  Scenario: Operator overrides the catalog URL for a category
    When the admin PATCHes /api/v1/categories/adult {url: "https://custom/adult.txt"}
    Then subsequent refreshes pull from the override URL
    And the category in /api/v1/categories shows the override + a "reset to default" hint

  @fsid:FS-CategoryDohEnabledByDefault
  Scenario: The `doh` category is enabled on the default profile out of the box
    Given a freshly bootstrapped single-node dblock
    When the admin GETs /api/v1/categories
    Then the `doh` category shows `enabled_for_profiles: ["default"]`
    And queries for "cloudflare-dns.com", "dns.google", "dns.adguard.com" return NXDOMAIN

  Non-goals:
    - User-contributed categories (operator can still create custom blocklists)
    - Category-specific blocking policies different from the profile's default
    - Time-based category activation (handled by schedule-rules.feature)
