# Slice 08 — Tool-call grouping and the read tree

- **Slice ID:** `tool-call-grouping`
- **Outcome:** Consecutive same-kind, low-information tool calls (chiefly reads)
  collapse into one navigable item that still names every target, rendered as a
  tree.
- **Why this slice exists:** A step that reads eleven files currently produces
  eleven rows of near-identical chrome. Grouping is the single biggest density
  win available, and it is independent of the diff work.
- **Depends on:** Slices 02, 06.

---

## omp reference

### Only `read` is grouped

`packages/coding-agent/src/modes/components/read-tool-group.ts:330-889`.
Eligibility (`:39-43`):

```ts
export function readArgsCollapseIntoGroup(args: unknown): boolean {
    const target = readArgsTarget(args);
    if (target === undefined) return false;
    return target.startsWith(XD_URL_PREFIX) || !InternalUrlRouter.instance().canHandle(target);
}
```

Plain filesystem paths group. Internal URLs that resolve to *content*
(`skill://`, `agent://`) render as full cards so the resolved content stays
visible. This is the right instinct: **group calls whose result is uninteresting,
not calls that happen to share a name.**

### The three header shapes

Zero entries (`:547`):

```
 • Read
```

Exactly one row (`:552-567`) — no tree, the path rides the header:

```
 ● Read packages/coding-agent/src/tools/glob.ts:437-448
```

`●` is `status.enabled` in the plain **text** color for success — asserted by
omp's own test (`test/read-tool-group.test.ts:74-77`) to be specifically *not*
`✔` and *not* success-colored. A quiet dot, not a celebration.

Two or more (`:570-581`) — count badge plus tree:

```
 • Read (3)
   ├─ packages/coding-agent/test/streaming-preview-height.test.ts:301-409
   ├─ packages/coding-agent/test/tool-live-region-scrollback.test.ts:143-310
   └─ ✘ packages/tui/test/streaming-scrollback-defer.test.ts
```

- Header: `" • "` + bold `Read` (`toolTitle`) + dim `" (3)"`.
- Rows: **3-space indent** + `├─`/`└─` in dim + space + path in `accent`.
- **Successful rows omit their status glyph entirely** (`#formatRow`, `:717-721`:
  `statusPrefix = status === "success" ? "" : …`). Only failures, warnings, and
  pending calls show one. Verified at `test/read-tool-group.test.ts:94-95`.
- **No card.** The group is a flat, unframed compact list.

### Selector merging

Multiple ranges of the same file in one batch collapse to a single row; more than
three selectors elide the middle (`:322-328`):

```
   └─ src/task/render.ts:507-605,1070-1194,…,1270-1274
```

### Tree connectors are a shared 3-column primitive

`tui/utils.ts:80-90`, `tui/tree-list.ts:45-48`:

```
getTreeBranch(isLast)         → isLast ? "└─" : "├─"
getTreeContinuePrefix(isLast) → isLast ? "   " : "│  "
buildTreePrefix(ancestors)    → per ancestor: hasNext ? "│  " : "   "
```

`renderTreeList` appends one space after the connector, so the effective prefix
is **exactly 3 columns** in every state — connectors and continuations align
perfectly.

### What breaks a group

`event-controller.ts` — the group is finalized when:

- a non-read tool execution is created (`:1302-1303`)
- the streaming assistant message emits a new visible text or thinking block
  (`:1216-1219`)
- turn end, abort, session boundary, or a usage flush that cannot attach

The principle: **a group is a run of adjacent, uninterrupted, same-kind calls.**
Any intervening content closes it.

### Call and result are always one block

Every one of omp's 30 registered renderers sets `mergeCallAndResult: true`
(`tools/renderers.ts:95-141`). jig already implements this via
`transcriptItemToolExchange` — see CC-5. No work needed.

### Displacement — a distinct mechanism

`tool-execution.ts:70-79, 738-746` and
`chat-transcript-builder.ts:189-217`: `hub` waiting-polls and `todo` snapshots
mark themselves *displaceable*. A later same-tool call **removes** the earlier
block entirely rather than stacking. Failed follow-ups return `false` from
`canBeDisplacedBy`, so the last good panel survives. Best-effort — a block
already committed to scrollback is not retracted.

jig has no direct analogue today, but a repeated poll-shaped step could use one.

---

## Current jig state

`buildTranscriptItems` (`internal/tui/monitor/monitor_transcript_items.go:6`)
already does the harder half of this problem: it correlates tool uses with tool
results across entries, by `toolCorrelationKey{generation, iteration, attempt,
toolUseID}`, and even **reorders** a result-first ACP stream back to use position:

```go
// Move the completed exchange to its use position, preserving the
// visual ordering contract even when an ACP result arrived first.
```

It handles page-edge honesty (`standaloneToolItem` for a use or result whose
partner is off-page) and running-step state. This is solid infrastructure.

**What it does not do is group.** Each exchange is its own `transcriptItem`, so
eleven reads are eleven items — eleven cursor stops, eleven headers.

The now-dead `writeGroupHeader` (`monitor_transcript.go:962`, removed by slice
00) shows the previously attempted shape:

```
▌ ▸ 3 tool calls
```

A bare count with no content — strictly less useful than omp's tree, which names
the targets without expanding.

---

## In Scope

- A grouping pass over `[]transcriptItem` producing a group item that holds its
  members, run after `buildTranscriptItems` and before filtering.
- Eligibility rules: same tool kind, adjacent, same execution coordinate
  (generation / iteration / attempt), and a **kind allowlist** — start with
  `read`, and consider `glob`/`grep` only if their results are genuinely
  low-information.
