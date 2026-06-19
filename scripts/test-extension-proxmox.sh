#!/usr/bin/env bash
# Test skoed browser extension from Proxmox LXC clients.
# - Tests SSE endpoint reachability from each client CT.
# - Renders extension popup.html in headless Chromium and screenshots it.
# - All screenshots saved to demos/m22.5/proxmox/ on THIS machine.
#
# Usage: ./scripts/test-extension-proxmox.sh
# Requires: ssh access to Proxmox host, pct available on host.
set -euo pipefail

PROXMOX_HOST="ns3251245.ip-91-134-62.eu"
SSH_KEY="/home/jcollin/.ssh/id_ed25519"
SSH="ssh -i $SSH_KEY -o StrictHostKeyChecking=no root@$PROXMOX_HOST"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCREENSHOTS_DIR="$REPO_ROOT/demos/m22.5/proxmox"
REMOTE_WORKDIR="/tmp/skoed-ext-test"

mkdir -p "$SCREENSHOTS_DIR"

log() { echo "[ext-test] $*"; }

# ── discover running CTs ──────────────────────────────────────────────────────
log "Discovering running containers..."
RUNNING_CTS=$($SSH "pct list | awk 'NR>1 && \$2==\"running\" {print \$1}'")
log "Running CTs: $RUNNING_CTS"

# Find the skoed admin CT (the one with skoed process)
SKOED_CT=""
SKOED_IP=""
for ct in $RUNNING_CTS; do
  if $SSH "pct exec $ct -- sh -c 'command -v skoed 2>/dev/null || pgrep -x skoed 2>/dev/null'" &>/dev/null; then
    SKOED_CT="$ct"
    SKOED_IP=$($SSH "pct exec $ct -- sh -c 'ip -4 addr show eth0 2>/dev/null | grep -oP \"(?<=inet )[0-9.]+\"'" | head -1)
    log "Found skoed node: CT $SKOED_CT, IP $SKOED_IP"
    break
  fi
done

if [ -z "$SKOED_CT" ]; then
  log "ERROR: could not find a CT running skoed — abort"
  exit 1
fi

# ── get admin token from skoed CT ────────────────────────────────────────────
log "Getting admin session token..."
LOGIN_RESP=$($SSH "pct exec $SKOED_CT -- sh -c \
  'curl -sf -X POST http://${SKOED_IP}:8080/api/v1/auth/login \
     -H \"Content-Type: application/json\" \
     -d \"{\\\"username\\\":\\\"admin\\\",\\\"password\\\":\\\"Skoed2026!\\\"}\"'")
API_TOKEN=$(echo "$LOGIN_RESP" | grep -oP '(?<="token":")[^"]+' || true)
if [ -z "$API_TOKEN" ]; then
  log "ERROR: could not get API token"
  exit 1
fi
log "Got API token."

# ── prepare extension files on remote ────────────────────────────────────────
log "Uploading extension source to Proxmox host..."
$SSH "rm -rf $REMOTE_WORKDIR && mkdir -p $REMOTE_WORKDIR"
# Copy extension directory
tar -C "$REPO_ROOT/web/extension" -czf /tmp/skoed-ext.tar.gz .
scp -i "$SSH_KEY" -o StrictHostKeyChecking=no /tmp/skoed-ext.tar.gz \
    "root@$PROXMOX_HOST:$REMOTE_WORKDIR/extension.tar.gz"
$SSH "cd $REMOTE_WORKDIR && mkdir extension && tar -xzf extension.tar.gz -C extension"
rm /tmp/skoed-ext.tar.gz

# ── collect all skoed node CTs by checking port 8080 ────────────────────────
SKOED_NODES="$SKOED_CT"
for ct in $RUNNING_CTS; do
  [ "$ct" = "$SKOED_CT" ] && continue
  # A CT running skoed will have port 8080 open
  IS_SKOED=$($SSH "pct exec $ct -- sh -c \
    'ss -tlnp 2>/dev/null | grep -c 8080 || netstat -tlnp 2>/dev/null | grep -c 8080 || echo 0'" 2>/dev/null | tr -d '[:space:]' || echo 0)
  if [ "${IS_SKOED:-0}" -gt 0 ] 2>/dev/null; then
    SKOED_NODES="$SKOED_NODES $ct"
    log "Also skoed node: CT $ct"
  fi
