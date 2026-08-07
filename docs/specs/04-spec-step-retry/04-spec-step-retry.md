# 04-spec-step-retry.md

> **⚠️ Superseded by [`05-spec-run-integration-reset`](../05-spec-run-integration-reset/05-spec-run-integration-reset.md).**
> A grilling session (2026-08-07) inverted this spec's central premise. Retry is no
> longer a *finished-run* action layered on **park-after-finish**; it is a *mid-run*
> **reset** reached by **stopping** the running step, and it depends on a new
> **run-integration-branch** model for code changes (steps could not build on each
> other's code under the isolated model this spec assumed). The park-after-finish
> lifecycle, `RunResumed`, the done-latch fix, and per-step worktree-freshness are all
> retired. This document is kept for its verified engine/datastore/TUI findings and its
> Q&A history; the live design is spec 05.

## Introduction/Overview

Today a jig run is a one-shot traversal. Once the scheduler settles, it emits
`RunFinished` and **returns** (`internal/engine/engine.go:530-533`); its deferred
teardown then closes the journal writer (`engine.go:506-508`) and removes the run's
worktrees (`engine.go:509-516`). There is **no way for a user to re-run a step**.
The only re-executions that exist are automatic and engine-initiated: a
`[step.validate]` gate with `on_failure = "retry"` (`applyFailurePolicy`,
`engine.go:1055-1093`), the recovery gate that re-runs a *failed* step
(`enterRecovery`/`handleRecover`, `engine.go:1095-1153`), and a `[step.loop]`
back-edge (`fireLoop`, `engine.go:1503-1568`). None of them is triggered by the
user against an arbitrary step, and none of them (except a loop) touches steps
*downstream* of the one being re-run.

This spec adds a **manual, user-driven retry**: from the run monitor, select any
step in a **finished** run and re-run it. If the chosen step sits *upstream* of
other steps that already ran, re-running it makes their output stale — those
downstream steps are **reset so they re-run fresh** against the new upstream
result. If the chosen step is the tip of what ran (nothing downstream), retry just
re-runs it. This is the manual mirror of what `fireLoop` already does for a loop
body (`engine.go:1503-1568` resets a band of steps to `pending` with a bumped
counter) — generalized to *any* step, triggered by a human, and extended to purge
the downstream steps' on-disk output.

**Two invariants shape the design.** First, jig's durable run state is
reconstructable as **`state = fold(journal.jsonl)`** and the journal is
append-only (`ReplayJournal`, `internal/engine/replay.go`). A reset therefore
cannot be a silent in-memory mutation plus a file delete — a reopened run would
fold the journal back to the *pre-reset* state and resurrect the stale downstream
results. The reset must itself be a **journaled event**. Second, the run lifecycle
tears itself down on finish (journal closed, worktrees removed), so retry requires
the scheduler to **stay alive after `RunFinished`** rather than returning — this is
the central mechanism change and is recorded as a design decision below.

Scope was fixed with the user in [Questions Round 1](04-questions-1-step-retry.md):
retry targets a **live, in-memory, finished** run (not a run reconstructed from an
earlier session's journal — that is a deliberate follow-up); it is available **only
once the whole run is done**; "downstream" is the **transitive `depends_on`
closure**; the retried step keeps its own history (bump `Attempt`, append); and the
trigger is **`r`** with a confirmation that names the blast radius. Design
decisions and rejected alternatives are logged in
[Design Decisions & Rationale](#design-decisions--rationale); the architectural
call warrants a new ADR (proposed **ADR 0007 — manual step retry invalidates the
transitive downstream closure**; 0005 is reserved by spec 02, 0006 by spec 03).

## Goals

- Let a user **re-run any completed step** of a finished run from the TUI run
  monitor, with a single keypress and a confirmation that states the blast radius.
- **Invalidate exactly the transitive downstream** of the retried step: reset those
  steps to `pending`, delete their **result/output** files so they re-run fresh,
  and leave independent parallel branches untouched.
- **Preserve the durable record.** The journal stays append-only — the reset is a
  new journaled event, so `state = fold(journal)` still reconstructs the correct
  post-reset state. Per-step transcripts are never truncated (they are logs, like
  the journal); a re-run **appends** a new generation.
- **Reuse the existing single-writer engine model.** Retry is one new
  `scheduler.inbox` message and one handler, mirroring `Run.Recover`/`Run.Resolve`;
  no new concurrency surface.
- **Keep the determinism guarantees.** The DAG, static validation, and termination
  guarantee are unchanged — retry adds no new graph edges and no new workflow
  schema; it is a runtime + TUI capability only.

## User Stories

**As a jig operator reviewing a finished run**, I want to re-run one step whose
output was wrong, so that I don't have to discard the whole run and start over.

**As an operator who re-runs an early step**, I want every step that depended on it
to automatically re-run against the corrected output, so that I never ship a run
where a late step is built on a stale early result.

**As an operator re-running the last step** (or a step with nothing downstream), I
want it to just re-run with no ceremony, so that the common "that answer wasn't
quite right, try again" case is frictionless.

**As an operator about to retry a mid-graph step**, I want to be told *how many*
downstream steps will be reset before it happens, so that I don't destroy work by
retrying the wrong step.

**As someone who reopens a run later**, I want the reset to have been recorded
durably, so that the run's history honestly shows "the operator reset to step X and
re-ran from there" rather than silently losing the earlier results.

**As a maintainer**, I want retry to reuse the scheduler's single-writer message
loop and the existing `Attempt`/transcript machinery, so that it introduces no new
locking, no new schema, and no new event-loss surface.

## Demoable Units of Work

The units are dependency-ordered. Unit 1 builds the pure "what is downstream and
what gets cleared" core. Unit 2 makes the reset survive a journal replay. Unit 3
wires the engine action and the run-lifecycle change end-to-end (the core value).
Unit 4 adds the TUI trigger and confirmation. Unit 5 aligns docs and records the
ADR.

### Unit 1: Downstream closure + on-disk output reset

**Purpose:** The pure, testable core — given a workflow and a target step, compute
the set of steps to invalidate, and delete exactly those steps' result/output files
while preserving the append-only records. No engine wiring yet.

**Functional Requirements:**
- The system shall provide a function that returns the **transitive `depends_on`
  closure** of a step: every step reachable by following forward `depends_on` edges
  from the target (direct dependents, their dependents, transitively), excluding the
  target itself, in a **deterministic order** (workflow declaration order). Steps not
  reachable from the target (independent parallel branches) shall be excluded.
- The system shall provide a datastore helper (proposed
  `datastore.ClearStepOutputs(runDir, stepID string) error`) that removes a step's
  **derived output** on disk: `result.json`, `output.md`, `output.json`
  (`datastore.go:88-111`), and the files that step produced under `artifacts/`. It
  shall **not** delete or truncate the step's `transcript.jsonl`, and it shall
  **never** touch `journal.jsonl`.
- The helper shall be **persistence-off safe**: when `runDir == ""` it is a no-op
  returning nil, and a missing file is not an error (removing an already-absent
  output succeeds).
- Path construction shall use the existing `datastore` path helpers only — never a
  path derived from untrusted input — so deletion cannot escape the run directory.

**Proof Artifacts:**
- Unit test (closure): for a fixture DAG `A → B → C` with an independent `A → D`,
  `downstream(A)` returns `[B, C]` in declaration order and excludes `D`;
  `downstream(C)` returns `[]`.
- Unit test (clear): given a populated `steps/<id>/`, `ClearStepOutputs` removes
  `result.json`/`output.md`/`output.json`/produced artifacts and leaves
  `transcript.jsonl` and `journal.jsonl` intact; a second call is idempotent; a
  `runDir == ""` call is a no-op.
- Proof artifact: the closure test output saved under
  `04-proofs/1.0-downstream-closure.txt`.

### Unit 2: The reset event + journal round-trip + fold handler

**Purpose:** Make the reset **replay-consistent**. A new journaled event records
that the operator reset to a step; folding it reconstructs the post-reset state, so
a reopened/replayed run agrees with the live scheduler.

**Functional Requirements:**
- The system shall add an engine event (proposed
  `StepsReset{RunID, Target string, Reset []string}`) alongside the existing events
  in `internal/engine/event.go`, with `isEvent()` and a journal kind (proposed
  `"steps_reset"`) plus encoder/decoder in `internal/engine/journal.go` (mirroring
  `LoopFired` / `run_finished`, `journal.go:30-31,77-78`).
- The event shall carry the **target** step id and the ordered list of **reset**
  downstream step ids, so the fold has everything it needs without re-deriving the
  graph.
- The state-fold used by `ReplayJournal` consumers (and the live snapshot path)
  shall, on a `StepsReset` event, set each `Reset` step back to `pending` with
  `Attempt = 0`, `Iteration = 0`, `Result = nil`, and record the target as re-run
  (bumped `Attempt`). Folding a journal whose tail is `…all steps succeeded,
  steps_reset(target=B, reset=[C])` shall yield `A,B` context intact, `B` marked
  re-run, and `C` back to `pending`.
- The event shall round-trip through `MarshalEnvelope`/`UnmarshalEnvelope`
  unchanged, and an unknown-kind line shall still be skipped by `ReplayJournal`
  (best-effort read invariant preserved).

**Proof Artifacts:**
- Unit test (journal): `StepsReset` round-trips through the envelope codec.
- Unit test (replay/fold): a synthesized journal ending in a `steps_reset` event
  folds to the expected reset state (target re-run, listed downstream `pending`,
  independent steps untouched).
- Proof artifact: `04-proofs/2.0-steps-reset-replay.txt` — the folded state before
  and after the reset event.

### Unit 3: Engine `Run.Retry` + `handleRetry` + park-after-finish lifecycle

**Purpose:** Wire it together in the engine — the core deliverable. A public
`Run.Retry(stepID)` resets the target and its downstream, journals the reset, and
re-dispatches the frontier; and the scheduler stays alive after finishing so it can
receive the retry.

**Functional Requirements:**
- The system shall add `Run.Retry(stepID string)` (`engine.go:181` `Run` block,
  beside `Recover`/`Resolve`) that sends a new `retryMsg{stepID}` to
  `scheduler.inbox`, handled in the scheduler's single-writer `handle` switch.
- `handleRetry(stepID)` shall, in order:
  1. **Guard.** Reject (no-op, with an observable reason) unless the **run is
     finished** (settled — no step `running`/`validating` and the loop reached its
     terminal check) and the **target step is terminal** (`succeeded`/`failed`/
     `skipped`). This matches Q2: retry is only available on a done run.
  2. **Compute downstream** via Unit 1's closure.
  3. **Clear downstream output** via Unit 1's `ClearStepOutputs` for each downstream
     step; reset each downstream `step.State` to `pending`, `Attempt = 0`,
     `Iteration = 0`, `Result = nil`. A previously `skipped` downstream step returns
     to `pending` so its `when` guard re-evaluates on the re-run.
  4. **Reset the target**: bump its `Attempt`, set it back to `pending`, clear its
     in-memory `Result`. Its own `transcript.jsonl` is **kept** (the re-run appends
     a new generation, tagged with the bumped `Attempt`); its stale `output.*` are
     overwritten by the re-run.
  5. **Journal** a `StepsReset{Target, Reset}` event (Unit 2) to the still-open
     writer, and emit `StepStatus` events for the target and every reset step so the
     TUI reflects the transition to `pending`/`running`.
- After `handleRetry`, the scheduler's main loop shall re-dispatch: the target
  becomes ready (its upstream `depends_on` are untouched and still satisfied), runs,
  and its downstream become ready in turn — a normal traversal that eventually emits
  a **fresh** `RunFinished`.
- **Park-after-finish lifecycle.** The scheduler shall no longer treat the first
  terminal settle as end-of-life. Instead of `return`ing at `engine.go:530-533`
  after emitting `RunFinished`, it shall **block on `inbox`** awaiting a possible
  `retryMsg` (or `Cancel`). The teardown deferred today (journal close
  `engine.go:506-508`; worktree removal `engine.go:509-516`) shall run **only on
  `Cancel`/`ctx.Done()`**, so the journal stays writable and worktrees stay
  available for a retry. `RunFinished` may be emitted more than once over a run's
  life (once per settle); consumers already treat it as "currently done," which
  retry simply reverses.
- Retry shall be **framing-preserving for untouched steps**: steps outside the
  downstream closure keep their state, result, and transcript exactly.

**Proof Artifacts:**
- Unit test (engine, fake executor): run `A → B → C` and independent `A → D` to
  completion; `Run.Retry("A")`; assert `A` re-runs with `Attempt = 1`, `B` and `C`
  re-execute after being reset, `D` is never re-run, and a second `RunFinished` is
  emitted.
- Unit test (tip): `Run.Retry("C")` re-runs only `C`; no other step's state changes
  and no output is cleared (empty downstream closure — the "same/most-recent step"
  case from the request).
- Unit test (guard): `Run.Retry` on a non-terminal step, or before the run is done,
  is a rejected no-op leaving all state unchanged.
- Proof artifact: `04-proofs/3.0-retry-reexecutes-downstream.txt` — the ordered
  status events for the `Retry("A")` scenario.

### Unit 4: TUI trigger + confirmation + routing

**Purpose:** The human surface — press `r` on a step in the monitor's (read-only
today) step list to retry it, with a confirmation that names the blast radius.

**Functional Requirements:**
- In the monitor's `focusSteps` region (`internal/tui/monitor.go`; keymap
  `internal/tui/keys.go:196-239`), pressing **`r`** on the selected step shall,
  **only when the run is done and the step is terminal**:
  - If the step's downstream closure is **non-empty**, open a lightweight
    **confirmation overlay** stating how many downstream steps will be reset (e.g.
    "Reset step `B` and re-run 1 downstream step (`C`)? [y/N]"). On confirm, emit a
    new `retryStepMsg{stepID}`; on cancel, dismiss with no change.
  - If the downstream closure is **empty** (retrying the tip), emit `retryStepMsg`
    **immediately with no confirmation** — matching the request's "don't worry about
    it" clause.
  - Otherwise (run not done, or step not terminal), be a **no-op** with a brief
    footer hint (e.g. "retry available when the run is done").
- The confirmation overlay shall obey the existing overlay precedence (it takes
  keyboard focus like the review/prompt gates, `monitor.go` overlay early-returns)
  so navigation keys can't leak past it.
- `rootModel.Update` shall route `retryStepMsg` to `run.Retry(stepID)` (mirroring
  the `reviewVerdictMsg → run.Resolve` / `recoverResponseMsg → run.Recover` routing
  at `internal/tui/root.go:242-277`).
- The step list shall reflect the reset promptly (steps returning to `pending`/
  `running`) via the `StepStatus` events emitted in Unit 3.

**Proof Artifacts:**
- TUI test (key sequence): `r` on a mid-graph terminal step opens the confirmation
  naming the correct downstream count; `y` emits `retryStepMsg{that step}`; `n`/`esc`
  emits nothing.
- TUI test: `r` on the tip step emits `retryStepMsg` with no confirmation; `r` while
  the run is still running (or on a non-terminal step) emits nothing and shows the
  hint.
- Proof artifact: `04-proofs/4.0-retry-confirmation.txt` — the rendered confirmation
  overlay for a 2-downstream retry.

### Unit 5: Docs, example alignment, and ADR

**Purpose:** Make retry a documented, first-class capability and record the
lifecycle decision.

**Functional Requirements:**
- `docs/ARCHITECTURE.md` shall document the retry seam (the new engine action) and
  the **park-after-finish** run lifecycle (a run is "done" but its scheduler stays
  alive until `Cancel`, so retry can re-enter the traversal), plus the note that
  worktree removal and journal close now happen on cancel, not first-settle.
- `docs/engine-design.md` shall describe the reset flow (compute closure → clear
  downstream outputs → reset state → journal `StepsReset` → re-dispatch).
- `docs/run-monitor-transcript-plan.md` (which already anticipates
  `attempt`/`iter`-tagged retries) shall be cross-referenced for the transcript
  append behavior.
- A new ADR (proposed `docs/adr/0007-manual-step-retry-downstream-invalidation.md`)
  shall record: retry invalidates the **transitive downstream closure**; the reset
  is a **journaled event** (not a silent mutation) to keep `state = fold(journal)`;
  and the **park-after-finish** lifecycle with its trade-off (worktrees/journal linger
  until cancel).
- The change shall add **no** workflow schema surface: `go run ./cmd/jig validate
  examples/feature.toml` still exits 0 with no new fields.

**Proof Artifacts:**
- `go build ./...`, `go vet ./...`, and `go test ./...` pass.
- The ADR file exists and is linked from the ADR index/README if one exists.
- Proof artifact: `04-proofs/5.0-docs-and-adr.md` — a short spot-check listing the
  doc sections added and the ADR created.

## Non-Goals (Out of Scope)

1. **Retrying a run reconstructed from a previous session's journal.** Retry
   targets a **live, in-memory** run only (Q1). Reopening yesterday's finished run
   from `.jig/runs/` and retrying a step in it requires re-instantiating an
   executable scheduler from the journal fold and is a deliberate **follow-up spec**.
2. **Retrying mid-run or at a gate.** Retry is offered **only when the whole run is
   done** (Q2). Cancelling in-flight workers, and retrying a step parked at a review/
   input/recovery gate (those have their own resolution paths), are out of scope.
3. **Hard-deleting the journal or truncating transcripts.** The journal is
   append-only (the reset is an event); transcripts are logs and are never truncated
   (Q3). Wholesale cleanup remains `jig prune`.
4. **Partial or selective downstream reset.** Retry invalidates the *entire*
   transitive closure or nothing — there is no "reset B but not C" mode.
5. **A workflow schema surface.** No per-step `retryable` flag and no author-facing
   TOML config; retry is a runtime/TUI action, so it adds no load-time validation.
6. **Editing a step before retry.** Retry re-runs the step against its existing
   definition and the (unchanged) upstream artifacts; it does not let the user edit
   the prompt, inputs, or model first.
7. **An undo of a retry.** The journal records the reset for auditability, but there
   is no "un-reset" action that restores the discarded downstream output.

## Design Considerations

The only new visual element is the **confirmation overlay** in Unit 4. It reuses the
existing gate/overlay pattern and precedence (like the review and recovery prompts),
so it inherits the monitor's focus handling and styling. It must state the blast
radius plainly — the target step and the count (and ids) of downstream steps to be
reset — and default to **No** (destructive action). No confirmation is shown when
the downstream closure is empty.

The step list gains a footer hint advertising `r` when the selected step is
eligible, and a disabled-state hint otherwise, following the mode-specific footer
convention already in `footerView()`.

## Repository Standards

- **Engine extensibility, thin consumers (ADR 0003).** The reset logic lives in the
  scheduler (the single owner of state, the DAG, and the journal writer); the TUI
  only sends a message and renders — mirroring `Run.Recover`/`handleRecover`.
- **File is truth, bus is liveness.** The reset mutates on-disk output and journals
  an event; the bus carries only the resulting `StepStatus` liveness signals. No bulk
  content rides the bus (CLAUDE.md).
- **Single-writer scheduler.** All state mutation happens on the scheduler goroutine
  via an `inbox` message; retry adds a `retryMsg` and a `handle` case — no locks
  (`engine.go` inbox pattern, `engine.go:274-344`).
- **Persistence-off is a first-class path.** `ClearStepOutputs` and all disk work
  no-op when `runDir == ""`; the engine tests that run without a run dir must keep
  passing (CLAUDE.md).
- **Tests are table-driven with inline fixtures**; new engine behavior gets both the
  happy path (retry re-executes downstream) and the guard path (retry rejected)
  (`engine` test house style).
- **Comments explain the non-obvious "why"** — especially the park-after-finish
  lifecycle change, which reverses a currently-load-bearing `return`.

## Technical Considerations

- **Park-after-finish is the crux.** Today `run()` returns at the terminal check
  (`engine.go:530-533`) and its `defer` closes the journal writer and removes
  worktrees. Retry requires the goroutine to **stay parked on `inbox`** after
  emitting `RunFinished`, deferring teardown to `Cancel`/`ctx.Done()`. Consequences
  to handle deliberately:
  - The **journal writer stays open** across the "done" state (good — the reset
    event appends normally; no reopen needed).
  - **Worktrees are not removed at first-settle** — they persist until cancel so a
    retried mutating step can re-run in its worktree. This lengthens worktree
    lifetime; document it and ensure `Cancel` still cleans up.
  - `Run.Snapshot()`'s lock-free read currently relies on `finalSnap` being written
    before `done` closes in the `defer` (`engine.go:498-502`). Parking instead of
    returning must not break snapshot reads for a "done-but-alive" run; the snapshot
    path may need to publish on each settle rather than once at exit.
- **Reset must reset both memory and the fold.** The live `handleRetry` mutation and
  the `StepsReset` fold handler (Unit 2) must produce **identical** state, or a live
  snapshot and a replay of the same run will disagree. Keep them derived from one
  helper if practical.
- **Worktree freshness for reset mutating steps.** A downstream mutating step that
  ran in a worktree may have left changes there; on reset its worktree should present
  a clean starting point for the re-run. Whether that means reusing, resetting, or
  recreating the worktree is an implementation detail (see Open Questions) that does
  not change the user-facing behavior.
- **The target's re-run uses the append-`Attempt` path** already exercised by
  `on_failure = "retry"`, so the transcript writer's monotonic-seq-on-reopen and the
  monitor's attempt/iteration separators work with no new code.
- **No schema, no validation.** Unlike spec 03, there is nothing to parse or
  validate at load time; the "schema additions are exhaustive/load-time" rule does
  not apply here.

## Security Considerations

- **Bounded deletion.** `ClearStepOutputs` deletes only within
  `.jig/runs/<id>/steps/<stepID>/` and the run's `artifacts/`, using `datastore`
  path helpers exclusively — never a path derived from user input — so a retry can
  never remove files outside the run directory.
- **Confirmation guards destruction.** The mid-graph retry is destructive to
  downstream output; the Unit 4 confirmation (defaulting to No, naming the count) is
  the guard rail against retrying the wrong step.
- **Auditability.** The `StepsReset` journal event records *what* was reset and the
  target, so a run's history honestly reflects operator intervention rather than
  silently dropping earlier results.
- **No new secrets or surfaces.** Retry introduces no credentials, no network calls,
  and no new external inputs; `.jig/` remains git-ignored.

## Success Metrics

1. **Downstream re-executes:** retrying an upstream step in a finished run re-runs it
   and its full transitive `depends_on` closure, and leaves independent parallel
   branches untouched (Unit 3 test).
2. **Records preserved:** after a retry, `journal.jsonl` and every `transcript.jsonl`
   are strictly longer (append-only) — never truncated — and the deleted files are
   only downstream `result.json`/`output.*`/artifacts (Unit 1 + Unit 3 tests).
3. **Replay consistency:** folding the post-retry journal reconstructs the same state
   the live scheduler holds (Unit 2 test).
4. **Tip retry is frictionless:** retrying a step with no downstream re-runs only that
   step, with no confirmation and no file deletion (Unit 3 + Unit 4 tests).
5. **Guarded trigger:** `r` is a no-op with a hint on a non-terminal step or a
   not-yet-done run; on a mid-graph step it shows a confirmation naming the blast
   radius (Unit 4 tests).
6. **No regressions:** `go build ./...`, `go vet ./...`, `go test ./...` pass —
   including the persistence-off engine/runner tests — and `jig validate
   examples/feature.toml` still exits 0 (no schema change).

## Design Decisions & Rationale

Resolved with the user in [Questions Round 1](04-questions-1-step-retry.md) on
2026-08-07, grounded in a read-only exploration of `internal/engine`,
`internal/datastore`, `internal/step`, `internal/transcript`, and `internal/tui`.
This log preserves the rationale and rejected alternatives.

1. **Retry lives in the engine; the TUI stays thin.** Only the scheduler owns the
   DAG, step state, and the journal writer, so the reset can only be done there. The
   TUI sends a `retryStepMsg` that `root` routes to `Run.Retry`, exactly mirroring
   the existing `Resolve`/`Recover` path (`root.go:242-277`). Honors ADR 0003.

2. **Scope: live, in-memory, finished runs only.** (Q1, Q2.) Retrying a live run
   reuses the running `scheduler`/`Run` machinery; the new work is one message plus a
   reset routine. Retrying a run *reconstructed from disk* would require rebuilding an
   executable scheduler (worktrees, session ids, in-flight assumptions) from the
   journal fold — roughly double the surface — and is deferred (Non-Goal 1).
   Restricting to a **done** run (not merely quiescent) guarantees no workers are in
   flight, so the reset is a clean single-writer mutation with nothing to cancel.

3. **"Downstream" is the transitive `depends_on` closure.** (Q4.) Only this
   definition actually delivers "downstream starts fresh": if `C` consumes `B`
   consumes the retried `A`, re-running `A` must invalidate both `B` and `C`.
   Stopping at direct dependents would leave `C` built on a stale `B`. Because data
   edges *are* `depends_on` edges (ARCHITECTURE.md), the closure spares independent
   parallel branches. A reset step restarts from `Iteration = 0`, re-arming any loop
   it contains.

4. **The journal is immutable; the reset is an event; outputs are disposable.**
   (Q3, the user's key refinement.) `state = fold(journal)` and the append-only
   journal mean a silent file-delete would be undone by a later replay. So the reset
   is journaled as `StepsReset`, and only the **derived output** files
   (`result.json`, `output.md`, `output.json`, artifacts) of downstream steps are
   physically removed. Rejected the naive "delete the `steps/<id>/` dir" because it
   breaks never-truncate and desynchronizes disk from the fold.

5. **Transcripts are logs, kept and appended.** (Q3 sub-point; assumed, confirmable
   — see Open Questions.) A per-step `transcript.jsonl` is treated like the journal:
   never truncated. A reset downstream step keeps its prior transcript, and its
   re-run appends a fresh generation (bumped `attempt`/`iter`) — reusing the writer's
   monotonic-seq-on-reopen and the monitor's iteration separators.

6. **The retried step keeps its own history.** (Q5a.) Bump `Attempt` and append,
   exactly as `on_failure = "retry"` already does — so the retry itself is
   inspectable in the transcript rather than erased. Rejected wiping the target.

7. **Park-after-finish, not relaunch.** Because retry is scoped to live runs, the
   simplest correct mechanism is to keep the scheduler goroutine **alive after
   `RunFinished`**, parked on `inbox`, deferring teardown (journal close, worktree
   removal) to `Cancel`. Rejected relaunching a torn-down scheduler (would need to
   reopen the journal and recreate worktrees — the harder, disk-reconstruction path
   that Non-Goal 1 defers). Trade-off: worktrees and the open journal linger until
   cancel; recorded in the ADR.

8. **`r` with a blast-radius confirmation; none for the tip.** (Q5b.) Retry is
   destructive to downstream output, so a mid-graph retry confirms and names the
   count (defaulting to No). The empty-downstream case ("same/most-recent step") is
   not special-cased in logic — it simply has nothing to clear, so it skips the
   confirmation, matching the request's "don't worry about it" clause. `r` mirrors
   the recovery gate's retry verb for consistency.

## Open Questions

1. **Downstream transcript handling** is assumed to be *keep-and-append* (Decision
   5), consistent with the journal principle and never-truncate. If a literal clean
   slate is preferred (wipe downstream transcripts too), say so — it is a one-line
   change to `ClearStepOutputs` and does not affect any other requirement, acceptance
   criterion, or proof artifact.
2. **Worktree freshness for reset mutating downstream steps** (reuse vs. reset vs.
   recreate the worktree before the re-run) is a non-blocking implementation detail
   to settle during Unit 3; the user-facing behavior ("the step re-runs fresh") is
   fixed regardless.
3. **Snapshot publication under park-after-finish** — whether `Run.Snapshot()`
   publishes on each settle instead of once at exit — is an implementation detail of
   Unit 3, not a scope question.
