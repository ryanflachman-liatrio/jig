# 08-audit-reset-to-step.md

## Executive Summary

- Overall Status: PASS
- Required Gate Failures: 0
- Flagged Risks: 2

## Gateboard

| Gate | Status | Why it failed (<=10 words) | Exact fix target |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | All functional requirements have mapped test artifacts | — |
| Proof artifact verifiability | PASS | All artifacts name exact commands, test names, and paths | — |
| Repository standards consistency | PASS | CLAUDE.md + TESTING.md + ARCHITECTURE.md read; no conflicts | — |
| Open question resolution | PASS | Both open questions resolved with explicit assumptions | — |
| Regression-risk blind spots | FLAG | Git persistence-off path, cherry-pick conflict, and settled-run guard are happy-path adjacent; guard test exists but conflict cherry-pick test deferred | `## Tasks > 3.0` |
| Non-goal leakage | FLAG | Task 4.5 includes inline closure recomputation in the TUI, which is logic duplication; bounded and justified | `## Tasks > 4.0` |

## Standards Evidence Table (Required)

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Table-driven tests; persistence-off must no-op; single-writer scheduler; file-is-truth/bus-is-liveness; comments explain non-obvious "why" | none |
| `docs/TESTING.md` | yes | Fake executors behind interfaces; no live model calls; race detector for concurrency work; `go test ./...` + `gofmt -l -w . && go vet ./...` before commit | none |
| `docs/ARCHITECTURE.md` | yes | `internal/` packages have clear responsibilities; engine extensibility via thin consumers (ADR 0003); no bulk content on event bus | none |
| `CONTRIBUTING.md` | not found | — | — |
| `.github/pull_request_template.md` | not found | — | — |

## Findings

### FLAG Findings

1. **Cherry-pick conflict path not integration-tested**
   - Risk: The survivor cherry-pick in `handleReset` can produce an `IntegrationConflictRequest` when commits overlap. Task 3.3 specifies the routing logic but no test exercises the conflict branch; a regression in the conflict path would be silent.
   - Suggested remediation: Add `TestResetCherryPickConflict` to `integration_test.go` in a follow-up or as an optional sub-task under 3.0, scripting a survivor commit that touches the same file as a reset-set commit. This is flagged rather than required because the underlying `IntegrationConflictRequest` gate is already tested by spec 06 A2 tests.

2. **`closureOf` duplicated in TUI (Task 4.5)**
   - Risk: Task 4.5 asks the `monitorModel` to replicate the DAG-walk closure logic using `RunSnapshot.Steps`, creating a second copy of logic that lives authoritatively in `scheduler.closureOf`. If the engine's definition changes, the TUI copy can silently diverge.
   - Suggested remediation: Expose `closureOf` result as part of the `StepsReset` event (already emitted on reset) or add a `ClosureOf(targetID string) []string` method to `Run` that sends a query message through the inbox (similar to `Snapshot`). The current plan is bounded — the TUI only uses it for the confirmation count, not for correctness — but is worth revisiting in a follow-on.

## User-Approved Remediation Plan

- Pending approval

## Chain-of-Verification

- All REQUIRED gates reviewed against spec functional requirements (C1–C4), task file, and three repository-standard sources.
- FR coverage: `closureOf` (C1 FR1 → 1.1/1.3), `rewindPlan` (C1 FR2 → 1.2/1.4), `handleReset` guard (C2 FR1 → 3.2/3.6), journal-before-destructive (C2 FR2 order → 3.3), git rewind+cherry-pick (C2 FR3 → 3.3/3.4), `ClearStepOutputs` (C2 FR4 → 3.1/3.4), state reset (C2 FR4 → 3.3/3.4), `StepsReset` event (C3 FR1 → 2.3/2.7), `Generation` (C3 FR3 → 2.1/2.2/2.5/2.6), TUI stop/r/resume (C4 FR1 → 4.3/4.5), confirmation (C4 FR2 → 4.4/4.6/4.8), rootModel routing (C4 FR3 → 4.1/4.2), footer hints (C4 FR4 → 4.7).
- Open Question 1 (read-only target): assumption recorded in sub-task 1.4 — `rewindTo` maps to just before the first downstream mutating commit; covered by the read-only-target assertion in `TestRewindPlan`.
- Open Question 2 (quiescence predicate): assumption recorded in sub-task 3.3 — `!s.terminated && s.inFlight == 0`; covered by `TestResetGuard`.
- No REQUIRED gates failed after self-questioning.
