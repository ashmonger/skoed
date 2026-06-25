# Custom Filtering Rules — Technical Specification

x-tsid: TS-CustomRules
x-fsid-links: [FS-CustomRulesRegexBlock, FS-CustomRulesExactBlock, FS-CustomRulesRegexAllow, FS-CustomRulesExactAllow, FS-CustomRulesPriority, FS-CustomRulesOverrideBlocklist, FS-CustomRulesEdit, FS-CustomRulesValidation, FS-CustomRulesClusterSync]

## API Contract

### GET /api/v1/custom-rules

Returns the current cluster-wide custom rules text.

**Auth:** Bearer token required.

**Response 200:**
```json
{ "rules": "<rules text>" }
```

The `rules` field is the raw text exactly as stored. Empty string when no rules are configured.

### PUT /api/v1/custom-rules

Replaces the cluster-wide custom rules text atomically via Raft.

**Auth:** Bearer token required. Write-forwarded to cluster leader.

**Request:**
```json
{ "rules": "<rules text>" }
```

**Validation:** Each line is parsed before applying. If any line contains an invalid regex pattern the request is rejected before any Raft command is issued.

**Response 200:** `{ "rules": "<accepted text>" }`

**Response 422:** `{ "error": "invalid rule: line N: invalid regex \"pattern\": ..." }`

---

## Storage

Custom rules are stored as raw bytes in the `bucketSettings` bbolt bucket under key `custom_rules`. No JSON encoding — the text is stored verbatim. This is consistent with the cluster-level approach for plain-text content.

Raft command: `CmdCustomRulesSet` (`custom_rules.set`), payload `{ "rules": "..." }`.

---

## Rule Syntax

AdGuard Home compatible. Each non-empty, non-comment line is one rule:

| Syntax       | Effect                              |
|--------------|-------------------------------------|
| `/regex/`    | Block domains matching the regex    |
| `@@/regex/`  | Allow domains matching the regex    |
| `domain`     | Exact-domain block (+ sub-domains)  |
| `@@domain`   | Exact-domain allow (+ sub-domains)  |
| `# comment`  | Ignored                             |

Empty lines are ignored. Regex flags: case-insensitive (`(?i)` prepended automatically).

---

## Evaluation Order

Custom rules are evaluated **before** the global allowlist and blocklists:

1. Custom allow rules (first allow match → `Allow`, `BlocklistID: "custom_rule"`)
2. Custom block rules (first block match → `Block`, `BlocklistID: "custom_rule"`, global policy)
3. Global allowlist (existing)
4. Per-profile allowlists (existing)
5. Blocklists (existing)

Within custom rules: allow wins over block for the same domain (allow rules are checked first).

---

## Cluster Replication

Rules are replicated via the standard Raft pipeline (`CmdCustomRulesSet`). All nodes rebuild their filter engines on every committed apply via the `Subscribe` callback in `api.App.onApply`. There is no additional synchronisation step; convergence follows normal Raft latency.

---

## Shadow YAML

`CustomRules` is a field on `config.FilteringConfig`, which is already serialised as part of `clusterSections.Filtering` in `shadow_yaml.go`. No changes to the shadow writer are required.
