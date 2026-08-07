# 07-spec-stop-resume-step.md

> **Foundation B of the [`05-spec-run-integration-reset`](../05-spec-run-integration-reset/05-spec-run-integration-reset.md) mega-spec**, split into its own SDD workflow.
> This foundation builds **per-step stop and session-resume-as-continue**, so an operator can
> bring a single running step to a stop mid-run — leaving the run alive and **quiescent** — and
> then either let it continue or (via Feature C) reset. It is independent of Foundation A
> ([`06-spec-run-integration-branch`](../06-spec-run-integration-branch/06-spec-run-integration-branch.md));
> Feature C ([`08-spec-reset-to-step`](../08-spec-reset-to-step/08-spec-reset-to-step.md))
> depends on **both** A and B (it needs the run branch to rewind and quiescence to mutate
> safely). Read the parent mega-spec for the full cross-foundation rationale; this document is
> self-contained for implementation.

## Introduction/Overview

jig puts a deterministic orchestration layer around non-deterministic agents. Today **a run is a
one-shot forward traversal**: the only re-executions are engine-initiated (`on_failure =
"retry"`, the recovery gate, and `[step.loop]` back-edges), and cancellation is **run-level
only** — a single `context.WithCancel` at `internal/engine/engine.go:99`, shared by all workers
via `s.dispatch(ctx, …)`. There is no way for an operator to stop *one* step while the rest of
the run keeps going.

