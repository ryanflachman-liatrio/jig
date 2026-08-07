# 07-tasks-stop-resume-step.md

Task list for **[`07-spec-stop-resume-step`](./07-spec-stop-resume-step.md)** —
per-step stop (reach quiescence without ending the run) and
session-resume-as-continue. Foundation B of the
[`05` mega-spec](../05-spec-run-integration-reset/05-spec-run-integration-reset.md);
independent of Foundation A (`06`); a prerequisite for Feature C (`08`).

> Parent tasks only. Sub-tasks and the Relevant Files table are generated after
> explicit user confirmation (Phase 3). Do not begin implementation from this
> draft.

## Requirement → Parent Task coverage

| Spec requirement (unit) | Parent task |
| --- | --- |
| Per-worker child context, tracked by step id (B1) | 1.0 |
| `Run.Stop` → `stopMsg` → `handleStop` cancels only that worker (B1) | 1.0 |
| Run stays alive & quiescent after a stop, not end-of-run (B1) | 1.0 |
| Preserve partial worktree diff + transcript on cancel (B1) | 2.0 |
| `StatusStopped` parked terminal-for-now status (B1) | 2.0 |
| Capture SDK session id at session start (B2) | 3.0 |
| `Run.Resume(stepID)` reuses `WithResume`/`WithContinueConversation` (B2) | 4.0 |
| Degrade to fresh restart when no session id (B2) | 4.0 |
| No regressions incl. persistence-off; build/vet/test (Success Metric 4) | 5.0 |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/engine/engine.go` | Scheduler/run loop. Single run ctx at `:99`; worker dispatch (`:851-960`, ctx passed to `s.exec.Execute` at `:957`); `s.reporters[st.ID]` + `s.inFlight` tracking; single-writer `handle` switch (`:1063-1200`); scheduler message set (`:290-375`); loop-exit / `RunFinished` detection (`:614-626`); resume stashing (`:946-951`). Add per-step cancel-func map, `stopMsg`/`resumeMsg` + `handleStop`/`handleResume`, and quiescence-not-end-of-run logic here. |
| `internal/engine/engine_test.go` | Engine test suite. `testExec` ctx-aware fake (`:71-96`); `TestScheduler_Cancel` (`:523-559`); persistence on/off setup (`:1571-1667`). Add `TestStopOneStep`, `TestStopNonRunningStep`, `TestStoppedStepCapturesDiff`, `TestStopPersistenceOff`, `TestResumeContinuesSession`, `TestResumeWithoutSessionRestarts`. |
| `internal/engine/handlers.go` | Post-exec chain (`phCaptureWorktreeDiff` at `:22-29`, decision states). Capture only runs on the `stepDoneMsg` success path today, so it is skipped on cancel — factor the diff/transcript capture so a cancelled worker still runs it. |
| `internal/engine/executor.go` | `StepRequest` struct (`:18-44`) already carries `ResumeSessionID` + `Message`. No new fields expected; confirm resume dispatch reuses them. |
| `internal/engine/recovery_test.go` | `recoveringExec` (`:19-52`) captures re-dispatch `StepRequest`s to assert `ResumeSessionID`; the parked-but-alive assertion pattern (`:79-150`) is the template for the "run stays alive after stop" test. |
| `internal/runner/agent.go` | `AgentExecutor`. Resume plumbing `WithResume`/`WithContinueConversation` (`:51-56`, `:76-87`); session id captured only on `ResultMessage` (`:298,304`) — move capture to session start / first stream event. |
| `internal/runner/agent_test.go` | Runner tests. Add `TestSessionIDCapturedAtStart` (interrupt before `ResultMessage`, assert non-empty `SessionID`). |
| `internal/runner/fake.go` | `FakeExecutor` (`:49-82`) honors `ctx.Done()`; usable to drive stop tests that need scripted deltas before cancel. |
| `internal/step/step.go` | Status constants (`:14-33`) and `State`/`Result` (`:35-59`, `Result.SessionID`). Add `StatusStopped`; ensure it stringifies and is eligible for resume/reset. |
| `internal/step/step_test.go` | Add/extend a table case asserting `StatusStopped` string value and journaled transition (create the file if absent, following the workflow-test template). |
| `07-proofs/B1.0-stop-one-step.txt` | Proof artifact: stop-one-step test output (A cancelled, B finished, run alive). |
| `07-proofs/B1.0-partial-work-preserved.txt` | Proof artifact: non-empty captured diff for a stopped mutating step. |
| `07-proofs/B2.0-session-id-at-start.txt` | Proof artifact: captured session id from a stopped step + SDK-timing note. |
| `07-proofs/B2.0-resume-continues-session.txt` | Proof artifact: resume-with-id continues, resume-without-id restarts. |
| `07-proofs/B.regression.txt` | Proof artifact: `gofmt`/`vet`/`test -race`/`validate` clean output. |

### Notes

- Tests live beside the code (`engine_test.go`, `agent_test.go`, `step_test.go`) and are **table-driven with inline fixtures**; run under `-race` for the engine concurrency work (`docs/TESTING.md`).
- No live model calls in unit tests — drive `testExec`/`FakeExecutor`/`recoveringExec` fakes that honor `ctx.Done()`.
- Every writer must no-op when `runDir == ""` / `TranscriptPath == ""` (persistence-off first-class path).
- `Stop`/`Resume` are each **one `inbox` message + one `handle` case, no locks** (single-writer discipline); comments must explain the non-obvious "why" of reversing the single-run-ctx assumption at `engine.go:99`.
- No workflow schema field and no load-time validation is added (Non-Goal 4).

## Tasks

### [x] 1.0 Per-step cancellation: child contexts + `Stop` reaching quiescence

Reverse the single-run-context assumption at `internal/engine/engine.go:99`. Give
each dispatched worker its own child context tracked by step id, add
`Run.Stop(stepID)` → `stopMsg{stepID}` handled in the single-writer `handle`
switch (`handleStop` cancels only that step's context), and ensure the run loop
does **not** treat a deliberately-stopped step as end-of-run — the run stays
alive and becomes quiescent (no worker in flight, no `RunFinished`).

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestStopOneStep -v` passes — a fake
  executor honoring `ctx.Done()`; dispatch `A` and `B` in parallel; `Run.Stop("A")`
  cancels only `A`'s context; `B` runs to completion; the run emits **no**
  `RunFinished` from the stop and stays alive. Demonstrates surgical per-step stop.
