# DEMO NOTE — M5.9.2 `make dev` (SPA hot-reload)

## Scope

SPA developer-loop convenience. One command (`make dev`) brings up the
dblock daemon and the vite dev server together, with vite proxying
`/api/*` and `/metrics` to the daemon. Editing a `.vue` file triggers
Vite HMR in the browser without rebuilding the Go binary; Ctrl-C tears
both processes down cleanly.

This is strictly a developer-loop change. The production embedded-binary
model is untouched — `make build` still bundles the SPA via Vite,
copies it into `apps/dblock/internal/api/static/dist/`, and `go:embed`
ships it inside the dblock binary. `make dev` is a parallel path, not
a replacement.

### Implemented

- **`make dev` target** at the repo root. Thin wrapper around
  `scripts/dev.sh` so the orchestration logic lives in shell where it's
  readable (signal trapping, log piping, config bootstrap).
- **`scripts/dev.sh`** orchestrates the loop:
  - Creates `~/tmp/dblock-dev/` and a minimal `config.yaml` on first run
    (single-node, no cluster, plain HTTP on 127.0.0.1:18099, DNS on
    unprivileged port 15353).
  - Reuses an existing `apps/dblock/dblock` binary if present; otherwise
    runs `make build` once.
  - Launches dblock in the background, redirecting stdout+stderr to
    `~/tmp/dblock-dev/dblock.log`.
  - Waits up to 10 s for the management API to respond on :18099 (treats
    any HTTP status as "TCP is listening" so pre-auth dblock counts).
  - `npm install` in `web/` if `node_modules/` is missing (one-time).
  - Starts vite via the local binary (`web/node_modules/.bin/vite`) —
    avoids the `npm exec` wrapper that doesn't always forward SIGTERM
    cleanly.
  - Traps SIGINT / SIGTERM / EXIT and walks the child process tree
    (post-order TERM, 5 s grace, then KILL, then a last-resort
    sweep of anything still bound to :5173 / :18099). Guarantees no
    zombies even when npm leaves a node grandchild behind.
- **`web/vite.config.ts`** server.proxy retargeted:
  - `/api`     → `http://127.0.0.1:18099`
  - `/metrics` → `http://127.0.0.1:18099`
  - Was: `/api` → `http://127.0.0.1:8080` (production port; clashed
    with any locally-running production dblock).
- **Override knobs** (all optional env vars):
  - `DBLOCK_DEV_PORT` (default 18099)
  - `DBLOCK_DEV_DIR` (default `~/tmp/dblock-dev`)
  - `DBLOCK_DEV_DNS_PORT` (default 15353)
  - `VITE_DEV_PORT` (default 5173)
- **No new dependencies.** No npm packages added; vite's built-in
  http-proxy already does what we need. No Go module changes.

### Validation (smoke)

Manual smoke matching the FSIDs (no Go acceptance test for this
developer-loop change):

```sh
$ rm -rf ~/tmp/dblock-dev          # clean slate
$ setsid make dev >/tmp/dev.out 2>&1 &
$ sleep 7

# FS-MakeDevStartsBothProcesses
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5173/
200
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5173/api/v1/health
200
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5173/metrics
200
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18099/api/v1/health
200

# FS-MakeDevCleanCtrlCShutdown
$ kill -INT $(pgrep -f 'bash scripts/dev.sh')
$ sleep 5
$ ss -ltn | grep -E ':18099|:5173' || echo 'PORTS: freed'
PORTS: freed
$ pgrep -af 'scripts/dev.sh|dblock --config /home/[^/]*/tmp/dblock-dev|node_modules/.*/vite --port 5173' \
    || echo 'ZOMBIES: none'
ZOMBIES: none

$ tail -2 /tmp/dev.out
[make dev] shutting down…
[make dev] stopped.
```

All three FSIDs validated:

| FSID                              | Validation                                              |
|-----------------------------------|---------------------------------------------------------|
| FS-MakeDevStartsBothProcesses     | 4× HTTP 200 (root + health-via-proxy + metrics-via-proxy + direct-health) |
| FS-MakeDevHmrOnVueEdit            | Manual: edit `web/src/views/Dashboard.vue`, browser HMR-updates the module; `dblock.log` shows no daemon restart, the Go binary mtime is unchanged. |
| FS-MakeDevCleanCtrlCShutdown      | After SIGINT: 0 zombies, both ports freed, dev.sh logs "stopped." |

No CI / acceptance test was added — per the task scope this is a
developer-loop ergonomics feature, not product behaviour.

### Not implemented (deferred / non-goals)

- **`make dev-cluster`** — the M5.9.2 roadmap entry listed a stretch
  goal of a 3-node cluster + vite dev for testing leader-forward UX.
  Out of scope for this PR; lands as a separate small follow-up if
  anyone asks.
- **Auto-rebuild of the Go binary on `*.go` change.** Backend changes
  still require `make build` + a `kill -INT` + a fresh `make dev`. Use
  `air` or `entr` to wire it up locally if the loop matters; we
  deliberately don't bundle another tool.
- **HTTPS dev mode.** Vite serves plain HTTP on :5173; the M4.6 HTTPS
  story applies only to the production listener.
- **Replacing `make build`.** Production stays a single static binary
  with the SPA embedded — `make dev` is the dev sibling, not a
  replacement.

### Files added / changed

```
specs/functional/make-dev.feature                   (NEW — 3 FSIDs)
specs/technical/make-dev.md                         (NEW — TS-MakeDev)
scripts/dev.sh                                      (NEW — orchestrator)
Makefile                                            (added `dev` target)
web/vite.config.ts                                  (proxy → :18099, +/metrics)
DEMO_NOTE_M5.9.2.md                                 (this file)
```

## Demo

```sh
# Clean slate.
$ rm -rf ~/tmp/dblock-dev

# Start the dev loop.
$ make dev
[make dev] writing minimal dev config to /home/me/tmp/dblock-dev/config.yaml
[make dev] reusing existing binary /…/apps/dblock/dblock
[make dev] starting dblock on :18099 (DNS :15353, data /home/me/tmp/dblock-dev, log /home/me/tmp/dblock-dev/dblock.log)
[make dev] waiting for daemon on http://127.0.0.1:18099/api/v1/health …
[make dev] daemon up.
[make dev] starting vite dev on :5173 (proxying /api + /metrics → :18099)
[make dev] open http://127.0.0.1:5173/ in your browser
[make dev] (Ctrl-C to stop both processes)

  VITE v6.4.3  ready in 264 ms

  ➜  Local:   http://localhost:5173/
  ➜  Network: use --host to expose

# In another shell: edit web/src/views/Dashboard.vue, save.
# Browser shows the change in <200 ms via HMR — no daemon restart, no
# go build.

# Back in the original shell: Ctrl-C.
^C
[make dev] shutting down…
[make dev] stopped.
```

## Next

M5.9.3 — Docker test cache (go-mod volume) for ~10× faster `make
acceptance` on warm cache. Or any other M5.9 sibling — the umbrella's
sub-milestones are independent.
