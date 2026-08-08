# Task 1.0 Proofs — Dependency Closure and Rewind/Replay Plan

## Task Summary

This task proves that the engine can compute, without touching any run state,
the exact set of steps to reset (`closureOf`) and the git operations needed to
rewind the run branch while preserving independent work (`rewindPlan`). These
are the pure-logic foundations for the live reset execution in Task 3.0.

## What This Task Proves

- `closureOf(targetID)` returns the target plus every step that transitively
  depends on it, in declaration order, and excludes independent parallel
  branches.
- `rewindPlan(targetID)` identifies the correct git rewind point and the ordered
  list of survivor commits to cherry-pick back after the reset.
- Edge cases are covered: linear-tip reset (no survivors), independent-step
  reset (survivors on the other side), and read-only step with no commit (no-op).

## Evidence Summary

All 6 test cases pass. Four sub-tests cover the fan-out, linear, no-commit, and
independent-branch scenarios documented in the spec's Unit C1 proof artifacts.

## Artifact: TestResetClosure — forward-reachability closure

**What it proves:** `closureOf` correctly computes the reset set for each step
in a fan-out + independent-branch graph (`A→B→C`, `D` standalone).

**Why it matters:** The closure drives everything downstream — which steps get
reset, which commits to count as closure members, which to treat as survivors.

**Command:** `go test -v -count=1 -run TestResetClosure ./internal/engine/`

**Result summary:** All four closure assertions pass — resetting A returns
`[a,b,c]` (D excluded), resetting B returns `[b,c]`, resetting C returns `[c]`,
resetting D returns `[d]`.

## Artifact: TestRewindPlan — git rewind point and survivor list

**What it proves:** `rewindPlan` correctly identifies the rewind commit and
survivor SHAs for each reset scenario, using a real git repository with scripted
`jig-step:` trailer commits.

**Why it matters:** An incorrect rewind point would either over-revert
independent work (if too early) or under-revert the target closure (if too late).
An incorrect survivor list would lose independent branches' commits.

**Command:** `go test -v -count=1 -run TestRewindPlan ./internal/engine/`

**Sub-test results:**
- `fanout_reset_a` — rewindTo = runBaseSHA (before A), survivors = [D's SHA] ✓
- `linear_reset_b` — rewindTo = D's SHA (just before B), survivors = [] ✓
- `no_commit_read_only` — no commit for C yet → `("", nil)` ✓
- `independent_reset_d` — rewindTo = A's SHA, survivors = [B's SHA] ✓

**Raw output:** See `C1.0-reset-plan.txt`

```
=== RUN   TestResetClosure
--- PASS: TestResetClosure (0.00s)
=== RUN   TestRewindPlan
=== RUN   TestRewindPlan/fanout_reset_a
=== RUN   TestRewindPlan/linear_reset_b
=== RUN   TestRewindPlan/no_commit_read_only
=== RUN   TestRewindPlan/independent_reset_d
--- PASS: TestRewindPlan (0.14s)
    --- PASS: TestRewindPlan/fanout_reset_a (0.00s)
    --- PASS: TestRewindPlan/linear_reset_b (0.00s)
    --- PASS: TestRewindPlan/no_commit_read_only (0.00s)
    --- PASS: TestRewindPlan/independent_reset_d (0.00s)
PASS
ok  	jig/internal/engine	0.327s
```

## Reviewer Conclusion

`closureOf` and `rewindPlan` correctly implement the pure-logic layer of spec
Unit C1. The forward-reachability closure matches the spec's definition, and the
rewind/survivor plan matches the expected outputs for all documented scenarios
including the fan-out case (commit order A,D,B → rewind-to-base, survivors=[D]).
