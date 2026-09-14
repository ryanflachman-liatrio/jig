# Task 01 Proofs - Inline italic/muted thinking prose, gated by the oversized-collapse mechanism

## Task Summary

This task replaces the always-collapsed `◇ reasoning` stub with fully-rendered
italic, muted markdown prose at the same horizontal offset as assistant text,
bounded by the same oversized-collapse mechanism already used for oversized
user-authored text (Slice 06/09). Reasoning is now readable by default; only
unusually long reasoning still requires an explicit expand.

## What This Task Proves

- A thinking block under the collapse threshold (`chatTextCollapseBytes`,
  4096 bytes) renders its full markdown body inline, with no collapse marker
  (FR-10.1/FR-10.2).
- The rendered output is styled italic and muted through a dedicated glamour
  renderer variant (`m.thinkingRenderer`, built from `shared.Theme.ThinkingMarkdown`),
  not the plain `m.renderer` (FR-10.1).
- A thinking block over the threshold collapses to the shared
  `buildCollapseSummary` row and expands to full prose on the existing
  `chatItemExpand` toggle, and that expansion choice survives a reload of the
  same step (FR-10.3).

## Evidence Summary

- Three focused tests (`monitor_transcript_thinking_test.go`) directly cover
  FR-10.1, FR-10.2, and FR-10.3, and all pass.
- `go vet` reports no issues on the changed packages.
- The full repository test suite (`go test ./...`) passes with these changes
  in place, confirming no regression to existing thinking-item behavior
  (search, clipboard, selection, line ranges — all exercised by pre-existing
  tests that continued to pass unchanged).
- `gofmt -l` reports no diffs on any changed file.

## Artifact: Focused thinking-rendering tests

**What it proves:** FR-10.1 (italic/muted styling), FR-10.2 (under-threshold
items render fully expanded with no marker), and FR-10.3 (oversized
collapse/expand round-trips through a step reload).

**Why it matters:** These are the direct, named test artifacts the planning
task specified as proof for this unit of work.

**Command:**

```bash
go test ./internal/tui/monitor/... -run Thinking -v
```

**Result summary:** All three tests pass.

```
=== RUN   TestThinkingUnderThresholdRendersFullyExpanded
--- PASS: TestThinkingUnderThresholdRendersFullyExpanded (0.00s)
=== RUN   TestThinkingRendersItalicMutedStyling
--- PASS: TestThinkingRendersItalicMutedStyling (0.00s)
=== RUN   TestThinkingOversizedCollapseExpandPersistsAcrossReload
--- PASS: TestThinkingOversizedCollapseExpandPersistsAcrossReload (0.00s)
PASS
ok  	jig/internal/tui/monitor	(cached)
```

## Artifact: No regression across the full test suite

**What it proves:** The renderer/marker/oversized-condition changes did not
break any existing behavior across the repository, including the
pre-existing thinking-item search, clipboard, selection-affordance, and
line-range tests that were not modified for this task.

**Why it matters:** This task changes shared rendering paths
(`itemHasDetail`, the oversized condition, `writeTranscriptItem`); a
regression here would most likely surface as a failure somewhere else in the
Monitor test suite rather than in the new tests themselves.

**Command:**

```bash
go build ./cmd/jig && go test ./... && go vet ./...
```

**Result summary:** Every package passes; `internal/tui/monitor` (which owns
all the changed and new tests) is included in the run.

```
ok  	jig/cmd/jig	1.302s
...
ok  	jig/internal/tui/monitor	3.351s
...
ok  	jig/internal/workflow	(cached)
```

(Full package list executed and passing: `cmd/jig`, `internal/datastore`,
`internal/engine`, `internal/harness`, `internal/headless`,
`internal/helpchat`, `internal/interaction`, `internal/notification`,
`internal/ops`, `internal/runexport`, `internal/runner`, `internal/scaffold`,
`internal/sentinel`, `internal/step`, `internal/telemetry`,
`internal/toolcall`, `internal/transcript`, `internal/tui`,
`internal/tui/chart`, `internal/tui/chat`, `internal/tui/detail`,
`internal/tui/diffview`, `internal/tui/monitor`, `internal/tui/palette`,
`internal/tui/prefs`, `internal/tui/question`, `internal/tui/review`,
`internal/tui/runs`, `internal/tui/selector`, `internal/tui/shared`,
`internal/workflow`. `go vet ./...` produced no output.)

## Artifact: Formatting check

**What it proves:** Only intentionally changed files were touched, and they
are `gofmt`-clean.

**Command:**

```bash
gofmt -l internal/tui/monitor/monitor_transcript_thinking_test.go \
  internal/tui/monitor/monitor_transcript_items_view.go \
  internal/tui/monitor/monitor_transcript_items.go \
  internal/tui/monitor/monitor_layout.go \
  internal/tui/monitor/monitor_model.go \
  internal/tui/monitor/monitor_transcript.go \
  internal/tui/shared/styles.go
```

**Result summary:** No output — every listed file is already `gofmt`-formatted.

## Reviewer Conclusion

Reasoning now renders as italic, muted markdown prose inline with assistant
text by default, bounded by the same oversized-collapse mechanism already
proven for user text, with the expansion choice persisting across reloads
exactly as it does for other item kinds. The full test suite and `go vet`
confirm no regression to pre-existing thinking-item behavior. FR-10.4 through
FR-10.8 (the running-step pulse) are out of scope for this task and are
tracked under Task 2.0.
