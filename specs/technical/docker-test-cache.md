---
x-tsid: TS-DockerTestCache
x-fsid-links:
  - FS-DockerTestCacheColdWarmsCache
  - FS-DockerTestCacheWarmRunIsFast
  - FS-DockerTestCacheCleanWipesVolumes
  - FS-DockerTestCacheCanBeDisabled
---

# TS-DockerTestCache — Persistent go-mod + go-build cache for acceptance runs

## Context

The dblock acceptance suite runs inside a disposable
`golang:1.24-alpine` container via `tests/acceptance/run-in-docker.sh`
(see memory: `feedback-tests-in-docker.md`). Today the container has no
shared cache, so every invocation re-downloads every Go module
(`prometheus/client_golang`, `hashicorp/raft`, `miekg/dns`, …) and
re-compiles the world. Cold runs are ~10 min; the build/network spend
dominates the actual test work.

A persistent named Docker volume mounted at the Go cache paths
makes every rerun reuse the downloaded modules and compiled artefacts.
First run still pays the cost; subsequent runs target ~1 min on the
M5.9.x suite.

## Decision

Two named Docker volumes, mounted by `run-in-docker.sh` by default:

| Volume name              | Mount path inside container | Purpose                |
|--------------------------|------------------------------|------------------------|
| `dblock-gomod-cache`     | `/go/pkg/mod`                | go module download cache (`GOMODCACHE`) |
| `dblock-gobuild-cache`   | `/root/.cache/go-build`      | go build cache (`GOCACHE` default for root in alpine) |

Why named volumes (not bind mounts to `$HOME/go/pkg/mod`):

- No UID/perm collision with the developer's host user (the container
  runs as root by default; `chown`-on-bind is a footgun).
- `docker volume rm` is the single, obvious cleanup verb.
- Survives `docker system prune` unless `--volumes` is explicitly passed.
- Works the same in CI (where the host has no Go toolchain).

## Implementation

### `tests/acceptance/run-in-docker.sh`

The runner gains a header comment documenting the cache, two
`docker volume create` calls (idempotent), and the two `-v` flags on
the `docker run` invocation:

```sh
GOMOD_VOL="${DBLOCK_GOMOD_VOLUME:-dblock-gomod-cache}"
GOBUILD_VOL="${DBLOCK_GOBUILD_VOLUME:-dblock-gobuild-cache}"

mount_args=""
if [ "${DBLOCK_TEST_NO_CACHE:-0}" != "1" ]; then
  docker volume create "$GOMOD_VOL"  >/dev/null
  docker volume create "$GOBUILD_VOL" >/dev/null
  mount_args="-v $GOMOD_VOL:/go/pkg/mod -v $GOBUILD_VOL:/root/.cache/go-build"
fi

docker run --rm \
  --network bridge \
  -v "$ROOT":/src \
  $mount_args \
  -w /src/apps/dblock \
  …
```

`DBLOCK_TEST_NO_CACHE=1` disables the mounts entirely for environments
that prefer ephemeral caches (a CI runner already using
`actions/cache` for `GOMODCACHE`, an `unshare`d sandbox, …).

### `Makefile` (root)

A new phony target:

```make
acceptance-clean:
	docker volume rm dblock-gomod-cache dblock-gobuild-cache || true
```

`|| true` so the target is idempotent (no error when the volumes
already don't exist).

## Non-goals

- Changing the production image (M5.7 multi-arch goreleaser pipeline).
  The release Dockerfile is `Dockerfile`; the dev runner uses
  `golang:1.24-alpine` and does not share that lineage.
- Changing any test code: the acceptance Go files are untouched.
- Sharing the cache between the container and the host
  `$GOPATH/pkg/mod` (UID/permission gotchas; not worth the
  complexity for a dev-loop accelerator).
- Caching the test binary itself (changes every commit; payoff is
  in the module + std-lib build cache, not the test object code).

## Validation

Smoke-validated manually (no Go acceptance test for tooling changes;
matches the M5.7 multi-arch convention):

1. `make acceptance-clean` — ensure a cold baseline.
2. `time tests/acceptance/run-in-docker.sh` — first run; record
   wall-clock time and confirm `docker volume ls | grep dblock`
   shows both volumes.
3. `time tests/acceptance/run-in-docker.sh` — second run; record
   wall-clock time. The delta is the operator-visible win.
4. `DBLOCK_TEST_NO_CACHE=1 tests/acceptance/run-in-docker.sh` —
   confirms the override path still works and skips the mounts.
5. `make acceptance-clean` — confirms wipe works and idempotency
   when re-run on already-removed volumes.

Wall-clock numbers vary by host (network, disk, CPU) — the demo note
records the actual delta observed on the development box.
