# 08-spec-reset-to-step.md

> **Feature C of the [`05-spec-run-integration-reset`](../05-spec-run-integration-reset/05-spec-run-integration-reset.md) mega-spec**, split into its own SDD workflow.
> This feature lets an operator **reset a run to an earlier step** — rewind the run branch to
> before the target step's dependency closure, replay independent survivors, return the closure
> to `pending`, and re-run. It **depends on both foundations**: Foundation A
> ([`06-spec-run-integration-branch`](../06-spec-run-integration-branch/06-spec-run-integration-branch.md))
> for the addressable per-step commit history to rewind, and Foundation B
> ([`07-spec-stop-resume-step`](../07-spec-stop-resume-step/07-spec-stop-resume-step.md)) for the
> per-step stop that reaches quiescence mid-run. Recorded in ADR
> [0008](../../adr/0008-manual-reset-rewind-and-replay.md). Read the parent mega-spec for the
> full cross-foundation rationale; this document is self-contained for implementation.

## Introduction/Overview

jig puts a deterministic orchestration layer around non-deterministic agents. Even with the
integration branch (Foundation A) and per-step stop (Foundation B) in place, there is **no way
for an operator to rewind a run** when an agent produces the wrong thing.

The operator need that motivated this work: *"I made it through planning and review, the
implementation isn't what I wanted, and I want to reset the workflow back to the planning phase —
undoing both the workflow state **and** the code changes."*

