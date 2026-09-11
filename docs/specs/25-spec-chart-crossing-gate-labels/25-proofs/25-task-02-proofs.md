# Task 02 Proofs - Compact Validation-Gate Labels in Chart Nodes

## Task Summary

This task extends every chart node to a uniform three-content-line box and renders the existing compact `[step.validate]` description on the third line. Gate labels use the shared chart theme, participate in the existing 18-cell bounded width calculation, and are truncated with the shared Unicode-aware title helper.

## What This Task Proves

- Command, output-schema, output-contains, and output-exists gates display their established compact descriptions inside chart nodes.
- Gate labels remain bounded and Unicode-aware.
- Gate, loop, foreach, and retry markers remain composed on the node type line.
- Gated and non-gated boxes use identical height, keeping rank and connector geometry deterministic.
- Conditional and loop labels survive the geometry change in checked-in ANSI-stripped fixtures.

## Evidence Summary

- Focused gate/node tests pass for all validation forms, Unicode truncation, marker composition, and uniform box height.
- The chart golden suite passes and visibly includes `go build` plus a bounded `go build ./... &&…` label.
- The full repository suite, `go vet`, formatting, and diff checks pass.

## Artifact: Focused validation-gate rendering tests

**What it proves:** Each supported validation form is derived and rendered inside the node, the gate glyph remains visible, long Unicode text truncates to the box limit, all existing markers compose, and non-gated boxes retain the same height.

**Why it matters:** This directly covers the Unit 2 behavior independently of individual golden fixture dimensions.

**Command:**

~~~bash
go test ./internal/tui/chart -run 'Test.*Gate|Test.*Node'
~~~

**Result summary:** All focused renderer cases passed.

~~~text
ok  	jig/internal/tui/chart	(cached)
~~~

**Test source:** `internal/tui/chart/render_test.go`

## Artifact: Checked-in chart fixtures

**What it proves:** Fixed-width rendered charts reflect the uniform box geometry while retaining conditional-edge, route-back-edge, foreach, and non-gated-node content. The gated command nodes visibly contain their validation descriptions.

**Why it matters:** The ANSI-stripped fixtures provide fast human-reviewable evidence of the actual terminal composition.

**Artifact paths:**

- `internal/tui/chart/testdata/conditional_loop.golden`
- `internal/tui/chart/testdata/wide_labels.golden`
- `internal/tui/chart/testdata/linear.golden`
- `internal/tui/chart/testdata/fanout_fanin.golden`
- `internal/tui/chart/testdata/foreach.golden`

**Command:**

~~~bash
go test ./internal/tui/chart -run TestChartGolden
~~~

**Result summary:** All regenerated fixtures match deterministic renderer output.

~~~text
ok  	jig/internal/tui/chart	0.214s
~~~

**Visible gated-node excerpt:**

~~~text
╭────────────────────╮
│ impl               │
│ command ⇢          │
│ go build ./... &&… │
╰────────────────────╯
~~~

## Artifact: Repository regression and quality gates

**What it proves:** The taller node geometry and shared-theme gate line do not regress chart consumers or other repository behavior.

**Why it matters:** Connector anchors, canvas height, and Detail integration all depend on renderer dimensions.

**Commands:**

~~~bash
env GOCACHE=/private/tmp/jig-spec25-go-cache go test ./...
env GOCACHE=/private/tmp/jig-spec25-go-cache go vet ./...
gofmt -l .
git diff --check
~~~

**Result summary:** The full repository test suite passed. Vet, formatting, and diff checks exited successfully with no output.

~~~text
ok  	jig/internal/tui/chart	0.707s
ok  	jig/internal/tui/detail	0.528s
ok  	jig/internal/workflow	(cached)
~~~

## Reviewer Conclusion

The artifacts show that every supported deterministic validation gate is now visible inside a bounded, uniformly sized chart node using shared theme ownership, with existing markers and connector labels preserved.
