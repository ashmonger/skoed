Feature: Schedule-Based Rules
  As a parent
  I want a profile's blocklists to apply only during certain hours
  So that streaming is fine on weekends but blocked on school nights.

  Background:
    Given a "kids" profile with the "social" blocklist
    And the node's timezone is configured (default: UTC; overridable via
      node.timezone in config.yaml)

  @fsid:FS-ScheduleActiveWindow
  Scenario: A schedule restricts the profile's blocking to its active window
    Given the "kids" profile has a schedule "evening-clamp" active
      Mon-Fri 20:00-23:59 and Sun 21:00-23:59 in the node's local timezone
    And the schedule scopes the "social" blocklist
    When a kids client queries "facebook.com" at 19:30 local time on Wednesday
    Then the response is forwarded (schedule inactive → blocklist off)
    When the same client queries at 21:30 local time
    Then the response is NXDOMAIN (schedule active → blocklist applied)

  @fsid:FS-ScheduleAllowMode
  Scenario: An allow-window schedule INVERTS the meaning (allowed only inside)
    Given a "homework" schedule with mode=allow_only_inside
      Mon-Fri 16:00-19:00 scoping the "social" blocklist
    When a kids client queries "facebook.com" inside the window
    Then the response is forwarded
    When the same client queries outside the window
    Then the response is NXDOMAIN

  @fsid:FS-ScheduleMultipleProfiles
  Scenario: Schedules attach to specific profile/blocklist pairs
    When the admin attaches "evening-clamp" to ("kids", "social")
    And separately attaches "evening-clamp" to ("teens", "gaming")
    Then the two clamps evaluate independently per profile
    And removing one does not affect the other

  @fsid:FS-ScheduleApiCrud
  Scenario: Admin manages schedules via the API
    When the admin POSTs a new schedule
      {id, name, mode: block_only_inside | allow_only_inside,
       windows: [{days:[Mon,Tue,…], start: "20:00", end: "23:59"}]}
    Then the schedule is replicated cluster-wide
    When the admin attaches the schedule to a (profile, blocklist) pair
    Then the binding is replicated
    When the admin DELETEs the schedule
    Then every binding referencing it is implicitly dropped

  @fsid:FS-ScheduleTimezoneIsNodeLocal
  Scenario: Schedule evaluation respects the node's configured timezone
    Given the node's timezone is "America/Los_Angeles"
    And a schedule fires Mon-Fri 20:00-22:00
    When a client queries at 03:30 UTC (= 20:30 Pacific the previous day)
    Then the schedule treats it as 20:30 Monday in Pacific time
    And evaluates accordingly

  Non-goals:
    - One-off date overrides (e.g., "no social on 2026-12-25") — defer to M4
    - Sunrise/sunset relative windows
    - Per-client (not per-profile) schedules
