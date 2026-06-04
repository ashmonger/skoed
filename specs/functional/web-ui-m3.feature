Feature: Web UI — M3 surfaces (profiles, schedules, categories, DoH)
  As a household administrator using the Web UI
  I want point-and-click management of profiles, schedules, and categories
  So that I can manage M3 features without composing JSON.

  Background:
    Given a running dblock node with the SPA embedded
    And the admin is logged in
    And the M3 backend endpoints (/profiles, /schedules, /categories) are present

  @fsid:FS-WebUiProfilesPage
  Scenario: The Profiles page surfaces every profile and lets the admin edit one
    When the admin opens /profiles
    Then a table lists every profile with id, name, blocklist count, client IP/CIDR count, and SafeSearch providers
    And clicking a row opens an inline editor with:
      | name                                   |
      | blocklists multi-select                |
      | allowlist textarea                     |
      | SafeSearch toggles per provider        |
      | client IPs textarea (one IP per line)  |
      | client CIDRs textarea (one CIDR/line)  |
    And Save submits PATCH /api/v1/profiles/{id} with only the dirty fields

  @fsid:FS-WebUiProfileCreate
  Scenario: Admin creates a new profile from the UI
    When the admin clicks "+ New profile"
    Then a modal collects id, name, and optional initial blocklists / client IPs
    And submit POSTs /api/v1/profiles
    And on success the new row appears in the table

  @fsid:FS-WebUiProfileDeleteProtectsDefault
  Scenario: The default profile cannot be deleted from the UI
    Given the default profile is in the table
    When the admin hovers the default row's Actions cell
    Then the Delete button is absent (or disabled with a tooltip)
    And POSTing DELETE on the default id via raw API still returns 409 server-side

  @fsid:FS-WebUiSchedulesPage
  Scenario: The Schedules page surfaces every schedule and lets the admin edit windows
    When the admin opens /schedules
    Then a table lists every schedule with name, mode (block_only_inside | allow_only_inside),
      and a summarized weekly window string ("Mon-Fri 20:00-23:59")
    And clicking a row opens an inline editor with a weekly grid + mode toggle
    And the editor exposes per-window day/start/end controls
    And Save submits PATCH /api/v1/schedules/{id}

  @fsid:FS-WebUiScheduleBindings
  Scenario: Admin attaches a schedule to a (profile, blocklist) pair
    Given a schedule "evening-clamp" exists
    When the admin opens the schedule's Bindings panel
    Then they can add a binding via two selects (profile, blocklist) and a Save button
    And the add submits POST /api/v1/schedules/{id}/bindings
    And they can remove an existing binding with a per-row delete button

  @fsid:FS-WebUiCategoriesPage
  Scenario: The Categories page lists the built-in catalog
    When the admin opens /categories
    Then a card per category surfaces name, description, effective URL, format, and the list of profiles currently subscribed
    And each card has Enable / Disable buttons that prompt for a target profile
    And the URL override editor accepts a custom upstream and PATCHes /api/v1/categories/{name}

  @fsid:FS-WebUiDohWidget
  Scenario: The Stats view surfaces DoH attempts today
    Given several clients have queried known DoH-resolver hostnames today
    When the admin opens /stats
    Then a "DoH attempts today" panel lists each client, probe count, and a link to /query-log?client=<ip>&category=doh-probe
    And the panel is hidden (or shows an empty state) when no DoH probes exist

  @fsid:FS-WebUiM3SidebarEntries
  Scenario: The sidebar surfaces the M3 routes
    When the admin views the sidebar
    Then the nav contains "Profiles", "Schedules", and "Categories" between "Local DNS" and "Cluster"
    And clicking each item routes to the corresponding view

  Non-goals:
    - Drag-and-drop schedule editor (a static weekly grid is enough at M3.1)
    - Visual flow chart of profile-rule precedence (operator reads the table)
    - I18n for the new strings (English only, same as M2.6)
