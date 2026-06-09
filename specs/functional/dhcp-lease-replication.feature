Feature: Raft-replicated DHCP lease cache
  As an operator running a multi-node skoed cluster pointed at a single DHCP source
  I want only the leader to poll the source and replicate the lease set through Raft
  So that the DHCP server is not woken N times per refresh interval
  And every node serves the same canonical lease view (no per-node drift)
  And a leader failover does not produce a cold-start window or a double-poll burst.

  Background:
    Given a 3-node skoed cluster (one leader, two followers)
    And all nodes have identical DHCP connector configuration pointing at the same source
    And the cluster has reached steady state (all nodes at the same Raft commit index)

  @fsid:FS-LeaseReplOnlyLeaderPolls
  Scenario: Only the leader polls the configured DHCP source
    Given the DHCP source records every inbound request and groups them by caller
    When the cluster has been running for 5 refresh intervals
    Then the source has been contacted by exactly one skoed node — the current leader
    And the two follower nodes have produced zero inbound requests to the source

  @fsid:FS-LeaseReplFollowersServeReplicatedSnapshot
  Scenario: Followers serve the same /api/v1/clients view as the leader
    Given the leader has just observed three leases for 10.42.0.10, 10.42.0.11, 10.42.0.12
    When the admin calls GET /api/v1/clients on each of the three nodes
    Then every node returns the same three client records (same IPs, MACs, hostnames, client-ids)
    And each response carries an "x-leader-node-id" header naming the current leader

  @fsid:FS-LeaseReplLeasesEndpointExposesSnapshot
  Scenario: GET /api/v1/leases returns the replicated lease snapshot
    When the admin calls GET /api/v1/leases on any node
    Then the response is 200
    And the body has shape {"leases":[...], "source":{"connector_kind":"...", "leader_node_id":"...", "last_poll_unix":N}}
    And every lease record carries ip, mac, hostname, client_id, source, expires_at
    And the body is byte-identical (modulo response timing fields) across all three nodes

  @fsid:FS-LeaseReplSourceEndpointReportsLeader
  Scenario: GET /api/v1/leases/source reports which node owns the poll loop
    When the admin calls GET /api/v1/leases/source on a follower
    Then the response is 200
    And the body has shape {"connector_kind":"kea|dnsmasq|http_json","last_poll_unix":N,"source_url":"...","leader_node_id":"..."}
    And the reported leader_node_id matches the current Raft leader

  @fsid:FS-LeaseReplLeaderFailoverResumesPolling
  Scenario: The newly-elected leader resumes polling without a cold start
    Given the leader has been polling for 10 minutes and the replicated snapshot has 200 leases
    When the leader process is killed and a follower is elected within 10 seconds
    Then within one refresh interval after election the new leader has produced a poll
    And the replicated snapshot still contains the prior 200 leases (no transient empty state)
    And the deposed leader, when restarted as a follower, makes zero new requests to the source

  @fsid:FS-LeaseReplNoDoublePollDuringTransition
  Scenario: At most one node polls the source even across a leadership change
    Given the leader has been killed and a new leader has been elected
    When the source records inbound requests over the next 60 seconds
    Then no two skoed nodes have polled the source in overlapping windows
    And the former leader (if restarted) does not resume polling unless it is re-elected

  @fsid:FS-LeaseReplEmptyClusterReturns503
  Scenario: No leader yet at boot — lease endpoints surface a clear retryable error
    Given the cluster is bootstrapping and no leader has been elected yet
    When the admin calls GET /api/v1/leases on any node
    Then the response is 503
    And the body has shape {"error":"no leader","retry_after_seconds":N}
    And the response carries a Retry-After header
    And the response is NOT a stale empty list (no 200 with leases:[])

  @fsid:FS-LeaseReplFollowerAnomaliesMatchLeader
  Scenario: Anti-spoof anomalies surface on followers via replicated state
    Given the leader's most recent poll produced one anomaly of kind "mac_changed_for_client_id"
    When the admin calls GET /api/v1/clients/anomalies on a follower
    Then the same anomaly record appears in the follower's response
    And the anomaly id, kind, detected_at, ip, mac, and client_id fields match the leader byte-for-byte

  @fsid:FS-LeaseReplFollowerWriteForwarded
  Scenario: Acknowledging an anomaly on a follower is forwarded to the leader
    Given an unresolved anomaly id "ANOM-001" exists in the replicated state
    When the admin POSTs /api/v1/clients/anomalies/ANOM-001/acknowledge to a follower
    Then the response is 200
    And within 5 seconds GET /api/v1/clients/anomalies on every node shows acknowledged_at set

  @fsid:FS-LeaseReplChurnDoesNotAmplifyRaftLog
  Scenario: A high-churn lease source does not produce one Raft entry per lease per poll
    Given the configured refresh interval is 60 seconds
    And the source has 1000 leases of which 5 change between polls
    When the cluster has been running for 10 refresh intervals
    Then the number of Raft log entries appended for DHCP state is bounded
      (snapshot-style replace OR delta coalescing — design decision lives in the TSID,
       observable invariant is "well under 1 entry per lease per poll")
    And the cluster's Raft snapshot cadence is unaffected (no snapshot thrash)

  @fsid:FS-LeaseReplStaleFollowerCatchesUp
  Scenario: A follower reconnecting after a partition catches up to the current lease snapshot
    Given a follower has been partitioned from the cluster for 5 minutes
    And during the partition the replicated lease snapshot has changed (10 leases added, 3 removed)
    When the partitioned follower reconnects
    Then within 10 seconds GET /api/v1/leases on the reconnected follower
      returns the current snapshot (matching the leader byte-for-byte)
    And the reconnected follower does NOT itself poll the DHCP source

  @fsid:FS-LeaseReplLastPollUnixAdvances
  Scenario: last_poll_unix reflects the leader's most recent successful poll
    Given the leader has just completed a successful poll at time T
    When the admin calls GET /api/v1/leases/source on any node within the next refresh interval
    Then the reported last_poll_unix equals T (within one second)
    And the value is identical across leader and followers (no per-node clock drift in the reported field)

  @fsid:FS-LeaseReplSourceUnreachableKeepsLastGood
  Scenario: A transient DHCP source failure keeps the last known-good snapshot
    Given the leader's last successful poll produced 200 leases
    When the DHCP source returns errors for the next 3 refresh intervals
    Then GET /api/v1/leases on every node continues to return the prior 200 leases
    And last_poll_unix does NOT advance during the failure window
    And a structured-log event "dhcp_poll_failed" is emitted on the leader (not on followers)

  Non-goals:
    - Per-node connector configuration drift (every node MUST be configured with the
      same connector; mismatched configs are an operator error, not a cluster contract).
    - Replicating raw connector wire payloads (only the canonical Lease records flow
      through Raft; connector-specific quirks stay on the leader).
    - Replicating lease-write operations back to the DHCP server (skoed is read-only
      with respect to DHCP — see dhcp-connectors.feature non-goals).
    - DHCPv6 lease replication (covered by dhcpv6-lease-parsing.feature; the
      replication mechanism here is connector-agnostic but the v6 fields land in M6.5
      under their own FSIDs).
    - Sub-second propagation guarantees from leader to followers (Raft commit latency
      applies; the contract is "within one refresh interval after the leader poll").
    - Per-node lease views that intentionally differ (e.g. "show me only the leases
      this node has seen DNS traffic from") — out of scope for M6.5.
