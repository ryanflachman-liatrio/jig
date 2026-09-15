# Compact tool groups: visual and interaction artifact

These are implementation mockups, not pixel-perfect screenshots. They define
hierarchy, density, selection, and key behavior while leaving color and exact
border glyphs to the existing Monitor theme and card primitive.

## Legend

```text
▸  collapsed group or card
▾  expanded group or card
▌  selected navigable block
├─ non-final child
└─ final child
⋮  tree continuation through a multi-line child card
```

The group header and every visible child card are independent `n`/`N` stops.
Read groups keep their existing behavior and do not expose child stops.

## Default: compact grouping off

The new preference defaults to off. Existing edit and write cards therefore
remain exactly as they are today, including the default-open structured edit.

```text
  ╭─── ▾ ✎ Edit: internal/tui/model.go ─────────────────────╮
  │  @@ -41,6 +41,8 @@                                      │
  │  + compactToolGroups bool                               │
  ╰──────────────────────────────────────────────────────────╯

  ╭─── ▸ ✎ Edit: internal/tui/view.go ──────────────────────╮
  ╰──────────────────────────────────────────────────────────╯

  ╭─── ▸ ✎ Write: docs/TUI.md ──────────────────────────────╮
  ╰──────────────────────────────────────────────────────────╯
```

Pressing `c` enables compact non-read groups and briefly shows:

```text
  compact tool groups: on
```

## Collapsed same-kind groups

Calls group only with adjacent calls of the same canonical kind. The preview
is a table of calls, not a merged operation.

```text
  ▸ ✎ Edit (3)
    ├─ internal/tui/model.go
    ├─ internal/tui/view.go
    └─ docs/TUI.md

  ▸ ✎ Write (2)
    ├─ internal/tui/monitor/testdata/edit.golden
    └─ internal/tui/monitor/testdata/bash.golden

  ▸ $ Run (3)
    ├─ go test ./internal/tui/monitor
    ├─ go vet ./...
    └─ go build ./cmd/jig
```

`Edit` and `Write` do not mix. Likewise, `Grep` and `Glob`, `WebSearch` and
`WebFetch`, and `Task` and `Skill` are distinct group kinds.

## Expanded group: bordered summary cards

Pressing `enter` or space on the Edit header reveals the existing bordered
summary card for every call. Each child starts collapsed, including edits.

```text
  ▾ ✎ Edit (3)

    ├─ ╭─── ▸ ✎ Edit: model.go ─────────────────────────────╮
    │  ╰─────────────────────────────────────────────────────╯

▌   ├─ ╭─── ▸ ✎ Edit: view.go ──────────────────────────────╮
▌   │  ╰─────────────────────────────────────────────────────╯

    └─ ╭─── ▸ ✎ Edit: TUI.md ───────────────────────────────╮
       ╰─────────────────────────────────────────────────────╯
```

Here the second child is selected. `n` moves to the third child; `N` moves to
the first child and then the group header.

## Expanded child: existing full detail

Pressing `enter` or space on the selected child expands its existing card.
The tree continuation prefixes every row, including the border and diff.

```text
  ▾ ✎ Edit (3)

    ├─ ╭─── ▸ ✎ Edit: model.go ─────────────────────────────╮
    │  ╰─────────────────────────────────────────────────────╯

▌   ├─ ╭─── ▾ ✎ Edit: view.go ──────────────────────────────╮
▌   │  │  @@ -88,6 +88,10 @@                                │
▌   │  │  +func renderToolGroup(...) {                      │
▌   │  │  +    // existing diff presentation                │
▌   │  │  +}                                                 │
▌   │  ╰─────────────────────────────────────────────────────╯

    └─ ╭─── ▸ ✎ Edit: TUI.md ───────────────────────────────╮
       ╰─────────────────────────────────────────────────────╯
```

Collapsing the parent while this child is selected moves selection to the
`Edit (3)` header. Pressing `o` expands every group and every child detail;
pressing it again collapses both levels.

## Other tool families together

All supported non-read tools obey the same hierarchy. Each group retains its
tool-specific title, glyph, and safe one-line summary.

| Canonical kind | Group title | Preview identity |
|---|---|---|
| `edit` | Edit | file path |
| `write` | Write | file path |
| `notebookedit` | Edit notebook | notebook path |
| `glob` | Find | glob pattern |
| `grep` | Search | pattern/query plus existing safe scope metadata |
| `bash` | Run | command |
| `websearch` | Search web | query |
| `webfetch` | Fetch | host/URL summary |
| `task` | Agent | description |
| `skill` | Use skill | skill name |
| `todowrite` | Update | task count |
| `read` | Read | existing automatic read-group renderer |
| `todoread` | — | standalone because it has no target |
| `askuserquestion` | — | always standalone interaction gate |
| unknown/malformed | — | standalone, fail closed |

```text
  ▸ ⌕ Search (3)
    ├─ transcriptItemToolGroup in internal/tui/monitor
    ├─ chatItemLineRanges in internal/tui/monitor
    └─ compact_tool_groups in internal/tui

  ▸ ⌕ Find (2)
    ├─ internal/tui/monitor/*group*.go
    └─ docs/plans/*tool-groups*.md

  ▸ ↗ Search web (2)
    ├─ Bubble Tea hierarchical list keyboard navigation
    └─ terminal tree nested card UX

  ▸ ↗ Fetch (2)
    ├─ go.dev/doc
    └─ charm.land/bubbles/v2

  ▸ ✎ Edit notebook (2)
    ├─ analysis.ipynb
    └─ report.ipynb

  ▸ ⊙ Agent (2)
    ├─ Review transcript grouping behavior
    └─ Verify navigation invariants

  ▸ ⊙ Use skill (2)
    ├─ plan
    └─ qa

  ▸ ⊙ Update (2)
    ├─ 4 todo items
    └─ 6 todo items
```

