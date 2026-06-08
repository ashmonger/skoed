Feature: Query log
  As a network administrator
  I want skoed to record every DNS query it processes
  So that I can audit network activity and diagnose filtering behavior per client

  Non-goals:
    - The query log is not a security audit log (configuration changes are out of scope; see audit log in M4)
    - Query log entries are not replicated across cluster nodes in M1 (each node keeps its own log)
    - Log entries are not exported as part of the config import/export archive

  @fsid:FS-QueryLogRecordsEntry
  Scenario: Every DNS query produces a log entry
    When a client with address "192.168.1.10" sends an A query for "example.com"
    Then a log entry is created containing:
      | field       | value                     |
      | timestamp   | the time of the query     |
      | client      | 192.168.1.10              |
      | domain      | example.com               |
      | query type  | A                         |
      | outcome     | forwarded                 |

  @fsid:FS-QueryLogOutcomeBlocked
  Scenario: Blocked query is logged with outcome "blocked"
    Given "ads.example.com" is on an active blocklist
    When a client sends an A query for "ads.example.com"
    Then the log entry for this query has outcome "blocked"
    And the log entry includes the name of the matching blocklist

  @fsid:FS-QueryLogOutcomeLocal
  Scenario: Query resolved from a local DNS entry is logged with outcome "local"
    Given a local A record exists for "nas.home"
    When a client sends an A query for "nas.home"
    Then the log entry for this query has outcome "local"

  @fsid:FS-QueryLogIPv6Client
  Scenario: Query from an IPv6 client is logged with the IPv6 address
    When a client with address "fd00::42" sends an A query for "example.com"
    Then the log entry records client address "fd00::42"

  @fsid:FS-QueryLogBrowseAll
  Scenario: Admin browses the query log
    Given several queries have been processed
    When the admin requests the query log
    Then the response contains log entries in reverse chronological order
    And each entry includes timestamp, client address, domain, query type, and outcome

  @fsid:FS-QueryLogFilterByClient
  Scenario: Admin filters the query log by client address
    Given queries have been processed from clients "192.168.1.10" and "192.168.1.20"
    When the admin requests the query log filtered by client "192.168.1.10"
    Then the response contains only entries with client "192.168.1.10"

  @fsid:FS-QueryLogFilterByOutcome
  Scenario: Admin filters the query log by outcome
    When the admin requests the query log filtered by outcome "blocked"
    Then the response contains only entries where outcome is "blocked"

  @fsid:FS-QueryLogRetentionBound
  Scenario: Query log is bounded to a maximum number of entries
    Given the log retention limit is set to 10000 entries
    When more than 10000 queries are processed
    Then the log contains at most 10000 entries
    And the oldest entries are discarded first

  @fsid:FS-QueryLogRetentionConfigurable
  Scenario: Admin configures the log retention limit
    When the admin sets the log retention limit to 5000 entries
    Then the log retains at most 5000 entries going forward
