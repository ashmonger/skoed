#!/usr/bin/env bash
# scripts/shoot-all.sh — one-shot orchestrator. Boots a 3-node skoed
# cluster + helper services (release-feed, blocklist-hosts source,
# synthetic Kea DHCP), seeds rich data on the leader, runs every
# Playwright shoot script + every vhs tape, then tears down.
#
# Re-runnable; cleans up data dirs and PIDs on exit.
#
# Output: docs/screenshots/*.png + *.gif refreshed from scratch.
#
# Usage:  scripts/shoot-all.sh
# Prereqs: ttyd, ffmpeg, vhs, aha (for ANSI screenshots), node (web/)

set -uo pipefail
# NOTE: `set -e` deliberately off — optional shoot scripts may fail
# (router paths drift, missing selectors); we want to continue past
# them so steps 6-7 (CLI+TUI GIFs) always run.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# ---- Ports ----
LEADER_API=18499
LEADER_DNS=18453
LEADER_RAFT=18421
N2_API=18599
N2_DNS=18553
N2_RAFT=18521
N3_API=18699
N3_DNS=18653
N3_RAFT=18621
FEED_PORT=18811
HOSTS_PORT=18815
KEA_PORT=18820
EMPTY_API=18799
EMPTY_DNS=18753
EMPTY_RAFT=18721

# ---- Paths ----
WORK="$HOME/tmp/skoed-shootall"
BIN="$ROOT/apps/skoed/skoed"
SHOTS="$ROOT/docs/screenshots"
PIDS="$WORK/pids"

mkdir -p "$WORK" "$SHOTS" "$PIDS"
> "$PIDS/all"

