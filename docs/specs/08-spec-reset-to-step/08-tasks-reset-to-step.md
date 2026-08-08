# 08-tasks-reset-to-step.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/step/step.go` | Add `Generation int` to `step.State`; used by the scheduler to track manual re-run count. |
| `internal/transcript/transcript.go` | Add `Generation int` to `Entry` (JSON: `"gen"`) so re-runs are distinguishable in the transcript file. |
| `internal/engine/event.go` | Add `StepsReset` event type and `isEvent()` method. Add `Generation int` to `StepStatus`. |
| `internal/engine/journal.go` | Register `steps_reset` in `eventKind` and `decoders`; update `eventKind` switch for `StepsReset`. |
| `internal/engine/engine.go` | Add `closureOf`, `rewindPlan`, `resetMsg`, `Run.Reset`, `handleReset`; update `transition` to emit `Generation`; add `resetMsg` to `handle` switch and `isSchedMsg`. |
| `internal/datastore/datastore.go` | Add `ClearStepOutputs(runDir, stepID string) error` helper; removes `result.json`, `output.md`, `output.json` for a reset step. |
| `internal/tui/keys.go` | Add `StopStep`, `ResetStep`, `ResumeStep` bindings to `monitorKeys` and `defaultMonitorKeys()`. |
| `internal/tui/root.go` | Add `stopStepMsg`, `resetStepMsg`, `resumeStepMsg` types; route them in `rootModel.Update` to `run.Stop`, `run.Reset`, `run.Resume`. |
| `internal/tui/monitor.go` | Add key handling (stop/reset/resume) in Steps panel; add `inputKindResetConfirm` entry type; reset confirmation gate strip; `── re-run N ──` separator in `chatBody`; footer hints gated by eligibility. |
| `internal/engine/reset_test.go` | New file: table-driven unit tests for `closureOf`, `rewindPlan`, and the quiescence/settled guards. |
| `internal/engine/integration_test.go` | Add `TestResetFanOut`, `TestResetLinearTip` using a real git repo and `composeExec`. |
| `internal/engine/journal_test.go` | Add `TestStepsResetRoundTrip` for marshal/unmarshal and unknown-kind skip. |
| `internal/engine/replay_test.go` | Add `TestReplayPostReset` for synthesized journal with `steps_reset`. |
| `internal/tui/monitor_test.go` | Add `TestResetConfirmation`, `TestResetLinearTipTUI`, `TestStopKey`. |
| `docs/specs/08-spec-reset-to-step/08-proofs/C1.0-reset-plan.txt` | Proof artifact for Task 1.0. |
| `docs/specs/08-spec-reset-to-step/08-proofs/C2.0-reset-reexecutes-closure.txt` | Proof artifact for Task 3.0. |
| `docs/specs/08-spec-reset-to-step/08-proofs/C3.0-steps-reset-audit.txt` | Proof artifact for Task 2.0. |
| `docs/specs/08-spec-reset-to-step/08-proofs/C4.0-reset-confirmation.txt` | Proof artifact for Task 4.0. |

### Notes

- Unit tests for `internal/engine` live in the `engine` package (same package, no `_test` suffix on the package declaration). Integration tests that need a real git repo belong in `integration_test.go` (already the pattern for spec 06 work).
- Tests use `go test ./internal/engine -run TestXxx -v`; use `-race` for TUI/engine concurrency tests.
- Run `gofmt -l -w . && go vet ./...` before marking any task complete.
- Persistence-off (`runDir == ""`) must remain a clean no-op on every new code path.
- No schema changes; no new load-time validation rules; no `CLAUDE.md` or `examples/*.toml` updates needed.

## Tasks

### [x] 1.0 Dependency Closure and Rewind/Replay Plan (Unit C1)

