Feature: Packaging and distribution
  As an operator deploying skoed on a Linux host or Kubernetes cluster,
  I want to install it through my native package manager or Helm,
  so that upgrades, uninstalls, and system integration follow platform conventions.

  Non-goals:
    - Publishing to the official Debian/Ubuntu PPA (requires Debian Developer sponsorship)
    - Publishing to Alpine Linux's official edge repository (requires Alpine maintainer)
    - Homebrew formula (macOS; skoed targets Linux only)
    - Automatic documentation translation

  # ─── Alpine Linux ──────────────────────────────────────────────────────────────

  @fsid:FS-AlpinePackageBuilt
  Scenario: Alpine APK artifact is produced on every release
    Given a tagged release is triggered on the dblock-m11 branch
    When the CI release pipeline runs
    Then a `.apk` artifact for `amd64` is attached to the GitHub Release
    And  a `.apk` artifact for `arm64` is attached to the GitHub Release
    And  the artifact can be installed with `apk add --allow-untrusted skoed_<version>_amd64.apk`
    And  the installed binary passes `skoed --version`

  @fsid:FS-AlpinePackageContents
  Scenario: Alpine package installs the binary to the expected path
    Given the Alpine `.apk` is installed on an Alpine Linux host
    Then `/usr/bin/skoed` is present and executable
    And  the package installs the default config to `/etc/skoed/config.yaml`
    And  the data dir `/var/lib/skoed` is created with mode 0700

  # ─── Arch Linux AUR ────────────────────────────────────────────────────────────

  @fsid:FS-AurPkgbuildPresent
  Scenario: AUR PKGBUILD is present and installable
    Given the repository contains `packaging/aur/PKGBUILD`
    When an Arch Linux user runs `makepkg -si` inside `packaging/aur/`
    Then skoed is built from the upstream tarball published on GitHub Releases
    And  the installed binary passes `skoed --version`

  @fsid:FS-AurPkgbuildVersionSync
  Scenario: AUR PKGBUILD version matches the current release
    Given a new release has been tagged and CI has run
    Then the `pkgver` field in `packaging/aur/PKGBUILD` matches the released version
    And  the `source` field resolves to a valid tarball URL on GitHub Releases

  # ─── Helm chart ────────────────────────────────────────────────────────────────

  @fsid:FS-HelmChartInstallsSkoed
  Scenario: Helm chart deploys a single-node skoed to Kubernetes
    Given a CNCF-conformant Kubernetes cluster (kind or k3s)
    When an operator runs `helm install skoed oci://ghcr.io/ashmonger/charts/skoed`
    Then a skoed Pod reaches Running state within 60 seconds
    And  the Pod exposes port 53 for DNS
    And  the Pod exposes port 8080 for the management API
    And  `kubectl exec -- skoed --version` exits 0

  @fsid:FS-HelmChartLints
  Scenario: Helm chart passes lint and template validation
    Given the chart source in `charts/skoed/`
    When `helm lint charts/skoed` runs
    Then exit code is 0 and no warnings are emitted

  @fsid:FS-HelmChartPublished
  Scenario: Helm chart is published to GHCR on every release
    Given a tagged release is triggered
    When the CI release pipeline runs
    Then the chart is pushed to `ghcr.io/ashmonger/charts/skoed:<version>`
    And  `helm pull oci://ghcr.io/ashmonger/charts/skoed --version <version>` succeeds

  # ─── Proxmox LXC bootstrap script ─────────────────────────────────────────────

  @fsid:FS-ProxmoxScriptInRelease
  Scenario: Proxmox bootstrap script is attached to GitHub Releases
    Given a tagged release is triggered
    When the CI release pipeline runs
    Then `proxmox-create.sh` is present as a downloadable artifact on the GitHub Release
    And  the script is executable (mode 755)

  # ─── Documentation site ────────────────────────────────────────────────────────

  @fsid:FS-DocsBuildSucceeds
  Scenario: Documentation site builds without errors
    Given the `docs/` directory contains mdBook source
    When `mdbook build docs` runs
    Then exit code is 0
    And  an `index.html` is produced under `docs/book/`
    And  no broken internal links are reported by mdbook

  @fsid:FS-DocsPublishedToPages
  Scenario: Documentation site is deployed to GitHub Pages on master push
    Given a commit touching any file under `docs/` is merged to master
    When the docs CI workflow runs
    Then the built site is deployed to the `gh-pages` branch
    And  the site is reachable at the project's GitHub Pages URL
