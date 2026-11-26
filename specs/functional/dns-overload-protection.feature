Feature: DNS overload protection
  As a network administrator running skoed as the DNS server of my network
  I want a node that receives more DNS queries than its DNS capacity to keep
  answering up to that capacity and to refuse the rest
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
  with the recommended option. The Background sets the DNS capacity
  explicitly, which works with the recommended option of the first
  DECISION (a setting the administrator can change); if the capacity is
  derived by each node instead, the Background cannot fix its value and
  the rates below must be expressed relative to the capacity each node
  reports.

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
    And the DNS cache and the query log of each node hold at most 10,000 entries (the defaults)
    And a blocklist "ads" with block policy NXDOMAIN containing "tracker.example.com" is loaded on every node
    And the upstream resolver answers every query within 10 ms
    And every client sending queries is allowed to query the nodes
    And the administrator has set the DNS capacity to 4,000 queries per second
    And the load generator runs on a host that does not share CPU with the nodes
    And unless a scenario says otherwise, the queries sent are 70 % names in the DNS cache, 20 % names not in the DNS cache and 10 % names blocked by "ads"

  # DECISION: Who sets the DNS capacity of a node — options: A (recommended) a cluster-wide setting the administrator can change, with a default that skoed ships / B derived automatically by each node from its CPU and memory / C both, the administrator value overriding the derived one
  # DECISION: What the default DNS capacity is — options: A (recommended) the highest rate at which a node with 2 cores and 2 GB, running the fixed code with the query mix of this Background, loses less than 1 % and keeps p99 below the bound of FS-DnsOverloadAnswersUpToCapacity, measured once and recorded in the technical spec / B 4,000 queries per second (not a healthy measurement: in T10 nodes failed at 4,000 with the current code) / C no default, no limit until the administrator sets one
  @fsid:FS-DnsOverloadCapacityConfigurable
  Scenario: A cluster has a default DNS capacity that the administrator can change
    Given a new cluster where the administrator has not set a DNS capacity
    When the administrator reads the DNS settings on any node
    Then the DNS settings show the default DNS capacity
    When the administrator sets the DNS capacity to 2,000 queries per second
    Then within 5 seconds the DNS settings of every node show a DNS capacity of 2,000 queries per second
    And a node receiving 3,000 queries per second answers between 1,800 and 2,000 of them per second

  # DECISION: What latency bound applies to the queries a node answers during overload — options: A (recommended) p99 below 50 ms, between the T2 p99 at 5,000 qps (23.6 ms) and at full rate (56–61 ms) / B p99 below 25 ms, the T2 p99 at 5,000 qps / C no latency bound, only the answered rate
  @fsid:FS-DnsOverloadAnswersUpToCapacity
  Scenario: An overloaded node keeps answering up to its capacity
    When clients send 16,000 queries per second to one node for 15 minutes
    Then in every minute of the run the node answers at least 3,600 queries per second
    And the 99th percentile latency of the answered queries stays below 50 ms

  # DECISION: What a client receives for a query above the capacity — options: A (recommended) a REFUSED response, so its resolver moves to its next configured server at once / B a SERVFAIL response / C no answer, the client waits for its own timeout. This is decided together with the not-ready response of FS-DnsStartupRefusesUntilReady (dns-startup-readiness.feature), so that a client sees one behavior for "this node cannot answer now".
  # DECISION: Are clients expected to fall back to another node — options: A (recommended) yes, the administrator gives clients at least two nodes and skoed documents it / B no, skoed makes no assumption about client configuration
  @fsid:FS-DnsOverloadRefusesExcess
  Scenario: Queries above the capacity receive a REFUSED response
    When clients send 16,000 queries per second to one node for 15 minutes
    Then at least 99 % of the queries sent receive an answer or a REFUSED response
    And every response that is not an answer is a REFUSED response
    And the 99.9th percentile response time stays below 1 second

  @fsid:FS-DnsOverloadFilteringKept
  Scenario: Answered queries are filtered as usual during overload
    Given clients send 16,000 queries per second to one node
    When a client sends 1,000 A queries for "tracker.example.com" to that node over 10 seconds
    Then at least one of them receives a NXDOMAIN response
    And every response is either NXDOMAIN or REFUSED
    And no response contains an address for "tracker.example.com"

  # DECISION: How much the memory of an overloaded node may grow — options: A (recommended) at most 200 MB over its level with no DNS load, about the 160 MB of heap in use measured at 10,000 qps in Findings 1 plus a margin / B at most 100 MB / C a fixed ceiling for the whole process (e.g. 1 GB) whatever the level with no load
  @fsid:FS-DnsOverloadMemoryBounded
  Scenario: An overloaded leader stays within bounded memory and is not killed
    Given the memory used by the leader with its lists loaded and no DNS load is recorded
    When clients send 16,000 queries per second to the leader for 15 minutes
    Then the memory used by the leader grows by at most 200 MB over that level
    And the leader is never killed for lack of memory
    And the leader stays the leader and no leader election takes place during the run

  # DECISION: How soon after the load drops a node must be back to normal — options: A (recommended) it answers every query within 5 seconds, and its memory is back within 50 MB of its level before the load within 5 minutes / B the same, memory within 60 seconds / C only answering is bounded, memory stays within the bound of FS-DnsOverloadMemoryBounded but need not go back down. Risk for A and B: the memory going back down depends on the Go runtime returning freed memory to the system; in Findings 1 the node still held 458 MB against 52 MB before the load, 120 s after the backlog had drained, so B probably needs skoed to force the release.
  @fsid:FS-DnsOverloadRecoversWhenLoadDrops
  Scenario: A node recovers as soon as the load goes below its capacity
    Given the memory used by the node with its lists loaded and no DNS load is recorded
    And clients have sent 16,000 queries per second to one node for 5 minutes
    When the load drops to 1,000 queries per second
    Then within 5 seconds the node answers every query and refuses none
    And within 5 minutes the memory used by the node is back within 50 MB of its level before the load

  @fsid:FS-DnsOverloadNoRefusalBelowCapacity
  Scenario: A node below its capacity refuses no query
    Given every name queried receives an answer other than REFUSED when the node has no load
    When clients send 3,000 queries per second to one node for 15 minutes
    Then no query receives a REFUSED response
    And less than 0.1 % of the queries sent receive no response

  # DECISION: How the administrator sees that a node is overloaded — options: A (recommended) a count of refused queries per node in the metrics endpoint and one log line per minute while queries are refused / B A plus an "overloaded" state per node in the cluster status and the web UI / C log lines only
  @fsid:FS-DnsOverloadVisibleToAdministrator
  Scenario: The administrator sees how many queries a node refused
    Given clients sent 16,000 queries per second to one node for 1 minute
    When the administrator reads the metrics of that node
    Then the metrics show a count of the queries the node refused because it was overloaded
    And that count equals, within 1 %, the number of REFUSED responses the clients received
    And the node's log has one line per minute of the run saying it refused queries and how many
