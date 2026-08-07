# 05-spec-run-integration-reset.md

> **Mega-spec — SPLIT (parent index, not an active SDD target).** This document spans three
> dependency-ordered foundations (A → B → C) plus a docs unit (D). On 2026-08-07 it was split,
> per-foundation, into four self-contained SDD workflows; run those, not this one. This file is
> kept as the parent: the canonical home for the cross-foundation rationale, ADR links, and open
> questions. The children:
>
> - **Foundation A → [`06-spec-run-integration-branch`](../06-spec-run-integration-branch/06-spec-run-integration-branch.md)** (Units A1–A3)
> - **Foundation B → [`07-spec-stop-resume-step`](../07-spec-stop-resume-step/07-spec-stop-resume-step.md)** (Units B1–B2)
> - **Feature C → [`08-spec-reset-to-step`](../08-spec-reset-to-step/08-spec-reset-to-step.md)** (Units C1–C4)
> - **Unit D → [`09-spec-docs-examples-adrs`](../09-spec-docs-examples-adrs/09-spec-docs-examples-adrs.md)**
>
> Dependencies: A and B are independent; C depends on both; D depends on A, B, and C.
>
> This mega-spec **supersedes** [`04-spec-step-retry`](../04-spec-step-retry/04-spec-step-retry.md),
> whose finished-run, park-after-finish premise was inverted during the 2026-08-07 grilling session.

## Introduction/Overview

jig puts a deterministic orchestration layer around non-deterministic agents. Today the
part of that layer that handles **code changes between steps** is too thin for real
multi-step engineering workflows, and there is **no way for an operator to rewind a run**
when an agent produces the wrong thing.

Two facts about the current engine, verified against the code, frame this spec:

1. **Steps cannot build on each other's code.** Each mutating step runs in its own git
   worktree branched off the **repo-root HEAD** (`internal/engine/engine.go:720-742`,
   `worktree.go:23-37`), so `implement` never sees the code `plan`'s predecessor wrote.
   Steps exchange only structured `@ref` output; code lands on the user's branch only via
   an explicit `merge` **command** step an author wires by hand (`examples/bugfix.toml`).
2. **A run is a one-shot forward traversal.** The only re-executions are engine-initiated
   (`on_failure = "retry"`, the recovery gate, and `[step.loop]` back-edges). A finished
   run tears itself down (journal closed, worktrees removed —
   `internal/engine/engine.go:498-517`). There is no operator "go back to step X."

The operator need that motivated this work: *"I made it through planning and review, the
implementation isn't what I wanted, and I want to reset the workflow back to the planning
phase — undoing both the workflow state **and** the code changes."* Delivering that
honestly requires three changes, in dependency order:

- **Foundation A — Run-integration branch.** A per-run branch into which each step's
  changes are squash-merged (one commit per step, tagged with the step id); each step's
  worktree branches off the run branch's current HEAD, so steps compose on code;
  integration conflicts surface through a human gate; one final human-gated merge lands the
  run branch on the user's branch. (ADR 0007.)
- **Foundation B — Stop / resume a step.** Per-step interruption (the run stays alive and
  becomes quiescent) and session-resume-as-continue, so an operator can bring a running
  step to a stop mid-run and then either reset or let it continue.
- **Feature C — Reset to a step.** The operator rewinds the run branch to before the
  target step's dependency closure, replays the survivor commits, returns the closure to
  `pending`, and re-runs — the manual, generalized mirror of a `[step.loop]` back-edge.
  (ADR 0008.)

