# Slice 13 — phase-locked spinners visual proofs

Deterministic Monitor captures for the liveness pulse feature
(omp-transcript-parity slice 13). All fixture content is fabricated —
turn text, step ids, timestamps. No real `.jig/` data is referenced.

## Generating

```bash
JIG_UI_SNAPSHOT_DIR=$(pwd)/docs/specs/25-spec-liveness-and-spinners/25-proofs \
  go test ./internal/tui/monitor -run TestLiveCrumbVisualProof -count=1
```

The test skips unless `JIG_UI_SNAPSHOT_DIR` is set, so a normal
`go test ./...` run does not create or overwrite these files.

## Files

| Scene | What the crumb proves |
|---|---|
| `slice-13-pulse-running-following.*` | All four preconditions hold: `contentTranscript && showsTranscriptFollow && chatAutoScroll && anyRunning`. The Transcript panel title crumb and status line render `<frame> LIVE`. `-notes.txt` enumerates the full 8-frame `"status"` revolution (`⣾ ⣽ ⣻ ⢿ ⡿ ⣟ ⣯ ⣷`), each with `lipgloss.Width == 6` so the trailing breadcrumb slot never shifts. |
| `slice-13-settled-static-live.*` | Step has succeeded; `anyRunning() == false`. The crumb collapses to the static word `LIVE`, byte-identical to main. The persistent label survives so search text and screen readers keep the state legible even without animation. |
| `slice-13-paused-no-live-crumb.*` | Running step, but the operator has scrolled up (`chatAutoScroll == false`). `LIVE` does not appear at all — the panel switches to the `N new` branch on `main`, unchanged by this slice. |
| `slice-13-idle-no-live-crumb.*` | No running step, and follow is off. `LIVE` is absent entirely. Confirms FR-13.5: an idle monitor does not animate. |

Each scene has three companion files:

- `*.ansi` — the raw ANSI capture from `Model.View()` at 80×30.
- `*.html` — the terminal-to-HTML conversion for browser inspection.
- `*-notes.txt` — the fixture flags (status, `chatAutoScroll`,
  `anyRunning`, `liveCrumbShouldPulse`), the resolved crumb at capture
  time, and the full 8-frame revolution with per-frame widths.

## Reading the notes

`-notes.txt` records, in order:

1. **Fixture flags.** The four preconditions the pulse rides
   (`status`, `chatAutoScroll`, `anyRunning`) and the derived
   `liveCrumbShouldPulse` boolean, so a reviewer can trace the code
   path without opening the fixture.
2. **`liveCrumb(now)`.** The exact string composed at capture time.
3. **Full revolution gallery.** Every glyph in the `"status"` set,
   its offset in ms from a window boundary, and its rendered cell
   width. The single-cell contract is the reason the trailing
   breadcrumb slot does not jitter as frames advance
   (`TestLiveCrumbConstantWidthAcrossOneRevolution` locks the same
   invariant).
4. **Assertion summary.** `LIVE` present or absent, per the
   preconditions.