This foundation adds **per-step cancellation** (stop one running step without ending the run,
reaching a *quiescent* state — no worker in flight) and **session-resume-as-continue** (bring a
stopped step's agent session back up with a new message). Quiescence is the precondition Feature
C's reset needs: reset is a mid-run action on an unfinished, quiescent run, and stopping the
running step is how an operator reaches quiescence when the run isn't already parked at a gate.

Two engine facts shape the work:

1. **Cancellation is run-level.** Each worker needs its **own child context** derived from the
   run context so one worker can be cancelled independently, and diff/transcript capture must run
   **on cancel** (today the post-exec capture chain is skipped when the run ctx is done —
   `phCaptureWorktreeDiff`), or a stopped step loses its partial work.
2. **The session id is captured too late to resume.** Today the SDK session id is set only on the
   final `ResultMessage`, so a cancelled step ends with `SessionID == ""` and cannot resume. It
   must be captured **at session start** for resume to survive a mid-turn stop.

**Invariants preserved.** `state = fold(journal.jsonl)` with an append-only journal is unchanged;
`StepStatus` transitions remain journaled. Transcripts are logs and are never truncated — a stop
preserves the partial transcript, and a resume/continue appends to it. Persistence-off (`runDir
== ""`) stays a first-class no-op path.

## Goals

- **Let an operator stop a single running step** without cancelling the whole run, reaching a
  quiescent state from which they can resume the step or (via Feature C) reset the run.
- **Preserve a stopped step's partial work** — its worktree diff and partial transcript are
  captured on cancel, not discarded.
- **Let a stopped step continue its agent session** by resuming with a new message, reusing the
  existing resume machinery, and degrade gracefully to a fresh restart when no session id is
  available.
- **Keep determinism and the single-writer discipline.** Stop and resume are scheduler messages;
  the run stays alive and quiescent after a stop rather than tearing down.

## User Stories

**As an operator watching a step go off the rails**, I want to stop that one step (not the whole
run), so I can decide whether to let it continue or reset to an earlier step.

**As an operator who stopped a step by mistake**, I want to resume it and let its agent continue,
so I don't have to throw away the work it had already done.

## Demoable Units of Work

Dependency-ordered. Foundation B (Units B1–B2) builds stop/resume. Each unit is a candidate
implementation increment.

### Unit B1: Per-step cancellation

**Purpose:** Stop one running step without ending the run. Today cancellation is run-level only
(single `context.WithCancel` at `engine.go:99`, shared by all workers via `s.dispatch(ctx, …)`),
so this is new infrastructure.

**Functional Requirements:**
- The scheduler shall give each dispatched worker its **own child context** derived from the
  run context, tracked by step id, so one worker can be cancelled independently.
- The system shall add `Run.Stop(stepID string)` → `stopMsg{stepID}` handled in the
  single-writer `handle` switch; `handleStop` cancels that step's child context only. The run
  loop shall **not** treat a stopped step as end-of-run — the run stays alive and becomes
  quiescent (no worker in flight).
- On stop, the step's **partial worktree state and partial transcript shall be preserved** on
  disk (diff capture must run on cancel — today `phCaptureWorktreeDiff` is skipped when the run
  ctx is done; move capture to a defer or the worker's own exit path).
- The stopped step shall transition to a parked terminal-for-now status (proposed
  `StatusStopped`) that is eligible for both **resume** (B2) and **reset** (Feature C).

**Proof Artifacts:**
- Unit test (fake executor honoring ctx): dispatch `A` and `B` in parallel; `Run.Stop("A")`
  cancels only `A`'s context; `B` runs to completion; the run does not emit `RunFinished` due
  to the stop and stays alive.
- Unit test: a stopped mutating step's captured diff is non-empty (capture ran on cancel).
- Proof artifact: `07-proofs/B1.0-stop-one-step.txt`.

### Unit B2: Session capture at start + resume-as-continue

**Purpose:** Let a stopped step **continue** its agent session. Requires capturing the session id
at session *start* (today it is captured only on the final `ResultMessage`, so a cancelled step
ends with `SessionID == ""` and cannot resume).

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
- Proof artifact: `07-proofs/B2.0-resume-continues-session.txt`.
- Open dependency: verify against the pinned Claude Agent SDK whether the session id is
  available pre-`ResultMessage` (docs review found this undocumented; see Open Questions).

## Non-Goals (Out of Scope)

1. **Reset.** Reaching quiescence is this foundation's job; rewinding the run and re-running a
   dependency closure is Feature C ([`08-spec-reset-to-step`](../08-spec-reset-to-step/08-spec-reset-to-step.md)).
2. **Recovering the exact interrupted turn on resume.** Resume continues the session with a new
   message; the SDK does not expose mid-turn partial-turn recovery.
3. **Cancelling the whole run differently.** Run-level cancellation is unchanged; this adds a
   *per-step* stop alongside it.
4. **A workflow schema surface for stop/resume.** These are runtime/TUI actions, adding no
   per-step flag and no load-time validation.

## Design Considerations

Stop and resume add no new Gate surface of their own; the operator triggers them from the
Monitor's Steps region (the TUI trigger is specified with Feature C's Unit C4, which owns the
`stop`/`resume` key handling and footer hints). Footer hints advertise `stop`/`resume` only when
the run is quiescent and the selected step is eligible, following the mode-specific `footerView`
convention.

## Repository Standards

- **Engine extensibility, thin consumers (ADR 0003).** Stop and resume live in the scheduler —
  the single owner of state and the per-step contexts; the TUI only sends messages and renders.
- **Single-writer scheduler.** `Stop` and `Resume` are each one `inbox` message + one `handle`
  case — no locks.
- **File is truth, bus is liveness.** A stop preserves the partial worktree diff and transcript
  on disk; the bus carries only `StepStatus` liveness.
- **Persistence-off is first-class.** Diff/transcript capture no-ops when there is no run dir;
  the engine tests that run without a run dir must keep passing.
- **Tests are table-driven with inline fixtures**, with both the happy path and the guard path
  (e.g. stop of a non-running step, resume without a session id).
- **Comments explain the non-obvious "why"** — especially per-step cancellation, which reverses
  the single-run-context assumption at `engine.go:99`.

## Technical Considerations

- **Per-step cancellation is new.** Today `engine.go:99` is a single run context shared by all
  workers; each worker needs its own child context, and diff/transcript capture must run on cancel
  (today the post-exec chain is skipped when the run ctx is done).
- **The run stays alive after a stop.** The run loop must not interpret "no worker in flight" as
  end-of-run when a step was deliberately stopped; quiescence is a first-class resting state, not
  a terminal one.
- **Session id must be captured at start** for resume to survive a mid-turn stop; today it is
  only set on the final `ResultMessage`.
- **Resume reuses `WithResume`/`WithContinueConversation`** (`agent.go:51-56`) and continues with
  a new message; the SDK does not support recovering a mid-turn partial turn.
- **No schema, no load-time validation** is added.

## Security Considerations

- **Bounded operations.** Stop cancels only the targeted worker's context; capture writes only
  within the step's own worktree and transcript path, using datastore/worktree helpers — never a
  path from user input.
- **No new write to the user's branch.** Stop/resume operate entirely within the run's own
  worktrees and transcripts.

## Success Metrics

1. **Stop is surgical:** stopping one running step leaves siblings running and the run alive and
   quiescent (B1).
2. **Partial work preserved:** a stopped mutating step's captured diff and partial transcript are
   non-empty on disk (B1).
3. **Resume continues:** a stopped step with a captured session id resumes with the session set;
   without one it restarts fresh, documented, not errored (B2).
4. **No regressions:** `go build/vet/test ./...` pass including persistence-off paths.

## Design Decisions & Rationale

Resolved with the operator across the 2026-08-07 grilling session, grounded in read-only
exploration of `internal/engine` and `internal/runner`, plus a Claude Agent SDK docs review.

1. **Stop is per-step; resume continues the session, not the partial turn.** Per-step cancellation
   is new (today's context is run-level); resume reuses `WithResume`/`WithContinueConversation` but
   needs early session-id capture, and the SDK does not support recovering a mid-turn partial turn.
2. **Quiescence is a resting state, not end-of-run.** A stopped step must not trip the run's
   done-detection, so reset (Feature C) can act on an unfinished, quiescent run without racing a
   live worker — keeping the later reset a clean single-writer mutation.
3. **Resume degrades to a fresh restart** when no session id is available, rather than erroring —
   the honest behavior given the SDK limitation, and it keeps stop→reset unaffected either way.

## Open Questions

1. **SDK session-id timing (blocks B2's resume, not stop).** A docs review found no documented way
   to obtain the session id before the final `ResultMessage`, nor mid-turn partial-turn recovery.
   Verify against the pinned Go SDK whether the id is available at session creation / first stream
   event. If not, resume degrades to a fresh restart (documented), and stop (and therefore the
   quiescence Feature C needs) is unaffected.
