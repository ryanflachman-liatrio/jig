# Task 03 Proofs - Detail Integration and Chart Regression Safety

## Task Summary

This task completes the chart integration evidence for Spec 25. It adds a fixed-width crossing-reduced golden, exercises gate-label rendering at an extremely narrow requested width, verifies real horizontal viewport movement in the Detail screen, and covers failed-reload plus nil-workflow fallbacks.

## What This Task Proves

- The crossing-reduction layout produces three uncrossed vertical dependency pairs in a checked-in ANSI-stripped chart.
- Normal, conditional, loop, foreach, wide-label, and crossing-reduced fixtures all match deterministic renderer output.
- Narrow requested widths retain the node ID, gate glyph, and Unicode-aware truncated gate label without runaway canvas growth or panic.
- Detail chart mode supports actual left/right horizontal scrolling for a naturally wide graph.
- An unavailable workflow exits chart mode safely, disables the toggle, renders the invalid-workflow fallback, and tolerates a direct nil-workflow chart call.
- Focused packages, the full repository, vet, and formatting all pass.

## Evidence Summary

- `go test ./internal/tui/chart ./internal/tui/detail` passes the chart and Detail integration suites.
- `go test ./internal/tui/chart -run TestChartGolden` passes every fixed-width fixture.
- `go test ./...` passes the repository regression suite.
- `go vet ./...`, `gofmt -l .`, and `git diff --check` return success with no findings.

## Artifact: Crossing-reduced fixed-width chart

**What it proves:** A TOML order containing two avoidable crossing pairs renders as three aligned, uncrossed dependency pairs after within-rank reordering.

**Why it matters:** This is the human-reviewable rendering proof for Unit 1, complementing the exact crossing-count assertions in Task 1.

**Artifact path:** `internal/tui/chart/testdata/crossing_reduced.golden`

**Result summary:** `left → left_branch → left_result` and `right → right_branch → right_result` render as separate vertical lanes with no connector crossing.

~~~text
╭──────────────╮    ╭──────────────╮
│ left         │    │ right        │
╰──────────────╯    ╰──────────────╯
        │                   │
        ▼                   ▼
╭──────────────╮    ╭──────────────╮
│ left_branch  │    │ right_branch │
╰──────────────╯    ╰──────────────╯
~~~

## Artifact: Complete chart golden suite

**What it proves:** The intentionally retained fixtures cover non-gated linear nodes, fan-out/fan-in, conditional forward labels, route back-edge labels, static foreach markers, bounded validation labels, and crossing reduction.

**Why it matters:** These fixtures make unrelated visual regressions immediately observable.

**Command:**

~~~bash
go test ./internal/tui/chart -run TestChartGolden
~~~

**Result summary:** All fixtures matched.

~~~text
ok  	jig/internal/tui/chart	0.219s
~~~

**Reviewed fixture paths:**

- `internal/tui/chart/testdata/linear.golden`
- `internal/tui/chart/testdata/fanout_fanin.golden`
- `internal/tui/chart/testdata/conditional_loop.golden`
- `internal/tui/chart/testdata/foreach.golden`
- `internal/tui/chart/testdata/wide_labels.golden`
- `internal/tui/chart/testdata/crossing_reduced.golden`

Only `crossing_reduced.golden` was newly generated during Task 3; the other five intentional node-height/label updates were generated and reviewed during Task 2.

## Artifact: Chart and Detail integration tests

**What it proves:** Narrow gate labels remain visible and bounded, Detail chart content scrolls horizontally in both directions, and unavailable-workflow fallbacks are safe.

**Why it matters:** This validates the user-facing escape hatch when uniform nodes or a wide rank exceed the viewport.

**Command:**

~~~bash
go test ./internal/tui/chart ./internal/tui/detail
~~~

**Result summary:** Both focused packages passed.

~~~text
ok  	jig/internal/tui/chart	(cached)
ok  	jig/internal/tui/detail	(cached)
~~~

**Test sources:**

- `internal/tui/chart/render_test.go`
- `internal/tui/detail/detail_test.go`

## Artifact: Final repository quality gates

**What it proves:** Spec 25 remains compatible with the complete repository and satisfies required Go static-analysis and formatting standards.

**Why it matters:** The final implementation affects shared chart layout and rendering paths, so completion requires broader regression evidence.

**Commands:**

~~~bash
env GOCACHE=/private/tmp/jig-spec25-go-cache go test ./...
env GOCACHE=/private/tmp/jig-spec25-go-cache go vet ./...
gofmt -l .
git diff --check
~~~

**Result summary:** Every tested package passed. Vet, formatting, and diff checks exited successfully with no output.

~~~text
ok  	jig/internal/tui	3.597s
ok  	jig/internal/tui/chart	(cached)
ok  	jig/internal/tui/detail	(cached)
ok  	jig/internal/tui/monitor	3.710s
ok  	jig/internal/workflow	(cached)
~~~

## Reviewer Conclusion

The final evidence shows the chart remains deterministic and readable across normal, conditional, looping, foreach, gated, crossing-prone, and narrow-width cases, while the Detail viewport preserves its horizontal-scroll and unavailable-workflow contracts.
