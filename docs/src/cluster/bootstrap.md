# Bootstrap a 3-node cluster

A dblock cluster is a Raft consensus group: one leader, two or more
followers. Every replicated mutation (blocklists, profiles, settings,
password, audit log entries) goes through Raft so every node converges.

## Topology

Recommended: **3 nodes** (single-node tolerates zero failures; 3
nodes tolerate one). 5+ nodes only if you genuinely have a hostile
single-fault domain. dblock is not active-active — only the leader
accepts writes, but every node serves reads.

## Per-node config

Each node ships with `/etc/dblock/config.yaml`. Edit the `node`
section to give each a unique `id`, distinct `raft_address`, and a
shared `data_dir` (e.g. `/var/lib/dblock`).

```yaml
# /etc/dblock/config.yaml on node-1
node:
  id: dblock-1
  raft_address: 192.168.1.10:7000
  api_address:  192.168.1.10:8080
  data_dir: /var/lib/dblock
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: true
```

```yaml
# node-2 and node-3 differ only in id + addresses.
```

## Bootstrap node-1

```sh
sudo systemctl enable --now dblock
# Sets the admin password on the freshly-bootstrapped cluster.
curl -fsS -X POST http://192.168.1.10:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"<your-password>"}'
```

Confirm it's a leader:

```sh
curl -fsS -u admin:<password> http://192.168.1.10:8080/api/v1/cluster/self | jq
# { "node_id":"dblock-1", "role":"leader", "raft_term":2, "commit_index":… }
```

## Issue a join token on node-1

Tokens are single-use, expire in 15 minutes by default.

```sh
TOKEN=$(curl -fsS -u admin:<password> \
  -X POST http://192.168.1.10:8080/api/v1/cluster/tokens \
  | jq -r '.token')
echo "$TOKEN"
```

## Join node-2 and node-3

Each joining node needs its `bootstrap:` section in
`/etc/dblock/config.yaml`:

```yaml
bootstrap:
  leader_address: http://192.168.1.10:8080
  token:          <paste TOKEN here>
```

Then:

```sh
sudo systemctl restart dblock
journalctl -u dblock -n 30 --no-pager
# expect: "enrolled into cluster via http://192.168.1.10:8080 (response: …)"
```

Tokens are consumed on use; issue a fresh one on the leader for
each new joiner.

## Verify convergence

```sh
curl -fsS -u admin:<password> http://192.168.1.10:8080/api/v1/cluster/health | jq
# { "status":"ok", "mode":"cluster", "has_leader":true, "members":3,
#   "reachable_members":3, "raft_term":2, "commit_index":17 }
```

In the Web UI the Dashboard's "Cluster nodes" table shows all three
with `role` + `sync_state=in_sync`.

## Encrypted cluster mesh (optional)

For inter-node mTLS, see [Encrypted mesh](encrypted-mesh.md).
Cluster-wide flip; restart every node.

## Next

- [Add or remove nodes](add-nodes.md)
- [Configure your first blocklist](../first-run/first-blocklist.md)
