# Install on Debian / Ubuntu

dblock ships a Debian package built by [nfpm](https://nfpm.goreleaser.com/).
Tested on Debian 12 (bookworm), Debian 13 (trixie), Ubuntu 24.04 LTS.

## Quick install

```sh
# Grab the latest .deb from the GitHub releases page.
ARCH=$(dpkg --print-architecture)          # amd64 or arm64
VERSION=0.5.0
curl -fsSLO "https://github.com/dblock/dblock/releases/download/v${VERSION}/dblock_${VERSION}_${ARCH}.deb"

# Install (apt auto-resolves the adduser dependency).
sudo apt install -y "./dblock_${VERSION}_${ARCH}.deb"

# Enable + start.
sudo systemctl enable --now dblock
sudo systemctl status dblock --no-pager
```

## What the package installs

| Path | Purpose |
|------|---------|
| `/usr/bin/dblock` | the binary (statically linked, ~9 MB) |
| `/lib/systemd/system/dblock.service` | hardened systemd unit |
| `/etc/dblock/config.yaml` | default config (conffile — your edits survive upgrades) |
| `/var/lib/dblock/` | data dir, owned by `dblock:dblock` (mode 0700) |
| `/var/log/dblock/` | log dir, owned by `dblock:dblock` |

## systemd unit details

The unit runs as the unprivileged **`dblock`** user, with
`AmbientCapabilities=CAP_NET_BIND_SERVICE` so port 53 binds without
root. Other hardening: `NoNewPrivileges`, `ProtectSystem=strict`,
`ProtectHome`, `PrivateTmp`, `ProtectKernelTunables`,
`RestrictNamespaces`, `RestrictRealtime`, `LockPersonality`,
`ReadWritePaths` scoped to `/var/lib/dblock` + `/var/log/dblock`.

## Upgrade

```sh
sudo apt install -y ./dblock_<new-version>_${ARCH}.deb
sudo systemctl restart dblock
```

Conffiles (`/etc/dblock/config.yaml`) are preserved by default. dpkg
will prompt only if YOU edited the file AND we shipped a new default
in the new release.

## Remove

```sh
sudo apt remove dblock        # stops + disables; preserves data
sudo apt purge  dblock        # also wipes /var/lib/dblock + /etc/dblock + the user
```

## Next

- [Set the admin password](../first-run/auth-setup.md)
- [Bootstrap a 3-node cluster](../cluster/bootstrap.md)