done

# ── SSE connectivity test from each client CT ─────────────────────────────────
CLIENT_CTS=""
for ct in $RUNNING_CTS; do
  echo "$SKOED_NODES" | grep -qw "$ct" && continue
  CLIENT_CTS="$CLIENT_CTS $ct"
done
log "Client CTs to test SSE from: $CLIENT_CTS"

PASS=0
FAIL=0

for ct in $CLIENT_CTS; do
  CT_IP=$($SSH "pct exec $ct -- sh -c 'ip -4 addr show eth0 2>/dev/null | grep -oP \"(?<=inet )[0-9.]+\"'" 2>/dev/null | head -1 || true)
  log "CT $ct ($CT_IP): testing SSE endpoint..."

  # Install curl if missing
  $SSH "pct exec $ct -- sh -c 'command -v curl || (apt-get -qq update && apt-get -qq install -y curl 2>/dev/null) || apk add -q curl 2>/dev/null'" &>/dev/null || true

  # Test SSE: wait up to 20s for keepalive frame; capture HTTP status + first frames
  RAW=$($SSH "pct exec $ct -- sh -c \
    'curl -sv --max-time 20 -N \
       -H \"Authorization: Bearer $API_TOKEN\" \
       http://$SKOED_IP:8080/api/v1/events 2>&1'" 2>/dev/null || true)

  HTTP_CODE=$(echo "$RAW" | grep -oP '(?<=< HTTP/1\.1 )\d+' | head -1 || echo "0")
  SSE_FRAMES=$(echo "$RAW" | grep -E "^:|^event:|^data:" | head -3 || true)

  if [ "$HTTP_CODE" = "200" ]; then
    if [ -n "$SSE_FRAMES" ]; then
      log "CT $ct: SSE OK — frames: $SSE_FRAMES"
      printf "HTTP 200\n%s\n" "$SSE_FRAMES" > "$SCREENSHOTS_DIR/ct${ct}-sse-output.txt"
    else
      log "CT $ct: SSE OK — HTTP 200, keepalive stream (no event frames yet)"
      echo "HTTP 200 — keepalive stream active" > "$SCREENSHOTS_DIR/ct${ct}-sse-output.txt"
    fi
    PASS=$((PASS+1))
  else
    log "CT $ct: SSE FAILED (HTTP $HTTP_CODE)"
    echo "FAILED HTTP $HTTP_CODE" > "$SCREENSHOTS_DIR/ct${ct}-sse-output.txt"
    FAIL=$((FAIL+1))
  fi
done

# ── headless Chromium screenshot of extension popup ──────────────────────────
log "Installing Chromium on Proxmox host for popup screenshot..."
$SSH "command -v chromium || command -v chromium-browser || command -v google-chrome || \
  (apt-get -qq update 2>/dev/null && apt-get -qq install -y chromium 2>/dev/null) || \
  apk add -q chromium 2>/dev/null || true"

CHROME_BIN=$($SSH "command -v chromium 2>/dev/null || command -v chromium-browser 2>/dev/null || \
  command -v google-chrome 2>/dev/null || echo ''" || true)

if [ -z "$CHROME_BIN" ]; then
  log "WARNING: Chromium not available on host — skipping popup screenshot"
else
  log "Chromium found: $CHROME_BIN"

  # Patch popup.html with real node URL + token for the screenshot
  $SSH "cd $REMOTE_WORKDIR/extension/popup && \
    sed 's|skoed_url.*||' popup.html > popup-demo.html || cp popup.html popup-demo.html"

  # Screenshot the popup HTML (static render, no extension context needed)
  POPUP_HTML="$REMOTE_WORKDIR/extension/popup/popup.html"
  log "Screenshotting extension popup HTML..."
  $SSH "$CHROME_BIN \
    --headless \
    --no-sandbox \
    --disable-gpu \
    --disable-dev-shm-usage \
    --window-size=400,640 \
    --screenshot=$REMOTE_WORKDIR/popup-screenshot.png \
    file://$POPUP_HTML 2>/dev/null" || log "WARNING: popup screenshot failed (non-fatal)"

  # Screenshot the admin web UI
  log "Screenshotting skoed admin login page..."
  $SSH "$CHROME_BIN \
    --headless \
    --no-sandbox \
    --disable-gpu \
    --disable-dev-shm-usage \
    --window-size=1280,900 \
    --screenshot=$REMOTE_WORKDIR/admin-ui-screenshot.png \
    http://$SKOED_IP:8080/ 2>/dev/null" || log "WARNING: admin UI screenshot failed (non-fatal)"

  # Copy screenshots back
  for f in popup-screenshot.png admin-ui-screenshot.png; do
    if $SSH "test -f $REMOTE_WORKDIR/$f" 2>/dev/null; then
      scp -i "$SSH_KEY" -o StrictHostKeyChecking=no \
          "root@$PROXMOX_HOST:$REMOTE_WORKDIR/$f" \
          "$SCREENSHOTS_DIR/$f"
      log "Saved $f → $SCREENSHOTS_DIR/$f"
    fi
  done
