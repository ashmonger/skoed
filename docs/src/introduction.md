# Introduction

**skoed** is a self-hosted DNS filtering daemon with multi-node sync.
It's a drop-in alternative to [Pi-hole](https://pi-hole.net/) and
[AdGuard Home](https://adguard.com/en/adguard-home/overview.html) for
households and small offices that want:

- **One config, many nodes** — every change replicates through Raft;
  no per-node tickling.
- **DoH (RFC 8484) + DoT (RFC 7858) servers** built in. Optional
  Let's Encrypt ACME for a publicly-reachable resolver.
- **Per-client profiles** with schedules (kid-mode 19:00–07:00) and
  pre-baked categories (DoH probes, social, gambling, …).
- **Encrypted cluster mesh** (mTLS for Raft + internal API) so the
  cluster runs safely without a separate VPN.
- **Operator-friendly observability**: Prometheus `/metrics`,
  replicated audit log, Dashboard alert cards for stale blocklists
  and identity-spoofing anomalies.
- **One-binary deploy**: static, ~9 MB stripped, runs on a Raspberry
  Pi 4 or a 1-vCPU Proxmox LXC just as well as a managed Kubernetes.

## Three deployment shapes

| Shape | Best for | Get started |
|-------|----------|-------------|
| `.deb` on a Debian / Ubuntu host | Bare-metal, low-fuss | [Debian / Ubuntu](install/debian-ubuntu.md) |
| Proxmox LXC | Self-hosters with a Proxmox cluster | [Proxmox LXC](install/proxmox-lxc.md) |
| Docker / Kubernetes | Existing container infra | [Docker](install/docker.md) / [Kubernetes](install/kubernetes.md) |

## What skoed is NOT

- A DHCP server — skoed *reads* leases from Kea / dnsmasq / generic
  HTTP-JSON sources to enrich the query log; serving leases is a
  permanent non-goal.
- A captive-portal / parental-control proxy — DNS filtering is the
  hammer; if a client uses hard-coded resolver IPs, a firewall rule
  is your only ETF (skoed surfaces DoH probes so you know which
  clients to chase).
- A general-purpose HTTPS reverse proxy. Use Caddy / Traefik /
  ingress-nginx for that.

## License

MIT. See [LICENSE](https://github.com/skoed/skoed/blob/main/LICENSE).
