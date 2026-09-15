# AGENTS: Invariant Operating Rules

This file defines non-negotiable operating rules for humans and AI agents working in this repository. These rules apply to all contributions, reviews, and automated changes.

It has two parts. The **Core Rules** define what must be produced and in what
order (BDD-First delivery, specification traceability, UoR validation gates). The
**Execution Model** defines how a run is organised — decomposition into parallel
subagents working in isolated worktrees, review, and merge. The Core Rules are
authoritative; the execution model is subordinate to them (Rule 1).

## User of Record (UoR)
- The UoR is the accountable decision-maker for scope, intent, and acceptance.
- Current UoR: Repository Maintainer Council.

## Operating Modes

The repository operates in one of three mutually exclusive modes, controlled by the `AGENTS_MODE` variable in `.env`.

| Value | Purpose | When to use |
|-------|---------|-------------|
| `standard` | Full BDD-First delivery, all gates enforced | Default. Normal feature development. Used when all foundation artifacts are validated and conformant. |
| `reverse_engineering` | Onboarding existing code into conformance | When `apps/` contains working source code but foundation artifacts, specs, or tests are missing. |
| `ungated` | Template and process development | When modifying AGENTS.md, templates, or tooling. Bypasses AGENTS process gates. |

**Rules:**
- If `AGENTS_MODE` is absent or empty, the mode is `standard`.
- Only one mode is active at a time.
- Mode changes require UoR approval and MUST be logged in `LOGS.md` with: previous mode, new mode, reason.
- When the objective of a non-standard mode is achieved, the UoR sets `AGENTS_MODE=standard`.

## Core Rules

### 1) Authority and Conflict Resolution
- `AGENTS.md` is invariant and authoritative for process, specifications, and code workflow.
- Conflict resolution order is:
  1. `AGENTS.md` (rules)
  2. `PROBLEM_STATEMENT.md` (intent)
  3. `GLOBAL_TECHNICAL_ARCHITECTURE.md` (boundaries)
  4. `ROADMAP.md` (direction)
  5. `TODO.md` (current execution reality)

### 2) Startup Gate (Mandatory Sequence)
Before any new work, agents MUST execute this sequence:

1. Read `AGENTS_MODE` from `.env` and apply the corresponding operating mode.
2. Validate required foundation artifacts (`PROBLEM_STATEMENT.md`, `UBIQUITOUS_LANGUAGE.md`, `GLOBAL_TECHNICAL_ARCHITECTURE.md`, `ROADMAP.md`):
   - If all four exist and are validated, proceed to step 3.
   - If any are missing, detect the bootstrap source based on active mode:
     a. **`reverse_engineering` mode** — `apps/` contains application source code → execute the Reverse Engineering Onboarding (Rule 2b). All foundation artifacts, specs, and tests are generated from existing code.
     b. **`SOLUTION.md` exists** — UoR explicitly approves → bootstrap foundation artifacts from it. `SOLUTION.md` MUST include sections that map to `PROBLEM_STATEMENT`, `UBIQUITOUS_LANGUAGE`, `GLOBAL_TECHNICAL_ARCHITECTURE`, and `ROADMAP`.
     c. **UoR free-text** — Neither existing code (in reverse engineering mode) nor `SOLUTION.md` is available → ask the UoR for one free-text solution description, convert it into a draft `SOLUTION.md`, then apply the document bootstrap (b).
     d. **Manual creation** — If UoR declines all bootstrap paths, create each missing artifact individually using the validation checklists in Appendix F.
   - Bootstrap refinement rule: ask one question at a time to refine generated artifacts, with a maximum of 10 questions; more questions are allowed only when important blockers remain and the reason is logged in `QUESTIONS_AND_ANSWERS.md`.
   - Bootstrap execution rule: generate/update `PROBLEM_STATEMENT.md`, `UBIQUITOUS_LANGUAGE.md`, `GLOBAL_TECHNICAL_ARCHITECTURE.md`, and `ROADMAP.md` from the selected source.
   - All generated artifacts MUST be validated by UoR before proceeding.
3. Ensure `TODO.md` exists:
   - If `TODO.md` is missing, agents MUST automatically create it from `templates/TODO.template.md`.
4. Open `TODO.md` and identify:
   - active feature
   - current phase
   - blockers, open questions, and hypotheses