cleanup() {
    set +e
    if [ -f "$PIDS/all" ]; then
        while read -r pid; do
            kill "$pid" 2>/dev/null
        done < "$PIDS/all"
    fi
    sleep 1
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# ---- 0. Sanity ----
PATH="$HOME/.asdf/installs/golang/1.26.1/bin:$PATH"  # vhs lives here

test -x "$BIN" || { echo "binary missing at $BIN — run make build" >&2; exit 1; }
command -v vhs >/dev/null || { echo "vhs missing (charmbracelet/vhs)" >&2; exit 1; }
command -v aha >/dev/null || { echo "aha missing (apt install aha)" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 missing" >&2; exit 1; }

# ---- 1. Helper servers ----
echo ">>> [1/8] helper servers"

# Release feed for the M5.6 upgrade banner
mkdir -p "$WORK/feed"
cat > "$WORK/feed/feed.json" <<'EOF'
{
  "version": "0.9.0",
  "published_at": "2026-07-01T09:00:00Z",
  "release_notes_url": "https://github.com/skoed/skoed/releases/tag/v0.9.0"
}
EOF
(cd "$WORK/feed" && python3 -m http.server "$FEED_PORT" >/dev/null 2>&1) &
echo $! >> "$PIDS/all"

# Hosts-format URL blocklist source
mkdir -p "$WORK/hosts"
cat > "$WORK/hosts/hosts.txt" <<'EOF'
0.0.0.0 doubleclick.net
0.0.0.0 googletagmanager.com
0.0.0.0 tracker.example
0.0.0.0 ads.example
0.0.0.0 pixel.example
0.0.0.0 analytics.example
0.0.0.0 telemetry.example
EOF
cp "$WORK/hosts/hosts.txt" "$WORK/hosts/dead.txt"
cat > "$WORK/hosts/easy.txt" <<'EOF'
||affiliate.example^
||pop.example^
||metrics.example^
EOF
(cd "$WORK/hosts" && python3 -m http.server "$HOSTS_PORT" >/dev/null 2>&1) &
echo $! >> "$PIDS/all"

# Synthetic Kea control-agent for M3.6 DHCP integration
cat > "$WORK/kea.py" <<KEAPY
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

LEASES = [
    {"ip-address":"10.42.10.20","hw-address":"aa:bb:cc:dd:ee:01","hostname":"nas.lab",
     "client-id":"01:aa:bb:cc:dd:ee:01","valid-lft":86400,"cltt":1750000000},
    {"ip-address":"10.42.10.21","hw-address":"aa:bb:cc:dd:ee:02","hostname":"printer.lab",
     "client-id":"01:aa:bb:cc:dd:ee:02","valid-lft":86400,"cltt":1750000000},
    {"ip-address":"10.42.10.50","hw-address":"aa:bb:cc:dd:ee:50","hostname":"kid-laptop",
     "client-id":"01:aa:bb:cc:dd:ee:50","valid-lft":86400,"cltt":1750000000},
    {"ip-address":"10.42.10.51","hw-address":"aa:bb:cc:dd:ee:51","hostname":"kid-tablet",
     "client-id":"01:aa:bb:cc:dd:ee:51","valid-lft":86400,"cltt":1750000000},
    {"ip-address":"10.42.10.60","hw-address":"aa:bb:cc:dd:ee:60","hostname":"living-room-tv",
     "client-id":"01:aa:bb:cc:dd:ee:60","valid-lft":86400,"cltt":1750000000},
    {"ip-address":"10.42.10.99","hw-address":"aa:bb:cc:dd:ee:99","hostname":"guest-phone",
     "client-id":"01:aa:bb:cc:dd:ee:99","valid-lft":3600,"cltt":1750000000},
]

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        ln = int(self.headers.get('content-length','0'))
        body = self.rfile.read(ln) if ln else b''
        cmd = json.loads(body) if body else {}
        if cmd.get('command') == 'lease4-get-all':
            resp = [{"result":0,"text":"ok","arguments":{"leases":LEASES}}]
        else:
            resp = [{"result":1,"text":"unknown command"}]
        b = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def log_message(self,*a,**k): pass

HTTPServer(('127.0.0.1',$KEA_PORT),H).serve_forever()
KEAPY
python3 "$WORK/kea.py" >/dev/null 2>&1 &
echo $! >> "$PIDS/all"

sleep 1
curl -fsS "http://127.0.0.1:$FEED_PORT/feed.json" >/dev/null && echo "  feed ok"
curl -fsS "http://127.0.0.1:$HOSTS_PORT/hosts.txt" >/dev/null && echo "  hosts ok"
curl -fsS -X POST "http://127.0.0.1:$KEA_PORT/" -H 'content-type: application/json' \
    -d '{"command":"lease4-get-all"}' >/dev/null && echo "  kea ok"

# ---- 2. 3-node skoed cluster ----
echo ">>> [2/8] 3-node cluster"

write_config() {
    local id=$1 raft=$2 api=$3 dns=$4 dir=$5 dhcp_kind=${6:-} bootstrap_addr=${7:-} bootstrap_token=${8:-}
    cat > "$dir/config.yaml" <<YAML
node:
  id: $id
  raft_address: 127.0.0.1:$raft
  api_address: 127.0.0.1:$api
  data_dir: $dir
  dns:
    listen:
      port: $dns
      ipv4: true
      ipv6: false
YAML
    if [ -n "$dhcp_kind" ]; then
        cat >> "$dir/config.yaml" <<YAML
  dhcp:
    enabled: true
    kind: kea
    url: http://127.0.0.1:$KEA_PORT/
    refresh_seconds: 5
YAML
    fi
    if [ -n "$bootstrap_addr" ]; then
        cat >> "$dir/config.yaml" <<YAML
bootstrap:
  leader_address: $bootstrap_addr
  token: $bootstrap_token
YAML
    fi
}

# Node 1 — leader (has DHCP enabled for M3.6 shots)
N1_DIR="$WORK/n1"
mkdir -p "$N1_DIR"
write_config skoed-1 "$LEADER_RAFT" "$LEADER_API" "$LEADER_DNS" "$N1_DIR" kea
SKOED_UPGRADE_FEED_URL="http://127.0.0.1:$FEED_PORT/feed.json" \
    "$BIN" --config "$N1_DIR/config.yaml" >"$N1_DIR/skoed.log" 2>&1 &
echo $! >> "$PIDS/all"
sleep 3

curl -fsS -X POST "http://127.0.0.1:$LEADER_API/api/v1/auth/setup" \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"demopass123"}' >/dev/null
echo "  node-1 leader up + auth set"

issue_token() {
    curl -fsS -u admin:demopass123 -X POST \
        "http://127.0.0.1:$LEADER_API/api/v1/cluster/tokens" \
        | python3 -c "import json,sys; print(json.load(sys.stdin)['token'])"
}

# Node 2 + Node 3 — followers, also DHCP-enabled so the Clients page
# is populated cluster-wide
for idx in 2 3; do
    case $idx in
        2) NDIR="$WORK/n2"; RAFT=$N2_RAFT; API=$N2_API; DNS=$N2_DNS ;;
        3) NDIR="$WORK/n3"; RAFT=$N3_RAFT; API=$N3_API; DNS=$N3_DNS ;;
    esac
    mkdir -p "$NDIR"
    TOKEN=$(issue_token)
    write_config "skoed-$idx" "$RAFT" "$API" "$DNS" "$NDIR" kea \
        "http://127.0.0.1:$LEADER_API" "$TOKEN"
    SKOED_UPGRADE_FEED_URL="http://127.0.0.1:$FEED_PORT/feed.json" \
        "$BIN" --config "$NDIR/config.yaml" >"$NDIR/skoed.log" 2>&1 &
    echo $! >> "$PIDS/all"
    sleep 3
    echo "  node-$idx joined"
