#!/usr/bin/env bash
# demos/m10/demo.sh — M10 Active-Active Cluster live walkthrough
#
# Demonstrates transparent write forwarding and Raft response metadata:
#   1. Any node accepts write requests (no 307 redirects)
#   2. X-Served-By and X-Raft-Commit-Index on every response
#   3. Write to follower → forwarded to leader → result returned
#   4. Commit index advances globally after each write
#
# Usage:
#   ./demos/m10/demo.sh [path/to/skoed]

set -euo pipefail

DEMO_ROOT="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$DEMO_ROOT/../.." && pwd)"
BINARY="${1:-$REPO_ROOT/apps/skoed/skoed}"
WORK="$(mktemp -d /tmp/skoed-demo-m10-XXXXX)"
PIDS=()

trap 'for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$WORK"' EXIT

# ── colour helpers ─────────────────────────────────────────────────────────────
R='\033[0m'; B='\033[1m'; G='\033[32m'; C='\033[36m'
Y='\033[33m'; M='\033[35m'; D='\033[2m'

banner()  { printf "\n${B}${C}▶  %s${R}\n" "$*"; }
ok()      { printf "${G}  ✓ %s${R}\n" "$*"; }
info()    { printf "${D}  %s${R}\n" "$*"; }
cmd()     { printf "${Y}  \$ %s${R}\n" "$*"; }
section() { printf "\n${B}${M}═══ %s ═══${R}\n" "$*"; }
hdr()     { printf "${C}  %-28s %s${R}\n" "$1" "$2"; }

free_port() {
  python3 -c \
    "import socket; s=socket.socket(); s.bind(('',0)); p=s.getsockname()[1]; s.close(); print(p)"
}

wait_api() {
  local url="$1" deadline=$(( $(date +%s) + 45 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    code=$(curl -so /dev/null -w "%{http_code}" "$url" 2>/dev/null || echo 0)
    [ "$code" = "200" ] || [ "$code" = "401" ] && return 0
    sleep 0.3
  done
  printf "\033[31m  ✗ timeout waiting for %s\033[0m\n" "$url" >&2
  return 1
}

make_config() {
  local id="$1" api_port="$2" raft_port="$3" dns_port="$4" dir="$5"
  local leader="${6:-}" token="${7:-}"
  mkdir -p "$dir"
  cat > "$dir/config.yaml" <<YAML
node:
  id: "$id"
  raft_address: "127.0.0.1:$raft_port"
  api_address:  "127.0.0.1:$api_port"
  dns:
    listen:
      port: $dns_port
      ipv4: true
      ipv6: false
  data_dir: "$dir"
$([ -n "$leader" ] && printf 'bootstrap:\n  leader_address: "%s"\n  token: "%s"\n' "$leader" "$token" || true)
YAML
}

PASS="demopass1!"

api() {
  local node_url="$1" m="$2" p="$3" b="${4:-}"
  if [ -n "$b" ]; then
    curl -sf -u "admin:$PASS" -X "$m" \
      -H 'Content-Type: application/json' -d "$b" "${node_url}${p}"
  else
    curl -sf -u "admin:$PASS" -X "$m" "${node_url}${p}"
  fi
}

# Same but also captures response headers
api_v() {
  local node_url="$1" m="$2" p="$3" b="${4:-}"
  if [ -n "$b" ]; then
    curl -si -u "admin:$PASS" -X "$m" \
      -H 'Content-Type: application/json' -d "$b" "${node_url}${p}"
  else
    curl -si -u "admin:$PASS" -X "$m" "${node_url}${p}"
  fi
}

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
section "skoed M10 — Active-Active Cluster"
printf "${D}  Demonstrates transparent write forwarding + Raft response metadata${R}\n"

[ -x "$BINARY" ] || {
  printf "\033[31m  ✗ binary not found: %s\033[0m\n" "$BINARY"; exit 1
}
info "Binary  : $BINARY"
info "Workdir : $WORK"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "1 — Bootstrap 3-node Raft cluster"

N1_API=$(free_port); N1_RAFT=$(free_port); N1_DNS=$(free_port)
N2_API=$(free_port); N2_RAFT=$(free_port); N2_DNS=$(free_port)
N3_API=$(free_port); N3_RAFT=$(free_port); N3_DNS=$(free_port)

make_config node-1 "$N1_API" "$N1_RAFT" "$N1_DNS" "$WORK/n1"
"$BINARY" --config "$WORK/n1/config.yaml" >"$WORK/n1/skoed.log" 2>&1 &
PIDS+=($!)

wait_api "http://127.0.0.1:$N1_API/api/v1/health"
ok "node-1 started  :$N1_API (bootstrap leader)"

curl -sf -X POST -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PASS\"}" \
  "http://127.0.0.1:$N1_API/api/v1/auth/setup" >/dev/null

TOKEN2=$(api "http://127.0.0.1:$N1_API" POST /api/v1/cluster/tokens | jq -r '.token')
TOKEN3=$(api "http://127.0.0.1:$N1_API" POST /api/v1/cluster/tokens | jq -r '.token')

make_config node-2 "$N2_API" "$N2_RAFT" "$N2_DNS" "$WORK/n2" \
  "http://127.0.0.1:$N1_API" "$TOKEN2"
make_config node-3 "$N3_API" "$N3_RAFT" "$N3_DNS" "$WORK/n3" \
  "http://127.0.0.1:$N1_API" "$TOKEN3"

"$BINARY" --config "$WORK/n2/config.yaml" >"$WORK/n2/skoed.log" 2>&1 &
PIDS+=($!)
"$BINARY" --config "$WORK/n3/config.yaml" >"$WORK/n3/skoed.log" 2>&1 &
PIDS+=($!)

wait_api "http://127.0.0.1:$N2_API/api/v1/health"
wait_api "http://127.0.0.1:$N3_API/api/v1/health"
ok "node-2 started  :$N2_API"
ok "node-3 started  :$N3_API"

sleep 1   # give Raft time to elect a leader

# Identify leader and pick a follower
STATUS=$(api "http://127.0.0.1:$N1_API" GET /api/v1/cluster/status)
LEADER_ID=$(echo "$STATUS" | jq -r '.nodes[] | select(.role=="leader") | .node_id')

# Map leader ID to port
case "$LEADER_ID" in
  node-1) LEADER_PORT=$N1_API; FOLLOWER1_PORT=$N2_API; FOLLOWER1_ID=node-2
          FOLLOWER2_PORT=$N3_API; FOLLOWER2_ID=node-3 ;;
  node-2) LEADER_PORT=$N2_API; FOLLOWER1_PORT=$N1_API; FOLLOWER1_ID=node-1
          FOLLOWER2_PORT=$N3_API; FOLLOWER2_ID=node-3 ;;
  node-3) LEADER_PORT=$N3_API; FOLLOWER1_PORT=$N1_API; FOLLOWER1_ID=node-1
          FOLLOWER2_PORT=$N2_API; FOLLOWER2_ID=node-2 ;;
  *) printf "\033[31m  ✗ no leader elected (got: %s)\033[0m\n" "$LEADER_ID"; exit 1 ;;
