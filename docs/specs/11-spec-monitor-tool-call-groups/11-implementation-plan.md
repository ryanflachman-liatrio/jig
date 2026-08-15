# Implementation Plan: Monitor Tool Call Groups

Derived from the grilling session on the spec. All decisions are settled — this is
the authoritative design reference for implementation.

## Decision Map

| # | Topic | Decision |
|---|-------|----------|
| Q1 | chatItem union type | Discriminated struct: `{isGroup bool; key blockKey; group *toolGroup}` |
| Q2 | chatBlocks rebuild strategy | Two slices: canonical `chatGroupHeaders` + active `chatBlocks` |
| Q3 | chatExpandAll navigation | Full rebuild — `o` also populates chatBlocks with individual blocks from all groups |
| Q4 | toolGroup storage | Inline in chatItem (no separate map) |
| Q5 | primaryArg truncation display | `…` suffix when truncated to 40 runes |
| Q6 | groupKey type | Reuse `blockKey` of the group's first block; `chatGroupExpand map[blockKey]bool` |
| Q7 | Cursor stability after rebuild | Save group header item before rebuild; linear-search for its new index after |
| Q8 | chatExpandAll / chatGroupExpand interaction | Pure read-only override — `chatGroupExpand` is never mutated by `o` |
| Q9 | Group header render path | New `writeGroupHeader` helper, parallel to `writeCollapsible` |
| Q10 | chatBody render strategy | Render plan `chatRenderPlan []renderItem`; `chatBody` is a dumb iterator over it |
| Q11 | Entry headers inside expanded groups | Suppressed — tool-role entry headers are omitted from the render plan |
| Q12 | Name list truncation width | Dynamic: `m.transcriptInnerW` minus fixed left-margin prefix |
| Q13 | renderItem variant set | Five kinds: `renderEntrySep`, `renderEntryHeader`, `renderText`, `renderGroupHeader`, `renderBlock` |
| Q14 | Name list formatting | Dynamic inside `writeGroupHeader` at render time — no plan rebuild on resize |
| Q15 | rebuildActiveState | One combined helper producing both `chatBlocks` and `chatRenderPlan` in a single pass |

---

## Data Structures

### New types (monitor_transcript.go or a small internal type file)

```go
// chatItem is one entry in the canonical navigation list (chatGroupHeaders)
// and the active navigation list (chatBlocks).
// isGroup=false: a standalone collapsible block (thinking, or a block outside any group).
// isGroup=true:  a tool call group header; group points to its toolGroup payload.
// key is always the blockKey of the item's first block (used as groupKey for groups).
type chatItem struct {
    isGroup bool
    key     blockKey  // for groups: first block in the group (= groupKey for chatGroupExpand)
    group   *toolGroup
}

// toolGroup is the payload for a group header chatItem.
type toolGroup struct {
    blocks []blockKey  // all tool_use / tool_result blockKeys in the group, in order
    tools  []toolLabel // one entry per tool_use block (for the summary line)
    count  int         // len(tools) — the N in "N tool calls"
}

// toolLabel is one entry in the group summary line.
type toolLabel struct {
    name string // blk.Name from the transcript block
    arg  string // primaryArg(name, input) — already truncated to 40 runes with … suffix
}

// renderItem is one element of the pre-computed render plan.
// kind determines which fields are populated.
type renderItem struct {
    kind renderKind

    // renderEntrySep
    sep string // e.g. "── iteration 2 ──"

    // renderEntryHeader
    // key.seq is the entry seq; role is the entry role.

    // renderText, renderBlock
    // key identifies the block; blk is a pointer into chatEntries.

    // renderGroupHeader
    // key is the groupKey (first block); group points to the toolGroup.

    key  blockKey
    blk  *transcript.Block
    role transcript.Role
    group *toolGroup
}

type renderKind int

const (
    renderEntrySep    renderKind = iota
    renderEntryHeader
    renderText
    renderGroupHeader
    renderBlock
)
```

### New fields on Model (monitor_model.go)

```go
// chatGroupHeaders is the canonical navigation list built by loadChat: one
// chatItem per collapsible block (thinking) or tool call group. Stable until
// the next loadChat call.
chatGroupHeaders []chatItem

// chatRenderPlan is the materialized render sequence for chatBody. Rebuilt by
// rebuildActiveState whenever expansion state changes.
chatRenderPlan []renderItem

// chatGroupExpand records per-group expand state. Keyed by the blockKey of
// the group's first block (the groupKey). Parallel to chatExpand for individual
// blocks. Reset in reloadTranscript alongside chatExpand.
chatGroupExpand map[blockKey]bool
```

---

## Key Implementation Flow

### loadChat (monitor_transcript.go)

Extend the existing single loop into a **single-pass accumulator**:

1. For each entry and block, check `collapsible(blk.Type)`:
   - If `BlockToolUse` or `BlockToolResult`: append to `pendingGroup` accumulator.
   - If `BlockThinking`, text, or any non-tool type: flush `pendingGroup` as a
     group chatItem (if non-empty), then emit the non-tool block as a standalone
     chatItem (for thinking) or handle as before (for text).
2. After all entries, flush any remaining `pendingGroup`.
3. Store the result as `chatGroupHeaders`.
4. Call `rebuildActiveState()`.

Tool-role entries (containing only `tool_result` blocks) are absorbed into the
pending group from the previous assistant entry — no special cross-entry logic
needed; the accumulator just continues across the entry boundary.

### rebuildActiveState (monitor_transcript.go or inline in loadChat)

Single pass over `chatGroupHeaders`. For each canonical item:

