# Troubleshooting

Symptoms, causes, and diagnostic steps for common skoed problems.

---

## 1. DNS Not Resolving

**Symptoms:** Clients receive `SERVFAIL`, `NXDOMAIN`, or no response. `dig` times out.

**Diagnostic steps:**

Check that skoed is running:

```bash
systemctl status skoed
# or for Docker:
docker inspect --format '{{.State.Status}}' skoed
```

Confirm something is listening on port 53:

```bash
ss -ulnp | grep 53
# Expected: a line showing skoed (or your binary name) bound to 0.0.0.0:53 or :::53
```

Test DNS resolution directly:

```bash
dig @127.0.0.1 example.com
```

If `dig` succeeds but clients fail, the problem is routing or firewall, not skoed.

Check the upstream configuration in `config.yaml`:

```yaml
upstream:
  - "1.1.1.1:53"
  - "8.8.8.8:53"
```

Verify the upstreams are reachable from the skoed host:

```bash
dig @1.1.1.1 example.com
```

Check skoed logs for upstream timeout errors:

```bash
journalctl -u skoed -n 100 --no-pager
# or:
docker logs skoed --tail=100
```

---

## 2. Web UI Not Loading

**Symptoms:** Browser shows connection refused, timeout, or a blank page on port 8080.

**Diagnostic steps:**

Verify port 8080 is listening:

```bash
ss -tlnp | grep 8080
```

Check for bind errors in the logs (another process may already be on 8080):

```bash
journalctl -u skoed -n 50 --no-pager | grep -i "bind\|listen\|address"
```

Check firewall rules:

```bash
# iptables
iptables -L INPUT -n -v | grep 8080

# nftables
nft list ruleset | grep 8080

# ufw
ufw status | grep 8080
```

If skoed is in a container, confirm the port is published:

```bash
docker port skoed
# Expected: 8080/tcp -> 0.0.0.0:8080
```

Try `curl` from the host to isolate a browser/proxy issue:

```bash
curl -v http://127.0.0.1:8080/
```

---

## 3. Cluster Won't Form / No Leader Elected

**Symptoms:** `/api/v1/cluster/status` shows no leader, all nodes report `candidate` or `follower` state indefinitely.

**Diagnostic steps:**

Verify Raft port connectivity between nodes. Run this from each node toward each peer:

```bash
nc -vz node-2 9000
nc -vz node-3 9000
```

All connections must succeed. A `Connection refused` or timeout means a firewall rule or misconfigured `cluster.raft_listen` is blocking Raft traffic.

Check that node clocks are synchronized. Raft election timeouts are sensitive to clock skew beyond a few seconds:

```bash
timedatectl status | grep -E "synchronized|NTP"
# or:
chronyc tracking | grep "System time"
```

If clocks are drifting, enable and start NTP:

```bash
timedatectl set-ntp true
```

Check join token expiry. Tokens issued by `POST /api/v1/cluster/tokens` are valid for 15 minutes by default. If the node took longer to start, generate a new token and retry.

Review logs on each node for election or peer-dial errors:

```bash
docker logs skoed-node-1 2>&1 | grep -i "raft\|election\|peer\|dial"
```

---

## 4. Node Stuck in Follower / Not Syncing

**Symptoms:** One node's data is stale; queries against it return older block-list state. Log replication appears stopped.

**Diagnostic steps:**

Query the cluster status from each node and compare commit indexes:

```bash
for NODE in node-1:8080 node-2:8081 node-3:8082; do
  echo "=== $NODE ==="
  curl -s "http://${NODE}/api/v1/cluster/status" \
    -H "Authorization: Bearer ${SKOED_TOKEN}" | jq '{leader, term, members}'
done
```

Also inspect the `X-Raft-Commit-Index` response header, which skoed appends to every API response:

```bash
curl -si "http://node-2:8081/api/v1/cluster/status" \
  -H "Authorization: Bearer ${SKOED_TOKEN}" | grep X-Raft-Commit-Index
```

A significantly lower commit index on one node compared to the leader indicates a replication lag. Common causes:

