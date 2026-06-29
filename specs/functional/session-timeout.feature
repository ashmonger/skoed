Feature: Configurable session timeout
  As a network administrator
  I want to control how long a web UI session stays valid before requiring re-authentication
  So that unattended consoles are protected and session lifetime matches my organisation's security policy

  Background:
    Given the admin is authenticated in the web UI

  Non-goals:
    - Inactivity-based timeout (idle detection) — this feature controls absolute session lifetime from login
    - Per-user or per-role timeout (skoed has a single admin account)
    - API token lifetime (API tokens are managed separately under M7)
    - Token rotation or sliding-window expiry
    - "Remember me" / keep-me-logged-in override
    - Forcing existing active sessions to expire immediately when the setting is changed

  @fsid:FS-SessionTimeoutViewCurrentSetting
  Scenario: Admin views the current session timeout setting
    Given the admin navigates to the Settings page
    Then the admin can see the current session timeout value
    And the displayed value matches what was last configured, or the default if never changed

  @fsid:FS-SessionTimeoutSetCustomDuration
  Scenario: Admin sets a custom session timeout duration
    Given the admin is on the Settings page
    When the admin selects a session timeout duration and saves the setting
    Then skoed confirms the change
    And new sessions created after the change expire after the configured duration

  @fsid:FS-SessionTimeoutDefaultApplied
  Scenario: Default session timeout applies when the setting has never been changed
    Given the session timeout setting has never been explicitly configured
    When the admin logs in
    Then the session expires after 8 hours
    And the admin is redirected to the login page upon expiry

  @fsid:FS-SessionTimeoutExpiredSessionRedirectedToLogin
  Scenario: Expired session redirects the browser to the login page
    Given the admin logged in more than <timeout> ago
    When the admin performs any action in the web UI
    Then skoed returns HTTP 401
    And the browser redirects the admin to the login page
    And a message indicates the session has expired

  @fsid:FS-SessionTimeoutExistingSessionsUnaffected
  Scenario: Changing the timeout does not invalidate currently active sessions
    Given the admin has an active session that was created before the setting was changed
    When the admin changes the session timeout to a shorter duration and saves
    Then the currently active session remains valid until its original expiry time
    And only sessions created after the change use the new timeout duration

  @fsid:FS-SessionTimeoutPersistsAcrossRestart
  Scenario: Session timeout setting persists across server restarts
    Given the admin has configured a non-default session timeout
    When skoed is restarted
    Then the session timeout setting retains the configured value
    And new sessions use the configured duration, not the default