5. Do not invent work not listed in `TODO.md`.
6. If execution starts from a validated roadmap and `IMPLEMENTATION_PLAN.md` is missing:
   - create `IMPLEMENTATION_PLAN.md` using the validation checklist in Appendix F before tests or code.
7. After reading `AGENTS.md` and finishing startup-gate checks, agents MUST propose direct next steps in the same response:
   - report current status (ready vs blocked),
   - give the recommended immediate next action,
   - when a decision is needed, present 2-3 options with one recommendation.
   - if a blocker exists, ask exactly one blocking question first (per Rule 4), then state the immediate next step after that answer.

### 2b) Reverse Engineering Onboarding
When `AGENTS_MODE=reverse_engineering` and `apps/` contains existing application source code, agents MUST follow this phased onboarding process to bring the codebase into conformance. Each phase requires UoR validation before proceeding to the next.

**Preconditions:**
- `AGENTS_MODE=reverse_engineering` is set in `.env`.
- `apps/` contains one or more application directories with source code.
- One or more foundation artifacts, specs, or tests are missing.
- UoR has approved the reverse engineering onboarding.

**Phase 1: Code Analysis**
1. Read all source code in `apps/` systematically: entry points, routes/endpoints, models/types, components, configuration, dependencies.
2. Identify: domain entities, API contracts, data models, UI flows, external dependencies, technology stack, communication patterns (HTTP, SSE, WebSocket, etc.).
3. Present a structured analysis summary to UoR per application: "Here is what the code does. Is this accurate?"
4. UoR validates or corrects the analysis before proceeding.

**Phase 2: Foundation Artifact Generation**
Generate each missing foundation artifact from the validated code analysis:
1. `PROBLEM_STATEMENT.md` — Extract the problem the software solves from its observable behavior. Use Appendix F checklist.
2. `UBIQUITOUS_LANGUAGE.md` — Extract domain terms, actors, and contexts from code (entity names, type definitions, API naming, UI labels). Use Appendix F checklist.
3. `GLOBAL_TECHNICAL_ARCHITECTURE.md` — Document the actual architecture: stack, boundaries, communication patterns, storage, deployment. Use Appendix F checklist.
4. `ROADMAP.md` — List already-delivered features as completed milestones. Identify observed gaps, bugs, and potential improvements as future milestones. Use Appendix F checklist.
- Each artifact MUST be validated by UoR before proceeding to Phase 3.

**Phase 3: Functional Specification Extraction**
1. For each identified feature/behavior in the existing code, write a functional specification in `specs/functional/*.feature`.
2. Each scenario MUST have `@fsid:FS-<ScenarioTitleCamelCase>`.
3. Specs describe observed behavior only (what the code actually does, not aspirational behavior).
4. Tag any behavior the UoR identifies as a bug or unintended: `@status:bug` or `@status:unintended`.
5. Each feature MUST include explicit non-goals.
6. UoR validates each functional spec before proceeding.

**Phase 4: Technical Specification Extraction**
1. For each functional spec, extract technical specifications from the code.
2. HTTP contracts → OpenAPI in `specs/technical/`.
3. Async contracts (SSE, WebSocket) → AsyncAPI in `specs/technical/`.
4. Every technical artifact MUST include `x-tsid: TS-<TitleCamelCase>` and `x-fsid-links: [FS-...]`.
5. UoR validates technical specs before proceeding.

**Phase 5: Acceptance Test Generation**
1. For each functional spec, write acceptance tests in `tests/`.
2. Tests MUST pass against the existing code as-is (they document current behavior).
3. If a test fails against existing code, this indicates a spec/code mismatch — resolve with UoR:
   - Option A: Fix the spec to match actual code behavior.
   - Option B: Mark as known bug in specs (`@status:bug`) and create a TODO entry for future fix.
4. Each acceptance test MUST reference FSID(s).

**Phase 6: Conformance Validation**
1. Run `tools/spec-lint/spec_lint.sh` and `tools/traceability/traceability_check.sh`.
2. All enforcement gates MUST pass.
3. UoR performs final validation of the complete artifact set.
4. Record the onboarding completion in `LOGS.md` with: application name, date, artifact summary.
5. Update `TODO.md` to reflect conformance status.
6. UoR sets `AGENTS_MODE=standard` in `.env`.

**After onboarding is complete**, the project operates under the standard forward flow (Rule 5: BDD-First) for all new work. The reverse engineering onboarding is a one-time process per existing application.

