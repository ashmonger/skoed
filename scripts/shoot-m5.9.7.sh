#!/usr/bin/env bash
# scripts/shoot-m5.9.7.sh — focused, fast capture of the M5.9.7
# "Would this domain be blocked?" surfaces.
#
# Boots a single skoed node + a tiny hosts-format blocklist source,
# seeds one blocked domain + a "kids" profile, runs the Playwright
# shoot script, then tears everything down.
#
# Useful when you only need the M5.9.7 artefacts and don't want to
# pay the cost of the full 3-node orchestrator (shoot-all.sh).
#
# Output:
#   docs/screenshots/m5.9.7-landing-domain-card.png
#   docs/screenshots/m5.9.7-test-domain-tool.png

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API=18497
DNS=18453
RAFT=18421
HOSTS_PORT=18495

WORK="$HOME/tmp/skoed-shoot-m5.9.7"
BIN="$ROOT/apps/skoed/skoed"
PIDS="$WORK/pids"

mkdir -p "$WORK" "$PIDS"
> "$PIDS/all"

cleanup() {
    set +e
    if [ -f "$PIDS/all" ]; then
        while read -r pid; do
            [ -n "$pid" ] && kill "$pid" 2>/dev/null
        done < "$PIDS/all"
        sleep 0.5
        while read -r pid; do
            [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null
        done < "$PIDS/all"
    fi
    # Belt and braces: SIGTERM via PID sometimes leaves python alive (the
    # shell job points at a wrapper PID, not python's). Match by port.
    pkill -9 -f "python3 -m http.server $HOSTS_PORT" 2>/dev/null
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# ---- 1. Build binary if missing ----
if [ ! -x "$BIN" ]; then
    echo ">>> [1/5] building skoed binary"
    (cd "$ROOT" && CGO_ENABLED=0 make build >/dev/null)
fi

# ---- 2. Hosts blocklist source ----
echo ">>> [2/5] hosts source"
mkdir -p "$WORK/hosts"
cat > "$WORK/hosts/blocked.txt" <<HOSTS
0.0.0.0 doubleclick.net
0.0.0.0 googletagmanager.com
0.0.0.0 tracker.example
HOSTS
cd "$WORK/hosts" && python3 -m http.server "$HOSTS_PORT" >/dev/null 2>&1 &
echo $! >> "$PIDS/all"
cd "$ROOT"

# ---- 3. skoed single node ----
echo ">>> [3/5] skoed node on :$API"
mkdir -p "$WORK/n1"
cat > "$WORK/n1/config.yaml" <<YAML
node:
  id: skoed-1
  raft_address: 127.0.0.1:$RAFT
  api_address: 127.0.0.1:$API
  data_dir: $WORK/n1
  dns:
    listen:
      port: $DNS
      ipv4: true
      ipv6: false
YAML

"$BIN" --config "$WORK/n1/config.yaml" >"$WORK/n1/skoed.log" 2>&1 &
echo $! >> "$PIDS/all"
sleep 2

curl -fsS -X POST "http://127.0.0.1:$API/api/v1/auth/setup" \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"demopass123"}' >/dev/null
echo "  auth set"

# ---- 4. Seed minimum data ----
echo ">>> [4/5] seed"
auth='-u admin:demopass123 -H content-type:application/json'
# Use a manual blocklist (domains inline) instead of a URL source.
# Manual blocklists land in cfg.Filtering.Blocklists[].Domains in the
# same Raft apply, so the engine has them ready as soon as POST returns.
# URL sources need a second roundtrip to populate Domains and have shown
# replication-vs-fetch races in this script.
# shellcheck disable=SC2086
curl -fsS $auth -X POST "http://127.0.0.1:$API/api/v1/blocklists" \
    -d '{"id":"hagezi-pro","name":"Hagezi Pro","source":{"type":"manual"},"domains":["doubleclick.net","googletagmanager.com","tracker.example"]}' >/dev/null
# shellcheck disable=SC2086
curl -fsS $auth -X POST "http://127.0.0.1:$API/api/v1/profiles" \
    -d '{"id":"kids","name":"Kids","blocklists":["hagezi-pro"],"client_ips":["10.42.10.50"]}' >/dev/null
# Also attach hagezi-pro to the default profile so the guest landing
# tester (which always evaluates against default) shows "Blocked"
# instead of "Allowed → forwarded".
# shellcheck disable=SC2086
curl -fsS $auth -X PATCH "http://127.0.0.1:$API/api/v1/profiles/default" \
    -d '{"blocklists":["hagezi-pro"]}' >/dev/null
# Verify the verdict is actually "blocked" before we shoot — bail
# loudly otherwise so we never produce a misleading screenshot.
seeded=0
for _ in 1 2 3 4 5; do
    # shellcheck disable=SC2086
    if curl -fsS $auth -X POST "http://127.0.0.1:$API/api/v1/test-domain" \
        -d '{"domain":"doubleclick.net"}' | grep -q '"would_block":true'; then
        seeded=1; break
    fi
    sleep 1
done
if [ "$seeded" != "1" ]; then
    echo "FATAL: doubleclick.net is not being blocked after seed; aborting" >&2
    curl -sS $auth -X POST "http://127.0.0.1:$API/api/v1/test-domain" \
        -d '{"domain":"doubleclick.net"}' >&2
    exit 1
fi
echo "  seeded"

# ---- 5. Run shoot ----
echo ">>> [5/5] playwright"
(cd web && SKOED_BASE_URL="http://127.0.0.1:$API" \
    SKOED_AUTH_USER=admin SKOED_AUTH_PASS=demopass123 \
    node shoot-m5.9.7.mjs 2>&1 | sed 's/^/  /')

echo ">>> done"
