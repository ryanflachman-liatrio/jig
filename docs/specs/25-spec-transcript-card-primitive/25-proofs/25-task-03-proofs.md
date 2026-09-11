# Task 03 Proofs - Truthful Monitor tool-exchange header cards

## Task Summary

This task converts exactly normalized tool exchanges to tinted, header-only cards while preserving the existing summary grammar, selection cue, expansion details, structured edits, clipboard source data, and persistence-off behavior.

## What This Task Proves

- Successful, failed, running use-only, and terminal incomplete use-only exchanges map to success, error, running, and warning card states.
- Selected and unselected outside prefixes are each measured once and applied consistently to both card rows at narrow and wide widths.
- Header-only exchanges emit exactly a top and bottom row with no synthetic body row.
- Orphan results, text, system, thinking, and unsupported items retain flat presentation.
- Expanded details and structured new-code output remain below the header card, while clipboard payloads remain raw and undecorated.
- Persistence-off loading remains empty and never populates the card cache.

## Evidence Summary

The focused Monitor integration tests and full Monitor package test pass. The cases use fabricated transcript entries and assert visible widths with `lipgloss.Width`.

## Artifact: Focused exchange-card behavior

**What it proves:** State mapping, header grammar, two-row rendering, prefixes, non-exchange regressions, expansion, clipboard, and persistence-off behavior are executable and passing.

**Why it matters:** This is the user-facing integration seam where the shared primitive must preserve the Monitor's established transcript semantics.

**Command:**

```bash
go test ./internal/tui/monitor -run 'Test.*(ToolExchange|HeaderCard|StructuredEdit|Orphan|PersistenceOff|ClipboardItem)' -count=1 -v
```

**Result summary:** PASS for all state/width combinations at transcript widths 40 and 72, selected/unselected cards, flat orphan/non-exchange kinds, structured edits, clipboard payloads, cache refresh smoke coverage, and persistence-off rendering.

```text
--- PASS: TestToolExchangeHeaderCardStateMapping
--- PASS: TestToolExchangeHeaderCardStatesAndWidths
--- PASS: TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes
--- PASS: TestOrphanAndNonExchangeItemsStayFlat
--- PASS: TestToolExchangeRendersHeaderOnlyCardAndOrphanStaysFlat
--- PASS: TestStructuredEditShowsNewCodeInDefaultOpenCard
--- PASS: TestClipboardItemPayloads
--- PASS: TestTranscriptPersistenceOffBuildsNoItems
PASS
ok  jig/internal/tui/monitor
```

## Artifact: Full Monitor package regression

**What it proves:** The card integration does not break other Monitor rendering, update, search, paging, gate, or clipboard behavior covered by the package suite.

**Why it matters:** The transcript item renderer is shared by several navigation and operational paths.

**Command:**

```bash
go test ./internal/tui/monitor -count=1
```

**Result summary:** PASS.

```text
ok  jig/internal/tui/monitor
```

## Reviewer Conclusion

The Monitor now frames tool exchanges according to their truthful display state while keeping every non-exchange and evidence-bearing behavior on its prior path.
