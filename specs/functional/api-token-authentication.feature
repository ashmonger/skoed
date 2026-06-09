Feature: API token authentication
  As a household admin who runs scripts, Home Assistant, or CLI tools
    that interact with skoed
  I want to mint revocable, scoped bearer tokens
  So that non-interactive callers authenticate without storing my main
    password, and I can revoke individual integrations without changing
    my login credentials

  Background:
    Given skoed is running with a configured admin username and password
    And the management API is reachable

  Non-goals:
    - OAuth 2.0 / OIDC integration
    - LDAP / SAML federation
    - Per-IP or per-CIDR token binding
    - Token-for-Web-UI-login (sessions keep using password)
    - Replacing Basic Auth in the same release (two-release deprecation window)

  # ── Minting ──────────────────────────────────────────────────────────────────

  @fsid:FS-TokenMintReturnsValueOnce
  Scenario: Minting a token returns the raw value exactly once
    Given the admin is authenticated
    When the admin POSTs to /api/v1/tokens with body
      {"label":"home-assistant","scopes":["read","write"]}
    Then the response is 201
    And the body contains a non-empty "token" string
    And the body contains "id", "label", "scopes", "created_at"
    And the body does NOT contain "token_hash"
    When the admin GETs /api/v1/tokens
    Then the listed entry for that token id has no "token" field
    And the listed entry has "label" = "home-assistant"

  @fsid:FS-TokenMintDefaultScopeIsReadWrite
  Scenario: Minting without explicit scopes yields read+write
    Given the admin is authenticated
    When the admin POSTs to /api/v1/tokens with body
      {"label":"default-scope-token"}
    Then the response is 201
    And the body contains "scopes" containing ["read","write"]
    And the body does NOT contain "cluster:admin" in scopes

  @fsid:FS-TokenMintRequiresClusterAdminScope
  Scenario: Only a token with cluster:admin scope can mint new tokens
    Given a token T1 with scopes ["read","write"] exists
    When a request is made to POST /api/v1/tokens using T1 as Bearer
    Then the response is 403
    Given a token T2 with scopes ["cluster:admin"] exists
    When a request is made to POST /api/v1/tokens using T2 as Bearer
    Then the response is 201

  @fsid:FS-TokenMintWithExpiry
  Scenario: Admin mints a token with an explicit expiry
    Given the admin is authenticated
    When the admin POSTs to /api/v1/tokens with body
      {"label":"ci-token","scopes":["read","write"],"expires_at":"2287-11-09T11:46:39Z"}
    Then the response is 201
    And the body contains "expires_at" = "2287-11-09T11:46:39Z"

  # ── Listing ───────────────────────────────────────────────────────────────────

  @fsid:FS-TokenListNeverExposesRawValue
  Scenario: The token list never exposes the raw token value or hash
    Given tokens T1 and T2 exist
    When the admin GETs /api/v1/tokens
    Then the response is 200
    And the body is a JSON array
    And no entry in the array has a "token" field
    And no entry in the array has a "token_hash" field
    And every entry has "id", "label", "scopes", "created_at", "last_used_at"

  # ── Revocation ───────────────────────────────────────────────────────────────

  @fsid:FS-TokenRevokeImmediatelyInvalidates
  Scenario: Revoking a token immediately rejects future requests bearing it
    Given a token T with scopes ["read","write"] exists
    And T successfully authenticates a request to GET /api/v1/health
    When the admin DELETEs /api/v1/tokens/{T.id}
    Then the response is 204
    When a request to GET /api/v1/health uses T as Bearer
    Then the response is 401

  @fsid:FS-TokenRevokeRequiresClusterAdminScope
  Scenario: Revoking a token requires cluster:admin scope
    Given a token T_victim with scopes ["read","write"] exists
    And a token T_unprivileged with scopes ["read","write"] exists
    When T_unprivileged attempts DELETE /api/v1/tokens/{T_victim.id}
    Then the response is 403
    And T_victim still authenticates requests successfully

  # ── Authentication ────────────────────────────────────────────────────────────

  @fsid:FS-TokenBearerAuthenticatesRequest
  Scenario: A valid bearer token authenticates any protected route
    Given a token T with scopes ["read","write"] exists
    When a request to GET /api/v1/blocklists uses Authorization: Bearer <T>
    Then the response is 200
    When a request to POST /api/v1/blocklists uses Authorization: Bearer <T>
      and a valid blocklist body
    Then the response is 201

  @fsid:FS-TokenInvalidBearer401
  Scenario: Invalid, expired, or revoked bearer tokens yield 401
    When a request to GET /api/v1/health uses Authorization: Bearer invalid-garbage
    Then the response is 401
    Given a token T with expires_at in the past exists
    When a request to GET /api/v1/health uses Authorization: Bearer <T>
    Then the response is 401
    Given a token T2 is revoked
    When a request to GET /api/v1/health uses Authorization: Bearer <T2>
    Then the response is 401

  @fsid:FS-TokenBasicAuthStillWorks
  Scenario: Basic Auth continues to work as a deprecated transition path
    When a request to GET /api/v1/blocklists uses Authorization: Basic <admin:password>
    Then the response is 200
    And the response does NOT include a "Deprecation" warning for Basic Auth
      (deprecation notice deferred to a later minor release)

  # ── Scopes ────────────────────────────────────────────────────────────────────

  @fsid:FS-TokenReadScopeBlocksWrites
  Scenario: A read-only token cannot call state-mutating endpoints
    Given a token T with scopes ["read"] exists
    When T requests POST /api/v1/blocklists with a valid body
    Then the response is 403
    When T requests PATCH /api/v1/profiles/default with a valid body
    Then the response is 403
    When T requests DELETE /api/v1/blocklists/some-id
    Then the response is 403
    When T requests GET /api/v1/blocklists
    Then the response is 200

  @fsid:FS-TokenWriteScopeAllowsMutations
  Scenario: A write-scoped token can call all data-mutation endpoints
    Given a token T with scopes ["read","write"] exists
    When T requests POST /api/v1/blocklists with a valid body
    Then the response is 201
    When T requests PATCH /api/v1/profiles/default with a valid body
    Then the response is 200

  # ── Expiry ────────────────────────────────────────────────────────────────────

  @fsid:FS-TokenNeverExpiresWhenNoExpirySet
  Scenario: A token with no expires_at never expires
    Given a token T with no expires_at was minted one year ago
    When T requests GET /api/v1/health
    Then the response is 200

  @fsid:FS-TokenExpiryEnforced
  Scenario: An expired token is immediately rejected
    Given a token T with expires_at = one second ago exists
    When T requests GET /api/v1/health
    Then the response is 401

  # ── Patching ──────────────────────────────────────────────────────────────────

  @fsid:FS-TokenPatchRelabelsAndUpdatesExpiry
  Scenario: Admin can relabel a token and change its expiry but not its scopes
    Given a token T with label "old-label" and scopes ["read","write"] exists
    When the admin PATCHes /api/v1/tokens/{T.id} with
      {"label":"new-label","expires_at":"2287-11-09T11:46:39Z"}
    Then the response is 200
    And GET /api/v1/tokens returns the entry with label = "new-label"
    And the scopes remain ["read","write"] unchanged
    When the admin PATCHes /api/v1/tokens/{T.id} with {"scopes":["read"]}
    Then the response is 400
    And the body mentions that scopes cannot be changed after minting

  # ── Audit log ─────────────────────────────────────────────────────────────────

  @fsid:FS-TokenAuditEntryRecordsTokenId
  Scenario: State-changing API calls made via a token record actor = "token:<id>"
    Given a token T with id "abc123" and scopes ["read","write"] exists
    When T requests POST /api/v1/blocklists with a valid body
    Then the audit log entry for that action has actor = "token:abc123"
    And the entry has action = "blocklist.create"

  @fsid:FS-TokenAuditEntryForPasswordAuth
  Scenario: State-changing calls made via Basic Auth record actor = "user:<username>"
    When the admin uses Basic Auth to POST /api/v1/blocklists with a valid body
    Then the audit log entry for that action has actor = "user:admin"
