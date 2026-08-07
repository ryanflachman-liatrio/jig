# Task 04 Proofs — Final gated merge (run branch → user branch) + de-merge examples

## Task Summary

Task 4.0 lands the run under human control. At run end (all steps terminal) with a
non-empty run branch, the scheduler emits a single `FinalMergeRequest` and parks
(non-blocking, ADR 0002) instead of finishing. `Run.FinalMerge(approve)` settles
it: approve merges the run branch onto the user's working branch (fast-forward
where possible) so the base HEAD gains the run's commits; discard leaves the run
branch in place and merges nothing. Either outcome then emits `RunFinished`. This
retires hand-wired `merge` command steps — `examples/bugfix.toml` no longer carries
one.

**FLAG-2 constraint:** the final-merge gate is a *pre-`RunFinished` completion
step*. The scheduler never emits `RunFinished` until the operator decides, and
introduces **no** `RunResumed` event and no post-finish re-entry (a `terminated`
flag makes `run()` return after the settling `RunFinished`).

## What This Task Proves

- Approving the gate merges the run branch onto the base; the base HEAD advances
  and contains the run's `jig-step`-tagged commits.
- Discarding leaves the base untouched and the run branch present for inspection.
- `FinalMergeRequest` round-trips through the journal.
- The TUI renders the final-merge gate (merge/discard) and its `y`/`d` actions
  emit `finalMergeResponseMsg`.
- `examples/bugfix.toml` validates (exit 0) after the hand-wired `merge` step is
  removed.

## Evidence Summary

- `TestFinalMergeApproveLandsCommits`, `TestFinalMergeDiscardLeavesBase`,
  `TestJournalRoundTrip` (now covering `FinalMergeRequest`),
  `TestMonitorFinalMergeGate` all PASS under `-race`.
- `go test ./... -race` green; `gofmt -l .` clean; `go vet ./...` clean.
- `go run ./cmd/jig validate examples/bugfix.toml` → `ok: "bugfix" v1 — 3 step(s)`.

## Artifact: Approve lands the run's commits onto base

**What it proves:** After a run leaves two `jig-step`-tagged commits on the run
branch, approving the final-merge gate advances the base branch HEAD to include
them, and the run's files appear in the base working tree.

**Why it matters:** This is the observable evidence that a run lands *once*, under
human control, replacing hand-wired `git merge` steps.

**Artifact path:** `06-proofs/A3.0-final-merge.txt`

**Result summary:** base `git log --oneline` before approve is `init` only; after
`git merge --no-edit <run-branch>` it carries `jig-step: a` and `jig-step: b`, and
`file_a.txt` / `file_b.txt` are present on base.

```
## base branch BEFORE approve
0a5d939 init
## base branch AFTER approve
5016acb b jig-step: b
b114522 a jig-step: a
0a5d939 init
```

## Artifact: Engine + journal + TUI tests under -race

**Command:**

```bash
go test ./internal/engine -race -run 'TestFinalMerge|TestJournal' -v
go test ./internal/tui -run TestMonitorFinalMergeGate -v
go test ./... -race
go run ./cmd/jig validate examples/bugfix.toml
```

**Result summary:** All targeted tests PASS; the whole suite is green under
`-race`; the de-merged example validates.

```
--- PASS: TestFinalMergeApproveLandsCommits (0.30s)
--- PASS: TestFinalMergeDiscardLeavesBase (0.24s)
--- PASS: TestJournalRoundTrip (0.00s)
--- PASS: TestMonitorFinalMergeGate (0.00s)
ok: "bugfix" v1 — 3 step(s)
```

## Regression note

Adding the gate changes run-end behavior for every git-backed run: a successful
run now parks awaiting the final merge. The pre-existing git-backed engine tests
(`TestScheduler_WorktreePath`, `TestScheduler_ReviewDiff`,
`TestScheduler_WorktreeBranchReuseAcrossRuns`, and the Task 2/3 compose/conflict
tests) were updated to answer the gate (`run.FinalMerge(false)` / the shared
`driveFinalMerge` helper). Non-git / persistence-off runs (`repoRoot == ""`) never
create a run branch, so they finish exactly as before — the gate is skipped.

## Reviewer Conclusion

The run lands once, under a human gate: approve fast-forwards the base onto the
run's `jig-step` commits, discard leaves both base and run branch intact. The gate
is a clean pre-`RunFinished` completion step with no resume semantics, and the
example workflows no longer hand-wire a merge.
