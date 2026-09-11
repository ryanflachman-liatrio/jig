# Slice 00 — Remove the unreachable render-plan path

- **Slice ID:** `remove-dead-render-plan`
- **Outcome:** `internal/tui/monitor/monitor_transcript.go` contains only code
  that can execute, roughly 450 lines lighter, with `go test ./...` green.
- **Why this slice exists:** Every other slice in this epic edits transcript
  rendering. A reader (human or agent) opening `monitor_transcript.go` today
  finds two complete rendering implementations and no signal about which one
  runs. Deleting the dead one first makes every subsequent diff smaller,
  reviewable, and unambiguous. It carries no design risk.
- **Depends on:** None.

---

## The finding

`chatBody()` short-circuits into the item renderer at
`internal/tui/monitor/monitor_transcript.go:657-673`:

```go
func (m *Model) chatBody() string {
	if m.selKind == "file" && m.selFile != "" {
		return m.fileBody()
	}
	if len(m.chatItems) > 0 {
		body := m.itemTranscriptBody()
		if i, ok := m.index[m.chatStep]; ok {
			s := m.steps[i]
			if s.status == step.StatusFailed && s.err != "" {
				return "  " + shared.Theme.Error.Render(shared.IconError+" "+s.err) + "\n\n" + body
			}
		}
		return body
	}
	// ...everything below here only runs when chatItems is empty
```

`m.chatItems` is assigned in exactly one place, `setChatPage`
(`monitor_transcript.go:172`):

```go
m.chatItems = buildTranscriptItems(page.Entries, m.currentChatStepRunning())
```

`buildTranscriptItems` (`monitor_transcript_items.go:6`) appends at least one
item per block of every entry. Therefore:

> `len(m.chatItems) == 0` **iff** `len(m.chatEntries) == 0`.

And `chatRenderPlan` is built by `rebuildActiveState` from
`m.filteredEntries()`, so when `chatEntries` is empty the plan is empty too. The
render-plan loop at `:784` iterates zero times in every reachable state.

## Inventory of dead code

| Symbol | Lines | Reachability |
|---|---|---|
| `rebuildLoadedChat` | `255–288` | Called from `setChatPage`, but its only *output* (`chatGroupHeaders`) feeds `rebuildActiveState`. |
| `rebuildActiveState` | `435–640` | Builds `chatBlocks` + `chatRenderPlan`; both consumed only by the dead branch. |
| `collapsible` | `645–652` | No callers. |
| render-plan loop inside `chatBody` | `781–842` | Unreachable branch. |
| `writeGroupHeader` | `962–993` | Called only from the loop. |
| `writeBlock` | `999–1030` | Called only from the loop. |
| `writeCollapsible` | `1118–1167` | Called only from `writeBlock`. |
| `withBar` | `1173–1180` | Called only from `writeCollapsible`. |
| `collapseLine` | `1185–1191` | Called only from `writeCollapsible`. |
| `writeDiff` | `1225–1253` | **Zero callers anywhere.** |
| `hunkAt` | `1255–1262` | Called only from `writeDiff`. |
| `writeRawDiff` | `1264–1275` | Called only from `writeDiff`. |

Verification command used:

```sh
grep -rn "writeDiff\|writeCollapsible\|withBar(" internal/ --include='*.go' | grep -v '_test.go'
```

`internal/tui/review/view.go` hits are a **different** function,
`writeDiffGutters` — not `writeDiff`.

## Model fields that become unused

Confirm each with a grep before deleting; some may be referenced by tests that
also need removing.

- `chatBlocks`, `chatBlockCursor`
- `chatRenderPlan`
- `chatGroupHeaders`, `chatGroupExpand`, `chatGroupForBlock`
- `chatExpand`, `chatExpandAll` (distinct from `chatItemExpand` /
  `chatItemExpandAll`, which **are** live)
- `chatLineRanges` (distinct from `chatItemLineRanges`, which **is** live)
- the `renderItem` struct and its `renderKind` constants (`renderEntrySep`,
  `renderEntryHeader`, `renderText`, `renderGroupHeader`, `renderGroupGap`,
  `renderBlock`)
- `chatItem`, `toolGroup`, `blockKey`-keyed maps that survive only the dead path

**Careful:** `blockKey` itself is live — `transcriptItemKey.anchor` is a
`blockKey`, and `renderMarkdown` is keyed by it. Do not delete the type.

`prunePageState` (`:386-427`) prunes both live and dead maps; trim it to the
live ones rather than deleting it.

