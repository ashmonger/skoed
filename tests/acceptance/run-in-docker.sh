#!/bin/sh
# Run the skoed acceptance suite inside a Docker container.
#
# Why container: running on the host accumulates TIME_WAIT sockets and
# host-specific port-allocation quirks that flake the suite around
# minute 5-7. Inside a container with its own network namespace, port
# allocation is clean for every run.
# See ~/.claude/projects/.../memory/feedback-tests-in-docker.md.
#
# ----- Persistent Go cache (M5.9.3) -----------------------------------
# Two named Docker volumes are mounted by default so warm reruns reuse
# downloaded modules and compiled artefacts instead of re-doing the
# ~10-minute cold build:
#
#   skoed-gomod-cache    -> /go/pkg/mod           (GOMODCACHE)
#   skoed-gobuild-cache  -> /root/.cache/go-build (GOCACHE)
#
# Wipe both with: `make acceptance-clean`
# (or manually: `docker volume rm skoed-gomod-cache skoed-gobuild-cache`)
#
# Disable the cache for a single invocation:
#   SKOED_TEST_NO_CACHE=1 ./tests/acceptance/run-in-docker.sh
#
# Override the volume names (e.g. parallel branches):
#   SKOED_GOMOD_VOLUME=foo SKOED_GOBUILD_VOLUME=bar ./...run-in-docker.sh
# ----------------------------------------------------------------------
#
# Usage (from repo root or anywhere):
#   ./tests/acceptance/run-in-docker.sh                 # run full suite
#   ./tests/acceptance/run-in-docker.sh -run TestDoh    # filter
#   ./tests/acceptance/run-in-docker.sh -v -count=1 -run 'TestAcme'

set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IMG="golang:1.24-alpine"

GOMOD_VOL="${SKOED_GOMOD_VOLUME:-skoed-gomod-cache}"
GOBUILD_VOL="${SKOED_GOBUILD_VOLUME:-skoed-gobuild-cache}"

# Pre-pull the golang image so the first run isn't dominated by download.
docker image inspect "$IMG" >/dev/null 2>&1 || docker pull "$IMG"

cache_mounts=""
if [ "${SKOED_TEST_NO_CACHE:-0}" != "1" ]; then
  docker volume create "$GOMOD_VOL"   >/dev/null
  docker volume create "$GOBUILD_VOL" >/dev/null
  cache_mounts="-v ${GOMOD_VOL}:/go/pkg/mod -v ${GOBUILD_VOL}:/root/.cache/go-build"
fi

# shellcheck disable=SC2086  # $cache_mounts is intentionally word-split
docker run --rm \
  --network bridge \
  -v "$ROOT":/src \
  $cache_mounts \
  -w /src/apps/skoed \
  -e CGO_ENABLED=0 \
  -e SKOED_TEST_MODE=1 \
  "$IMG" sh -c '
    apk add --no-cache bind-tools >/dev/null
    go build -ldflags="-s -w" -o /tmp/skoed ./cmd/skoed/
    cd /src/tests/acceptance
    SKOED_BINARY=/tmp/skoed exec go test -timeout 1200s -parallel 8 "$@" ./...
  ' -- "$@"