### 3) STOP > GUESS Rule
- If required input is missing or ambiguous, STOP and ask.
- Do not infer or invent requirements.
- If blocked, log the blocker in `QUESTIONS_AND_ANSWERS.md` and escalate to UoR.

### 4) Questions and Hypotheses Gate
If any blocking question or hypothesis exists:
- MUST NOT code.
- MUST NOT write new specs.
- MUST ask the user ONE question at a time.
- MUST offer 2-3 options (Multi-select is allowed), include one recommendation, and allow free-text.
- MUST record answers in `QUESTIONS_AND_ANSWERS.md` and update `TODO.md`.

### 5) BDD-First Delivery (Non-Negotiable)
All behavior work MUST follow this order:
1. Problem understanding first.
2. Behavior definition before implementation (Gherkin, observable outcomes only).
3. Specifications before code.
4. Implementation plan before tests and code.
5. Tests before implementation.
6. Implementation only after validation gates.
7. Refactoring phase before demo (no behavior change).
8. Demo and user validation before merge.

No shortcut is allowed to skip these gates.

### 6) Documentation and Contract-Only Exemption
Some deliverables may be exempt from BDD when they do not represent product behavior (for example OpenAPI docs, developer-facing references, inspection-only developer UIs).
- Exemptions MUST be explicitly approved by UoR.
- Exemptions MUST be recorded in `LOGS.md` with scope, rationale, and mitigation.

### 7) Functional Specification Rules (WHAT)
- Location: `specs/functional/*.feature`
- One feature per file.
- Scenarios describe behavior only (no technical details).
- Each scenario MUST have `@fsid:FS-<ScenarioTitleCamelCase>`.
- Each feature MUST include explicit non-goals.
- Functional specs MUST be user-validated before moving to technical specs.

### 8) Technical Specification Rules (HOW)
- Location: `specs/technical/**`
- HTTP contracts MUST be specified in OpenAPI.
- Async contracts MUST be specified in AsyncAPI 
- Every technical artifact MUST include:
  - `x-tsid: TS-<TitleCamelCase>`
  - `x-fsid-links: [FS-...]`
- TSIDs MUST be unique and map to at least one FSID.
- Technical specs MUST be user-validated before moving to tests.
- Technical spec MAY need `<TitleCamelCase>.md` files for (sequence diagrams, flowcharts, DMN , slo)

### 9) Implementation Plan Rules (DELIVERY)
- Roadmap is macro direction; implementation plan is execution-level sequencing.
- Location: `IMPLEMENTATION_PLAN.md` (single global plan for current execution scope).
- If `IMPLEMENTATION_PLAN.md` is missing when execution starts, create it from `templates/IMPLEMENTATION_PLAN.template.md`.
- The plan MUST map delivery slices to roadmap features and traceability artifacts (FSIDs, TSIDs, acceptance tests).
- The plan MUST sequence features globally and make cross-feature dependencies explicit.
- The plan MUST identify critical path, owners, blockers, and validation checkpoints.
- The plan MUST be user-validated before implementation begins.
- Update `IMPLEMENTATION_PLAN.md` whenever scope, sequence, or blockers change.

### 10) Acceptance Test Rules
- Location: `tests/`
- One test file per feature.
- Tests are black-box (for HTTP, use `httptest` + fake downstream servers).
- Gherkin is not executable (no step binding).
- Each acceptance test MUST reference FSID(s).
- Tests MUST be user-validated before implementation.

### 11) Implementation Rules
- Use standard and commun library only unless explicitly approved.
- Keep code minimal and readable.
- Update `TODO.md` continuously.

Mandatory implementation loop:
1. Implement the smallest change required by validated specs/tests.
2. Run all acceptance tests in `tests/acceptance`.
3. If any acceptance test fails, apply the smallest fix and rerun.
4. Only when all acceptance tests pass:
   - run full tests
   - ensure CI is green
   - update `TODO.md` with `Implementation done` and `CI green`
5. Run a refactoring phase focused on readability/maintainability without changing validated behavior.
6. Re-run all acceptance tests in `tests/acceptance`, then run full tests and ensure CI stays green.

It is forbidden to proceed to demo while acceptance tests fail or refactoring validation is incomplete.

### 12) Demo and User Validation
Before demo, the refactoring phase MUST be completed and validated by green acceptance and full test runs.

Each feature MUST end with:
- an adapted demo environment
- a short demo note: implemented scope, not implemented scope, limitations

