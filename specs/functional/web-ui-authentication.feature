Feature: Web UI authentication
  As a network administrator
  I want the skoed management interface to require authentication
  So that unauthorized users cannot modify the DNS filtering configuration

  Non-goals:
    - DNS query processing (port 53) is never gated by authentication
    - Multi-user access control (roles, permissions) is out of scope for M1
    - OAuth, OIDC, or SSO integration is out of scope for M1
    - Session token rotation and expiry policies are out of scope for M1 (basic auth per request is acceptable)

  @fsid:FS-WebUiAuthUnauthenticatedRequestRejected
  Scenario: Unauthenticated request to management API is rejected
    When a client sends a request to any management API endpoint without credentials
    Then skoed returns HTTP 401 Unauthorized
    And the response includes a WWW-Authenticate header

  @fsid:FS-WebUiAuthUnauthenticatedUiRedirect
  Scenario: Unauthenticated browser request to web UI is challenged
    When a browser requests the web UI root without credentials
    Then skoed returns HTTP 401 or redirects to a login page

  @fsid:FS-WebUiAuthValidCredentials
  Scenario: Request with valid credentials is accepted
    Given the admin password is set
    When a client sends a management API request with valid credentials
    Then skoed returns the requested resource with HTTP 200

  @fsid:FS-WebUiAuthInvalidCredentials
  Scenario: Request with invalid credentials is rejected
    When a client sends a management API request with an incorrect password
    Then skoed returns HTTP 401 Unauthorized

  @fsid:FS-WebUiAuthFirstRunSetup
  Scenario: Admin sets credentials on first run
    Given skoed has just been installed with no credentials configured
    When the admin accesses the web UI for the first time
    Then skoed prompts the admin to set a username and password before granting access
    And subsequent requests require those credentials

  @fsid:FS-WebUiAuthPasswordChange
  Scenario: Admin changes the password
    Given the admin is authenticated
    When the admin changes the password to a new value
    Then subsequent requests with the old password are rejected with HTTP 401
    And requests with the new password are accepted

  @fsid:FS-WebUiAuthDnsUnaffected
  Scenario: DNS resolution continues while the management API is unauthenticated
    Given no credentials have been configured yet
    When a client sends a DNS query to port 53
    Then skoed resolves and responds to the query normally
    And authentication state has no effect on DNS query processing
