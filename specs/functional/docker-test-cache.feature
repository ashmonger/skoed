Feature: Docker Test Cache (go-mod volume)
  As a skoed developer running the acceptance suite locally
  I want the Docker test runner to reuse a persistent go-module
  and go-build cache across runs
  So a warm rerun takes ~1 minute instead of re-downloading all
  modules every time (~10 minutes cold).

  Background:
    Given Docker is available on the host
    And `tests/acceptance/run-in-docker.sh` is the entry point for the suite

  @fsid:FS-DockerTestCacheColdWarmsCache
  Scenario: A cold run downloads modules and warms the cache volumes
    Given no `skoed-gomod-cache` Docker volume exists
    And no `skoed-gobuild-cache` Docker volume exists
    When the operator runs `tests/acceptance/run-in-docker.sh`
    Then both named volumes are created
    And `/go/pkg/mod` inside the container is the `skoed-gomod-cache` volume
    And `/root/.cache/go-build` inside the container is the `skoed-gobuild-cache` volume
    And after the run, `docker volume ls` lists both volumes
    And the volumes are non-empty (go modules + build artefacts persisted)

  @fsid:FS-DockerTestCacheWarmRunIsFast
  Scenario: A warm rerun reuses the cache and is materially faster
    Given the `skoed-gomod-cache` and `skoed-gobuild-cache` volumes are warm
    When the operator runs `tests/acceptance/run-in-docker.sh` a second time
    Then no go modules are re-downloaded
    And the `go build` step reuses cached compilation artefacts
    And the wall-clock time is materially lower than the cold run
      (target on a typical dev box: ~1 min versus ~10 min cold)

  @fsid:FS-DockerTestCacheCleanWipesVolumes
  Scenario: `make acceptance-clean` wipes both cache volumes
    Given the cache volumes exist (warm or cold)
    When the operator runs `make acceptance-clean` from the repo root
    Then `skoed-gomod-cache` and `skoed-gobuild-cache` are removed
    And `docker volume ls` no longer lists them
    And the next `tests/acceptance/run-in-docker.sh` invocation behaves
      like a cold run (recreates the volumes from scratch)

  @fsid:FS-DockerTestCacheCanBeDisabled
  Scenario: Setting SKOED_TEST_NO_CACHE=1 skips the volume mounts
    Given the operator wants throwaway caches (e.g. CI with actions/cache)
    When `SKOED_TEST_NO_CACHE=1 tests/acceptance/run-in-docker.sh` runs
    Then neither volume is mounted into the container
    And the run behaves like the legacy pre-cache shell script
    And no `skoed-gomod-cache` / `skoed-gobuild-cache` volumes are created
      by this invocation

  Non-goals:
    - Caching the compiled test binary (changes every commit, low payoff)
    - Sharing the cache with host `$GOPATH/pkg/mod` (host UID/perm mismatch
      pain — keep the cache container-internal in a named volume)
    - Production image impact: the dev runner uses `golang:1.24-alpine`;
      the M5.7 release images are untouched
    - Multi-host / shared-cache distribution (single-developer-box scope)