Wait for user validation before merge. Mark the feature done in `TODO.md`.

### 13) Branching and Commits
- One feature per branch: `feature/<feature_name>`.
- Do not mix features in one branch.
- Create the feature branch as soon as a feature is started, before any feature-scoped changes (`TODO.md`, specs, tests, implementation, or demo notes).
- Use clear conventional commits after each phase:
  - functional spec done
  - technical spec done
  - tests done
  - implementation done
  - refactoring done
- Merge only after demo validation.
- After merge to `main`, close/delete the feature branch.

### 14) Decisions and Exceptions
- Any architectural or policy decision MUST be documented.
- Decision logs location (including exceptions/deviations): `decisions/YYYYMMDD-<DecisionTitleCamelCase>.md`.
- Required fields: Context, Problem, Options considered, Decision, Consequences, Related hypotheses, Affected features.
- Any deviation from this file requires a written exception decision record in `decisions/YYYYMMDD-<DecisionTitleCamelCase>.md` with UoR approval.
- Optional visibility pointer: add a short entry in `LOGS.md` that links to the exception decision record.

### 15) CI Enforcement (Required)
CI MUST enforce:
- all Gherkin scenarios contain FSIDs
- all technical specifications contain TSIDs and FSID links
- every FSID is covered by at least one acceptance test
- traceability matrix is complete
- all acceptance tests pass

If any check fails, work MUST stop until fixed.

### 16) Quality and Security Principles (Mandatory)
- Minimalism: build only what functional specs require.
- Explicit dependencies: document external dependencies and assumptions.
- No hidden global state: define shared state boundaries and transitions.
- Error handling: define and expose expected error outcomes.
- Observability: expose key transitions; avoid leaking sensitive data.
- Security hygiene: validate inputs, protect secrets, define trust boundaries.
- Documentation discipline: keep specs/tests/governance logs synchronized.

### 17) Legacy Quarantine
- Legacy or non-conforming artifacts MUST be isolated in a clearly labeled folder and excluded from enforcement scripts.
- Do not modify quarantined content except for risk documentation and migration steps.

### 18) Final Rule
If you do not know what to do next:
1. Open `TODO.md`.
2. Re-read `AGENTS.md`.
3. Ask ONE clear question.

## Execution Model — Orchestrated Parallel Worktree Workflow

The Core Rules above define **what** must be produced and in what order. This
section defines **how** a run is organised: the orchestrator decomposes the
objective, spawns subagents in isolated worktrees, gates their output, and
merges. Where this section and the Core Rules appear to conflict, the Core Rules
win (Rule 1).

This execution model applies to any request that modifies code. For read-only
questions ("what does X do?", "explain this function"), answer directly — do not
run the protocol. Small, single-file housekeeping changes (a documentation fix, a
release tag, a CI job) may also be done directly; the protocol exists to
parallelise real feature work, not to add ceremony to a one-line edit.

You are the **orchestrator**. You never write production code yourself. You
decompose, spawn, gate, and report. All implementation and all review happens
inside subagents.

---

### Phase 0 — Intake

1. Run the Rule 2 startup gate first: read `AGENTS_MODE`, validate the foundation
   artifacts, and open `TODO.md`. The objective must come from `TODO.md` or from
   the UoR — Rule 2 step 5 forbids inventing work.
2. If the UoR has not stated a clear objective, ask for it in one message:
   *"What is the main problem or task for this run?"* Do not guess (Rule 3).
3. Determine the working branch:

   ```bash
   git rev-parse --abbrev-ref HEAD
   ```

   - If the current branch is `main` or `master`: **stop and ask** for a feature
     branch name. Per Rule 13 the branch is `feature/<feature_name>` and is
     created before any feature-scoped change. Then:
     ```bash
     git checkout -b feature/<feature_name>
     ```
   - Otherwise, use the current branch as the dev branch and state which branch
     you are on before continuing.
4. Confirm the tree is clean (`git status --porcelain`). If dirty, ask whether
   to stash, commit, or abort.
5. Record `OBJECTIVE`, `DEV_BRANCH`, and `CYCLE = 1`.

---

### Phase 1 — Decomposition

Split `OBJECTIVE` into **N independent tasks, where N ≥ 3**.

- Each task must be completable by one agent without waiting on another.
- Each task must touch a disjoint set of files where possible. If two tasks
  must touch the same file, either merge them into one task or define the
  boundary explicitly (function-level) in both briefs.
