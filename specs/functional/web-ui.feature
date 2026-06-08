Feature: Web UI
  As an administrator
  I want a browser-based interface for every skoed management operation
  So that I do not have to compose JSON or remember API paths.

  Background:
    Given a running skoed node with the SPA embedded in the binary
    And an authenticated admin session (basic auth credentials accepted)

  @fsid:FS-WebUiServedAtRoot
  Scenario: The SPA shell is served at GET /
    When the user opens GET /
    Then the response status is 200
    And the response body contains the `<div id="app">` mount point
    And asset URLs under /assets/ resolve to the embedded JS and CSS

  @fsid:FS-WebUiFirstRunSetup
  Scenario: A fresh node renders the setup form before login
    Given a node whose auth credentials have never been configured
    When the user opens the SPA
    Then the client-side router renders the Setup view
    And submitting the form POSTs /api/v1/auth/setup with the new credentials
    And the user is redirected to the dashboard on success

  @fsid:FS-WebUiLoginFlow
  Scenario: A returning admin signs in
    Given the node has admin credentials configured
    When the user submits valid credentials via the login form
    Then the SPA stores the credentials in sessionStorage
    And subsequent API calls carry an HTTP Basic Authorization header

  @fsid:FS-WebUiDashboardSurfaces
  Scenario: Dashboard surfaces cluster health, top blocked domains, per-node table
    When the user opens the Dashboard view
    Then the page renders cluster status, mode, members and total queries
    And it lists the cluster's top blocked domains for the current window
    And it lists every cluster node with role, sync state, and commit index

  @fsid:FS-WebUiBlocklistsCrud
  Scenario: An admin manages blocklists end-to-end
    When the user opens the Blocklists view
    Then they can create a blocklist from a URL or manual entries
    And they can toggle the enabled flag inline (PATCH replicated cluster-wide)
    And they can refresh a URL-sourced blocklist (POST .../refresh)
    And they can delete a blocklist with confirmation

  @fsid:FS-WebUiAllowlistCrud
  Scenario: An admin manages allowlist entries
    When the user opens the Allowlist view
    Then they can add a domain or `*.subdomain` wildcard
    And they can bulk-add domains from a textarea
    And they can search/filter entries client-side
    And they can remove an entry with confirmation

  @fsid:FS-WebUiLocalDnsCrud
  Scenario: An admin manages local DNS entries
    When the user opens the Local DNS view
    Then they can create A, AAAA, and CNAME entries with a TTL
    And they can edit an entry inline
    And they can delete an entry with confirmation

  @fsid:FS-WebUiQueryLog
  Scenario: An admin inspects query history
    When the user opens the Query Log view
    Then they see a live-tailing list of recent queries with outcome badges
    And they can filter by client IP and by outcome (forwarded/blocked/cached/local)
    And the cluster-wide toggle switches between local /query-log and /cluster/query-log

  @fsid:FS-WebUiStatsDashboard
  Scenario: An admin reads cluster-wide stats
    When the user opens the Stats view
    Then they see cluster totals (queries / blocked / forwarded / cached / local)
    And they see top-N domains and top-N clients aggregated across nodes
    And they see a per-node table with each node's hourly aggregate

  @fsid:FS-WebUiClusterOps
  Scenario: An admin operates the cluster from the UI
    When the user opens the Cluster view
    Then they see every node's role, sync state and last-contact
    And they can generate a single-use join token displayed once with a copy button
    And they can trigger a leadership transfer to a chosen follower
    And they can remove a node (non-leader only) with confirmation

  @fsid:FS-WebUiSettings
  Scenario: An admin edits cluster settings per section
    When the user opens the Settings view
    Then DNS, Filtering, and Query Log are three independently-savable cards
    And saving a section PATCHes /api/v1/settings with only that section's keys
    And "Saved." flashes on success and fades after ~2 seconds

  @fsid:FS-WebUiAccount
  Scenario: An admin changes their password
    When the user opens the Account view
    Then they can change their password by supplying current + new
    And success bounces them back to the login form to re-authenticate

  @fsid:FS-WebUiThemes
  Scenario: An admin chooses between Monokai and Monokai-Solarized themes, with light/dark mode
    When the user opens the theme controls in the header
    Then a sun/moon icon toggles light ↔ dark mode (class on <html>)
    And a palette select swaps between "monokai" and "monokai-solarized" data attribute
    And the choice persists in localStorage across reloads
    And the chosen mode is applied BEFORE first paint (no flash of wrong theme)

  Non-goals:
    - Multi-language (i18n) — English only at M2.6.
    - Custom user theming (only the two built-in palettes).
    - Mobile-first PWA install — responsive layout only.
    - Browser-driven E2E tests in CI (HTTP-level + manual screenshots cover M2.6).
