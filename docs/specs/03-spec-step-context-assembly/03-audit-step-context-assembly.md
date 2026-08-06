# 03-audit-step-context-assembly.md

Planning audit for
[`03-tasks-step-context-assembly.md`](03-tasks-step-context-assembly.md) against
[`03-spec-step-context-assembly.md`](03-spec-step-context-assembly.md).

## Executive Summary

- Overall Status: **PASS** (Run 2, after approved remediation)
- Required Gate Failures: **0**
- Flagged Risks: **0 open** (both FLAGs accepted into the plan)

Run 1 failed one REQUIRED gate (requirement-to-test traceability). The user
approved remediation; both FLAGs and the required fix were folded into the tasks.
All REQUIRED gates now pass. Ready for the implementation phase.

## Gateboard

| Gate | Status | Note | Target |
| --- | --- | --- | --- |
| Requirement-to-test traceability | **PASS** | non-agent empty-context test added | `## Tasks > 2.6` |
| Proof artifact verifiability | PASS | — | — |
| Repository standards consistency | PASS | conflicts documented with precedence (below) | — |
| Open question resolution | PASS | contradiction check locked to **explicit-false** | `## Tasks > 4.4` |
| Regression-risk blind spots | PASS | multiple-loops test added (FLAG-1 accepted) | `## Tasks > 3.8` |
| Non-goal leakage | PASS | tasks stay in engine/runner/schema/docs; ADR is spec-directed | — |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Schema changes exhaustive & load-time (parse+default+validate+test both paths); `internal/step` imports nothing; comments explain the "why"; persistence-off is a first-class no-op; examples are executable docs | states engine is real (vs README) |
| `docs/TESTING.md` | yes | Table-driven inline-TOML tests; `Decode(data, baseDir)` seam; assert invalid by error substring; a schema change proves valid + every failure mode + defaulting/precedence; run engine work `-race` | none |
| `README.md` | yes | Build/run commands; three step types; kitchen-sink example | **"Go 1.24"** & **"engine not built"** — both stale |
| `mise.toml` | yes | Go toolchain pinned **1.25** | vs README "1.24" |
| `docs/adr/0001–0004` | yes | ADR format; ADR 0003 governs (extensibility in engine+schema, consumers thin); next free number **0006** | none |
| `docs/specs/02-…/02-tasks-…md` | yes | Repo tasks format: parents 1:1 with Demoable Units; proof bullets cite exact `go test -run` + committed `.txt` | none |
| `AGENTS.md`, `CONTRIBUTING.md`, `.github/` | not found | — | absent |

**Precedence decision (documented to satisfy the standards gate):** where
`README.md` conflicts with `CLAUDE.md`/`mise.toml` on toolchain version and engine
status, **`mise.toml` (toolchain pin) and `CLAUDE.md` ("Current state," explicitly
maintained) win.** The spec is grounded against the actual code, which was
independently verified (`buildRequest`@832, `fireLoop`@1398, `depsReady`@561,
`buildAgentPrompt`@433). README staleness is out of this spec's scope (see FLAG 2).

## Findings

### REQUIRED Failures (max 3)

1. **FR "command and review steps shall leave `WorkflowContext` empty" (Unit 2)
   has no mapped test.**
   - Missing item: a test asserting a `command` and a `review` step both dispatch
     with `req.WorkflowContext == ""`.
   - File section to edit: task **2.6** (extend `TestBuildRequestWorkflowContext`
     with command/review assertions, or add `TestBuildRequestNonAgentEmpty`) and
     the 2.0 Proof Artifact list.
   - Acceptance condition: a named engine test asserts both non-agent step kinds
     yield an empty preamble; the task/proof references it.

### FLAG Findings (max 2)

1. **Multiple loops targeting one step is validated only structurally.** Task 3.4
   relies on `rerunSource` last-write-wins for the "many loops → one step" case,
   but the tests in 3.6 exercise a single loop. Regression risk if a second loop
   is added later.
   - Risk: an incorrect re-run reason when two loops share a `goto` target.
   - Suggested remediation (optional): add a fixture with two loops targeting the
     same step and assert the last-fired source drives `RerunReason`.
2. **`README.md` is stale** ("Go 1.24", "engine not built"). Not caused by this
   work and out of scope, but the example/docs edits in Unit 6 touch neighboring
   docs.
   - Risk: reader confusion; not a blocker for this spec.
   - Suggested remediation (optional): a one-line README refresh, or a separate
     doc-hygiene task — your call; not added to the plan by default.

## Open Question (needs your decision; assumption recorded)

**Contradiction-check semantics — explicit vs. effective `inject_context = false`
(tasks 4.4 / 4.2).** The spec rejects "`[step.context]` together with
`inject_context = false` on the same step." Because `inject_context` inherits from
`[defaults]`, a step could declare `[step.context]` and inherit `false` without
setting it explicitly — also inert.
- **Recorded assumption (current plan):** reject only the **explicit** per-step
  `inject_context = false` + `[step.context]` combination, evaluated before
  defaulting collapses the value. An inherited-false step with a context block is
  **not** flagged (its block is inert but the author may be mid-edit).
- **Alternative:** reject the **effective** false (explicit or inherited) — stricter,
  catches the inert-block case at parse time, but requires validation to compute the
  effective value first.
- This satisfies the Open-Question gate (resolved with an explicit assumption); it
  is surfaced only so you can confirm or switch to the stricter rule before
  implementation.

## User-Approved Remediation Plan

- Status: **Completed** (approved 2026-08-06).
- **REQUIRED-1** — added `TestBuildRequestNonAgentEmpty` to task 2.6 + a proof
  bullet under 2.0 (command & review steps dispatch with empty `WorkflowContext`).
- **FLAG-1** — added task 3.8 `TestWorkflowContextMultipleLoops` (two loops → one
  step, last-fired source wins).
- **FLAG-2** — added task 6.8: refresh stale `README.md` (Go 1.25; engine runs
  today), folded into Unit 6 docs; `README.md` added to Relevant Files.
- **Open Question** — resolved: contradiction check checks the **explicit**
  per-step `inject_context = false` only (task 4.4 unchanged).

## Re-Audit Delta (Run 2)

- Changed gate statuses since Run 1:
  - Requirement-to-test traceability: now **PASS** (was the Run 1 blocker; task
    2.6 test added).
  - Regression-risk blind spots: now **PASS** (was FLAG; task 3.8 added).
- Still-failing REQUIRED gates: **none**.
- Newly introduced findings: **none**.

## Chain-of-Verification (pre-handoff)

- Do all REQUIRED gates pass with explicit evidence? **Yes** — each maps to a
  named test/proof in the tasks file.
- Fact-check: the Unit 2 non-agent FR now maps to `TestBuildRequestNonAgentEmpty`;
  standards conflicts carry a documented precedence; the one material design
  decision is resolved (explicit-false). Verified against spec + tasks + the code
  anchors (`buildRequest`@832, `fireLoop`@1398, `depsReady`@561, `buildAgentPrompt`@433).
- Inconsistencies: none outstanding.
- Final synthesis: **PASS — proceed to the implementation phase.**
