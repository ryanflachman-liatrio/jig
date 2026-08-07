# 06-tasks-run-integration-branch.md

Task plan for **Foundation A — Run-integration branch**
(spec: [`06-spec-run-integration-branch.md`](./06-spec-run-integration-branch.md)).

> **Status: sub-tasks generated (Phase 3 complete). Planning audit:
> [`06-audit-run-integration-branch.md`](./06-audit-run-integration-branch.md).**
> All file:line references are grounded against the current `internal/engine` code
> (assessed 2026-08-07); verify at implementation time since line numbers drift.

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | (1) Single-writer scheduler owns state/DAG/journal; engine extensibility, thin TUI consumers (ADR 0003). (2) "File is truth, bus is liveness" — durable state on disk, bus carries only liveness. (3) Persistence-off (`runDir==""`) is a first-class no-op path every writer must honor. | Declares the engine **real and running today** — conflicts with TESTING.md/ARCHITECTURE.md staleness (see below). |
| `docs/TESTING.md` | yes | (1) Table-driven tests with inline fixtures; `go test ./...`, run engine/TUI work under `-race`. (2) Keep agent/shell execution behind interfaces so DAG/gate/loop logic is testable with fakes (no live model calls). (3) `gofmt -l -w . && go vet ./...` and `jig validate examples/*.toml` before a change. | **Stale:** coverage table calls `internal/engine`,`runner`,`step`,`manifest`,`datastore` "Not implemented — empty placeholders." Code + CLAUDE.md contradict this. |
| `docs/ARCHITECTURE.md` | yes | (1) Package layout marks engine/runner/step/transcript/manifest/datastore `[DONE]`. (2) Run dir layout `.jig/runs/<id>/` (journal.jsonl, steps/<id>/result.json+transcript.jsonl, artifacts/) kept outside the worktree. (3) Invariant: control flow is a DAG + bounded back-edges; termination guaranteed. | **Partially stale:** "Where the engine will plug in (planned)" prose describes the engine as not-yet-built, contradicting its own `[DONE]` table and CLAUDE.md. |
| `README.md` | yes | (1) Go 1.25 (`mise.toml`). (2) Build/run: `go build ./cmd/jig`, `go run ./cmd/jig validate examples/feature.toml`. (3) Three step types (agent/command/review) wired by `depends_on`; mutating tools → git worktree. | none |

**Conflict resolution / precedence:** where TESTING.md and ARCHITECTURE.md describe the engine as
"planned / not implemented," they are **stale**. CLAUDE.md is authoritative — it states the
execution engine "is real and runs workflows today" and warns the list drifts, so implementation
tasks are grounded against the **actual code** (verified by the current-state code assessment),
not the stale prose. Correcting that stale prose is out of scope for Foundation A and is owned by
**Unit D** (`docs/specs/09-spec-docs-examples-adrs/`). No unresolved standards conflict blocks
this plan.

## Functional-Requirement → Parent-Task Coverage

| Spec FR (unit) | Parent task |
| --- | --- |
| A1: create run branch + run worktree at working-branch HEAD on run start | 1.0 |
| A1: persistence-off / non-git no-op (no run branch, steps run in place) | 1.0 |
| A1: step worktree branches off **run branch HEAD at dispatch**; read-only steps get none | 2.0 |
| A1: squash-merge each step as one commit tagged `jig-step: <id>`; advance run HEAD | 2.0 |
| A1: maintain `stepCommits[stepID]=sha`, reconstructable from `jig-step` trailer | 2.0 |
| A1: parallel dispatch honored, **integration serialized** in declaration order | 2.0 |
| A2: integration conflict → new Gate input kind, park step, run stays alive | 3.0 |
| A2: operator resolves in run worktree → Gate completion → finish integration; abort → recovery | 3.0 |
| A2: conflict resolution is a single-writer scheduler message + handler | 3.0 |
| A3: final merge gate at run end — approve merges run branch → base; discard leaves base | 4.0 |
| A3: drop hand-wired `merge` command steps in examples; `jig validate` exits 0 | 4.0 |