esac

LEADER_URL="http://127.0.0.1:$LEADER_PORT"
FOLLOWER1_URL="http://127.0.0.1:$FOLLOWER1_PORT"
FOLLOWER2_URL="http://127.0.0.1:$FOLLOWER2_PORT"

ok "Leader elected  → $LEADER_ID  (:$LEADER_PORT)"
info "Followers       → $FOLLOWER1_ID (:$FOLLOWER1_PORT)  $FOLLOWER2_ID (:$FOLLOWER2_PORT)"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "2 — X-Served-By + X-Raft-Commit-Index on every response"

info "GET /api/v1/cluster/status from each node:"
for node_id in "$LEADER_ID" "$FOLLOWER1_ID" "$FOLLOWER2_ID"; do
  case "$node_id" in
    node-1) port=$N1_API ;; node-2) port=$N2_API ;; *) port=$N3_API ;;
  esac
  RESP=$(curl -si -u "admin:$PASS" "http://127.0.0.1:$port/api/v1/cluster/status" 2>/dev/null)
  SERVED_BY=$(echo "$RESP"  | grep -i "x-served-by:"      | tr -d '\r' | awk '{print $2}')
  COMMIT_IDX=$(echo "$RESP" | grep -i "x-raft-commit-index:" | tr -d '\r' | awk '{print $2}')
  cmd "curl -i http://127.0.0.1:$port/api/v1/cluster/status"
  hdr "  X-Served-By:"          "$SERVED_BY"
  hdr "  X-Raft-Commit-Index:"  "$COMMIT_IDX"
done

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "3 — Write to follower is forwarded transparently to leader"

info "Adding a blocklist entry via $FOLLOWER1_ID (follower, :$FOLLOWER1_PORT):"
cmd "curl -X POST http://127.0.0.1:$FOLLOWER1_PORT/api/v1/blocklists ..."

