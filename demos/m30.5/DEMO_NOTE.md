# M30.5 — Custom Filtering Rules (Regex + Exact)

## Implemented

### Filter Engine — Custom Rules Layer
- `internal/filter/custom_rules.go` — NEW: `ParseCustomRules()`, `customRuleSet.evaluate()`
- Supports 4 rule types (AdGuard Home-compatible syntax):
  - `/regex/` — block domains matching the regex pattern (case-insensitive)
  - `@@/regex/` — allow domains matching the regex pattern
  - `domain` — exact block (also matches all sub-domains)
  - `@@domain` — exact allow
- Allow rules are evaluated before block rules (allow > block for same domain)
- Empty lines and `#` comment lines are ignored
- Rule evaluation inserted at highest priority: after global pause, before global allowlist

### Cluster Storage — Raft Command
- `CmdCustomRulesSet` Raft command stores raw text bytes under `bucketSettings["custom_rules"]`
- `Cluster.SetCustomRules(rules string) error` — applies via Raft, replicated to all nodes
- `Snapshot()` and `importM1Config()` persist/restore `CustomRules` through `config.FilteringConfig`

### API
- `GET /api/v1/custom-rules` — returns current rules text as `{"rules": "..."}`
- `PUT /api/v1/custom-rules` — validates all rules via `ParseCustomRules` before Raft commit; returns 422 on invalid regex

### Web UI
- New page: `web/src/views/CustomRules.vue` — textarea editor with syntax reference card, save/discard, rule count, error banner
- Navigation: "Custom Rules" entry with `PencilSquareIcon` in Filtering section of Shell.vue
- Route: `/dashboard/custom-rules`

## Not Implemented

- Per-profile custom rules (cluster-wide only; M30.5 is intentionally scoped to cluster-wide)
- Rule ordering / priority UI (allow > block is enforced by engine, not configurable)
- Wildcard syntax beyond `*.apex` prefix matching (regexes cover all advanced patterns)
- Import/export of rules as separate file (rules are part of cluster config export)

## Validation

Acceptance tests: **8/8 pass** (Docker harness, `SKOED_BINARY` env):
- `TestCustomRulesRegexBlock` — `/regex/` blocks matching domain → NXDOMAIN (FS-CustomRulesRegexBlock)
- `TestCustomRulesExactBlock` — bare domain blocks apex and sub-domains (FS-CustomRulesExactBlock)
- `TestCustomRulesRegexAllow` — `@@/regex/` overrides blocklist for matching domain (FS-CustomRulesRegexAllow)
- `TestCustomRulesExactAllow` — `@@domain` allows domain blocked by blocklist (FS-CustomRulesExactAllow)
- `TestCustomRulesPriority` — allow rule beats block rule for same domain (FS-CustomRulesPriority)
- `TestCustomRulesOverrideBlocklist` — custom allow overrides active blocklist entry (FS-CustomRulesOverrideBlocklist)
- `TestCustomRulesEdit` — PUT replaces rules; old rules no longer active (FS-CustomRulesEdit)
- `TestCustomRulesValidation` — invalid regex → 422, cluster state unchanged (FS-CustomRulesValidation)

## Proxmox Enterprise Validation (2026-06-24)

3-node Raft cluster: CT200 (skoed-1 / 10.0.0.100), CT201 (skoed-2 / 10.0.0.101), CT202 (skoed-3 / 10.0.0.102) — Alpine Linux.
Binary: `skoed v0.2.4-15` (M30.5 commit on `feature/m30.5-custom-regex-rules`).

**API validation (skoed-2, leader):**
- `PUT /api/v1/custom-rules` with regex rules → 200 OK
- `GET /api/v1/custom-rules` on skoed-2 → rules returned
- DNS: `dig @10.0.0.101 ad123.example.com` → NXDOMAIN (regex `/^ad[0-9]+\./` matched) ✓
- DNS: `dig @10.0.0.101 regular.example.com` → NOERROR (no match) ✓

**Cluster replication:**
- `GET /api/v1/custom-rules` on skoed-3 (follower) → same rules as leader ✓
- DNS: `dig @10.0.0.102 ad123.example.com` → NXDOMAIN (rules replicated via Raft) ✓

**Validation rejection:**
- `PUT /api/v1/custom-rules` with `/[unclosed/` → 422 Unprocessable Entity ✓

## Screenshots

- `ss-30.5-01-editor.png` — Custom Rules editor page with syntax reference
- `ss-30.5-02-cluster-replication.png` — 3-node replication status
- `ss-30.5-03-validation.png` — 422 rejection for invalid regex
