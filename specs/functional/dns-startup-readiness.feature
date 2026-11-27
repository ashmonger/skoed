Feature: DNS startup readiness
  As a network administrator running skoed as the DNS server of my network
  I want a node to answer DNS queries only once its blocklists, allowlists
  and local DNS entries are loaded
  So that a client never resolves a blocked name because the node that
  answered it had just started.

  The 5-node performance run of 2026-11-26 (demos/perf-5node-lxc/REPORT.md,
  T8 and Findings 2) shows the current behavior: a wiped node that rejoined
  answered its first query at 1.05 s and blocked only from 4.10 s, and
  answered a blocked name NOERROR in between. Findings 2 reports the same
  after every restart that followed a kill for lack of memory. A plain
  service restart (T7) blocked at 2.3 s and answered at 2.4 s, so it did
  not show the issue in that run; this feature covers it as well.

  FS-BlocklistContentSurvivesRestartOffline
  (blocklist-content-distribution.feature) still holds.

  Decisions validated by the UoR on 2026-11-27; specification pending
  final UoR validation. Lines starting with "# Decided" record those
  decisions; lines starting with "# Derived:" say where a number comes
  from.

  # Derived: a query every 50 ms gives 20 samples per second, enough to see
  # the 1.05 s to 4.10 s window of T8. The 30-second bound matches
  # FS-BlocklistContentNewNodeCopiesOnJoin; T8 measured 4.10 s after a
  # rejoin and 9.0 s after a container reboot.

  Non-goals:
    - Reducing the time a node takes to load its lists
    - Changing what a running node answers while it refreshes a list (it
      keeps the previous version until the new one is loaded)
    - Delaying the start of skoed until the host network is configured
    - Readiness of the management API, the web UI and the DHCP server
    - Sending queries that arrive during startup to another node
    - What a node joining through the web UI answers before the join: it
      first runs as a standalone node (cluster-join-webui.feature)

  Background:
    Given a 3-node skoed cluster
    And a blocklist "ads" containing "tracker.example.com" whose block policy is NXDOMAIN
    And an allowlist containing "safe.tracker.example.com"
    And a local DNS entry "nas.home.arpa" with address "192.168.1.10"
    And every node holds the leader's copy of "ads"
    And every client sending queries is inside the trusted subnets of the nodes
    And the upstream resolver answers "safe.tracker.example.com" with the address "203.0.113.20"

  # Decided (UoR, 2026-11-27): a node that has not loaded its lists yet sends a REFUSED response, the same response as for a query above the capacity (FS-DnsOverloadRefusesExcess), so the client's resolver moves to its next configured server at once.
  @fsid:FS-DnsStartupRefusesUntilReady
  Scenario: A starting node refuses queries until its lists are loaded
    Given a client sends an A query for "tracker.example.com" to a follower every 50 ms
    When that follower is stopped and started again
    Then from the moment the stop has completed (the skoed process has exited), the responses are zero or more REFUSED responses followed only by NXDOMAIN responses
    And queries sent while the skoed process is down may receive no response
    And no query receives a NOERROR response
    And within 30 seconds of the start the queries receive a NXDOMAIN response

  @fsid:FS-DnsStartupFilteredAfterKill
  Scenario: A node killed and restarted by its service manager never answers unfiltered
    Given a client sends an A query for "tracker.example.com" to a follower every 50 ms
    When the skoed process of that follower is killed and the service manager restarts it
    Then from the moment the process is killed, no query receives a NOERROR response
    And within 30 seconds of the restart the queries receive a NXDOMAIN response

  @fsid:FS-DnsStartupFilteredAfterRejoin
  Scenario: A node that rejoins the cluster with no data never answers unfiltered
    Given each node has 2 CPU cores and 2 GB of memory, as in T8
    And the blocklists of the cluster hold 2,800,000 domains
    And a client sends an A query for "tracker.example.com" every 50 ms to the address of a node with no data
    When the skoed service of that node is started with a new join token in its configuration, as in T8
    And that node joins the cluster
    Then the responses are zero or more REFUSED responses or no response, followed only by NXDOMAIN responses
    And no query receives a NOERROR response
    And within 30 seconds of joining the queries receive a NXDOMAIN response

  @fsid:FS-DnsStartupAllRulesApplyFromFirstAnswer
  Scenario: Allowlists and local DNS entries apply from the first answer
    Given clients send queries for "safe.tracker.example.com" and "nas.home.arpa" to a follower every 50 ms
    When that follower is stopped and started again
    Then from the moment the stop has completed (the skoed process has exited), the first response other than REFUSED for "safe.tracker.example.com" contains the address "203.0.113.20"
    And the first response other than REFUSED for "nas.home.arpa" contains the address "192.168.1.10"
    And no query for "nas.home.arpa" is forwarded to an upstream resolver
    And within 30 seconds of the start both names receive a response other than REFUSED

  # Decided (UoR, 2026-11-27): a node that cannot load one of its lists waits at most 60 seconds for that list, then answers with the lists it could load, shows DNS state "ready" and reports the missing list as not loaded.
  # Derived: 90 seconds is the 60-second wait plus the 30-second bound of this feature.
  @fsid:FS-DnsStartupMissingListDoesNotBlockForever
  Scenario: A list that cannot be loaded does not keep the node refusing forever
    Given a blocklist "extra" that a follower holds no copy of
    And neither the URL of "extra" nor the leader is reachable from that follower
    When that follower restarts
    Then within 90 seconds of the restart the follower answers queries
    And queries for "tracker.example.com" receive a NXDOMAIN response
    And the blocklists listed by that follower show "extra" as not loaded
    And the cluster status read on that follower shows it with DNS state "ready"

  # Decided (UoR, 2026-11-27): a restart of the whole cluster at once is covered; a node that holds a copy of every list loads it from disk and answers without the leader and without internet access, so after a power cut every node refuses only while it loads its lists.
  @fsid:FS-DnsStartupWholeClusterRestart
  Scenario: Every node restarting at once with no internet access answers from its copies
    Given no node can reach the internet
    And a client sends an A query for "tracker.example.com" to each node every 50 ms
    When every node of the cluster restarts at the same time
    Then no query receives a NOERROR response
    And within 30 seconds of its restart each node answers the queries with a NXDOMAIN response, whether or not a leader has been elected

  # Decided (UoR, 2026-11-27): the cluster status shows the node with DNS state "starting" until it answers queries, then "ready", and the node logs when it starts answering.
  # Derived: the status is read 10 seconds after the restart, before the 60-second wait for "extra" ends; 5 seconds matches the bounds of cluster-config-sync.feature.
  @fsid:FS-DnsStartupStateVisible
  Scenario: The cluster status shows a node that is not ready yet
    Given a blocklist "extra" that a follower holds no copy of
    And neither the URL of "extra" nor the leader is reachable from that follower
    And a client sends an A query for "tracker.example.com" to that follower every 50 ms
    When that follower restarts
    And the administrator requests the cluster status on that follower 10 seconds after the restart
    Then the entry of that follower has DNS state "starting"
    And the queries sent at that time receive a REFUSED response
    And once the follower answers "tracker.example.com" with NXDOMAIN, its entry has DNS state "ready" within 5 seconds