done

# Wait for convergence
for _ in 1 2 3 4 5 6 7 8 9 10; do
    members=$(curl -fsS -u admin:demopass123 "http://127.0.0.1:$LEADER_API/api/v1/cluster/health" \
        | python3 -c "import json,sys; print(json.load(sys.stdin)['members'])" 2>/dev/null || echo 0)
    [ "$members" -ge 3 ] && break
    sleep 1
done
echo "  cluster converged: $members members"

# ---- 3. Seed rich data on the leader ----
echo ">>> [3/8] seed rich data"

BASE="http://127.0.0.1:$LEADER_API"
AUTH="admin:demopass123"
post() { curl -fsS -u "$AUTH" -X POST "$BASE$1" -H 'content-type: application/json' -d "$2" >/dev/null; }
postq() { curl -fsS -u "$AUTH" -X POST "$BASE$1" -H 'content-type: application/json' -d "$2" >/dev/null 2>&1 || true; }

post /api/v1/blocklists '{"id":"hagezi-pro","name":"Hagezi Pro","source":{"type":"url","url":"http://127.0.0.1:'"$HOSTS_PORT"'/hosts.txt","format":"hosts"},"refresh_interval_seconds":86400}'
post /api/v1/blocklists '{"id":"adblock-easy","name":"EasyList","source":{"type":"url","url":"http://127.0.0.1:'"$HOSTS_PORT"'/easy.txt","format":"adblock"},"refresh_interval_seconds":3600}'
post /api/v1/blocklists '{"id":"stale-feed","name":"Stale feed","source":{"type":"url","url":"http://127.0.0.1:'"$HOSTS_PORT"'/dead.txt","format":"hosts"},"refresh_interval_seconds":2}'
echo "  blocklists added"

for d in www.example.com github.io golang.org news.ycombinator.com; do
    post /api/v1/allowlist '{"domain":"'"$d"'"}'
done
echo "  allowlist seeded"

for spec in 'nas.lab|A|10.42.10.20' 'printer.lab|A|10.42.10.21' 'router.lab|A|10.42.10.1' 'unifi.lab|A|10.42.10.2'; do
    h=${spec%%|*}; rest=${spec#*|}; t=${rest%%|*}; v=${rest#*|}
    post /api/v1/local-dns '{"hostname":"'"$h"'","type":"'"$t"'","value":"'"$v"'","ttl":300}'
done
echo "  local DNS seeded"

post /api/v1/profiles '{"id":"kids","name":"Kids","blocklists":["hagezi-pro","cat:doh"],"client_ips":["10.42.10.50","10.42.10.51"],"client_macs":["aa:bb:cc:dd:ee:50","aa:bb:cc:dd:ee:51"],"safesearch":["google","youtube"]}'
post /api/v1/profiles '{"id":"guests","name":"Guests","blocklists":["hagezi-pro"],"client_cidrs":["10.42.20.0/24"]}'
post /api/v1/profiles '{"id":"iot","name":"IoT","blocklists":["adblock-easy"],"client_hostnames":["living-room-tv"]}'
echo "  profiles seeded"

post /api/v1/schedules '{"id":"bedtime","name":"Kids bedtime","mode":"block_only_inside","windows":[{"days":["Mon","Tue","Wed","Thu","Sun"],"start":"21:00","end":"07:00"}]}'
post /api/v1/schedules/bedtime/bindings '{"profile_id":"kids","blocklist_id":"hagezi-pro"}'
echo "  schedules seeded"

# Fire some queries from various synthetic client IPs so:
#  - stats panel + top-blocked populated
#  - query log has entries to filter
#  - per-client DoH alert may surface
for c in 10.42.10.20 10.42.10.50 10.42.10.60 10.42.10.99; do
    for d in doubleclick.net googletagmanager.com tracker.example example.com github.com nas.lab; do
        for _ in 1 2 3; do
            dig @127.0.0.1 -p $LEADER_DNS -b "$c" "$d" +short +time=1 +tries=1 >/dev/null 2>&1 || true
        done
    done
done
echo "  queries fired"

sleep 7   # let stale-feed cross its 2s × 2 threshold; let stats settle
echo "  done"

# ---- 4. Empty-state node for M5.9.4 getting-started ----
echo ">>> [4/8] empty-state node"
EMPTY_DIR="$WORK/empty"
mkdir -p "$EMPTY_DIR"
write_config skoed-empty "$EMPTY_RAFT" "$EMPTY_API" "$EMPTY_DNS" "$EMPTY_DIR"
"$BIN" --config "$EMPTY_DIR/config.yaml" >"$EMPTY_DIR/skoed.log" 2>&1 &
echo $! >> "$PIDS/all"
sleep 3
curl -fsS -X POST "http://127.0.0.1:$EMPTY_API/api/v1/auth/setup" \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"demopass123"}' >/dev/null
echo "  empty node ready"

