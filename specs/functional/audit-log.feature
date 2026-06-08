Feature: Audit Log
  As an operator who shares dblock administration with one or two
  housemates / sysadmins
  I want every state-changing API call recorded with who / when / what
  So I can answer "who turned cat:doh off at 2 AM?" — and so the next
  M7 token attribution work has somewhere to send its writes.

  Background:
    Given a running dblock cluster with at least one node

  @fsid:FS-AuditWriteRecorded
  Scenario: A successful mutating API call writes one audit entry
    Given the operator is authenticated as admin
    When the admin POSTs /api/v1/blocklists with a valid body
    Then the response is 201
    And exactly one new audit entry exists with:
      | field         | value                |
      | actor         | "user:admin"         |
      | action        | "blocklist.create"   |
      | result        | "ok"                 |
    And the entry's `timestamp` is within 5 seconds of now
    And the entry carries a non-empty `target` referring to the created blocklist

  @fsid:FS-AuditFailedWriteRecorded
  Scenario: A failed mutating API call still writes an entry (result=error)
    Given an existing blocklist with id "house-block"
    When the admin POSTs /api/v1/blocklists with an id collision on "house-block"
    Then the response is 4xx
    And the audit log contains one new entry with result = "error"
    And the entry carries the rejected payload's summary

  @fsid:FS-AuditReadsNotRecorded
  Scenario: Read-only API calls do NOT write audit entries
    Given the operator is authenticated as admin
    When the admin issues GET /api/v1/blocklists 5 times
    Then the audit log is unchanged in size

  @fsid:FS-AuditListEndpointShape
  Scenario: GET /api/v1/audit returns paged entries
    Given the audit log contains at least 3 entries
    When the admin GETs /api/v1/audit?limit=2
    Then the response is 200
    And the body has shape:
      | field         | type     |
      | entries       | array(2) |
      | total         | integer  |
      | limit         | integer  |
      | offset        | integer  |
    And entries are sorted newest first

  @fsid:FS-AuditFilterByActor
  Scenario: Audit listing filters by actor
    Given the audit log has entries from "user:admin" AND "user:alice"
    When the admin GETs /api/v1/audit?actor=user:alice
    Then every returned entry has `actor == "user:alice"`

  @fsid:FS-AuditFilterByAction
  Scenario: Audit listing filters by action prefix
    Given the audit log has "blocklist.create" AND "profile.update" AND "settings.update" entries
    When the admin GETs /api/v1/audit?action=blocklist.
    Then every returned entry's action starts with "blocklist."

  @fsid:FS-AuditReplicatesAcrossNodes
  Scenario: Audit entries replicate to every node in a cluster
    Given a 3-node cluster with the leader and 2 followers
    When the admin makes a mutating API call on the leader
    Then GET /api/v1/audit on EACH of the 3 nodes returns the same entry within 2 seconds

  @fsid:FS-AuditRequiresAuth
  Scenario: GET /api/v1/audit requires authentication
    Given no Authorization header
    When a request hits /api/v1/audit
    Then the response is 401

  @fsid:FS-AuditMetricsCounter
  Scenario: Each audit write increments a Prometheus counter
    Given a baseline value of dblock_audit_events_total{action="blocklist.create"} = N
    When the admin POSTs /api/v1/blocklists
    Then /metrics shows dblock_audit_events_total{action="blocklist.create"} = N + 1

  Non-goals:
    - Tamper-evident hash chain (Raft replication already provides
      per-entry consensus; tampering one node = breaking Raft)
    - Forwarding to external SIEM (operator pipes the API)
    - Audit of read operations (writes only)
    - Per-field diff (`diff_summary` is a human string, not a JSON patch)
    - Retention sweeper UI (90-day default trim is non-configurable for M5.2)
