# Implementation Plan: compact non-read tool groups

**Status:** Implemented — 2026-09-15
**Risk:** **medium** — this is a new hierarchical Monitor interaction spanning
the `monitor` and `prefs` packages. It does not change workflow schema, engine
scheduling, harness normalization, or `transcript.jsonl`; the main risk is
keeping selection, line ranges, search hits, and cursor restoration coherent
when a group exposes independently navigable children.
**Visual contract:**
[`compact-tool-groups-mockups.md`](compact-tool-groups-mockups.md)
**Supersedes:** the mixed-group and parallel expansion-state ideas in
[`11-spec-monitor-tool-call-groups`](../specs/11-spec-monitor-tool-call-groups/11-spec-monitor-tool-call-groups.md).
The shipped read-group implementation remains the compatibility baseline.

## Summary

Add an opt-in `compact_tool_groups` Monitor preference that groups adjacent,
successful calls of the same canonical non-read tool while preserving every
member as its existing bordered, independently navigable card when expanded.
The top risk is introducing a child cursor layer without regressing filtering,
search, copy, resize restoration, or the existing automatic read groups.

## Decisions

This plan records the approved interaction contract:

- Read grouping stays automatic and keeps its current one-stop behavior.
- Non-read grouping defaults off, so the current transcript is unchanged until
  the operator enables it.
- `c` toggles compact non-read groups and persists the choice in
  `.jig/tui.json`; clear search/filter moves from `c` to `x`.
- A group contains at least two adjacent, settled, successful exchanges with
  the same canonical kind and the same generation/iteration/attempt.
- Adjacency ignores settled reasoning hidden by the default view. Showing
  reasoning restores its original positions and breaks groups there; search
  and other filters never erase group boundaries.
- Failed, running, incomplete, malformed, targetless, unknown, and
  `askuserquestion` exchanges remain standalone and split adjacent runs.
- Different canonical kinds never mix. Repeated calls remain separate members,
  even if their target, command, query, or summary is identical.
- A collapsed group shows no more than five preview rows: its first three
  calls, an `… N more` row, and its final call.
- Expanding a non-read group retains the group header and reveals a
  tree-connected bordered summary card for every member. Member cards start
  collapsed inside compact groups, including structured edits.
- `enter`/space on a group toggles its child list; on a child it toggles that
  child's existing detail. `n`/`N` visits group headers and visible children.
- Collapsing a group while a child is selected returns selection to the group
  header. `o` expands or collapses every group and every child detail.
- Search, filtering, copying, page boundaries, cursor restoration, and line
  ranges inspect members without splitting a matching group.
- The command palette and help surface expose the toggle, and the transcript
  shows a short `compact tool groups: on/off` confirmation after it changes.

## Approach

Keep `buildTranscriptItems` and durable transcript correlation unchanged. In
`internal/tui/monitor`, continue running `groupReadTranscriptItems` first, then
apply a new pure, page-local `groupCompactToolTranscriptItems` pass only when
the preference is enabled; a strict `toolGroupPolicy` registry keyed by the
canonical kind owns eligibility, title/glyph choice, and safe one-line member
summaries. Add `transcriptItemToolGroup` for non-read groups while retaining
`transcriptItemReadGroup` and its renderer. Keep `chatVisibleItems` as the
filtered top-level render units, and derive `chatCursorTargets` from them so an
expanded group can expose its members without mutating the normalized items or
inventing a second transcript model. Render each child by the existing tool
exchange card path at a reduced width, prefix every resulting row with the
tree gutter, and record separate line ranges under the existing stable member
keys. Finally, extend `prefs.Prefs`, bind `c`/`x`, and update rebuilding,
search, filter, copy, and selection restoration around that stable-key model.

## Data and state model

### Normalized items

Add one item kind in `monitor_model.go`:

```go
transcriptItemToolGroup
```

It uses the existing `groupMembers []transcriptItem` field. The group key is
derived from the first member's anchor plus `transcriptItemToolGroup`; every
child retains its ordinary `transcriptItemToolExchange` key. Grouping is a
presentation transform over a loaded page, never a transcript or engine event.

### Policy registry

Create a closed registry in `monitor_tool_group.go`:

```go
type toolGroupPolicy struct {
    title     string
    kind      string
    summarize func(*toolcall.Activity) (string, bool)
}
```

The registry covers the canonical kinds already understood by
`summarizeActivity`: `edit`, `write`, `notebookedit`, `glob`, `grep`, `bash`,
`websearch`, `webfetch`, `task`, `skill`, and `todowrite`. A summarizer returns
`false` when its required safe identity is absent; such calls stay standalone.
`read` remains owned by `monitor_read_group.go`, `askuserquestion` is always
excluded, and `todoread` is targetless by design and therefore remains
standalone. Unknown future kinds fail closed until explicitly registered.

The policy extracts only the already-sanitized one-line label used by the
existing tool summary surface. It does not include tool output, command output,
web content, prompts, or other potentially sensitive detail in the collapsed
preview.

### Cursor projection

Add a small derived value type:

