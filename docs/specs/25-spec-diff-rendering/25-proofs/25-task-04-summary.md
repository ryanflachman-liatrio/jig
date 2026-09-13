# 25-task-04-summary.md

Task 4 — Acceptance and scope integrity.

## Commands and outcomes

| Command | Result | Captured to |
| --- | --- | --- |
| `go build ./cmd/jig` | PASS (exit 0) | [`25-task-4-acceptance/build.txt`](./25-task-4-acceptance/build.txt) |
| `go vet ./...` | PASS (exit 0) | [`25-task-4-acceptance/vet.txt`](./25-task-4-acceptance/vet.txt) |
| `go test -race ./internal/tui/... ./internal/helpchat -count=1` | PASS (exit 0) | [`25-task-4-acceptance/race-tui.txt`](./25-task-4-acceptance/race-tui.txt) |
| `gofmt -l <changed-go-files>` | PASS (exit 0; no output) | [`25-task-4-acceptance/gofmt.txt`](./25-task-4-acceptance/gofmt.txt) |
| `git diff --check origin/main...HEAD` | PASS (exit 0) | [`25-task-4-acceptance/git-diff-check.txt`](./25-task-4-acceptance/git-diff-check.txt) |
| `go test ./... -count=1` | FAIL (exit 1; pre-existing engine/harness flakes) | [`25-task-4-acceptance/test.txt`](./25-task-4-acceptance/test.txt), [`25-task-4-limitations.md`](./25-task-4-limitations.md) |

The `go test ./...` failure is documented in
[`25-task-4-limitations.md`](./25-task-4-limitations.md): every
failing test passes when re-run in isolation, all failures are in
`internal/engine` and `internal/harness` (packages not touched by
this slice), and the timeouts are load-related, not caused by any
change here.

## Requirement coverage — FR-07.1 through FR-07.22

| Requirement | Task | Evidence |
| --- | --- | --- |
| FR-07.1 compute + project | 1 | `TestComputeDiff*` |
| FR-07.2 library choice | 1 | ADR 0013 |
| FR-07.3 bounded input | 1 | `TestComputeDiffOversizeByte`, `TestComputeDiffOversizeLine` |
| FR-07.4 parse/emit error routing | 1, 3 | `TestComputeDiffNilDiff`, `TestMonitorDiffComputeFailureFallback` |
| FR-07.5 determinism | 1 | `TestComputeDiffDeterministic`, `TestComputeDiffPatchStringIsDeterministic` |
| FR-07.6 fused-gutter shape | 2 | `TestRenderDiffGutterShape` |
| FR-07.7 width floor at 3 | 2 | `TestRenderDiffWidthFloor` |
| FR-07.8 duplicate-number suppression | 2 | `TestRenderDiffDuplicateSuppression` |
| FR-07.9 1↔1 intra-line + leading-ws exclusion | 2 | `TestRenderDiffIntralineReverseVideo`, `TestRenderDiffIntralineExcludesLeadingWhitespace`, `TestRenderDiffMultiLineNoIntraline` |
| FR-07.10 batch-highlighted context; flat add/remove | 2 | Renderer implementation + `TestRenderDiffNoBackground` (add/remove foreground-only assertion) + gallery captures |
| FR-07.11 indent visualization | 2 | `TestRenderDiffIndentation` + gallery captures |
| FR-07.12 foreground-only styling | 2 | `TestThemeDiffTokens`, `TestRenderDiffNoBackground` |
| FR-07.13 wrap continuation + SGR terminator | 2 | `TestRenderDiffWrapContinuation` |
| FR-07.14 collapse budget through slice-06 vocabulary | 2 | `TestRenderDiffCollapseBudget`, `TestRenderDiffCollapseFooterVocabulary` |
| FR-07.15 Diff · label + fallback hint | 3 | `TestMonitorDiffSectionLabel`, `TestMonitorDiffComputeFailureFallback` |
| FR-07.16 file-creation preserves resulting-source | 3 | `TestMonitorDiffFileCreationFallback` |
| FR-07.17 clamped-content hint above diff | 3 | `TestMonitorDiffClampedContentHint` |
| FR-07.18 +N/-M badge on expanded non-error | 3 | `TestMonitorDiffBadgeExpanded`, `TestMonitorDiffBadgeAbsentCollapsed`, `TestMonitorDiffBadgeAbsentErrored`, `TestStructuredEditShowsNewCodeInDefaultOpenCard` |
| FR-07.19 line-range accounting | 3 | `TestMonitorDiffLineRangesCoverBody` |
| FR-07.20 anchor tail-running through slice-06 bound | 3 | Anchor path in `writeDiffSection`; slice-06's anchor helper covered by existing tests |
| FR-07.21 cache lifecycle | 3 | `TestMonitorDiffCacheStableOnRepeatedRender`, `TestMonitorDiffCacheEvictedOnWidthChange` |
| FR-07.22 no wire-format changes | 4 | Final-diff review: no touched files under `internal/transcript/`, `internal/toolcall/`, `internal/runner/`, `internal/harness/`, or `internal/tui/shared/palette.go`. |

## Final-diff scope integrity

`git diff --stat origin/main...HEAD` shows changes limited to:

- `docs/adr/0013-diff-computation-library.md` (new; slot 13 verified unused)
- `docs/adr/README.md` (ADR index)
- `docs/specs/25-spec-diff-rendering/**` (spec, tasks, proofs)
- `go.mod` (promote `hexops/gotextdiff v1.0.3` to direct require)
- `internal/tui/monitor/monitor_diff_compute.go` (new)
- `internal/tui/monitor/monitor_diff_compute_test.go` (new)
- `internal/tui/monitor/monitor_diff_render.go` (new)
- `internal/tui/monitor/monitor_diff_render_test.go` (new)
- `internal/tui/monitor/monitor_diff_wire_test.go` (new)
- `internal/tui/monitor/monitor_diff_gallery_test.go` (new)
- `internal/tui/monitor/monitor_transcript_items_view.go`
- `internal/tui/monitor/monitor_transcript_items_view_test.go`
- `internal/tui/monitor/monitor_transcript_card_test.go` (SGR 2/7/22/27 support in the HTML converter)
- `internal/tui/monitor/monitor_model.go` (new cache surface constant)
- `internal/tui/shared/styles.go` (four new `Diff.*` tokens)
- `internal/tui/shared/styles_test.go` (locked)
- `internal/tui/shared/truncation.go` (two new helpers)
- `internal/tui/shared/truncation_test.go` (locked)

No changes under `internal/transcript/`, `internal/toolcall/`,
`internal/runner/`, `internal/harness/`, `internal/tui/shared/palette.go`,
or the review workspace. FR-07.22 holds.
