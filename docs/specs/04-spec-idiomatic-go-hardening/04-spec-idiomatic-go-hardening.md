# 04-spec-idiomatic-go-hardening.md

## Introduction/Overview

A senior-Go code review of the whole tree (2026-08-07) found that the codebase is
structurally healthy and idiomatic — a single-writer engine, consumer-side
interfaces, a sealed event union, "file is truth / bus is liveness," a pure
`internal/step`, and a deliberately broken `manifest → engine` cycle. The risk is
concentrated where the complexity lives: `internal/engine`, whose scheduler
goroutine has absorbed too much. It runs **blocking git/shell/file I/O inline on
the one goroutine that owns all run state**, has a worker send with **no
cancellation-guarded exit** (a latent goroutine leak), and carries **sixteen
parallel maps keyed by step id** that make per-step state hard to reason about and
extend. Smaller idiomatic gaps exist in `internal/workflow` (scattered enum
switches, one non-`%w` error path, tests asserting on `err.Error()`) and
`internal/tui` (one missing polymorphic seam), plus a few cross-cutting duplications.

This spec turns those findings into a **behavior-preserving hardening pass**. It
changes *how* the code is organized and *how safely* the scheduler runs concurrent
work — it does **not** change what a workflow does, what a run produces, or any
on-disk / on-wire format. Because there is no user-facing behavior change, the
acceptance evidence is test-based: every existing test in `go test ./...` must keep
passing (the regression lock), plus a small set of **new** tests that prove the
specific defects are gone (a leak test, a non-blocking-scheduler test, an
event-serialization exhaustiveness test, and a `validate` CLI check).

