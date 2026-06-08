Feature: Anti-spoof detection via lease history
  As a household admin
  I want skoed to flag MAC / hostname / Client-ID changes that look like spoofing
  So that I notice when a device's identity unexpectedly shifts

  Background:
    Given skoed records lease history in bbolt: `(client_id, mac, hostname, first_seen, last_seen)` tuples
    And anomalies are kept for 7 days then evicted
    And the lease cache currently contains:
      | client_id   | mac               | hostname    |
      | id:tablet42 | aa:bb:cc:dd:ee:42 | kid-tablet  |
      | id:laptop10 | aa:bb:cc:dd:ee:10 | home-laptop |

  @fsid:FS-SpoofMacChangedForKnownClientId
  Scenario: A known Client-ID appears with a brand-new MAC
    Given the lease history shows id:tablet42 paired with aa:bb:cc:dd:ee:42
    When a DHCP refresh produces a lease "id:tablet42 ff:00:00:00:00:99 kid-tablet"
    Then an anomaly is recorded with kind "mac_changed_for_client_id"
    And the anomaly references both MACs and the Client-ID
    And GET /api/v1/clients/anomalies returns the new entry

  @fsid:FS-SpoofClientIdChangedForKnownMac
  Scenario: A known MAC suddenly reports a different Client-ID
    Given the lease history shows aa:bb:cc:dd:ee:42 paired with id:tablet42
    When a DHCP refresh produces a lease "id:attacker99 aa:bb:cc:dd:ee:42 kid-tablet"
    Then an anomaly with kind "client_id_changed_for_mac" is recorded

  @fsid:FS-SpoofNewMacForExistingHostname
  Scenario: A known hostname appears on a brand-new MAC + Client-ID
    Given the lease history shows hostname "kid-tablet" on aa:bb:cc:dd:ee:42 / id:tablet42
    When a DHCP refresh produces a lease "id:nobody 11:22:33:44:55:66 kid-tablet"
    Then an anomaly with kind "new_device_steals_hostname" is recorded

  @fsid:FS-SpoofHostnameChangeIsInfo
  Scenario: A known device renames itself — info-level, no anomaly
    Given the lease history shows id:laptop10 with hostname "home-laptop"
    When a DHCP refresh produces a lease for id:laptop10 with hostname "office-laptop"
    Then NO anomaly is recorded
    And the lease cache is updated with the new hostname
    And a structured-log event "device_renamed" is emitted

  @fsid:FS-SpoofAnomaliesInResponse
  Scenario: Per-client lookup surfaces recent anomalies
    Given two anomalies exist for ip 192.168.1.42 in the last 24 hours
    When the admin calls GET /api/v1/clients/192.168.1.42
    Then the response body contains an `anomalies` field with both records
    And each record carries `kind`, `detected_at`, and `details`

  @fsid:FS-SpoofDashboardAlert
  Scenario: Dashboard alert card surfaces active anomalies
    Given two unresolved anomalies exist cluster-wide
    When the admin loads the Dashboard
    Then a warning card "Possible identity spoofing" lists the anomalies
    And each line links to /clients/{ip}

  @fsid:FS-SpoofAnomalyRetention
  Scenario: Anomalies older than 7 days are evicted
    Given an anomaly with detected_at 8 days ago exists in bbolt
    When the retention sweep runs (boot-time + every 24 h)
    Then the old anomaly is removed
    And anomalies less than 7 days old are kept

  @fsid:FS-SpoofAcknowledge
  Scenario: Admin acknowledges (dismisses) an anomaly
    Given an unresolved anomaly id "ANOM-001" exists
    When the admin POSTs /api/v1/clients/anomalies/ANOM-001/acknowledge
    Then the anomaly's `acknowledged_at` is set
    And the Dashboard alert card no longer lists it

  Non-goals:
    - Active mitigation — alert only
    - ARP/NDP cross-check (Layer 3 anti-spoof, backlog)
    - Cross-cluster anomaly correlation (each node's bbolt FSM is
      replicated, so anomalies surface cluster-wide automatically)
    - Heuristic confidence scoring — every event is a binary anomaly
