# skoed — Full Codebase Security & Code-Quality Audit

**Date:** 2026-07-03 · **Version:** v0.3.5-dev (commit 0e0b3c7) · **Scope:** ~31.7k LOC Go across 16 packages, all milestones
**Method:** 7 parallel subsystem reviewers + live verification against the 3-node Proxmox cluster
**Interactive report:** published as a Claude artifact (see chat).

**Tally:** 2 Critical · 12 High · 18 Medium · 9 Low.

---

## Fix status — branch `feature/security-audit-fixes` (2026-07-03)

**Fixed & verified (build + acceptance tests green):**
- ✅ **C-1** FSM `EndedAt` determinism — `closePauseHistoryEntry` now takes the payload timestamp. (M35 pause tests pass.)
- ✅ **C-2** Unsigned binary swap — **fully closed** (integrity + authenticity):
  - Mandatory SHA-256 verification in `upgrade.Swap`; interim download `0600`; direct-URL/rolling-upgrade paths require `sha256`.
  - **OpenPGP signature verification**: `checksums.txt` is signed at release time (goreleaser `signs` → `checksums.txt.sig`) with a dedicated release subkey under GPG primary `762F13A88A0D63D5`; skoed embeds the release public key (`internal/upgrade/release_pubkey.asc`, `//go:embed`) and refuses any upgrade whose signature doesn't verify. Uses `golang.org/x/crypto/openpgp` (already a dep → no new module; deprecation exception recorded in `decisions/20260703-UpgradeArtifactSignatureVerification.md`).
  - Custom feeds must reference `checksums_url` + `checksums_sig_url`; inline unsigned checksums are no longer trusted.
  - Tests: `TestUpgradeBinarySwap` (signed happy path), `TestUpgradeRejectsChecksumMismatch` (SHA mismatch), `TestUpgradeRejectsBadSignature` (untrusted signer → refused, binary not swapped). This closes the "authenticated user supplies malicious binary + matching hash" gap.
  - **Release-side TODO for the maintainer:** create the release signing subkey (`gpg --edit-key 762F13A88A0D63D5` → `addkey`), re-export & commit `release_pubkey.asc`, and set `GPG_FINGERPRINT` + import the subkey secret in CI.
- ✅ **H-1** Per-client pause fail-closed on unparseable IPs + handler rejects invalid `client_ip` (400). New test `TestPerClientPauseRejectsInvalidIP`.
- ✅ **H-4** New-dynamic dismiss now a replicated tombstone; tracking skips dismissed IPs. New test `TestNewDynamicClientDismissIsDurable`.
- ✅ **H-6** Bypass passcodes redacted from config exports (`redactProfileSecrets`).
- ✅ **H-7** Webhook HMAC secret redacted from API responses; preserved on secret-less updates.
- ✅ **H-8** Blocklist download SSRF-guarded (`internal/netguard`, dial-time IP check) + 64 MiB response cap.
- ✅ **H-9** Webhook dispatch uses the SSRF-guarded client.
- ✅ **H-11** Join-token consume is now authoritative in the FSM (rejects already-consumed/expired) — closes the TOCTOU.
- ✅ **H-12** `decodeJSON` bounded to 4 MiB (`MaxBytesReader`); server `ReadTimeout` added.
- ✅ **H-13** `X-Served-By` audit actor sanitized (charset/length bound, non-empty fallback).
- ✅ **M-14** Pause-expiry watcher: panic recovery + context cancellation + shutdown wiring + leader-only guard.
- ✅ **M-18 (partial)** DNS-rebinding TOCTOU closed for blocklist/webhook/public-tester fetches via the dial-time `netguard` guard. (XFF-spoofable rate-limit key still open.)

New shared component: `internal/netguard` — SSRF-hardened HTTP client (dial-time rejection of loopback/private/link-local/metadata; defeats DNS-rebinding & redirect SSRF). Test escape hatch `SKOED_ALLOW_PRIVATE_FETCH=1` (harness only; never set in prod).

**Deferred to focused follow-up branches (large / higher-risk — not rushed):**
- ⏳ **H-5** Node-local sessions → needs Raft-replicated sessions or stateless signed tokens + revocation list (cluster-wide auth redesign).
- ⏳ **H-10** DNS recursor bailiwick checks → resolution-correctness-sensitive; needs dedicated DNS test fixtures.
- ⏳ **H-14** DHCP hardening (lease reaper, rate-limit, RELEASE ownership, out-of-pool rejection, real anti-spoof) → multi-part; feature currently disabled on the cluster.
- ⏳ Remaining Mediums (M-1..M-13, M-15..M-17) and Lows — see below.

