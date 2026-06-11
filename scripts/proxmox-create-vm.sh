#!/usr/bin/env bash
# proxmox-create-vm.sh — create a Debian 12 VM (cloud-init) with skoed
# Run on the Proxmox host (needs `qm`).
#
# Usage:
#   ./proxmox-create-vm.sh --id 201 --hostname skoed-2 --ip 10.0.0.101/24 --gw 10.0.0.1
#
# Optional flags:
#   --storage     local       (default: local)
#   --bridge      vmbr1       (default: vmbr1 — private NAT bridge)
#   --memory      1024        (MB, default 1024)
#   --cores       2           (default 2)
#   --disk        8           (GB, default 8)
#   --image       /var/lib/vz/template/iso/debian-12-genericcloud-amd64.qcow2
#   --deb         /var/lib/vz/packages/skoed_0.1.0_amd64.deb
#   --ip          10.0.0.101/24   (static IP with prefix, required)
#   --gw          10.0.0.1        (default gateway, required)
#   --leader-api  <ip>:8080   (leader API address for cluster join)
#   --token       <join-token> (single-use token issued by leader)

set -euo pipefail

VM_ID=""
HOSTNAME=""
STORAGE="local"
BRIDGE="vmbr1"
MEMORY=1024
CORES=2
DISK=8
QCOW2="/var/lib/vz/template/iso/debian-12-genericcloud-amd64.qcow2"
DEB="/var/lib/vz/packages/skoed_0.1.0_amd64.deb"
IP=""
GW=""
LEADER_API=""
JOIN_TOKEN=""

while [ $# -gt 0 ]; do
    case "$1" in
        --id)          VM_ID="$2";      shift 2 ;;
        --hostname)    HOSTNAME="$2";   shift 2 ;;
        --storage)     STORAGE="$2";    shift 2 ;;
        --bridge)      BRIDGE="$2";     shift 2 ;;
        --memory)      MEMORY="$2";     shift 2 ;;
        --cores)       CORES="$2";      shift 2 ;;
        --disk)        DISK="$2";       shift 2 ;;
        --image)       QCOW2="$2";      shift 2 ;;
        --deb)         DEB="$2";        shift 2 ;;
        --ip)          IP="$2";         shift 2 ;;
        --gw)          GW="$2";         shift 2 ;;
        --leader-api)  LEADER_API="$2"; shift 2 ;;
        --token)       JOIN_TOKEN="$2"; shift 2 ;;
        --help|-h)     sed -n '2,22p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$VM_ID" ] || [ -z "$HOSTNAME" ] || [ -z "$IP" ] || [ -z "$GW" ]; then
    echo "usage: $0 --id <vm-id> --hostname <name> --ip <cidr> --gw <gateway> [options]" >&2
    exit 2
fi
if [ ! -r "$QCOW2" ]; then
    echo "cloud image not readable: $QCOW2" >&2
    echo "download with: wget -P /var/lib/vz/template/iso/ https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2" >&2
    exit 2
fi
if [ ! -r "$DEB" ]; then
    echo "deb not readable: $DEB" >&2
    exit 2
fi
if ! command -v qm >/dev/null 2>&1; then
    echo "this script must run on a Proxmox host (no \`qm\` in PATH)" >&2
    exit 2
fi
if qm status "$VM_ID" >/dev/null 2>&1; then
    echo "VM $VM_ID already exists — refusing to overwrite" >&2
    exit 1
fi

# Enable snippets content type on local storage if needed.
if ! pvesm status -storage local | grep -q snippets 2>/dev/null; then
    pvesm set local --content "$(pvesm status -storage local | awk 'NR==2{print $5}'),snippets" 2>/dev/null || true
fi

echo "[1/5] creating VM $VM_ID ($HOSTNAME, ${MEMORY}MB / ${CORES} cores / ${DISK}GB / $BRIDGE)…"
qm create "$VM_ID" \
    --name "$HOSTNAME" \
    --ostype l26 \
    --machine q35 \
    --cpu host \
    --cores "$CORES" \
    --memory "$MEMORY" \
    --net0 "virtio,bridge=$BRIDGE" \
    --scsihw virtio-scsi-pci \
    --serial0 socket \
    --vga serial0 \
    --agent "enabled=1" \
    --onboot 1

echo "[2/5] importing cloud image disk…"
IMPORT_OUT=$(qm importdisk "$VM_ID" "$QCOW2" "$STORAGE" --format qcow2 2>&1)
echo "$IMPORT_OUT"
DISK_VOL=$(echo "$IMPORT_OUT" | grep -oP "(?<=')[^']+" | tail -1)
if [ -z "$DISK_VOL" ]; then
    # Fallback: construct the expected volume path
    DISK_VOL="${STORAGE}:${VM_ID}/vm-${VM_ID}-disk-0.qcow2"
fi

qm set "$VM_ID" --scsi0 "${DISK_VOL},discard=on"
qm set "$VM_ID" --ide2 "${STORAGE}:cloudinit"
qm set "$VM_ID" --boot "order=scsi0"
qm disk resize "$VM_ID" scsi0 "${DISK}G"

