# Slice 11 — per-turn metadata row visual proofs

Deterministic 80×30 Monitor captures for the per-turn metadata row
feature (omp-transcript-parity slice 11). All fixture content is
fabricated — paths, step ids, tool titles, cost and token figures, and
timestamps. No real `.jig/` data is referenced.

## Generating

```bash
JIG_UI_SNAPSHOT_DIR=$(pwd)/docs/specs/25-spec-turn-metadata-row/25-proofs \
  go test ./internal/tui/monitor -run TestTurnMetadataRowVisualProof -count=1
```

The test in `internal/tui/monitor/monitor_transcript_metadata_visual_test.go`
skips unless `JIG_UI_SNAPSHOT_DIR` is set, so a normal `go test ./...`
run does not create or overwrite these files.

## Files

| Scene | What the metadata row proves |
|---|---|
| `slice-11-running-step-no-step-end.*` | The interior boundary row appears in the coordinate-change gap between iteration 0 and iteration 1; the step-end row is absent because `monitorStep.end` is zero (still `Running`). The item ranges in the accompanying `-notes.txt` show that the previous item's `end` line covers the metadata row so `n`/`N` block navigation still lands on the item. |
| `slice-11-settled-success-step-end.*` | `monitorStep.end` is set and cost + token figures are populated. The step-end metadata row appears below the last item with `$0.0412` and `4.2k tok`. |
| `slice-11-settled-failed-step-end.*` | Failed step: the error banner keeps its home at the top of `chatBody` (Q-11.3 resolution) and the metadata row still renders below the last item — the row does not duplicate the failure. |
| `slice-11-no-timestamps-row-absent.*` | Empty `Ts` on every entry and `iter=attempt=0` for the closing turn: every metadata field is absent, so the row is dropped entirely and the gap between items matches slice 04 (`2` blank lines) byte-for-byte. |

Each scene has three companion files:

- `*.ansi` — the raw ANSI capture from `Model.View()`.
- `*.html` — the terminal-to-HTML conversion for browser inspection.
- `*-notes.txt` — per-item `chatItemLineRanges` and inter-item gap
  counts computed independently by the test, plus a slice-11
  assertion outcome.

## Reading the notes

`-notes.txt` lists each visible item's `chatItemLineRanges` entry with
its `start`/`end` line and the number of blank lines between it and
the previous item. In the running-step and settled scenes the previous
item's `end` extends to include the metadata row, so the `blank
line(s) since previous item's last row` figure is `1` even though the
visual gap contains two rows (one blank + row + one blank): the row
is not counted as blank because it carries the timestamp/elapsed
text.
