---
x-tsid: TS-MakeDev
x-fsid-links:
  - FS-MakeDevStartsBothProcesses
  - FS-MakeDevHmrOnVueEdit
  - FS-MakeDevCleanCtrlCShutdown
---

# TS-MakeDev — `make dev` SPA hot-reload

## Goal

A single command (`make dev`) gives the SPA developer an iterate loop
measured in milliseconds:

1. Edit `web/src/**/*.vue` (or `.ts`, `.css`).
2. Vite's HMR updates the module in the browser.
3. The Go backend keeps running unchanged — no `go build`, no daemon restart.

This is strictly a developer-loop convenience. The production model
(SPA built and embedded via `go:embed` into the dblock binary at
`apps/dblock/internal/api/static/dist/`) is untouched. `make build`
keeps doing exactly what it did before.

## Topology while running

```
                            ┌──────────────────────────────────────┐
                            │  ~/tmp/dblock-dev/                   │
                            │   config.yaml                        │
                            │   dblock.log                         │
                            │   raft/, bbolt, … (real data dir)    │
                            └──────────────────────────────────────┘
                                              ▲
                                              │ --config + data_dir
                                              │
       browser                                ▼
    http://127.0.0.1:5173 ─────────►  vite dev server (port 5173)
                                             │
                                             │ proxy   /api/*     ──┐
                                             │         /metrics   ──┤
                                             ▼                      ▼
                                       dblock daemon (port 18099, plain HTTP)
```

- `vite dev` (port 5173) serves `web/index.html` + on-the-fly transformed
  `.vue` / `.ts` / `.css` with HMR over its websocket.
- `dblock` (port 18099) runs as a single-node, no-cluster instance from a
  dedicated `~/tmp/dblock-dev` data dir. Its DNS listener is held to a
  high unprivileged port so this works without sudo.
- Any browser request to `/api/*` or `/metrics` is forwarded by vite's
  built-in proxy to `127.0.0.1:18099`.

## Files added

```
Makefile                  → new `dev` target (kicks off scripts/dev.sh)
scripts/dev.sh            → orchestrates dblock + vite, traps SIGINT/SIGTERM
web/vite.config.ts        → server.proxy retargeted to :18099, /metrics added
```

No new npm dependencies; vite already ships HMR + http-proxy.
No new Go dependencies.

## `make dev` target

```makefile
dev:
	scripts/dev.sh
```

A thin shim around `scripts/dev.sh` so the orchestration logic — signal
trapping, log piping, config bootstrap — lives in shell where it's
readable, not in tab-significant Makefile recipes.

## `scripts/dev.sh` contract

Inputs (environment overrides, all optional):

| Var                 | Default                       | Purpose                            |
|---------------------|-------------------------------|------------------------------------|
| `DBLOCK_DEV_PORT`   | `18099`                       | management API + proxy target      |
| `DBLOCK_DEV_DIR`    | `~/tmp/dblock-dev`            | data dir + config + logs           |
| `DBLOCK_DEV_DNS_PORT` | `15353`                     | unprivileged DNS bind              |
| `VITE_DEV_PORT`     | `5173`                        | vite dev server                    |

Behaviour:

1. `mkdir -p $DBLOCK_DEV_DIR`.
2. If `$DBLOCK_DEV_DIR/config.yaml` is absent, write a minimal one:
   ```yaml
   node:
     id: dblock-dev
     raft_address: 127.0.0.1:17000
     api_address: 127.0.0.1:18099
     data_dir: <expanded $DBLOCK_DEV_DIR>
     dns:
       listen:
         port: 15353
         ipv4: true
         ipv6: false
   ```
3. Build the dblock binary if `apps/dblock/dblock` doesn't already exist
   (one-time; subsequent `make dev` runs reuse it). The Go binary doesn't
   need to be rebuilt on UI edits — that's the whole point.
4. Launch dblock in the background, redirect stdout+stderr to
   `$DBLOCK_DEV_DIR/dblock.log`. Capture its PID.
5. Wait up to 10 s for `http://127.0.0.1:18099/api/v1/health` to return
   any non-connection-refused response. Fail loudly if the daemon never
   comes up (operator's stack is broken, surface it).
6. `cd web && npx vite --port 5173`. Capture its PID.
7. Trap SIGINT, SIGTERM, EXIT — on signal, send SIGTERM to both PIDs and
   `wait` for them. Idempotent so a double Ctrl-C doesn't leak.
8. Exit with the vite exit code (vite is the foreground process).

## `vite.config.ts` server.proxy

```ts
server: {
  port: 5173,
  proxy: {
    '/api':     'http://127.0.0.1:18099',
    '/metrics': 'http://127.0.0.1:18099',
  },
},
```

Vite's http-proxy honours WebSocket upgrades transparently for `/api/*`
endpoints that switch protocol (e.g. the M3 SSE query-log stream rides
the HTTP path; no special handling needed).

## Why a known port (18099) instead of :8080?

- Avoids clashing with any locally-running production dblock the operator
  has on `:8080`.
- The 18xxx range matches the existing M2.5 helm test convention
  (`tests/acceptance/`-spawned nodes pick from 18xxx-19xxx).
- One less surprise when the operator runs `dblock health --api
  http://127.0.0.1:18099`.

## What this does NOT do (verified non-goals)

- It does NOT auto-rebuild the Go binary on `*.go` change. The whole
  point is that backend edits and frontend edits have different cadences;
  for backend HMR-ish loops the operator wires `air` or `entr` to
  `make build` themselves.
- It does NOT spawn multiple nodes. The roadmap's `make dev-cluster`
  stretch goal is out of scope here.
- It does NOT touch `make build`. The production embed pipeline is
  unchanged and the embedded `apps/dblock/internal/api/static/dist`
  copy still wins when an operator opens `http://127.0.0.1:18099/`
  directly (without going through vite).

## Smoke test (no Go acceptance test)

This is a developer-loop change, not a product behaviour change. The
verification is shell-level:

```sh
make dev &  # background
sleep 5
curl -sf -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5173/
curl -sf -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5173/api/v1/health
kill -INT %1
wait
# expect: two 200s, then both PIDs gone from `ps`.
```

Done as part of the M5.9.2 demo, recorded in `DEMO_NOTE_M5.9.2.md`.

## Risks and trade-offs

- **Port-in-use races.** If another local process holds :5173 or :18099,
  vite / dblock will fail loudly at startup — that's the right behaviour
  (operator was about to be very confused).
- **Different code path from production.** Vite dev serves uncompiled
  modules; production serves the rolled-up bundle. A bug that only
  reproduces in the bundle (rare; mostly tree-shaking or Rollup-specific)
  still requires a full `make build`. Documented as a known limitation.
- **State persistence.** `~/tmp/dblock-dev` persists across `make dev`
  runs by design (so operator's local blocklists / auth survive). To
  reset, the operator removes the directory manually (`rm -rf
  ~/tmp/dblock-dev`).