Add two pure scheduler methods that compute everything needed for a reset
without touching any state: `closureOf(targetID)` returns the reset set (target
∪ its transitive `depends_on` closure in declaration order, excluding
independent branches), and `rewindPlan(targetID)` uses `stepCommits` to produce
the commit to `git reset --hard` to and the ordered list of survivor commits to
cherry-pick.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestResetClosure -v` passes — verifies `A→B→C` with independent `A→D`; `closureOf("A")=[A,B,C]` excludes `D`; `closureOf("C")=[C]`; `closureOf("D")=[D]`
- Test: `go test ./internal/engine -run TestRewindPlan -v` passes — verifies that with commit order `A,D,B` on the run branch, `rewindPlan("A")` yields rewind-to `base` and survivor-replay `[D]`; linear-tail reset yields empty replay
- CLI: `cat docs/specs/08-spec-reset-to-step/08-proofs/C1.0-reset-plan.txt` shows trimmed `-v` output for both tests

#### 1.0 Tasks

- [x] 1.1 Add `closureOf(targetID string) []string` method to `scheduler` in `internal/engine/engine.go`. Compute the transitive `depends_on` forward-reachability closure of `targetID` (target plus all steps that transitively depend on it), filtered to declaration order. Exclude steps not in the closure. Use the same forward-reachability iteration pattern as `loopBody`'s `fwd` set (engine.go:1981–1997) but without the backward intersection — this is a pure forward closure.
- [x] 1.2 Add `rewindPlan(targetID string) (rewindTo string, survivors []string)` method to `scheduler` in `internal/engine/engine.go`. Use `stepCommits` to find all commits belonging to the closure; find the earliest commit in the run branch's topological order (walk `git log` on `runWorktree` / use `stepCommitsFromLog` ordering); `rewindTo` is the parent of that earliest commit; `survivors` is every later commit on the run branch that is NOT in the closure (in original commit order). Return `("", nil)` when `runWorktree == ""` or no closure step has a commit.
- [x] 1.3 Create `internal/engine/reset_test.go`. Write `TestResetClosure` as a table-driven test: construct a `scheduler` with a fake workflow graph (no executor, no git) and assert `closureOf` returns the correct set for at least: fan-out exclusion (`A→B→C` with independent `A→D`), single-node self-closure, empty dependents (leaf node).
- [x] 1.4 Write `TestRewindPlan` in `internal/engine/reset_test.go`. Use a real `t.TempDir()` git repo (initialized via `initRepo` helper from `integration_test.go`) with scripted commits on the run branch tagged with `jig-step:` trailers. Assert: fan-out workflow with out-of-order commits (A committed first, then D, then B) → `rewidnPlan("A")` returns rewind-to = parent of A's commit and survivors = [D's sha]; linear tail → empty survivors; no-commit step (read-only step, no `stepCommits` entry) → `rewindTo` maps to just before the first downstream commit.
- [x] 1.5 Run `go test ./internal/engine -run "TestResetClosure|TestRewindPlan" -v 2>&1 | head -80` and save trimmed output to `docs/specs/08-spec-reset-to-step/08-proofs/C1.0-reset-plan.txt`.

---

### [x] 2.0 StepsReset Audit Event and Generation Provenance (Unit C3)

Add the data-model changes that Task 3.0's `handleReset` will use: `Generation
int` in `step.State` and `transcript.Entry`; `StepsReset` event in `event.go`;
`steps_reset` encoder/decoder in `journal.go`; `Generation` threaded through
`StepStatus` and `transition()`; and the `── re-run N ──` separator in
`chatBody()`.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestStepsResetRoundTrip -v` passes — `StepsReset` round-trips through `MarshalEnvelope`/`UnmarshalEnvelope`; an unknown-kind line decodes to a skipped event (not an error at the `UnmarshalEnvelope` level, which now must return `(env, nil, nil)` for unknown kinds per the `ReplayJournal` skip contract)
- Test: `go test ./internal/engine -run TestReplayPostReset -v` passes — a synthesized journal with `steps_reset` + subsequent `step_status` transitions survives `ReplayJournal` intact (no lines silently dropped for known kinds)
- Test: `go test ./internal/transcript -run TestGenerationField -v` passes — an `Entry` with `Generation: 2` marshals to `"gen":2` and back
- CLI: `cat docs/specs/08-spec-reset-to-step/08-proofs/C3.0-steps-reset-audit.txt` shows trimmed test output

#### 2.0 Tasks

