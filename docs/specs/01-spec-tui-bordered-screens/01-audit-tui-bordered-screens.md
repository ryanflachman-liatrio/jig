# 01-audit-tui-bordered-screens.md

Planning audit for [`01-tasks-tui-bordered-screens.md`](./01-tasks-tui-bordered-screens.md)
against [`01-spec-tui-bordered-screens.md`](./01-spec-tui-bordered-screens.md).

## Executive Summary

- Overall Status: **PASS** (Run 2, after approved remediation)
- Required Gate Failures: **0**
- Flagged Risks: **0**

## Gateboard

| Gate | Status | Why it failed (<=10 words) | Exact fix target |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | Detail (1.9) and Runs (1.10) now have test tasks | — |
| Proof artifact verifiability | PASS | — | — |
| Repository standards consistency | PASS | README/CLAUDE.md conflict resolved with documented precedence | — |
| Open question resolution | PASS | Spec grilled & settled; no open questions | — |
| Regression-risk blind spots (FLAG) | PASS | Task 2.11 now asserts gate-cancellation response | — |
| Non-goal leakage (FLAG) | PASS | Chat in-scope (NG#6 superseded); no mouse/theme added | — |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Styles in `styles.go` from ~7 tokens (no bare vars/hex); frame helpers not magic numbers; v2 idioms (sub-models return `string`, key on `tea.KeyPressMsg`). | README staleness (below) |
| `docs/TESTING.md` | yes | Table-driven; drive `Update`/assert on `View()`; run TUI work under `-race`; `gofmt`+`vet` clean pre-change. | none |
| `README.md` | yes | Project intent; build/run commands. | **Stale**: "engine not built"/"Go 1.24" vs CLAUDE.md "engine real"/"Go 1.25". |
| `docs/adr/0001` | yes | Manual top-edge compositing; single place to replace if lipgloss adds title API. | none |
| `docs/adr/0002` | yes | Gates are non-blocking focus regions; per-focus key routing. | none |
| `AGENTS.md` | not found | — | — |
| `CONTRIBUTING.md` | not found | — | — |

Precedence decision: **`CLAUDE.md` is authoritative** for coding standards; the
root `README.md` is treated as stale narrative where it conflicts. Four guideline
sources read (>2 required); no `AGENTS.md` exists to review. Standards gate PASSES.

## Findings

### REQUIRED Failures

1. **Two Unit-1 functional requirements have no planned test artifact.**
   - Missing item: The FRs "render the **detail** screen inside a titled panel
     titled with the workflow name (falling back to the file path)" and "render
     the **runs** screen inside a titled panel titled 'Runs' … scrolling within
     the frame" map only to **screenshots** (`1.0-runs.*`) and the blanket
     `go test ./internal/tui` pass — no assertion proves the detail title/fallback
     or the runs panel + viewport scroll. The traceability gate requires a *test*
     artifact per FR, not a screenshot.
   - File section to edit: `## Tasks > #### 1.0 Tasks` (add sub-tasks) and
     `## Relevant Files` (add `detail_test.go`, `runs_test.go`).
   - Acceptance condition: add (a) a `detail_test.go` case asserting `View()`
     carries the workflow name on the top edge and falls back to the path when the
     name is empty, and (b) a `runs_test.go` case asserting the "Runs" title
     appears and that a scroll/cursor key keeps the selection within the framed
     viewport. Then every Unit-1 FR maps to a test.

### FLAG Findings

1. **Gate-cancellation regression is validation-blind.**
   - Risk: The spec explicitly requires "cancellation delivering the appropriate
     response so no reporter goroutine hangs" (a known prior bug class — see commit
     `3c4563a`). Task 2.11's `TestMonitorGateNonBlocking` asserts a gate *resolves*
     but not that `esc`/`q` cancellation delivers a response message
     (`agentQuestionResponseMsg{answer:"cancelled"}` etc.). A refactor of the gate
     strip could silently reintroduce a hang.
   - Suggested remediation: extend `TestMonitorGateNonBlocking` (or add a case) to
     assert that cancelling each gate type emits the cancellation response command,
     run under `-race`. Low cost; high regression value.

## User-Approved Remediation Plan

- **Approved & Completed** (user approval given; edits applied to the task file):
  1. Added sub-tasks **1.9** (`detail_test.go`: title + path fallback) and **1.10**
     (`runs_test.go`: "Runs" title + framed-viewport scroll) to `#### 1.0 Tasks`;
     added both test files to `## Relevant Files`; added `TestDetail`/`TestRuns` to
     the 1.0 Proof Artifacts.
  2. Expanded task **2.11** to assert gate **cancellation** emits the cancellation
     response command.

## Re-Audit Delta (Run 2)

- Changed gate statuses since Run 1:
  - Requirement-to-test traceability: **now PASS** (was failing in Run 1; 1.9/1.10
    added, so every Unit-1 FR now maps to a test).
  - Regression-risk blind spots: **now PASS** (was flagged in Run 1; 2.11 asserts
    gate cancellation).
- Still-failing REQUIRED gates: none.
- Newly introduced findings: none.

## Chain-of-Verification (Run 2)

- Do all REQUIRED gates pass with explicit evidence? **Yes** — traceability
  verified against the amended `#### 1.0 Tasks` / Relevant Files; proof artifacts
  carry exact `go test -run` targets; ≥2 standards sources read with documented
  README-vs-CLAUDE.md precedence; spec is grilled/settled with no open questions.
- Inconsistencies found: none. Final status: **PASS**.
