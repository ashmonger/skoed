# Logs

## Outcomes log

- **Date**: 2026-05-29
  **Artifact**: Foundation artifacts (SOLUTION.md, PROBLEM_STATEMENT.md, UBIQUITOUS_LANGUAGE.md, GLOBAL_TECHNICAL_ARCHITECTURE.md, ROADMAP.md, TODO.md, QUESTIONS_AND_ANSWERS.md)
  **Outcome**: Generated from UoR free-text description via bootstrap path (c). Awaiting UoR validation.

## Hypotheses log

- **Date**: 2026-05-29
  **Hypothesis**: H1 — `miekg/dns` is sufficient for skoed's DNS engine (forwarding + root resolution + custom records).
  **Validation plan**: Prototype DNS engine at M1 implementation start; evaluate alternatives (`coredns`) if blocked.
  **Status**: Open

- **Date**: 2026-05-29
  **Hypothesis**: H2 — Quorum-based primary step-down (last-seen timestamps + health checks) is sufficient split-brain prevention at home/lab scale (≤ 10 nodes).
  **Validation plan**: Validate during M2 design; if not sufficient, evaluate Raft.
  **Status**: Open

## Visibility pointers (optional)
- Canonical decisions and exceptions are recorded in `decisions/YYYYMMDD-<CamelCaseName>.md`.

## Exception pointers (optional)

- **2026-06-10 — M11 Documentation Exemption** (Rule 6)
  - Scope: `README.md` rewrite and all `docs/src/**/*.md` page content.
  - Rationale: these deliverables are documentation only — they describe observable product behavior but do not themselves implement it. Changes have no effect on test outcomes.
  - Mitigation: documentation accuracy is validated against the already-shipped implementation and FSID catalog; any claim in the docs maps to a spec or demo output.
  - UoR approval: inline (UoR requested M11 explicitly including "full wiki" and "nice README.md").
