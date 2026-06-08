# Install in a Proxmox LXC

`scripts/proxmox-create.sh` provisions a fresh Debian 12 LXC, copies
the .deb in, and enables the service in one command.

```sh
# On the Proxmox host (needs `pct`):
git clone https://github.com/dblock/dblock.git
cd dblock

# Grab a .deb (or build one with `make deb`).
DEB=/root/dblock_0.5.0_amd64.deb
wget -O "$DEB" https://github.com/dblock/dblock/releases/download/v0.5.0/dblock_0.5.0_amd64.deb

# Provision LXC 200 named dblock-1 on default storage + bridge.
scripts/proxmox-create.sh --id 200 --hostname dblock-1 --deb "$DEB"
```

### Flags

| Flag        | Default                 | What it does                          |
|-------------|-------------------------|---------------------------------------|
| `--id`      | required                | LXC container ID                      |
| `--hostname`| required                | container hostname                    |
| `--deb`     | required                | path to the .deb on the Proxmox host  |
| `--storage` | `local-lvm`             | Proxmox storage pool                  |
| `--bridge`  | `vmbr0`                 | bridge for `--net0`                   |
| `--memory`  | `512` (MB)              | RAM                                   |
| `--cores`   | `1`                     | CPU cores                             |
| `--disk`    | `4` (GB)                | rootfs size                           |
| `--template`| `debian-12-standard…`   | LXC template (must be pre-downloaded) |

The script:

1. Refuses to overwrite an existing container ID.
2. `pct create` with the defaults above + `--unprivileged 1 --onboot 1`.
3. `pct start`; waits up to 20 s for DNS to come up inside the LXC.
4. `pct push` the .deb, `dpkg -i`, `systemctl enable --now dblock`.
5. Polls `/api/v1/health` on the container's IP for 30 s.
6. Prints the IP + the first-run `auth-setup` curl line.

### Multiple containers

Re-run with different `--id` and `--hostname` to grow a cluster:

```sh
scripts/proxmox-create.sh --id 200 --hostname dblock-1 --deb "$DEB"
scripts/proxmox-create.sh --id 201 --hostname dblock-2 --deb "$DEB"
scripts/proxmox-create.sh --id 202 --hostname dblock-3 --deb "$DEB"
```

Then proceed to [Bootstrap a 3-node cluster](../cluster/bootstrap.md).
