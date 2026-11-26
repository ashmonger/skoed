Feature: DNS overload protection
  As a network administrator running skoed as the DNS server of my network
  I want a node that receives more DNS queries than it can answer to keep
  answering up to its capacity and to refuse the rest
  So that a burst of queries does not make the node stop answering, does
  not get it killed for lack of memory, and does not move the cluster
  leadership.

  The 5-node performance run of 2026-11-26 (demos/perf-5node-lxc/REPORT.md,
  Findings 1) shows the current behavior: above about 5,000 queries per
  second on 2 cores, a node accepts every query, its memory grows until it
  is killed, and in a 15-minute run every node stopped answering after 2 to
  5 minutes and still used 2 GB of memory 45 minutes after the load stopped.

  Draft awaiting UoR validation. Lines starting with "# DECISION:" mark
  choices the UoR has not made yet; each scenario below them is written
  with the recommended option.

  Non-goals:
    - Raising the number of queries per second a node can answer (this
      feature bounds what happens above that number, it does not move it)
    - Sending the excess queries of a node to another node of the cluster
    - Limiting the queries of one client so that it cannot take the
      capacity of the others (per-client fairness)
    - Giving some queries priority over others, e.g. cached or blocked
      names over names that need an upstream resolver
    - Protecting the DNS-over-HTTPS, DNS-over-TLS and DNSCrypt listeners
    - Protecting the management API and the web UI against request floods
    - Changing how a node starts after a restart (see
      dns-startup-readiness.feature)

  Background:
    Given a 3-node skoed cluster
    And each node has 2 CPU cores and 2 GB of memory
    And a blocklist "ads" containing "tracker.example.com" is loaded on every node
    And the DNS capacity of each node is 4,000 queries per second

  # DECISION: Who sets the DNS capacity of a node — options: A (recommended) a cluster-wide setting the administrator can change, with a default that skoed ships / B derived automatically by each node from its CPU and memory / C both, the administrator value overriding the derived one
  @fsid:FS-DnsOverloadCapacityConfigurable
  Scenario: The administrator changes the DNS capacity of the nodes
    When the administrator sets the DNS capacity to 2,000 queries per second
    Then within 5 seconds every node reports a DNS capacity of 2,000 queries per second
    And a node receiving 3,000 queries per second answers about 2,000 of them per second

  @fsid:FS-DnsOverloadAnswersUpToCapacity
  Scenario: An overloaded node keeps answering up to its capacity
    When clients send 16,000 queries per second to one node for 15 minutes
    Then in every minute of the run the node answers at least 3,600 queries per second
    And the 99th percentile latency of the answered queries stays below 50 ms

  # DECISION: What a client receives for a query above the capacity — options: A (recommended) a REFUSED response, so its resolver moves to its next configured server at once / B a SERVFAIL response / C no answer, the client waits for its own timeout
  # DECISION: Are clients expected to fall back to another node — options: A (recommended) yes, the administrator gives clients at least two nodes and skoed documents it / B no, skoed makes no assumption about client configuration
  @fsid:FS-DnsOverloadRefusesExcess
  Scenario: Queries above the capacity receive a REFUSED response
    When clients send 16,000 queries per second to one node
    Then the queries the node does not answer within its capacity receive a REFUSED response
    And no query waits more than 1 second for its answer

  @fsid:FS-DnsOverloadFilteringKept
  Scenario: Answered queries are filtered as usual during overload
    Given clients send 16,000 queries per second to one node
    When a client sends an A query for "tracker.example.com" to that node
    Then the node returns either the block response of "ads" or a REFUSED response
    And the node never returns the upstream address of "tracker.example.com"

  @fsid:FS-DnsOverloadMemoryBounded
  Scenario: An overloaded node stays within bounded memory and is not killed
    Given the memory used by the node with no DNS load is recorded
    When clients send 16,000 queries per second to one node for 15 minutes
    Then the memory used by the node grows by at most 200 MB over that level
    And the node is never killed for lack of memory
    And no leader election takes place during the run

  # DECISION: How soon after the load stops a node must be back to normal — options: A (recommended) answering everything within 5 seconds, memory back within 50 MB of its level before the load within 60 seconds / B the same within 5 minutes / C only "answers everything" is bounded, memory is not
  @fsid:FS-DnsOverloadRecoversWhenLoadDrops
  Scenario: A node recovers as soon as the load goes below its capacity
    Given the memory used by the node with no DNS load is recorded
    And clients have sent 16,000 queries per second to one node for 5 minutes
    When the load drops to 1,000 queries per second
    Then within 5 seconds the node answers every query and refuses none
    And within 60 seconds the memory used by the node is back within 50 MB of its level before the load

  @fsid:FS-DnsOverloadNoRefusalBelowCapacity
  Scenario: A node below its capacity refuses no query
    When clients send 3,000 queries per second to one node for 15 minutes
    Then the node answers every query
    And no query receives a REFUSED response because of the load

  # DECISION: How the administrator sees that a node is overloaded — options: A (recommended) a count of refused queries per node in the metrics endpoint and one log line per minute while queries are refused / B A plus an "overloaded" state per node in the cluster status and the web UI / C log lines only
  @fsid:FS-DnsOverloadVisibleToAdministrator
  Scenario: The administrator sees how many queries a node refused
    Given clients sent 16,000 queries per second to one node for 1 minute
    When the administrator reads the metrics of that node
    Then the metrics show the number of queries the node refused because of the load
    And the node's log has one line per minute saying it refused queries and how many
