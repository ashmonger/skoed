# Demo note — mTLS across hosts (2026-11-26)

Three Debian 13 containers on Proxmox, on separate LAN addresses:
demo1 192.168.1.179, demo2 192.168.1.192, demo3 192.168.1.155. Each runs
the `feature/mtls-cross-host` build (f45bca2) with
`node.cluster.mtls.enabled: true`, Raft on `<ip>:7000`.

## Implemented scope

* Raft dials set `ServerName` to the peer's node ID, so leaves verify
  between hosts without IP SANs.
* `ResetRaftForJoin` keeps the TLS stream layer.
* A join through the web UI fetches the leader's CA and a leaf for the
  node, installs them, removes the node's own CA key and reloads the
  certificates in the running process.

## Results

* **Join from config.** demo1 generated the CA; demo2 joined with a
  bootstrap token. Both at commit index 15, same CA fingerprint, no
  `ca.key` on demo2. `openssl s_client` with a cluster leaf on ports 7000
  and 7001 of both nodes: TLSv1.3, verify 0, `CN=demo1` / `CN=demo2`;
  without a client certificate: `tlsv13 alert certificate required`.
* **Join from the web UI.** demo3 started alone with its own CA
  (`2B:A7:40:78…`). `POST /api/v1/node/join-cluster` returned 200; within
  5 s all three nodes reported 3 members at commit index 20. demo3's
  `ca.crt` is now the cluster CA (`10:CF:32:EC…`), `node.crt` has
  `CN=demo3`, `ca.key` is gone, same PID (no restart).
* **Replication and forwarding.** Blocklists created on the leader and on
  each follower appear on all three nodes and filter DNS on all three.
  With tcp/7001 from demo2 rejected on demo1, a write to demo2 fails:

  ```
  HTTP 503 {"error":"forward to leader failed: forward to leader: Post \"https://192.168.1.179:7001/api/v1/blocklists\": ... connection reset by peer"}
  ```

  It succeeds again once the rule is removed. `X-Cluster-Secret` on the
  main API: 401 on every node, with or without a session.
* **Failover.** demo1 (leader) stopped at 09:55:55.45; demo3 won the
  election at 09:55:57.807. DNS kept answering on the two other nodes and a
  write to demo2 during the outage was forwarded to demo3. demo1 restarted
  and rejoined as follower; all three at term 3, commit index 36.
* No x509, `bad certificate` or `unknown rpc type` error from the new
  build in any journal.

## Not implemented / limitations

* A join that adds a voter which cannot be reached still leaves a 2-voter
  cluster without a leader (TODO.md).
* At first start a node logs `members without capability
  blocklist-content-v1` naming itself, once. `cluster_id` is empty in
  responses and status, and `api_address` reports `0.0.0.0:8080` (TODO.md).
* Certificate rotation still needs a restart, except for the web-UI join.
