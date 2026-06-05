#!/bin/sh
# Run the dblock acceptance suite inside a Docker container.
#
# Why: running on the host accumulates TIME_WAIT sockets and host-specific
# port-allocation quirks that flake the suite around minute 5-7. Inside a
# container with its own network namespace, port allocation is clean for
# every run. See ~/.claude/projects/.../memory/feedback-tests-in-docker.md.
#
# Usage (from repo root or anywhere):
#   ./tests/acceptance/run-in-docker.sh                 # run full suite
#   ./tests/acceptance/run-in-docker.sh -run TestDoh    # filter
#   ./tests/acceptance/run-in-docker.sh -v -count=1 -run 'TestAcme'

set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IMG="golang:1.24-alpine"

# Pre-pull the golang image so the first run isn't dominated by download.
docker image inspect "$IMG" >/dev/null 2>&1 || docker pull "$IMG"

docker run --rm \
  --network bridge \
  -v "$ROOT":/src \
  -w /src/apps/dblock \
  -e CGO_ENABLED=0 \
  -e DBLOCK_TEST_MODE=1 \
  "$IMG" sh -c '
    apk add --no-cache bind-tools >/dev/null
    go build -ldflags="-s -w" -o /tmp/dblock ./cmd/dblock/
    cd /src/tests/acceptance
    DBLOCK_BINARY=/tmp/dblock exec go test -timeout 900s "$@" ./...
  ' -- "$@"