# ---- 5. SPA shoots ----
echo ">>> [5/8] SPA shoots"

(cd web && \
    SKOED_BASE_URL="http://127.0.0.1:$LEADER_API" node shoot-milestones.mjs 2>&1 \
    | grep -E "saved|error" | sed 's/^/  /')
(cd web && \
    SKOED_BASE_URL="http://127.0.0.1:$LEADER_API" node shoot-about.mjs 2>&1 \
    | grep -E "saved|error" | sed 's/^/  /')

# Focused feature shots — these scripts have not all been updated for
# M5.9.5's router restructure (/page → /dashboard/page). Tolerate
# per-script failures and move on; the m5.9-* set above already covers
# every page top-level.
run_optional() {
    local script=$1
    local base=$2
    [ -f "web/$script" ] || return 0
    echo "  → $script"
    ( cd web && SKOED_BASE_URL="$base" DBLOCK_BASE_URL="$base" \
        node "$script" 2>&1 | sed 's/^/    /' ) || echo "    (script failed — continuing)"
    return 0
}

for s in shoot-m3.6.mjs shoot-m4x.mjs shoot-m5.mjs shoot-m5.4.mjs shoot-m5.6.mjs shoot-m5.9.5.mjs; do
    run_optional "$s" "http://127.0.0.1:$LEADER_API"
done
run_optional shoot-m5.9.4.mjs "http://127.0.0.1:$EMPTY_API"

# Social preview
echo "  → shoot-social.mjs"
(cd web && node shoot-social.mjs 2>&1 | sed 's/^/    /')

# ---- 6. CLI verbs composite + GIF ----
echo ">>> [6/8] CLI verbs"

# vhs tape: CLI walkthrough
cat > "$SHOTS/m5.9.1-cli.tape" <<TAPE
Output ../../docs/screenshots/m5.9.1-skoed-cli.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1100
Set Height 720
Set TypingSpeed 60ms
Set Padding 24
Set PlaybackSpeed 1.5

Hide
Type "export PS1='$ '"
Enter
Type "export SKOED_AUTH=admin:demopass123"
Enter
Type "alias skoed='$BIN --api http://127.0.0.1:$LEADER_API'"
Enter
Type "clear"
Enter
Show

Type "skoed --version"
Enter
Sleep 1500ms

Type "skoed health"
Enter
Sleep 2500ms

Type "skoed status"
Enter
Sleep 2500ms

Type "skoed token create"
Enter
Sleep 3500ms
TAPE

(cd "$SHOTS" && vhs m5.9.1-cli.tape 2>&1 | grep -iE "creating|error" | sed 's/^/  /')

