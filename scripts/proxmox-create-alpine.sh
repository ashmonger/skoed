#!/usr/bin/env bash
# proxmox-create-alpine.sh — create an Alpine 3.22 LXC container with skoed
# Run on the Proxmox host (needs `pct`).
#
# The script is tied to the release: SKOED_VERSION matches the GitHub release
# tag it was distributed with. The binary is extracted from the tar.gz archive
# downloaded automatically from GitHub Releases (cached in /tmp/skoed-packages/).
#
# Usage:
#   ./proxmox-create-alpine.sh --id 201 --hostname skoed-2 --ip 10.0.0.101/24 --gw 10.0.0.1
#
# Optional flags:
#   --storage   local       (default: local)
#   --bridge    vmbr1       (default: vmbr1 — private NAT bridge)
#   --memory    256         (MB, default 256)
#   --cores     1           (default 1)
#   --disk      2           (GB, default 2)
#   --template  alpine-3.22-default_20250617_amd64.tar.xz
#   --version   0.1.4       (skoed release version; default: embedded below)
#   --ip        10.0.0.101/24   (static IP with prefix, required)
#   --gw        10.0.0.1        (default gateway, required)
#   --leader-api <ip>:8080  (leader API address for cluster join)
#   --token     <join-token> (single-use token issued by leader)

set -euo pipefail

# ─── Release version — updated automatically when bundled with a release ───
SKOED_VERSION="0.1.4"
GH_REPO="ashmonger/skoed"

CT_ID=""
HOSTNAME=""
STORAGE="local"
BRIDGE="vmbr1"
MEMORY=256
CORES=1
DISK=2
TEMPLATE="alpine-3.22-default_20250617_amd64.tar.xz"
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

# ─── Download tar.gz from GitHub Releases, extract binary (cached) ─────────

CACHE_DIR="/tmp/skoed-packages"
mkdir -p "$CACHE_DIR"
TGZ="${CACHE_DIR}/skoed_${SKOED_VERSION}_linux_amd64.tar.gz"
BIN="${CACHE_DIR}/skoed_${SKOED_VERSION}_linux_amd64"

if [ ! -f "$BIN" ]; then
    if [ ! -f "$TGZ" ]; then
        TGZ_URL="https://github.com/${GH_REPO}/releases/download/v${SKOED_VERSION}/skoed_${SKOED_VERSION}_linux_amd64.tar.gz"
        echo "[0/4] downloading skoed v${SKOED_VERSION} (tar.gz)…"
        curl -fsSL -o "$TGZ" "$TGZ_URL" || {
            echo "ERROR: failed to download $TGZ_URL" >&2
            exit 1
        }
    fi
    echo "      extracting binary…"
    TMP_EXTRACT=$(mktemp -d)
    tar xzf "$TGZ" -C "$TMP_EXTRACT" skoed
    mv "$TMP_EXTRACT/skoed" "$BIN"
    rm -rf "$TMP_EXTRACT"
    echo "      cached at $BIN"
fi

# ─── LXC template ───────────────────────────────────────────────────────────

TMPL_PATH="/var/lib/vz/template/cache/$TEMPLATE"
if [ ! -f "$TMPL_PATH" ]; then
    echo "[0/4] downloading template $TEMPLATE…"
    pveam download local "$TEMPLATE"
fi

BARE_IP="${IP%%/*}"   # strip CIDR prefix

echo "[1/4] creating Alpine LXC $CT_ID ($HOSTNAME, ${MEMORY}MB / ${CORES} core / ${DISK}GB / $BRIDGE, IP=$IP)…"
pct create "$CT_ID" "local:vztmpl/$TEMPLATE" \
    --hostname "$HOSTNAME" \
    --memory "$MEMORY" \
    --cores "$CORES" \
    --rootfs "${STORAGE}:${DISK}" \
    --net0 "name=eth0,bridge=$BRIDGE,ip=${IP},gw=${GW}" \
    --unprivileged 0 \
    --onboot 1 \
    --features "nesting=0"

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
pct push "$CT_ID" "$BIN" /usr/bin/skoed

pct exec "$CT_ID" -- sh -c '
    set -e
    chmod +x /usr/bin/skoed

    addgroup -S skoed 2>/dev/null || true
    adduser -S -G skoed -H -D skoed 2>/dev/null || true

    mkdir -p /var/lib/skoed /var/log/skoed /etc/skoed
    chown skoed:skoed /var/lib/skoed /var/log/skoed

    ls -la /usr/bin/skoed
'

# Write config
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

# OpenRC init script
TMP_INIT=$(mktemp)
cat > "$TMP_INIT" << 'INITEOF'
#!/sbin/openrc-run

name="skoed"
description="skoed DNS filter"
command="/usr/bin/skoed"
command_args="--config /etc/skoed/config.yaml"
command_user="root"
pidfile="/run/skoed.pid"
command_background=true
output_log="/var/log/skoed/skoed.log"
error_log="/var/log/skoed/skoed.log"

depend() {
    need net
    use logger
}
INITEOF
pct push "$CT_ID" "$TMP_INIT" /etc/init.d/skoed
rm -f "$TMP_INIT"

pct exec "$CT_ID" -- chmod +x /etc/init.d/skoed
pct exec "$CT_ID" -- rc-update add skoed default
pct exec "$CT_ID" -- rc-service skoed start

echo "[4/4] waiting for /api/v1/health…"
for i in $(seq 1 60); do
    if curl -fsS "http://${BARE_IP}:8080/api/v1/health" >/dev/null 2>&1; then
        echo
        NOTE="skoed node — Alpine 3.22 LXC (v${SKOED_VERSION})
Hostname:     $HOSTNAME
IP:           $BARE_IP
Raft:         ${BARE_IP}:7000
API:          http://${BARE_IP}:8080
Service:      rc-service skoed status
Logs:         cat /var/log/skoed/skoed.log
Config:       /etc/skoed/config.yaml
Data:         /var/lib/skoed
Init:         /etc/init.d/skoed (OpenRC)"
        if [ -n "$LEADER_API" ]; then
            NOTE="${NOTE}
Cluster:      joined ${LEADER_API}"
        fi
        pct set "$CT_ID" --description "$NOTE" 2>/dev/null || true
        echo "skoed-alpine $CT_ID ($HOSTNAME) is up at http://$BARE_IP:8080"
        echo "NODE_IP=$BARE_IP"
        exit 0
    fi
    sleep 2
done

echo "WARNING: /api/v1/health did not respond within 120s" >&2
echo "  check: pct enter $CT_ID && rc-service skoed status && cat /var/log/skoed/skoed.log" >&2
exit 1
