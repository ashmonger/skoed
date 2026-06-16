#!/usr/bin/env bash
# proxmox-cluster.sh — deploy a 3-node skoed cluster on Proxmox
# Run on the Proxmox host as root.
#
# The skoed packages are downloaded automatically from GitHub Releases —
# no pre-staging required. The script is tied to the release it ships with.
#
# Creates:
#   Node 1 (ID 200): Debian 12 LXC   — bootstrap Raft leader   — 10.0.0.100
#   Node 2 (ID 201): Alpine 3.22 LXC — joins cluster            — 10.0.0.101
#   Node 3 (ID 202): Debian 12 LXC   — joins cluster            — 10.0.0.102
#
# All nodes share a private bridge (vmbr1, 10.0.0.0/24) with NAT.
# The bridge is created automatically if it doesn't exist.
#
# Usage (run on the Proxmox host):
#   ./proxmox-cluster.sh [--admin-password <pass>] [--destroy]
#
# Optional flags:
#   --version       0.1.2           (skoed release to install; default: embedded)
#   --node1-id      200             (default: 200)
#   --node2-id      201             (default: 201)
#   --node3-id      202             (default: 202)
#   --node1-ip      10.0.0.100      (default: 10.0.0.100)
#   --node2-ip      10.0.0.101      (default: 10.0.0.101)
#   --node3-ip      10.0.0.102      (default: 10.0.0.102)
#   --net           10.0.0.0/24     (internal subnet)
#   --host-ip       10.0.0.1        (host IP on private bridge)
#   --bridge        vmbr1           (private bridge name)
#   --storage       local
#   --admin-password <pass>         (auto-generated if omitted)
#   --destroy                       destroy existing containers before creating
#   --skip-node1                    skip node 1 (must already exist and be ready)
#   --skip-node2                    skip node 2
#   --skip-node3                    skip node 3

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ─── Release version — updated automatically when bundled with a release ───
SKOED_VERSION="0.1.2"

NODE1_ID=200
NODE2_ID=201
NODE3_ID=202
STORAGE="local"
BRIDGE="vmbr1"
HOST_IP="10.0.0.1"
NODE1_IP="10.0.0.100"
NODE2_IP="10.0.0.101"
NODE3_IP="10.0.0.102"
CIDR="24"
ADMIN_PASS=""
DESTROY=0
SKIP_NODE1=0
SKIP_NODE2=0
SKIP_NODE3=0

while [ $# -gt 0 ]; do
    case "$1" in
        --version)         SKOED_VERSION="$2"; shift 2 ;;
        --node1-id)        NODE1_ID="$2";      shift 2 ;;
        --node2-id)        NODE2_ID="$2";      shift 2 ;;
        --node3-id)        NODE3_ID="$2";      shift 2 ;;
        --node1-ip)        NODE1_IP="$2";      shift 2 ;;
        --node2-ip)        NODE2_IP="$2";      shift 2 ;;
        --node3-ip)        NODE3_IP="$2";      shift 2 ;;
        --storage)         STORAGE="$2";       shift 2 ;;
        --bridge)          BRIDGE="$2";        shift 2 ;;
        --host-ip)         HOST_IP="$2";       shift 2 ;;
        --admin-password)  ADMIN_PASS="$2";    shift 2 ;;
        --destroy)         DESTROY=1;          shift ;;
        --skip-node1)      SKIP_NODE1=1;       shift ;;
        --skip-node2)      SKIP_NODE2=1;       shift ;;
        --skip-node3)      SKIP_NODE3=1;       shift ;;
        --help|-h)         sed -n '2,36p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$ADMIN_PASS" ]; then
    ADMIN_PASS="skoed-$(head -c 9 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 12)"
fi

echo "================================================================"
echo "  skoed cluster deployment (v${SKOED_VERSION})"
echo "  Node 1 (Debian LXC,  ID $NODE1_ID):   skoed-1 @ $NODE1_IP"
echo "  Node 2 (Alpine LXC,  ID $NODE2_ID):   skoed-2 @ $NODE2_IP"
echo "  Node 3 (Debian LXC,  ID $NODE3_ID):   skoed-3 @ $NODE3_IP"
echo "  Bridge: $BRIDGE ($HOST_IP/$CIDR) — NAT via vmbr0"
echo "  Storage: $STORAGE"
echo "================================================================"

# ─── Private bridge setup ────────────────────────────────────────────────────

echo
echo "━━━ STEP 0: ensuring private bridge $BRIDGE exists ━━━"

if ! ip link show "$BRIDGE" >/dev/null 2>&1; then
    echo "  creating $BRIDGE with IP $HOST_IP/$CIDR…"
    ip link add name "$BRIDGE" type bridge
    ip link set "$BRIDGE" up
    ip addr add "${HOST_IP}/${CIDR}" dev "$BRIDGE"
