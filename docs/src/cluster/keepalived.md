# High-availability VIP with keepalived

A 3-node skoed cluster tolerates one node going down — Raft re-elects a
leader automatically. But DNS clients and your browser need a stable IP to
connect to. keepalived provides a **floating virtual IP** (VIP) that moves
to a healthy node whenever the current holder fails.

> **Note:** skoed already forwards every write from any node to the Raft
> leader internally. You do not need to track the leader manually; any
> healthy node accepts both reads and writes.

## What keepalived solves

| Without VIP | With VIP |
|-------------|----------|
| DNS clients hard-code `10.0.0.11:53`. If CT301 goes down, all clients break. | DNS clients use `10.0.0.10:53` (VIP). keepalived moves the VIP to CT302 or CT303 automatically. |
| Browser bookmark `http://10.0.0.11:8080` breaks on node failure. | `http://10.0.0.10:8080` always works. |

## Prerequisites

- keepalived ≥ 2.2 installed on every node (`apt install keepalived` or `apk add keepalived curl`)
- `curl` installed on every node (needed by the health-check script)
- All nodes on the same L2 segment (VRRP uses multicast or unicast)
- A free IP on your LAN subnet to use as the VIP (e.g. `10.0.0.10`)
- skoed configured with `api_address: 0.0.0.0:8080` (not a specific node IP) so it
  serves the VIP address automatically when keepalived assigns it. The Raft address
  (`raft_address`) must still be the node's own IP.

## Installation

### 1. Copy the health-check script

On **each** node:

```sh
cp /path/to/deploy/keepalived/skoed-health.sh /etc/keepalived/skoed-health.sh
chmod +x /etc/keepalived/skoed-health.sh
```

The script calls `GET /api/v1/health` on `127.0.0.1:8080`. If skoed is down
or has lost quorum, the script exits 1 and the node yields the VIP.

### 2. Configure keepalived

Copy `deploy/keepalived/keepalived.conf.template` to
`/etc/keepalived/keepalived.conf` on **each** node, then replace the
placeholders for your environment. Example for a 3-node Proxmox LXC cluster
(CT301 = `10.0.0.11`, CT302 = `10.0.0.12`, CT303 = `10.0.0.13`,
VIP = `10.0.0.10`):

**CT301** (`priority 101` — preferred primary):

```
interface eth0
priority  101
NODE_IP   10.0.0.11
PEER_IP_1 10.0.0.12
PEER_IP_2 10.0.0.13
VIRTUAL_IP 10.0.0.10/24
```

**CT302** (`priority 100`):

```
interface eth0
priority  100
NODE_IP   10.0.0.12
PEER_IP_1 10.0.0.11
PEER_IP_2 10.0.0.13
VIRTUAL_IP 10.0.0.10/24
```

**CT303** (`priority 99`):

```
interface eth0
priority  99
NODE_IP   10.0.0.13
PEER_IP_1 10.0.0.11
PEER_IP_2 10.0.0.12
VIRTUAL_IP 10.0.0.10/24
```

Set the same `VRRP_PASSWORD` (8-char max) on all three nodes.

### 3. Start keepalived

```sh
systemctl enable keepalived
systemctl start  keepalived
```

Verify: `ip addr show eth0` on CT301 should include `10.0.0.10/24`.

> **Proxmox LXC note:** LXC containers use `eth0` as their primary interface name,
> not `ens18` or other predictable-name scheme interfaces. Always confirm with
> `ip link show` inside the container before writing the config.

### 4. Point DHCP at the VIP

In dnsmasq (runs on CT301):

```
dhcp-option=6,10.0.0.10
```

Restart dnsmasq. New DHCP leases will advertise the VIP as the DNS server.

## Verifying failover

```sh
# On CT301, stop skoed and watch the VIP move to CT302:
systemctl stop skoed
sleep 10
ssh root@10.0.0.12 "ip addr show eth0 | grep 10.0.0.10"
```

DNS queries to `10.0.0.10:53` should keep working within 3–5 seconds.

## Notes

- The VRRP master and the Raft leader are independent. When CT301 holds the
  VIP but CT302 is the Raft leader, writes to `10.0.0.10` are transparently
  forwarded by CT301 to CT302. This is by design.
- `weight -20` in the health script means a failing node's priority drops
  below any healthy peer (101 − 20 = 81 < 99), so it always yields the VIP.
- Adjust `VRRP_PASSWORD` to something site-specific; keepalived transmits
  it in plain text on the LAN (VRRP is a L2 protocol).
