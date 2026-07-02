Feature: Schedule Binding Web UI and Bulk Operations
  As an operator
  I want to manage schedule bindings entirely from the Web UI
  So that I can configure time-gated filtering without touching the API directly

  Non-goals:
  - Per-client or per-device schedule granularity (profile-level only)
  - Calendar-based one-off overrides (scheduled windows repeat weekly)
  - Mobile-optimised touch editor (desktop browser only for M37)

  Background:
    Given the skoed web interface is accessible
    And I am logged in as admin

  @fsid:FS-ScheduleBindingListPage
  Scenario: Operator can view all schedule bindings in a table
    Given schedule bindings exist for multiple profiles and blocklists
    When I navigate to the Schedules page
    Then I see a table with columns: profile, blocklist, schedule name, active-window summary, next-trigger time
    And each row has links to the relevant profile and blocklist detail pages

  @fsid:FS-DragDropTimeWindowEditor
  Scenario: Operator can create time windows using a drag-and-drop weekly grid
    Given I am creating or editing a schedule
    When I drag across a time range in the weekly grid
    Then a new time window is created covering the dragged hours and days
    And the underlying window data is updated to reflect the selection

  @fsid:FS-ScheduleTemplateLibrary
  Scenario: Operator can start from a built-in schedule preset
    Given I am creating a new schedule
    When I select the "Weekdays school hours" preset
    Then the time windows are pre-populated with weekday 08:00–15:00 windows
    When I select the "Bedtime" preset
    Then the time windows are pre-populated with nightly 21:00–07:00 windows

  @fsid:FS-BulkBindScheduleToBlocklists
  Scenario: Operator can apply a schedule to multiple profile–blocklist pairs at once
    Given multiple blocklists are checked on the blocklist list page
    When I select "Apply schedule" from the bulk-action menu
    And I choose a profile and a schedule in the dialog
    Then bindings are created for each selected blocklist
    And a success message shows how many bindings were created

  @fsid:FS-ScheduleBindingConflictWarning
  Scenario: Creating a duplicate binding shows a warning
    Given a binding already exists for profile "kids" and blocklist "ads" on schedule "evenings"
    When I try to create another binding for the same profile and blocklist
    Then a warning is shown: "A binding already exists for this profile–blocklist pair"
    And I can confirm to replace it or cancel
