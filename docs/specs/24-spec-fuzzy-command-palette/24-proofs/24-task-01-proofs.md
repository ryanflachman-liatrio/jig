# Task 01 Proofs - Fuzzy ranking and category-aware matching

## Task Summary

This task replaces the command palette's flat, order-preserving substring
filter with real fuzzy ranking (best match first) and inline highlighting of
the matched characters, and extends the match text to include each command's
section/category label so a query like "steps" surfaces every Steps command
even when no title contains that word literally.

## What This Task Proves

- `Model.refilter` ranks visible commands by `github.com/sahilm/fuzzy` match
  score (best match first), replacing catalog order while a filter is active.
- The match text for each command includes its category (the `prefix` passed
  to `FromBindings`), so a category-only query still returns the right
  commands.
- An empty filter falls back to the full catalog in its original order,
  matching today's behavior exactly.
- Disabled commands stay excluded regardless of how well they would score.
- `highlightMatches` wraps only the matched rune indexes within a title in the
  new `shared.Theme.Help.Match` style token, leaving the rest of the title
  untouched.

## Evidence Summary

- `go test ./internal/tui/palette/...` passes, including four new fuzzy-ranking
  tests and one highlight-wrapping test, alongside all pre-existing palette
  tests (no regression from the `Command`/`refilter` rewrite).
- A rendered `View()` frame (`artifacts/palette-demo.txt`) shows the query
  `swr` ranking "switch run" to the top with `s`, `w`, and `r` highlighted in
  place, over a catalog where "switch run" was not the first entry.
- `gofmt -l .`, `go vet ./...`, and `go build ./...` are all clean.

## Artifact: Fuzzy ranking, category matching, and highlighting unit tests

**What it proves:** `refilter` ranks by fuzzy score, matches on category text,
falls back to catalog order when the filter is empty, keeps disabled commands
excluded, and `highlightMatches` wraps exactly the given indexes.

**Why it matters:** These are the four functional requirements from spec Unit
1; each has a dedicated table-driven-style test rather than relying on
end-to-end UI inspection.

**Command:**

```bash
go test ./internal/tui/palette/... -v
```

**Result summary:** All 10 tests pass, including the four new fuzzy-ranking
tests (`TestRefilterRanksByFuzzyScore`, `TestRefilterMatchesCategory`,
`TestRefilterEmptyFallsBackToCatalogOrder`,
`TestRefilterExcludesDisabledRegardlessOfScore`) and the highlight test
(`TestHighlightMatchesWrapsGivenIndexes`), plus all five pre-existing palette
tests unaffected by the `Command`/`refilter` change.

```
=== RUN   TestPaletteFilterAndEsc
--- PASS: TestPaletteFilterAndEsc (0.00s)
=== RUN   TestPaletteEnterDispatchesKey
--- PASS: TestPaletteEnterDispatchesKey (0.00s)
=== RUN   TestPaletteEnterInvokesRunWhenSet
--- PASS: TestPaletteEnterInvokesRunWhenSet (0.00s)
=== RUN   TestPaletteEnterFallsBackToDispatchKeyWhenRunNil
--- PASS: TestPaletteEnterFallsBackToDispatchKeyWhenRunNil (0.00s)
=== RUN   TestFromBindingsSkipsDisabled
--- PASS: TestFromBindingsSkipsDisabled (0.00s)
=== RUN   TestRefilterRanksByFuzzyScore
--- PASS: TestRefilterRanksByFuzzyScore (0.00s)
=== RUN   TestRefilterMatchesCategory
--- PASS: TestRefilterMatchesCategory (0.00s)
=== RUN   TestRefilterEmptyFallsBackToCatalogOrder
--- PASS: TestRefilterEmptyFallsBackToCatalogOrder (0.00s)
=== RUN   TestRefilterExcludesDisabledRegardlessOfScore
--- PASS: TestRefilterExcludesDisabledRegardlessOfScore (0.00s)
=== RUN   TestHighlightMatchesWrapsGivenIndexes
--- PASS: TestHighlightMatchesWrapsGivenIndexes (0.00s)
=== RUN   TestParseKey
--- PASS: TestParseKey (0.00s)
PASS
ok  	jig/internal/tui/palette	0.395s
```

(`TestPaletteEnterInvokesRunWhenSet`/`TestPaletteEnterFallsBackToDispatchKeyWhenRunNil`
belong to Task 2.0's direct-execution path and are re-proven there; they show
here only because they live in the same package/test run.)

## Artifact: Rendered palette frame — ranked result with highlighted match

**What it proves:** End-to-end, `Model.View()` renders the best fuzzy match
first with the matched characters visibly highlighted, using a real catalog
where the best match ("switch run") is not the first, second, or third entry.

**Why it matters:** The unit tests prove the ranking/highlighting logic in
isolation; this is the visual proof that the rendered box actually looks
right — the terminal-capture equivalent of a screenshot for a TUI app (this
repo has no native screenshot tooling, so `View()` frames captured to a text
file are the established substitute, e.g.
`docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit3-nav.txt`).

**Artifact path:** `docs/specs/24-spec-fuzzy-command-palette/artifacts/palette-demo.txt`
(ANSI-stripped) and `palette-demo.ans` (raw ANSI, showing the distinct
highlight color escape codes applied only to the matched runes `s`, `w`, `r`).

**Result summary:** With a catalog of `["reset step", "resume step", "switch
run", "stop step", "open transcript", "copy transcript"]` (in that order) and
the query `swr`, the palette renders "switch run" as the sole, top-ranked,
cursor-selected row with `s`, `w`, and `r` highlighted — proving both the
ranking and highlighting requirements together, not just in isolation.

```
                ╭──────────────────────────────────────────────╮
                │  Commands                                    │
                │  > swr                                       │
                │  › switch run                                │
                │                                              │
                │  type to filter · enter run · esc            │
                ╰──────────────────────────────────────────────╯
```

## Artifact: Quality gates

**What it proves:** The new `sahilm/fuzzy` dependency, the `Command` field
additions, and the `Help.Match` style token compile cleanly, pass `go vet`,
and are correctly formatted.

**Command:**

```bash
gofmt -l . && go vet ./... && go build ./...
```

**Result summary:** All three commands produced no output (clean) and exited
successfully.

## Reviewer Conclusion

The palette's filter is now a real fuzzy ranker with category-aware match
text and inline highlighting, proven both at the unit level (ranking order,
category matching, empty-filter fallback, disabled-command exclusion,
highlight-index precision) and visually (a rendered frame showing the ranked,
highlighted result), with no regression to the five pre-existing palette
tests or the repository's quality gates.