- Break rules: any intervening item of a different kind, any text or thinking
  item, any execution-coordinate change.
- Tree rendering: 3-column connectors via a shared helper, target path per row,
  status glyph only on non-success rows.
- Single-member groups degrade to the plain one-line form (no tree).
- Selector/range merging for repeated reads of the same path.
- Navigation: a group is **one** cursor stop; expanding it reveals per-member
  detail. Preserve `chatItemLineRanges` correctness across both states.
- Filtering and search must see through a group — a query matching one member
  must keep the group visible. jig's `filteredTranscriptItems`
  (`monitor_transcript.go:229-246`) already implements exactly this contract for
  tool exchanges and its comment says so:

  > *"retains a complete conversation unit whenever one of its members matches.
  > In particular, a tool input or output hit never splits the exchange into two
  > independently visible rows."*

  Extend the same rule one level up.

## Out of Scope

- Displacement (superseding a previous poll/snapshot card) — no jig analogue yet;
  note as possible future work.
- `mergeCallAndResult` — already done (CC-5).
- Per-tool bespoke group bodies (omp's optional read content previews).
- Grouping across execution-coordinate boundaries — an iteration change is
  meaningful and must break the group.

## Functional Requirements

- **FR-08.1** Two or more adjacent eligible same-kind exchanges at the same
  execution coordinate shall render as one group item.
- **FR-08.2** A group shall name every member's target without requiring
  expansion.
- **FR-08.3** A group of one shall render identically to an ungrouped exchange.
- **FR-08.4** Any item of a different kind, or any text or thinking item, between
  two eligible exchanges shall prevent them from grouping.
- **FR-08.5** A change in generation, iteration, or attempt shall break a group.
- **FR-08.6** Tree connector prefixes shall occupy the same visible width in
  every state (branch, last, continuation).
- **FR-08.7** Successful member rows shall omit a status glyph; failed, warning,
  and pending members shall show one.
- **FR-08.8** A group shall be a single cursor stop, and expanding it shall
  reveal per-member detail.
- **FR-08.9** A search or filter match on any member shall keep the whole group
  visible.
- **FR-08.10** A group containing at least one failed member shall be visibly
  distinguishable from an all-success group without expansion.
- **FR-08.11** `chatItemLineRanges` shall remain accurate for a group in both
  collapsed and expanded states.

## Technical and Repository Constraints

- `transcriptItemKey` is `{anchor blockKey, kind transcriptItemKind}`. A group
  needs a stable key across reloads — anchor it to the **first member's** key so
  `rebuildTranscriptItemState`'s saved-cursor restoration keeps working
  (`monitor_transcript.go:211-224`).
- `prunePageState` (`:386-427`) prunes expand/render/line-range maps against
  loaded item keys. Group keys must participate or their state leaks.
- Grouping must run **before** `filteredTranscriptItems` so the group is the unit
  that filtering sees (FR-08.9).
- Page edges: `completeToolBoundaryContext` (`monitor_transcript.go:349`) already
  pulls neighbouring tool-only entries across a page boundary so an exchange is
  not split. A group may still straddle a page edge — it must render honestly as
  a partial group rather than silently dropping members.
- `defaultExpandEditCodeItems` (`:184-191`) auto-expands items with structured
  diffs. Confirm a group never contains one (reads do not) or the interaction
  needs defining.
- Per CC-7, tree glyphs go through the icon vocabulary.

## Security and Data Considerations

Grouping shows more paths in less space. These are already in the transcript and
already redacted; no new exposure. Prefer `shortFile` for display, consistent
with `summarizeActivity`.

## Acceptance Evidence

- Tests: three adjacent reads group; a read–bash–read sequence does not; a read,
  a text item, then a read does not; an iteration change breaks a group.
- A test asserting a one-member group renders identically to an ungrouped
  exchange.
- A test asserting a search hit on the third member keeps the group visible.
- A test asserting connector prefix widths are equal across branch/last/
  continuation.
- A test asserting a group with one failure is distinguishable from one without.
- A test asserting `chatItemLineRanges` matches rendered offsets in both states.

## Inputs for the Child Spec

- `buildTranscriptItems` already solves correlation, ordering, and page-edge
  honesty. **Do not rewrite it** — add a grouping pass over its output.
- omp's real rule is "group calls whose *result* is uninteresting." Resist
  grouping by tool name alone; a `bash` run is never uninteresting.
- The status-glyph-only-on-failure rule (FR-08.7) is what keeps the tree quiet;
  it is deliberate in omp and asserted by its tests.
- jig's existing "a match on any member keeps the unit visible" contract in
  `filteredTranscriptItems` is the precedent for FR-08.9 — extend it, do not
  invent a new rule.

## Open Questions

- **Q-08.1** Which kinds are eligible beyond `read`? `glob` and `grep` produce
  substantive results and probably should not group. *Suggest starting with
  `read` alone and widening on evidence.*
- **Q-08.2** Should an expanded group show each member's full detail sections, or
  a per-member preview? *Suggest per-member preview; full sections would make one
  cursor stop enormous.*
- **Q-08.3** How does a group interact with `o` (expand all)? Does it expand the
  group *and* every member? *Suggest: group first, members on a second press —
  but verify against the existing `chatItemExpandAll` semantics.*
- **Q-08.4** Should a partial group at a page edge render a marker saying members
  are off-page? *Leaning yes, using slice 06's vocabulary.*
