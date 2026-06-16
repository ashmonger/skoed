#!/bin/sh
# skoed-health.sh — keepalived health-check script for a skoed node.
#
# keepalived runs this script as the VRRP instance's "track_script".
# Exit 0 → node is healthy, eligible to hold the VIP.
# Exit 1 → node is unhealthy, give up the VIP.
#
# Configuration:
#   SKOED_API_PORT  — management API port (default 8080)
#   SKOED_API_HOST  — API host (default 127.0.0.1)
#
# The script checks GET /api/v1/health. The endpoint returns HTTP 200 with
# {"status":"ok"} when skoed is alive. A non-200 or connection failure means
# the node is unhealthy and should not hold the VIP.

SKOED_API_PORT="${SKOED_API_PORT:-8080}"
SKOED_API_HOST="${SKOED_API_HOST:-127.0.0.1}"
URL="http://${SKOED_API_HOST}:${SKOED_API_PORT}/api/v1/health"

# Require curl; fail immediately if missing.
if ! command -v curl > /dev/null 2>&1; then
    echo "skoed-health: curl not found" >&2
    exit 1
fi

# -s silent, -f fail on HTTP errors, -m 3 timeout 3s.
body=$(curl -sf -m 3 "$URL" 2>/dev/null) || exit 1

# Accept the liveness probe response.
case "$body" in
    *'"status":"ok"'*|\
    *'"status": "ok"'*)
        exit 0
        ;;
    *)
        exit 1
        ;;
esac
