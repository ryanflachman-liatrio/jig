# 04 Questions Round 1 - Step Retry

Please answer each question below (check one or more options, or add your own
notes under **Your answer:**). Feel free to add context anywhere.

Context for all questions: today jig has no user-driven "retry this step" action.
The step list in the run monitor is read-only (`internal/tui/monitor.go`); the only
existing re-runs are **automatic** — `on_failure = "retry"` and the recovery gate
(`enterRecovery`/`handleRecover`, `engine.go:1095-1153`) both re-run a *failed*
step and never touch downstream steps — and **loop back-edges** (`fireLoop`,
`engine.go:1503-1568`), which reset a band of steps to `pending` with a bumped
`Iteration`. Your request ("retry *any* step; clear downstream so it starts
fresh") is a new, manual, unconditional capability. The answers below pin down its
edges before I write the spec.

---

## 1. Which runs can be retried — a live run, or also a finished run reopened from disk?

The engine keeps a live run's state in memory (`scheduler.states`) and exposes it
through a `Run` handle. A finished run from an earlier session has **no** live
handle — the monitor reconstructs it by replaying `journal.jsonl`
(`ReplayJournal`, `replay.go`). Retrying a finished run therefore means
re-instantiating a scheduler from that replayed state, which is a materially larger
lift than retrying a run jig is already executing.

- [ ] (A) **Live runs only** — the run must be one this jig session is currently
      executing (running, paused at a gate, or just finished but still in memory).
- [ ] (B) **Live *and* finished-from-disk runs** — reopen any past run in the
      monitor and retry a step, rebuilding the scheduler from the journal fold.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` is a complete, demoable vertical slice on top of the existing live
  `Run`/`scheduler` machinery — the new work is one scheduler message plus a
  downstream-reset routine, with no scheduler-resurrection problem.
- `(B)` layers cleanly on top of `(A)` as a follow-up spec once retry semantics are
  proven; doing it now couples this feature to reconstructing an executable
  scheduler from a replayed journal (worktrees, in-flight assumptions, session
  ids), roughly doubling the surface and the risk.
- If your primary use is reopening yesterday's run and re-running a step, say so and
  I'll scope for `(B)` directly — but I'd still recommend building `(A)` first.

**Your answer:** (A) **Live runs only.**

---

## 2. Which steps are eligible to retry, and must the run be idle first?

A step can be in many states (`pending`, `running`, `validating`,
`awaiting_review`, `needs_input`, `awaiting_recovery`, `succeeded`, `failed`,
`skipped` — `step/step.go:14-28`). Retrying a step while sibling steps are still
executing raises concurrency questions (a downstream step you're about to reset
might be mid-flight).

- [ ] (A) **Terminal steps only (`succeeded`/`failed`/`skipped`), and only when the
      run is quiescent** (no steps currently `running`/`validating`). Retry is
      blocked with a message while anything is in flight.
- [ ] (B) **Terminal steps, even while other steps run** — the engine cancels any
      in-flight downstream work as part of the reset.
- [ ] (C) **Any non-running step**, including ones parked at a human gate
      (`awaiting_review`/`needs_input`/`awaiting_recovery`).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` sidesteps the hardest concurrency case (cancelling and cleaning up a
  worker mid-execution) and matches the mental model of "go back and redo a step
  once things have settled." It keeps the reset a pure, single-writer state
  mutation on the scheduler goroutine.
- `(B)` is more powerful but needs cooperative cancellation of live workers plus
  worktree cleanup — a meaningful amount of the feature's risk for a case you may
  rarely hit. Best deferred unless you know you need it.
- `(C)`'s gate states already have their own resolution paths (verdict, answer,
  recovery); overloading retry onto them muddies two different interactions. A
  failed step parked at the recovery gate is already covered by the existing
  recovery retry.

