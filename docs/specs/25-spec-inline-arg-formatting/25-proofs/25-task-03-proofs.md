# Task 03 Proofs - Meta-slot secondary arguments for known kinds (grep)

## Task Summary

This task extended `grep`'s curated summarization with a `meta []string`
field on `toolCallSummary`: `grep`'s existing curated `pattern` detail stays
exactly as it was in the `Description` slot, while its secondary arguments
(`path`, `case`, `gitignore`) are rendered via `formatArgsInline` into the
status line's `Meta` slot. Every other per-kind mapping is untouched.

## What This Task Proves

- A `grep` exchange with `pattern`, `path`, and `case` arguments keeps the
  curated pattern in the title/description position (`Search: TODO`,
  unchanged from before this feature) and shows `path=`/`case=` in the Meta
  position — not folded into, or replacing, the curated detail.
- An error row shows only its error hint, never a stale argument preview
  alongside it (Meta's error-hint-wins precedence, extended for this new
  source).
- No other per-kind mapping test regressed.

## Evidence Summary

Both the unit-level test (`summarizeActivity` called directly) and the
integration-level test (through the real `itemTranscriptBody` render path,
via `newMonitorWithSteps`/`setChatPage`) pass. The integration-level test is
what caught the Input-restoration gap fixed in task 2.0 — the unit-level test
alone would not have.

## Artifact: Unit-level grep summary test

**What it proves:** `summarizeActivity`'s grep case keeps `detail` unchanged
and populates `meta` with the secondary arguments.

**Command:**

```bash
go test ./internal/tui/monitor -run TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta -v
```

**Result summary:** Pass — `detail == "TODO"`, `meta` contains `path=` and
`case=` entries.

```
=== RUN   TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta
--- PASS: TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta (0.00s)
```

## Artifact: Full header-composition and summarize suite

**What it proves:** The rendered header (through the real card/border
pipeline, not just the summary struct) shows `Search: TODO` unchanged plus
`path=`/`case=` in Meta, and every pre-existing `TestToolExchangeHeader*` and
`TestSummarize*` case still passes.

**Why it matters:** This is the proof that the feature works in the actual
render path a user sees, not only against the summarization function in
isolation.

**Command:**

```bash
go test ./internal/tui/monitor -run 'TestToolExchangeHeader|TestSummarize' -v
```

**Result summary:** All 10 test functions (and their subtests) pass,
including the new `TestToolExchangeHeaderGrepMetaAlongsideCuratedPattern`.

```
=== RUN   TestToolExchangeHeaderGrepMetaAlongsideCuratedPattern
--- PASS: TestToolExchangeHeaderGrepMetaAlongsideCuratedPattern (0.00s)
=== RUN   TestToolExchangeHeaderErrorHintInMeta
--- PASS: TestToolExchangeHeaderErrorHintInMeta (0.00s)
PASS
ok  	jig/internal/tui/monitor	0.523s
```

(Full output includes `TestToolExchangeHeaderCardStateMapping`,
`TestToolExchangeHeaderCardStatesAndWidths`,
`TestToolExchangeHeaderSignatureGlyphOnlyOnSettledSuccess`,
`TestToolExchangeHeaderSelectedTitleOnly`,
`TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes`,
`TestToolExchangeHeaderRunningToSuccessTransitionSwapsSignatureGlyph`, and
the `TestSummarize*` group — all passing.)

## Reviewer Conclusion

Grep's curated pattern detail is unchanged and its secondary arguments now
surface in the Meta slot, on both a settled success row and correctly
suppressed on an error row, with no regression to any other tool kind's
header.