C depends on A (there is nothing to rewind without the run branch's per-step commits) and
on B (to reach quiescence mid-run). Retry as spec 04 imagined it — a finished-run action
built on keeping the scheduler parked after `RunFinished` — is **retired**; reset is a
mid-run action on an **unfinished, quiescent** run, and a fully-settled run is **locked**.

**Invariants preserved.** `state = fold(journal.jsonl)` with an append-only journal
(`internal/engine/replay.go`): the reset is a journaled event, not a silent mutation.
`StepStatus` transitions are journaled (`journal.go`, kind `step_status`), so a replay
already reconstructs post-reset state from the status stream — the `StepsReset` event is
therefore an **audit** record of operator intent, not a replay-correctness crutch.
Transcripts are logs and are never truncated; a re-run **appends** a new generation.
Persistence-off (`runDir == ""`) stays a first-class no-op path.

## Goals

- **Let steps build on each other's code** via a per-run integration branch, with each
  step contributing exactly one squash commit, keeping each step's turn isolated in its own
  worktree.
- **Let an operator stop a single running step** without cancelling the whole run, reaching
  a quiescent state from which they can reset or resume.
- **Let an operator reset a run to any earlier step** on an unfinished, quiescent run —
  rewinding the run branch to before the target's transitive `depends_on` closure, replaying
  independent survivors, and re-running the closure fresh.
- **Keep determinism and the durable record.** No new graph edges, no new termination risk;
  the reset is a journaled audit event written before any destructive operation; conflicts
  are human-resolved through the existing Gate, never auto-resolved.
- **Land the run once, under human control** — a single gated merge of the run branch onto
  the user's working branch, replacing hand-wired `merge` command steps.

## User Stories

**As an operator whose implementation is wrong**, I want to reset the run back to the
planning step, so the plan, the implementation, and everything downstream re-run fresh and
the wrong code is unwound — without discarding the whole run.

**As an operator watching a step go off the rails**, I want to stop that one step (not the
whole run), so I can decide whether to let it continue or reset to an earlier step.

**As an operator resetting one branch of a parallel fan-out**, I want the *other* parallel
branches' code and results kept, so I only pay to re-run what actually depended on the step
I reset.

**As an author of a code-writing workflow**, I want downstream steps to see the code
upstream steps wrote, so I can chain `plan → implement → test` without hand-wiring merges.

**As an operator at the end of a run**, I want to approve one final merge of the run's work
onto my branch, so integration is explicit and under my control.

**As someone auditing a run later**, I want the reset recorded — which step I targeted and
what it invalidated — so the history honestly shows the intervention.

## Demoable Units of Work

Dependency-ordered. Foundation A (Units A1–A3) builds the integration model. Foundation B
(Units B1–B2) builds stop/resume. Feature C (Units C1–C4) builds reset on top of A and B.
Unit D aligns docs. Each unit is a candidate SDD workflow.

### Foundation A — Run-integration branch

#### Unit A1: Run branch + step worktrees off run HEAD + squash-per-step integration

**Purpose:** Replace "each step off repo-root HEAD" with "each step off the run branch's
current HEAD, squash-merged back as one commit." The core of code composition.

**Functional Requirements:**
- On run start (when `repoRoot != ""`), the system shall create a **run branch** at the
  user's working-branch HEAD and a run worktree, recorded on the scheduler.
- A mutating step's worktree (`isolation = "worktree"`; auto-enabled per
  `internal/workflow/load.go:134` + `mutatingTools`) shall branch off the **run branch's
  current HEAD at dispatch time**, not repo-root HEAD. Read-only steps get no worktree.
- On a step's successful completion, the system shall **squash-merge that step's worktree
  branch into the run branch as exactly one commit**, whose message is tagged with the step
  id (proposed trailer `jig-step: <stepID>`), advancing the run branch HEAD by one commit.
- The system shall maintain a **step → commit map** (`stepCommits[stepID] = sha`) on the
  scheduler, so a step's contribution is addressable later (Unit C1/C2). The map shall be
  reconstructable from the run branch history via the `jig-step` trailer.
- Parallel dispatch shall still be honored, but **integration is serialized** in a
  deterministic order (declaration order) so the run branch has a single linear history.
- Persistence-off / non-git (`repoRoot == ""`) shall remain a no-op path: no run branch, no
  worktrees, steps run in place, integration is skipped.

**Proof Artifacts:**
- Unit test: `A → B` where `A` writes `file_a`, `B` reads it and writes `file_b`; assert
  `B`'s worktree contains `file_a` (it branched off the run branch after `A` integrated),
  and the run branch ends with two commits tagged `jig-step: A`, `jig-step: B`.
- Unit test: read-only step produces no commit; the run branch HEAD is unchanged across it.
- Proof artifact: `05-proofs/A1.0-run-branch-log.txt` — `git log --format=%s` of the run
  branch showing one tagged commit per mutating step.

#### Unit A2: Integration-conflict gate

**Purpose:** When a squash-merge (A1) hits a conflict — two steps touched the same lines —
surface it to a human instead of failing silently or auto-resolving.