- [x] 2.1 Add `Generation int` field to `step.State` in `internal/step/step.go`. Document it as the manual re-run counter (distinct from `Attempt` which gates `MaxRetries`).
- [x] 2.2 Add `Generation int` (JSON tag `"gen"`) to `transcript.Entry` in `internal/transcript/transcript.go`. Update the doc comment to mention `Generation` alongside `Attempt` and `Iteration`.
- [x] 2.3 Add `Generation int` to `StepStatus` in `internal/engine/event.go`. Add `StepsReset` struct with fields `RunID, Target string, Closure []string, RewindTo string` and its `isEvent()` method.
- [x] 2.4 Register `StepsReset` in `internal/engine/journal.go`: add `case StepsReset: return "steps_reset"` to `eventKind`; add a `"steps_reset"` entry to `decoders`. Also update `UnmarshalEnvelope` so that an unrecognised kind returns `(env, nil, nil)` (no error) rather than `(env, nil, err)` — this is required by `ReplayJournal`'s skip-on-error contract and matches the intent of "an unknown-kind line shall still be skipped."
- [x] 2.5 Update `transition()` in `internal/engine/engine.go` to set `ev.Generation = state.Generation` alongside `Attempt` and `Iteration`.
- [x] 2.6 Add `── re-run N ──` separator to `chatBody()` in `internal/tui/monitor.go`. Track `lastGen` alongside `lastIter` / `lastAttempt`; emit the separator when `e.Generation > lastGen && lastGen != -1`. Initialize `lastGen = -1` before the loop.
- [x] 2.7 Write `TestStepsResetRoundTrip` in `internal/engine/journal_test.go` — marshal a `StepsReset{RunID: "r1", Target: "A", Closure: []string{"A","B"}, RewindTo: "abc123"}` and unmarshal; assert all fields round-trip. Also decode a raw line with kind `"future_event"` and assert no error.
- [x] 2.8 Write `TestReplayPostReset` in `internal/engine/replay_test.go` — write a `journal.jsonl` with a `steps_reset` line followed by `step_status` lines; call `ReplayJournal`; assert the returned slice includes the `StepsReset` event and the subsequent `StepStatus` events with no gaps.
- [x] 2.9 Write `TestGenerationField` in `internal/transcript/transcript_test.go` — append an `Entry{Generation: 2, Role: RoleAssistant, ...}` via the writer, read it back via the reader, assert `Generation == 2`.
- [x] 2.10 Run `go test ./internal/engine -run "TestStepsResetRoundTrip|TestReplayPostReset" -v` and `go test ./internal/transcript -run TestGenerationField -v`; save trimmed output to `docs/specs/08-spec-reset-to-step/08-proofs/C3.0-steps-reset-audit.txt`.

---

### [x] 3.0 Reset Execution — Rewind, Replay, State Reset, Re-Dispatch (Unit C2)

