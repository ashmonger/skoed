Feature: Browser Extension — Push Notification Bridge
  As an administrator with skoed running on their home network
  I want a Firefox or Chrome browser extension that connects to skoed's event stream
  So that I receive native OS push notifications when important events occur
  without having to keep the skoed web UI open.

  Background:
    Given the administrator has installed the skoed browser extension
    And the extension is configured with the skoed admin URL and API token
    And the skoed node is reachable from the browser

  Non-goals:
    - Replacing the full skoed web UI (the extension is notification-only)
    - Mobile browser support (Firefox for Android / Chrome for Android)
    - Supporting more than one skoed instance per extension instance
    - Syncing extension settings across browser profiles via browser sync
    - Modifying DNS filtering rules from the extension popup

  # ── SSE connection ────────────────────────────────────────────────────────

  @fsid:FS-BrowserExtSseConnect
  Scenario: Extension opens a persistent SSE connection to the skoed event stream
    When the extension starts (browser launch or extension enable)
    Then the extension opens GET /api/v1/events with the configured API token
    And the connection stays open indefinitely, receiving server-sent events
    And if the connection drops, the extension reconnects with exponential back-off
    And the extension does not open more than one SSE connection at a time

  @fsid:FS-BrowserExtSseReconnect
  Scenario: Extension reconnects automatically after network disruption
    Given the extension has an open SSE connection
    When the network is interrupted for 10 seconds
    Then the extension detects the disconnection within 15 seconds
    And the extension retries the connection after a back-off delay (≤ 30 seconds)
    And upon reconnection the extension resumes receiving events without user action

  # ── Native OS notifications ────────────────────────────────────────────────

  @fsid:FS-BrowserExtNotifyDeviceNew
  Scenario: New device on the network triggers a native OS notification
    Given the extension is connected to the event stream
    And the administrator has enabled "device.new" notifications in the extension
    When skoed emits a device.new event for client IP "192.168.1.42"
    Then the extension displays a native OS notification:
      | title   | skoed — New Device             |
      | body    | New device: 192.168.1.42       |
    And clicking the notification opens the skoed web UI client page

  @fsid:FS-BrowserExtNotifyClusterDown
  Scenario: A cluster node going down triggers a native OS notification
    Given the extension is connected to the event stream
    And the administrator has enabled "cluster.node_down" notifications
    When skoed emits a cluster.node_down event for node "node-2"
    Then the extension displays a native OS notification:
      | title   | skoed — Node Down              |
      | body    | Cluster node node-2 is down    |
    And the notification persists until dismissed (requireInteraction = true)

  @fsid:FS-BrowserExtNotifyBlocklistFailed
  Scenario: A failed blocklist refresh triggers a native OS notification
    Given the extension is connected to the event stream
    And the administrator has enabled "blocklist.download_failed" notifications
    When skoed emits a blocklist.download_failed event
    Then the extension displays a native OS notification with the blocklist name and error

  @fsid:FS-BrowserExtNotifyFilterPause
  Scenario: Filtering pause start and expiry trigger native OS notifications
    Given the extension is connected to the event stream
    And the administrator has enabled "filter.pause_started" and "filter.pause_expired" notifications
    When skoed emits a filter.pause_started event
    Then the extension displays a notification "skoed — Filtering Paused"
    When skoed emits a filter.pause_expired event
    Then the extension displays a notification "skoed — Filtering Resumed"

  # ── Toolbar badge ────────────────────────────────────────────────────────

  @fsid:FS-BrowserExtBadgeConnected
  Scenario: Extension badge reflects connection state
    Given the extension is installed
    When the SSE connection is established and healthy
    Then the extension toolbar icon shows a green badge dot
    When the SSE connection is lost or the node is unreachable
    Then the extension toolbar icon shows a red badge dot
    When the extension is not configured
    Then the extension toolbar icon shows a grey badge dot

  # ── Popup panel ────────────────────────────────────────────────────────────

  @fsid:FS-BrowserExtPopupStatus
  Scenario: Extension popup shows connection status and recent events
    Given the extension toolbar icon is clicked
    When the popup opens
    Then the popup displays:
      | field           | value                                  |
      | Connection      | Connected / Disconnected / Unconfigured|
      | Node URL        | the configured skoed URL               |
      | Recent events   | last 10 events received, newest first  |
    And each event row shows the event type, timestamp, and a one-line summary

  @fsid:FS-BrowserExtPopupSettings
  Scenario: Administrator configures the extension from the popup
    Given the popup is open
    When the administrator enters a skoed URL and API token and clicks Save
    Then the extension stores the credentials in the browser's local storage
    And the extension immediately attempts to open the SSE connection
    And the administrator can toggle which event types trigger OS notifications

  # ── Multi-browser compatibility ────────────────────────────────────────────

  @fsid:FS-BrowserExtFirefoxMv2
  Scenario: Extension loads and functions correctly in Firefox (Manifest V2)
    Given the extension is packaged as a Firefox add-on (Manifest V2)
    When it is installed in Firefox via about:debugging or the AMO
    Then all scenarios above pass in Firefox
    And the extension uses the browser.* WebExtension API namespace

  @fsid:FS-BrowserExtChromeMv3
  Scenario: Extension loads and functions correctly in Chrome (Manifest V3)
    Given the extension is packaged as a Chrome extension (Manifest V3)
    When it is installed in Chrome via the developer mode or the CWS
    Then all scenarios above pass in Chrome
    And the extension uses a service worker (not a persistent background page)
    And the SSE connection is managed via the service worker keep-alive mechanism