```go
type transcriptCursorTarget struct {
    key         transcriptItemKey
    itemIndex   int
    memberIndex int // -1 means the top-level item or group header
}
```

`chatVisibleItems` continues to own filtering and top-level order.
`chatCursorTargets` is rebuilt from it and current group expansion state:

1. append each selectable top-level item;
2. after an expanded `transcriptItemToolGroup`, append each member in order;
3. do not append read-group members, preserving current read behavior.

`chatItemCursor` indexes `chatCursorTargets`. Helpers resolve a target back to
its group header or member item, so rendering, toggling, copying, search, and
viewport anchoring use one definition of "selected block." This projection is
ephemeral and contains no alternate expansion state.

### Expansion defaults

Continue using `chatItemExpand` with stable keys for both group headers and
members. `defaultExpandEditCodeItems` must ignore edits nested inside a compact
group; those child cards begin collapsed. When grouping is disabled, the same
edit becomes standalone again and receives today's default structured-diff
expansion. `chatItemExpandAll` overrides both levels without rewriting the
per-item map.

### Preference persistence

Extend `prefs.Prefs`:

```go
CompactToolGroups bool `json:"compact_tool_groups"`
```

Its zero value is the required default. Replace the Monitor's field-specific
save calls with one helper that writes the complete current preference state,
so toggling simple mode cannot erase compact grouping and vice versa. Empty
`jigRoot` remains a successful no-op through `prefs.Save`.

## Behavioral rules

### Eligibility and boundaries

`groupCompactToolTranscriptItems` flushes its current run on any of these:

- canonical kind changes;
- generation, iteration, or attempt changes;
- text, visible thinking, an existing read group, or another non-tool item appears;
- the exchange is not paired and settled successfully;
- the activity is excluded, unknown, malformed, or lacks the policy's required
  summary identity.

A one-member run is emitted unchanged. The algorithm is linear in the number
of page items and only copies member slices for actual groups.

Before non-read grouping, omit settled reasoning that the default view hides,
while preserving execution-coordinate transitions. Rebuild this projection from
the loaded entries when the reasoning filter changes. The complete transcript
page remains available for reasoning inspection; search and all other filters
still run after grouping.

### Collapsed rendering

The header uses the registry title, the existing tool-status glyph/style, and
the member count. Preview rows preserve call order and use tree connectors.
For four calls, show all four. For five or more, show the first three, an
ellipsis count, and the final call.

### Expanded rendering

The group header remains visible. Each child is rendered through the same
`renderToolExchangeCard`/detail path as a standalone exchange, with card width
reduced by the tree gutter. A helper prefixes every rendered row with `├─` or
`└─` plus the required continuation column, including blank, bordered, diff,
output, truncation, and selected rows. No turn metadata row, boundary banner,
or ordinary inter-item spacing is inserted between children; the group is one
top-level transcript cluster.

The selected child uses the existing selected card treatment. Expanding that
child reveals its existing diff/output/details inside the same tree branch.

### Navigation and restoration

- `n`/`N` traverses `chatCursorTargets`.
- `enter`/space toggles the selected target's key.
- If a selected child becomes hidden by collapsing its parent, selection moves
  to the parent key before targets are rebuilt.
- On resize, streaming refresh, page reload, filter changes, and preference
  toggles, restore by selected stable key. If the key is no longer exposed,
  fall back to its parent group, then the nearest surviving top-level item.
- When compact grouping is turned off while a child is selected, restore to
  that child's now-standalone key. When a group header is selected, restore to
  its first member.
- `ensureTranscriptItemCursorVisible` resolves the current target key through
  `chatItemLineRanges`, whose ranges include group headers and every rendered
  child separately.

### Search, filters, and copy

Group membership is indivisible at the top level:

- a search or filter inspects all members;
- if any member matches, the complete group remains visible;
- selecting a member search hit expands its parent and lands on that child's
  stable key;
- subsequent/previous search visits matching members in transcript order;
- copying a selected child uses the existing single-exchange payload;
- copying a group header uses the complete group payload, independent of the
  five-row visual preview.

Search highlighting may appear inside visible child cards. A collapsed group
may highlight its matching preview row; a match outside the preview expands the
group before selection so the result is never hidden.

### Toggle confirmation

Store an ephemeral Monitor-only notice string after `c` is handled:
`compact tool groups: on` or `compact tool groups: off`. Render it in the
existing transcript status/help register and clear it on the next handled key
or step/page transition. This avoids a timer and does not create durable state.