The actual glyphs come from the existing shared tool icon/status vocabulary;
the examples above communicate role, not a request for new icons.

## Expanded Bash (`Run`) child

Tool-specific details remain unchanged. Bash output is not copied into the
collapsed preview; it appears only when its child card is expanded.

```text
  ▾ $ Run (2)

    ├─ ╭─── ▸ $ Run: gofmt -w … ────────────────────────────╮
    │  ╰─────────────────────────────────────────────────────╯

▌   └─ ╭─── ▾ $ Run: go test ./internal/tui/monitor ────────╮
▌      │  ok   jig/internal/tui/monitor  0.842s              │
▌      │                                                      │
▌      │  exit 0                                              │
▌      ╰─────────────────────────────────────────────────────╯
```

## Large groups

The collapsed height is bounded. Four calls show four rows. Five or more show
the first three calls, an ellipsis count, and the final call.

```text
  ▸ ✎ Edit (9)
    ├─ internal/tui/monitor/monitor_model.go
    ├─ internal/tui/monitor/monitor_transcript.go
    ├─ internal/tui/monitor/monitor_update.go
    ├─ … 5 more
    └─ docs/TUI.md
```

Repeated calls are not deduplicated:

```text
  ▸ $ Run (3)
    ├─ go test ./internal/tui/monitor
    ├─ go test ./internal/tui/monitor
    └─ go test ./internal/tui/monitor
```

## Failures and other hard boundaries

Only successful settled exchanges group. A failure stands alone and splits
the run, so its diagnostic state cannot disappear into a success summary.

```text
  ▸ $ Run (2)
    ├─ gofmt -w internal/tui/monitor/monitor_model.go
    └─ go test ./internal/tui/monitor

  ╭─── ! Run: go vet ./... ──────────────────────────────────╮
  │  internal/tui/monitor/monitor_model.go: undefined: ...   │
  ╰───────────────────────────────────────────────────────────╯

  ▸ $ Run (2)
    ├─ go test ./internal/tui/monitor
    └─ go build ./cmd/jig
```

Running and incomplete exchanges behave the same way as the failed card above.
Unknown, malformed, and targetless tools also remain standalone.
`AskUserQuestion` always remains standalone because it is an interaction gate,
not compactible transcript activity.

## Read groups remain distinct

Read retains the automatic grouping that ships today. Enabling compact tools
does not turn read members into child cursor stops or bordered member cards.

```text
  ◈ Read (4)
    ├─ monitor_model.go:432, 485
    ├─ monitor_transcript.go:162
    └─ monitor_update.go:548, 588

  ▸ ✎ Edit (2)
    ├─ internal/tui/monitor/monitor_model.go
    └─ internal/tui/monitor/monitor_update.go
```

The read group is one `n`/`N` stop. The Edit group becomes three stops when
expanded: header, first child, second child.

## Search and filter behavior

Suppose only `docs/TUI.md` matches the current search. The entire group remains
present, the group opens, and selection lands on the matching child:

```text
  ▾ ✎ Edit (3)

    ├─ ╭─── ▸ ✎ Edit: model.go ─────────────────────────────╮
    │  ╰─────────────────────────────────────────────────────╯

    ├─ ╭─── ▸ ✎ Edit: view.go ──────────────────────────────╮
    │  ╰─────────────────────────────────────────────────────╯

▌   └─ ╭─── ▸ ✎ Edit: TUI.md ───────────────────────────────╮
▌      ╰─────────────────────────────────────────────────────╯
```

Search next/previous walks matching member keys in transcript order. A tool
filter retains the complete group when any member satisfies it. `x` clears the
active search and filters; `c` never clears the view.

## Copy behavior

The selected level determines the payload:

- group header: copy the complete ordered group, including members omitted by
  the five-row preview;
- child card: copy only that existing tool exchange payload;
- standalone card: unchanged current behavior.

Visual collapse never causes data loss in copied content.

## Narrow width

Tree gutters consume width before the existing card renderer receives its
budget. Headers and summaries truncate with the repository's current
truncation vocabulary; borders never exceed the transcript inner width.

```text
  ▾ ✎ Edit (2)
    ├─ ╭─ ▸ ✎ Edit: monitor_trans… ╮
    │  ╰────────────────────────────╯
▌   └─ ╭─ ▸ ✎ Edit: monitor_updat… ╮
▌      ╰────────────────────────────╯
```

At extremely narrow widths, content truncates before the connector. The
connector and selected bar remain visible because they communicate hierarchy
and focus.

## Key-state summary

| Focus | `enter` / space | `n` / `N` | `o` | `c` | `x` |
|---|---|---|---|---|---|
| Standalone tool | toggle existing detail | next/previous visible target | all detail/groups | toggle compact groups | clear search/filter |
| Collapsed group | reveal children | next/previous target | expand all levels | toggle compact groups | clear search/filter |
| Expanded group | hide children | next/previous target | collapse all levels | toggle compact groups | clear search/filter |
| Child card | toggle child detail | sibling/header/next item | all detail/groups | toggle compact groups | clear search/filter |

The command palette exposes the same named actions. Help text reflects current
state (`compact tools: on` / `compact tools: off`) while keeping `c` as the
dedicated key.
