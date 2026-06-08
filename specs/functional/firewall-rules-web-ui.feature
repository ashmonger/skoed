Feature: Web UI "Copy rules" buttons on Clients / Stats pages
  As an operator looking at the skoed dashboard
  I want a one-click way to grab firewall rules that close the DoH gap
  For a single client, a subnet, or every IP attached to a profile
  So I can paste them into my edge router without leaving the browser.

  Background:
    Given the operator is authenticated on the skoed Web UI
    And the curated DoH/DoT resolver IP database has at least one snapshot loaded
    And the API endpoint GET /api/v1/firewall-rules is reachable

  @fsid:FS-FwRuleUiClientsRowActionVisible
  Scenario: Each client row exposes a "Copy DoH-gap rules" action
    Given the Clients page lists at least one client with IP 10.42.10.50
    When the page is rendered
    Then the row for 10.42.10.50 carries a "Copy DoH-gap rules" action
    And clicking it opens a modal scoped to that single client IP

  @fsid:FS-FwRuleUiClientsModalPlatformTabset
  Scenario: The client-scoped modal lets the operator pick a platform
    Given the "Copy DoH-gap rules" modal is open for client 10.42.10.50
    Then a tabset is visible with one tab per supported platform
      | tab        |
      | iptables   |
      | nftables   |
      | mikrotik   |
      | opnsense   |
      | unifi      |
    And the active tab shows a preview of the generated rule blob
    And switching tabs reloads the preview for the new platform

  @fsid:FS-FwRuleUiClientsCopyToClipboard
  Scenario: The copy button puts the previewed blob on the clipboard
    Given the "Copy DoH-gap rules" modal is open for client 10.42.10.50
    And the "nftables" tab is active and shows a non-empty preview
    When the operator clicks "Copy"
    Then the clipboard contains the exact text shown in the preview
    And the modal surfaces a "Copied" confirmation

  @fsid:FS-FwRuleUiStatsSubnetCallout
  Scenario: The Stats page exposes a top-level "Closing the DoH gap" callout
    When the Stats page is rendered
    Then a "Closing the DoH gap" callout is visible above the fold
    And the callout offers a subnet picker pre-populated with subnets observed in the cluster
    And the callout offers the same platform tabset as the Clients modal

  @fsid:FS-FwRuleUiStatsSubnetPreviewAndCopy
  Scenario: The Stats callout previews and copies subnet-scoped rules
    Given the Stats callout is showing subnet 10.0.0.0/24
    When the operator selects the "iptables" tab
    Then a preview is rendered for scope=subnet&subnet=10.0.0.0/24&platform=iptables
    And clicking "Copy" places the preview text on the clipboard

  @fsid:FS-FwRuleUiProfileScope
  Scenario: The modal can be scoped to every IP on a profile
    Given a profile "kids" exists with client IPs 10.42.10.50 and 10.42.10.51
    And the operator opens the "Copy DoH-gap rules" modal from the profile context
    When the "mikrotik" tab is active
    Then the preview is generated for scope=profile&profile=kids&platform=mikrotik
    And the preview text references both 10.42.10.50 and 10.42.10.51 as source addresses

  @fsid:FS-FwRuleUiKeyboardNavigablePlatformTabs
  Scenario: The platform tabset is keyboard-navigable
    Given the "Copy DoH-gap rules" modal is open
    And the "iptables" tab is focused
    When the operator presses ArrowRight
    Then focus moves to the "nftables" tab
    And the preview switches to the nftables blob
    And pressing Home returns focus to the first tab

  @fsid:FS-FwRuleUiStaleSnapshotBanner
  Scenario: A stale resolver snapshot surfaces a warning in the UI
    Given the resolver snapshot is older than 7 days and the API returns stale=true
    When the "Copy DoH-gap rules" modal opens
    Then a stale-snapshot banner is visible above the preview
    And the banner names the snapshot's fetched_at timestamp
    And the "Copy" button remains enabled

  @fsid:FS-FwRuleUiEmptyResolverDatabase
  Scenario: An empty resolver database disables the copy action
    Given the resolver database has no entries (no snapshot has ever been fetched)
    When the "Copy DoH-gap rules" modal opens
    Then the preview area shows an empty-state message
    And the "Copy" button is disabled
    And a link to refresh the resolver database is offered to admins

  @fsid:FS-FwRuleUiUnauthorizedRedirect
  Scenario: An unauthenticated session cannot reach the firewall-rules UI
    Given the operator is logged out
    When the operator navigates directly to the Clients page
    Then the UI redirects to the login screen
    And the "Copy DoH-gap rules" action is never rendered

  Non-goals:
    - Pushing rules directly to firewall devices (skoed only generates text)
    - SSH-ing into routers or invoking vendor APIs from the browser
    - A rule-editor / WYSIWYG diff view (operators paste as-is or hand-edit)
    - Per-resolver toggles in the UI (the snapshot is consumed whole; curation is the database's job)
    - A dry-run "would these rules break my LAN?" simulator
    - Mobile-first layout (the dashboard is a desktop operator tool)
