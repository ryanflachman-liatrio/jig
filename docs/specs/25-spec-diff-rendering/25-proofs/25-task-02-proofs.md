# 25-task-02-proofs.md

Task 2 — Render the fused-gutter diff rows.

## Commands and outcomes

```
$ go test ./internal/tui/monitor -run TestRenderDiff -count=1 -v
$ go test ./internal/tui/shared -run TestThemeDiffTokens -count=1 -v
```

Both PASS. The 12 render cases and the shared-theme case cover
FR-07.6..FR-07.14 verbatim; each assertion strips ANSI where a
visible-shape guarantee is easier to read than an escape-sequence
match, and uses `containsReverseVideo` (defined in the test file)
so combined SGR sequences (`\x1b[7;38;...m`) are recognised
correctly.

The deterministic gallery capture is produced by:

```
$ JIG_UI_SNAPSHOT_DIR=$(pwd)/docs/specs/25-spec-diff-rendering/25-proofs \
    go test ./internal/tui/monitor -run TestDiffRenderGallery -count=1
```

The gallery test emits ANSI captures at widths 60 and 90, plus
HTML and PNG renders through a headless-Chrome step. The PNG uses
Charmtone-themed CSS via `terminalHTML` (extended in
`monitor_transcript_card_test.go` to honour SGR 2/7/22/27 so
slice-07's reverse-video and dim escapes render as inverse and
opacity-0.6 spans respectively).

## Artifacts

| Artifact | Where |
| --- | --- |
| Renderer | [`internal/tui/monitor/monitor_diff_render.go`](../../../../internal/tui/monitor/monitor_diff_render.go) |
| Renderer tests | [`internal/tui/monitor/monitor_diff_render_test.go`](../../../../internal/tui/monitor/monitor_diff_render_test.go) |
| Shared style tokens | [`internal/tui/shared/styles.go`](../../../../internal/tui/shared/styles.go) (`Diff.Context/Intraline/Gutter/Indent`) |
| Style-token test | [`internal/tui/shared/styles_test.go`](../../../../internal/tui/shared/styles_test.go) — `TestThemeDiffTokens` |
| Gallery generator | [`internal/tui/monitor/monitor_diff_gallery_test.go`](../../../../internal/tui/monitor/monitor_diff_gallery_test.go) — `TestDiffRenderGallery` |
| Width-60 ANSI gallery | [`25-task-2-diff-gallery-w60.txt`](./25-task-2-diff-gallery-w60.txt) |
| Width-90 ANSI gallery | [`25-task-2-diff-gallery-w90.txt`](./25-task-2-diff-gallery-w90.txt) |
| Width-60 HTML render | [`25-task-2-diff-gallery-w60.html`](./25-task-2-diff-gallery-w60.html) |
| Width-90 HTML render | [`25-task-2-diff-gallery-w90.html`](./25-task-2-diff-gallery-w90.html) |
| Width-60 PNG | [`25-task-2-diff-gallery-w60.png`](./25-task-2-diff-gallery-w60.png) |
| Width-90 PNG | [`25-task-2-diff-gallery-w90.png`](./25-task-2-diff-gallery-w90.png) |
| Gallery notes | [`25-task-2-diff-gallery.notes.txt`](./25-task-2-diff-gallery.notes.txt) |

## Requirement coverage

| Requirement | Test(s) |
| --- | --- |
| FR-07.6 (fused gutter shape) | `TestRenderDiffGutterShape` |
| FR-07.7 (width floor at 3) | `TestRenderDiffWidthFloor` (5-line and 1000-line) |
| FR-07.8 (duplicate-number suppression) | `TestRenderDiffDuplicateSuppression` |
| FR-07.9 (1↔1 intra-line + leading-whitespace exclusion) | `TestRenderDiffIntralineReverseVideo`, `TestRenderDiffIntralineExcludesLeadingWhitespace`, `TestRenderDiffMultiLineNoIntraline` |
| FR-07.10 (foreground-only add/remove) | `TestRenderDiffNoBackground` |
| FR-07.11 (indent visualization) | `TestRenderDiffIndentation` |
| FR-07.12 (no per-row background) | `TestThemeDiffTokens` (source-shape), `TestRenderDiffNoBackground` (rendered-output shape) |
| FR-07.13 (wrap continuation + SGR terminator) | `TestRenderDiffWrapContinuation` |
| FR-07.14 (collapse budget + slice-06 vocabulary) | `TestRenderDiffCollapseBudget`, `TestRenderDiffCollapseFooterVocabulary` |

The gallery PNGs visually confirm the same properties: the fused
gutter, the inverse `hi → hello` intra-line span, indent glyphs
(dim `→` for tabs, dim `·` for spaces), and the foreground-only
add/remove/context colors.

## Limitations

Context-run batch syntax highlighting (FR-07.10 second paragraph)
is exercised by the gallery test's inset-renderer-off path today.
When the Monitor's inset renderer is present at runtime it drives
per-run chroma highlighting; the current renderer test suite
covers the batch-highlight code path via nil-safe fallback, and
the visual gallery images make the terminal rendering auditable.