- Test: `go test ./internal/engine -run TestStopNonRunningStep -v` passes —
  guard path: `Stop` of a step with no in-flight worker is a no-op (or documented
  error), not a panic. Demonstrates the guard-path handling.
- Proof artifact file: `07-proofs/B1.0-stop-one-step.txt` — captured output of the
  stop test showing `A` cancelled, `B` finished, run alive.

### [x] 2.0 Preserve partial work on cancel + `StatusStopped`

Move diff/transcript capture so it runs **on cancel** — today `phCaptureWorktreeDiff`
and the post-exec capture chain are skipped when the run ctx is done, so a stopped
step loses its partial work; move capture to a `defer`/worker-exit path. Introduce
`step.StatusStopped` (journaled transition) as the parked terminal-for-now status,
eligible for both resume (4.0) and reset (Feature C). Persistence-off (`runDir == ""`)
must remain a graceful no-op.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestStoppedStepCapturesDiff -v` passes —
  a stopped mutating step's captured worktree diff is **non-empty** on disk
  (capture ran on cancel). Demonstrates partial work is preserved, not discarded.
- Test: `go test ./internal/engine -run TestStopPersistenceOff -v` passes —
  with `runDir == ""` the cancel-capture chain no-ops without error. Demonstrates
  the persistence-off first-class path is intact.
- Test: `go test ./internal/step -v` passes — asserts `StatusStopped` exists,
  stringifies, and journals as a valid transition. Demonstrates the new status.
- Proof artifact file: `07-proofs/B1.0-partial-work-preserved.txt` — output
  showing a non-empty captured diff for a stopped step.

### [x] 3.0 Capture the SDK session id at session start

Capture the SDK session id **as early as the SDK exposes it** (session creation /
first stream event) and record it on the step state, replacing today's capture on
the final `ResultMessage` — so a step stopped mid-turn still carries a resumable
session id. Resolves Open Question 1: verify against the pinned Claude Agent SDK
whether the id is available pre-`ResultMessage`; if not, document that resume will
degrade to a fresh restart (handled in 4.0).

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/runner -run TestSessionIDCapturedAtStart -v` passes —
  a step interrupted before its `ResultMessage` has a **non-empty** `SessionID` on
  its recorded state. Demonstrates the id survives a mid-turn stop.
- Proof artifact file: `07-proofs/B2.0-session-id-at-start.txt` — output showing
  a captured session id from a stopped step, plus a one-line note recording the
  SDK-timing finding (id available at start / only at result → restart fallback).

### [x] 4.0 `Run.Resume(stepID)`: resume-as-continue with fresh-restart fallback