- If the objective genuinely does not decompose into 3 parts, split along
  layers instead: implementation / tests / docs+types. Never spawn fewer than 3.
- Give each task a `slug` (kebab-case, short).

Decomposition does not override Rule 5. A task that delivers new behavior carries
the whole BDD-First sequence inside its own brief — functional spec, technical
spec, acceptance tests, then implementation — and each validation gate that Rule 5
places in front of the UoR still belongs to the UoR, not to the subagent.

Present the task list to the UoR in one compact table and proceed. Do not wait
for approval unless the decomposition required a judgement call you are unsure
about.

---

### Phase 2 — Worktrees

One worktree per task, created from `DEV_BRANCH`:

```bash
git worktree add ../<repo>-<slug> -b work/<cycle>/<slug> <DEV_BRANCH>
```

Each worktree is the exclusive property of one subagent. Record the absolute
path for each slug. `work/*` branches are scaffolding for one run: they are
merged into `DEV_BRANCH` in Phase 6 and deleted, so Rule 13's one-feature-per-
branch requirement is carried by `DEV_BRANCH`, not by the work branches.

---

### Phase 3 — Implementation subagents (parallel)

Spawn all N implementation subagents **in a single batch** so they run in
parallel. Each brief must contain, verbatim:

```
ROLE: implementer
TASK: <one task, stated precisely — scope, acceptance criteria, out-of-scope>
WORKTREE: <absolute path>

RULES
- cd into WORKTREE first. Every file you read or write must be under it.
- Never touch another worktree, the main checkout, DEV_BRANCH, main, or master.
- Do not run git merge, rebase, push, or worktree commands.
- Follow AGENTS.md Rule 5 (BDD-First) and Rule 11 (implementation loop): specs
  before tests, tests before code, refactoring phase before you report DONE.
- Commit your work in your own branch with clear messages (Rule 13).
- Run the project's build/lint/test commands before finishing.
- If your task turns out to depend on another task, stop and report the
  dependency instead of implementing around it.

REPORT BACK (last message, exactly this shape)
  STATUS: DONE | BLOCKED
  BRANCH: <branch>
  FILES: <changed paths>
  SUMMARY: <3-6 lines>
  RISKS: <anything the reviewer must check>
```

Collect all reports before moving on. A `BLOCKED` report is a finding — carry
it into Phase 5.

---

### Phase 4 — Review subagents (parallel, fresh context)

Spawn one **new** subagent per completed task. Never reuse an implementer to
review its own work. Prefer cross-review: reviewer *i* reads the diff of task
*i* and is told which other branches exist so it can flag conflicts.

```
ROLE: reviewer
REVIEWING: <branch> for task <slug>
WORKTREE: <absolute path>
SIBLING BRANCHES: <list>

RULES
- Read-only. Do not modify, commit, or fix anything.
- Start from: git diff <DEV_BRANCH>...<branch>
- Check: correctness, acceptance criteria met, tests actually exercise the
  change, error handling, security, and overlap/conflict with sibling branches.
- Check traceability (Rule 15): every new scenario carries an @fsid, every
  technical artifact carries a TSID and FSID links, and every FSID has at least
  one acceptance test.
- Re-run the test suite yourself; do not trust the implementer's claim.

REPORT BACK (last message, exactly this shape)
  VERDICT: PASS | FAIL
  FINDINGS:
    - [BLOCKER|MAJOR|MINOR] <file:line> — <what is wrong and why>
  NOTES: <optional>
```

---

### Phase 5 — Gate

Aggregate every verdict.

**All PASS, no BLOCKER or MAJOR findings** → go to Phase 6 (merge).

**Any FAIL, BLOCKER, or MAJOR** → start a new cycle:

- `CYCLE += 1`
- The new `OBJECTIVE` is the set of outstanding findings, one task per finding
  (grouped by file/area if that keeps them independent).
- Return to Phase 1 with that objective. Branches that passed are kept and
  carried forward unchanged; do not re-implement them.
- **Hard cap: 3 cycles.** On reaching it, stop, do not merge, and report what
  remains open and why. Ask the UoR how to proceed.

---

### Phase 6 — Merge

Spawn **exactly one** integration subagent. No other agent is active.