fi

if ! ip addr show "$BRIDGE" | grep -q "$HOST_IP"; then
    ip addr add "${HOST_IP}/${CIDR}" dev "$BRIDGE" 2>/dev/null || true
fi

sysctl -w net.ipv4.ip_forward=1 >/dev/null

SUBNET="${NODE1_IP%.*}.0/${CIDR}"
if ! iptables -t nat -C POSTROUTING -s "$SUBNET" -o vmbr0 -j MASQUERADE 2>/dev/null; then
    iptables -t nat -A POSTROUTING -s "$SUBNET" -o vmbr0 -j MASQUERADE
    echo "  NAT masquerade added for $SUBNET → vmbr0"
fi

if ! grep -q "iface $BRIDGE" /etc/network/interfaces 2>/dev/null; then
    cat >> /etc/network/interfaces << NETEOF

auto $BRIDGE
iface $BRIDGE inet static
    address ${HOST_IP}/${CIDR}
    bridge-ports none
    bridge-stp off
    bridge-fd 0
    post-up   echo 1 > /proc/sys/net/ipv4/ip_forward
    post-up   iptables -t nat -A POSTROUTING -s '$SUBNET' -o vmbr0 -j MASQUERADE
    post-down iptables -t nat -D POSTROUTING -s '$SUBNET' -o vmbr0 -j MASQUERADE
NETEOF
    echo "  $BRIDGE persisted to /etc/network/interfaces"
fi

echo "  bridge $BRIDGE is ready ($HOST_IP/$CIDR, NAT enabled)"

# ─── Pre-destroy all nodes upfront when --destroy is set ─────────────────────
# Must happen before creating any node so that no old Raft peer can replicate
# its state (auth credentials, config) to a newly-bootstrapped node.

if [ "$DESTROY" -eq 1 ]; then
    echo
    echo "━━━ PRE-DESTROY: stopping all existing cluster nodes ━━━"
    for _pre_id in "$NODE1_ID" "$NODE2_ID" "$NODE3_ID"; do
        if pct status "$_pre_id" >/dev/null 2>&1; then
            echo "  stopping + destroying container $_pre_id…"
            pct stop "$_pre_id" 2>/dev/null || true
        fi
    done
    sleep 4
    for _pre_id in "$NODE1_ID" "$NODE2_ID" "$NODE3_ID"; do
        pct destroy "$_pre_id" 2>/dev/null || true
    done
    echo "  all existing containers removed"
fi

# ─── Helper functions ─────────────────────────────────────────────────────────

destroy_if_exists_ct() {
    local id="$1"
    if pct status "$id" >/dev/null 2>&1; then
        if [ "$DESTROY" -eq 1 ]; then
            echo "  destroying existing container $id…"
            pct stop "$id" 2>/dev/null || true
            sleep 3
            pct destroy "$id" 2>/dev/null || true
        else
            echo "ERROR: container $id already exists. Use --destroy to overwrite." >&2
            exit 1
        fi
    fi
}

wait_health() {
    local ip="$1" label="$2"
    for i in $(seq 1 60); do
        if curl -fsS "http://${ip}:8080/api/v1/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    echo "ERROR: $label at $ip did not become healthy within 120s" >&2
    return 1
}

issue_token() {
    local leader_ip="$1"
    # The API uses session Bearer tokens — Basic Auth is not accepted.
    # Step 1: login to get a short-lived session token.
    local BEARER
    BEARER=$(curl -fsS \
        -X POST "http://${leader_ip}:8080/api/v1/auth/login" \
        -H "content-type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PASS}\"}" \
        | python3 -c "import json,sys; print(json.load(sys.stdin)['token'])")
    # Step 2: use the Bearer token to issue a cluster join token.
    curl -fsS \
        -H "Authorization: Bearer ${BEARER}" \
        -X POST "http://${leader_ip}:8080/api/v1/cluster/tokens" \
        -H "content-type: application/json" \
        | python3 -c "import json,sys; print(json.load(sys.stdin)['token'])"
}

# ─── Node 1: Debian 12 LXC (bootstrap leader) ────────────────────────────────

if [ "$SKIP_NODE1" -eq 0 ]; then
    destroy_if_exists_ct "$NODE1_ID"
    echo
    echo "━━━ STEP 1: deploying Debian 12 LXC $NODE1_ID (skoed-1 @ $NODE1_IP) ━━━"
    bash "$SCRIPT_DIR/proxmox-create.sh" \
        --id "$NODE1_ID" \
        --hostname "skoed-1" \
        --storage "$STORAGE" \
        --bridge "$BRIDGE" \
        --ip "${NODE1_IP}/${CIDR}" \
        --gw "$HOST_IP" \
        --version "$SKOED_VERSION"
else
    echo "SKIP: node 1 ($NODE1_ID) — assuming already deployed"
