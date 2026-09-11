# Task 01 Proofs - Deterministic Within-Rank Crossing Reduction

## Task Summary

This task adds a pure, bounded barycentric sweep to the chart layout pass. The layout may reorder nodes only inside their existing longest-path rank, considers only forward `depends_on` edges, retains TOML step index as the final score tie-breaker, and accepts a candidate order only when it strictly reduces the measured forward-edge crossings.

## What This Task Proves

- A crossing-prone three-rank workflow is reduced from two forward-edge crossings to zero without changing node ranks or dependency endpoints.
- Already-uncrossed layouts remain in their existing order.
- Equal barycentric scores retain TOML order, and repeated layouts produce identical rank order.
- Conditional edge decoration and bounded route back-edges remain distinct from the crossing objective.

## Evidence Summary

- The focused `TestLayoutChart` suite passes all crossing, rank, topology, determinism, conditional-edge, and back-edge cases.
- The complete repository test suite passes.
- `go vet ./...` passes and `gofmt -l .` reports no unformatted Go files.

## Artifact: Focused chart layout tests

**What it proves:** The pure layout implementation reduces the fixture's crossing count from two to zero, preserves rank membership and forward-edge endpoints, remains stable across repeated calls, uses TOML order for equal scores, and keeps route back-edges outside the forward edge set.

**Why it matters:** These are the observable Unit 1 requirements and directly exercise the crossing heuristic without renderer geometry or terminal styling.

**Command:**

~~~bash
go test ./internal/tui/chart -run TestLayoutChart
~~~

**Result summary:** The focused layout suite passed.

~~~text
ok  	jig/internal/tui/chart	0.268s
~~~

**Test source:** `internal/tui/chart/layout_test.go`

## Artifact: Full repository regression suite

**What it proves:** The crossing-reduction change does not regress other chart, Detail-screen, workflow, engine, runner, or CLI behavior.

**Why it matters:** Layout ordering feeds the renderer and Detail viewport, so repository-wide coverage guards integrations beyond the pure helper.

**Command:**

~~~bash
env GOCACHE=/private/tmp/jig-spec25-go-cache go test ./...
~~~

**Result summary:** Every package passed; packages without tests were reported normally.

~~~text
ok  	jig/cmd/jig	0.495s
ok  	jig/internal/tui	1.349s
ok  	jig/internal/tui/chart	(cached)
ok  	jig/internal/tui/detail	(cached)
ok  	jig/internal/workflow	(cached)
~~~

## Artifact: Static quality gates

**What it proves:** The implementation satisfies the repository's Go vet and formatting requirements.

**Why it matters:** Deterministic layout code is production code; passing static analysis and canonical formatting keeps the change aligned with repository standards.

**Commands:**

~~~bash
env GOCACHE=/private/tmp/jig-spec25-go-cache go vet ./...
gofmt -l .
~~~

**Result summary:** Both commands exited successfully with no output.

~~~text
(no output)
~~~

## Reviewer Conclusion

The evidence demonstrates that Task 1 implements deterministic, rank-preserving crossing reduction over forward dependencies, improves the crossing-prone fixture, preserves stable/tied layouts and edge semantics, and passes focused plus repository-wide quality gates.
