Feature: Webhook / Push Alerts
  As a network administrator
  I want skoed to push JSON event notifications to external HTTP endpoints
  So that I can integrate skoed alerts into my monitoring or automation systems

  Non-goals:
    - Email or SMS delivery
    - Per-client device alerts beyond device.new
    - Durable message queue with guaranteed exactly-once delivery
    - Fan-out deduplication across cluster nodes
    - Webhook payload replay or history

  Background:
    Given a running skoed node

  @fsid:FS-WebhookCreate
  Scenario: Operator creates a webhook endpoint and retrieves it
    When the operator sends POST /api/v1/webhooks with a valid url, secret, and events list
    Then the response status is 201 and the body contains the new endpoint id
    And GET /api/v1/webhooks returns a list that includes the created endpoint
    And the returned endpoint reflects the url, events, and enabled state submitted

  @fsid:FS-WebhookDelete
  Scenario: Operator deletes a webhook endpoint
    Given a webhook endpoint has been created
    When the operator sends DELETE /api/v1/webhooks/{id}
    Then the response status is 204
    And GET /api/v1/webhooks no longer returns an entry with that id

  @fsid:FS-WebhookTestFire
  Scenario: Operator fires a test event to a webhook endpoint
    Given a webhook endpoint pointing to a reachable HTTP receiver has been created
    When the operator sends POST /api/v1/webhooks/{id}/test
    Then the response status is 200
    And the HTTP receiver receives exactly one POST request
    And the request body is valid JSON with event type "webhook.test"
    And the request carries an X-Skoed-Signature header

  @fsid:FS-WebhookEventDeviceNew
  Scenario: device.new event fires when an unknown IP sends its first DNS query
    Given a webhook endpoint subscribed to "device.new" is configured and points to a reachable HTTP receiver
    When a DNS query arrives from an IP address that skoed has not seen before
    Then the HTTP receiver receives a POST with event type "device.new" within 5 seconds
    And the payload data field contains the new client IP address

  @fsid:FS-WebhookEventBlocklistFailed
  Scenario: blocklist.download_failed event fires when a blocklist refresh fails
    Given a webhook endpoint subscribed to "blocklist.download_failed" is configured
    And a URL-source blocklist is configured with a URL that returns an error
    When the automatic refresh runs for that blocklist
    Then a POST with event type "blocklist.download_failed" is delivered to the configured endpoint
    And the payload data field contains the blocklist id and the error description

  @fsid:FS-WebhookSignature
  Scenario: Webhook delivery includes a valid HMAC-SHA256 signature
    Given a webhook endpoint with a known secret has been created
    When an event is delivered to that endpoint (via POST /{id}/test or a real event)
    Then the X-Skoed-Signature header value equals "sha256=" followed by the hex-encoded HMAC-SHA256 of the raw request body using the endpoint secret as the key
    And the receiver can independently verify the signature matches the body
