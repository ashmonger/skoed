#!/usr/bin/env bash
# proxmox-create.sh — create a Debian 12 LXC container with skoed
# pre-installed. Run on the Proxmox host (needs `pct`).
#
# Usage:
#   ./proxmox-create.sh --id 200 --hostname skoed-1 \
#                       --deb dist/skoed_0.5.0_amd64.deb
#
# Optional:
#   --storage local-lvm     (default: local-lvm)
#   --bridge  vmbr0         (default: vmbr0)
#   --memory  512           (MB, default 512)
#   --cores   1             (default 1)
#   --disk    4             (GB, default 4)
#   --template debian-12-standard_12.7-1_amd64.tar.zst

set -euo pipefail

CT_ID=""
HOSTNAME=""
DEB=""
STORAGE="local-lvm"
BRIDGE="vmbr0"
MEMORY=512
CORES=1
DISK=4
TEMPLATE="debian-12-standard_12.7-1_amd64.tar.zst"

while [ $# -gt 0 ]; do
    case "$1" in
        --id)        CT_ID="$2"; shift 2 ;;
        --hostname)  HOSTNAME="$2"; shift 2 ;;
        --deb)       DEB="$2"; shift 2 ;;
        --storage)   STORAGE="$2"; shift 2 ;;
        --bridge)    BRIDGE="$2"; shift 2 ;;
        --memory)    MEMORY="$2"; shift 2 ;;
        --cores)     CORES="$2"; shift 2 ;;
        --disk)      DISK="$2"; shift 2 ;;
        --template)  TEMPLATE="$2"; shift 2 ;;
        --help|-h)   sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$CT_ID" ] || [ -z "$HOSTNAME" ] || [ -z "$DEB" ]; then
    echo "usage: $0 --id <ct-id> --hostname <name> --deb <path-to-.deb>" >&2
    exit 2
fi
if [ ! -r "$DEB" ]; then
    echo "deb not readable: $DEB" >&2
    exit 2
fi
if ! command -v pct >/dev/null 2>&1; then
    echo "this script must run on a Proxmox host (no \`pct\` in PATH)" >&2
    exit 2
fi
if pct status "$CT_ID" >/dev/null 2>&1; then
    echo "container $CT_ID already exists — refusing to overwrite" >&2
    exit 1
fi

echo "[1/4] creating LXC $CT_ID ($HOSTNAME, ${MEMORY}MB / ${CORES} core / ${DISK}GB / $BRIDGE)…"
pct create "$CT_ID" "local:vztmpl/$TEMPLATE" \
    --hostname "$HOSTNAME" \
    --memory "$MEMORY" \
    --cores "$CORES" \
    --rootfs "${STORAGE}:${DISK}" \
    --net0 "name=eth0,bridge=$BRIDGE,ip=dhcp" \
    --unprivileged 1 \
    --onboot 1 \
    --features "nesting=0"

echo "[2/4] starting…"
pct start "$CT_ID"
# Wait briefly for the network to come up inside the container.
for _ in $(seq 1 20); do
    if pct exec "$CT_ID" -- getent hosts deb.debian.org >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

echo "[3/4] copying + installing .deb…"
pct push "$CT_ID" "$DEB" /tmp/skoed.deb
pct exec "$CT_ID" -- bash -c '
    set -e
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends adduser
    dpkg -i /tmp/skoed.deb || (apt-get -f install -y && dpkg -i /tmp/skoed.deb)
    systemctl enable --now skoed
'

echo "[4/4] waiting for /api/v1/health…"
IP=$(pct exec "$CT_ID" -- hostname -I | awk "{print \$1}")
for _ in $(seq 1 30); do
    if curl -fsS "http://$IP:8080/api/v1/health" >/dev/null 2>&1; then
        echo
        echo "skoed is up at http://$IP:8080"
        echo "Set the admin password with:"
        echo "  curl -X POST http://$IP:8080/api/v1/auth/setup -H 'content-type: application/json' \\"
        echo "       -d '{\"username\":\"admin\",\"password\":\"…\"}'"
        exit 0
    fi
    sleep 1
done

echo "WARNING: /api/v1/health did not respond within 30s — check 'pct enter $CT_ID' + 'systemctl status skoed'" >&2
exit 1
