# Task 02 Proofs - Monitor tool-exchange headers compose via RenderStatusLine

## Task Summary

This task rewrites the Monitor tool-exchange header at
`internal/tui/monitor/monitor_transcript_items_view.go` to compose every
row through `shared.RenderStatusLine`, deletes the appended state prose
(`" failed"`, `" · running"`, `" · incomplete"`), routes the error hint
into the Meta slot, applies selection emphasis to the title slot only, and
migrates the orphan `Result (unknown origin)` path to the same builder.
It closes FR-02.11 through FR-02.20 at the integration layer.

## What This Task Proves

- Every tool-exchange header row is a `shared.StatusLine` composed by
  `RenderStatusLine`; no other renderer path emits a header.
- The icon slot carries the exchange's state (running/pending/error/
  success/warning) via `shared.ToolStatusIcon(state, kind)`; the card
  border color reinforces the same signal.
- Settled success swaps in the tool's signature glyph; running exchanges
  keep the generic pending glyph regardless of kind (CC-4).
- Selection emphasizes the title only. The card frame, description, and
  meta are not re-wrapped in `TranscriptSelected`.
- The error hint from `toolErrorHint` appears in the Meta slot so it
  stays visible on collapsed error rows without occupying the title.
- Orphan `Result (unknown origin)` rows use the same `RenderStatusLine`
  path so the warning icon carries state on the flat orphan row.
- Slice-01's card cache identity works with the new header: transitioning
  a running exchange to settled success produces a different composed
  `header` string, which updates `transcriptRenderKey.header` and forces
  a fresh cache entry.
- With persistence off (`RunDir == ""`), the transcript body renders the
  empty-state banner and never enters `itemTranscriptBody`, so
  `RenderStatusLine` is not called and no card cache entries appear.

## Evidence Summary

Focused Monitor tests exercise every FR through the real
`itemTranscriptBody` render path. A broad grep-based regression asserts
that no state prose leaks into any header on any state. The slice-01 card
cache lifecycle tests continue to pass with the new header composition.

## Artifact: Header composition and grammar tests

**What it proves:** Every state × kind combination renders through
`RenderStatusLine`; the ANSI-stripped body carries the correct icon per
state; settled success emits the kind's signature glyph while running
keeps the generic pending glyph.

**Why it matters:** This is the authoritative check that Monitor now
speaks the four-slot grammar instead of appending prose to a fused
string.

**Command:**

```bash
go test ./internal/tui/monitor \
  -run 'TestToolExchangeHeader(CardStatesAndWidths|SignatureGlyphOnlyOnSettledSuccess|SelectedTitleOnly|ErrorHintInMeta|RunningToSuccessTransitionSwapsSignatureGlyph)|TestMonitorHeaderNoStateProseRegression|TestOrphanAndNonExchangeItemsStayFlat|TestToolExchangeRendersHeaderOnlyCardAndOrphanStaysFlat' \
  -count=1 -v
```

**Result summary:** PASS. Cases include
`TestToolExchangeHeaderCardStatesAndWidths`,
`TestToolExchangeHeaderSignatureGlyphOnlyOnSettledSuccess`,
`TestToolExchangeHeaderSelectedTitleOnly`,
`TestToolExchangeHeaderErrorHintInMeta`,
`TestMonitorHeaderNoStateProseRegression`,
`TestOrphanAndNonExchangeItemsStayFlat`,
`TestToolExchangeRendersHeaderOnlyCardAndOrphanStaysFlat`, and the
cache-identity transition
`TestToolExchangeHeaderRunningToSuccessTransitionSwapsSignatureGlyph`.

## Artifact: Cache lifecycle regressions

**What it proves:** Slice-01's card cache lifecycle regressions continue
to pass with the new composed header. Same-key page replacement, width
changes, step changes, and expansion toggles keep the cache bound to
`len(items)` and refresh output when the composed `header` differs.

**Why it matters:** The cache identity is `transcriptRenderKey.header`.
Any regression in the composed header string that broke the cache would
be visible here.

**Command:**

```bash
go test ./internal/tui/monitor \
  -run 'TestToolExchangeCardCache|TestTranscriptCardLineRanges|TestTranscriptCardCacheLifecycleBounded|TestTranscriptCardPageBoundsSearchExpansionAndClipboard' \
  -count=1 -v
```

**Result summary:** PASS.

## Artifact: Summary contract regression

**What it proves:** `summarizeActivity` keeps `icon`, `action`, and
`detail` distinct after the removal of the pre-fused `label`/`preview`
fields; the new `kind` field is populated for canonical tools.

**Why it matters:** The four-field summary is the presentation contract
the header composer relies on; a regression that collapses `action` and
`detail` would defeat the four-slot grammar.

**Command:**

```bash
go test ./internal/tui/monitor -run 'TestSummarize' -count=1 -v
```

**Result summary:** PASS. Cases include
`TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls` and
`TestSummarizeActivityKeepsSlotsDistinct`.

## Artifact: Persistence-off regression

**What it proves:** With `RunDir == ""`, `chatEntries` and `chatItems`
stay empty, `chatBody()` renders the "Persistence is off" banner,
`itemTranscriptBody()` returns an empty string, and the card cache is
untouched.

**Why it matters:** The status-line header path must not be entered when
there is no data. This guards the primitive against synthesizing a card
row from an empty page.

**Command:**

```bash
go test ./internal/tui/monitor \
  -run '^TestPersistenceOffKeepsEmptyStateOutOfStatusLineHeader$' \
  -count=1 -v
```

**Result summary:** PASS.

## Reviewer Conclusion

Monitor's tool-exchange header is now built exclusively via the shared
four-slot grammar. State prose is gone from every header code path,
selection emphasizes only the title, the error hint has moved to the meta
slot, and orphan rows use the same builder. Slice-01's cache, width, and
line-range regressions continue to pass, and the persistence-off path
never enters `RenderStatusLine`.