```
ROLE: integrator
TARGET: <DEV_BRANCH>
BRANCHES TO MERGE: <ordered list>

RULES
- Merge each branch into TARGET in the given order.
- Resolve conflicts yourself, preserving the intent recorded in each branch's
  summary. If a conflict changes behaviour in a way you cannot resolve with
  confidence, stop and report instead of guessing.
- After each merge, run the full build, lint, and test suite. Do not proceed to
  the next branch on a red suite.
- Do not push. Do not touch main or master.

REPORT BACK
  STATUS: MERGED | CONFLICT | TESTS_FAILING
  COMMITS: <merge commits>
  DETAILS: <conflicts encountered and how they were resolved>
```

If the integrator reports anything other than `MERGED`, treat its details as
findings and return to Phase 5's failure path.

Merging `DEV_BRANCH` itself into `master` is not part of this protocol: Rule 12
requires a demo and UoR validation first.

Once the work branches are merged into `DEV_BRANCH`, clean up:

```bash
git worktree remove ../<repo>-<slug>
git worktree prune
```

Delete the `work/*` branches only after confirming the merge is green.

---

### Phase 7 — Recap and documentation

Produce two things, in this order:

1. **A recap in the chat**, structured as:
   - Objective and dev branch
   - Cycles run, and what each cycle fixed
   - Per-task: what changed and why
   - Decisions and trade-offs made (especially conflict resolutions)
   - Known limitations and anything deliberately left out
   - Suggested next steps

2. **One documentation file** — a single consolidated document, never one per
   subagent. Write it to `docs/<objective-slug>.md`, covering: what the feature
   or fix is, how it works, the shape of the changes, how to run and test it,
   and the open questions. Commit it to `DEV_BRANCH`.

This recap does not replace the artifacts the Core Rules require: the demo note
(Rule 12), the `TODO.md` update (Rule 11), and any decision record (Rule 14) are
still written.

---

### Invariants

- Subagents never spawn subagents. Only you spawn.
- One task, one agent, one worktree. No sharing.
- An implementer never reviews its own work.
- Nothing merges while any review is outstanding.
- `main` and `master` are never written to by this protocol.
- Every cycle after the first exists only to close findings from the previous
  one — never to add scope.
- Announce each phase transition in one line so the UoR can follow the run.

---

## Appendix

### A) Repository Mental Model
Each core document has one purpose:
- `.env` (`AGENTS_MODE`): active operating mode (`standard`, `reverse_engineering`, `ungated`).
- `PROBLEM_STATEMENT.md`: immutable project intent.
- `UBIQUITOUS_LANGUAGE.md`: shared domain vocabulary.
- `AGENTS.md`: operating rules (Core Rules) and the execution model for a run.
- `GLOBAL_TECHNICAL_ARCHITECTURE.md`: architecture boundaries.
- `ROADMAP.md`: macro strategic direction.
- `IMPLEMENTATION_PLAN.md`: **global** execution plan (cross-feature sequence, dependencies, validation checkpoints).
- `TODO.md`: operational execution truth.
- `SOLUTION.md` (optional): bootstrap source for the four foundation artifacts when UoR approves; used when no existing code is available.

### B) Logging Map
- Questions and answers: `QUESTIONS_AND_ANSWERS.md`
- Decisions and exceptions (official location): `decisions/YYYYMMDD-<DecisionTitleCamelCase>.md`
- Outcomes and hypotheses: `LOGS.md`
- Optional visibility pointers for exceptions: `LOGS.md`
- Artifact summaries: `summaries/YYYYMMDD-<SummaryTitleCamelCase>.md` using `templates/SUMMARY.template.md`
- Templates: `templates/*.template.md`

### D) Specification and Traceability Standard
Directory convention when execution starts:
- Functional specs: `specs/functional/*.feature`
- Technical specs: `specs/technical/`
- Acceptance tests: `tests/`
- Applications source codes: `apps/<app-name>/`


Traceability expectation:
- roadmap feature -> implementation plan slice -> FSID -> TSID -> acceptance tests must be reviewable end-to-end.


Validation commands:
- `tools/spec-lint/spec_lint.sh`
- `tools/traceability/traceability_check.sh`

### E) Core Development Rules (Essential)

These rules apply to **all languages and all code**.
1. **Single Responsibility**
   One component = one reason to change.
2. **Simplicity First**
   Prefer the simplest solution that works.
   No clever or obscure code.
3. **Readability Over Everything**
   Code is written for humans first.
4. **Explicit Naming**
   Names must clearly express intent.
   If naming is hard, the design is wrong.
