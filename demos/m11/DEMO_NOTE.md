# M11 Distribution & Documentation — Demo Note

## Implemented

### Packaging

- **Alpine Linux APK** — `.apk` packages (amd64 + arm64) produced by goreleaser via nfpm on every tagged release. Installable with `apk add --allow-untrusted skoed_<version>_amd64.apk`. Added `make apk` and `make apk-arm64` targets.
- **AUR PKGBUILD** — `packaging/aur/PKGBUILD` + `.SRCINFO` for Arch Linux. CI release workflow patches `pkgver`/`sha256sums` and pushes to `aur.archlinux.org/skoed.git` when `AUR_SSH_PRIVATE_KEY` secret is set.
- **Helm chart** (`charts/skoed/`) — deploys skoed as a `Deployment` or `DaemonSet` on Kubernetes. Includes ConfigMap for config.yaml, PVC for data, Service (DNS + API ports), ServiceAccount, liveness/readiness probes. Published to `ghcr.io/ashmonger/charts/skoed` as an OCI chart on every release.
- **Proxmox script in releases** — `scripts/proxmox-create.sh` attached to every GitHub Release via goreleaser `extra_files`.

### CI

- `ci.yml`: added `helm-lint` job (helm lint + template render), `docs` job (mdbook build), `dblock-*` branch trigger, `make apk` step in packaging job.
- `release.yml`: added Helm OCI publish step, AUR sync step (conditional on secret), docs rebuild dispatch.

### Documentation

- **README.md** — complete product README: badges, dashboard screenshot, 30-second Docker quickstart, feature list, install matrix (Docker / .deb / .apk / Helm / Proxmox), cluster quickstart, configuration overview, links.
- **All doc stubs filled** — 15 placeholder pages replaced with real content:
  - `install/docker.md` — single-node run + 3-node Docker Compose
  - `install/kubernetes.md` — Helm quickstart, values reference, cluster mode
  - `cluster/add-nodes.md` — join tokens, bootstrap config, removal, quorum table
  - `cluster/encrypted-mesh.md` — mTLS config, openssl cert generation, rotation
  - `configuration/dns.md`, `blocklists.md`, `profiles.md`, `categories.md`, `doh-dot.md`, `api-https.md`, `metrics.md`, `audit-log.md`
  - `operations/automated-refresh.md`, `in-place-upgrade.md`, `backup-restore.md`, `troubleshooting.md`
  - `reference/yaml-schema.md`, `cli.md`, `api-openapi.md`

## Not implemented

- Publishing to the official Debian/Ubuntu PPA (requires Debian Developer sponsorship).
- Publishing to Alpine Linux's official `edge` repository (requires an Alpine maintainer account).
- Live AUR push requires `AUR_SSH_PRIVATE_KEY` secret to be configured in the repository settings. The PKGBUILD and CI step are ready; only the secret is missing.

## Limitations

- DaemonSet mode in Helm does not support PVCs (Kubernetes limitation with `volumeClaimTemplates` on DaemonSets). Data volume falls back to `emptyDir` for DaemonSets; use a StatefulSet for persistent DaemonSet-style deployments if needed in a future milestone.
- The Helm chart publishes to `ghcr.io/ashmonger/charts/skoed`; the goreleaser config still references `ghcr.io/skoed/skoed` for the Docker image. These will align when the GitHub organisation is settled.
- AUR PKGBUILD currently uses `SKIP` for checksums as placeholders until a release tag exists.

## Demo commands

```sh
# Helm lint (local)
helm lint charts/skoed

# Helm template render
helm template skoed charts/skoed | grep 'kind:'

# Build APK locally (requires nfpm installed)
make apk

# goreleaser snapshot (builds all packages without publishing)
goreleaser release --snapshot --clean
ls dist/*.deb dist/*.apk
```
