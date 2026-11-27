Feature: DNS overload protection
  As a network administrator running skoed as the DNS server of my network
  I want a node that receives more DNS queries than its DNS capacity to keep
  answering up to that capacity and to refuse the rest
  So that a burst of queries does not make the node stop answering, does
  not get it killed for lack of memory, and does not move the cluster
  leadership.

  The 5-node performance run of 2026-11-26 (demos/perf-5node-lxc/REPORT.md,
  Findings 1) shows the current behavior: above about 5,000 queries per
  second on 2 cores, a node accepts every query and its memory grows until
  it is killed. In a 15-minute run every node stopped answering after 2 to
  5 minutes; after the load stopped, one node was killed 10 minutes later
  and the four others still used 2 GB of memory 45 minutes later.

  Decisions and specification validated by the UoR on 2026-11-27. Lines starting with "# Decided" record those
  decisions; lines starting with "# Derived:" say where a number comes
  from.

  Rates are relative to C, the DNS capacity a node shows in its DNS
  settings, at most the default DNS capacity, because the default is
  measured later (see FS-DnsOverloadCapacityConfigurable). The memory used
  by a node is the resident memory of its skoed service as seen by the
  system.

  Non-goals:
    - Raising the number of queries per second a node can answer (this
      feature bounds what happens above that number, it does not move it)
    - Sending the excess queries of a node to another node of the cluster
    - Limiting the queries of one client so that it cannot take the
      capacity of the others (per-client fairness)
    - Giving some queries priority over others, e.g. cached or blocked
      names over names that need an upstream resolver
    - Protecting plain DNS over TCP, and the DNS-over-HTTPS, DNS-over-TLS
      and DNSCrypt listeners
    - Protecting the management API and the web UI against request floods
    - Changing how a node starts after a restart (see
      dns-startup-readiness.feature)

  Background:
    Given a 3-node skoed cluster
    And each node has 2 CPU cores, 2 GB of memory and no swap
    And the DNS cache and the query log of each node hold at most 10,000 entries (the defaults)
    And a blocklist "ads" with block policy NXDOMAIN containing "tracker.example.com" is loaded on every node
    And the upstream resolver answers every query within 10 ms
    And every client sending queries is inside the trusted subnets of the nodes
    And the load generator runs on a host that does not share CPU with the nodes
    And the load generator sends its queries over UDP at an even rate
    And unless a scenario says otherwise, "one node" is a follower
    And unless a scenario says otherwise, the queries sent are 70 % names in the DNS cache, 20 % names not in the DNS cache and 10 % names blocked by "ads"

  # Decided (UoR, 2026-11-27): the DNS capacity is a cluster-wide setting the administrator can change, with a default that skoed ships.
  # Decided (UoR, 2026-11-27): the default DNS capacity is the highest C for which the 2 × C scenarios of this feature (FS-DnsOverloadAnswersUpToCapacity, FS-DnsOverloadRefusesExcess, FS-DnsOverloadMemoryBounded) pass on a node with 2 cores and 2 GB running the fixed code with the query mix of this Background, measured under that overload because refusing queries also uses CPU, and recorded in the technical spec.
  # Derived: 5 seconds matches the configuration propagation bound of cluster-config-sync.feature. The burst allowance (see FS-DnsOverloadNoRefusalBelowCapacity) holds 2,000 queries; at 3,000 queries per second against a capacity of 2,000 it drains by 1,000 per second and lasts 2 seconds, during which the node answers every query (6,000 instead of 4,000). So the first 2 seconds are excluded from the average, and the total over the minute is at most 60 × 2,000 + 2,000 = 122,000. The 2 % above 2,000 is for the jitter of measuring per second on the client side.
  @fsid:FS-DnsOverloadCapacityConfigurable
  Scenario: A cluster has a default DNS capacity that the administrator can change
    Given a new cluster where the administrator has not set a DNS capacity
    When the administrator reads the DNS settings on any node
    Then the DNS settings show the default DNS capacity
    When the administrator sets the DNS capacity to 2,000 queries per second
    Then within 5 seconds the DNS settings of every node show a DNS capacity of 2,000 queries per second
    When clients send 3,000 queries per second to one node for 1 minute
    Then from the third second of the run, the node answers between 1,800 and 2,040 queries per second, averaged over the rest of the minute
    And over the whole minute the node answers at most 122,000 queries (60 seconds at capacity plus the burst allowance)

  # Decided (UoR, 2026-11-27): overloaded nodes are loaded at 2 × C. At 2 × C the burst allowance drains by C per second and lasts 1 second, so the first second of a run is excluded.
  # Decided (UoR, 2026-11-27): above capacity, the node answers at least 90 % of C per second and at least 98 % of the queries sent receive an answer or a REFUSED response; below capacity, less than 0.1 % of the queries sent receive no response.
  # Derived: from T2. The fixed-rate curve (cached names, follower) lost 0.15 % at 5,000 qps and 1.86 % at 10,000 qps; the maximum-rate table, which has cached, blocked and cache-miss rows, lost 4.7–14.9 %.
  # Decided (UoR, 2026-11-27): during overload, p99 of the answers stays below 50 ms and p99.9 of all responses (answers and refusals) below 1 second.
  # Derived: from T2. The fixed-rate curve (cached names, follower) gave p99 23.6 ms at 5,000 qps and 75.8 ms at 10,000 qps; the maximum-rate table gave 56–61 ms on the follower for cached and blocked names and 164 ms for cache misses. The leader rows (up to 233 ms, 117 ms for blocked names) are not used because most of them ran on a follower after the first kill moved the leadership. The figures above 5,000 qps include the backlog this feature removes; 50 ms leaves room for the 20 % of cache misses and the 10 ms upstream of this Background.
  @fsid:FS-DnsOverloadAnswersUpToCapacity
  Scenario: An overloaded node keeps answering up to its capacity
    Given the DNS capacity of each node is C
    When clients send 2 × C queries per second to one node for 15 minutes
    Then in every minute of the run, excluding its first second, the node answers at least 90 % of C queries per second
    And the 99th percentile latency of the answers stays below 50 ms

  # Decided (UoR, 2026-11-27): a query above the capacity receives a REFUSED response, the same response a node that is not ready gives (FS-DnsStartupRefusesUntilReady), so the client's resolver moves to its next configured server at once.
  # Decided (UoR, 2026-11-27): clients are expected to fall back to another node; the administrator gives clients at least two nodes, and skoed documents it.
  @fsid:FS-DnsOverloadRefusesExcess
  Scenario: Queries above the capacity receive a REFUSED response
    Given the DNS capacity of each node is C
    When clients send 2 × C queries per second to one node for 15 minutes
    Then at least 98 % of the queries sent receive an answer or a REFUSED response
    And every response that is not an answer is a REFUSED response
    And the 99.9th percentile response time stays below 1 second

  # Derived: at 2 × C about half of the queries are refused, so 1,000 queries make at least one NXDOMAIN certain in practice; 100 queries per second is small next to C.
  @fsid:FS-DnsOverloadFilteringKept
  Scenario: Blocked names stay blocked during overload
    Given the DNS capacity of each node is C
    And clients send 2 × C queries per second to one node
    When a client sends 1,000 A queries for "tracker.example.com" to that node over 10 seconds
    Then at least one of them receives a NXDOMAIN response
    And every response is either NXDOMAIN or REFUSED
    And no response contains an address for "tracker.example.com"

  # Decided (UoR, 2026-11-27): the memory of an overloaded node grows by at most 200 MB over its level with no DNS load.
  # Derived: about the 160 MB of heap in use measured at 10,000 qps in Findings 1, plus a margin.
  @fsid:FS-DnsOverloadMemoryBounded
  Scenario: An overloaded leader stays within bounded memory and is not killed
    Given the DNS capacity of each node is C
    And the memory used by the leader with its lists loaded and no DNS load is recorded
    When clients send 2 × C queries per second to the leader for 15 minutes
    Then the memory used by the leader grows by at most 200 MB over that level
    And the leader is never killed for lack of memory
    And the leader stays the leader and no leader election takes place during the run

  # Decided (UoR, 2026-11-27): from 5 seconds after the load drops, the node refuses no query, and within 5 minutes its memory is back within 50 MB of its level before the load.
  # Risk: the memory going back down depends on the Go runtime returning freed memory to the system; in Findings 1 the node still held 458 MB against 52 MB before the load 120 s after the load stopped, when the backlog had just drained.
  @fsid:FS-DnsOverloadRecoversWhenLoadDrops
  Scenario: A node recovers as soon as the load goes below its capacity
    Given the DNS capacity of each node is C
    And the memory used by the node with its lists loaded and no DNS load is recorded
    And clients have sent 2 × C queries per second to one node for 5 minutes
    When the load drops to 25 % of C queries per second
    Then from 5 seconds until 5 minutes after the drop, no query receives a REFUSED response
    And less than 0.1 % of the queries sent in that time receive no response
    And within 5 minutes the memory used by the node is back within 50 MB of its level before the load

  # Decided (UoR, 2026-11-27): the DNS capacity is counted per second, with a burst allowance of one second's worth of queries, so a node under its capacity on average never refuses.
  # Derived: 75 % of C leaves a margin for the jitter of an even-rate load generator.
  @fsid:FS-DnsOverloadNoRefusalBelowCapacity
  Scenario: A node below its capacity refuses no query
    Given the DNS capacity of each node is C
    And every name queried receives an answer other than REFUSED when the node has no load
    When clients send 75 % of C queries per second to one node for 15 minutes
    Then no query receives a REFUSED response
    And less than 0.1 % of the queries sent receive no response

  # Decided (UoR, 2026-11-27): the metrics endpoint shows a count of refused queries per node, and the node logs a line at most once per minute while it refuses queries.
  @fsid:FS-DnsOverloadVisibleToAdministrator
  Scenario: The administrator sees how many queries a node refused
    Given the DNS capacity of each node is C
    And clients sent 2 × C queries per second to one node for 1 minute
    When the administrator reads the metrics of that node
    Then the metrics show a count of the queries the node refused because it was overloaded
    And the increase of that count during the run is at least the number of REFUSED responses the clients received
    And it exceeds that number by at most 2 % of the queries sent, the share of responses FS-DnsOverloadRefusesExcess allows to be lost on the way back
    And the node's log, read 70 seconds after the run started, has at least one line, and at most one per 60-second interval, saying it refused queries and how many