## Ordered tasks

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `CompactToolGroups` with default-off load/save behavior and full-state persistence coverage | `internal/tui/prefs/prefs.go` | 15 min |
| 2 | Add preference round-trip, missing/corrupt file, and persistence-off tests | `internal/tui/prefs/prefs_test.go` | 15 min |
| 3 | Add `transcriptItemToolGroup`, `transcriptCursorTarget`, model fields, and stable target-resolution helpers | `internal/tui/monitor/monitor_model.go` | 25 min |
| 4 | Implement the strict canonical policy registry and pure non-read grouping pass | `internal/tui/monitor/monitor_tool_group.go` | 30 min |
| 5 | Add grouping-table tests for every policy, run boundaries, duplicates, failure/incomplete/malformed cases, and the five-row preview selection | `internal/tui/monitor/monitor_tool_group_test.go` | 30 min |
| 6 | Load/save the complete Monitor preference state and rebuild page items when compact grouping changes | `internal/tui/monitor/monitor_transcript.go` | 25 min |
| 7 | Render collapsed headers and expanded tree-connected child cards through the existing card/detail path | `internal/tui/monitor/monitor_tool_group_view.go` | 30 min |
| 8 | Add golden/visual tests for default-off parity, collapsed groups, nested bordered cards, expanded edits/bash output, failure boundaries, large groups, and narrow widths | `internal/tui/monitor/monitor_tool_group_view_test.go` | 30 min |
| 9 | Change transcript bindings to `c` for compact groups and `x` for clear, with dynamic help labels | `internal/tui/monitor/keys.go` | 10 min |
| 10 | Implement hierarchical toggle, `n`/`N`, `o`, selection fallback, preference persistence, and transient confirmation handling | `internal/tui/monitor/monitor_update.go` | 30 min |
| 11 | Add keyboard and palette tests for `c`, `x`, group/child toggles, collapse fallback, expand-all, and default-off behavior | `internal/tui/monitor/monitor_tool_group_interaction_test.go` | 30 min |
| 12 | Teach transcript layout and line-range accounting to render group clusters and independently anchor child cards | `internal/tui/monitor/monitor_transcript_items_view.go` | 30 min |
| 13 | Add viewport, resize, streaming-refresh, page-boundary, and cursor-restoration regression tests | `internal/tui/monitor/monitor_tool_group_navigation_test.go` | 30 min |
| 14 | Make search/filter hit construction member-aware without splitting groups | `internal/tui/monitor/monitor_search.go` | 25 min |
| 15 | Add search/filter tests for hidden-preview matches, multiple child hits, full-group retention, and read-group parity | `internal/tui/monitor/monitor_tool_group_search_test.go` | 25 min |
| 16 | Resolve copy payloads through cursor targets for group headers and individual children | `internal/tui/monitor/clipboard.go` | 15 min |
| 17 | Add copy tests proving complete group payloads and unchanged child/standalone payloads | `internal/tui/monitor/clipboard_test.go` | 15 min |
| 18 | Document compact grouping, its default, hierarchy, and updated key bindings | `docs/TUI.md` | 15 min |

## Verification matrix

| Contract | Automated proof |
|---|---|
| Default off preserves current UI | Golden comparison of the same fixture before the feature flag and with `CompactToolGroups=false` |
| Reads remain automatic and one-stop | Existing read-group suites plus a mixed read/non-read fixture |
| Same-kind/same-coordinate/success-only grouping | Table tests over all registry kinds and every split condition |
| First 3 / ellipsis / final preview | Six-member golden and direct row assertions |
| Existing bordered card per child | Expanded edit, write, bash, and web fixtures |
| Independent navigation/detail | Key-sequence test over header → child → child detail → sibling |
| Collapse returns child to header | Selection-key assertion after `enter` on the parent |
| `o` controls both hierarchy levels | Render and expansion-map assertions |
| Search/filter retain complete group | Member-only query and tool-filter tests |
| Copy distinguishes header and child | Clipboard payload assertions |
| Preference survives other toggles | Load → compact toggle → simple toggle → reload round trip |
| Persistence-off stays safe | Empty-root preference test |
| Narrow and resized layouts remain bounded | Width sweep plus line-range/viewport assertions |

Run, in order:

```bash
gofmt -w internal/tui/prefs/prefs.go internal/tui/prefs/prefs_test.go \
  internal/tui/monitor/monitor_model.go internal/tui/monitor/monitor_tool_group.go \
  internal/tui/monitor/monitor_tool_group_test.go internal/tui/monitor/monitor_transcript.go \
  internal/tui/monitor/monitor_tool_group_view.go internal/tui/monitor/monitor_tool_group_view_test.go \
  internal/tui/monitor/keys.go internal/tui/monitor/monitor_update.go \
  internal/tui/monitor/monitor_tool_group_interaction_test.go \
  internal/tui/monitor/monitor_transcript_items_view.go \
  internal/tui/monitor/monitor_tool_group_navigation_test.go \
  internal/tui/monitor/monitor_search.go internal/tui/monitor/monitor_tool_group_search_test.go \
  internal/tui/monitor/clipboard.go internal/tui/monitor/clipboard_test.go
go test ./internal/tui/prefs ./internal/tui/monitor
go test ./...
go vet ./...
go build ./cmd/jig
```

## Non-goals

- No change to `transcript.jsonl`, tool correlation, harness events, workflow
  schema, engine scheduling, or run persistence.
- No grouping across page, turn, generation, iteration, or attempt boundaries.
- No semantic merging or deduplication of repeated calls.
- No configurable per-tool preferences in this slice; grouping is one boolean.
- No nested groups, mixed-kind groups, automatic grouping of failures, or
  grouping of unknown future tools.
- No change to the current read-group child navigation model.
