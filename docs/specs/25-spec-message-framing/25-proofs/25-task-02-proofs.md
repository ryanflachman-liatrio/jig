# Task 02 Proofs - User bubble framing replaces the `User` label

## Task Summary

This task removes the literal `User` label from the transcript body and
replaces it with a full-content-width background bubble: operator input is
now identified by tint alone, matching omp parity. Assistant prose is
unchanged — same offset, no tint. The dead `Theme.Chat.UserGuidance` style
and its sole consumer (`writeUserGuidance`) are removed.

## What This Task Proves

- No literal `User` label remains anywhere in a rendered user-role text item.
- The bubble covers the full panel content width, including trailing fill,
  with one tinted blank row above and below the content (FR-09.1/FR-09.2).
- Assistant text renders exactly as before: same leading offset, no tint
  (FR-09.3).
- The bubble survives glamour's background-resetting SGR sequences inside a
  fenced code block, via the `shared.TintRow` helper from Task 1.0
  (FR-09.10).
- The bubble's padding rows survive `trimStructuralBlankEdges` because they
  carry an SGR escape rather than being plain blank lines (FR-09.9).
- Selection places the cursor-bar prefix on every bubble row, matching
  `prefixCardRows` (FR-09.13).
- The bubble's background resolves to the existing `hexBBQ` token; no new
  palette constant was added (FR-09.11).
- `Theme.Chat.UserGuidance` and its dead consumer are gone; the binary still
  builds (FR-09.12).

## Evidence Summary

- `go test ./internal/tui/monitor/... -run <bubble tests>` — 6/6 pass.
- `go build ./cmd/jig` succeeds after removing `UserGuidance` and
  `monitor_transcript_view.go`.
- `go test ./... && go vet ./...` pass at the repository root — no
  regressions in the wider suite.
- A rendered capture shows the end state: a tinted user bubble above
  untinted assistant prose at the same offset.

## Artifact: Bubble unit tests (FR-09.1, FR-09.2, FR-09.3, FR-09.9, FR-09.10, FR-09.11, FR-09.13)

**What it proves:** Each functional requirement in this unit has a direct,
named test in `internal/tui/monitor/monitor_transcript_bubble_test.go`.

**Why it matters:** These are the requirement-to-test mappings promised in
the Phase 2 planning audit; a reviewer can match test name to FR directly.

**Command:**

~~~bash
go test ./internal/tui/monitor/... -v -run \
  'TestUserTextItemHasNoLabelAndTintedPadding|TestAssistantAndUserTextShareLeadingOffset|TestUserBubbleSurvivesFencedCodeReset|TestUserBubbleSurvivesStructuralEdgeTrim|TestSelectedUserBubbleCarriesPrefixOnEveryRow|TestUserBubbleBackgroundIsHexBBQ'
~~~

**Result summary:** All 6 tests pass.

~~~text
=== RUN   TestUserTextItemHasNoLabelAndTintedPadding
--- PASS: TestUserTextItemHasNoLabelAndTintedPadding (0.00s)
=== RUN   TestAssistantAndUserTextShareLeadingOffset
--- PASS: TestAssistantAndUserTextShareLeadingOffset (0.00s)
=== RUN   TestUserBubbleSurvivesFencedCodeReset
--- PASS: TestUserBubbleSurvivesFencedCodeReset (0.00s)
=== RUN   TestUserBubbleSurvivesStructuralEdgeTrim
--- PASS: TestUserBubbleSurvivesStructuralEdgeTrim (0.00s)
=== RUN   TestSelectedUserBubbleCarriesPrefixOnEveryRow
--- PASS: TestSelectedUserBubbleCarriesPrefixOnEveryRow (0.00s)
=== RUN   TestUserBubbleBackgroundIsHexBBQ
--- PASS: TestUserBubbleBackgroundIsHexBBQ (0.00s)
PASS
ok  	jig/internal/tui/monitor	0.516s
~~~

## Artifact: Build succeeds after removing `UserGuidance` and its dead consumer

**What it proves:** Removing `Theme.Chat.UserGuidance` and deleting
`monitor_transcript_view.go` (its only consumer, `writeUserGuidance`, had no
remaining callers) leaves no dangling reference (FR-09.12).

**Command:**

~~~bash
go build ./cmd/jig
~~~

**Result summary:** Build succeeds with no output (no errors).

## Artifact: Repository-wide regression pass

**What it proves:** The bubble change and style removal introduce no
regressions in the wider `internal/tui` suite or the rest of the repository
(Success Metric 3).

**Command:**

~~~bash
go build ./... && go vet ./... && go test ./...
~~~

**Result summary:** All packages report `ok`; `go vet` reports no issues.

~~~text
ok  	jig/internal/tui	3.462s
ok  	jig/internal/tui/monitor	2.948s
ok  	jig/internal/tui/shared	(cached)
... (all other packages ok)
~~~

## Artifact: Rendered capture — user bubble above untinted assistant prose

**What it proves:** The end-to-end visual state: a full-width tinted user
bubble ("Can you check the failing test...") immediately above untinted
assistant prose ("I found the issue...") at the same horizontal offset, with
no `User` label anywhere.

**Why it matters:** This is the human-readable confirmation that the
mechanical test assertions correspond to what an operator actually sees.

**Command:** (`transcriptInnerW=60`, first item selected)

~~~text
▌                                                             
▌   Can you check the failing test in                         
▌   payment_test.go and fix it?                               
▌                                                             

    I found the issue: the mock clock wasn't
  advanced before the assertion. Fixed in 
  payment_test.go.                        
~~~

**Result summary:** The plain-text rendering (ANSI stripped for readability)
shows the user turn as a padded block (blank/content/content/blank) with a
selection bar, immediately followed by unindented, untinted assistant prose
starting at the same content column. No `User` or `Assistant` label appears
anywhere.

## Reviewer Conclusion

The transcript body no longer emits a literal `User` label for operator
input; role is now carried entirely by the bubble's background tint, which
extends across the full panel width, survives glamour's fenced-code SGR
resets and the structural edge trim, and composes correctly with selection.
Assistant prose is provably unaffected in both offset and styling, and the
dead `UserGuidance` code path has been fully removed.
