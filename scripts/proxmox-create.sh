#!/usr/bin/env bash
# proxmox-create.sh — create a Debian 12 LXC container with skoed
# Run on the Proxmox host (needs `pct`).
#
# The script is tied to the release: SKOED_VERSION matches the GitHub release
# tag it was distributed with. The .deb is downloaded automatically from
# GitHub Releases on first run and cached in /tmp/skoed-packages/.
#
# Usage:
#   ./proxmox-create.sh --id 200 --hostname skoed-1 --ip 10.0.0.100/24 --gw 10.0.0.1
#
# Optional flags:
#   --storage   local       (default: local)
#   --bridge    vmbr1       (default: vmbr1 — private NAT bridge)
#   --memory    512         (MB, default 512)
#   --cores     1           (default 1)
#   --disk      4           (GB, default 4)
#   --template  debian-12-standard_12.12-1_amd64.tar.zst
#   --version   0.1.2       (skoed release version; default: embedded below)
#   --ip        10.0.0.100/24  (static IP with prefix length, required)
#   --gw        10.0.0.1       (default gateway, required)
#   --leader-api <ip>:8080  (leader API address for cluster join)
#   --token     <join-token> (single-use token issued by leader)

set -euo pipefail

# ─── Release version — updated automatically when bundled with a release ───
SKOED_VERSION="0.1.2"
GH_REPO="ashmonger/skoed"

CT_ID=""
HOSTNAME=""
STORAGE="local"
BRIDGE="vmbr1"
MEMORY=512
CORES=1
DISK=4
TEMPLATE="debian-12-standard_12.12-1_amd64.tar.zst"
IP=""
GW=""
LEADER_API=""
JOIN_TOKEN=""

while [ $# -gt 0 ]; do
    case "$1" in
        --id)          CT_ID="$2";          shift 2 ;;
        --hostname)    HOSTNAME="$2";       shift 2 ;;
        --storage)     STORAGE="$2";        shift 2 ;;
        --bridge)      BRIDGE="$2";         shift 2 ;;
        --memory)      MEMORY="$2";         shift 2 ;;
        --cores)       CORES="$2";          shift 2 ;;
        --disk)        DISK="$2";           shift 2 ;;
        --template)    TEMPLATE="$2";       shift 2 ;;
        --version)     SKOED_VERSION="$2";  shift 2 ;;
        --ip)          IP="$2";             shift 2 ;;
        --gw)          GW="$2";             shift 2 ;;
        --leader-api)  LEADER_API="$2";     shift 2 ;;
        --token)       JOIN_TOKEN="$2";     shift 2 ;;
        --help|-h)     sed -n '2,30p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$CT_ID" ] || [ -z "$HOSTNAME" ] || [ -z "$IP" ] || [ -z "$GW" ]; then
    echo "usage: $0 --id <ct-id> --hostname <name> --ip <cidr> --gw <gateway> [options]" >&2
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

# ─── Download .deb from GitHub Releases (cached) ───────────────────────────

CACHE_DIR="/tmp/skoed-packages"
mkdir -p "$CACHE_DIR"
DEB="${CACHE_DIR}/skoed_${SKOED_VERSION}_amd64.deb"

if [ ! -f "$DEB" ]; then
    DEB_URL="https://github.com/${GH_REPO}/releases/download/v${SKOED_VERSION}/skoed_${SKOED_VERSION}_amd64.deb"
    echo "[0/4] downloading skoed v${SKOED_VERSION} (.deb)…"
    curl -fsSL -o "$DEB" "$DEB_URL" || {
        echo "ERROR: failed to download $DEB_URL" >&2
        exit 1
    }
    echo "      cached at $DEB"
fi

# ─── LXC template ───────────────────────────────────────────────────────────

TMPL_PATH="/var/lib/vz/template/cache/$TEMPLATE"
if [ ! -f "$TMPL_PATH" ]; then
    echo "[0/4] downloading template $TEMPLATE…"
    pveam download local "$TEMPLATE"
fi

