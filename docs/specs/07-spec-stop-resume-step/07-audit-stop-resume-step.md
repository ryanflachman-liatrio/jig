# 07-audit-stop-resume-step.md

Planning audit for **[`07-tasks-stop-resume-step`](./07-tasks-stop-resume-step.md)**
against **[`07-spec-stop-resume-step`](./07-spec-stop-resume-step.md)**. Runs 1–2.

## Executive Summary

- Overall Status: **PASS**
- Required Gate Failures: 0
- Flagged Risks: 1 (F1 remediated in run 2; F2 accepted as justified)

## Re-Audit Delta (Run 2)

- **F1 remediated (user-approved):** added task **2.7** (transcript preserved + resume
  appends, not truncates) and task **4.6** (`TestStopResumeStopReentersQuiescence`:
  stop→resume→stop stays alive and re-enters quiescence). Proof-capture tasks
  renumbered (2.8, 4.7). F1 is now closed.
- **F2 retained** as justified (regression sweep operationalizes Success Metric 4).
- No REQUIRED gate statuses changed; all still PASS. No new findings.

## Gateboard

| Gate | Status | Note (<=10 words) | Anchor |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | every FR maps to a planned test | `## Requirement → Parent Task coverage` |
| Proof artifact verifiability | PASS | all artifacts have exact command/path | per-task `Proof Artifact(s)` |
| Repository standards consistency | PASS | 5 sources read, no conflicts | Standards Evidence Table |
| Open question resolution | PASS | OQ1 resolved w/ explicit restart fallback | 3.1, 3.3 |
| Regression-risk blind spots | FLAG | see F1 | 2.0, 4.0 |
| Non-goal leakage | FLAG | see F2 | 5.0 |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Single-writer scheduler (1 inbox msg + 1 handle case, no locks); file-is-truth/bus-is-liveness; persistence-off first-class no-op | none |
| `README.md` | yes | Engine+runner execute the DAG today; Go 1.25; build/vet/test gates | none |
| `docs/ARCHITECTURE.md` | yes | `engine.Executor` + `runner.Mux`; state = fold(journal); DAG + bounded back-edges | none |
| `docs/TESTING.md` | yes | Table-driven inline fixtures; ctx-honoring fakes, no live model calls; `-race` for engine | stale "not implemented" note; conventions valid |
| `docs/adr/0003-*.md` | yes | Extensibility lives in engine/schema; TUI stays a thin renderer | none |
| `AGENTS.md` | not found | — (CLAUDE.md is the equivalent root guidance) | n/a |

## Requirement-to-Test Traceability (evidence)

| Functional requirement | Planned test artifact |
| --- | --- |
| Per-worker child ctx tracked by step id (B1) | `TestStopOneStep` (only A cancelled) |
| `Run.Stop`→`stopMsg`→`handleStop` cancels one worker (B1) | `TestStopOneStep`, `TestStopNonRunningStep` |
| Run stays alive & quiescent, not end-of-run (B1) | `TestStopOneStep` (no `RunFinished`, snapshot alive) |
| Preserve partial diff + transcript on cancel (B1) | `TestStoppedStepCapturesDiff`, `TestStopPersistenceOff` |
| `StatusStopped` parked status (B1) | `internal/step` table case (2.6) |
| Session id captured at start (B2) | `TestSessionIDCapturedAtStart` |
| `Run.Resume` reuses `WithResume`/continue (B2) | `TestResumeContinuesSession` |
| Degrade to fresh restart when no id (B2) | `TestResumeWithoutSessionRestarts` |
| No regressions incl. persistence-off (Metric 4) | `go test ./... -race`, `validate` |

## FLAG Findings

1. **F1 — Resume/continue behavior is only asserted at the dispatch boundary.**
   - Risk: tests assert `ResumeSessionID` is set/empty on the re-dispatched
     `StepRequest`, but not that a resumed worker actually re-enters quiescence
     (stop→resume→stop) or that the transcript *appends* rather than truncates.
   - Suggested remediation: add one assertion in 4.5 (or a 2.x transcript test)
     that a resume appends to the existing `transcript.jsonl` and the run can be
     stopped again — matching the spec's "resume/continue appends" invariant.

2. **F2 — Task 5.0 (regression sweep) exceeds the spec's two named units.**
   - Risk: adds work beyond Units B1/B2. Justified: it operationalizes Success
     Metric 4 ("no regressions incl. persistence-off"), which is in-spec. No
     scope creep — no new runtime behavior, only the repo's standing quality gates.

## Chain-of-Verification

- Do all REQUIRED gates pass with explicit evidence? **Yes** — traceability table
  maps 9/9 requirements to named tests; each proof artifact names an exact
  command/path; 5 guidance sources read with no conflicts; OQ1 has an explicit
  documented fallback (fresh restart) in 3.3.
- Fact-check: `StepRequest.ResumeSessionID`/`Message` (executor.go:18-44),
  `resumeSessions` stashing (engine.go:946-951), session capture site
  (agent.go:298,304), single run ctx (engine.go:99), and ctx-honoring fakes
  (`testExec`, `recoveringExec`) all verified present, so the reuse-based tasks
  rest on real seams.
- Inconsistencies: none unresolved. Both findings are FLAG (advisory), not blocking.

## Next Action

All REQUIRED gates pass. Ready for the implementation phase. The two FLAGs are
advisory — you can accept them as-is or ask me to fold F1's remediation into task
4.5/2.x before handoff.
