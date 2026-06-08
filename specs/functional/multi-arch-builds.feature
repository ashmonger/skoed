Feature: Multi-arch Release Builds
  As a release engineer
  I want every release to ship both linux/amd64 and linux/arm64 binaries,
  Docker images, and .deb packages from a single CI run
  So Raspberry Pi / arm64 servers install the same release tag as
  x86 hosts.

  Background:
    Given a git tag of the form vX.Y.Z

  @fsid:FS-MultiArchGoreleaserBuilds
  Scenario: goreleaser produces both arches per artefact
    Given a clean working tree at the tag
    When `goreleaser release --snapshot --clean` runs
    Then `dist/` contains:
      | skoed_<ver>_linux_amd64.tar.gz |
      | skoed_<ver>_linux_arm64.tar.gz |
      | skoed_<ver>_amd64.deb           |
      | skoed_<ver>_arm64.deb           |
    And each tar.gz extracts to a working `skoed` binary for its arch
    And the .deb passes `packaging/test-deb.sh`

  @fsid:FS-MultiArchDockerManifest
  Scenario: Docker images publish a multi-arch manifest
    Given goreleaser is configured with buildx
    When `goreleaser release` publishes images
    Then `docker buildx imagetools inspect ghcr.io/skoed/skoed:<ver>`
      lists both `linux/amd64` and `linux/arm64` platforms
    And `docker pull ghcr.io/skoed/skoed:<ver>` on an arm64 host
      pulls the arm64 image (no manifest mismatch)

  @fsid:FS-MultiArchImageSize
  Scenario: Per-arch image stays under the M1 size budget
    Given a built skoed image for amd64 or arm64
    When `docker image ls` lists it
    Then the size is ≤ 100 MB

  @fsid:FS-MultiArchChecksums
  Scenario: Release notes carry per-arch SHA256 + cosign signatures
    Given a published release
    Then `dist/checksums.txt` lists one SHA256 per .tar.gz / .deb
    And `dist/*.cosign` files exist for each artefact (M5.7.1 follow-up
    once cosign is wired into the workflow)

  Non-goals:
    - `linux/arm` (32-bit) — almost no demand
    - macOS / Windows builds — skoed is a Linux daemon
    - FreeBSD / OpenBSD ports — community-driven
    - RPM packaging — when anyone asks (M5.5.1)
    - Snap / Flatpak — operator-level config is more portable as native
