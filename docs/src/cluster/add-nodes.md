# Add or Remove Nodes

Expand a running skoed cluster by joining new nodes, or shrink it by gracefully removing members.

---

## Prerequisites

- An existing cluster with at least 1 healthy node (a leader must be reachable)
- The API of the current leader accessible from your shell
- An admin API token (set `SKOED_TOKEN` for the examples below)

```bash
export SKOED_LEADER="http://node-1:8080"
export SKOED_TOKEN="<your-admin-token>"
```

---

## Add a Node

### Step 1 — Generate a join token

Ask the leader to issue a one-time join token. The token expires after 15 minutes by default.

```bash
curl -s -X POST "${SKOED_LEADER}/api/v1/cluster/tokens" \
  -H "Authorization: Bearer ${SKOED_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"ttl": "15m"}' | jq .
```

Example response:

```json
{
  "token": "jtk_a1b2c3d4e5f6...",
  "expires_at": "2026-06-10T12:30:00Z"
}
```

Copy the `token` value.

### Step 2 — Start the new node with bootstrap config

Create a `config.yaml` for the new node that references the leader address and the join token:

```yaml
# config.yaml (new node)
node:
  id: "node-4"
  raft_advertise: "node-4:9000"

dns:
  listen: "0.0.0.0:53"

api:
  listen: "0.0.0.0:8080"

cluster:
  raft_listen: "0.0.0.0:9000"
  bootstrap:
    leader_address: "node-1:9000"
    token: "jtk_a1b2c3d4e5f6..."

upstream:
  - "1.1.1.1:53"
```

Start the node. It will contact the leader, authenticate with the token, and begin log replication:

```bash
# Docker example
docker run -d \
  --name skoed-node-4 \
  -p 53:53/udp -p 53:53/tcp \
  -p 8080:8080/tcp \
  -p 9000:9000/tcp \
  -v skoed_data_4:/var/lib/skoed \
  -v $(pwd)/config.yaml:/etc/skoed/config.yaml:ro \
  ghcr.io/ashmonger/skoed:latest
```

### Step 3 — Verify membership

Query the cluster status from any node:

```bash
curl -s "${SKOED_LEADER}/api/v1/cluster/status" \
  -H "Authorization: Bearer ${SKOED_TOKEN}" | jq .
```

Example response after node-4 joins:

```json
{
  "leader": "node-1",
  "term": 4,
  "members": [
    { "id": "node-1", "address": "node-1:9000", "state": "leader",   "last_contact_ms": 0   },
    { "id": "node-2", "address": "node-2:9000", "state": "follower", "last_contact_ms": 12  },
    { "id": "node-3", "address": "node-3:9000", "state": "follower", "last_contact_ms": 8   },
    { "id": "node-4", "address": "node-4:9000", "state": "follower", "last_contact_ms": 34  }
  ]
}
```

`last_contact_ms` should drop to single digits within a few seconds as the new node catches up with the log.

---

## Remove a Node

### Graceful removal

Send a `DELETE` to the leader to remove a member from the Raft configuration:

```bash
curl -s -X DELETE "${SKOED_LEADER}/api/v1/cluster/members/node-4" \
  -H "Authorization: Bearer ${SKOED_TOKEN}" | jq .
```

The leader removes the node from the quorum and the node stops receiving log entries. You can then stop and delete the container or VM.

### Quorum safety rule

Raft requires a majority of nodes to be alive to commit entries. Before removing a node, confirm the remaining cluster will still have quorum:

| Cluster size before | Minimum nodes after removal | Safe to remove? |
|--------------------|-----------------------------|-----------------|
| 3 | 2 | Yes |
| 3 | 1 | **No** — quorum lost |
| 5 | 3 | Yes |
| 5 | 2 | **No** — quorum lost |

Do not remove a node if it would leave fewer than `⌊n/2⌋ + 1` members healthy.

---

## Replace a Failed Node

If a node's data directory is lost (disk failure, accidental deletion), re-add it using the same steps as a new node. You may reuse the old `node.id` — the cluster treats it as a fresh member and replicates the full log to it.

1. Generate a join token from the leader (Step 1 above).
2. Start a new instance with the original `node.id` and a **fresh, empty** data directory.
3. Use the `bootstrap.leader_address` + `bootstrap.token` config (Step 2 above).
4. Verify membership (Step 3 above).

The failed node does not need to be explicitly removed before re-adding; the leader will replace the stale entry when the new node joins with the same ID.
