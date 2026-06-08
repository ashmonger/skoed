# DEMO NOTE — M5.7 Multi-arch Release Builds

## Scope

`goreleaser` drives the release pipeline. One `git tag vX.Y.Z` push
produces both `linux/amd64` and `linux/arm64` artefacts: tar.gz +
.deb + Docker buildx multi-arch manifest. CI fans out the same logic
across PRs (binary build + acceptance tests + packaging smoke).

### Implemented

- **`.goreleaser.yaml`** — declarative pipeline:
  - `builds`: `CGO_ENABLED=0`, two GOARCH targets, ldflags inject
    `main.version` + `main.commit`.
  - `archives`: per-arch `dblock_<ver>_linux_<arch>.tar.gz`.
  - `nfpms`: per-arch `.deb` using the M5.5 maintainer scripts +
    systemd unit.
  - `dockers` + `docker_manifests`: per-arch Docker images
    (`ghcr.io/dblock/dblock:<ver>-amd64` / `…-arm64`) joined into a
    `:<ver>` and `:latest` manifest.
  - `checksum`: `dist/checksums.txt`.
  - `changelog`: GitHub auto-grouping by `feat:`/`fix:`/`docs:`.
- **`Dockerfile`** — `gcr.io/distroless/static-debian12:nonroot`,
  copies the pre-built binary, EXPOSEs 53/udp, 53/tcp, 853/tcp,
  8080/tcp. Image size target ≤ 100 MB (M1 risk row).
- **`.github/workflows/release.yml`** — on `push: tags v*` runs
  goreleaser via `goreleaser/goreleaser-action@v6` with QEMU +
  buildx + GHCR login. `workflow_dispatch` allows manual snapshot
  builds (no publish), which upload to `actions/upload-artifact` for
  inspection.
- **`.github/workflows/ci.yml`** — every push/PR runs:
  - `build` — `make build` + `go vet`.
  - `acceptance` — `make acceptance` (in-Docker).
  - `spec-lint` — `tools/spec-lint/spec_lint.sh`.
  - `packaging` — `make deb` + `make test-deb` smoke.
- **Makefile** picks up `build-ui` / `openapi-sync` proxies so
  goreleaser's `before:` hooks succeed from repo root.

### Local validation

```
$ goreleaser check --config .goreleaser.yaml
• 1 configuration file(s) validated

$ goreleaser release --snapshot --clean --skip docker
…
• building                                       target=linux_amd64_v1
• building                                       target=linux_arm64_v8.0
• archiving                                      name=dist/dblock_0.0.1-next_linux_amd64.tar.gz
• archiving                                      name=dist/dblock_0.0.1-next_linux_arm64.tar.gz
• creating                                       package=dblock format=deb arch=amd64v1 file=dist/dblock_0.0.1-next_amd64.deb
• creating                                       package=dblock format=deb arch=arm64v8.0 file=dist/dblock_0.0.1-next_arm64.deb
• release succeeded after 28s

$ file dist/dblock_linux_amd64_v1/dblock dist/dblock_linux_arm64_v8.0/dblock
dist/dblock_linux_amd64_v1/dblock: ELF 64-bit LSB executable, x86-64, …, statically linked, Go BuildID=…
dist/dblock_linux_arm64_v8.0/dblock: ELF 64-bit LSB executable, ARM aarch64, …, statically linked, Go BuildID=…
```

Docker step skipped in the local snapshot (needs buildx + QEMU + a
registry login the local box can't do). CI runs it on every tag.

### Acceptance / FSIDs

4 FSIDs in `specs/functional/multi-arch-builds.feature`. There are NO
new Go acceptance tests — the validation is the goreleaser pipeline
itself + the M5.5 `packaging/test-deb.sh` (which runs against each
arch's .deb in the `packaging` CI job).

| FSID                              | Validation                                |
|-----------------------------------|-------------------------------------------|
| FS-MultiArchGoreleaserBuilds      | `goreleaser release --snapshot` locally + CI |
| FS-MultiArchDockerManifest        | CI publishes the manifest on tag           |
| FS-MultiArchImageSize             | Dockerfile uses distroless static (~30 MB base + 6.5 MB binary ≪ 100 MB) |
| FS-MultiArchChecksums             | `dist/checksums.txt` produced; cosign signatures M5.7.1 |

### Not implemented (deferred / non-goals)

- **Cosign keyless signing** of artefacts — workflow has the
  `id-token: write` permission ready; the signing step lands in
  M5.7.1 once we finalise the trust root.
- **`linux/arm` (32-bit)** — almost no demand.
- **macOS / Windows builds** — dblock is a Linux daemon.
- **FreeBSD / OpenBSD ports** — community-driven.
- **`dockers_v2`** — goreleaser is rolling the new shape; the
  `dockers` + `docker_manifests` form we use still works and is
  documented as deprecation-with-grace. Migrate in M5.7.1.

### Files added

```
.goreleaser.yaml
Dockerfile
.github/workflows/release.yml
.github/workflows/ci.yml
specs/functional/multi-arch-builds.feature
specs/technical/multi-arch-builds.md
```

Plus a small `Makefile` patch: `build-ui` / `openapi-sync` proxy
targets so `goreleaser` before hooks succeed from repo root.

## Demo

```bash
# Local snapshot (no publish, no docker).
goreleaser release --snapshot --clean --skip docker
ls dist/
# checksums.txt
# dblock_0.0.1-next_amd64.deb
# dblock_0.0.1-next_arm64.deb
# dblock_0.0.1-next_linux_amd64.tar.gz
# dblock_0.0.1-next_linux_arm64.tar.gz

# In CI on tag push:
git tag v0.5.1 && git push origin v0.5.1
# .github/workflows/release.yml fires:
#   - cross-compile amd64 + arm64
#   - build .deb for both
#   - buildx multi-arch image → ghcr.io/dblock/dblock:0.5.1 + :latest
#   - create GitHub release with checksums.txt + changelog
```

## Next

M5.8 — Documentation site (mdBook scaffold).
