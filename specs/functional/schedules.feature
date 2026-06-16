Feature: Schedule Bindings and Config YAML Persistence
  As an administrator managing schedule-based filtering rules
  I want to list which profiles and blocklists a schedule is bound to
  And I want those bindings to survive a filesystem-level backup and restore
  So that I can inspect binding state via the API and trust PBS snapshots to capture it.

  Background:
    Given a running skoed cluster
    And the administrator is authenticated

  @fsid:FS-ScheduleBindingsList
  Scenario: Admin retrieves the binding list for a schedule
    Given a schedule "evening-clamp" exists
    And the binding (profile="kids", blocklist="social") is attached to "evening-clamp"
    And the binding (profile="teens", blocklist="gaming") is attached to "evening-clamp"
    When the administrator sends GET /api/v1/schedules/evening-clamp/bindings
    Then the response status is 200
    And the response body is a JSON array of two objects
    And each object contains "schedule_id", "profile_id", and "blocklist_id"
    And one entry has profile_id="kids" and blocklist_id="social"
    And one entry has profile_id="teens" and blocklist_id="gaming"

  @fsid:FS-ScheduleBindingsListEmpty
  Scenario: GET bindings returns an empty array when a schedule has no bindings
    Given a schedule "unbound-sched" exists with no bindings
    When the administrator sends GET /api/v1/schedules/unbound-sched/bindings
    Then the response status is 200
    And the response body is an empty JSON array

  @fsid:FS-ScheduleBindingsListNotFound
  Scenario: GET bindings for a non-existent schedule returns 404
    When the administrator sends GET /api/v1/schedules/ghost/bindings
    Then the response status is 404

  @fsid:FS-ScheduleConfigYaml
  Scenario: Schedules and their bindings appear in the on-disk shadow YAML
    Given a schedule "evening-clamp" exists
    And the binding (profile="kids", blocklist="social") is attached to "evening-clamp"
    When the Raft commit propagates and the debounce window elapses
    Then /var/lib/skoed/config.yaml contains a "schedules" section
    And that section includes the "evening-clamp" schedule with its mode and windows
    And the file contains a "schedule_bindings" section listing the (kids, social) binding
    And the file remains valid YAML parseable by standard tools

  Non-goals:
    - Editing schedules or bindings by writing to config.yaml (YAML is read-only shadow).
    - Filtering the bindings list by profile or blocklist (full list only at this milestone).
    - Paginating the bindings list.