Implement `Run.Reset(stepID)` → `resetMsg` → `handleReset` on the scheduler.
`handleReset` is guarded to unfinished-and-quiescent runs (`!s.terminated &&
s.inFlight == 0`); journals `StepsReset` and `StepStatus(→pending)` before any
destructive git/file operation; rewinds the run branch via `git reset --hard`
on `runWorktree`, cherry-picks survivors (conflict → routes to the integration
gate), calls `ClearStepOutputs` for each reset step, resets in-memory state
(Status=pending, Attempt/Iteration/Result zeroed, Generation bumped), and purges
stale routing maps. `ClearStepOutputs` is a new helper in `internal/datastore`.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/engine -run TestResetFanOut -v` passes — fan-out workflow (A→B→C + A→D) run to a quiescent gate; `Run.Reset("A")` triggers re-run of A/B/C (bumped `Generation`), D keeps its state and commit; run branch log excludes old A/B/C commits and retains D's
- Test: `go test ./internal/engine -run TestResetLinearTip -v` passes — linear A→B→C; `Run.Reset("C")` re-runs only C; A and B unaffected; run branch rewound by one commit
- Test: `go test ./internal/engine -run "TestResetGuard|TestResetPersistenceOff" -v` passes — settled run and in-flight worker each rejected; persistence-off is a clean no-op
- CLI: `cat docs/specs/08-spec-reset-to-step/08-proofs/C2.0-reset-reexecutes-closure.txt` shows ordered StepStatus events + `git log --oneline` before/after for the fan-out test

#### 3.0 Tasks

- [x] 3.1 Add `ClearStepOutputs(runDir, stepID string) error` to `internal/datastore/datastore.go`. Remove `result.json`, `output.md`, and `output.json` for the given step (using `os.Remove`; `os.IsNotExist` errors are silently swallowed). No-op and return `nil` when `runDir == ""`.
- [x] 3.2 Add `resetMsg{stepID string}`, `func (resetMsg) isSchedMsg() {}`, and `func (r *Run) Reset(stepID string) { r.inbox <- resetMsg{stepID: stepID} }` to `internal/engine/engine.go`. Add a `case resetMsg: s.handleReset(m)` to the `handle()` switch (after `resumeMsg`).
- [x] 3.3 Implement `func (s *scheduler) handleReset(m resetMsg)` in `internal/engine/engine.go`:
  - Guard: return silently if `s.terminated` or `s.inFlight > 0` or `s.runWorktree == ""`.
  - Compute closure via `s.closureOf(m.stepID)`; if empty (should not happen since target is always in its own closure) return.
  - Journal `StepsReset{RunID: s.runID, Target: m.stepID, Closure: closure, RewindTo: rewindTo}` via `s.emit`.
  - For each step in the closure: call `s.transition(stepID, currentStatus, step.StatusPending)` to journal `StepStatus(→pending)` before any git or file operation.
  - Compute `rewindTo, survivors := s.rewindPlan(m.stepID)`. If `rewindTo != ""`: run `gitCmd(s.runWorktree, "reset", "--hard", rewindTo)`. For each survivor sha: run `gitCmd(s.runWorktree, "cherry-pick", sha)`; on conflict detect via `mergeConflictPaths(s.runWorktree)` and route to `handleResolveIntegration` pattern (run `git cherry-pick --abort`, emit `IntegrationConflictRequest` for the cherry-pick step, park).
  - For each step in the closure: call `datastore.ClearStepOutputs(s.runDir, stepID)` (ignore errors — partial cleanup is acceptable).
  - For each step in the closure: reset in-memory `step.State` — `Status=pending` (already transitioned above), `Attempt=0`, `Iteration=0`, `Result=nil`, `Generation++`; delete from `s.stepCommits`.
  - Purge stale routing maps for every step in the closure: delete from `s.resumeSessions`, `s.stepMessage`, `s.stepFeedback`, `s.rerunSource`, `s.recoverCount`, `s.reviewMessages`, `s.stepInputCount`, `s.pendingUserInputs`, `s.collectedUserInputs`, `s.preResolvedInputs`, `s.stopping`.
  - Add a comment above the function explaining the before-destruction journaling order and the single-writer guarantee.
- [x] 3.4 Write `TestResetFanOut` in `internal/engine/integration_test.go` using `initRepo` + `composeExec`. Set up a fan-out workflow (A→B→C with A→D as an independent branch); run to a review gate at C (or use a quiescent stop via `Run.Stop`); call `Run.Reset("A")`; collect subsequent `StepStatus` events; assert A/B/C re-run with `Generation==1` and D remains at `Generation==0` with its original status; verify `git log --oneline` on the run worktree.
- [x] 3.5 Write `TestResetLinearTip` in `internal/engine/integration_test.go` — linear A→B→C; run to completion of C (stopped or gate); call `Run.Reset("C")`; assert only C re-runs with `Generation==1`; A and B unaffected; run branch has one fewer commit than before.
- [x] 3.6 Write `TestResetGuard` in `internal/engine/reset_test.go` — two sub-cases: (a) settled run (`s.terminated=true`): calling `Run.Reset` emits no events; (b) in-flight worker (`s.inFlight=1` via a blocking fake executor): calling `Run.Reset` emits no events. Use a `snapshotReqMsg` round-trip to confirm state is unchanged.
- [x] 3.7 Write `TestResetPersistenceOff` in `internal/engine/reset_test.go` — start a run with `runDir=""` (persistence-off path); call `Run.Reset` on a stopped step; assert no panic and no observable state change.
- [x] 3.8 Capture proof: from `TestResetFanOut` output, extract ordered StepStatus events and before/after `git log --oneline`; save to `docs/specs/08-spec-reset-to-step/08-proofs/C2.0-reset-reexecutes-closure.txt`.

---

### [x] 4.0 TUI — Stop, Reset Trigger, Confirmation, and Footer Hints (Unit C4)

Wire the operator surface. Add `stopStepMsg`, `resetStepMsg`, `resumeStepMsg`
in `root.go` and route them in `rootModel.Update` to `run.Stop`, `run.Reset`,
`run.Resume`. In `monitor.go`, add step-panel key handling: the stop key on a
running step emits `stopStepMsg`; `r` on a quiescent terminal/stopped step
either opens a `inputKindResetConfirm` gate entry (non-empty closure) or emits
`resetStepMsg` directly (empty closure); the resume key on a stopped step emits
`resumeStepMsg`. Footer hints in `focusSteps` advertise stop/r/resume only when
eligible.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestResetConfirmation -v` passes — `r` on a mid-graph terminal step (quiescent run) opens the confirmation gate with the correct downstream count; `y` emits `resetStepMsg` via `tea.Batch`; `n`/`esc` clears the entry and emits nothing
- Test: `go test ./internal/tui -run "TestResetLinearTipTUI|TestStopKey" -v` passes — `r` on a linear-tip step emits `resetStepMsg` immediately; `r` on a settled run emits nothing; stop key on a running step emits `stopStepMsg`; stop on a non-running step is a no-op
- CLI: `cat docs/specs/08-spec-reset-to-step/08-proofs/C4.0-reset-confirmation.txt` shows trimmed test output

#### 4.0 Tasks

