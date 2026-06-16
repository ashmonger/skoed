Feature: In-place Upgrade
  As an operator who installed skoed from the .deb (M5.5)
  I want a Dashboard banner when a new release is available
  And a one-click upgrade that downloads, verifies, and swaps the binary
  So I don't have to babysit apt or copy binaries manually.

  Background:
    Given skoed is running with a known version
    And the operator can configure node.upgrade.feed_url + node.upgrade.cosign_pub_key

  @fsid:FS-UpgradeCheckEndpoint
  Scenario: GET /api/v1/upgrade/check returns version availability
    Given the feed URL serves a release manifest
    When the admin GETs /api/v1/upgrade/check
    Then the response body has:
      | field             | type    |
      | current_version   | string  |
      | available_version | string  |
      | upgrade_available | bool    |
      | release_notes_url | string  |
      | published_at      | string  |
    And `upgrade_available` is true iff available_version > current_version

  @fsid:FS-UpgradeCheckRequiresAuth
  Scenario: /api/v1/upgrade/check requires auth
    Given no Authorization header
    When a request hits /api/v1/upgrade/check
    Then the response is 401

  @fsid:FS-UpgradeStartRequiresLeader
  Scenario: POST /api/v1/upgrade/start is forwarded to the leader
    Given a 3-node cluster
    When the admin POSTs /api/v1/upgrade/start to a follower
    Then the response carries the leader's redirect (503 with leader_address)

  @fsid:FS-UpgradeStartRecordedInAudit
  Scenario: A triggered upgrade writes an audit entry
    Given a 1-node cluster
    When the admin POSTs /api/v1/upgrade/start (dry-run)
    Then a new audit entry with action="upgrade.start" exists
    And actor = "user:admin"

  @fsid:FS-UpgradeBinarySwap
  Scenario: POST /api/v1/upgrade/start downloads and swaps the binary
    Given a 1-node cluster with a feed reporting version 99.0.0
    And the feed's assets.linux_amd64 URL serves a valid skoed tar.gz
    When the admin POSTs /api/v1/upgrade/start
    Then the response is 202 with accepted=true and target_version=99.0.0
    And the new binary has been written to the executable path
    And (in production) the process calls os.Exit(0) so the supervisor restarts it
    And an audit entry with action="upgrade.start" is created

  @fsid:FS-UpgradeBannerOnDashboard
  Scenario: Dashboard shows the upgrade-available banner
    Given the feed reports an available version > current
    When the admin opens the Dashboard
    Then a banner labelled "Upgrade available" lists the new version + release notes link

  @fsid:FS-UpgradeNoBannerWhenCurrent
  Scenario: No banner when running the latest version
    Given the feed reports an available version <= current
    When the admin opens the Dashboard
    Then no upgrade-available banner is visible

  Non-goals:
    - Rollback to prior version (operator keeps the prior .deb if they
      want it; an upgrade flag --rollback is M5.6.1)
    - Zero-downtime upgrade on single-node deployments (a single-node
      restart IS the downtime)
    - GUI-managed OS updates (out of scope; that's apt unattended-
      upgrades)
    - Auto-apply upgrades (operator always clicks the button; nothing
      ever upgrades without intent)
    - Cosign signature verification — wired in M5.6.1, behind a
      `node.upgrade.require_signature` flag default false for v1
    - Rolling cluster-aware upgrade (nodes upgrade independently when
      triggered; coordinated rolling upgrade is M5.6.1)
