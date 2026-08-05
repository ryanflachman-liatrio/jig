# Task 06 Proofs — AgentQuestion option-list overflow scrolling

## Task Summary

This task adds overflow scrolling to the AgentQuestion option list within the
fixed gate strip height. When more options exist than fit in the body budget,
j/↓ and k/↑ scroll a window over the list with ▲/▼ indicators at the clip
boundaries. The gate panel height never changes and digit selection always maps
to the absolute option index regardless of scroll position.

## What This Task Proves

- Pressing j/↓ advances `scrollOffset` and shifts the visible option window.
- The gate strip height (`lipgloss.Height(m.gateStrip())`) is identical at
  scrollOffset=0 and all other offsets — no layout jitter.
- ▲ more appears above the options when scrolled past the first; ▼ more appears
  below when more options follow the visible window.
- Digit selection uses the absolute `[N]` label regardless of scroll position —
  pressing `1` selects Option1 even when Option1 is off-screen.
- `QuestionScroll` (↑/↓) is added to `keys.go` and appears in the footer hint.
- j/k do not collide with tab, left/right, digit select, enter/space (QConfirm),
  or q (cancel).
- Full TUI suite passes with the race detector.

## Evidence Summary

- `TestQuestionScroll` PASS — scrollOffset changes on j/k, height constant,
  digit 1 selects Option1 even when scrolled.
- `unit6-scroll.txt` artifact shows windowed list with ▲/▼ and unchanged height.
- Full TUI suite (`go test ./internal/tui -race`) clean.

## Artifact: TestQuestionScroll

**What it proves:** All three key behaviors — scrollOffset changes, constant
height, absolute digit selection — hold under scrolling.

**Why it matters:** This is the Unit 6 acceptance gate. Failing any of these
three would mean options are either unreachable or the gate layout jitters.

**Command:**

```bash
go test ./internal/tui -run TestQuestionScroll -v
```

**Result summary:** PASS — scrollOffset increments on j, Option1 scrolls off,
▲ more appears, k restores; digit `1` selects Option1 from scrollOffset=3.

```
=== RUN   TestQuestionScroll
--- PASS: TestQuestionScroll (0.00s)
PASS
ok  	jig/internal/tui	0.516s
```

## Artifact: unit6-scroll.txt — Scrolled question screenshot

**What it proves:** The windowed option list renders Options 3–5 with both ▲ more
and ▼ more visible; the panel height equals the empty-state and unscrolled heights
(no growth); the footer shows `↑/↓ scroll`.

**Artifact path:** `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit6-scroll.txt`

**Result summary:** Gate body shows `▲ more` / `[3] Option3` / `[4] Option4` /
`[5] Option5` / `▼ more`; panel height unchanged from baseline.

## Artifact: Full TUI test suite with race detector

**What it proves:** All TUI tests pass with no data races after Task 6 changes.

**Command:**

```bash
gofmt -l -w . && go vet ./... && go test ./internal/tui -race
```

**Result summary:** Clean format, no vet warnings, all tests pass.

```
ok  	jig/internal/tui	1.861s
```

## Reviewer Conclusion

Task 6 is complete: the AgentQuestion option list scrolls within the fixed gate
height, height is provably stable (verified by lipgloss.Height comparison in
test), and blind digit selection remains correct across any scroll position. The
scroll binding (j/k/↑/↓) is documented in keys.go and surfaced in the footer
hint for question entries.
