# Task 02 Proofs - Wrapped and safely tinted card bodies

## Task Summary

This task proves card bodies preserve multiline and ANSI-styled content, hard-wrap without truncation, and maintain a contained state tint across resets and fill cells.

## What This Task Proves

- Embedded newlines, blank rows, indentation, trailing whitespace, and long unbroken words render at exact widths.
- Full resets, background resets, and combined SGR parameters restore the card tint without treating zero-valued RGB components as reset codes.
- Intentional inner backgrounds and foreground styling survive until their own reset.
- Neutral and error tint never expose the terminal background inside a row and do not leak into the following sentinel.
- A deterministic Glamour Go fence using jig's Chroma formatter renders inside both tint states.

## Evidence Summary

The focused body/tint tests and styled gallery pass. The assertions parse SGR state at every visible cell rather than merely searching for a background escape.

## Artifact: Body wrapping and tint state tests

**What it proves:** Content behavior and background stabilization work at normal and defensive widths.

**Why it matters:** Styled Markdown output contains resets that would otherwise punch terminal-background holes through a tinted card.

**Command:**

```bash
go test ./internal/tui/shared -run 'TestRenderCard(Content|Tint|Width)' -count=1 -v
```

**Result summary:** PASS, including explicit newlines, indentation, blank lines, hard-wrapped words, defensive width 5, full/default/background resets, combined parameters, RGB zero components, intentional inner backgrounds, and the Glamour/Chroma fence.

```text
--- PASS: TestRenderCardWidth
--- PASS: TestRenderCardTintRestoresAfterContentReset
--- PASS: TestRenderCardContentWrappingAndStyledCode
PASS
ok  jig/internal/tui/shared
```

## Artifact: Styled content gallery

**What it proves:** Neutral and error cards contain the same fabricated prose and jig-formatted Go code with exact 60-cell rows and complete background coverage.

**Why it matters:** This combines the card body, syntax formatter, wrapping, and tint stabilizer in a reviewer-readable component capture.

**Artifact path:** `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-2-styled-gallery.txt`

**Result summary:** Both 18-row cards passed exact-width and per-visible-cell background audits; the post-card sentinel remained untinted.

## Reviewer Conclusion

Card bodies wrap styled content without truncation, restore their semantic tint after real-world SGR resets, and contain that tint to each rendered row.