## What must survive

The live surface of `monitor_transcript.go` after this slice:

- paging and follow state: `scrollTranscript`, `reloadTranscript`,
  `latestChatSeq`, `unseenChatEntries`, `showsTranscriptFollow`,
  `resumeTranscriptFollow`, `updateTranscriptFollow`, `loadChatTail`,
  `setChatPage`, `loadOlderChat`, `loadNewerChat`, `loadChatBefore`,
  `completeToolBoundaryContext`, `toolOnlyEntry`, `prunePageState`
- item state: `defaultExpandEditCodeItems`, `itemHasStructuredDiff`,
  `rebuildTranscriptItemState`, `filteredTranscriptItems`,
  `currentChatStepRunning`
- `chatBody`'s empty-state, search, and filter chrome
- `writeReviewOverview`, `reviewOutcomeFor`, `reviewFormatLabel`
- `renderMarkdown`, `renderInsetMarkdown`
- `fenceJSON`, `jsonlToMarkdown`, `expandView`, `clampRunes`, `clampRunesTail`
- `writeVerbatim`, `stripBlankEdges`, `stripSGR`
- `fileBody`

## In Scope

- Delete the symbols in the inventory table and their now-unused model fields.
- Delete or rewrite tests that exercise only the dead path. **Read each test
  first**: some may be asserting behavior that the item renderer also provides,
  in which case retarget rather than delete.
- Collapse `chatBody`'s remaining structure: with the item branch always taken
  when there is content, the function becomes "empty states and chrome, or
  delegate."
- Update `docs/run-monitor-transcript-plan.md` if it documents the removed path.

## Out of Scope

- Any visual change whatsoever. This slice is behavior-preserving by
  construction: the deleted code did not execute.
- Touching `monitor_transcript_items_view.go`.
- The `writeDiff` *capability* — real diffs are slice 07. This slice deletes the
  unused implementation; slice 07 writes a new one against `diffview` and
  `toolcall.Diff`.

## Functional Requirements

- **FR-00.1** The system shall render transcripts identically before and after
  this change, verified by the existing `monitor_transcript_items_view_test.go`
  and `monitor_test.go` suites passing unmodified.
- **FR-00.2** The system shall contain no function in
  `internal/tui/monitor/monitor_transcript.go` that is unreachable from
  `chatBody`, `reloadTranscript`, or the paging entry points.
- **FR-00.3** `go vet ./...` shall report no unused symbols introduced or left
  by this change.

## Technical and Repository Constraints

- `AGENTS.md:17-22` (pre-v1) explicitly authorizes deleting a replaced mechanism
  in the same change rather than leaving a dual code path. Cite it in the commit
  message.
- Deletion must not disturb `internal/tui/shared` — `Theme.Chat.BarThinking`,
  `.BarToolCall`, `.BarToolResult`, `.BarError`, `.BlockCursor`, and
  `shared.BarThick` become unreferenced by the monitor. **Leave the style fields
  in place for this slice** and let slice 01 decide their fate; removing them
  here would couple a pure deletion to a theme change.
- `gofmt -l -w .` and `go vet ./...` before committing.

## Security and Data Considerations

None identified. No data path, credential, or persisted artifact is touched.

## Acceptance Evidence

- `go test ./...` green with no test file modified other than deletions of
  dead-path-only tests, each justified in the commit message.
- `git diff --stat` shows a net deletion of roughly 400–500 lines concentrated in
  `monitor_transcript.go`.
- A before/after screenshot of the Transcript panel on the same run is
  pixel-identical.

## Inputs for the Child Spec

- The reachability argument above is the whole justification; the spec does not
  need to re-derive it, but **should re-run the greps** because the line numbers
  will have drifted.
- The child spec must enumerate every deleted test and say why it was
  dead-path-only.
- Preserve `blockKey` (live), `chatItemExpand` / `chatItemExpandAll` /
  `chatItemRendered` / `chatItemLineRanges` (all live).

## Open Questions

- **Q-00.1** Does `docs/run-monitor-transcript-plan.md` describe the render-plan
  design as current architecture? If so it needs an update in this slice, not a
  later one. *Not a blocker — resolvable by reading the file.*
- **Q-00.2** Are any of the dead symbols exported or referenced from
  `internal/tui/monitor`'s test helpers in a way that other packages consume?
  Expected answer: no, the package is `internal` and these are unexported.
  *Not a blocker.*
