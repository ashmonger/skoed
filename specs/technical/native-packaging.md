---
x-tsid: TS-NativePackaging
x-fsid-links:
  - FS-PackagingDebBuilds
  - FS-PackagingDebInstallsCleanly
  - FS-PackagingSystemdUnit
  - FS-PackagingProxmoxLxcScript
---

# TS-NativePackaging — .deb + Proxmox LXC

## Tooling

[`nfpm`](https://nfpm.goreleaser.com/) is the .deb builder. Single
binary, declarative YAML config, supports both .deb and .rpm if we
ever need it. Bundled into the dev container; CI pins a version.

## Layout (post-install)

```
/usr/bin/skoed                       # binary, mode 0755
/lib/systemd/system/skoed.service    # systemd unit
/etc/skoed/config.yaml               # default config (conffile)
/var/lib/skoed/                      # data_dir, owned by skoed:skoed 0700
/var/log/skoed/                      # logs (when not journalctl)
```

## systemd unit shape

```
[Unit]
Description=skoed — self-hosted DNS filtering with multi-node sync
Documentation=https://github.com/skoed/skoed
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=/usr/bin/skoed --config /etc/skoed/config.yaml
Restart=on-failure
RestartSec=3

User=skoed
Group=skoed
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/skoed /var/log/skoed
StateDirectory=skoed

[Install]
WantedBy=multi-user.target
```

The `Type=notify` is M5.5.1 — for v1 the unit is `Type=simple`. skoed
doesn't yet sd_notify; revisit when in-place upgrade (M5.6) wants
graceful restart.

## Package metadata

```yaml
# packaging/nfpm.yaml
name: skoed
arch: amd64           # overridden by `nfpm pkg --target amd64|arm64`
version: 0.5.0        # injected by Makefile via -X main.version
maintainer: skoed maintainers <maintainers@skoed.io>
description: |
  Self-hosted DNS filtering with multi-node sync. Drop-in alternative
  to Pi-hole / AdGuard-Home for households and small offices.
section: net
priority: optional
contents:
  - src: ./apps/skoed/skoed
    dst: /usr/bin/skoed
    file_info:
      mode: 0755
  - src: ./packaging/skoed.service
    dst: /lib/systemd/system/skoed.service
  - src: ./packaging/config.example.yaml
    dst: /etc/skoed/config.yaml
    type: config
  - dst: /var/lib/skoed
    type: dir
    file_info: { mode: 0700, owner: skoed, group: skoed }
scripts:
  preinstall:  ./packaging/scripts/preinst.sh
  postinstall: ./packaging/scripts/postinst.sh
  preremove:   ./packaging/scripts/prerm.sh
```

`preinst.sh`: creates the `skoed` system user / group if missing.
`postinst.sh`: `systemctl daemon-reload`; does NOT auto-start (operator
runs `systemctl enable --now skoed` when ready). `prerm.sh`: stops
the service on removal; leaves `/var/lib/skoed` in place.

## Makefile target

```make
deb: build openapi-sync
	@mkdir -p dist
	nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/
```

Multi-arch (M5.7) iterates the target arches via `--arch`.

## Proxmox LXC helper

`scripts/proxmox-create.sh`:

```sh
#!/usr/bin/env bash
# Usage: scripts/proxmox-create.sh --id 200 --hostname skoed-1 \
#        [--storage local-lvm] [--bridge vmbr0]
#
# Creates a Debian 12 LXC container, copies the latest skoed .deb in,
# installs + enables the service. Idempotent: re-running with the same
# --id is rejected (no accidental overwrite).
```

Defaults to 1 vCPU + 512 MB RAM + 4 GB disk (matches the M1 resource
profile in PROBLEM_STATEMENT). Skipping unprivileged-container details
in this spec — the script just calls `pct create` with sensible
defaults; operators tune from there.

## Validation

- `dpkg-deb --info dist/skoed_*_amd64.deb` shows required fields.
- `dpkg-deb --contents` lists the expected paths.
- `lintian` runs in CI; warnings allowed for now (LinPkg policy
  compliance is M5.5.1).
- In-Docker smoke: spin a `debian:bookworm` container, install the
  .deb, `systemctl status` — proves install + unit shape.

Acceptance is intentionally NOT a full Go acceptance test. Package
artifacts are out-of-scope for the Go test harness; instead a
`packaging/test-deb.sh` runs in CI and exits non-zero on failure.
