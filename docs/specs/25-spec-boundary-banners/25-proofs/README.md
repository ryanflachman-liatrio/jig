# Slice 12 — centered boundary banners visual proofs

Deterministic Monitor and helper captures for the boundary-banner
feature (omp-transcript-parity slice 12). All fixture content is
fabricated — turn text, tool titles, and timestamps. No real `.jig/`
data is referenced.

## Generating

```bash
JIG_UI_SNAPSHOT_DIR=$(pwd)/docs/specs/25-spec-boundary-banners/25-proofs \
  go test ./internal/tui/monitor ./internal/tui/shared \
    -run 'TestBoundaryBannerVisualProof|TestRuleGallery' -count=1
```

Both tests skip unless `JIG_UI_SNAPSHOT_DIR` is set, so a normal
`go test ./...` run does not create or overwrite these files.

## Files

| Scene | What the banner proves |
|---|---|
| `slice-12-iteration-bump-interior.*` | An interior iteration transition. The gap between turn 0 and turn 1 contains, in order: blank / metadata row (slice 11) / boundary banner (slice 12) / blank. `-notes.txt` records the arithmetic — `chatItems[0].range.end` sits on the banner row so `n`/`N` block navigation lands on the arriving item, not on the trailing chrome. |
| `slice-12-all-three-labels-in-order.*` | A page stepping through `retry 1` → `iteration 2` → `reset 2` in one render, exercising the outermost-changed-wins precedence (a generation bump implicitly resets iteration and attempt but only draws one banner). |
| `slice-12-page-edge-banner.*` | FR-12.6: a page whose first item's coord is non-zero (`iteration = 2`). The banner announces `iteration 3` above the very first rendered item and sits *outside* any `chatItemLineRanges` entry, so `chatItems[0].start` still points at the item body. |
| `slice-12-boundary-banner-gallery.txt` | `shared.Rule` output at widths 20/40/60/80/100 for every ships-in-slice-12 label plus the degradation frontier (14/15/16 cells) and the empty-label fill. Every ruled row equals its declared width to the cell; the fallback row at 14 cells drops the rule glyphs and centers the bare label. |

Each Monitor scene has three companion files:

- `*.ansi` — the raw ANSI capture from `Model.View()`.
- `*.html` — the terminal-to-HTML conversion for browser inspection.
- `*-notes.txt` — per-item `chatItemLineRanges` entries, post-fold
  gap counts, and banner-label offsets in the plain body.

## Reading the notes

`-notes.txt` records, for every visible item:

1. `range={start,end}` — the folded line range. On a coord change,
   the closing item's range end sits on the banner row (or the
   metadata row when the banner is absent), because both fold in per
   the plan §Layout matrix.
2. `X blank line(s) since previous item's last row (post-fold)` —
   the count of raw blank lines between the folded ranges. Under
   slice 12 this is **1** on any coord change (the trailing blank
   after the banner) rather than the pre-slice-12 value of **2**;
   the reduction is not lost visual gap — it is the banner row
   absorbing one of the two.
3. `Banner label placement` — the byte offset of each expected
   banner label inside the plain body, so a reviewer can cross-check
   ordering (`retry 1` before `iteration 2` before `reset 2`).
