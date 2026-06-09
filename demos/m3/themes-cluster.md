# Demo Note — M3 themes + 5-node cluster screenshots

**Date:** 2026-06-04
**Branch:** master (M3 already merged)
**Image:** `skoed:m3`

## What this proves

1. The skoed Web UI ships with **four palettes** (Monokai Solarized,
   Monokai vivid, Monokai Blue, Monokai Pro) and a light/dark toggle
   per palette — eight visual combinations in total.
2. An **n=5 Raft cluster** comes up cleanly via the same join-token
   flow used in the M2 demo. All five nodes (1 leader + 4 followers)
   converge to the same commit_index and the Web UI's Cluster page
   surfaces the full topology with per-node sync state and last-contact.

## Cluster topology

| Container | Role     | API host port | DNS host port |
|-----------|----------|---------------|---------------|
| skoed-1  | leader   | 8091          | 5391          |
| skoed-2  | follower | 8092          | 5392          |
| skoed-3  | follower | 8093          | 5393          |
| skoed-4  | follower | 8094          | 5394          |
| skoed-5  | follower | 8095          | 5395          |

All five on the user-defined network `skoed-m3`, leader bootstrapped
with default config, the four followers enrolled via single-use
tokens minted by skoed-1.

Seed state: one inline blocklist (`ads` with 3 domains), one profile
(`kids` scoped to `192.168.10.0/24` with SafeSearch enabled for Google
and YouTube), 20+ DNS queries spread across the five nodes' DNS
listeners to populate the query log.

## Theme additions (this session)

Two palettes added beyond the M2.6 baseline:

- **Monokai Blue** — cool blue-tinted variant. Light mode = soft
  blue-paper canvas (`#EEF2F8`) with `#2563EB` accent. Dark mode =
  navy ground (`#0F1B2C`) with `#60A5FA` accent.
- **Monokai Pro** — Wimer Hazenberg's modern Monokai. Light mode =
  cream paper (`#FAF4ED`) with muted teal `#1A8BAB` accent. Dark mode =
  canonical Pro `#2D2A2E` ground with `#78DCE8` (Pro cyan) accent and
  `#FF6188` (Pro red), `#A9DC76` (Pro green), `#FC9867` (Pro orange).

Implementation notes:

- The old `html.dark` global override is replaced by per-palette
  `html[data-palette="…"]:not(.dark)` (light) and
  `html.dark[data-palette="…"]` (dark) cascades in
  `web/src/style.css`. Adding a fifth palette = appending another
  block; no token churn in `tailwind.config.js`.
- `stores/theme.ts`'s `Palette` type now lists all four IDs; the
  Shell's palette `<select>` has all four options.
- Theme + mode persist in `localStorage` and apply before first paint
  via `useThemeStore().applyOnStartup()` in `main.ts`.

## Screenshots captured

`docs/screenshots/cluster-<palette>-<mode>.png`, ~90 KB each:

| Palette | Light | Dark |
|---|---|---|
| Monokai Solarized | `cluster-solarized-light.png` | `cluster-solarized-dark.png` |
| Monokai (vivid)   | `cluster-monokai-light.png`   | `cluster-monokai-dark.png`   |
| Monokai Blue      | `cluster-blue-light.png`      | `cluster-blue-dark.png`      |
| Monokai Pro       | `cluster-pro-light.png`       | `cluster-pro-dark.png`       |

Captured via Playwright (`web/shoot-cluster.mjs`) against the live
5-node cluster. Each shot shows:

- The header card: `MODE = cluster`, `STATUS = ok`, `LEADER = skoed-1`,
  `RAFT TERM = 2`, `MEMBERS = 5 / 5`.
- The nodes table: 5 rows, leader row's role badge in the palette's
  accent color, all sync states green ("in sync"), commit indices all
  equal (36), last_contact "just now".
- The "Add a node" panel with the Generate token CTA.
- The sidebar's status block at the bottom (`node skoed-1`, `role leader`,
  `mode cluster`, `term 2 commit 36`).

## Verification commands

```sh
# bring up the 5-node cluster
docker network create skoed-m3
docker run -d --name skoed-1 --network skoed-m3 --hostname skoed-1 \
  -v /tmp/skoed-m3/n1:/var/lib/skoed -p 8091:8080 -p 5391:53/udp \
  skoed:m3 --config /var/lib/skoed/config.yaml
# (write config.yaml with bootstrap.token + bootstrap.leader_address
#  for n2…n5; tokens minted via POST /api/v1/cluster/tokens on skoed-1)

# verify
curl -s -u admin:demo1234 http://localhost:8091/api/v1/cluster/health
curl -s -u admin:demo1234 http://localhost:8091/api/v1/cluster/status

# capture screenshots
cd web && node shoot-cluster.mjs
```

## Cleanup

```sh
docker rm -f skoed-{1..5}
docker network rm skoed-m3
sudo rm -rf /tmp/skoed-m3
```
