# DEMO NOTE — M5.5 Native Packaging (.deb + Proxmox LXC)

## Scope

Operators install dblock via `dpkg -i dblock_<version>_<arch>.deb` (or
`apt install ./dblock_*.deb`) on Debian / Ubuntu. Proxmox operators
run one helper script to provision a fresh Debian 12 LXC with dblock
pre-installed and the service enabled.

### Implemented

- **`packaging/nfpm.yaml`** — nfpm config producing a Debian package
  with: binary at `/usr/bin/dblock`, systemd unit at
  `/lib/systemd/system/dblock.service`, default config at
  `/etc/dblock/config.yaml` (conffile, no replace on upgrade), data
  dir at `/var/lib/dblock` (mode 0700, owned by `dblock:dblock`),
  log dir at `/var/log/dblock`.
- **systemd unit** (`packaging/dblock.service`):
  - Runs as the unprivileged `dblock` user/group.
  - Binds privileged ports via `AmbientCapabilities=CAP_NET_BIND_SERVICE`.
  - Hardened with `NoNewPrivileges`, `ProtectSystem=strict`,
    `ProtectHome`, `PrivateTmp`, `ProtectKernelTunables`,
    `ProtectKernelModules`, `ProtectControlGroups`,
    `RestrictNamespaces`, `RestrictRealtime`, `LockPersonality`,
    `ReadWritePaths=/var/lib/dblock /var/log/dblock`.
- **Maintainer scripts** (`packaging/scripts/`):
  - `preinst.sh` — creates the `dblock` system user/group via
    `adduser --system`.
  - `postinst.sh` — `systemctl daemon-reload`, fixes ownership,
    prints next-steps. Does NOT auto-start — operator runs
    `systemctl enable --now dblock`.
  - `prerm.sh` — stops + disables the service; leaves data in place
    so `apt remove` + reinstall keeps cluster state.
  - `postrm.sh` — on `apt purge`, wipes `/var/lib/dblock`,
    `/var/log/dblock`, `/etc/dblock`, and the user/group.
- **`scripts/proxmox-create.sh`** — provisions a fresh Debian 12 LXC
  via `pct create`, copies the .deb in, installs + enables dblock,
  waits for `/api/v1/health` to respond. Idempotent (refuses to
  overwrite an existing container ID).
- **`Makefile` (repo root)** — convenience targets:
  - `make build`       → builds the binary
  - `make deb`         → builds `dist/dblock_<version>_amd64.deb`
  - `make deb-arm64`   → arm64 variant (used by M5.7)
  - `make test-deb`    → smoke-tests the .deb in `debian:bookworm`
  - `make test` / `acceptance` → in-Docker acceptance suite
- **`packaging/test-deb.sh`** — smoke-tests the .deb against a clean
  `debian:bookworm` container. Asserts:
  - `dpkg-deb --info` carries Package, Version, Architecture,
    Maintainer, Description.
  - `dpkg-deb --contents` lists `/usr/bin/dblock`,
    `/lib/systemd/system/dblock.service`, `/etc/dblock/config.yaml`,
    `/var/lib/dblock/`.
  - `dpkg -i` exits 0 (postinst runs cleanly).
  - The `dblock` system user/group is created.
  - `/usr/bin/dblock --help` runs.

### Validation

Local run on this branch:

```
$ make deb
created package: dist/dblock_0.5.0-1_amd64.deb

$ packaging/test-deb.sh
==> testing dist/dblock_0.5.0-1_amd64.deb
[... dpkg-deb --info ...]
Adding system user `dblock' ...
Setting up dblock (0.5.0-1) ...
dblock installed.
[... binary --help ...]
uid=100(dblock) gid=101(dblock) groups=101(dblock)
==> OK
```

### Not implemented (deferred / non-goals)

- **`.rpm`**: nfpm supports it; flip the packager when anyone asks.
- **`apt.dblock.io` repo + signed-by-cosign**: M5.7 publishes the
  artefacts; turning them into an APT repo is M5.5.1.
- **Snap / Flatpak**: operator-level config is more portable as
  native; not chasing.
- **`Type=notify` systemd unit**: `Type=simple` for v1. Wire `sd_notify`
  when M5.6 in-place upgrade wants graceful restart.
- **lintian-clean**: a few warnings about debian/changelog and
  watchfile remain; cleaned up in M5.5.1.
- **In-Docker `systemctl start` validation**: would need
  `--privileged` + systemd inside the container, which our local
  docker authz plugin blocks. CI environment doesn't have that
  constraint; the script is structured to skip cleanly when
  systemd-analyze isn't available.

### Files added

```
packaging/
  nfpm.yaml
  dblock.service
  config.example.yaml
  test-deb.sh
  scripts/
    preinst.sh
    postinst.sh
    prerm.sh
    postrm.sh
scripts/
  proxmox-create.sh
Makefile              (repo root)
```

## Demo

```bash
# Build the .deb.
make build && make deb
# → dist/dblock_0.5.0-1_amd64.deb (6.5 MB stripped)

# Install on a Debian box.
sudo apt install ./dist/dblock_0.5.0-1_amd64.deb
sudo systemctl enable --now dblock
sudo systemctl status dblock

# Or, on Proxmox:
scripts/proxmox-create.sh \
  --id 200 \
  --hostname dblock-1 \
  --deb dist/dblock_0.5.0-1_amd64.deb
# Creates LXC 200, installs the .deb inside, enables the service,
# polls /api/v1/health until it returns 200, then prints the IP +
# the first-run auth-setup curl line.
```

## Next

M5.6 — In-place upgrade (depends on this packaging + M5.7 multi-arch).