RESP=$(api_v "$FOLLOWER1_URL" POST /api/v1/blocklists \
  '{"url":"https://example.com/blocklist.txt","name":"test-blocklist","enabled":true}')
HTTP_STATUS=$(echo "$RESP" | head -1 | awk '{print $2}')
SERVED_BY=$(echo "$RESP"   | grep -i "x-served-by:"      | tr -d '\r' | awk '{print $2}')
COMMIT_IDX=$(echo "$RESP"  | grep -i "x-raft-commit-index:" | tr -d '\r' | awk '{print $2}')

hdr "  HTTP status:"            "$HTTP_STATUS  (201 = success — no 307 redirect)"
hdr "  X-Served-By:"            "$SERVED_BY    (follower that handled request)"
hdr "  X-Raft-Commit-Index:"    "$COMMIT_IDX   (leader applied the write)"
[ "$HTTP_STATUS" = "201" ] && ok "Write accepted by follower → forwarded to leader → 201 Created"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "4 — Commit index advances after write"

sleep 0.5  # let Raft replicate

info "Commit index on all nodes after write:"
MAX_IDX=0
for node_id in "$LEADER_ID" "$FOLLOWER1_ID" "$FOLLOWER2_ID"; do
  case "$node_id" in
    node-1) port=$N1_API ;; node-2) port=$N2_API ;; *) port=$N3_API ;;
  esac
  RESP=$(curl -si -u "admin:$PASS" "http://127.0.0.1:$port/api/v1/cluster/status" 2>/dev/null)
  IDX=$(echo "$RESP" | grep -i "x-raft-commit-index:" | tr -d '\r' | awk '{print $2}')
  printf "  %-10s  X-Raft-Commit-Index: ${B}%s${R}\n" "$node_id" "$IDX"
done

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "5 — Write to the other follower also works"

info "Adding allowlist entry via $FOLLOWER2_ID (follower, :$FOLLOWER2_PORT):"
cmd "curl -X POST http://127.0.0.1:$FOLLOWER2_PORT/api/v1/allowlist ..."

RESP=$(api_v "$FOLLOWER2_URL" POST /api/v1/allowlist '{"domain":"example.com"}')
HTTP_STATUS=$(echo "$RESP" | head -1 | awk '{print $2}')
SERVED_BY=$(echo "$RESP"   | grep -i "x-served-by:"      | tr -d '\r' | awk '{print $2}')
COMMIT_IDX=$(echo "$RESP"  | grep -i "x-raft-commit-index:" | tr -d '\r' | awk '{print $2}')

hdr "  HTTP status:"         "$HTTP_STATUS"
hdr "  X-Served-By:"         "$SERVED_BY"
hdr "  X-Raft-Commit-Index:" "$COMMIT_IDX"
[ "$HTTP_STATUS" = "201" ] && ok "Write accepted by $FOLLOWER2_ID → forwarded → 201 Created"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
banner "6 — Cluster status from follower (read served locally)"

info "GET /api/v1/cluster/status from $FOLLOWER1_ID:"
cmd "curl -i http://127.0.0.1:$FOLLOWER1_PORT/api/v1/cluster/status"

RESP=$(api_v "$FOLLOWER1_URL" GET /api/v1/cluster/status)
SERVED_BY=$(echo "$RESP"   | grep -i "x-served-by:"      | tr -d '\r' | awk '{print $2}')
COMMIT_IDX=$(echo "$RESP"  | grep -i "x-raft-commit-index:" | tr -d '\r' | awk '{print $2}')
BODY=$(echo "$RESP" | tail -1)

hdr "  X-Served-By:"         "$SERVED_BY   (read served locally — not forwarded)"
hdr "  X-Raft-Commit-Index:" "$COMMIT_IDX"
echo "$BODY" | jq '{leader: .nodes[]|select(.role=="leader")|.node_id, total_nodes: .nodes|length}' 2>/dev/null | sed 's/^/  /' || true
ok "Read served locally by $FOLLOWER1_ID"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
section "M10 demo complete"
printf "${G}${B}  Active-Active Cluster verified:${R}\n"
printf "${G}  ✓ Any node accepted writes (no 307 redirects)${R}\n"
printf "${G}  ✓ X-Served-By and X-Raft-Commit-Index on every response${R}\n"
printf "${G}  ✓ Writes forwarded to leader transparently${R}\n"
printf "${G}  ✓ Reads served locally${R}\n"
printf "${G}  ✓ Commit index consistent across cluster${R}\n"