Add `Run.Resume(stepID)` as one `inbox` message + one `handle` case (single-writer,
no locks). Reuse the existing resume machinery — `resumeSessions` + `WithResume` +
`WithContinueConversation` at `internal/runner/agent.go:51-56` — to **continue the
conversation with a new message** (not recover the interrupted turn — an SDK
limitation). When no session id is available, resume degrades to a **fresh restart**
of the step (documented behavior, not an error).

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestResumeContinuesSession -v` passes —
  a stopped step **with** a captured session id resumes with `ResumeSessionID` set
  on the dispatch request. Demonstrates resume-as-continue.
- Test: `go test ./internal/engine -run TestResumeWithoutSessionRestarts -v`
  passes — guard path: a stopped step **without** a session id dispatches a fresh
  restart (no `ResumeSessionID`), no error. Demonstrates the documented fallback.
- Proof artifact file: `07-proofs/B2.0-resume-continues-session.txt` — output of
  both resume cases (with-id continues, without-id restarts).

### [x] 5.0 Regression sweep: build, vet, full test incl. persistence-off

Confirm Success Metric 4: no regressions across the engine/runner/step packages
and the persistence-off paths. Run the repo quality gates (`gofmt`, `go vet`,
`go build`, `go test ./...`, `-race` for the concurrency-touching engine work) and
validate the examples still parse.

#### 5.0 Proof Artifact(s)

- CLI: `gofmt -l .` prints nothing and `go vet ./...` exits 0. Demonstrates format
  and vet are clean.
- CLI: `go test ./... -race -count=1` passes. Demonstrates no regressions and no
  data races in the new per-step context/worker-tracking code.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0. Demonstrates the
  kitchen-sink example still validates (no schema surface was added, per Non-Goal 4).
- Proof artifact file: `07-proofs/B.regression.txt` — captured output of the
  test/vet/validate run.

#### 1.0 Tasks

- [x] 1.1 Add a per-step cancel registry to the scheduler: a `map[string]context.CancelFunc`
  keyed by step id (mutated only in the single-writer `handle`/dispatch path, alongside
  `s.reporters`). Document *why* this reverses the single-run-ctx assumption at `engine.go:99`.
- [x] 1.2 In the worker dispatch path (`engine.go:851-960`), derive a child context per worker
  with `context.WithCancel(runCtx)`, store its cancel func in the registry keyed by step id, and
  pass the child ctx (not the run ctx) to `s.exec.Execute(childCtx, req, rep)` at `:957`. Delete
  the registry entry when the worker's `stepDoneMsg` is handled.
- [x] 1.3 Add a `stopMsg{stepID string}` implementing `isSchedMsg()` (with the other messages at
  `:290-375`) and a `Run.Stop(stepID string)` method that sends it on the inbox (mirror
  `Run.Cancel`).
- [x] 1.4 Add a `handleStop` case to the `handle` switch (`:1063-1200`): look up the step's cancel
  func, call it (guard: no-op when the step id is absent / not in flight — see 1.6), and leave all
  other workers untouched. Do **not** call `s.cancel()` (the run-level cancel).
- [x] 1.5 Ensure the run loop's done-detection (`:614-626`) treats a deliberately-stopped step as
  quiescent, not end-of-run: reaching zero in-flight workers must **not** emit `RunFinished` when
  the reason a worker left was a stop. Follow the parked-but-alive pattern used by
  `StatusAwaitingRecovery`.
- [x] 1.6 Handle the guard path: `Stop` of a step with no in-flight worker (pending, already
  terminal, or unknown id) is a documented no-op, never a panic.
- [x] 1.7 Add `TestStopOneStep` (two parallel `testExec` steps A+B; `Run.Stop("A")` cancels only A;
  B completes; no `RunFinished` from the stop; snapshot shows run alive) and `TestStopNonRunningStep`
  (guard no-op). Run `go test ./internal/engine -run 'TestStop' -race -v`.
- [x] 1.8 Capture `07-proofs/B1.0-stop-one-step.txt` from the passing stop test output.

### [x] 2.0 Preserve partial work on cancel + `StatusStopped`

Move diff/transcript capture so it runs **on cancel** — today `phCaptureWorktreeDiff`
and the post-exec capture chain are skipped when the run ctx is done, so a stopped
step loses its partial work; move capture to a `defer`/worker-exit path. Introduce
`step.StatusStopped` (journaled transition) as the parked terminal-for-now status,
eligible for both resume (4.0) and reset (Feature C). Persistence-off (`runDir == ""`)
must remain a graceful no-op.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestStoppedStepCapturesDiff -v` passes —
  a stopped mutating step's captured worktree diff is **non-empty** on disk
  (capture ran on cancel). Demonstrates partial work is preserved, not discarded.
