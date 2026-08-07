# Task 01 Proofs — Run-branch & run-worktree lifecycle

## Task Summary

Task 1.0 gives every run a **per-run integration branch** created at the user's
working-branch HEAD, plus a run worktree checked out on it, both owned by the
single-writer scheduler. Persistence-off / non-git remains a first-class no-op:
no run branch, no run worktree, steps run in place. This is the substrate the
squash-per-step integration (Task 2.0) and final gated merge (Task 4.0) build on.

## What This Task Proves

- Starting a run in a git repo creates a branch `jig/<workflow>/run-<runID>`
  whose tip equals the working-branch HEAD at run start.
- With no repo root (persistence-off / non-git) the run completes and **no** run
  branch is created — steps run in place exactly as before spec 06.
- The change does not regress the existing engine suite (unit, recovery, replay,
  worktree) under `-race`.

## Evidence Summary

- `TestRunBranchCreatedAtWorkingHead` passes: the run branch exists after the run
  and its tip SHA equals the working HEAD captured before start.
- `TestRunBranchNoopWhenNoRepoRoot` passes under `-race`: the run finishes
  unfailed and no `jig/*` branch is created.
- `go test ./internal/engine -race` and `go test ./...` are green; `gofmt`/`go vet` clean.

## Artifact: Run branch rooted at working HEAD

**What it proves:** `scheduler.setupRunBranch` creates `jig/<workflow>/run-<runID>`
at the working-branch HEAD before any step runs.

**Why it matters:** Steps must compose on top of a branch that starts exactly at
the user's HEAD; if the root drifted, the final merge would carry unrelated history.

**Artifact path:** `06-proofs/A1.0-run-branch-log.txt`

**Result summary:** `git branch --list 'jig/*'` lists the run branch, and its
`git log -1` tip SHA is identical to `git rev-parse HEAD` — the run branch is
rooted at the working HEAD with no extra commits.

```
$ git rev-parse HEAD   # working-branch HEAD before any step runs
f849cdbfd31bfd74873423add204e14fb3ef5bfd

$ git branch --list 'jig/*'
+ jig/compose/run-20260807-183000-abc123de

$ git log --format='%H %s' -1 jig/compose/run-...   # tip == working HEAD
f849cdbfd31bfd74873423add204e14fb3ef5bfd init
```

## Artifact: Engine tests (new + regression) under -race

**What it proves:** The run-branch creation and its no-op path both behave, and
existing engine behavior is unregressed.

**Why it matters:** `setupRunBranch` runs on the scheduler goroutine at run start
for every run; a regression here would break all runs.

**Command:**

```bash
go test ./internal/engine -race -run 'TestRunBranch' -v
go test ./internal/engine -race
```

**Result summary:** `TestRunBranchCreatedAtWorkingHead` and
`TestRunBranchNoopWhenNoRepoRoot` PASS; the full `internal/engine` suite is `ok`
under `-race`.

```
=== RUN   TestRunBranchCreatedAtWorkingHead
--- PASS: TestRunBranchCreatedAtWorkingHead (0.19s)
=== RUN   TestRunBranchNoopWhenNoRepoRoot
--- PASS: TestRunBranchNoopWhenNoRepoRoot (0.08s)
PASS
ok  	jig/internal/engine	2.068s
```

## Reviewer Conclusion

The run-branch lifecycle is in place and correct: a run against a git repo leaves
an inspectable run branch rooted at the working HEAD, a persistence-off run is an
exact no-op, and the existing engine suite still passes under `-race`.
