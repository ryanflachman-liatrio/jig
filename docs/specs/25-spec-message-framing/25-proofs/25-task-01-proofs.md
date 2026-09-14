# Task 01 Proofs - Shared row-tinting helper extraction (CC-9 reuse)

## Task Summary

This task extracts the SGR background-reset-reapply logic out of
`internal/tui/shared/card.go`'s `finishRow` into an exported
`shared.TintRow` helper, so the upcoming user-bubble renderer (Task 2.0) can
reuse the proven CC-9 tint-over-glamour rule instead of duplicating it.

## What This Task Proves

- `TintRow` produces byte-identical output to the previous inline
  `finishRow` logic — proven indirectly by the unchanged `card_test.go`
  suite passing after the extraction.
- The parameter-aware reset rule (treat `0`/empty/`49` as resets, but not a
  literal `0` inside an extended RGB or palette-indexed color component)
  survives the move into a general-purpose, row-agnostic function.

## Evidence Summary

- `go test ./internal/tui/shared/...` passes, including the pre-existing
  `card_test.go` suite (unchanged) and the new `tint_test.go` suite.
- `gofmt -l` reports no formatting issues on the changed files.
- `go vet ./internal/tui/shared/...` reports no issues.

## Artifact: Existing card test suite passes unchanged after extraction

**What it proves:** Moving `sgrSequence`/`sgrResetsBackground`/the
reapply loop into `shared.TintRow` and having `Card.finishRow` delegate to
it introduces no behavior drift.

**Why it matters:** `card_test.go` was not modified as part of this task;
a passing run is direct evidence the refactor is behavior-preserving.

**Command:**

~~~bash
go test ./internal/tui/shared/... -v -run TestRenderCard
~~~

**Result summary:** All `RenderCard`-family subtests pass unchanged.

~~~text
=== RUN   TestRenderCardTint
--- PASS: TestRenderCardTint (0.00s)
=== RUN   TestRenderCardBorderStates
--- PASS: TestRenderCardBorderStates (0.00s)
PASS
ok  	jig/internal/tui/shared	0.4s
~~~

(Full local run: `go test ./internal/tui/shared/...` → `ok`.)

## Artifact: New `TintRow` unit tests cover the reset-parameter rule

**What it proves:** `TintRow` reapplies the given background after a bare
reset (`\x1b[0m`), an empty-parameter reset (`\x1b[m`), and a
background-only reset (`\x1b[49m`); it leaves a non-reset SGR sequence
untouched; and it does not treat a literal `0` inside an extended RGB
(`38;2;0;0;0`) or palette-indexed (`48;5;0`) color component as a reset.

**Why it matters:** This is the exact rule FR-09.10 depends on for the
bubble to survive glamour's fenced-code-block rendering in Task 2.0.

**Command:**

~~~bash
go test ./internal/tui/shared/... -run TestTintRow -v
~~~

**Result summary:** All four `TintRow` tests (and their subtests) pass.

~~~text
=== RUN   TestTintRowReappliesAfterBackgroundResets
=== RUN   TestTintRowReappliesAfterBackgroundResets/bare_reset
=== RUN   TestTintRowReappliesAfterBackgroundResets/empty_params
=== RUN   TestTintRowReappliesAfterBackgroundResets/background_only_reset
--- PASS: TestTintRowReappliesAfterBackgroundResets (0.00s)
    --- PASS: TestTintRowReappliesAfterBackgroundResets/bare_reset (0.00s)
    --- PASS: TestTintRowReappliesAfterBackgroundResets/empty_params (0.00s)
    --- PASS: TestTintRowReappliesAfterBackgroundResets/background_only_reset (0.00s)
=== RUN   TestTintRowLeavesNonResetSGRUntouched
--- PASS: TestTintRowLeavesNonResetSGRUntouched (0.00s)
=== RUN   TestTintRowIgnoresLiteralZeroInExtendedColor
--- PASS: TestTintRowIgnoresLiteralZeroInExtendedColor (0.00s)
=== RUN   TestTintRowIgnoresLiteralZeroInPaletteColor
--- PASS: TestTintRowIgnoresLiteralZeroInPaletteColor (0.00s)
PASS
ok  	jig/internal/tui/shared	0.46s
~~~

## Artifact: Package-level test, vet, and format checks

**What it proves:** The extraction leaves the `internal/tui/shared`
package building, testing, and vetting cleanly.

**Command:**

~~~bash
gofmt -l internal/tui/shared/tint.go internal/tui/shared/tint_test.go internal/tui/shared/card.go
go vet ./internal/tui/shared/...
go test ./internal/tui/shared/...
~~~

**Result summary:** `gofmt -l` printed no files (all formatted); `go vet`
reported no issues; the full package test suite passed.

~~~text
ok  	jig/internal/tui/shared	0.531s
~~~

## Reviewer Conclusion

`shared.TintRow` is now the single, tested source of truth for
background-reset-reapply behavior, consumed today by `Card.finishRow` with
identical output to before, and ready for the bubble renderer in Task 2.0
to reuse without re-deriving the SGR reset rule.
