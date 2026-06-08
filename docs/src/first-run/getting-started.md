# Getting started

A 5-minute walk-through from a fresh install to a working skoed.
The Dashboard shows the same checklist while the cluster is empty —
each step here matches a card on `/`.

If you haven't installed skoed yet, see
[Debian / Ubuntu (.deb)](../install/debian-ubuntu.md) (`apt install
./skoed_*.deb` from the M5.5 .deb release artifacts), or pick the
shape that fits your environment under **Install** in the left nav.

---

## 1. Set the admin password

skoed ships with **no credentials**. The first request you make to
the management API creates them — see
[Set the admin password](auth-setup.md) for the full reference.
The short version:

```sh
# Replace <HOST> with the IP / hostname of your skoed node.
curl -fsS -X POST http://<HOST>:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"<your-password>"}'
```

Then open `http://<HOST>:8080` in your browser and log in as `admin`.
You'll land on the Dashboard. While the cluster is empty, the
**Getting Started** card is what you'll see at the top.

---

## 2. Add a blocklist

A blocklist is a named set of domains that resolve to your chosen
block policy (NXDOMAIN by default). The fastest path is a hosts-format
URL list — skoed will fetch, parse, and start blocking on the next
DNS query. Full reference:
[Add your first blocklist](first-blocklist.md).

```sh
# Hagezi Pro, auto-refreshed every 24 h (the M5.4 refresh shape).
curl -fsS -u admin:<your-password> http://<HOST>:8080/api/v1/blocklists \
  -H 'content-type: application/json' \
  -d '{
    "id":     "hagezi-pro",
    "name":   "Hagezi Pro",
    "enabled": true,
    "source": {
      "type":   "url",
      "url":    "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt",
      "format": "hosts"
    },
    "refresh_interval_seconds": 86400
  }'
```

Refresh the Dashboard — the **Getting Started** card is now gone,
replaced by populated stat tiles once a few DNS queries flow through.

In the Web UI you can do the same via **Blocklists → New blocklist**.

---

## 3. (Optional) Bootstrap a cluster

A single-node skoed works for any home setup. If you want HA — say,
two cluster members on a Proxmox box and a third on a Raspberry Pi —
follow [Bootstrap a 3-node cluster](../cluster/bootstrap.md). The
short version:

```sh
# On the first node — issue a join token (single-use, 5 min TTL).
curl -fsS -u admin:<your-password> -X POST \
  http://<NODE1>:8080/api/v1/cluster/tokens

# Response includes a `bootstrap:` block you paste into NODE2's
# /etc/skoed/config.yaml, then `systemctl restart skoed`.
```

Repeat for NODE3. The Dashboard's **Cluster nodes** table shows each
member's role (leader / follower) and sync state.

You can also use the M5.9.1 CLI: `skoed token create` formats the
same `bootstrap:` block inside a copy-pasteable Lipgloss box.

---

## 4. Point a client at skoed

The simplest test is one `dig`:

```sh
dig @<HOST> example.com         # should resolve normally
dig @<HOST> doubleclick.net     # should be NXDOMAIN once a blocklist is loaded
```

For real client traffic, set the DNS server on your network:

- **Router** — set DHCP option 6 (DNS server) to the skoed IP.
  Every device that takes a fresh lease will use skoed from then on.
- **Single host (Linux)** — edit `/etc/resolv.conf` (or
  `nmcli`/`systemd-resolved` config) to point at the skoed IP.
- **Single host (macOS / Windows)** — set the DNS server in System
  Settings → Network → DNS.

skoed listens on UDP/TCP 53 by default. To serve DoH or DoT (TLS
or HTTPS on port 853 / 443 respectively), see
[DoH / DoT serving](../configuration/doh-dot.md).

---

## You're done

At this point:

- Dashboard shows live query stats, top blocked domains, cluster
  health.
- The **Getting Started** card has auto-hidden — it only re-appears
  if you clear localStorage (it stays gone after a manual [x]).

### Where to next

- [Profiles & schedules](../configuration/profiles.md) — different
  rules for different clients (kids' devices, guest network).
- [Categories](../configuration/categories.md) — pre-baked lists
  (DoH probes, social, gambling).
- [Automated blocklist refresh](../operations/automated-refresh.md) —
  the M5.4 worker that keeps URL blocklists current.
- [In-place upgrade](../operations/in-place-upgrade.md) — the M5.6
  Dashboard banner + one-click upgrade flow.
- [Troubleshooting](../operations/troubleshooting.md) — when DNS
  doesn't resolve, when a blocklist won't refresh, etc.