- Network packet loss or high latency on the Raft port between the stuck node and the leader
- The node was partitioned and reconnected; it will catch up automatically once connectivity is restored
- Disk I/O saturation on the stuck node preventing the Raft log from being written

Check disk I/O on the stuck node:

```bash
iostat -xz 1 5
```

If the node never catches up after several minutes, restart it. It will replay the log from the leader on reconnect.

---

## 5. High Query Latency

**Symptoms:** DNS responses are slow (>100 ms). Users notice browsing sluggishness.

**Diagnostic steps:**

Inspect the metrics endpoint for upstream resolver latency and cache hit rate:

```bash
curl -s http://127.0.0.1:8080/api/v1/metrics \
  -H "Authorization: Bearer ${SKOED_TOKEN}" | jq '{
  upstream_latency_p99_ms: .dns.upstream_latency_p99_ms,
  cache_hit_rate: .dns.cache_hit_rate,
  queries_per_second: .dns.queries_per_second
}'
```

If `upstream_latency_p99_ms` is high, the configured upstream resolvers are slow from this host. Switch to geographically closer resolvers or add a local unbound/resolved as a forwarder.

If `cache_hit_rate` is low (below 0.6 on a typical home network), the TTL override may be too aggressive or the cache size too small. Adjust in `config.yaml`:

```yaml
cache:
  max_entries: 10000
  ttl_override: 300   # seconds; set 0 to respect original TTL
```

Check whether the node is the Raft leader. Writes to the block-list are forwarded to the leader; if you are querying a follower for DNS, resolution itself is local and should not be affected by leadership. Confirm with `GET /api/v1/cluster/status`.

---

## 6. Package Install Fails on systemd

**Symptoms:** `apt install` / `dnf install` / `systemctl enable skoed` errors; service fails to start on port 53.

**Diagnostic steps:**

Confirm systemd is PID 1:

```bash
pidof systemd
# Should print: 1
```

If this returns nothing or a non-1 PID, the host is not running systemd (e.g., a minimal container). Use the Docker install instead.

Port 53 binding requires `CAP_NET_BIND_SERVICE` for non-root processes. Verify the capability is set on the binary:

```bash
getcap $(which skoed)
# Expected: /usr/bin/skoed cap_net_bind_service=+ep
```

If the capability is missing, set it:

```bash
sudo setcap cap_net_bind_service=+ep $(which skoed)
sudo systemctl restart skoed
```

Also check whether `systemd-resolved` is occupying port 53:

```bash
ss -ulnp | grep 53
# If systemd-resolved appears, disable its stub listener:
```

```bash
sudo sed -i 's/#DNSStubListener=yes/DNSStubListener=no/' /etc/systemd/resolved.conf
sudo systemctl restart systemd-resolved
sudo systemctl restart skoed
```

---

## 7. OOM / Binary Restart Loop

**Symptoms:** skoed container or process is killed by the OOM killer and restarts repeatedly. `dmesg` shows `oom-kill`.

**Diagnostic steps:**

Check available disk space on the data directory. skoed uses bbolt for its block-list and Raft WAL; a full disk causes bbolt to fail writes, which can trigger a panic-and-restart loop:

```bash
df -h /var/lib/skoed
```

Free space or resize the volume/partition. For Docker, extend the named volume by migrating data to a larger volume.

Inspect the size of bbolt WAL and data files:

```bash
du -sh /var/lib/skoed/*
```

If the WAL is large, it has not been checkpointed. This can happen after frequent writes (e.g., high-frequency block-list updates). Ensure skoed exits cleanly (no `SIGKILL` loops) so bbolt can checkpoint on shutdown.

Check container memory limits. If running in Docker or Kubernetes with a low memory limit, skoed may be OOM-killed rather than running out of disk. Increase the limit:

```bash
# Docker run flag:
--memory 256m

# Kubernetes values.yaml:
resources:
  limits:
    memory: 256Mi
```

Review logs immediately before the restart for the specific error:

```bash
docker logs skoed --tail=50 2>&1 | tail -20
```