**Your answer:** (A), refined: retry is available **only once the whole run is
done** (reached terminal `run_finished`) — not merely quiescent, and never
mid-run or while paused at a gate. Combined with Q1, the target is a **finished,
still-in-memory** run; from there you can reset to any terminal step. This
guarantees no workers are in flight during the reset.

---

## 3. When downstream output is cleared, should it be hard-deleted or superseded in place?

jig's durable model has two relevant invariants: **"file is truth, bus is
liveness"** and, for transcripts specifically, **"retries and loop iterations
*append* to the same `transcript.jsonl` — never truncate"** (distinguished by
`attempt`/`iter`; see `docs/run-monitor-transcript-plan.md`). Separately, run state
is reconstructable as **`state = fold(journal.jsonl)`**, and the journal is
append-only (`replay.go`). "Remove the output so downstream starts fresh" can be
honored two ways:

- [ ] (A) **Supersede, don't destroy.** Reset each downstream step's in-memory
      state to `pending` and record a **reset event** in the journal so a replay
      reconstructs the fresh state. On disk, leave the old
      `transcript.jsonl`/`output.*` in place; the re-run **appends** a new
      generation (bumped `attempt`/`iter`), and the monitor shows the latest. Stale
      `artifacts/` are overwritten on re-run. Nothing is truncated or deleted.
- [ ] (B) **Hard-delete.** Physically delete each downstream `steps/<id>/` directory
      and its `artifacts/`, and reset in-memory state. Simpler "clean slate," but it
      truncates history (against the transcript invariant) and a naive journal
      replay would still resurrect the deleted steps unless the journal is also
      rewritten.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Current best-practice context:** This is an internal-architecture choice, not an
external-standard one — no library guidance applies. The relevant "standard" is
jig's own documented invariants (append-only journal, never-truncate transcript),
which `(A)` upholds and `(B)` breaks.

**Why these are recommended:**

- `(A)` keeps the two load-bearing invariants intact: the journal stays append-only
  (a reset is just another event the fold understands), and transcripts keep their
  full history — you can still see *what the stale downstream run produced* before
  it was invalidated. The user-visible behavior ("downstream starts fresh") is
  identical; only the on-disk bookkeeping differs.
- `(B)` reads simpler ("just delete the folder") but fights the architecture: it
  breaks never-truncate, and because `state = fold(journal)`, deleting files without
  a corresponding journal change means reopening the run later would show the old
  downstream results again. Making `(B)` correct would require rewriting/compacting
  the journal — more work *and* a weaker guarantee than `(A)`.
- If you specifically want deleted runs to leave no on-disk trace (e.g. for
  sensitive output), note that — `jig prune` already exists for wholesale cleanup,
  and we could add a targeted purge, but I'd keep it separate from retry.

**Your answer:** A hybrid of (A) and (B), scoped precisely:

- The **`journal.jsonl` is append-only and immutable** — never deleted. The reset
  is recorded as a new event ("user requested reset to step X") so
  `state = fold(journal)` reconstructs the fresh state on reopen. (This is (A)'s
  journal handling.)
- Downstream steps' **result and output files are physically removed** so those
  steps start fresh: `result.json`, `output.md`, `output.json`, and the step's
  produced `artifacts/` files. (This is (B), scoped to derived outputs only — not
  the whole `steps/<id>/` dir and not the journal.)
- **Transcript reconciliation (to confirm):** the per-step `transcript.jsonl` is a
  *log*, not an output, so — mirroring the journal principle and jig's
  "transcripts never truncate" invariant — it is **kept**, and a downstream step's
  eventual re-run **appends** a fresh generation (bumped `attempt`/`iter`). Override
  if you'd rather wipe downstream transcripts for a literal clean slate.

---

## 4. What counts as "downstream" — and how do loops and conditional edges factor in?

"Downstream" needs a precise definition against the DAG.