---

## Overall

The codebase is well-structured and mostly hygienic (bcrypt, `crypto/rand` tokens, SHA-256 token storage,
constant-time cluster-secret compare, RE2 regex, atomic-swap filter engine under RWMutex, hardened non-root
systemd unit, exemplary bounded fetch in `dohresolvers`). Risk concentrates in two areas:

1. **A Critical FSM-determinism bug in the M35 branch about to merge** — reproduced live (54 ms timestamp spread across nodes).
2. **A flat interior trust model** — any `write`-scoped credential is effectively `cluster:admin`, and that scope can trigger an unsigned remote binary swap (authenticated code execution), SSRF, and secret disclosure.

Recurring theme: **silent fail-open on bad input** (unparseable pause IPs pause everyone; unparseable profile
selectors widen `default`; malformed custom-rule imports drop all rules).

## Deployment posture (verified live)

- Runs as **non-root `skoed` user**, `CAP_NET_BIND_SERVICE` only, `NoNewPrivileges`, `ProtectSystem=strict`.
- `SKOED_TEST_MODE` **not** in the prod environment (verified `/proc`) — the DNS IP/MAC-spoof, schedule-time-spoof, and token-TTL-override affordances are inert in prod but ship in the release binary gated only by a runtime env var (should be a build tag).
- Caveat: `ReadWritePaths=/usr/bin` makes the **whole** dir writable (a swapped binary can overwrite any `/usr/bin` binary → priv-esc). Running the binary directly / via the root Docker path forfeits systemd containment.
- DHCP not enabled on the current cluster (and needs `CAP_NET_RAW`).

## M35 branch regressions — FIX BEFORE MERGE

- **C-1** FSM writes non-deterministic `EndedAt` → cluster divergence (live-verified). `store.go:644,1868`.
- **H-1** Per-client pause collapses to "pause whole profile" when client IPs fail to parse. `engine.go:385/543`, `filtering_pause.go:146`.
- **H-4** "Dismiss new dynamic client" is non-durable — resurrected by the next query. `store.go:661/1902`.
- **M-14** Pause-expiry watcher goroutine has no panic recovery / no context / never stops. `main.go:639,1104`.

> Note: my earlier "9/9 replication checks passed" was too shallow — it confirmed entries *existed* but never
> compared `ended_at` across nodes, and the dismiss test issued no follow-up query.

---

## Critical

### C-1 — FSM non-determinism: pause-history close writes `time.Now()` → cluster divergence  [VERIFIED LIVE] [M35]
`internal/cluster/store.go:644` → `:1857-1880` (line 1868).
`CmdProfilePauseClear` calls `closePauseHistoryEntry(tx, profileID)`, which sets `EndedAt = time.Now().UTC()`
inside Apply, ignoring the payload's `EndedAt`. Each node applies at a different instant → divergent
`pause_history` bytes for the same log entry. **Reproduced live: 54 ms spread across nodes 101/102/103.**
**Fix:** pass `p.EndedAt` through to `closePauseHistoryEntry` and use it; never read wall-clock in Apply.

### C-2 — Unsigned remote binary swap → authenticated code execution, fanned to all nodes
`internal/api/handlers/upgrade.go:97-158` · `internal/upgrade/swapper.go:32-59` · authorized at `app.go:1093` (write scope only).
`POST /api/v1/upgrade/start` takes a client `{"url":…}`, downloads over plain HTTP, and renames it over the
running binary — no signature/checksum/publisher check — gated only by `write`. `ClusterUpgradeApply` fans it
to every node. Runs as `skoed` user, but `ReadWritePaths=/usr/bin` → can overwrite any `/usr/bin` binary.
**Fix:** verify a signature (minisign/cosign or signed `checksums.txt`) with a build-time key before swap;
restrict to https + allowlisted host; require `cluster:admin`; forbid arbitrary `url` from external callers.

## High