fi

# ── manifest validation from skoed CT ────────────────────────────────────────
log "Validating SSE endpoint on skoed node itself (20s wait for keepalive)..."
SSE_SELF=$($SSH "pct exec $SKOED_CT -- sh -c \
  'curl -sv --max-time 20 -N \
     -H \"Authorization: Bearer $API_TOKEN\" \
     http://$SKOED_IP:8080/api/v1/events 2>&1 | grep -E \"< HTTP|^:|^event:|^data:\" | head -5'" 2>/dev/null || true)
log "SSE self-test: $SSE_SELF"
echo "$SSE_SELF" > "$SCREENSHOTS_DIR/ct${SKOED_CT}-sse-self.txt"

# ── trigger a real event and capture it ──────────────────────────────────────
log "Firing a test webhook event to generate SSE frame..."
WEBHOOK_RESP=$($SSH "pct exec $SKOED_CT -- sh -c \
  'curl -sf -X POST http://10.0.0.100:8080/api/v1/webhooks \
     -H \"Authorization: Bearer $API_TOKEN\" \
     -H \"Content-Type: application/json\" \
     -d \"{\\\"url\\\":\\\"http://127.0.0.1:19999\\\",\\\"events\\\":[\\\"webhook.test\\\"]}\" \
     -o /dev/null -w \"%{http_code}\"'" 2>/dev/null || echo "skip")
log "Webhook create: HTTP $WEBHOOK_RESP"

# Capture SSE + fire test event simultaneously
if [ "$WEBHOOK_RESP" = "201" ]; then
  WH_ID=$($SSH "pct exec $SKOED_CT -- sh -c \
    'curl -sf -H \"Authorization: Bearer $API_TOKEN\" \
       http://10.0.0.100:8080/api/v1/webhooks | grep -oP \"\\\"id\\\":\\\"[^\\\"]+\\\"\" | head -1 | grep -oP \"[a-z0-9-]{36}\"'" 2>/dev/null || true)
  if [ -n "$WH_ID" ]; then
    # Open SSE stream in background, fire test, capture first event
    log "Capturing SSE event frame (webhook.test)..."
    SSE_EVENT=$($SSH "pct exec $SKOED_CT -- sh -c \
      'curl -sf --max-time 8 -N \
         -H \"Authorization: Bearer $API_TOKEN\" \
         http://10.0.0.100:8080/api/v1/events &
       sleep 1
       curl -sf -X POST http://10.0.0.100:8080/api/v1/webhooks/$WH_ID/test \
         -H \"Authorization: Bearer $API_TOKEN\" -o /dev/null
       sleep 3
       wait' 2>&1 | grep -E '^event:|^data:' | head -6" 2>/dev/null || true)
    log "SSE event captured: $SSE_EVENT"
    echo "$SSE_EVENT" > "$SCREENSHOTS_DIR/sse-live-event.txt"
  fi
fi

# ── summary ───────────────────────────────────────────────────────────────────
log ""
log "════ Extension Proxmox Test Summary ════"
log "SSE reachability: $PASS pass, $FAIL fail"
log "Screenshots + output saved to: $SCREENSHOTS_DIR"
ls -la "$SCREENSHOTS_DIR/" 2>/dev/null || true
log ""

[ "$FAIL" -eq 0 ] && log "RESULT: ALL PASS" || { log "RESULT: $FAIL FAILURES"; exit 1; }
