# In-Place Upgrade

skoed supports upgrading a running node without losing configuration. The procedure differs slightly depending on how you installed skoed, but in all cases the existing config and data are preserved.

## Debian / Ubuntu (`.deb` package)

```bash
sudo apt install ./skoed_<new-version>_<arch>.deb
```

The package manager stops the old service, replaces the binary, and starts the new version automatically via systemd. Your `/etc/skoed/config.yaml` is never touched by the package — it is preserved across upgrades.

Verify the upgrade:

```bash
skoed version
systemctl status skoed
```

## Docker / Docker Compose

**Docker Compose:**

```bash
docker pull ghcr.io/ashmonger/skoed:<new-tag>
docker compose up -d
```

Compose replaces the running container with one based on the new image. Volumes (your data directory and config file) are preserved.

**Plain Docker:**

```bash
docker stop skoed
docker rm skoed
docker run -d \
  --name skoed \
  -v /etc/skoed:/etc/skoed:ro \
  -v /var/lib/skoed:/var/lib/skoed \
  -p 53:53/udp -p 8080:8080 \
  ghcr.io/ashmonger/skoed:<new-tag>
```

## Binary (tarball)

1. Stop the service:

   ```bash
   sudo systemctl stop skoed
   ```

2. Replace the binary:

   ```bash
   sudo cp skoed /usr/bin/skoed
   sudo chmod 755 /usr/bin/skoed
   ```

3. Start the service:

   ```bash
   sudo systemctl start skoed
   ```

Config at `/etc/skoed/config.yaml` and data at `/var/lib/skoed/` are untouched.

## Cluster rolling upgrade

Upgrade one node at a time to keep the cluster serving DNS throughout. Always upgrade **followers first**, then the **leader last**.

**Recommended order:**

1. Identify the current leader:

   ```bash
   skoed status --api http://<any-node>:8080
   ```

2. Upgrade each follower node using the appropriate method above. Wait for the node to rejoin and reach `follower` state before proceeding to the next one.

3. Upgrade the leader node last. Before the leader binary restarts, it transfers leadership to a healthy follower. The transfer takes less than one second. DNS queries are handled by the new leader immediately.

**Expected downtime per node:** less than 1 second during leader transfer. Followers experience zero downtime.

## Downgrade

Downgrading to an earlier release within the **same major version** is supported:

1. Stop skoed.
2. Restore the previous binary.
3. Start skoed.

Before downgrading across minor versions, check the `CHANGELOG` for database schema changes. If a schema migration was applied by the newer version, the older binary may refuse to start or silently misread data. When in doubt, restore from a backup taken before the upgrade rather than rolling back the binary alone.

## Upgrade availability banner

The skoed web UI displays a banner when a newer release is available. skoed checks the GitHub Releases API periodically and compares the latest published tag against the running version. The check is read-only and outbound — no data is sent. The banner links directly to the release notes for the newer version.

To disable the check, set `update_check: false` in `config.yaml` (under `node`).
