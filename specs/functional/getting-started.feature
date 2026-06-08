Feature: Getting Started card on the Dashboard
  As a new operator who just set the admin password (POST /api/v1/auth/setup)
  I want a "Getting Started" card on an empty Dashboard
  With a 3-step checklist (blocklist → cluster → point a client)
  So I know what to do next instead of staring at empty stat tiles.

  Background:
    Given a freshly-installed dblock node
    And the admin has set the password via POST /api/v1/auth/setup
    And the admin is logged into the Web UI

  @fsid:FS-GettingStartedShownWhenEmpty
  Scenario: Card appears when the cluster has no operator-added blocklists or profiles
    Given the cluster has no operator-added blocklists (bundled "cat:*" categories don't count)
    And the cluster has no operator-added profiles (the seeded "default" profile doesn't count)
    And the admin has not previously dismissed the card
    When the admin opens the Dashboard
    Then a "Getting Started" card is visible above any alert cards
    And the card shows a numbered 3-step checklist:
      | step | label                                                |
      | 1    | Add a blocklist                                      |
      | 2    | (optional) Bootstrap a cluster                       |
      | 3    | Point a client at dblock                             |
    And each step links to the matching page or docs anchor

  @fsid:FS-GettingStartedAutoHidesAfterFirstBlocklist
  Scenario: Card auto-hides once the operator adds their first blocklist
    Given the cluster has no operator-added blocklists or profiles
    And the "Getting Started" card is visible
    When the admin adds a blocklist via the API (any id that isn't a bundled "cat:*")
    And the admin reloads the Dashboard
    Then the "Getting Started" card is no longer visible

  @fsid:FS-GettingStartedDismissPersists
  Scenario: Operator-dismissed card stays dismissed across reloads
    Given the cluster has no operator-added blocklists or profiles
    And the "Getting Started" card is visible
    When the admin clicks the [x] dismiss button on the card
    Then the card disappears immediately
    And `localStorage["dblock.gettingStarted.dismissed"]` is `"true"`
    When the admin reloads the Dashboard
    Then the "Getting Started" card is still not visible
    Even though the cluster is otherwise still fresh

  @fsid:FS-GettingStartedDocsChapter
  Scenario: A docs chapter covers the same flow as the card
    Given the published docs site
    When the operator opens "First run > Getting started"
    Then the chapter exists at docs/src/first-run/getting-started.md
    And it lists the same three steps as the card
    And it includes copy-pasteable bash for each step
    And the card's CTA points operators at the chapter

  Non-goals:
    - A multi-step wizard / modal (operators dislike modals)
    - Pop-up toasts (zero pop-ups added)
    - Server-side dismissal state (per-browser is sufficient — operators
      who clear localStorage can see the card again, which is a feature)
    - Re-showing the card after dismissal once new blocklists are added
      (dismissal is sticky on purpose; we don't want to nag)
    - Account-level / multi-admin per-user state — single-org product
