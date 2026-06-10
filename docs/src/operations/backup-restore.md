# Backup and Restore

skoed stores all persistent state in two locations. Back up both to be able to fully restore a node.

| Path | Contents |
|------|----------|
| `/var/lib/skoed/` | bbolt database files — blocklists, allowlists, local DNS entries, profiles, DHCP leases, cluster state |
| `/etc/skoed/config.yaml` | Node configuration (listen addresses, upstream resolvers, TLS settings, etc.) |

## Config export and import

skoed provides an API-level export that captures all application-layer configuration in a portable archive.

**Export:**

```http
GET /api/v1/config/export
```

Returns a `tar.gz` archive containing all blocklists, allowlists, local DNS entries, profiles, and settings. The archive does not contain secrets such as API tokens or TLS private keys.

```bash
curl -H "Authorization: Bearer <token>" \
     http://localhost:8080/api/v1/config/export \
     -o skoed-config-$(date +%Y%m%d).tar.gz
```

**Import (on a fresh node):**

```http
POST /api/v1/config/import
Content-Type: multipart/form-data
```

```bash
curl -H "Authorization: Bearer <token>" \
     -F "file=@skoed-config-20260609.tar.gz" \
     http://localhost:8080/api/v1/config/import
```

The import is applied atomically. If any part of the archive fails to parse, the entire import is rejected and the node state is unchanged.

## Cold backup

A cold backup copies the database files while skoed is not running, guaranteeing a consistent on-disk state.

```bash
sudo systemctl stop skoed
sudo cp -a /var/lib/skoed /backup/skoed-$(date +%Y%m%d)
sudo cp /etc/skoed/config.yaml /backup/skoed-$(date +%Y%m%d)/
sudo systemctl start skoed
```

The node is offline for the duration of the copy. On typical hardware with a few hundred thousand blocked domains, this takes under a second.

## Hot backup

bbolt supports online consistent snapshots. If the debug endpoint is enabled in your config, you can stream a snapshot without stopping skoed:

```http
GET /api/v1/debug/backup
```

```bash
curl -H "Authorization: Bearer <token>" \
     http://localhost:8080/api/v1/debug/backup \
     -o skoed-hot-$(date +%Y%m%d-%H%M%S).db
```

The response is a raw bbolt database file. skoed continues serving DNS while the snapshot streams. The snapshot is consistent — it reflects a single transaction boundary.

> **Note:** The debug backup endpoint must be explicitly enabled. See `node.debug.enabled` in `config.yaml`. Do not expose it on a public interface.

## Restore

1. Stop skoed:

   ```bash
   sudo systemctl stop skoed
   ```

2. Replace the data directory with your backup:

   ```bash
   sudo rm -rf /var/lib/skoed
   sudo cp -a /backup/skoed-20260609 /var/lib/skoed
   sudo chown -R skoed:skoed /var/lib/skoed
   ```

3. Restore config if needed:

   ```bash
   sudo cp /backup/skoed-20260609/config.yaml /etc/skoed/config.yaml
   ```

4. Start skoed:

   ```bash
   sudo systemctl start skoed
   ```

## Cluster restore

Restoring a full cluster from backup requires rebuilding from a single authoritative node.

1. Pick one node to become the seed. Restore it using the single-node procedure above.
2. Start the restored node in single-node mode (remove `bootstrap.leader_address` from its config, or point `bootstrap` at itself).
3. The restored node starts as the new cluster leader with the backed-up state.
4. Issue new join tokens:

   ```http
   POST /api/v1/cluster/tokens
   ```

5. Re-join all other nodes against the restored leader using their fresh join tokens. They will receive the full replicated state via Raft snapshot replication — their previous local data is replaced.

> **Warning:** Re-joining a node discards its current local data in favour of the leader's state. Only do this intentionally during a restore operation.
