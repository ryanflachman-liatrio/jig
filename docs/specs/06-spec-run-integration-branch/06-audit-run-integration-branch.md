# 06-audit-run-integration-branch.md

Planning audit for **Foundation A — Run-integration branch**
(tasks: [`06-tasks-run-integration-branch.md`](./06-tasks-run-integration-branch.md),
spec: [`06-spec-run-integration-branch.md`](./06-spec-run-integration-branch.md)).

## Executive Summary

- **Overall Status: PASS** (all REQUIRED gates pass; 2 FLAG risks raised in run 1, both remediated — see Re-Audit Delta)
- Required Gate Failures: 0
- Flagged Risks: 2 (remediated)

## Gateboard

| Gate | Status | Why | Evidence |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | Every FR maps to a task and ≥1 planned test artifact | FR→task table + per-task Proof Artifacts (1.6, 2.6, 3.7, 4.7) |
| Proof artifact verifiability | PASS | Artifacts are named tests / exact CLI / concrete files, not "works as expected" | e.g. `TestStepsComposeOnCode`, `go run ./cmd/jig validate examples/bugfix.toml`, `06-proofs/A1.0-run-branch-log.txt` |
| Repository standards consistency | PASS | 4 guideline sources read (>2 bar); no `AGENTS.md`; root `README.md` reviewed; the TESTING/ARCHITECTURE staleness conflict has documented precedence (CLAUDE.md + code) | Standards Evidence Table in tasks file |
| Open question resolution | PASS | Both spec Open Questions carried as explicit, non-blocking assumptions | Branch name `jig/<workflow>/run-<runID>` + keep-after-merge (tasks 1.2/1.5/4.x); squash settled |
| Regression-risk blind spots | **FLAG** | Changing the per-step worktree base is a broad behavioral change | See FLAG 1 |
| Non-goal leakage | **FLAG** | Final-merge gate parks the run at end — must not resurrect retired park-after-finish | See FLAG 2 |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Single-writer scheduler; "file is truth, bus is liveness"; persistence-off is a first-class no-op | Declares engine real/running (see resolution) |
| `docs/TESTING.md` | yes | Table-driven tests with inline fixtures; `-race` for engine/TUI; executors behind interfaces | **Stale**: calls engine/runner/datastore "not implemented" |
| `docs/ARCHITECTURE.md` | yes | Run-dir layout; DAG + bounded back-edges invariant; package table marks engine `[DONE]` | **Partially stale**: "engine will plug in (planned)" prose |
| `README.md` | yes | Go 1.25; build/validate commands; mutating tools → worktree | none |

**Precedence:** CLAUDE.md + the actual code are authoritative over the stale "engine not
implemented" prose in TESTING.md/ARCHITECTURE.md. Correcting that prose is owned by **Unit D**
(spec 09), not Foundation A. No unresolved standards conflict blocks this plan.

## Findings

### FLAG Findings

1. **Regression risk — worktree base change touches all mutating steps.**
   - Risk: Task 2.1 repoints a mutating step's worktree from repo-root HEAD to the run-branch HEAD.
     This is the load-bearing behavioral change and can regress existing worktree **reuse on
     retry/loop** (`engine.go:722-723`), recovery flows, and journal replay.
   - Suggested remediation: before handoff, add an explicit regression check that the existing engine
     suites (`engine_test.go`, `recovery_test.go`, `replay_test.go`, `worktree_test.go`) pass under
     `-race`, and add a loop/retry case asserting a re-run step re-integrates correctly onto the run
     branch (not just the happy A→B path in 2.6). Non-blocking; can be folded into Task 2.6.

2. **Non-goal adjacency — final-merge gate vs. retired park-after-finish.**
   - Risk: Task 4.3 keeps the run "parked-but-alive" to present the final-merge gate at terminal
     detection. The parent mega-spec explicitly **retired** the park-after-finish / `RunResumed`
     lifecycle (that belonged to superseded spec 04). An implementer could mistake 4.3 for that.
   - Suggested remediation: add a one-line comment/constraint in Task 4.3/4.4 that the final-merge
     gate is a **pre-`RunFinished` completion step** (approve/discard → then emit `RunFinished`), not a
     resumable post-finish parked state, and that no `RunResumed`/scheduler re-entry is introduced.
     Non-blocking; documentation-only guard.

## User-Approved Remediation Plan

- **Approved & Completed (2026-08-07).** Both FLAGs folded into the task file:
  - FLAG 1 → new sub-task **2.7** (regression guard: existing engine suites pass under `-race` + a
    loop/retry re-integration case).
  - FLAG 2 → constraint added to sub-tasks **4.3/4.4** (final-merge gate is pre-`RunFinished`; no
    `RunResumed`, no post-finish re-entry).

## Re-Audit Delta (Run 2)

- Changed gate statuses: **Regression-risk blind spots FLAG → resolved** (covered by task 2.7);
  **Non-goal leakage FLAG → resolved** (covered by tasks 4.3/4.4 constraint).
- Still-failing REQUIRED gates: none.
- Newly introduced findings: none.
- **Overall Status: PASS** — 0 REQUIRED failures, 0 open FLAGs.

## Chain-of-Verification

- All REQUIRED gates pass with explicit evidence cited above.
- Findings fact-checked against the spec (FRs, Non-Goals, Open Questions), the tasks file, and the
  standards sources; the two FLAGs are supported by the referenced code lines and are now remediated.
- Final status: **PASS** — ready for the implementation phase.
