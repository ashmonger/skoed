# M43 — DNSSEC Detail on Query Stream

## Implemented

- **`dnssec_status` field on SSE events** — every `event: query` frame now includes `"dnssec_status"` when DNSSEC validation is active. Values follow RFC 4033 terminology: `"secure"`, `"insecure"`, `"bogus"`, `"indeterminate"`. The field is omitted (`omitempty`) when DNSSEC validation is disabled (transparent mode).
- **`dnssec_error` field on SSE events** — present only when `dnssec_status == "bogus"`; contains a short string describing the validation failure reason.
- **`?dnssec_status=` filter on the SSE stream** — operators can connect with `?dnssec_status=bogus` (or `secure`, `insecure`, `indeterminate`) to receive only events matching that status. Useful for real-time monitoring of DNSSEC failures.
- **Paginated query log (`GET /api/v1/query-log`)** — the existing paginated log endpoint also surfaces `dnssec_status` and `dnssec_error` on each entry (fields were already stored on `Entry`; the new `dnssec_error` field is new for M43).
- **Web UI DNSSEC column** — the Query Log table shows a DNSSEC column when any loaded entry carries a `dnssec_status`. Each row shows the status text (`secure` / `insecure` / `bogus` / `indeterminate`) with colour-coded badges (green/amber/red). Hovering over a bogus row shows the `dnssec_error` tooltip.
- **`node_id` on stream events** — every SSE event now carries `"node_id"` identifying the originating node (populated on single-node streams from the node's own ID; essential for M41 cluster fan-in).

## Not Implemented

- **DNSSEC chain display** — individual RRSIG, DS records in the query log. This belongs in a dedicated DNSSEC debug view.
- **Per-profile DNSSEC failure counters** — no per-profile breakdown of secure/insecure/bogus counts.
- **DNSSEC error persistence** — `dnssec_error` is not stored in the persistent query log (bbolt); it is only available on live SSE events.

## Limitations

- `dnssec_status` is populated only for forwarded queries resolved via the `logForwarded` path. Blocked, cached, and locally-resolved queries will always have an empty `dnssec_status` (and no `dnssec_error`).
- On the reference Proxmox cluster (recursive mode), DNSSEC validation produces status results. In forwarding mode, the upstream resolver must return the `AD` flag for `"secure"` status to be reported.
- The DNSSEC column in the Web UI appears dynamically once any entry with a `dnssec_status` is loaded. It does not appear on pages where all entries have empty status.