Every functional requirement maps to at least one parent task and (below) at least one planned
test/proof artifact.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/engine/engine.go` | Scheduler lifecycle. Add run-branch/run-worktree creation at run start (near repoRoot derivation `engine.go:130-133`, run setup `engine.go:99-133`); add `runBranch`/`runWorktree`/`stepCommits` fields beside `worktrees`/`wtBaseSHAs`/`diffs` (`engine.go:396-401`); change per-step worktree base from repo-root HEAD to run-branch HEAD in `dispatch` (`engine.go:721-742`); extend teardown to drop the run worktree but keep the run branch (`engine.go:498-517`); present the final-merge gate at terminal detection (`engine.go:530-531`); add new inbox message types (`engine.go:272-344`) + `handle` cases (`engine.go:921-1051`) + `Run.*` methods; teach `anyPendingRunnable` the new parked status (`engine.go:647-700`). |
| `internal/engine/worktree.go` | New low-level git helpers beside `createWorktree`/`removeWorktree`/`captureDiff`/`gitCmd` (`worktree.go:23-102`): `currentHEAD`, `createBranchAt(ref)`, `runBranchName` (via `sanitizeBranchName`, `worktree.go:80-92`), `squashMergeStep` (one commit, `jig-step:` trailer), conflict detection, `finalMerge`, and `stepCommitsFromLog` (rebuild the map from trailers). |
| `internal/engine/worktree_test.go` | Unit tests for the new git helpers in a `t.TempDir()` repo, following the existing init-repo pattern (`worktree_test.go`). |
| `internal/engine/integration.go` *(new)* | Run-branch lifecycle + squash-merge orchestration, keeping `engine.go` lean (small-package convention per CLAUDE.md). |
| `internal/engine/integration_test.go` *(new)* | Engine-level tests using the fake-executor pattern from `engine_test.go`: A→B composition, read-only no-commit, step→commit map, conflict gate, final merge. |
| `internal/engine/handlers.go` | Register a new `phSquashMergeIntegration` stage in `postExecChain` (init `handlers.go:478`) using the `postExecHandler` signature (`handlers.go:20`); return a parked decision on conflict (extend `postExecDecision`, `handlers.go:8-15`). |
| `internal/engine/event.go` | New `IntegrationConflictRequest{StepID, Paths}` and `FinalMergeRequest{RunBranch, Base}` events with `isEvent()`, mirroring `RecoveryRequest` (`event.go:125-132`). |
| `internal/engine/journal.go` | `eventKind` strings + decoders for the new events (`journal.go:26,72-117`) so they round-trip and unknown kinds still skip. |
| `internal/engine/journal_test.go` | Round-trip + unknown-kind-skip cases for the new events, following existing journal tests. |
| `internal/step/step.go` | New parked status `StatusAwaitingIntegration` beside `StatusAwaitingRecovery` (`step.go:24`). |
| `internal/tui/monitor.go` | New `inputKindIntegrationConflict` + `inputKindFinalMerge` in the `pendingInputKind` enum (`monitor.go:40-50`) and `pendingInputEntry` payloads (`monitor.go:56-84`); render conflicted paths / approve-or-discard. |
| `internal/tui/root.go` | Route the new messages to `Run.*`, mirroring `recoverResponseMsg → run.Recover` (`root.go:242-277`). |
| `internal/tui/monitor_test.go` | TUI tests: the conflict entry renders with paths and takes focus; the final-merge entry offers approve/discard — driving `Update`/`View` per the existing monitor test pattern. |
| `examples/bugfix.toml` | Drop the hand-wired `merge` command step (the only example that has one); must still pass `jig validate`. |
| `docs/specs/06-spec-run-integration-branch/06-proofs/` | Proof artifacts: `A1.0-run-branch-log.txt`, `A2.0-integration-conflict-gate.txt`, `A3.0-final-merge.txt`. |

### Notes

- **Tests live beside the code** and are table-driven with inline fixtures (`docs/TESTING.md`);
  run engine/TUI work under `go test ./... -race`. Keep executors behind the existing `Executor`
  interface so DAG/gate logic is tested with fakes — no live model calls.
- **Persistence-off is first-class:** every new git/disk operation must no-op when `s.repoRoot ==
  ""` / `s.runDir == ""`, mirroring the existing worktree guard (`engine.go:721`).
- **Single-writer discipline:** every new action is one `inbox` message + one `handle` case — no
  locks. Integration runs on the scheduler goroutine, which is what serializes it.
- Before finishing: `gofmt -l -w . && go vet ./...` and `go run ./cmd/jig validate examples/*.toml`.

## Tasks

### [x] 1.0 Run-branch & run-worktree lifecycle (scheduler-owned, persistence-off no-op)

On run start, when `repoRoot != ""`, create a run branch at the user's working-branch HEAD and a
run worktree, recorded on the scheduler; define the branch naming (`jig/<workflow>/run-<runID>`,
per Open Question 1) and the end-of-run keep/teardown behavior. When there is no repo root / no run
dir, this is a complete no-op: no run branch, no run worktree, steps run in place. This slice is
demoable on its own — a run against a git repo leaves an inspectable run branch at the right base;
a run with persistence off behaves exactly as today.

#### 1.0 Proof Artifact(s)

- Test: `TestRunBranchCreatedAtWorkingHead` (engine) — starting a run in a temp git repo creates a
  branch named `jig/<workflow>/run-<runID>` whose tip equals the working-branch HEAD at start;
  demonstrates the run branch is rooted correctly (FR: A1 run-branch creation).
- Test: `TestRunBranchNoopWhenNoRepoRoot` (engine) — with `repoRoot == ""` no branch/worktree is
  created and the run completes; demonstrates the persistence-off / non-git no-op path (FR: A1
  no-op). Run under `-race`.
- CLI/Diff: `06-proofs/A1.0-run-branch-log.txt` — `git branch --list 'jig/*'` and `git log
  --format=%H %s -1 <run-branch>` before any step runs, showing the branch at the base commit;
  observable, reproducible evidence the lifecycle is correct.

#### 1.0 Tasks

- [x] 1.1 Add `runBranch string` and `runWorktree string` fields to the scheduler struct beside the
  existing `worktrees`/`wtBaseSHAs`/`diffs` maps (`engine.go:396-401`), plus `stepCommits
  map[string]string` (populated by 2.0). Initialize the map where the sibling maps are initialized.
- [x] 1.2 In `worktree.go`, add `currentHEAD(dir string) (string, error)` and `createBranchAt(repoRoot,
  branch, ref string) error` as thin `gitCmd` wrappers, and `runBranchName(workflow, runID string)
  string` returning `jig/<sanitized-workflow>/run-<runID>` via `sanitizeBranchName` (`worktree.go:80-92`).
- [x] 1.3 At run start, when `s.repoRoot != ""`, create the run branch at the working-branch HEAD and
  add a run worktree for it (reuse/extend `createWorktree`), recording `s.runBranch`/`s.runWorktree`.
  Do it in the scheduler setup before the dispatch loop (near `engine.go:99-133`).
- [x] 1.4 Guard every step above behind `s.repoRoot != ""` exactly like the existing worktree guard
  (`engine.go:721`); when empty, `runBranch`/`runWorktree` stay `""` and steps run in place (no-op path).
- [x] 1.5 Extend the teardown defer (`engine.go:498-517`) to remove the run worktree but **keep** the
  run branch (Task 4.0 decides merge/keep); leave the existing per-step worktree removal intact.
- [x] 1.6 Add `TestRunBranchCreatedAtWorkingHead` and `TestRunBranchNoopWhenNoRepoRoot` to
  `integration_test.go` (temp git repo via the `worktree_test.go` init pattern); run under `-race`.
  Generate `06-proofs/A1.0-run-branch-log.txt` (`git branch --list 'jig/*'` + run-branch tip).

### [x] 2.0 Step worktrees off run HEAD + squash-per-step integration + step→commit map

Change a mutating step's worktree to branch off the **run branch's current HEAD at dispatch time**
(not repo-root HEAD); read-only steps get no worktree. On a step's successful completion,
squash-merge its worktree branch into the run branch as exactly one commit whose message carries
the `jig-step: <stepID>` trailer, advancing the run HEAD by one. Maintain `stepCommits[stepID]=sha`
on the scheduler, reconstructable from the run-branch history via the trailer. Keep parallel
dispatch, but **serialize integration** in declaration order so the run branch is a single linear
history. This is the core code-composition slice.

#### 2.0 Proof Artifact(s)

- Test: `TestStepsComposeOnCode` (engine) — `A → B` where `A` writes `file_a` and `B` reads it and
  writes `file_b`; assert `B`'s worktree contains `file_a` (it branched off the run branch after
  `A` integrated) and the run branch ends with two commits tagged `jig-step: A`, `jig-step: B`;
  demonstrates code composition + squash-per-step tagging (FR: A1 worktree-off-run-HEAD,
  squash-merge, ordering).
- Test: `TestReadOnlyStepProducesNoCommit` (engine) — a read-only step advances the run branch HEAD
  by zero commits; demonstrates read-only steps integrate nothing (FR: A1 read-only no worktree).
- Test: `TestStepCommitMapReconstructable` (engine) — `stepCommits` matches the shas parsed from the
  run-branch `jig-step:` trailers; demonstrates the map is rebuildable from history (FR: A1 map).
- CLI/Diff: `06-proofs/A1.0-run-branch-log.txt` — `git log --format=%s <run-branch>` showing one
  tagged commit per mutating step, in declaration order; observable, reproducible.

#### 2.0 Tasks

- [x] 2.1 In `dispatch` (`engine.go:721-742`), change a mutating step's worktree so it branches off the
  run branch's current HEAD (`currentHEAD(s.runWorktree)` / the `s.runBranch` ref) instead of repo-root
  HEAD. Leave read-only steps worktree-less and preserve worktree reuse on retry/loop (`engine.go:722-723`).
- [x] 2.2 In `worktree.go`, add `squashMergeStep(repoRoot, runWorktree, stepBranch, stepID string) (sha
  string, conflict bool, err error)`: squash-merge `stepBranch` into the run branch as one commit whose
  message carries the trailer `jig-step: <stepID>`; return the new sha, or `conflict=true` (no commit) on
  a merge conflict. Follow the `gitCmd` style.
- [x] 2.3 Add a `phSquashMergeIntegration` post-exec handler (`handlers.go`, signature at
  `handlers.go:20`) that runs on a succeeding mutating step: call `squashMergeStep`, set
  `s.stepCommits[stepID]=sha`. Register it in the `postExecChain` init (`handlers.go:478`) after
  `phCaptureWorktreeDiff`/`phRunValidateGate`. No-op when `s.repoRoot == ""`. On conflict, hand off to 3.0.
- [x] 2.4 Confirm integration is serialized: `stepDoneMsg` already funnels through the single-writer inbox
  (`engine.go:813,921-1051`), so the handler runs on the scheduler goroutine. Add a comment explaining this
  is what guarantees the run branch's single linear history; if parallel completions can interleave,
  integrate in declaration order via `wf.index`.
- [x] 2.5 Add `stepCommitsFromLog(runWorktree string) (map[string]string, error)` in `worktree.go` that
  parses `jig-step:` trailers from the run-branch history, so the map is reconstructable from history.
- [x] 2.6 Add `TestStepsComposeOnCode` (A writes `file_a`; B reads it and writes `file_b`; assert B's
  worktree contains `file_a` and the run branch has two `jig-step:`-tagged commits),
  `TestReadOnlyStepProducesNoCommit`, and `TestStepCommitMapReconstructable` to `integration_test.go`;
  extend the fake executor to write files into the step worktree. Refresh `06-proofs/A1.0-run-branch-log.txt`.
- [x] 2.7 **Regression guard (audit FLAG 1):** because 2.1 repoints every mutating step's worktree base,
  confirm the existing engine suites still pass under `go test ./internal/engine -race`
  (`engine_test.go`, `recovery_test.go`, `replay_test.go`, `worktree_test.go`), and add a loop/retry case
  asserting a re-run step re-integrates correctly onto the run branch (worktree reuse at `engine.go:722-723`
  still holds) — not just the happy A→B path.

### [x] 3.0 Integration-conflict gate

When a squash-merge in 2.0 hits a conflict (two steps touched the same lines), surface it to a
human instead of failing silently or auto-resolving. Add a new Gate input-entry kind (proposed
`inputKindIntegrationConflict`) naming the step and conflicted paths, park the step, and keep the
run alive (non-blocking gate, ADR 0002). The operator resolves in the run worktree and signals
completion through the Gate; the engine finishes the integration and continues. An abort path
fails the step, routing to the existing recovery gate. Implement resolution as a single-writer
scheduler message + handler mirroring `Run.Resolve` / `handleRecover`.

#### 3.0 Proof Artifact(s)

- Test: `TestIntegrationConflictRaisesGate` (engine) — two parallel steps writing the same file
  line; the second integration raises the gate; resolving it lands a merged commit; the run
  completes; demonstrates conflict detection, parking, resolution, and continuation (FR: A2 gate +
  resolve).
- Test: `TestIntegrationConflictAbortFailsStep` (engine) — the abort path transitions the step to
  failed and routes to the recovery gate; demonstrates the abort branch (FR: A2 abort).
- TUI test: the integration-conflict entry renders in the Gate with the conflicted paths and takes
  focus like other entries (feed the entry through `Update`, assert on `View()`); demonstrates the
  human surface (FR: A2 gate entry). Run under `-race`.
- Artifact: `06-proofs/A2.0-integration-conflict-gate.txt` — captured status events + resulting
  merged-commit log; observable, reproducible.

#### 3.0 Tasks

- [x] 3.1 Add `StatusAwaitingIntegration` to `internal/step/step.go` beside `StatusAwaitingRecovery`
  (`step.go:24`), with a comment that it is a parked-but-alive status.
- [x] 3.2 Add `IntegrationConflictRequest{StepID string, Paths []string}` to `event.go` with
  `isEvent()`, mirroring `RecoveryRequest` (`event.go:125-132`).
- [x] 3.3 Wire it into the journal: add the kind string in `eventKind` (`journal.go:26`) and a decoder
  (`journal.go:72-117`); add a round-trip + unknown-kind-skip row to `journal_test.go`.
- [x] 3.4 In `phSquashMergeIntegration`, on `conflict == true`: transition the step to
  `StatusAwaitingIntegration`, emit `IntegrationConflictRequest` with the conflicted paths (parsed from
  git output), and return a parked/halt `postExecDecision` (`handlers.go:8-15`) so the run stays alive.
  Add `StatusAwaitingIntegration` to `anyPendingRunnable` (`engine.go:647-700`) so the run does not settle
  while a conflict is parked (non-blocking gate, ADR 0002).
- [x] 3.5 Add `resolveIntegrationMsg{stepID string, abort bool}` (`engine.go:272-344`), a public
  `Run.ResolveIntegration(stepID string, abort bool)`, and a `handleResolveIntegration` case in `handle`
  (`engine.go:921-1051`) mirroring `handleRecover` (`engine.go:1021-1152`): on resolve, complete the
  squash-merge from the operator-resolved run worktree, record `stepCommits[stepID]`, transition the step
  to succeeded, and continue; on abort, fail the step (routes to the recovery gate via `applyFailurePolicy`).
- [x] 3.6 TUI: add `inputKindIntegrationConflict` to `pendingInputKind` (`monitor.go:40-50`) and a payload
  in `pendingInputEntry` (`monitor.go:56-84`); render the step id + conflicted paths and take focus like
  other entries; route resolve/abort in `root.go` mirroring `recoverResponseMsg → run.Recover`
  (`root.go:242-277`).
- [x] 3.7 Add `TestIntegrationConflictRaisesGate` and `TestIntegrationConflictAbortFailsStep` to
  `integration_test.go` (two parallel steps writing the same line; second integration raises the gate;
  resolve lands a merged commit and the run completes; abort fails the step), and a `monitor_test.go` case
  asserting the conflict entry renders with paths. Generate `06-proofs/A2.0-integration-conflict-gate.txt`.

### [ ] 4.0 Final gated merge (run branch → user branch) + de-merge examples

At run end (all steps terminal), present a final merge gate: merge the run branch onto the user's
working branch, or discard. On approve, jig performs the merge and the base HEAD gains the run's
commits; on discard, the run branch is left for inspection and nothing lands. Update the
`examples/*.toml` workflows that hand-merge step branches (e.g. `bugfix.toml`) to drop the explicit
`merge` command step, keeping `jig validate` at exit 0. This retires hand-wired merges and lands the
run once under human control.

#### 4.0 Proof Artifact(s)

- Test: `TestFinalMergeApproveLandsCommits` (engine) — after a run, approving the final merge
  fast-forwards/merges the run branch onto the base and the base HEAD contains the run's commits;
  demonstrates the approve path (FR: A3 approve).
- Test: `TestFinalMergeDiscardLeavesBase` (engine) — discarding leaves the base untouched and the
  run branch present for inspection; demonstrates the discard path (FR: A3 discard).
- CLI: `go run ./cmd/jig validate examples/bugfix.toml` (and other edited examples) exits 0 after
  the `merge` step is removed; demonstrates examples stay valid (FR: A3 examples).
- Artifact: `06-proofs/A3.0-final-merge.txt` — base-branch `git log --oneline` before and after
  approve; observable, reproducible.

#### 4.0 Tasks

- [ ] 4.1 Add `FinalMergeRequest{RunBranch string, Base string}` to `event.go` + `isEvent()`, and its
  journal kind/decoder (`journal.go:26,72-117`) with a round-trip test row in `journal_test.go`.
- [ ] 4.2 In `worktree.go`, add `finalMerge(repoRoot, base, runBranch string) (conflict bool, err error)`
  that merges the run branch onto the base working branch (fast-forward where possible); discard is simply
  not calling it (the run branch is left in place).
- [ ] 4.3 At terminal detection (`engine.go:530-531`), when a run branch exists with ≥1 commit, present the
  final-merge gate (emit `FinalMergeRequest`, keep the run parked-but-alive) instead of emitting
  `RunFinished` immediately. With no run branch / no commits (persistence-off), finish exactly as today.
  **Constraint (audit FLAG 2):** this gate is a **pre-`RunFinished` completion step** (approve/discard →
  *then* `RunFinished`), **not** the retired park-after-finish lifecycle — introduce **no** `RunResumed`
  event and no post-`RunFinished` scheduler re-entry. Add a comment saying so.
- [ ] 4.4 Add `finalMergeMsg{approve bool}` (`engine.go:272-344`), `Run.FinalMerge(approve bool)`, and a
  `handle` case: on approve, call `finalMerge` (a conflict routes to the 3.x integration-conflict gate); on
  discard, skip the merge and leave the run branch. Then emit `RunFinished` — the run settles here and is
  locked (no resume), per the FLAG 2 constraint above.
- [ ] 4.5 TUI: add `inputKindFinalMerge` (`monitor.go:40-84`) rendering an approve/discard prompt at run
  end; route the response in `root.go` mirroring the other gates (`root.go:242-277`).
- [ ] 4.6 Edit `examples/bugfix.toml` to remove the hand-wired `merge` command step; run `go run ./cmd/jig
  validate examples/bugfix.toml` (and the other `examples/*.toml`) and confirm exit 0.
- [ ] 4.7 Add `TestFinalMergeApproveLandsCommits` and `TestFinalMergeDiscardLeavesBase` to
  `integration_test.go`, plus a `monitor_test.go` case for the approve/discard entry. Generate
  `06-proofs/A3.0-final-merge.txt` (base `git log --oneline` before/after approve).
