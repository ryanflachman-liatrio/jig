# Task 02 Proofs — Step worktrees off run HEAD + squash-per-step integration

## Task Summary

Task 2.0 is the core code-composition slice. A mutating step's worktree now
branches off the **run branch's current HEAD at dispatch time** (not repo-root
HEAD), and on successful completion the step is **squash-merged into the run
branch as exactly one commit** tagged `jig-step: <stepID>`. The scheduler keeps
`stepCommits[stepID] = sha`, reconstructable from the run-branch trailers.
Integration runs on the single-writer scheduler goroutine, so parallel steps
integrate serially and the run branch stays a single linear history.

## What This Task Proves

- Downstream steps see upstream code: in `A → B`, B's worktree contains `file_a`.
- Each mutating step contributes exactly one `jig-step:`-tagged commit, in
  declaration order; read-only steps contribute none.
- The stepID→sha map is rebuildable from the run-branch history alone.
- A step that fails and succeeds on retry (reusing its worktree) still integrates
  exactly once — the FLAG-1 regression guard.

## Evidence Summary

- `TestStepsComposeOnCode`, `TestReadOnlyStepProducesNoCommit`,
  `TestStepCommitMapReconstructable`, `TestStepReintegratesAfterRetry` all PASS
  under `-race`.
- The full `internal/engine` suite and `go test ./...` are green; `gofmt`/`vet` clean.
- The run-branch log shows two tagged commits (`b`, `a`) over `init`, and B's
  step tree contains `file_a`.

## Artifact: Run-branch composition log

**What it proves:** One `jig-step:`-tagged commit per mutating step in declaration
order, and that B's worktree (branched off the run branch after A integrated)
contains A's `file_a`.

**Why it matters:** This is the observable evidence of code composition and the
addressable, linear per-step history the later reset foundation depends on.

**Artifact path:** `06-proofs/A1.0-run-branch-log.txt`

**Result summary:** `git log --format=%s` reads `b`, `a`, `init`; each step commit
carries its `jig-step` trailer; `git ls-tree jig/compose/b` lists `file_a`
alongside `file_b`.

```
$ git log --format='%s' <run-branch>
b
a
init

$ git log --format='%H  jig-step=%(trailers:key=jig-step,valueonly)' <run-branch>
0f1ce3c...  jig-step=b
1547388...  jig-step=a
82ee77c...  jig-step=

$ git ls-tree --name-only jig/compose/b
file_a
file_b
seed.txt
```

## Artifact: Engine tests (composition, read-only, map, retry) under -race

**What it proves:** All four Unit-A1 behaviors plus the FLAG-1 retry
re-integration guard hold, with no regression to the existing engine suite.

**Command:**

```bash
go test ./internal/engine -race -run 'TestSteps|TestReadOnly|TestStepCommit|TestStepReintegrates|TestRunBranch' -v
go test ./internal/engine -race
```

**Result summary:** All six tests PASS; the full engine suite is `ok` under `-race`.

```
--- PASS: TestStepsComposeOnCode (0.35s)
--- PASS: TestReadOnlyStepProducesNoCommit (0.14s)
--- PASS: TestStepCommitMapReconstructable (0.34s)
--- PASS: TestStepReintegratesAfterRetry (0.21s)
ok  	jig/internal/engine	2.887s
```

## Reviewer Conclusion

Steps compose on each other's code through the run branch, each contributing one
addressable tagged commit in a serialized linear history; read-only steps stay
inert; the commit map is reconstructable; and retry re-integration is safe. The
integration mechanic is in place for the conflict gate (Task 3) and final merge
(Task 4) to build on.