This feature is the **manual, generalized mirror of a `[step.loop]` back-edge**: the operator
rewinds the run branch to before the target step's transitive `depends_on` closure, replays the
survivor commits (independent parallel branches that don't depend on the target), returns the
closure to `pending`, bumps a per-step `Generation` counter, and lets the scheduler re-dispatch.
Reset is a **mid-run action on an unfinished, quiescent run** — reached at a gate or by stopping
the running step (Foundation B) — so it never races a live worker; a fully-settled run is
**locked**. This inverts spec 04's retired finished-run premise.

Two properties from the foundations make this addressable:

1. **The run branch has one squash commit per mutating step** (Foundation A), tagged with the
   step id, so the closure's contribution is addressable for a `git reset` + survivor cherry-pick.
2. **The run can be brought to quiescence** (Foundation B), so reset is a clean single-writer
   mutation on the scheduler goroutine with no worker in flight.

**Invariants preserved.** `state = fold(journal.jsonl)` with an append-only journal
(`internal/engine/replay.go`): the reset is a **journaled event**, not a silent mutation.
`StepStatus` transitions are journaled (`journal.go`, kind `step_status`), so a replay already
reconstructs post-reset state from the status stream — the `StepsReset` event is therefore an
**audit** record of operator intent, not a replay-correctness crutch. Transcripts are logs and are
never truncated; a re-run **appends** a new generation. Persistence-off (`runDir == ""`) stays a
first-class no-op path.

## Goals

- **Let an operator reset a run to any earlier step** on an unfinished, quiescent run — rewinding
  the run branch to before the target's transitive `depends_on` closure, replaying independent
  survivors, and re-running the closure fresh.
- **Spare independent parallel branches.** Resetting one branch of a fan-out keeps the other
  branches' code, results, transcripts, and commits — the operator re-runs only what actually
  depended on the target.
- **Keep determinism and the durable record.** No new graph edges, no new termination risk; the
  reset is a journaled audit event written *before* any destructive operation; cherry-pick
  conflicts are human-resolved through the existing Gate, never auto-resolved.
- **Make the re-run legible.** A `Generation` counter (a third provenance axis alongside
  `Attempt`/`Iteration`) marks the manual re-run in the transcript without stealing the auto-retry
  budget.

## User Stories

**As an operator whose implementation is wrong**, I want to reset the run back to the planning
step, so the plan, the implementation, and everything downstream re-run fresh and the wrong code is
unwound — without discarding the whole run.

**As an operator resetting one branch of a parallel fan-out**, I want the *other* parallel branches'
code and results kept, so I only pay to re-run what actually depended on the step I reset.

**As someone auditing a run later**, I want the reset recorded — which step I targeted and what it
invalidated — so the history honestly shows the intervention.

## Demoable Units of Work

Dependency-ordered. Feature C (Units C1–C4) builds reset on top of Foundations A and B. Each unit
is a candidate implementation increment.

### Unit C1: Dependency closure + commit selection

**Purpose:** The pure core — given the workflow and a target, compute the reset set and the git
rewind/replay plan. No engine mutation yet.

**Functional Requirements:**
- The system shall compute the **reset set** = target ∪ its transitive `depends_on` closure, in
  declaration order, excluding independent parallel branches (reuse the closure logic analogous to
  `loopBody`'s forward reachability, `engine.go:1570-1616`, but forward-only).
- Given the step → commit map (Foundation A), the system shall produce a **rewind/replay plan**:
  the commit to `git reset --hard` to (just before the earliest reset-set commit on the run
  branch), and the ordered list of **survivor commits** after that point that are *not* in the
  reset set (to cherry-pick). In a linear reset set this list is empty.
- All git paths shall use the run branch and jig's worktree helpers only — never a path from
  untrusted input.

**Proof Artifacts:**
- Unit test (closure): `A → B → C` with independent `A → D`; `closure(A) = [B, C]` excludes `D`;
  `closure(C) = []`.
- Unit test (plan): with commit order `A,D,B` on the run branch, `reset(A)` yields rewind-to `base`
  and survivor-replay `[D]`; `reset` of a linear tail yields empty replay.
- Proof artifact: `08-proofs/C1.0-reset-plan.txt`.

### Unit C2: Reset execution (rewind + replay + state reset + re-dispatch)

**Purpose:** Apply the plan on a live, quiescent run and re-run the closure.

**Functional Requirements:**
- The system shall add `Run.Reset(stepID)` → `resetMsg{stepID}`, handled single-writer as
  `handleReset`, guarded to run **only when the run is unfinished and quiescent** (no worker in
  flight, not settled — Q: reuse `inFlight == 0` plus a not-settled check).
- `handleReset` shall, in order: **(1)** journal the `StepsReset` audit event (C3) and emit
  `StepStatus(→pending)` for the reset set *before* any destructive op; **(2)** `git reset --hard`
  the run branch to the plan's rewind point and cherry-pick the survivor commits (a conflict routes
  to the integration-conflict gate from Foundation A's Unit A2); **(3)** `ClearStepOutputs` for
  each reset-set step (per-step `result.json`/`output.md`/`output.json` only — see Non-Goals on
  artifacts); **(4)** reset each reset-set step's in-memory state to `pending`, `Attempt = 0`,
  `Iteration = 0`, `Result = nil`, and **bump `Generation`**; clear per-step scheduler maps
  (`resumeSessions`, `stepMessage`/`stepFeedback`, `rerunSource`, `recoverCount`,
  pre-resolved/collected/pending user inputs) for the reset set so no stale routing survives.
- After `handleReset`, the main loop shall re-dispatch normally; the target's untouched upstream
  keeps it ready, it re-runs off the rewound run branch, and its downstream follow.
- Independent survivor steps shall keep their state, result, transcript, and commit exactly.

**Proof Artifacts:**
- Unit test: `A → B → C` and independent `A → D` run to a quiescent gate; `Run.Reset("A")` re-runs
  `A`, `B`, `C` (with bumped `Generation`), keeps `D` (state + survivor commit), and the run branch
  no longer contains the old `A/B/C` commits but still contains `D`'s.
- Unit test (linear tip): `Run.Reset("C")` re-runs only `C`; no other step changes; empty survivor
  replay; run branch rewound by one commit.
- Unit test (guard): `Run.Reset` on a settled run, or while a worker is in flight, is a rejected
  no-op with an observable reason.
- Proof artifact: `08-proofs/C2.0-reset-reexecutes-closure.txt` — ordered status events + run branch
  log before/after.

### Unit C3: `StepsReset` audit event + Generation provenance

**Purpose:** Record the operator's intent durably and make the re-run legible.

**Functional Requirements:**
- The system shall add `StepsReset{RunID, Target string, Closure []string, RewindTo string}` to
  `internal/engine/event.go` with `isEvent()`, a journal kind `"steps_reset"`, and envelope
  encode/decode in `journal.go` (mirroring `LoopFired`/`run_finished`); it shall round-trip through
  `MarshalEnvelope`/`UnmarshalEnvelope` and an unknown-kind line shall still be skipped by
  `ReplayJournal`.
- The event carries the target, the ordered closure, and the rewind commit — provenance the
  `StepStatus` stream cannot express. It is **not** required for state reconstruction (the journaled
  `StepStatus(→pending→…)` transitions already fold to the post-reset state); no state-changing fold
  handler is added (there is none for `LoopFired` either).
- `Generation` shall be added to `step.State` and threaded through the transcript writer, the
  `Entry`, and the `StepStatus` event exactly as `Attempt`/`Iteration` already are; the monitor
  shall render a `── re-run N ──` separator when `Generation` increases (mirroring the existing
  attempt/iteration separators, `monitor.go:1702-1712`).

**Proof Artifacts:**
- Unit test (journal): `StepsReset` round-trips through the envelope codec; an unknown kind is
  skipped.
- Unit test (replay): a synthesized journal ending in `steps_reset` + fresh `StepStatus`
  transitions folds to the post-reset state (closure re-run, survivors untouched).
- Unit test (transcript): a reset step's transcript shows two generations distinguished by
  `Generation`, and the monitor renders the separator.
- Proof artifact: `08-proofs/C3.0-steps-reset-audit.txt`.

### Unit C4: TUI — stop, reset trigger, confirmation, rendering

**Purpose:** The human surface — stop a step, then reset to a selected step, with a confirmation
that names the blast radius. (Drives Foundation B's `Run.Stop`/`Run.Resume` and this feature's
`Run.Reset`.)

**Functional Requirements:**
- In the Monitor's Steps region, when the run is **unfinished and quiescent**, pressing a **stop**
  key on a running step emits `stopStepMsg`; pressing **`r`** on a selected terminal step emits a
  reset request.
- If the target's closure is **non-empty**, `r` opens a confirmation entry naming the count and ids
  of downstream steps to be reset (default **No**); on confirm, emit `resetStepMsg{stepID}`. If the
  closure is **empty** (linear tip), emit `resetStepMsg` immediately, no confirmation.
- `rootModel.Update` shall route `stopStepMsg → run.Stop`, `resetStepMsg → run.Reset`,
  `resumeStepMsg → run.Resume` (mirroring `reviewVerdictMsg → run.Resolve` / `recoverResponseMsg →
  run.Recover`, `root.go:242-277`).
- The step list shall reflect resets promptly via the `StepStatus` events from C2/C3. Footer hints
  shall advertise `stop`/`r`/`resume` only when eligible (the `SetEnabled` pattern in `footerView`).

**Proof Artifacts:**
- TUI test: `r` on a mid-graph terminal step (run quiescent) opens the confirmation with the correct
  downstream count; `y` emits `resetStepMsg`; `n`/`esc` emits nothing.
- TUI test: `r` on a linear tip emits `resetStepMsg` with no confirmation; `r` on a settled run or a
  non-quiescent run emits nothing and shows a hint; `stop` on a running step emits `stopStepMsg`.
- Proof artifact: `08-proofs/C4.0-reset-confirmation.txt`.

## Non-Goals (Out of Scope)

1. **Resetting a finished, settled run.** Reset is only for an unfinished, quiescent run; a settled
   run is locked. Reopening a run from disk to reset it is a deferred follow-up (it needs an
   executable scheduler rebuilt from the journal fold).
2. **Auto-resolving cherry-pick (or integration) conflicts.** Overlapping changes are human-resolved
   through the Gate (reusing Foundation A's Unit A2); jig never guesses whose version wins.
3. **Undoing an already-final-merged run from the user's branch.** Reset operates on the run branch,
   not on the user's committed history.
4. **Per-step artifact GC.** `ClearStepOutputs` clears only the per-step derived outputs under
   `steps/<id>/` (`result.json`/`output.md`/`output.json`), which are unambiguously owned. The shared
   run-level `artifacts/` dir has no per-step ownership record; stale artifact files are overwritten
   on re-run, as they already are for loop/recovery re-runs.
5. **A workflow schema surface for reset.** No per-step `resettable` flag; reset is a runtime/TUI
   action, adding no load-time validation.
6. **Partial or selective reset.** Reset invalidates the entire transitive closure or nothing.

## Design Considerations

The new visual surface is the **reset confirmation** — it reuses the existing Gate/entry pattern and
precedence (ADR 0002), so it inherits focus handling and styling. The reset confirmation must state
the blast radius plainly and default to No. Footer hints advertise `stop`/`r`/`resume` only when the
run is quiescent and the selected step is eligible, following the mode-specific `footerView`
convention. The integration-conflict gate reused for cherry-pick conflicts is delivered by
Foundation A (Unit A2).

## Repository Standards

- **Engine extensibility, thin consumers (ADR 0003).** Reset lives in the scheduler — the single
  owner of state, the DAG, the run branch, and the journal writer; the TUI only sends messages and
  renders.
- **Single-writer scheduler.** Reset (and the conflict resolve it reuses) is one `inbox` message +
  one `handle` case — no locks.
- **File is truth, bus is liveness.** Reset mutates the run branch and per-step outputs and journals
  an audit event; the bus carries only `StepStatus` liveness.
- **Persistence-off / non-git is first-class.** Every git and disk operation no-ops when there is no
  run dir / no repo root; the engine tests that run without them must keep passing.
- **Tests are table-driven with inline fixtures**, and new engine behavior gets both the happy path
  and the guard path (settled run, worker in flight).
- **Comments explain the non-obvious "why"** — especially the rewind/replay reset and the
  before-destruction journaling order.

## Technical Considerations

- **Reset = rewind + replay, computed once, applied atomically on the scheduler goroutine.** The
  journaled `StepStatus(→pending)` transitions and the `StepsReset` audit event are written **before**
  the `git reset`/cherry-pick and the `ClearStepOutputs`, so a crash leaves the journal (pending, no
  expected output) consistent with the deleted files.
- **`StepStatus` is journaled**, so replay reconstructs post-reset state from the status stream;
  `StepsReset` is audit-only and needs no state-changing fold handler.
- **`Generation` reuses the `Attempt`/`Iteration` plumbing** through the transcript writer, `Entry`,
  `StepStatus`, and the monitor separators — a third provenance axis, gating no budget (unlike
  `Attempt`, which gates `MaxRetries` at `engine.go:1070`).
- **Quiescence guard.** Reset runs only when the run is unfinished and quiescent (no worker in flight,
  not settled), so it is a clean single-writer mutation that never races a live worker.
- **No schema, no load-time validation** is added; the "schema additions are exhaustive/load-time"
  rule does not apply.

## Security Considerations

- **Bounded git and file operations.** Reset operates only on the run branch and jig's own worktrees,
  and `ClearStepOutputs` only within `.jig/runs/<id>/steps/<stepID>/`, using datastore and worktree
  helpers exclusively — never a path from user input.
- **Confirmation guards destruction.** A mid-graph reset names its blast radius and defaults to No;
  cherry-pick conflicts pause for a human rather than being auto-resolved.
- **Auditability.** `StepsReset` records the target, the closure, and the rewind point, so a run's
  history reflects operator intervention rather than silently dropping work.
- **No unbounded git history writes to the user's branch.** Reset never touches the user's committed
  history; it operates on the run branch only.

## Success Metrics

1. **Reset unwinds correctly:** resetting an upstream step re-runs its transitive closure with a
   bumped `Generation`, rewinds the run branch past exactly those commits, and preserves independent
   survivor branches' state, transcript, and commits (C1–C3).
2. **Records preserved:** `journal.jsonl` and every `transcript.jsonl` are strictly append-only; only
   per-step `result.json`/`output.*` of the closure are deleted; folding the post-reset journal
   reconstructs the live state (C2, C3).
3. **Conflicts are human-owned:** an overlapping cherry-pick raises the gate and resolves cleanly; it
   is never auto-resolved (C2, reusing A2).
4. **Guarded:** reset on a settled or non-quiescent run is a rejected no-op with an observable reason
   (C2, C4).
5. **No regressions:** `go build/vet/test ./...` pass including persistence-off paths.

## Design Decisions & Rationale

Resolved with the operator across the 2026-08-07 grilling session, grounded in read-only exploration
of `internal/engine`, `internal/datastore`, `internal/transcript`, and `internal/tui`. Recorded in
ADR [0008](../../adr/0008-manual-reset-rewind-and-replay.md).

1. **Reset is mid-run on an unfinished, quiescent run; a settled run is locked.** (Inverts spec 04's
   finished-run premise.) Quiescence is reached at a gate or by stopping the running step (Foundation
   B) — so reset never races a live worker, keeping it a clean single-writer mutation.
2. **Reset = git rewind + survivor replay over the transitive `depends_on` closure.** A bare `git
   reset --hard` by time over-reverts independent parallel branches; replaying survivors spares
   exactly the branches that don't depend on the target. Linear workflows degenerate to a plain
   rewind.
3. **The reset is a journaled *audit* event, not a replay crutch.** `StepStatus` is journaled, so the
   fold already reconstructs post-reset state; `StepsReset` records operator intent (target + blast
   radius) and is ordered before destructive ops for crash-consistency. (Corrects spec 04's rationale,
   which wrongly assumed replay would resurrect stale results.)
4. **`Generation`, not `Attempt`, marks a manual re-run.** The auto-retry cap is `state.Attempt <
   maxRetries` (`engine.go:1070`), so bumping `Attempt` would steal the budget; `Generation` is a
   third provenance axis that gates nothing and makes the transcript re-run legible.
5. **Cherry-pick conflicts are human-resolved via the Gate**, reusing Foundation A's
   integration-conflict gate — jig never guesses whose version wins.

## Open Questions

1. **Read-only steps and reset granularity.** Read-only steps produce no commit, so resetting *to* a
   read-only step maps to the run-branch state as of just before its downstream mutating commits;
   confirm the exact mapping when the reset set has no commits of its own. Non-blocking: the linear and
   mutating-target cases are fully specified; the read-only-target edge can be pinned during C1.
2. **Quiescence check shape.** Proposed `inFlight == 0` plus a not-settled check for the `handleReset`
   guard; confirm the precise predicate against the settled-run detection when implementing C2.
