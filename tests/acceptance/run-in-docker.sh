#!/bin/sh
# Run the skoed acceptance suite inside a Docker container.
#
# Why container: running on the host accumulates TIME_WAIT sockets and
# host-specific port-allocation quirks that flake the suite around
# minute 5-7. Inside a container with its own network namespace, port
# allocation is clean for every run.
# See ~/.claude/projects/.../memory/feedback-tests-in-docker.md.
#
# NET_ADMIN and the iptables package let the M50 tests cut one node's
# access to the leader's API (FS-BlocklistContentFollowerCannotReachLeader).
#
# The upgrade test (FS-BlocklistContentUpgradeKeepsFiltering) starts a
# cluster on the previous release. The script extracts apps/skoed at
# SKOED_PREVIOUS_REF (default v0.4.4) with git archive, builds it to
# /tmp/skoed-prev in the container and passes it as SKOED_PREVIOUS_BINARY.
# When the ref does not exist (e.g. a clone without tags), the binary is
# not built and that test skips.
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
#
# The DNS overload tests (dns_overload_protection_test.go) take about
# 10 minutes on their own, so without -run or -skip the suite is run with
# -skip '^TestDnsOverload', as in CI. Run them with:
#   ./tests/acceptance/run-in-docker.sh -v -run '^TestDnsOverload'

set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IMG="golang:1.27-alpine"

GOMOD_VOL="${SKOED_GOMOD_VOLUME:-skoed-gomod-cache}"
GOBUILD_VOL="${SKOED_GOBUILD_VOLUME:-skoed-gobuild-cache}"

# Pre-pull the golang image so the first run isn't dominated by download.
docker image inspect "$IMG" >/dev/null 2>&1 || docker pull "$IMG"

# Extract the previous release on the host, where git is available, and
# mount it read-only. The OpenAPI document is copied into the swaggerui
# assets as `make openapi-sync` does, because go:embed needs it.
PREV_REF="${SKOED_PREVIOUS_REF:-v0.4.4}"
PREV_DIR=""
prev_mount=""
if git -C "$ROOT" rev-parse -q --verify "${PREV_REF}^{commit}" >/dev/null; then
  PREV_DIR="$(mktemp -d)"
  trap 'rm -rf "$PREV_DIR"' EXIT
  trap 'exit 130' INT TERM
  git -C "$ROOT" archive "$PREV_REF" apps/skoed | tar -x -C "$PREV_DIR"
  git -C "$ROOT" show "${PREV_REF}:specs/technical/management-api.openapi.yaml" \
    >"$PREV_DIR/apps/skoed/internal/api/swaggerui/assets/openapi.yaml"
  prev_mount="-v ${PREV_DIR}:/prev:ro"
else
  echo "warning: ${PREV_REF} not found, the upgrade test will skip" >&2
fi

# Skip the DNS overload tests unless the caller selects tests itself.
filtered=0
for arg in "$@"; do
  case "$arg" in
    -run|-run=*|--run|--run=*|-skip|-skip=*|--skip|--skip=*) filtered=1 ;;
  esac
done
if [ "$filtered" = 0 ]; then
  set -- -skip '^TestDnsOverload' "$@"
fi

cache_mounts=""
if [ "${SKOED_TEST_NO_CACHE:-0}" != "1" ]; then
  docker volume create "$GOMOD_VOL"   >/dev/null
  docker volume create "$GOBUILD_VOL" >/dev/null
  cache_mounts="-v ${GOMOD_VOL}:/go/pkg/mod -v ${GOBUILD_VOL}:/root/.cache/go-build"
fi

# shellcheck disable=SC2086  # $cache_mounts and $prev_mount are intentionally word-split
docker run --rm \
  --network bridge \
  --cap-add NET_ADMIN \
  -v "$ROOT":/src \
  $prev_mount \
  $cache_mounts \
  -w /src/apps/skoed \
  -e CGO_ENABLED=0 \
  -e SKOED_TEST_MODE=1 \
  "$IMG" sh -c '
    apk add --no-cache iptables >/dev/null
    go build -ldflags="-s -w" -o /tmp/skoed ./cmd/skoed/
    if [ -d /prev/apps/skoed ]; then
      if (cd /prev/apps/skoed && go build -ldflags="-s -w" -o /tmp/skoed-prev ./cmd/skoed/); then
        export SKOED_PREVIOUS_BINARY=/tmp/skoed-prev
      else
        echo "warning: building the previous release failed, the upgrade test will skip" >&2
      fi
    fi
    cd /src/tests/acceptance
    SKOED_BINARY=/tmp/skoed exec go test -timeout 1200s -parallel 8 "$@" ./...
  ' -- "$@"