fi

echo
echo "━━━ STEP 2: setting admin password on node 1 ━━━"
wait_health "$NODE1_IP" "node-1"
HTTP_CODE=$(curl -o /dev/null -w "%{http_code}" -fsS \
    -X POST "http://${NODE1_IP}:8080/api/v1/auth/setup" \
    -H "content-type: application/json" \
    -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PASS}\"}" 2>/dev/null || echo "000")
echo "  auth/setup HTTP $HTTP_CODE (409 = already configured, both OK)"

# ─── Node 2: Alpine 3.22 LXC ──────────────────────────────────────────────────

if [ "$SKIP_NODE2" -eq 0 ]; then
    destroy_if_exists_ct "$NODE2_ID"
    echo
    echo "━━━ STEP 3: issuing join token for node 2 ━━━"
    TOKEN2=$(issue_token "$NODE1_IP")
    echo "  token issued"

    echo
    echo "━━━ STEP 4: deploying Alpine 3.22 LXC $NODE2_ID (skoed-2 @ $NODE2_IP) ━━━"
    bash "$SCRIPT_DIR/proxmox-create-alpine.sh" \
        --id "$NODE2_ID" \
        --hostname "skoed-2" \
        --storage "$STORAGE" \
        --bridge "$BRIDGE" \
        --ip "${NODE2_IP}/${CIDR}" \
        --gw "$HOST_IP" \
        --leader-api "${NODE1_IP}:8080" \
        --token "$TOKEN2" \
        --version "$SKOED_VERSION"
else
    echo "SKIP: node 2 ($NODE2_ID) — assuming already deployed"
fi

# ─── Node 3: Debian 12 LXC ────────────────────────────────────────────────────

if [ "$SKIP_NODE3" -eq 0 ]; then
    destroy_if_exists_ct "$NODE3_ID"
    echo
    echo "━━━ STEP 5: issuing join token for node 3 ━━━"
    TOKEN3=$(issue_token "$NODE1_IP")
    echo "  token issued"

    echo
    echo "━━━ STEP 6: deploying Debian 12 LXC $NODE3_ID (skoed-3 @ $NODE3_IP) ━━━"
    bash "$SCRIPT_DIR/proxmox-create.sh" \
        --id "$NODE3_ID" \
        --hostname "skoed-3" \
        --storage "$STORAGE" \
        --bridge "$BRIDGE" \
        --ip "${NODE3_IP}/${CIDR}" \
        --gw "$HOST_IP" \
        --leader-api "${NODE1_IP}:8080" \
        --token "$TOKEN3" \
        --version "$SKOED_VERSION"
else
    echo "SKIP: node 3 ($NODE3_ID) — assuming already deployed"
fi

# ─── Cluster health verification ──────────────────────────────────────────────

echo
echo "━━━ STEP 7: verifying cluster health ━━━"

ALL_OK=1
for _entry in "${NODE1_ID}:${NODE1_IP}" "${NODE2_ID}:${NODE2_IP}" "${NODE3_ID}:${NODE3_IP}"; do
    _id="${_entry%%:*}"; _ip="${_entry##*:}"
    if curl -fsS "http://${_ip}:8080/api/v1/health" >/dev/null 2>&1; then
        echo "  [OK]  CT ${_id} / ${_ip}:8080"
    else
        echo "  [ERR] CT ${_id} / ${_ip}:8080 — health check failed"
        ALL_OK=0
    fi
done
[ "$ALL_OK" -eq 1 ] && echo "  all nodes healthy" || echo "  WARNING: some nodes unhealthy"

CLUSTER_NOTE="skoed cluster v${SKOED_VERSION}
Admin UI:     http://${NODE1_IP}:8080
Username:     admin
Password:     ${ADMIN_PASS}

Nodes:
  skoed-1 (leader, Debian LXC,  CT ${NODE1_ID}): http://${NODE1_IP}:8080
  skoed-2 (Alpine LXC,          CT ${NODE2_ID}): http://${NODE2_IP}:8080
  skoed-3 (Debian LXC,          CT ${NODE3_ID}): http://${NODE3_IP}:8080

Health:       GET http://${NODE1_IP}:8080/api/v1/health
Join tokens:  POST http://${NODE1_IP}:8080/api/v1/cluster/tokens (Basic auth)"
pct set "$NODE1_ID" --description "$CLUSTER_NOTE" 2>/dev/null || true

echo
echo "================================================================"
echo "  Cluster deployed! (v${SKOED_VERSION})"
echo "  Admin UI:  http://$NODE1_IP:8080"
echo "  Username:  admin"
echo "  Password:  $ADMIN_PASS"
echo "  Node 2:    http://$NODE2_IP:8080"
echo "  Node 3:    http://$NODE3_IP:8080"
echo "================================================================"