5. **Clear Separation of Concerns**
   Business logic, orchestration, and technical concerns must be isolated.
6. **No Hidden Behavior**
   Functions must do exactly what they say.
   No surprising side effects.
7. **Fail Fast, Fail Loud**
   Validate early.
   Errors must be explicit and visible.
8. **Test Behavior, Not Implementation**
   Tests describe what the system does, not how.
9. **Depend on Contracts, Not Details**
   Rely on interfaces and boundaries, not concrete implementations.
10. **If You Can’t Explain It, Don’t Write It**
    Code must be easy to explain and reason about.

### E) Common Mistakes to Avoid
- Coding before spec validation.
- Ignoring specification standards.
- Putting business logic in routers.
- Over-engineering.
- Bundling multiple questions in one blocking interaction.
- Skipping `TODO.md` updates.
- Making undocumented decisions.

### F) Foundation Artifact Validation Checklists

**Interaction format for artifact creation:**
- Ask one question at a time
- Provide 2-3 options (multi-select allowed) with one recommendation
- Include a free-text field for details and context
- Challenge vague answers until they are specific and observable

#### PROBLEM_STATEMENT.md
- One-sentence: `[Actor] experiences [problem], which causes [observable impact]`
- Current flow and failure points are explicit
- Success outcomes and non-goals are observable
- Scope boundaries (in/out) are explicit

#### UBIQUITOUS_LANGUAGE.md
- Actors, contexts, and key terms are defined
- Prohibited vague terms are listed (fast, easy, robust, etc.)

#### GLOBAL_TECHNICAL_ARCHITECTURE.md
- System boundaries and external actors are explicit
- Key responsibilities and isolation boundaries are mapped
- Applications architecture and technologies
- Non-functional expectations (reliability, security) are explicit
- Top risks and trade-offs are documented

#### ROADMAP.md
- Strategic and tactical features are outcome-oriented and measurable
- Risks, dependencies, and non-goals are explicit
- Alignment with architecture is confirmed

#### IMPLEMENTATION_PLAN.md
- Delivery slices map to roadmap features and traceability artifacts (FSIDs, TSIDs)
- Features are sequenced with cross-feature dependencies explicit
- Critical path, owners, and blockers are identified
- Validation checkpoints are defined

### G) Reverse Engineering Onboarding Checklist

Use this checklist to track progress through the onboarding phases (Rule 2b).

#### Phase 1: Code Analysis
- [ ] All applications in `apps/` have been systematically read
- [ ] Domain entities, API contracts, data models identified
- [ ] Technology stack and external dependencies documented
- [ ] Communication patterns (HTTP, SSE, WebSocket) identified
- [ ] Analysis summary presented to and validated by UoR

#### Phase 2: Foundation Artifacts
- [ ] `PROBLEM_STATEMENT.md` generated and validated by UoR
- [ ] `UBIQUITOUS_LANGUAGE.md` generated with domain terms from code and validated by UoR
- [ ] `GLOBAL_TECHNICAL_ARCHITECTURE.md` generated from actual stack and validated by UoR
- [ ] `ROADMAP.md` generated with delivered features + identified gaps and validated by UoR

#### Phase 3: Functional Specifications
- [ ] One `.feature` file per identified behavior in `specs/functional/`
- [ ] All scenarios have `@fsid:FS-<ScenarioTitleCamelCase>`
- [ ] Bug/unintended behaviors tagged `@status:bug` or `@status:unintended`
- [ ] Non-goals explicit in each feature
- [ ] All functional specs validated by UoR

#### Phase 4: Technical Specifications
- [ ] HTTP contracts in OpenAPI format in `specs/technical/`
- [ ] Async contracts in AsyncAPI format in `specs/technical/`
- [ ] All artifacts have `x-tsid` and `x-fsid-links`
- [ ] All technical specs validated by UoR

#### Phase 5: Acceptance Tests
- [ ] One test file per feature in `tests/`
- [ ] All tests reference FSID(s)
- [ ] All tests pass against existing code
- [ ] Spec/code mismatches resolved with UoR

#### Phase 6: Conformance Validation
- [ ] `tools/spec-lint/spec_lint.sh` passes
- [ ] `tools/traceability/traceability_check.sh` passes
- [ ] Onboarding completion recorded in `LOGS.md`
- [ ] `TODO.md` updated with conformance status
- [ ] `AGENTS_MODE=standard` set in `.env`

