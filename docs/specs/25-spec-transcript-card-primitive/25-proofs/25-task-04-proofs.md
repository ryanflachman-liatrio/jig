# Task 04 Proofs - Stable navigation and bounded card caching

## Task Summary

This task proves card output participates safely in the existing item-render cache while line ranges remain freshly derived, navigation remains stable across taller items, and page/resize/step transitions cannot retain stale or unbounded variants.

## What This Task Proves

- Fresh and cached renders produce identical item line ranges, including both card rows and expanded details while excluding inter-item spacing.
- Navigation walks successive cards and keeps a tall expanded exchange's actionable header visible.
- Page replacement refreshes same-key running/success/failure and changed-header output while preserving an explicitly selected item.
- Selection, expansion, repeated widths, pruning, and step transitions retain at most one current card entry per loaded exchange.
- A 300-entry synthetic page preserves normalization, search membership, expansion/detail bounds, clipboard source data, and repeated-render stability without render-time I/O.
- Cached 300-entry repaint performance is well inside the epic's 100 ms budget on the recorded machine.

## Evidence Summary

Focused cache/navigation tests pass, existing paging/search/detail/clipboard regressions pass as part of the Monitor suite, and three benchmark samples measured approximately 0.363–0.365 ms/op.

## Artifact: Cache and line-range behavior

**What it proves:** Cached/fresh ranges, tall-item navigation, invalidation identity, selection preservation, and cache bounds are enforced directly.

**Why it matters:** Header cards add a row to each tool item; stale range or cache data would make navigation target the wrong terminal rows or show obsolete status.

**Command:**

```bash
go test ./internal/tui/monitor -run 'Test.*(CardCache|LineRange|TranscriptCard|PageReplacement|Resize)' -count=1 -v
```

**Result summary:** PASS for line ranges, cache lifecycle, the 300-entry bounded page, same-key replacement, and resize behavior.

```text
--- PASS: TestTranscriptCardLineRangesCachedAndFresh
--- PASS: TestTranscriptCardCacheLifecycleBounded
--- PASS: TestTranscriptCardPageBoundsSearchExpansionAndClipboard
--- PASS: TestToolExchangeCardCacheRefreshesOnPageReplacementAndWidthChange
PASS
ok  jig/internal/tui/monitor
```

## Artifact: Existing bounded transcript regressions

**What it proves:** File-backed paging, tool-boundary completion, filtered membership, detail limits, clipboard bounds, and persistence-off behavior remain intact.

**Why it matters:** The synthetic render benchmark does not replace the repository's established filesystem-backed paging tests.

**Command:**

```bash
go test ./internal/tui/monitor -run 'Test(TranscriptPaging|TranscriptPage|TranscriptSearch|TranscriptFilter|BoundTranscriptDetail|ClipboardTranscript|ClipboardItem|TranscriptPersistenceOff)' -count=1 -v
```

**Result summary:** Covered by the passing Monitor package suite and rerun at this parent checkpoint.

## Artifact: 300-entry cached repaint benchmark

**What it proves:** Repeated rendering with populated card cache remains far below the repaint budget for a maximum-sized synthetic page.

**Why it matters:** The cache exists to keep transcript navigation responsive at the bounded page limit.

**Artifact path:** `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-4-benchmark.txt`

**Result summary:** Three samples measured 362,621–364,933 ns/op with 909,007–909,008 B/op and 2,587 allocs/op; the slowest sample was approximately 0.365 ms versus the 100 ms budget.

## Reviewer Conclusion

Card rendering stays fresh, line-aware, bounded to one variant per loaded exchange, and comfortably within the representative repaint budget.
