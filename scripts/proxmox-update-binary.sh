#!/usr/bin/env bash
# proxmox-update-binary.sh — push a rebuilt skoed binary to running cluster nodes
# Run from the local workstation; requires SSH access to the Proxmox host.
#
# Usage:
#   ./proxmox-update-binary.sh --host <proxmox-host> --binary <path-to-skoed>
#
# Optional:
#   --node1-id  200           (default: 200)
#   --node2-id  201           (default: 201)
#   --node3-id  202           (default: 202)
#   --node1-ip  10.0.0.100    (default: 10.0.0.100)
#   --node2-ip  10.0.0.101    (default: 10.0.0.101)
#   --node3-ip  10.0.0.102    (default: 10.0.0.102)
#   --skip-node1 / --skip-node2 / --skip-node3

set -euo pipefail

PROXMOX_HOST=""
BINARY=""
SSH_KEY="${HOME}/.ssh/id_ed25519"
NODE1_ID=200; NODE1_IP="10.0.0.100"
NODE2_ID=201; NODE2_IP="10.0.0.101"
NODE3_ID=202; NODE3_IP="10.0.0.102"
SKIP_NODE1=0
SKIP_NODE2=0
SKIP_NODE3=0

while [ $# -gt 0 ]; do
    case "$1" in
        --host)       PROXMOX_HOST="$2"; shift 2 ;;
        --binary)     BINARY="$2";       shift 2 ;;
        --key)        SSH_KEY="$2";      shift 2 ;;
        --node1-id)   NODE1_ID="$2";     shift 2 ;;
        --node2-id)   NODE2_ID="$2";     shift 2 ;;
        --node3-id)   NODE3_ID="$2";     shift 2 ;;
        --node1-ip)   NODE1_IP="$2";     shift 2 ;;
        --node2-ip)   NODE2_IP="$2";     shift 2 ;;
        --node3-ip)   NODE3_IP="$2";     shift 2 ;;
        --skip-node1) SKIP_NODE1=1;      shift ;;
        --skip-node2) SKIP_NODE2=1;      shift ;;
        --skip-node3) SKIP_NODE3=1;      shift ;;
        --help|-h)    sed -n '2,18p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$PROXMOX_HOST" ] || [ -z "$BINARY" ]; then
    echo "usage: $0 --host <proxmox-host> --binary <path-to-skoed>" >&2
    exit 2
fi
if [ ! -x "$BINARY" ]; then
    echo "binary not executable or not found: $BINARY" >&2
    exit 2
fi

SSH="ssh -i $SSH_KEY -o StrictHostKeyChecking=no root@$PROXMOX_HOST"
SCP="scp -i $SSH_KEY -o StrictHostKeyChecking=no"

echo "uploading binary to Proxmox host…"
$SCP "$BINARY" "root@${PROXMOX_HOST}:/tmp/skoed-new"
$SSH "chmod +x /tmp/skoed-new"

push_to_ct() {
    local id="$1" type="$2"

    if [ "$type" = "alpine" ]; then
        echo "  [CT $id] stopping skoed (OpenRC)…"
        $SSH "pct exec $id -- rc-service skoed stop 2>&1 || true"
    else
        echo "  [CT $id] stopping skoed (systemd)…"
        $SSH "pct exec $id -- systemctl stop skoed 2>&1 || true"
    fi
    sleep 1

    echo "  [CT $id] pushing binary…"
    $SSH "pct exec $id -- mkdir -p /var/lib/skoed/bin"
    $SSH "pct push $id /tmp/skoed-new /var/lib/skoed/bin/skoed"
    $SSH "pct exec $id -- chmod +x /var/lib/skoed/bin/skoed"

    if [ "$type" = "alpine" ]; then
        echo "  [CT $id] starting skoed (OpenRC)…"
        $SSH "pct exec $id -- rc-service skoed start"
        sleep 3
        STATUS=$($SSH "pct exec $id -- rc-service skoed status 2>&1" || true)
        echo "  [CT $id] status: $STATUS"
    else
        echo "  [CT $id] starting skoed (systemd)…"
        $SSH "pct exec $id -- systemctl start skoed"
        sleep 3
        STATUS=$($SSH "pct exec $id -- systemctl is-active skoed 2>&1" || true)
        echo "  [CT $id] status: $STATUS"
    fi
}

[ "$SKIP_NODE1" -eq 0 ] && push_to_ct "$NODE1_ID" "debian"
[ "$SKIP_NODE2" -eq 0 ] && push_to_ct "$NODE2_ID" "alpine"
[ "$SKIP_NODE3" -eq 0 ] && push_to_ct "$NODE3_ID" "debian"

$SSH "rm -f /tmp/skoed-new"

echo
echo "done — checking health…"
for entry in "${NODE1_ID}:${NODE1_IP}" "${NODE2_ID}:${NODE2_IP}" "${NODE3_ID}:${NODE3_IP}"; do
    id="${entry%%:*}"; ip="${entry##*:}"
    if $SSH "curl -fsS http://${ip}:8080/api/v1/health >/dev/null 2>&1"; then
        echo "  [OK]  CT $id / $ip"
    else
        echo "  [ERR] CT $id / $ip — health check failed"
    fi
done