BARE_IP="${IP%%/*}"   # strip CIDR prefix

echo "[3/5] configuring cloud-init identity and static IP…"
# Inject the Proxmox host root's OWN public key so we can SSH from this host into the VM.
# Check root's own keys first (NOT authorized_keys, which holds keys for inbound access).
SSH_PUBKEY=""
for keyfile in /root/.ssh/id_ed25519.pub /root/.ssh/id_rsa.pub; do
    if [ -r "$keyfile" ]; then
        SSH_PUBKEY=$(cat "$keyfile")
        break
    fi
done
if [ -z "$SSH_PUBKEY" ]; then
    echo "  no SSH public key for root — generating ed25519 key…"
    ssh-keygen -t ed25519 -f /root/.ssh/id_ed25519 -N '' -C 'proxmox-root' >/dev/null 2>&1
    SSH_PUBKEY=$(cat /root/.ssh/id_ed25519.pub)
fi

qm set "$VM_ID" --ciuser root --ipconfig0 "ip=${IP},gw=${GW}"
if [ -n "$SSH_PUBKEY" ]; then
    TMP_KEY=$(mktemp)
    echo "$SSH_PUBKEY" > "$TMP_KEY"
    qm set "$VM_ID" --sshkeys "$TMP_KEY"
    rm -f "$TMP_KEY"
else
    TMPPASS="skoed-$(head -c8 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c12)"
    echo "WARNING: no SSH key found; root password: $TMPPASS"
    qm set "$VM_ID" --cipassword "$TMPPASS"
fi

echo "[4/5] booting VM and waiting for SSH on $BARE_IP…"
qm start "$VM_ID"

for i in $(seq 1 90); do
    if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=3 "root@${BARE_IP}" true 2>/dev/null; then
        break
    fi
    sleep 5
done
echo "    VM IP: $BARE_IP"

echo "[5/5] installing skoed inside VM…"
# Build config on the host, SCP everything in, SSH to install.
NODE_ID="${HOSTNAME}"
TMP_CONFIG=$(mktemp)
cat > "$TMP_CONFIG" << EOF
node:
  id: ${NODE_ID}
  raft_address: ${BARE_IP}:7000
  api_address: ${BARE_IP}:8080
  data_dir: /var/lib/skoed

  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: false
EOF
if [ -n "$LEADER_API" ] && [ -n "$JOIN_TOKEN" ]; then
    # Ensure leader_address has http:// scheme — skoed rejects bare host:port
    LEADER_URL="$LEADER_API"
    case "$LEADER_URL" in http://*|https://*) ;; *) LEADER_URL="http://$LEADER_URL" ;; esac
    cat >> "$TMP_CONFIG" << EOF

bootstrap:
  leader_address: ${LEADER_URL}
  token: ${JOIN_TOKEN}
EOF
fi

scp -o StrictHostKeyChecking=no "$DEB" "root@${BARE_IP}:/tmp/skoed.deb"
scp -o StrictHostKeyChecking=no "$TMP_CONFIG" "root@${BARE_IP}:/tmp/skoed-config.yaml"
rm -f "$TMP_CONFIG"

ssh -o StrictHostKeyChecking=no "root@${BARE_IP}" bash << 'SSHEOF'
set -e
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends adduser qemu-guest-agent
dpkg -i /tmp/skoed.deb || DEBIAN_FRONTEND=noninteractive apt-get -f install -y
rm /tmp/skoed.deb
systemctl enable --now qemu-guest-agent 2>/dev/null || true

# systemd drop-in not needed for VMs (full kernel), but guard against regressions
mkdir -p /etc/skoed
cp /tmp/skoed-config.yaml /etc/skoed/config.yaml
rm /tmp/skoed-config.yaml
systemctl enable skoed
systemctl start skoed
SSHEOF

echo "waiting for /api/v1/health…"
for i in $(seq 1 60); do
    if curl -fsS "http://${BARE_IP}:8080/api/v1/health" >/dev/null 2>&1; then
        echo
        # Write Proxmox notes
        NOTE="skoed node — Debian 12 VM (cloud-init)
Hostname:     $HOSTNAME
IP:           $BARE_IP
Raft:         ${BARE_IP}:7000
API:          http://${BARE_IP}:8080
SSH:          ssh root@$BARE_IP
Service:      systemctl status skoed
Logs:         journalctl -u skoed -f
Config:       /etc/skoed/config.yaml
Data:         /var/lib/skoed"
        if [ -n "$LEADER_API" ]; then
            NOTE="${NOTE}
Cluster:      joined ${LEADER_API}"
        fi
        qm set "$VM_ID" --description "$NOTE" 2>/dev/null || true
        echo "skoed-vm $VM_ID ($HOSTNAME) is up at http://$BARE_IP:8080"
        echo "NODE_IP=$BARE_IP"
        exit 0
    fi
    sleep 2
done

echo "WARNING: /api/v1/health did not respond within 120s" >&2
echo "  check: ssh root@$BARE_IP && systemctl status skoed" >&2
exit 1