The work is grouped into seven dependency-ordered Demoable Units. **Units 1–3 are
the three HIGH engine findings** and carry almost all the value; each is
independently shippable. Units 4–7 are the MEDIUM/LOW idiomatic cleanups, ordered
so the engine settles before the peripheral packages. The reviewer recommended
splitting this into 2–3 specs; the user chose a single tracking spec, so the unit
boundaries below are the manageable increments (see [Open Questions](#open-questions)).

Source findings and their code anchors are preserved in the
[Design Decisions & Rationale](#design-decisions--rationale) log; this spec is the
single source of truth for the work.

## Goals

- **Make the engine scheduler a truly non-blocking single-writer actor.** No git,
  shell, or filesystem call blocks the scheduler goroutine; slow work (gate
  commands, worktree creation, diff capture) runs in workers and posts typed
  results back to the inbox, so `max_parallel` and human input stay responsive
  while one step's gate runs.
- **Remove the latent goroutine leak.** Every worker goroutine has a guaranteed
  exit path even when the run is cancelled.
- **Consolidate per-step scheduler state** from sixteen parallel maps into one
  record type, so a step's runtime state lives in one place and adding the next
  per-step concern is a one-field change with a single cleanup site.
- **Close the idiomatic gaps** the review flagged: exhaustive/loud event-kind
  serialization, a typed scheduler constructor and recovery API, table-driven
  enum capabilities in `workflow`, consistent `%w` wrapping, a polymorphic seam
  for the monitor's input kinds, and removal of duplicated helpers.
- **Preserve behavior exactly.** `go build ./...`, `go vet ./...`, and
  `go test ./...` pass unchanged; no on-disk (`journal.jsonl`, `result.json`,
  `transcript.jsonl`) or on-wire format changes; the persistence-off path stays a
  clean no-op.

## User Stories

**As a jig operator running a workflow with parallel branches**, I want a slow
`[step.validate]` gate (e.g. `go test ./...`) on one branch to not freeze the
whole run, so the TUI keeps updating and other ready steps keep progressing while
that gate runs.

**As a jig operator who cancels a run**, I want no goroutines left blocked forever
on an undrained channel, so a long TUI session that starts and cancels many runs
does not slowly leak memory.

**As a maintainer extending the engine**, I want a single per-step state record
instead of sixteen maps, so I can see everything about a step in one place and add
a new per-step field without wiring a new map into the constructor and every
cleanup site.

**As a maintainer adding a new engine event**, I want serialization to fail loudly
(or round-trip correctly) instead of silently journaling the event as
`kind:"unknown"` and dropping it on replay, so the "state = fold(journal)"
invariant cannot rot silently.

**As a contributor to the `workflow` package**, I want output-kind capabilities in
one table and error paths that wrap with `%w`, so adding an output kind is one edit
and callers/tests can inspect errors with `errors.Is`/`errors.As` instead of
matching message substrings.

**As a contributor to the TUI**, I want the monitor's interactive input kinds
behind one small interface, so adding a new gate kind is one new type rather than
edits to six parallel switch statements.

## Demoable Units of Work

The units are dependency-ordered. Units 1–3 are the HIGH engine findings and are
each independently shippable. Unit 3 (state consolidation) is sequenced **after**
Units 1–2 so those land as small, reviewable diffs before the large mechanical
refactor; Unit 4 builds on the consolidated state. Every unit's overarching proof
is "`go test ./...` still green" (the behavior-preservation lock) plus the
unit-specific new tests below.

### Unit 1: Offload blocking I/O off the scheduler goroutine (HIGH)

**Purpose:** Restore real parallelism and responsiveness. Today gate commands,
`git worktree add`, and `git diff` run **inline in the scheduler goroutine**, so
while any one runs, no other step's completion, no `Snapshot()`, and no human
input is processed for that run. This unit moves that work to worker goroutines
that post typed results back to the inbox, leaving the scheduler doing only
non-blocking state transitions.

**Current anchors (confirmed by review):**
- `createWorktree` runs in `dispatch` on the scheduler goroutine
  (`internal/engine/engine.go:727`).
- `captureDiff` runs in `phCaptureWorktreeDiff`
  (`internal/engine/handlers.go:26`), invoked from the post-exec chain inside
  `handle(stepDoneMsg)` (scheduler goroutine).
- `runGate` runs in `phRunValidateGate` (`internal/engine/handlers.go:40`,
  `engine.go:1178`), also on the scheduler goroutine, and itself shells out via
  `exec.Command("sh", "-c", …)` and reads files.

**Functional Requirements:**
- The system shall execute `[step.validate]` gates **off** the scheduler
  goroutine. The scheduler shall transition the step to `StatusValidating`, launch
  a worker that runs the gate, and resume on a new typed inbox message (proposed
  `gateDoneMsg{stepID, passed, detail}`); the `GateResult` emit and the
  pass/fail transition shall happen on the scheduler goroutine when that message is
  handled.
- The system shall create git worktrees **off** the scheduler goroutine. When a
  worktree-isolated step is first dispatched, the scheduler shall launch a worker
  that runs `createWorktree` and resume on a typed message (proposed
  `worktreeReadyMsg{stepID, path, baseSHA, err}`); on success it stores the path
  and proceeds to dispatch the step's execution, on error it enters recovery —
  preserving today's behavior (`engine.go:727-742`), just asynchronously.
- The system shall capture worktree diffs **off** the scheduler goroutine, or at
  minimum not on the path that blocks step-completion handling; the captured diff
  shall still be available to downstream review steps (`collectDepDiffs`,
  `engine.go:1462`) by the time a review gate fires.
- The gate/worktree/diff functions shall be made **pure** (no `*scheduler`
  receiver, or a receiver that touches no shared mutable state) so they are safe to
  call from a worker goroutine — mirroring how `runner` executors already receive
  only a `StepRequest` + `Reporter`.
- The scheduler's single-writer invariant shall be preserved: all state mutation
  and all `emit(...)` calls for these operations shall happen only when the
  corresponding `*DoneMsg`/`*ReadyMsg` is handled on the scheduler goroutine.
- `inFlight` accounting shall remain correct: an asynchronous gate/worktree step
  counts as in-flight until its terminal transition, and the terminal check
  (`engine.go:530`) shall not fire while such work is outstanding.

**Proof Artifacts:**
- Unit test (engine): a workflow with two independent branches where branch A's
  gate blocks on a signal; assert branch B's step completion and a `Snapshot()`
  are processed *while* A's gate is still blocked (proving the scheduler is not
  frozen). Saved as `04-proofs/1.0-nonblocking-gate.txt` (test output).
- Unit test (engine): worktree-setup failure still routes to recovery
  (`StatusAwaitingRecovery`), matching current behavior, via the async path.
- Regression: existing gate/worktree/diff tests
  (`TestScheduler_GatePass`, `TestScheduler_GateFail`, the worktree suite) pass
  unchanged. `04-proofs/1.1-go-test-engine.txt`.

### Unit 2: Close the worker-send goroutine leak (HIGH)

**Purpose:** Guarantee every worker goroutine exits. On the `ctx.Done()` path the
scheduler returns immediately (`engine.go:539`) while workers may still be running;
each then does an unguarded `s.inbox <- stepDoneMsg{…}` (`engine.go:815`) into a
channel nobody drains. It is masked today only because `inbox` is buffered at 64
and `max_parallel` defaults to 4 — but `max_parallel` is author-controlled with no
ceiling, so a large value plus a cancel leaks goroutines that block forever.

**Functional Requirements:**
- The worker goroutine launched in `dispatch` shall send its `stepDoneMsg` via a
  `select` that also observes `ctx.Done()`, so it never blocks forever after the
  scheduler has exited:
  ```go
  select {
  case s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err}:
  case <-ctx.Done():
  }
  ```
- The same guarded-send discipline shall apply to every worker introduced in
  Unit 1 (`gateDoneMsg`, `worktreeReadyMsg`) and to the reporter's non-blocking
  notify send (already guarded at `engine.go:787`, to be kept).
- No behavior change on the normal path: when the run completes without
  cancellation, `run()` still returns only after `inFlight == 0`, so all workers
  have already delivered.

**Proof Artifacts:**
- Unit test (engine): start a run with `max_parallel` larger than the inbox buffer
  and a `FakeExecutor` whose steps block until released; cancel the run; release
  the steps; assert (via `runtime.NumGoroutine()` sampled before/after, with a
  short settle) that worker goroutines return rather than remaining parked. Saved
  as `04-proofs/2.0-cancel-no-leak.txt`.
- Regression: `TestScheduler_Cancel` (`engine_test.go:523`) passes unchanged.

### Unit 3: Consolidate per-step scheduler state into one record (HIGH)

**Purpose:** Replace the sixteen parallel `map[string]…` fields on `scheduler`
(`engine.go:375-431`) with a single `map[string]*stepRuntime`, so a step's entire
runtime state is one lookup, initialization is one place, and cleanup is one
`delete`. This is a large mechanical refactor with no behavior change; it is
sequenced after Units 1–2 (and Unit 1 adds the async messages that this record can
also carry) and it makes Unit 4 cleaner.

**Current anchors:** the sixteen maps are `states`, `structured`, `stepFeedback`,
`rerunSource`, `worktrees`, `wtBaseSHAs`, `diffs`, `pendingUserInputs`,
`collectedUserInputs`, `preResolvedInputs`, `resumeSessions`, `stepMessage`,
`reviewMessages`, `stepInputCount`, `recoverCount`, `reporters`
(`engine.go:378-426`), all keyed by step id.

**Functional Requirements:**
- The system shall define a `stepRuntime` struct holding the per-step fields that
  are today spread across the sixteen maps (embedding or wrapping `step.State`),
  and the scheduler shall hold a single `runtime map[string]*stepRuntime` seeded in
  `newScheduler` from `wf.Steps`.
- All read/write sites currently indexing the sixteen maps shall be rewritten to
  go through the single record. `states[id]` becomes `runtime[id]` (or an accessor
  returning the embedded `*step.State`), and `snapshot()` shall continue to return
  `[]step.State` unchanged.
- Cleanup shall consolidate to a single `delete(s.runtime, id)` where per-step
  entries are currently deleted piecemeal (`delete(s.reporters, …)`,
  `delete(s.preResolvedInputs, …)`, `delete(s.structured, …)`, etc.).
- The change shall be **purely internal to `internal/engine`**: no change to
  `RunSnapshot`, the `Event` types, `StepRequest`, or any exported signature.
- Field-level comments explaining *why* a piece of per-step state exists (e.g. the
  `rerunSource` last-write-wins note, the `preResolvedInputs` re-intercept guard)
  shall be preserved on the corresponding `stepRuntime` fields.

**Proof Artifacts:**
- Diff: `scheduler` struct before/after showing sixteen maps collapsed to one
  `runtime` map (`04-proofs/3.0-scheduler-struct-diff.txt`).
- Regression (the whole acceptance for this unit): `go test ./internal/engine/...`
  passes unchanged — the existing scheduler test suite (~30 tests covering linear,
  parallel, retry, continue, gate, loop, review, block_on, recovery, snapshot,
  input-resolution) is the behavior lock. `04-proofs/3.1-go-test-engine.txt`.

### Unit 4: Engine seam hygiene — events, constructor, recovery API, dispatcher (MEDIUM)

**Purpose:** Tighten the engine's typed seams so mistakes fail at compile time or
loudly at runtime instead of silently. Four related MEDIUM findings.

**Functional Requirements:**
- **Exhaustive, loud event serialization (M1).** `eventKind`
  (`internal/engine/journal.go:26-53`) and the `decoders` map
  (`journal.go:72-117`) shall be reconciled with the `Event` union
  (`event.go:165-178`) so no event serializes to the silent catch-all
  `"unknown"`. Either (a) add `input_request`, `prompt_request`, and
  `agent_question` to both `eventKind` and `decoders` so they round-trip, or
  (b) if those human-input events are intentionally not journaled, document that
  explicitly and make the default path fail loudly rather than emitting
  `kind:"unknown"` (e.g. return `""` and treat an empty kind as a programming
  error in `MarshalEnvelope`). A test shall assert every `Event` implementer
  either has a non-empty kind that appears in `decoders`, or is on an explicit
  documented exclusion list — so adding an event without wiring serialization
  fails the test.
- **Typed scheduler constructor (M2).** `newScheduler`'s eleven positional
  parameters (`engine.go:433-445`), three of which are adjacent bare strings
  (`runDir`, `jigRoot`, `repoRoot`), shall be replaced by a single
  `schedulerConfig` struct so transposing the strings is impossible. Internal only;
  `Manager.Start` is the sole caller (`engine.go:141`).
- **Typed recovery action and verdict (M3).** `RecoverRetry`/`RecoverResume`/
  `RecoverAbort` (`engine.go:319-323`) shall become a defined type
  (`type RecoverAction string`) and `Run.Recover`'s `action` parameter
  (`engine.go:232`) shall take that type, so an invalid action cannot be passed;
  `handleRecover`'s switch (`engine.go:1123`) keeps its no-op default for
  stale/duplicate messages. The TUI caller shall be updated to pass the typed
  constants.
- **Uniform `handle` dispatcher (M5).** The inline cases in `handle`
  (`engine.go:921-1051`) that carry substantial logic — `userInputMsg`
  (`:974-1000`) and `verdictMsg` (`:1002-1013`) — shall be extracted to
  `handleUserInput`/`handleVerdict`, matching the existing
  `handleHumanMessage`/`handleAgentInput`/`handleRecover` pattern, so `handle` is a
  uniform one-line-per-case dispatcher.

**Proof Artifacts:**
- Unit test (engine): event-serialization exhaustiveness test described above,
  failing before and passing after (`04-proofs/4.0-event-exhaustiveness.txt`).
- Unit test (engine): a round-trip test that marshals then `ReplayJournal`-decodes
  each currently-dropped event (if option (a) is chosen), or an explicit assertion
  that the excluded kinds are documented (if option (b)).
- Compile-time proof: `Run.Recover` called with a bare string no longer compiles
  (shown as a short snippet in `04-proofs/4.1-typed-recover.txt`).
- Regression: `go test ./internal/engine/...` green.

### Unit 5: `workflow` package idioms (MEDIUM/LOW)

**Purpose:** Close the `internal/workflow` idiomatic gaps without touching the
validator's deliberate "collect all problems" design.

**Functional Requirements:**
- **Table-driven output-kind capabilities.** The `OutputKind` capability checks
  scattered across `schema.go:417-429` (`allows`), `validate.go:422-430`
  (`checkOutputType`), and `validate.go:535-546` (`checkCondValue`) shall read from
  one source-of-truth table (e.g. `map[OutputKind]struct{ allowsCompare,
  scalarVerdict bool }`), so `checkOutputType` becomes a membership test and adding
  an output kind is one table edit. A missing kind shall fail closed (validation
  error), not silently.
- **`StepType` predicate methods.** The scattered `s.Type == StepAgent` /
  `== StepReview` comparisons (`validate.go:101`, `load.go:132`,
  `engine/context.go:125`, `engine.go:584/903/1442`) shall be expressed via small
  predicates on the type (e.g. `func (t StepType) IsAgent() bool`), centralizing
  the enum knowledge. (Execution routing already goes through `runner.Mux`; this
  covers only the predicate residue.)
- **Consistent `%w` wrapping.** `agent_file.go:40` shall wrap the underlying
  filesystem error (`fmt.Errorf("agent step %q: agent_file %q: %w", …, err)`)
  instead of formatting a bare "not found" message, so callers can
  `errors.Is(err, os.ErrNotExist)` — matching the sibling path at
  `agent_file.go:44`.
- **De-duplicate the tuning merge (M6).** The identical "fill-if-unset" cascade
  over the seven tuning fields in `applyDefaults` (`load.go:96-116`) and
  `applyProfiles` (`load.go:171-194`) shall be factored into one shared helper so
  adding a tuning field is a single edit.
- **Tests assert on structure, not message text.** The validation tests that
  `strings.Contains(err.Error(), …)` (`profiles_test.go:101,124,146,168,191`)
  shall assert membership against the already-exported
  `ValidationError.Problems []string` (or via `errors.As` to `*ValidationError`)
  instead of substring-matching the joined `Error()` string. **No new per-problem
  `Code`/`StepID` fields** are added — nothing in the codebase branches on them
  (verified: no `errors.As`/`errors.Is` on `*ValidationError`), so that would be
  speculative (see Non-Goals).

**Proof Artifacts:**
- Unit test (workflow): adding a hypothetical fourth `OutputKind` to the table (in
  a test fixture) is picked up by validation without editing the check functions
  — demonstrating the single seam.
- CLI: `go run ./cmd/jig validate examples/feature.toml` still exits 0
  (`04-proofs/5.0-validate-ok.txt`).
- Unit test (workflow): an `agent_file` pointing at a missing path yields an error
  for which `errors.Is(err, os.ErrNotExist)` is true.
- Regression: `go test ./internal/workflow/...` green.

### Unit 6: TUI monitor input-kind polymorphic seam (MEDIUM)

**Purpose:** Replace the monitor's ~6 parallel switches on the interactive
input-kind enum with one small interface, so adding a gate kind is one new type,
not edits across sync/load/update/render/footer.

**Current anchors:** the input-kind switch recurs at approximately
`internal/tui/monitor.go:486, 505, 694, 1403, 2048, 2080`.

**Functional Requirements:**
- The system shall define an `inputHandler` interface capturing the per-kind
  behavior that the switches currently branch on (draft sync, draft load, key
  update/dispatch, gate-body render, and any footer-hint contribution).
- Each existing input kind shall be implemented as one `inputHandler`; the ~6
  switch statements shall collapse to a single dispatch through the active
  handler.
- Behavior shall be identical: the same keys, drafts, rendering, and footer hints
  as today. This is a refactor, not a UX change.

**Proof Artifacts:**
- Diff: the removed switches and the new interface + implementations
  (`04-proofs/6.0-inputhandler-seam.txt`).
- Regression: `go test ./internal/tui/...` green — the existing monitor tests
  (input-queue, gate-entry, key handling) are the behavior lock.

### Unit 7: Cross-cutting polish (LOW)

**Purpose:** Small, high-clarity dedups that touch more than one package, batched
so they land together.

**Functional Requirements:**
- **`step.Status.IsTerminal()`.** Add `func (s Status) IsTerminal() bool` to
  `internal/step` and use it at the three sites that hard-code
  `{StatusSucceeded, StatusFailed, StatusSkipped}`: `engine.go:1652`,
  `engine.go:1679`, `tui/runs.go:312`.
- **Remove hand-rolled `contains`.** Delete `containsStr` (`runner/agent.go:611`)
  and `contains` (`workflow/validate.go:616`); use `slices.Contains` (already used
  at `engine/context.go:78`).
- **De-duplicate agent error text (L3).** Have `subtypeErrText`
  (`runner/agent.go:519`) compute its prefix and delegate the parts-assembly to
  `resultErrorText` (`runner/agent.go:544`) instead of rebuilding it.
- **Signpost intentional error drops (L4).** The best-effort artifact writes
  (`runner/agent.go:331,335,346`) and the swallowed `w.Close()` in
  `writeCommandTranscript` (`runner/command.go:146`) shall carry a one-line comment
  justifying the drop; the command-transcript path shall be adjusted so a
  successful `Append` still nudges the TUI even if `Close` errors.

**Proof Artifacts:**
- Diff: the `IsTerminal` call-site replacements and the deleted helpers
  (`04-proofs/7.0-dedup-diff.txt`).
- Regression: `go build ./... && go vet ./... && go test ./...` all green
  (`04-proofs/7.1-full-suite.txt`).

## Non-Goals (Out of Scope)

1. **Any behavior change.** No change to what workflows do, what runs produce, or
   any on-disk (`journal.jsonl`, `result.json`, `transcript.jsonl`, `output.*`) or
   on-wire (event) format. This is a refactor/hardening pass only.
2. **A discriminated-union rewrite of `Step`.** The review floated a sum-type for
   the flat `Step` struct; with three step types and a validator that already
   enforces field-combination rules, that is heavier machinery than the problem
   warrants and is explicitly excluded.
3. **Structured `ValidationError` codes / a query API.** No consumer branches on
   validation-error subtypes today (verified: no `errors.As`/`errors.Is` on
   `*ValidationError`). Adding per-problem `Code`/`StepID` fields would be
   speculative abstraction; excluded until a real consumer (e.g. TUI step
   highlighting) needs it.
4. **Removing `context.Context` from the TUI model structs.** Storing a
   long-lived ctx in `rootModel`/`chatModel` (`root.go:120`, `chat.go:46`) is
   conventional for Bubble Tea; churning it is not worth the risk. Left as-is.
5. **Removing `NewAgentExecutor()`.** The empty-struct constructor is retained for
   interface symmetry with the other executors; not worth changing.
6. **Splitting `engine.go` into multiple files** beyond what Units 3–4 naturally
   produce. File-size reduction is a welcome side effect, not a goal in itself.
7. **New tests for already-covered behavior.** Only the defect-specific new tests
   named in each unit are added; the existing suite is the regression lock.
8. **Performance tuning.** The scheduler changes are for correctness/liveness, not
   throughput; no benchmarks are required beyond the non-blocking assertion.

## Design Considerations

This is a non-TUI-facing change (Unit 6 is an internal TUI refactor with identical
UX). There are no new visual elements and no user-visible output changes. The only
observable differences are timing (a run with a slow gate stays responsive) and the
absence of leaked goroutines after cancel — both asserted by tests, not by UI.

## Repository Standards

- **Idiomatic, well-factored Go** (CLAUDE.md): the engine changes keep the
  single-writer actor model — the invariant that only the scheduler goroutine
  mutates run state — while moving *blocking* work to workers, exactly as `runner`
  executors already run off-goroutine and report back.
- **Comments explain the non-obvious "why."** The concurrency invariants (guarded
  sends, "emit only on the scheduler goroutine," why gate/worktree functions must
  be pure) are precisely the subtleties that earn comments; preserve the existing
  memory-model notes (`engine.go:134-140`, `:498-505`).
- **`internal/` packages are the unit of design.** Unit 3 keeps all changes inside
  `internal/engine`; `internal/step` gains only the pure `IsTerminal` predicate
  (it imports nothing and stays that way).
- **Validation is exhaustive and load-time.** The Unit 5 output-kind table must
  keep every existing valid/invalid path covered with table-driven tests, per the
  `internal/workflow` house style (inline TOML strings,
  `workflow_test.go`).
- **Persistence-off is a first-class path.** Every new writer/worker must no-op
  gracefully when there is no run dir; the engine/runner tests that pass `""` for
  the root are the guard and must stay green.
- **Examples are documentation.** `examples/feature.toml` must still
  `validate` cleanly after Unit 5.
- **Format & vet before committing.** `gofmt -l -w .` and `go vet ./...` are part
  of every unit's proof.

## Technical Considerations

**Where the code changes land (from the 2026-08-07 review, file:line confirmed):**

- **`internal/engine/engine.go`** — `dispatch` (`:716`) splits worktree creation
  into an async worker (Unit 1); the worker send at `:815` gains a `ctx.Done()`
  guard (Unit 2); the sixteen maps at `:378-426` collapse into one `runtime` map
  (Unit 3); `newScheduler` (`:433`) takes a config struct and `handle` (`:921`)
  becomes a uniform dispatcher (Unit 4); `RecoverAction` type and `Run.Recover`
  (`:232`, `:319`) become typed (Unit 4); `emit`/`snapshot` terminal-status checks
  (`:1652`, `:1679`) use `IsTerminal` (Unit 7).
- **`internal/engine/handlers.go`** — `phRunValidateGate` (`:34`) and
  `phCaptureWorktreeDiff` (`:24`) become launchers for async workers rather than
  inline blocking calls; the actual gate/diff logic (`runGate` at `engine.go:1178`,
  `captureDiff` at `worktree.go:55`) is made pure/worker-safe (Unit 1).
- **`internal/engine/journal.go`** — `eventKind` (`:26`) and `decoders` (`:72`)
  reconciled with the `Event` union; loud default (Unit 4).
- **`internal/engine/event.go`** — the `Event` union (`:165-178`) is the
  exhaustiveness reference for the Unit 4 test; no new events required.
- **`internal/step/step.go`** — add `Status.IsTerminal()` (`:14-28`) (Unit 7).
- **`internal/workflow/schema.go`, `validate.go`, `load.go`, `agent_file.go`** —
  the output-kind table, `StepType` predicates, `%w` wrapping, and the tuning-merge
  helper (Unit 5).
- **`internal/workflow/profiles_test.go`** — assert on `Problems` not `Error()`
  (Unit 5).
- **`internal/tui/monitor.go`** — the `inputHandler` seam (Unit 6);
  **`internal/tui/runs.go:312`** — `IsTerminal` (Unit 7).
- **`internal/runner/agent.go`, `command.go`** — delete `containsStr`, dedup
  `subtypeErrText`, signpost error drops (Unit 7).

**Concurrency invariant (load-bearing).** The engine's correctness rests on
"only the scheduler goroutine mutates run state; workers communicate solely via the
inbox" (`engine.go:1-5`). Unit 1 must uphold this: gate/worktree/diff workers
receive an immutable snapshot of what they need (paths, the workflow step, base
SHA) and return results via typed messages; they must not read or write
`s.runtime`, `s.diffs`, or emit events directly. This mirrors the existing
`Reporter` contract, whose doc already warns "fanOutLive must not touch scheduler
state" (`engine.go:349-350`).

**No external standards research needed.** This is an internal Go refactor with no
new third-party technology surface; no latest-standards research was required.

## Security Considerations

No specific security considerations. The change touches no credentials, no network
calls, and no secret handling; it does not change what is written to disk or the
event stream. Gate commands and git operations run with exactly the same
privileges and working directories as today — only the goroutine they run on
changes. Proof artifacts under `04-proofs/` are test output and diffs from the
existing example workflow and contain no secrets.

## Success Metrics

1. **Non-blocking scheduler:** the Unit 1 test proves a second branch progresses
   (and `Snapshot()` returns) while a first branch's gate is blocked — impossible
   before this change.
2. **No leak:** the Unit 2 test shows worker goroutines return after a cancel with
   `max_parallel` above the inbox buffer size.
3. **State consolidation:** the `scheduler` struct holds **one** per-step map, not
   sixteen (Unit 3 diff), and `go test ./internal/engine/...` is unchanged-green.
4. **Loud serialization:** the Unit 4 exhaustiveness test fails if any `Event` is
   added without wiring its kind, and no event serializes to `"unknown"`.
5. **Single enum seam:** adding an `OutputKind` in a Unit 5 test fixture is honored
   by validation with no edits to the check functions.
6. **One TUI seam:** the monitor's input-kind switches are replaced by one
   `inputHandler` dispatch (Unit 6 diff), tests green.
7. **No regressions (the overarching gate):** `go build ./...`, `go vet ./...`,
   `gofmt -l .` (empty), and `go test ./...` all pass at every unit boundary, and
   `go run ./cmd/jig validate examples/feature.toml` exits 0.

## Open Questions

1. **Split into 2–3 specs?** (Non-blocking.) The reviewer recommended splitting
   this into (a) the engine HIGH fixes, (b) engine MEDIUM hygiene, and (c) the
   workflow/tui/cross-cutting polish; the user chose a single tracking spec with
   independently-shippable units. If task planning (Phase 2) finds the combined
   task list unwieldy, the units are already cut along the recommended split lines
   and can be promoted to separate specs without rework. Does not affect any unit's
   scope or acceptance.
2. **Unit 1: keep the diff synchronous?** (Non-blocking.) `captureDiff` is usually
   fast; if moving it off-goroutine complicates the "review sees the latest diff"
   ordering, an acceptable alternative is to leave diff capture synchronous and
   offload only the gate and worktree creation (the two that shell out / can be
   slow). The implementer may choose per the code once Unit 1 is underway; either
   satisfies the goal (scheduler not blocked on the *slow* operations).
3. **Unit 4 (M1): journal the human-input events, or document the exclusion?**
   (Non-blocking assumption.) Default assumption: document the exclusion and make
   the default loud, since `InputRequest`/`PromptRequest`/`AgentQuestion` are
   transient request signals not needed to reconstruct state on replay. If they
   turn out to be useful for replay/audit, option (a) (round-trip them) is a
   drop-in. Either satisfies the "no silent `unknown`" requirement.
