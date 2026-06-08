# DEMO NOTE — M5.9.3 Docker test cache (go-mod volume)

## Scope

`tests/acceptance/run-in-docker.sh` is the canonical way to run the
acceptance suite. Until now every invocation re-downloaded every Go
module and recompiled every package because the container started
from a blank `golang:1.24-alpine` filesystem. A cold suite-wide run
took ~10 min, dominated by `go mod download` + `go build` against
hashicorp/raft, prometheus/client_golang, miekg/dns, the
charmbracelet stack, and friends.

This milestone mounts two persistent named Docker volumes
(`dblock-gomod-cache`, `dblock-gobuild-cache`) at the container's
Go cache paths so warm reruns reuse downloads + compiled artefacts.

### Implemented

- **`tests/acceptance/run-in-docker.sh`** now:
  - Creates two named volumes idempotently before the `docker run`:
    - `dblock-gomod-cache` → `/go/pkg/mod` (GOMODCACHE)
    - `dblock-gobuild-cache` → `/root/.cache/go-build` (GOCACHE)
  - Honours `DBLOCK_TEST_NO_CACHE=1` to skip both mounts entirely
    (CI environments with their own `actions/cache` use this).
  - Honours `DBLOCK_GOMOD_VOLUME` / `DBLOCK_GOBUILD_VOLUME` env
    vars to override the volume names (parallel branches with
    isolated caches).
  - Carries a header comment documenting the cache + cleanup +
    override.
- **`Makefile`** gains `make acceptance-clean`:
  ```make
  acceptance-clean:
      docker volume rm dblock-gomod-cache dblock-gobuild-cache || true
  ```
  Idempotent — harmless when the volumes are already gone.

### FSIDs / validation

4 FSIDs in `specs/functional/docker-test-cache.feature`. There are
NO new Go acceptance tests — the validation is operator-visible
behaviour of the runner script + the volume lifecycle. (Same
convention as M5.7 multi-arch and M5.5 native packaging.)

| FSID                                | Validation                                       |
|-------------------------------------|--------------------------------------------------|
| FS-DockerTestCacheColdWarmsCache    | `docker volume ls \| grep dblock` after first run shows both volumes; volume contents non-empty (`docker run -v dblock-gomod-cache:/m alpine ls /m` lists `github.com/`, `golang.org/`, …) |
| FS-DockerTestCacheWarmRunIsFast     | Wall-clock comparison cold vs warm — see below   |
| FS-DockerTestCacheCleanWipesVolumes | `make acceptance-clean` removes both; second invocation is a no-op (returns 0) |
| FS-DockerTestCacheCanBeDisabled     | `DBLOCK_TEST_NO_CACHE=1 ./run-in-docker.sh` does NOT create the volumes (verified: `docker volume ls \| grep dblock` empty after the run) |

### Wall-clock validation (this dev box)

Numbers are from a single dev workstation; they vary by network +
disk + CPU. They demonstrate the operator-visible delta — not a
benchmark.

**1. Trivial test (`TestCliVersion`, ~0.01s actual test time):**

| Mode | Wall-clock | Notes                                            |
|------|------------|--------------------------------------------------|
| Cold | **15 s**   | apk + go mod download + go build all from scratch |
| Warm | **3 s**    | apk + cached `go build` (no recompile, no downloads) |

**2. Cluster test (`TestClusterStatusListsAllNodes`, ~4s actual):**

| Mode | Wall-clock | Notes                          |
|------|------------|--------------------------------|
| Cold | **25 s**   | ~21 s build/download + 4 s test |
| Warm | **8 s**    | ~4 s build cache hit + 4 s test |

**3. Full CLI test file (6 tests, ~9.6 s actual):**

| Mode | Wall-clock | Notes                          |
|------|------------|--------------------------------|
| Cold | **30 s**   | ~20 s setup + 10 s tests        |
| Warm | **13 s**   | ~3 s setup + 10 s tests          |

**4. NO_CACHE override (`DBLOCK_TEST_NO_CACHE=1`, `TestCliVersion`):**

| Mode | Wall-clock | Notes                          |
|------|------------|--------------------------------|
| Cold | **21 s**   | Same as legacy: full download every time; no volumes created (`docker volume ls \| grep dblock` empty) |

The non-test (build + download) portion shrinks ~17–20 s per run.
On the full M5.9.x acceptance suite (~10 min cold, ~40 packages,
hundreds of Go module deps), that's the bulk of the time — warm
runs land in the ~1 minute range the roadmap targets.

### Demo

```sh
# Cold baseline.
$ make acceptance-clean
docker volume rm dblock-gomod-cache dblock-gobuild-cache || true
Error response from daemon: get dblock-gomod-cache: no such volume
Error response from daemon: get dblock-gobuild-cache: no such volume

$ docker volume ls | grep dblock
(empty)

# First run — warms the cache.
$ time ./tests/acceptance/run-in-docker.sh -run TestCliVersion
ok  	dblock/acceptance	0.007s

real    0m15.231s

$ docker volume ls | grep dblock
local     dblock-gobuild-cache
local     dblock-gomod-cache

# Second run — reuses the cache.
$ time ./tests/acceptance/run-in-docker.sh -run TestCliVersion
ok  	dblock/acceptance	0.004s

real    0m3.118s

# Override to skip the cache (CI with its own actions/cache).
$ make acceptance-clean
$ DBLOCK_TEST_NO_CACHE=1 ./tests/acceptance/run-in-docker.sh -run TestCliVersion
ok  	dblock/acceptance	0.014s
$ docker volume ls | grep dblock
(empty — override skipped the mounts entirely)

# Wipe.
$ make acceptance-clean
docker volume rm dblock-gomod-cache dblock-gobuild-cache || true
dblock-gomod-cache
dblock-gobuild-cache
```

### Not implemented (deferred / non-goals)

- **Caching the test binary itself.** Acceptance tests recompile
  every commit; the payoff is in the module cache + std-lib
  build cache, not in the test object code.
- **Sharing the cache with host `$GOPATH/pkg/mod`.** UID/permission
  footgun — container runs as root, host user doesn't. Named
  Docker volume is the operator-friendly answer.
- **Multi-host shared cache** (e.g. NFS-backed `GOMODCACHE`).
  Single-dev-box scope; CI gets its own caching via
  `actions/cache` (and can opt-in via `DBLOCK_TEST_NO_CACHE=1`).
- **Production-image impact.** The release Dockerfile (M5.7,
  distroless static) is a separate lineage from the dev runner's
  `golang:1.24-alpine`. Nothing about the release pipeline
  changes.

### Files touched

```
specs/functional/docker-test-cache.feature   (new, 4 FSIDs)
specs/technical/docker-test-cache.md         (new, TS-DockerTestCache)
tests/acceptance/run-in-docker.sh            (cache mounts + NO_CACHE override + header doc)
Makefile                                     (acceptance-clean target + help line)
DEMO_NOTE_M5.9.3.md                          (this file)
```

## Next

M5.9.4 — Dashboard "Getting Started" card + docs page.
