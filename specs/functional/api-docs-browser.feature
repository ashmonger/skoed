Feature: API Documentation Browser
  As an operator integrating dblock with scripts / Home Assistant / a sidecar
  I want to read the full management API spec in my browser
  And try requests against my live node with one click
  So I don't have to alt-tab between the OpenAPI YAML and curl

  Background:
    Given a running dblock node with the M1 management API up
    And the OpenAPI doc at specs/technical/management-api.openapi.yaml is shipped inside the binary

  @fsid:FS-ApiDocsServed
  Scenario: Swagger UI is reachable at /api/docs
    When the admin opens GET /api/docs/ in a browser
    Then the response is 200
    And the body is HTML containing the string "swagger-ui"
    And the Content-Type is "text/html; charset=utf-8"

  @fsid:FS-ApiDocsOpenApiYaml
  Scenario: The raw OpenAPI YAML is served at /api/openapi.yaml
    When the admin requests GET /api/openapi.yaml
    Then the response is 200
    And the body starts with "openapi:"
    And the Content-Type is "application/yaml" or "text/yaml"
    And the body contains the x-tsid header from the source spec

  @fsid:FS-ApiDocsAssetsServed
  Scenario: The Swagger UI assets are bundled into the binary
    When the admin requests any of:
      | path                                     |
      | /api/docs/swagger-ui.css                 |
      | /api/docs/swagger-ui-bundle.js           |
    Then each returns 200 with the expected Content-Type
    And no file system reads happen at request time (all assets come from go:embed)

  @fsid:FS-ApiDocsHonorsBrowserAuth
  Scenario: Swagger UI's Try-it-out uses the operator's existing Basic Auth
    Given the admin is logged in (browser holds an authenticated session)
    When the operator clicks "Try it out" on GET /api/v1/blocklists
    Then the browser sends the Authorization: Basic header dblock already accepts
    And the response is 200 (no separate API key needed)

  @fsid:FS-ApiDocsDisabledByConfig
  Scenario: The docs bundle can be stripped via config
    Given the node config sets api.docs.enabled = false
    When the admin requests GET /api/docs/
    Then the response is 404
    And the same is true for /api/openapi.yaml

  Non-goals:
    - Public-facing docs (the API is on a private interface; docs ride along)
    - Generated client libraries (operators run `openapi-generator` themselves)
    - Redoc / Stoplight alternates — Swagger UI is the chosen renderer
    - Editing the spec from the UI