- Test: `go test ./internal/engine -run TestStopPersistenceOff -v` passes —
  with `runDir == ""` the cancel-capture chain no-ops without error. Demonstrates
  the persistence-off first-class path is intact.
- Test: `go test ./internal/step -v` passes — asserts `StatusStopped` exists,
  stringifies, and journals as a valid transition. Demonstrates the new status.
- Proof artifact file: `07-proofs/B1.0-partial-work-preserved.txt` — output
  showing a non-empty captured diff for a stopped step.

#### 2.0 Tasks

- [x] 2.1 Add `StatusStopped = "stopped"` to `internal/step/step.go` (`:14-33`) with a comment that
  it is a parked terminal-for-now status eligible for resume and reset; keep the string stable
  (journal/UI depend on it).
- [x] 2.2 In `handleStop` (from 1.4), transition the stopped step to `StatusStopped` via the
  scheduler's `transition()` path so the `StepStatus` event and journal record are emitted
  consistently.
- [x] 2.3 Refactor `phCaptureWorktreeDiff` (`handlers.go:22-29`) and transcript finalization so the
  capture runs on the worker's exit path (a `defer` in the worker goroutine or a cancel-aware
  branch) rather than only on the `stepDoneMsg` success chain — so a cancelled worker still
  captures its partial diff before the step parks as `StatusStopped`.
- [x] 2.4 Preserve persistence-off: every capture writer must short-circuit when
  `req.Worktree == ""` / `TranscriptPath == ""` / `runDir == ""`; no error, no write.
- [x] 2.5 Add `TestStoppedStepCapturesDiff` (a mutating fake writes a file, gets stopped mid-run;
  assert the captured diff on disk is non-empty) and `TestStopPersistenceOff` (`NewManager(exec, "")`;
  stop; assert no error and no write attempted).
- [x] 2.6 Add/extend `internal/step/step_test.go` with a table case asserting `StatusStopped`'s
  string value and that it is accepted as a journaled transition.
- [x] 2.7 Add `TestStoppedStepTranscriptAppends` — a stopped step's partial `transcript.jsonl`
  is preserved (never truncated), and a later resume/continue **appends** to it rather than
  rewriting it (spec invariant: "a resume/continue appends to it").
- [x] 2.8 Capture `07-proofs/B1.0-partial-work-preserved.txt` from the passing diff test.

### [x] 3.0 Capture the SDK session id at session start

Capture the SDK session id **as early as the SDK exposes it** (session creation /
first stream event) and record it on the step state, replacing today's capture on
the final `ResultMessage`, so a step stopped mid-turn still carries a resumable
session id. Resolves Open Question 1: verify against the pinned Claude Agent SDK
whether the id is available pre-`ResultMessage`; if not, document that resume will
degrade to a fresh restart (handled in 4.0).

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/runner -run TestSessionIDCapturedAtStart -v` passes —
  a step interrupted before its `ResultMessage` has a **non-empty** `SessionID` on
  its recorded state. Demonstrates the id survives a mid-turn stop.
- Proof artifact file: `07-proofs/B2.0-session-id-at-start.txt` — output showing
  a captured session id from a stopped step, plus a one-line note recording the
  SDK-timing finding (id available at start / only at result → restart fallback).

#### 3.0 Tasks

- [x] 3.1 Inspect the pinned Claude Agent SDK (`github.com/severity1/claude-agent-sdk-go`) message
  stream to determine the earliest message carrying the session id (init/system message or first
  stream event). Record the finding in `07-proofs/B2.0-session-id-at-start.txt`.
- [x] 3.2 In `internal/runner/agent.go`, set `res.SessionID` the first time the SDK surfaces a
  session id (before the loop that reads to `ResultMessage`), keeping the existing final-message
  capture (`:298,304`) as a fallback/overwrite. Report the id to the engine early enough that a
  cancelled worker's recorded state carries it.
- [x] 3.3 If the SDK does **not** expose the id pre-`ResultMessage`, add a comment documenting that
  a mid-turn stop yields `SessionID == ""` and resume will degrade to a fresh restart (4.0), and
  keep behavior correct either way.
- [x] 3.4 Add `TestSessionIDCapturedAtStart` to `internal/runner/agent_test.go` using a fake SDK
  stream that emits an early session id then is interrupted before `ResultMessage`; assert the
  recorded `SessionID` is non-empty (skip/guard-document if the SDK cannot supply it early).
- [x] 3.5 Capture `07-proofs/B2.0-session-id-at-start.txt` with the test output and the SDK-timing note.

### [x] 4.0 `Run.Resume(stepID)`: resume-as-continue with fresh-restart fallback

Add `Run.Resume(stepID)` as one `inbox` message + one `handle` case (single-writer,
no locks). Reuse the existing resume machinery — `resumeSessions` + `WithResume` +
`WithContinueConversation` at `internal/runner/agent.go:51-56` — to **continue the
conversation with a new message** (not recover the interrupted turn — an SDK
limitation). When no session id is available, resume degrades to a **fresh restart**
of the step (documented behavior, not an error).

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestResumeContinuesSession -v` passes —
  a stopped step **with** a captured session id resumes with `ResumeSessionID` set
  on the dispatch request. Demonstrates resume-as-continue.