BARE_IP="${IP%%/*}"   # strip CIDR prefix for use in config

echo "[1/4] creating LXC $CT_ID ($HOSTNAME, ${MEMORY}MB / ${CORES} core / ${DISK}GB / $BRIDGE, IP=$IP)…"
pct create "$CT_ID" "local:vztmpl/$TEMPLATE" \
    --hostname "$HOSTNAME" \
    --memory "$MEMORY" \
    --cores "$CORES" \
    --rootfs "${STORAGE}:${DISK}" \
    --net0 "name=eth0,bridge=$BRIDGE,ip=${IP},gw=${GW}" \
    --unprivileged 0 \
    --onboot 1 \
    --features "nesting=1"

echo "[2/4] starting…"
pct start "$CT_ID"
for i in $(seq 1 20); do
    if pct exec "$CT_ID" -- ping -c1 -W2 "$GW" >/dev/null 2>&1; then
        break
    fi
    sleep 2
done
echo "    container IP: $BARE_IP"

echo "[3/4] installing skoed v${SKOED_VERSION}…"
pct push "$CT_ID" "$DEB" /tmp/skoed.deb
pct exec "$CT_ID" -- bash -c '
    set -e
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends adduser curl
    dpkg -i /tmp/skoed.deb || DEBIAN_FRONTEND=noninteractive apt-get -f install -y
    rm /tmp/skoed.deb

    # Disable hardening options that fail in LXC kernel namespace
    mkdir -p /etc/systemd/system/skoed.service.d
    cat > /etc/systemd/system/skoed.service.d/lxc.conf << '"'"'EOF'"'"'
[Service]
ProtectKernelTunables=no
ProtectKernelModules=no
ProtectControlGroups=no
EOF
    systemctl daemon-reload
'

# Write config to a temp file on the host and push into the container.
NODE_ID="${HOSTNAME}"
TMP_CONFIG=$(mktemp)
cat > "$TMP_CONFIG" << EOF
node:
  id: ${NODE_ID}
  raft_address: ${BARE_IP}:7000
  api_address: 0.0.0.0:8080
  data_dir: /var/lib/skoed

  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: false
EOF
if [ -n "$LEADER_API" ] && [ -n "$JOIN_TOKEN" ]; then
    LEADER_URL="$LEADER_API"
    case "$LEADER_URL" in http://*|https://*) ;; *) LEADER_URL="http://$LEADER_URL" ;; esac
    cat >> "$TMP_CONFIG" << EOF

bootstrap:
  leader_address: ${LEADER_URL}
  token: ${JOIN_TOKEN}
EOF
fi
pct push "$CT_ID" "$TMP_CONFIG" /etc/skoed/config.yaml
rm -f "$TMP_CONFIG"

pct exec "$CT_ID" -- systemctl enable skoed
pct exec "$CT_ID" -- systemctl start skoed

echo "[4/4] waiting for /api/v1/health…"
for i in $(seq 1 60); do
    if curl -fsS "http://${BARE_IP}:8080/api/v1/health" >/dev/null 2>&1; then
        echo
        NOTE="skoed node — Debian 12 LXC (v${SKOED_VERSION})
Hostname:     $HOSTNAME
IP:           $BARE_IP
Raft:         ${BARE_IP}:7000
API:          http://${BARE_IP}:8080
Service:      systemctl status skoed
Logs:         journalctl -u skoed -f
Config:       /etc/skoed/config.yaml
Data:         /var/lib/skoed"
        if [ -n "$LEADER_API" ]; then
            NOTE="${NOTE}
Cluster:      joined ${LEADER_API}"
        fi
        pct set "$CT_ID" --description "$NOTE" 2>/dev/null || true
        echo "skoed-lxc $CT_ID ($HOSTNAME) is up at http://$BARE_IP:8080"
        echo "NODE_IP=$BARE_IP"
        exit 0
    fi
    sleep 2
done

echo "WARNING: /api/v1/health did not respond within 120s" >&2
echo "  check: pct enter $CT_ID && systemctl status skoed" >&2
exit 1
