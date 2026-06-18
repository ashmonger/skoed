# DNSSEC Validation Mode — Technical Specification

x-tsid: TS-DnssecValidationMode
x-fsid-links: [FS-DnssecModeConfigurable, FS-DnssecValidateBogusServfail, FS-DnssecValidateOkPassthrough, FS-DnssecValidateInsecurePassthrough, FS-DnssecValidateQueryLogStatus]

---

## Configuration Field

**Field:** `dns.dnssec_mode`
**Type:** string enum
**Allowed values:** `"transparent"` | `"validate"`
**Default:** `"transparent"`
**Replication:** replicated via Raft on every PATCH /api/v1/settings write — all cluster nodes observe the same value

| Value | Behavior |
|-------|----------|
| `transparent` | Current behavior. DO bit is forwarded unchanged; AD bit is never inspected; `dnssec_status` is `"n/a"` in query log. |
| `validate` | skoed sets the DO bit on every outgoing upstream query; inspects the AD bit and RRSIG presence in the upstream response to classify the result. |

---

## Settings API

### PATCH /api/v1/settings

Accepts a partial settings object. The `dns.dnssec_mode` field is validated against the allowed enum before the command is committed to Raft.

**Request body (example):**
```json
{"dns": {"dnssec_mode": "validate"}}
```

**Response 200:** settings updated, Raft command committed.

**Response 400:** `dnssec_mode` value is not `"transparent"` or `"validate"`.

### GET /api/v1/settings

The response includes `dns.dnssec_mode` reflecting the current committed value.

---

## DNS Validate Mode — Resolution Behavior

When `dns.dnssec_mode` is `"validate"`, the following logic applies to every upstream DNS resolution (blocked queries are never forwarded and bypass this logic entirely):

1. **Set DO bit** on the outgoing query to the upstream resolver, regardless of whether the originating client set it.
2. **Receive the upstream response** and classify it using the table below.
3. **Act on the classification** as described.

### Classification Table

| Condition | Classification | Action |
|-----------|---------------|--------|
| Response AD=1 | `ok` | Pass answer section through to client unchanged. |
| Response AD=0, at least one RRSIG present in answer section | `bogus` | Return SERVFAIL to client; do not pass upstream answer. |
| Response AD=0, no RRSIG records in answer section | `insecure` | Pass answer section through to client unchanged. |
| Upstream returns SERVFAIL | `bogus` | Return SERVFAIL to client. |
| Upstream timeout or network error | `indeterminate` | Return SERVFAIL to client (existing error handling unchanged). |

**Rationale for bogus detection:** AD=0 with RRSIG present means the upstream attempted validation and failed. This is the only heuristic used in this milestone; full chain verification against the root trust anchor is deferred.

**Transparent mode:** classification is always `"n/a"` and no AD bit inspection is performed.

---

## Query Log Extension

The existing query log entry struct is extended with one optional field:

```go
type QueryLogEntry struct {
    // ... existing fields ...
    DnssecStatus string `json:"dnssec_status,omitempty"`
}
```

**Field values:**

| Value | When set |
|-------|----------|
| `"ok"` | validate mode; AD=1 from upstream |
| `"bogus"` | validate mode; AD=0 + RRSIG present, or upstream SERVFAIL |
| `"insecure"` | validate mode; AD=0 + no RRSIG |
| `"indeterminate"` | validate mode; upstream network error or timeout |
| `"n/a"` | transparent mode (field may be omitted) |

In transparent mode the field is omitted from serialized log entries (`omitempty`).

---

## IANA Root Trust Anchor (Future Use)

The KSK-2017 root trust anchor (key tag 20326, ECDSA P-256) is embedded as a hardcoded constant for use in a future full-chain validation milestone. It is loaded at startup but not consulted in this milestone; the AD-bit heuristic described above is the sole validation mechanism.

```
. 172800 IN DNSKEY 257 3 8 AwEAAaz/tAm8yTn4Mfeh5eyI96WSVexTBAvkMgJzkKTO
                           Iz9oQ4J5VkSINi/m3m4A0WAFAMwSqKWD7nMVmPJNHdgY
                           l5iXBj3RXBQAcRaQZLk1B9T/8FnBzN8VqLJEsO0DnF35
                           Hl3BNKAY5m0/lkXXLKBK+I5JdgGKI3q3a5hSoSx0l51S
                           Wz4fXqvnE60GbxDAbvh/s6B2VH0NEBzajjgn6JllGcW6
                           p3lFuT8oW0= ;{id = 20326 (ksk), size = 2048b}
```

This constant is defined in a dedicated file (e.g., `internal/dns/dnssec_trust_anchor.go`) and is not wired into the resolution path in this milestone.

---

## Non-Goals

- skoed does not sign authoritative local DNS entries.
- Per-profile or per-client DNSSEC policy is not implemented.
- Cache eviction or cache-hit behavior is unchanged by DNSSEC classification.
- RFC 5011 automatic trust-anchor rollover is not implemented.
- Full DNSSEC chain validation (verifying RRSIGs against the embedded root KSK) is deferred to a future milestone.