- [ ] (A) **Transitive `depends_on` closure.** Every step reachable by following
      forward `depends_on` edges from the retried step — its direct dependents,
      their dependents, and so on. A downstream step inside a `[step.loop]` body is
      reset along with the rest; its loop simply gets a fresh chance to fire on
      re-run. Steps *not* reachable from the retried step (parallel branches that
      don't consume its output) are left untouched.
- [ ] (B) **Direct dependents only** (immediate consumers, not their dependents).
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` is the only definition that actually delivers "so any downstream steps
  start fresh": if `C` consumes `B` which consumes the retried `A`, then re-running
  `A` invalidates `B` *and* `C`. Stopping at direct dependents `(B)` would leave `C`
  holding output derived from a now-stale `B`.
- Restricting to the *reachable* set (not the whole graph after the step) correctly
  spares independent parallel branches, matching jig's rule that data edges are
  `depends_on` edges (`docs/ARCHITECTURE.md`).
- Loops: because a loop body is just steps with a back-edge, resetting the reachable
  set naturally re-arms any loop it contains. If you'd rather a retry *not* disturb
  a loop's accumulated iteration count in some case, flag it — otherwise I'll treat
  a reset step as starting from `Iteration = 0` again.

**Your answer:** (A) **Transitive `depends_on` closure**; reset steps restart from
`Iteration = 0`; independent parallel branches untouched.

---

## 5. What happens to the *retried* step itself, and how is retry triggered & confirmed?

Two coupled details about the target step and the UX.

**5a. The retried step's own history.** Your rule "if I'm rerunning the same step or
the most recent step, don't worry about [clearing downstream]" already tells me:
retrying the tip step just re-runs it. For the step's own record, the natural,
invariant-preserving choice is to **increment its `Attempt` and append** a new
generation to its transcript (never wipe its own history) — the same way
`on_failure = "retry"` already works.

- [ ] (A) **Increment `Attempt`, append** to the retried step's transcript (keep its
      prior attempts visible); reset and clear downstream per Q3.
- [ ] (B) **Wipe the retried step too** (fresh `Attempt = 0`, no prior history).
- [ ] (C) Other (describe)

**5b. Entry point & confirmation.** Retry is destructive to downstream results, so a
guard rail seems warranted.

- [ ] (A) In the monitor's step list, a keybinding (proposed **`r`**, mirroring the
      recovery gate's retry verb) on the selected step opens a **confirmation** that
      names how many downstream steps will be reset, then triggers the retry. No
      confirmation when nothing downstream is affected (retrying the tip).
- [ ] (B) Keybinding with **no** confirmation (immediate).
- [ ] (C) Other (describe — e.g. a different key, or a command-palette entry)

**Recommended answer(s):** [5a → (A), 5b → (A)]

**Why these are recommended:**

- `5a (A)` keeps the never-truncate invariant for the step you're deliberately
  re-running and reuses the existing `Attempt` mechanism and transcript tagging, so
  the monitor's iteration/attempt separators already render it. `(B)` throws away
  the very history that makes a retry inspectable.
- `5b (A)` makes the blast radius explicit *before* it happens ("this will reset 3
  downstream steps") — important because the action discards work. Suppressing the
  prompt when nothing is downstream keeps the common "re-run the tip" case
  friction-free, exactly matching your "don't worry about it" clause.
- `5b (B)` is faster but easy to fire by accident on the wrong step, destroying
  downstream output with no undo.

**Your answer (5a):** (A) **Increment `Attempt`, append** — never wipe the target
step's own history.

**Your answer (5b):** (A) **`r`** in the step list → confirmation naming how many
downstream steps will reset; **no** prompt when nothing is downstream (re-running
the tip).

---

### After you answer

Save the file and tell me you're done (e.g. "answers saved"). I'll re-check
sufficiency and, if it's clear, write the full spec at
`docs/specs/04-spec-step-retry/04-spec-step-retry.md`. If your answers open a new
material question, I'll add a Round 2 rather than guess.