**Functional Requirements:**
- On an integration conflict, the system shall create a new Gate **input entry** kind
  (proposed `inputKindIntegrationConflict`) naming the step and the conflicted paths, and
  transition the step to a parked state; the run stays alive (non-blocking gate, ADR 0002).
- The operator resolves the conflict in the run worktree, then signals completion through
  the Gate; the engine finishes the integration and continues. An abort path shall fail the
  step (routing to the existing recovery gate).
- The conflict resolution shall be a single-writer scheduler message + handler, mirroring
  `Run.Resolve`/`handleRecover` (`engine.go` inbox pattern).

**Proof Artifacts:**
- Unit test: two parallel steps writing the same file line; the second integration raises
  the gate; resolving it lands a merged commit; the run completes.
- TUI test: the integration-conflict entry renders in the Gate with the conflicted paths and
  takes focus like other entries.
- Proof artifact: `05-proofs/A2.0-integration-conflict-gate.txt`.

#### Unit A3: Final gated merge (run branch → user branch)

**Purpose:** Land the run's work once, under human control; retire hand-wired `merge`
command steps.

**Functional Requirements:**
- At run end (all steps terminal), the system shall present a **final merge gate**: merge the
  run branch onto the user's working branch, or discard. On approve, jig performs the merge;
  on discard, the run branch is left for inspection and nothing lands.
- The `examples/*.toml` workflows that hand-merge step branches (e.g. `bugfix.toml`) shall be
  updated to drop the explicit `merge` command step; `jig validate` shall still exit 0.

**Proof Artifacts:**
- Unit test: after a run, approving the final merge fast-forwards/merges the run branch onto
  the base and the base HEAD contains the run's commits; discarding leaves the base untouched.
- Proof artifact: `05-proofs/A3.0-final-merge.txt` — base branch log before/after approve.

### Foundation B — Stop / resume a step

#### Unit B1: Per-step cancellation

**Purpose:** Stop one running step without ending the run. Today cancellation is run-level
only (single `context.WithCancel` at `engine.go:99`, shared by all workers via
`s.dispatch(ctx, …)`), so this is new infrastructure.

**Functional Requirements:**
- The scheduler shall give each dispatched worker its **own child context** derived from the
  run context, tracked by step id, so one worker can be cancelled independently.
- The system shall add `Run.Stop(stepID string)` → `stopMsg{stepID}` handled in the
  single-writer `handle` switch; `handleStop` cancels that step's child context only. The run
  loop shall **not** treat a stopped step as end-of-run — the run stays alive and becomes
  quiescent (no worker in flight).