- **H-1 [M35]** Per-client pause → pauses everyone when client IPs don't parse. `engine.go:385-392/543-551`, unvalidated `filtering_pause.go:146`. Reject unparseable IPs (400); fail closed in the engine.
- **H-4 [M35]** New-dynamic-client dismiss non-durable — Raft delete vs local per-query re-insert. `store.go:661/1902`. Use a replicated tombstone.
- **H-5 [VERIFIED LIVE]** Login sessions are node-local — no cross-node logout/revocation (token from 201 → 401 on 202/203). `session.go:24-53`, `middleware.go:59-68`. Replicate sessions or use signed stateless tokens + revocation list.
- **H-6** Bypass passcodes exported in cleartext in unencrypted backups. `export.go:37-60`, `config.go:312`. Redact from exported profiles.
- **H-7** Webhook HMAC secret returned by `GET /api/v1/webhooks`. `webhooks.go:26`, `config.go:227`. Redact on read.
- **H-8** Authenticated blocklist SSRF + unbounded response. `blocklists.go:86`, `blocklist.go:13-40`. Reuse the tester's SSRF guard + `io.LimitReader`.
- **H-9** Webhook dispatch SSRF to internal/metadata. `dispatcher.go:230-256`. Validate URLs at upsert; block internal targets.
- **H-10** DNS recursor has no bailiwick check → cache poisoning in recursive mode. `recursor.go:131-207`. Enforce in-bailiwick answers/NS/glue.
- **H-11** Join-token single-use TOCTOU. `cluster.go:875-900`, `store.go:326-347`. Make FSM consume authoritative/deterministic.
- **H-12** Unbounded request bodies → pre-auth memory-exhaustion DoS. `handler.go:140-146`; no `ReadTimeout`. Add `MaxBytesReader` + `ReadTimeout`.
- **H-13** Audit actor / node identity spoofable via `X-Served-By` on the cluster-secret path. `middleware.go:37-46`. Derive from mTLS peer cert; strip inbound header.
- **H-14** DHCP server (when enabled): starvation (no reaper/rate-limit, attacker-controlled key), lease hijack by spoofed MAC/DUID, ARP anti-spoof is detection-only and unwired, out-of-pool grants. `server.go:470-550`, `server6.go:414-453`, `arp_check.go:228-298`.

## Medium (18)

Cert-rotation `os.WriteFile` inside Apply tx → divergence on disk fault (`store.go:668`); raft snapshot raw copy
not `tx.WriteTo` → torn read (`fsm.go:94`); `ResetRaftForJoin` drops mTLS stream layer (`cluster.go:213`);
backup download path traversal (`backup.go:154`); no login brute-force throttle (`auth.go:11`); non-const-time
bypass passcode compare (`bypass.go:71`); custom-template XSS via `?domain` (`blockpage/server.go:246`);
unanchored custom regex over-match / allow-rule bypass (`custom_rules.go:61`); permissive YAML parse (dup keys,
no `KnownFields`) (`config.go:509`); glueless-NS unbounded recursion / amplification (`recursor.go:196`);
cache key omits query class (`cache.go:14`); DoT/DoH `skip_verify` TLS-bypass foot-gun (`forwarder.go:186`);
per-query goroutine for device-new hook (`dns/handler.go:94`); **[M35]** pause watcher no panic-recovery/ctx
(`main.go:639,1104`); **[VERIFIED LIVE]** `/metrics` unauthenticated by default (`app.go:456,918`); client-IP
PII + untrusted domains replicated in aggregates (`log/aggregates.go:108`); query-log SSE param injection, no
`QueryEscape` (`query_log_stream.go:332`); public tester SSRF TOCTOU/DNS-rebind + XFF-spoofable rate-limit key
(`public.go:137`), plus `log.Fatalf` in bg join goroutine (`main.go:160`), CLI `InsecureSkipVerify:true`
(`cli/client.go:37`), and a data race on `*lease4` (`dhcp/server.go:442`).

## Low / hygiene (9)

Session tokens as plaintext map keys, non-const-time, never swept (`session.go`); `onApply` swallows
auth-store desync silently (`app.go:573`); custom-rule parse errors swallowed + `Validate()` never parses them
→ fail-open on import (`engine.go:280`); malformed profile selectors silently dropped, can re-widen `default`
(`engine.go:340`); cluster-secret grants blanket `cluster:admin` on all routes + verbatim header forwarding
(`middleware.go:37`); DHCP settings accept unvalidated IPs / unbounded lease time (`dhcp_server.go:114`);
`new_dynamic_clients` + `seenIPs` grow unbounded (`store.go:1902`, `dispatcher.go:69`); upgrade extracts to
world-exec `/tmp` file before rename (`swapper.go:44`); error strings reflected verbatim to clients.

## Verified correct (no action)

bcrypt passwords; `crypto/rand` tokens; raw tokens never persisted/returned; constant-time cluster-secret
compare; mTLS `RequireAndVerifyClientCert`; block/SafeSearch evaluated **before** the DNS cache (no filtered
domain served from cache); cache deep-copies; filter engine RWMutex + atomic swap; RE2 (no ReDoS); Prometheus
label cardinality controlled (no PII/secrets in labels); `dohresolvers/fetch.go` is a model bounded fetch;
`appendPauseHistory` deterministic + caps at 50; hardened systemd unit; chi `Recoverer` guards handler panics.
