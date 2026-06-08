Feature: Native Packaging
  As an operator who runs dblock on Debian / Ubuntu / Proxmox LXC
  I want one-command install via the OS package manager
  (and a one-command Proxmox container creation script)
  So I don't have to babysit a systemd unit by hand or copy binaries
  around.

  Background:
    Given an amd64 or arm64 Linux host

  @fsid:FS-PackagingDebBuilds
  Scenario: nfpm builds a working .deb
    Given the source tree
    When `make deb` runs
    Then `dist/dblock_<version>_<arch>.deb` exists
    And `dpkg-deb --info <pkg>` shows:
      | Package        | dblock          |
      | Architecture   | amd64 \| arm64   |
      | Maintainer     | non-empty       |
      | Description    | non-empty       |
    And `dpkg-deb --contents <pkg>` lists:
      | path                                |
      | /usr/bin/dblock                     |
      | /lib/systemd/system/dblock.service  |
      | /etc/dblock/config.yaml             |
      | /var/lib/dblock/                    |

  @fsid:FS-PackagingDebInstallsCleanly
  Scenario: Installing the .deb on a fresh Debian container
    Given a freshly-pulled debian:bookworm container
    When `dpkg -i dblock_<version>_amd64.deb` runs
    Then exit code is 0
    And `/usr/bin/dblock --version` prints a version + commit + go-version line
    And `systemctl status dblock` reports active (running) once enabled

  @fsid:FS-PackagingSystemdUnit
  Scenario: systemd unit binds as the unprivileged dblock user
    Given the dblock package installed
    When `systemctl start dblock` runs
    Then the process runs as user `dblock`, group `dblock`
    And it can bind UDP/TCP :53 via AmbientCapabilities=CAP_NET_BIND_SERVICE
    And dblock's data lives under /var/lib/dblock with mode 0700

  @fsid:FS-PackagingProxmoxLxcScript
  Scenario: scripts/proxmox-create.sh provisions a working LXC container
    Given an operator with `pct` on a Proxmox host
    When `scripts/proxmox-create.sh --id 200 --hostname dblock-1` runs
    Then a Debian 12 LXC with id 200 exists
    And the dblock .deb is installed inside
    And /api/v1/health responds 200 from the container's IP within 30s

  Non-goals:
    - RPM packaging (deferred until anyone asks)
    - Snap / Flatpak (operator-level config is more portable as native)
    - Brew formula (Linux daemon, not a CLI tool for developers)
    - Auto-publishing to apt.dblock.io (M5.7 multi-arch releases first)