# Static PNG composite via aha+playwright (operator-friendly thumbnail)
{
    echo "$ skoed --help"
    echo
    "$BIN" --help 2>&1
    echo
    echo "$ skoed --version"
    "$BIN" --version 2>&1
    echo
    echo "$ SKOED_AUTH=admin:demopass123 skoed health --api http://127.0.0.1:$LEADER_API"
    SKOED_AUTH=admin:demopass123 CLICOLOR_FORCE=1 "$BIN" health --api "http://127.0.0.1:$LEADER_API" 2>&1
    echo
    echo "$ skoed status"
    SKOED_AUTH=admin:demopass123 CLICOLOR_FORCE=1 "$BIN" status --api "http://127.0.0.1:$LEADER_API" 2>&1
    echo
    echo "$ skoed token create"
    SKOED_AUTH=admin:demopass123 CLICOLOR_FORCE=1 "$BIN" token create --api "http://127.0.0.1:$LEADER_API" 2>&1
} | aha --black --no-header > /tmp/skoed-cli.html

{
cat <<'HTML'
<!doctype html>
<html><head><meta charset="utf-8"><style>
  html,body { margin:0; padding:0; background:#0F0F18; color:#E0E0F0; font-family: 'Fira Code', 'DejaVu Sans Mono', monospace; font-size: 14px; line-height: 1.4; }
  .wrap { padding: 32px; max-width: 1100px; }
  pre { white-space: pre; margin: 0; }
  span[style*="#3333FF"] { color: #874BFD !important; background: transparent !important; }
  span[style*="background-color:#3333FF"] { background: #874BFD !important; color: #0F0F18 !important; }
  span[style*="color:lime"] { color: #20D998 !important; }
  span[style*="color:red"]  { color: #EB4444 !important; }
  span[style*="color:dimgray"] { color: #7C7C7C !important; }
</style></head><body><div class="wrap"><pre>
HTML
cat /tmp/skoed-cli.html
echo '</pre></div></body></html>'; } > /tmp/skoed-cli-page.html

(cd web && HTML=/tmp/skoed-cli-page.html \
    OUT="$SHOTS/m5.9.1-skoed-cli.png" W=1100 H=1100 \
    node shoot-html.mjs 2>&1 | sed 's/^/  /')

# ---- 7. TUI GIF + static snapshot ----
echo ">>> [7/8] TUI"

cat > "$SHOTS/m5.9.1-top.tape" <<TAPE
Output ../../docs/screenshots/m5.9.1-skoed-top.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1000
Set Height 760
Set Padding 24
Set TypingSpeed 60ms

Hide
Type "export SKOED_AUTH=admin:demopass123"
Enter
Type "export SKOED_API=http://127.0.0.1:$LEADER_API"
Enter
Type "alias skoed='$BIN'"
Enter
Type "clear"
Enter
Show

Type "skoed top"
Sleep 200ms
Enter
Sleep 6s
Type "r"
Sleep 3s
Type "q"
Sleep 500ms
TAPE

(cd "$SHOTS" && vhs m5.9.1-top.tape 2>&1 | grep -iE "creating|error" | sed 's/^/  /')

# TUI static snapshot via --snapshot mode
SKOED_AUTH=admin:demopass123 CLICOLOR_FORCE=1 \
    "$BIN" top --api "http://127.0.0.1:$LEADER_API" --snapshot 2>&1 \
    | aha --black --no-header > /tmp/skoed-top.html

{
cat <<'HTML'
<!doctype html>
<html><head><meta charset="utf-8"><style>
  html,body { margin:0; padding:0; background:#0F0F18; color:#E0E0F0; font-family: 'Fira Code', 'DejaVu Sans Mono', monospace; font-size: 16px; line-height: 1.4; }
  .wrap { padding: 32px; max-width: 900px; }
  pre { white-space: pre; margin: 0; }
  span[style*="#3333FF"] { color: #874BFD !important; background: transparent !important; }
  span[style*="background-color:#3333FF"] { background: #874BFD !important; color: #0F0F18 !important; }
  span[style*="color:lime"] { color: #20D998 !important; }
  span[style*="color:red"]  { color: #EB4444 !important; }
  span[style*="color:dimgray"] { color: #7C7C7C !important; }
</style></head><body><div class="wrap"><pre>
HTML
cat /tmp/skoed-top.html
echo '</pre></div></body></html>'; } > /tmp/skoed-top-page.html

(cd web && HTML=/tmp/skoed-top-page.html \
    OUT="$SHOTS/m5.9.1-skoed-top.png" W=900 H=700 \
    node shoot-html.mjs 2>&1 | sed 's/^/  /')

# ---- 8. Summary ----
echo ">>> [8/8] done"
ls -1 "$SHOTS"/*.png "$SHOTS"/*.gif 2>/dev/null | wc -l \
    | xargs -I{} echo "  {} artefacts in $SHOTS"