- [x] 4.1 Add `stopStepMsg{runID, stepID string}`, `resumeStepMsg{runID, stepID, message string}`, and `resetStepMsg{runID, stepID string}` to `internal/tui/root.go` (alongside the existing `recoverResponseMsg` and `resolveIntegrationResponseMsg` types).
- [x] 4.2 Add routing cases in `rootModel.Update` in `internal/tui/root.go` for the three new message types: `stopStepMsg → run.Stop(msg.stepID)`, `resumeStepMsg → run.Resume(msg.stepID, msg.message)`, `resetStepMsg → run.Reset(msg.stepID)`. Guard each with `if run, ok := m.handles[msg.runID]; ok { ... }`, mirroring the pattern at `root.go:283-299`.
- [x] 4.3 Add `StopStep`, `ResumeStep`, `ResetStep` bindings to the `monitorKeys` struct in `internal/tui/keys.go`. Initialize them in `defaultMonitorKeys()`: stop key = `s` with help `"s stop"`, resume key = `ctrl+r` with help `"ctrl+r resume"`, reset key = `r` with help `"r reset"`. (These three are in the Steps panel `focusSteps` branch; `r` is distinct from the gate-focused `RecoverRetry` binding which is only matched during `focusGate`.)
- [x] 4.4 Add `inputKindResetConfirm` to the `pendingInputKind` enum in `internal/tui/monitor.go`. Add a `resetConfirm *resetConfirmEntry` field to `pendingInputEntry` (non-nil only for `inputKindResetConfirm`). Define `resetConfirmEntry{targetID string; closure []string}` (a small struct in monitor.go).
- [x] 4.5 In `monitorModel.Update` in `internal/tui/monitor.go`, add key handling inside the `focusSteps` branch (after existing `Up`/`Down`/`OpenTranscript` handling):
  - `StopStep`: if selected step's `status == step.StatusRunning` and run is not done, return `tea.Batch(cmd, tea.Cmd(func() tea.Msg { return stopStepMsg{runID: m.runID, stepID: selectedID} }))`.
  - `ResetStep`: if run is not done, compute closure via calling `closureOf` logic inline (or add a helper `closureOf` on `monitorModel` that replicates the DAG walk from the workflow snapshot — use `RunSnapshot.Steps` to reconstruct). If closure is empty (linear tip or target with no dependents), emit `resetStepMsg` immediately. Otherwise, push a `pendingInputEntry{kind: inputKindResetConfirm, stepID: selectedID, resetConfirm: &resetConfirmEntry{...}}` onto `m.inputQueue` and focus the gate.
  - `ResumeStep`: if selected step's `status == step.StatusStopped`, emit `resumeStepMsg{runID: m.runID, stepID: selectedID, message: ""}`.
- [x] 4.6 Add `inputKindResetConfirm` handling to the gate strip (`gateStrip()`) and `Update` key handling (`focusGate` branch) in `internal/tui/monitor.go`. The gate shows: `"Reset to {targetID}? This will re-run {N} step(s): {ids} [y/n, default n]"`. `y` key → emit `resetStepMsg{runID: m.runID, stepID: entry.resetConfirm.targetID}` and remove the entry from the queue. `n`/`esc` → remove the entry, no msg.
- [x] 4.7 Update `footerView` in `internal/tui/monitor.go` for the `focusSteps` default branch: use `SetEnabled` on `StopStep`, `ResetStep`, `ResumeStep` based on the selected step's eligibility — stop only when `status == running && !done`; reset only when `!done && inFlight==0` and step is terminal/stopped; resume only when `status == stopped`. Include these keys in `hintString(...)` for the steps footer.
- [x] 4.8 Write `TestResetConfirmation` in `internal/tui/monitor_test.go`. Build a `monitorModel` with a synthetic run snapshot (fan-out workflow: A→B→C and A→D, run quiescent at C which is terminal). Send `tea.KeyPressMsg{Code: tea.KeyRunes, Text: "r"}` with C selected; assert the model's `inputQueue` has an `inputKindResetConfirm` entry with closure `[C]` (no dependents from C, so this is a linear tip — adjust test to select A instead for non-empty closure); send `y`; assert the returned `tea.Cmd` fires `resetStepMsg`.
- [x] 4.9 Write `TestResetLinearTipTUI` and `TestStopKey` in `internal/tui/monitor_test.go`. Linear-tip `r` → `resetStepMsg` immediately. Settled run `r` → no msg. Stop key on running step → `stopStepMsg`. Stop key on pending step → no msg.
- [x] 4.10 Run `go test ./internal/tui -run "TestResetConfirmation|TestResetLinearTipTUI|TestStopKey" -v 2>&1 | head -80`; save trimmed output to `docs/specs/08-spec-reset-to-step/08-proofs/C4.0-reset-confirmation.txt`.
