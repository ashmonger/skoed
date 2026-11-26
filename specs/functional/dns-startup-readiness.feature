Feature: DNS startup readiness
  As a network administrator running skoed as the DNS server of my network
  I want a node to answer DNS queries only once its blocklists, allowlists
  and local DNS entries are loaded
  So that a client never resolves a blocked name because the node that
  answered it had just started.

  The 5-node performance run of 2026-11-26 (demos/perf-5node-lxc/REPORT.md,
  T8 and Findings 2) shows the current behavior: a wiped node that rejoined
  answered its first query at 1.05 s and blocked only from 4.10 s, and
  answered a blocked name NOERROR in between. The same happens after every
  restart, including one that follows a kill for lack of memory.

  FS-BlocklistContentSurvivesRestartOffline
  (blocklist-content-distribution.feature) still holds.

  Draft awaiting UoR validation. Lines starting with "# DECISION:" mark
  choices the UoR has not made yet; each scenario below them is written
  with the recommended option.

  Non-goals:
    - Reducing the time a node takes to load its lists
    - Changing what a running node answers while it refreshes a list (it
      keeps the previous version until the new one is loaded)
    - Delaying the start of skoed until the host network is configured
    - Readiness of the management API, the web UI and the DHCP server
    - Sending queries that arrive during startup to another node

  Background:
    Given a 3-node skoed cluster
    And a blocklist "ads" containing "tracker.example.com" whose block policy is NXDOMAIN
    And an allowlist containing "safe.tracker.example.com"
    And a local DNS entry "nas.home.arpa" with address "192.168.1.10"
    And every node holds the leader's copy of "ads"

  # DECISION: What a client receives from a node that has not loaded its lists yet — options: A (recommended) a REFUSED response, the same as for an overloaded node, so its resolver moves to its next configured server at once / B no answer, the node does not listen on its DNS port until it is ready / C a SERVFAIL response
  @fsid:FS-DnsStartupRefusesUntilReady
  Scenario: A starting node refuses queries until its lists are loaded
    When a follower restarts
    And a client sends an A query for "tracker.example.com" to that follower every 50 ms
    Then every query sent before the follower has loaded "ads" receives a REFUSED response
    And every query sent after it receives a NXDOMAIN response
    And no query receives a NOERROR response

  @fsid:FS-DnsStartupFilteredAfterKill
  Scenario: A node killed and restarted by its service manager never answers unfiltered
    When the skoed process of a follower is killed and the service manager restarts it
    And a client sends an A query for "tracker.example.com" to that follower every 50 ms
    Then no query receives a NOERROR response
    And within 30 seconds of the restart the queries receive a NXDOMAIN response

  @fsid:FS-DnsStartupFilteredAfterRejoin
  Scenario: A node that rejoins the cluster with no data never answers unfiltered
    Given the blocklists of the cluster hold 2,800,000 domains
    When a node with no data joins the cluster with a new join token
    And a client sends an A query for "tracker.example.com" to that node every 50 ms
    Then no query receives a NOERROR response
    And within 30 seconds of joining the queries receive a NXDOMAIN response

  @fsid:FS-DnsStartupAllRulesApplyFromFirstAnswer
  Scenario: Allowlists and local DNS entries apply from the first answer
    When a follower restarts
    And clients send queries for "safe.tracker.example.com" and "nas.home.arpa" to that follower every 50 ms
    Then the first response other than REFUSED for "safe.tracker.example.com" is the upstream answer
    And the first response other than REFUSED for "nas.home.arpa" contains the address "192.168.1.10"
    And no query for "nas.home.arpa" is forwarded to an upstream resolver

  # DECISION: What a node does when it cannot load one of its lists at startup — options: A (recommended) after 60 seconds it answers with the lists it could load and reports the missing list as not loaded / B it refuses queries until every list is loaded, however long that takes / C it answers at once with the lists it could load
  @fsid:FS-DnsStartupMissingListDoesNotBlockForever
  Scenario: A list that cannot be loaded does not keep the node refusing forever
    Given a blocklist "extra" that a follower holds no copy of
    And neither the URL of "extra" nor the leader is reachable from that follower
    When that follower restarts
    Then within 60 seconds of the restart the follower answers queries
    And queries for "tracker.example.com" receive a NXDOMAIN response
    And the follower reports the blocklist "extra" as not loaded

  # DECISION: How the administrator sees that a node is not ready yet — options: A (recommended) the cluster status shows the node as "starting" until its lists are loaded, and the node logs when it starts answering / B a log line only / C A plus a readiness state in the metrics endpoint
  @fsid:FS-DnsStartupStateVisible
  Scenario: The cluster status shows a node that is not ready yet
    When a follower restarts
    And the administrator requests the cluster status before that follower has loaded "ads"
    Then the entry of that follower has DNS state "starting"
    And once the follower has loaded "ads" its entry has DNS state "ready"