- On stop, the step's **partial worktree state and partial transcript shall be preserved**
  on disk (diff capture must run on cancel — today `phCaptureWorktreeDiff` is skipped when the
  run ctx is done; move capture to a defer or the worker's own exit path).
- The stopped step shall transition to a parked terminal-for-now status (proposed
  `StatusStopped`) that is eligible for both **resume** (B2) and **reset** (C).

**Proof Artifacts:**
- Unit test (fake executor honoring ctx): dispatch `A` and `B` in parallel; `Run.Stop("A")`
  cancels only `A`'s context; `B` runs to completion; the run does not emit `RunFinished` due
  to the stop and stays alive.
- Unit test: a stopped mutating step's captured diff is non-empty (capture ran on cancel).
- Proof artifact: `05-proofs/B1.0-stop-one-step.txt`.

#### Unit B2: Session capture at start + resume-as-continue

**Purpose:** Let a stopped step **continue** its agent session. Requires capturing the
session id at session *start* (today it is captured only on the final `ResultMessage`, so a
cancelled step ends with `SessionID == ""` and cannot resume).

**Functional Requirements:**
- The agent runner shall capture the SDK **session id as early as the SDK exposes it** (at
  session creation / first stream event) and record it on the step state, so a step stopped
  mid-turn still has a resumable session id.
- The system shall add a resume path (`Run.Resume(stepID)`), reusing the existing
  `resumeSessions` + `WithResume` + `WithContinueConversation` machinery (`agent.go:51-56`):
  resume **continues the conversation with a new message**, it does **not** recover the exact
  interrupted turn (an SDK limitation — see Technical Considerations).
- If the SDK cannot surface a session id at cancel time, resume shall **degrade to a fresh
  restart** of the step (documented behavior, not an error).

**Proof Artifacts:**
- Unit test: a stopped step with a captured session id resumes with `ResumeSessionID` set on
  the dispatch request; without one, it restarts fresh.
- Proof artifact: `05-proofs/B2.0-resume-continues-session.txt`.
- Open dependency: verify against the pinned Claude Agent SDK whether the session id is
  available pre-`ResultMessage` (docs review found this undocumented; see Open Questions).

### Feature C — Reset to a step

#### Unit C1: Dependency closure + commit selection

**Purpose:** The pure core — given the workflow and a target, compute the reset set and the
git rewind/replay plan. No engine mutation yet.

**Functional Requirements:**
- The system shall compute the **reset set** = target ∪ its transitive `depends_on` closure,
  in declaration order, excluding independent parallel branches (reuse the closure logic
  analogous to `loopBody`'s forward reachability, `engine.go:1570-1616`, but forward-only).
- Given the step → commit map (A1), the system shall produce a **rewind/replay plan**: the
  commit to `git reset --hard` to (just before the earliest reset-set commit on the run
  branch), and the ordered list of **survivor commits** after that point that are *not* in the
  reset set (to cherry-pick). In a linear reset set this list is empty.
- All git paths shall use the run branch and jig's worktree helpers only — never a path from
  untrusted input.

**Proof Artifacts:**
- Unit test (closure): `A → B → C` with independent `A → D`; `closure(A) = [B, C]` excludes
  `D`; `closure(C) = []`.
- Unit test (plan): with commit order `A,D,B` on the run branch, `reset(A)` yields rewind-to
  `base` and survivor-replay `[D]`; `reset` of a linear tail yields empty replay.
- Proof artifact: `05-proofs/C1.0-reset-plan.txt`.

#### Unit C2: Reset execution (rewind + replay + state reset + re-dispatch)

**Purpose:** Apply the plan on a live, quiescent run and re-run the closure.

**Functional Requirements:**
- The system shall add `Run.Reset(stepID)` → `resetMsg{stepID}`, handled single-writer as
  `handleReset`, guarded to run **only when the run is unfinished and quiescent** (no worker
  in flight, not settled — Q: reuse `inFlight == 0` plus a not-settled check).
- `handleReset` shall, in order: **(1)** journal the `StepsReset` audit event (C3) and emit
  `StepStatus(→pending)` for the reset set *before* any destructive op; **(2)** `git reset
  --hard` the run branch to the plan's rewind point and cherry-pick the survivor commits (a
  conflict routes to the integration-conflict gate, A2); **(3)** `ClearStepOutputs` for each
  reset-set step (per-step `result.json`/`output.md`/`output.json` only — see Non-Goals on
  artifacts); **(4)** reset each reset-set step's in-memory state to `pending`, `Attempt = 0`,
  `Iteration = 0`, `Result = nil`, and **bump `Generation`**; clear per-step scheduler maps
  (`resumeSessions`, `stepMessage`/`stepFeedback`, `rerunSource`, `recoverCount`,
  pre-resolved/collected/pending user inputs) for the reset set so no stale routing survives.
- After `handleReset`, the main loop shall re-dispatch normally; the target's untouched
  upstream keeps it ready, it re-runs off the rewound run branch, and its downstream follow.
- Independent survivor steps shall keep their state, result, transcript, and commit exactly.

**Proof Artifacts:**
- Unit test: `A → B → C` and independent `A → D` run to a quiescent gate; `Run.Reset("A")`
  re-runs `A`, `B`, `C` (with bumped `Generation`), keeps `D` (state + survivor commit), and
  the run branch no longer contains the old `A/B/C` commits but still contains `D`'s.
- Unit test (linear tip): `Run.Reset("C")` re-runs only `C`; no other step changes; empty
  survivor replay; run branch rewound by one commit.
- Unit test (guard): `Run.Reset` on a settled run, or while a worker is in flight, is a
  rejected no-op with an observable reason.
- Proof artifact: `05-proofs/C2.0-reset-reexecutes-closure.txt` — ordered status events + run
  branch log before/after.

#### Unit C3: `StepsReset` audit event + Generation provenance

**Purpose:** Record the operator's intent durably and make the re-run legible.

**Functional Requirements:**
- The system shall add `StepsReset{RunID, Target string, Closure []string, RewindTo string}`
  to `internal/engine/event.go` with `isEvent()`, a journal kind `"steps_reset"`, and
  envelope encode/decode in `journal.go` (mirroring `LoopFired`/`run_finished`); it shall
  round-trip through `MarshalEnvelope`/`UnmarshalEnvelope` and an unknown-kind line shall still
  be skipped by `ReplayJournal`.
- The event carries the target, the ordered closure, and the rewind commit — provenance the
  `StepStatus` stream cannot express. It is **not** required for state reconstruction (the
  journaled `StepStatus(→pending→…)` transitions already fold to the post-reset state); no
  state-changing fold handler is added (there is none for `LoopFired` either).
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
- Proof artifact: `05-proofs/C3.0-steps-reset-audit.txt`.

#### Unit C4: TUI — stop, reset trigger, confirmation, rendering

**Purpose:** The human surface — stop a step, then reset to a selected step, with a
confirmation that names the blast radius.

**Functional Requirements:**
- In the Monitor's Steps region, when the run is **unfinished and quiescent**, pressing a
  **stop** key on a running step emits `stopStepMsg`; pressing **`r`** on a selected terminal
  step emits a reset request.
- If the target's closure is **non-empty**, `r` opens a confirmation entry naming the count and
  ids of downstream steps to be reset (default **No**); on confirm, emit `resetStepMsg{stepID}`.
  If the closure is **empty** (linear tip), emit `resetStepMsg` immediately, no confirmation.
- `rootModel.Update` shall route `stopStepMsg → run.Stop`, `resetStepMsg → run.Reset`,
  `resumeStepMsg → run.Resume` (mirroring `reviewVerdictMsg → run.Resolve` /
  `recoverResponseMsg → run.Recover`, `root.go:242-277`).
- The step list shall reflect resets promptly via the `StepStatus` events from C2/C3. Footer
  hints shall advertise `stop`/`r`/`resume` only when eligible (the `SetEnabled` pattern in
  `footerView`).

**Proof Artifacts:**
- TUI test: `r` on a mid-graph terminal step (run quiescent) opens the confirmation with the
  correct downstream count; `y` emits `resetStepMsg`; `n`/`esc` emits nothing.
- TUI test: `r` on a linear tip emits `resetStepMsg` with no confirmation; `r` on a settled
  run or a non-quiescent run emits nothing and shows a hint; `stop` on a running step emits
  `stopStepMsg`.
- Proof artifact: `05-proofs/C4.0-reset-confirmation.txt`.

### Unit D: Docs, examples, ADRs

**Functional Requirements:**
- `docs/ARCHITECTURE.md` shall document the run-integration-branch model (run branch, step
  worktrees off run HEAD, squash-per-step, the step→commit map) and the reset seam
  (rewind + survivor replay), replacing the per-step-isolation description.
- `docs/workflow-schema.md` shall note that explicit `merge` command steps are no longer the
  integration mechanism (the final gated merge is), and that reset/stop are runtime/TUI
  actions adding **no** workflow schema surface (`jig validate examples/feature.toml` exits 0).
- `docs/engine-design.md` shall describe stop (per-step context) and the reset flow.
- ADRs [0007](../../adr/0007-run-integration-branch-model.md) (integration model) and
  [0008](../../adr/0008-manual-reset-rewind-and-replay.md) (reset algorithm) are recorded;
  link them from any ADR index.

**Proof Artifacts:**
- `go build ./...`, `go vet ./...`, `go test ./...` pass, including persistence-off paths.
- `05-proofs/D.0-docs-and-adr.md` — the doc sections added and examples updated.

## Non-Goals (Out of Scope)

1. **Resetting a finished, settled run.** Reset is only for an unfinished, quiescent run;
   a settled run is locked. Reopening a run from disk to reset it is a deferred follow-up
   (it needs an executable scheduler rebuilt from the journal fold).
2. **Recovering the exact interrupted turn on resume.** Resume continues the session with a
   new message; the SDK does not expose mid-turn partial-turn recovery.
3. **Auto-resolving integration or cherry-pick conflicts.** Overlapping changes are
   human-resolved through the Gate; jig never guesses whose version wins.
4. **Undoing an already-final-merged run from the user's branch.** Once the final gated merge
   lands the run branch on the user's branch, that is history the user owns; reset operates on
   the run branch, not on the user's committed history.
5. **Per-step artifact GC.** `ClearStepOutputs` clears only the per-step derived outputs under
   `steps/<id>/` (`result.json`/`output.md`/`output.json`), which are unambiguously owned. The
   shared run-level `artifacts/` dir has no per-step ownership record; stale artifact files are
   overwritten on re-run, as they already are for loop/recovery re-runs.
6. **A workflow schema surface for reset/stop.** No per-step `resettable` flag; these are
   runtime/TUI actions, adding no load-time validation.
7. **Partial or selective reset.** Reset invalidates the entire transitive closure or nothing.

## Design Considerations

The new visual surfaces are the **integration-conflict gate**, the **final merge gate**, and
the **reset confirmation** — all reuse the existing Gate/entry pattern and precedence (ADR
0002), so they inherit focus handling and styling. The reset confirmation must state the blast
radius plainly and default to No. Footer hints advertise `stop`/`r`/`resume` only when the run
is quiescent and the selected step is eligible, following the mode-specific `footerView`
convention.

## Repository Standards

- **Engine extensibility, thin consumers (ADR 0003).** Integration, stop, and reset live in
  the scheduler — the single owner of state, the DAG, the run branch, and the journal writer;
  the TUI only sends messages and renders.
- **Single-writer scheduler.** Every new action (`Stop`, `Resume`, `Reset`, conflict resolve)
  is one `inbox` message + one `handle` case — no locks.
- **File is truth, bus is liveness.** Reset mutates the run branch and per-step outputs and
  journals an audit event; the bus carries only `StepStatus` liveness.
- **Persistence-off / non-git is first-class.** Every git and disk operation no-ops when there
  is no run dir / no repo root; the engine tests that run without them must keep passing.
- **Tests are table-driven with inline fixtures**, and new engine behavior gets both the happy
  path and the guard path.
- **Comments explain the non-obvious "why"** — especially per-step cancellation (reversing the
  single-run-context assumption) and the rewind/replay reset.

## Technical Considerations

- **Serialized integration over a parallel DAG.** Steps still run in parallel, but their
  squash-merges into the run branch are serialized in declaration order to keep a single linear
  history — the property that makes reset addressable by commit.
- **Reset = rewind + replay, computed once, applied atomically on the scheduler goroutine.**
  The journaled `StepStatus(→pending)` transitions and the `StepsReset` audit event are written
  **before** the `git reset`/cherry-pick and the `ClearStepOutputs`, so a crash leaves the
  journal (pending, no expected output) consistent with the deleted files.
- **`StepStatus` is journaled**, so replay reconstructs post-reset state from the status stream;
  `StepsReset` is audit-only and needs no state-changing fold handler.
- **Per-step cancellation is new.** Today `engine.go:99` is a single run context shared by all
  workers; each worker needs its own child context, and diff/transcript capture must run on
  cancel (today the post-exec chain is skipped when the run ctx is done).
- **Session id must be captured at start** for resume to survive a mid-turn stop; today it is
  only set on the final `ResultMessage`.
- **`Generation` reuses the `Attempt`/`Iteration` plumbing** through the transcript writer,
  `Entry`, `StepStatus`, and the monitor separators — a third provenance axis, gating no budget
  (unlike `Attempt`, which gates `MaxRetries`).
- **No schema, no load-time validation** is added; the "schema additions are exhaustive/load-time"
  rule does not apply.

## Security Considerations

- **Bounded git and file operations.** Reset operates only on the run branch and jig's own
  worktrees, and `ClearStepOutputs` only within `.jig/runs/<id>/steps/<stepID>/`, using datastore
  and worktree helpers exclusively — never a path from user input.
- **The final merge is the only write to the user's branch, and it is human-gated.** Nothing
  lands on the user's working history without an explicit approval.
- **Confirmation guards destruction.** A mid-graph reset names its blast radius and defaults to
  No; conflicts pause for a human rather than being auto-resolved.
- **Auditability.** `StepsReset` records the target, the closure, and the rewind point, so a
  run's history reflects operator intervention rather than silently dropping work.

## Success Metrics

1. **Code composes:** in `A → B`, `B`'s worktree contains `A`'s changes, and the run branch has
   one tagged commit per mutating step (A1).
2. **Stop is surgical:** stopping one running step leaves siblings running and the run alive and
   quiescent (B1).
3. **Reset unwinds correctly:** resetting an upstream step re-runs its transitive closure with a
   bumped `Generation`, rewinds the run branch past exactly those commits, and preserves
   independent survivor branches' state, transcript, and commits (C1–C3).
4. **Records preserved:** `journal.jsonl` and every `transcript.jsonl` are strictly append-only;
   only per-step `result.json`/`output.*` of the closure are deleted; folding the post-reset
   journal reconstructs the live state (C2, C3).
5. **Conflicts are human-owned:** an overlapping integration or cherry-pick raises the gate and
   resolves cleanly; it is never auto-resolved (A2, C2).
6. **One gated landing:** the run's work reaches the user's branch only via the approved final
   merge (A3).
7. **No regressions:** `go build/vet/test ./...` pass including persistence-off paths, and
   `jig validate examples/feature.toml` exits 0 (no schema change).

## Design Decisions & Rationale

Resolved with the operator across the 2026-08-07 grilling session, grounded in read-only
exploration of `internal/engine`, `internal/datastore`, `internal/transcript`,
`internal/runner`, `internal/workflow`, and `internal/tui`, plus a Claude Agent SDK docs review.

1. **Reset is mid-run on an unfinished, quiescent run; a settled run is locked.** (Inverts spec
   04's finished-run premise.) This deletes park-after-finish, `RunResumed`, the done-latch fix,
   and the lingering-worktree footprint. Quiescence is reached at a gate or by stopping the
   running step — so reset never races a live worker, keeping it a clean single-writer mutation.
2. **Steps compose on code via a per-run integration branch, squash-per-step (ADR 0007).** The
   per-step-isolation-off-repo-HEAD model cannot deliver "downstream builds on upstream code,"
   which is the actual requirement. Squash-per-step gives the addressable history reset needs.
3. **Reset = git rewind + survivor replay over the transitive `depends_on` closure (ADR 0008).**
   A bare `git reset --hard` by time over-reverts independent parallel branches; replaying
   survivors spares exactly the branches that don't depend on the target. Linear workflows
   degenerate to a plain rewind.
4. **The reset is a journaled *audit* event, not a replay crutch.** `StepStatus` is journaled, so
   the fold already reconstructs post-reset state; `StepsReset` records operator intent (target +
   blast radius) and is ordered before destructive ops for crash-consistency. (Corrects spec 04's
   rationale, which wrongly assumed replay would resurrect stale results.)
5. **`Generation`, not `Attempt`, marks a manual re-run.** The auto-retry cap is `state.Attempt <
   maxRetries` (`engine.go:1070`), so bumping `Attempt` would steal the budget; `Generation` is a
   third provenance axis that gates nothing and makes the transcript re-run legible.
6. **Integration and cherry-pick conflicts are human-resolved via the Gate.** jig cannot statically
   prevent two agents from writing the same file, so a runtime resolution path is the honest answer;
   a dedicated gate keeps it deterministic and inspectable.
7. **One final human-gated merge lands the run branch**, replacing hand-wired `merge` command steps
   — integration becomes an engine concern with a single explicit approval.
8. **Stop is per-step; resume continues the session, not the partial turn.** Per-step cancellation
   is new (today's context is run-level); resume reuses `WithResume`/`WithContinueConversation` but
   needs early session-id capture, and the SDK does not support recovering a mid-turn partial turn.

## Open Questions

1. **SDK session-id timing (blocks B2's resume, not stop or reset).** A docs review found no
   documented way to obtain the session id before the final `ResultMessage`, nor mid-turn
   partial-turn recovery. Verify against the pinned Go SDK whether the id is available at session
   creation / first stream event. If not, resume degrades to a fresh restart (documented), and
   stop→reset is unaffected.
2. **Run-branch naming and lifetime.** Proposed `jig/<workflow>/run-<runID>`; confirm the naming and
   whether the run branch is deleted after the final merge or kept for inspection.
3. **Read-only steps and reset granularity.** Read-only steps produce no commit, so resetting *to* a
   read-only step maps to the run-branch state as of just before its downstream mutating commits;
   confirm the exact mapping when the reset set has no commits of its own.
4. **Squash vs. preserve step commits.** Squash-per-step is settled (one addressable commit per
   step); if a future need arises to inspect an agent's intermediate commits, that is a separate
   change to the integration step, not to reset.