**Building `chatBlocks` (active navigation list):**
- Always append the item (group header or standalone block).
- If `item.isGroup && (chatGroupExpand[item.key] || chatExpandAll)`:
  append one chatItem per `item.group.blocks` (individual blocks, `isGroup=false`).

**Building `chatRenderPlan` (render sequence):**
- Emit `renderEntrySep` and `renderEntryHeader` items as before, but:
  - Suppress `renderEntryHeader` for any entry whose blocks are fully absorbed
    into a group (tool-role entries).
- For group items: emit `renderGroupHeader`.
  - If expanded: emit `renderBlock` for each member block.
- For standalone collapsible items: emit `renderBlock`.
- For text items: emit `renderText`.

**Cursor stability:** before calling `rebuildActiveState`, capture
`savedItem = chatBlocks[chatBlockCursor]`. After rebuild, find `savedItem`'s
new index in the rebuilt `chatBlocks` by comparing `isGroup` and `key`. Set
`chatBlockCursor` to the found index (always found — group headers never
disappear from the active list).

Call sites for `rebuildActiveState`:
1. End of `loadChat`
2. Toggle handler (after flipping `chatGroupExpand[key]`)
3. `chatExpandAll` toggle (after flipping `chatExpandAll`)

`WindowSizeMsg` does **not** call `rebuildActiveState` — only `rebuildRenderer`
as today (name list is formatted dynamically, not stored in the plan).

### writeGroupHeader (monitor_transcript.go)

New helper, parallel to `writeCollapsible`. Inputs: the `chatItem`, whether the
group is expanded, whether the cursor is on it, and the current `transcriptInnerW`.

Renders:
```
  ▌ ▸ N tool calls: Name1(arg), Name2(arg), … (+K)
```

- Bar (`▌`) in `shared.Theme.Chat.BarToolCall` (charple accent, matching tool_use).
- Marker `▸` (collapsed) or `▾` (expanded).
- Name list formatted dynamically to fit `transcriptInnerW - fixedPrefixLen` runes,
  truncated with `… (+K)` where K is the omitted count.
- Cursor highlight via `shared.Theme.Chat.BlockCursor` when cursored.

### writeGroupHeader name list truncation

`primaryArg` helper (pure function):

```go
func primaryArg(name string, input json.RawMessage) string
```

1. Unmarshal input into `map[string]any`.
2. Try keys in order: `file_path`, `path`, `command`, `pattern`, `query`, `url`,
   `description`, `content`. Return first non-empty string value.
3. If none match, iterate keys in sorted order; return first string value.
4. If input is empty/nil or has no string values, return `""`.

Truncation: if `len([]rune(arg)) > 40`, replace the last rune with `…` so the
result is exactly 40 runes.

Group label: `Name(arg)` when arg is non-empty; `Name` when arg is `""`.

### chatBody (monitor_transcript.go)

Replace the `chatEntries` loop with a dumb iterator over `chatRenderPlan`:

```go
for _, item := range m.chatRenderPlan {
    switch item.kind {
    case renderEntrySep:    // write separator line
    case renderEntryHeader: // write "#N role" line
    case renderText:        // writeBlock (text path)
    case renderGroupHeader: // writeGroupHeader(...)
    case renderBlock:       // writeBlock (collapsible path)
    }
}
```

No grouping logic in `chatBody`. All decisions pre-computed.

### Toggle handler (monitor_update.go)

```go
case keybind.Matches(msg, m.keys.Toggle):
    if n := len(m.chatBlocks); n > 0 && m.chatBlockCursor < n {
        item := m.chatBlocks[m.chatBlockCursor]
        saved := item
        if item.isGroup {
            m.chatGroupExpand[item.key] = !m.chatGroupExpand[item.key]
        } else {
            m.chatExpand[item.key] = !m.chatExpand[item.key]
        }
        m.rebuildActiveState(saved)
        m.refreshPanels()
    }
```

### ExpandAll handler (monitor_update.go)

```go
case keybind.Matches(msg, m.keys.ExpandAll):
    saved := chatItem{}
    if len(m.chatBlocks) > 0 {
        saved = m.chatBlocks[m.chatBlockCursor]
    }
    m.chatExpandAll = !m.chatExpandAll
    m.rebuildActiveState(saved)
    m.refreshPanels()
```

### reloadTranscript reset (monitor_transcript.go)

Add `chatGroupExpand` to the per-step state that is cleared on step change:

```go
m.chatGroupExpand = make(map[blockKey]bool)
```

---

## Invariants

- `chatGroupHeaders` is always built fresh by `loadChat`; never mutated after.
- `chatBlocks` and `chatRenderPlan` are always derived from `chatGroupHeaders` +
  expansion state; never mutated directly.
- `chatBlockCursor` always points to an item that is a group header or a
  non-group collapsible block — never to an individual block inside a collapsed group.
- `chatGroupExpand` and `chatExpand` use disjoint key spaces in practice (a block
  is either in a group or standalone, never both), but are separate maps so there
  is no collision even if the same blockKey appears in both.
- `chatExpandAll` is a pure read-only override; toggling it never writes to
  `chatGroupExpand` or `chatExpand`.

---

## Resize Safety

`WindowSizeMsg` calls `rebuildRenderer` (existing) which invalidates `chatRendered`.
It does NOT call `rebuildActiveState` — the render plan stores no width-dependent
strings. The name list is formatted dynamically in `writeGroupHeader` using
`m.transcriptInnerW` on each `chatBody` call. Group expansion state is fully
preserved across resize.