- Test: `go test ./internal/engine -run TestResumeWithoutSessionRestarts -v`
  passes — guard path: a stopped step **without** a session id dispatches a fresh
  restart (no `ResumeSessionID`), no error. Demonstrates the documented fallback.
- Proof artifact file: `07-proofs/B2.0-resume-continues-session.txt` — output of
  both resume cases (with-id continues, without-id restarts).

#### 4.0 Tasks

- [x] 4.1 Add a `resumeMsg{stepID string, message string}` implementing `isSchedMsg()` and a
  `Run.Resume(stepID string)` method that sends it on the inbox.
- [x] 4.2 Add a `handleResume` case: assert the step is `StatusStopped` (guard: no-op/documented
  error otherwise), then stash the resume info using the existing pattern at `engine.go:946-951`
  (`s.resumeSessions[stepID] = state.Result.SessionID`, `s.stepMessage[stepID] = message`) and
  re-dispatch the step, reusing the child-context registry from 1.x.
- [x] 4.3 When `state.Result.SessionID == ""`, re-dispatch **without** setting `ResumeSessionID`
  (fresh restart) — verify `StepRequest.ResumeSessionID`/`Message` remain empty so `agent.go:51`
  takes the full-prompt path; add a comment that this is the documented SDK-limitation fallback.
- [x] 4.4 Confirm no new `StepRequest` fields are needed (`ResumeSessionID`/`Message` already exist
  at `executor.go:18-44`) and that the resumed run stays alive and re-enters quiescence when the
  resumed worker later stops or completes.
- [x] 4.5 Add `TestResumeContinuesSession` and `TestResumeWithoutSessionRestarts` using
  `recoveringExec`-style capture of the re-dispatched `StepRequest` to assert `ResumeSessionID` is
  set (with id) / empty (without id). Run under `-race`.
- [x] 4.6 Add `TestStopResumeStopReentersQuiescence` — stop a step, resume it, then stop the
  resumed worker again; assert the run stays alive and re-enters quiescence each time (no
  `RunFinished` from either stop). Proves resume is not a one-shot terminal transition.
- [x] 4.7 Capture `07-proofs/B2.0-resume-continues-session.txt` from both passing resume tests.

### [x] 5.0 Regression sweep: build, vet, full test incl. persistence-off

Confirm Success Metric 4: no regressions across the engine/runner/step packages
and the persistence-off paths. Run the repo quality gates (`gofmt`, `go vet`,
`go build`, `go test ./...`, `-race` for the concurrency-touching engine work) and
validate the examples still parse.

#### 5.0 Proof Artifact(s)

- CLI: `gofmt -l .` prints nothing and `go vet ./...` exits 0. Demonstrates format
  and vet are clean.
- CLI: `go test ./... -race -count=1` passes. Demonstrates no regressions and no
  data races in the new per-step context/worker-tracking code.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0. Demonstrates the
  kitchen-sink example still validates (no schema surface was added, per Non-Goal 4).
- Proof artifact file: `07-proofs/B.regression.txt` — captured output of the
  test/vet/validate run.

#### 5.0 Tasks

- [x] 5.1 Run `gofmt -l -w .` and `go vet ./...`; fix any findings introduced by the change.
- [x] 5.2 Run `go build ./cmd/jig` and `go test ./... -race -count=1`; confirm all packages pass,
  including the persistence-off engine tests.
- [x] 5.3 Run `go run ./cmd/jig validate examples/feature.toml` (and any other `examples/*.toml`);
  confirm they still validate — no schema field was added.
- [x] 5.4 Capture `07-proofs/B.regression.txt` with the combined gofmt/vet/test/validate output.
