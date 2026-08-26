# Implementation Plan: Zed-Style Monitor Transcript

**Status:** Proposed; ready for human review

**Primary surface:** `internal/tui/monitor/monitor_transcript.go`

**Related implementation:** Spec 11, Monitor Tool Call Groups

**Risk:** medium

**Estimated focused implementation time:** 12–14 hours

---

## Summary

Replace the monitor's event-log presentation with a conversation-first transcript:
assistant prose remains visually primary, while reasoning and tool activity become
compact, muted, single-level disclosures inspired by Zed's Agent Panel. The main
risk is preserving stable search, navigation, paging, and Gate-context behavior
when a `tool_use` block and its later `tool_result` block become one visual item.

## Confirmed design decisions

The following decisions were confirmed in the plan review and are implementation
constraints, not deferred design questions:

1. A **Transcript item** is the page-local presentation unit for rendering,
   search, navigation, expansion, and restored monitor state. A **Tool exchange**
   is the Transcript item formed by a matching tool use and result in the same
   generation, iteration, and attempt.
2. Pairing is deliberately page-local. A bounded page may represent the same
   durable exchange as paired, use-only, or result-only depending on which blocks
   are loaded; the Monitor never performs an unbounded historical scan to repair
   that representation.
3. One Tool exchange has one navigation target and one disclosure. Expanding it
   reveals both Input and Output sections; it does not introduce nested section
   navigation.
4. Exact sequence numbers and timestamps remain in `transcript.jsonl`; this
   slice adds no metadata-mode key to the Monitor.
5. Expanded details use the tested initial contract of 4,096 bytes, 12 rendered
   rows, and a 3-row tail. Later tuning must update the related tests and ANSI
   snapshots rather than weakening the bound incidentally.
6. A successful result without a visible matching use is a muted result-only
   disclosure with unknown origin, not a normal successful exchange. A use without
   a visible result is pending only while its step runs and unknown thereafter.
7. A paired exchange is anchored and ordered at its tool use. Out-of-order results
   enrich their matching use row and never move it.
8. Search and role/filter matching inspect every member block, but retain a
   matching Tool exchange atomically. In particular, `role:user` can retain an
   exchange through its role-user tool result without presenting it as user prose.
9. Tool classification is presentation-only: recognize stable semantic tool
   families, use a generic sanitized fallback for unknown tools, and never let
   classification affect execution, security, or permissions.
10. Narrow activity rows preserve selected/state marker, action, and disclosure
    marker before optional detail; clipping uses terminal cell width.
11. Every `RoleUser` + `BlockText` is User guidance and renders as Markdown in
    the subtle user surface. Tool-result and system content remain verbatim.
12. Unknown roles or block types render as quiet, expandable unsupported items;
    they are never silently omitted.
13. In a filtered view, execution dividers reflect coordinate transitions between
    adjacent visible items, including a divider for an initial later coordinate.

## Feature summary

Make the per-step Transcript panel substantially quieter and more space-efficient
without losing inspectability: remove redundant entry chrome, pair each tool call
with its result, hide routine details by default, reserve strong color and framing
for meaningful states, and make all detail expansion bounded and predictable in a
terminal viewport.

## Approach

Introduce a small presentation-normalization layer between the raw bounded
`[]transcript.Entry` page and the rendered viewport. That layer will convert raw
blocks into stable `transcriptItem` values, correlate tool uses and results by a
scoped tool-use identity, apply search/filter policy to whole items, and produce
the navigation/render plan consumed by `chatBody`. Implement structural behavior
before styling: first characterize the target with tests, then replace Spec 11's
coarse tool groups and two-level expansion, then simplify visual styling, and
finally validate search, paging, auto-follow, resize, and Gate snapshot behavior.
The persisted transcript format, runner, engine event bus, and persistence-off
path remain unchanged.

---

## Why this change is needed

The current renderer contains many individually useful features, but their visual
weights combine into a noisy whole:

- `chatBody` repeats the selected step's status inside the Transcript body even
  though the same state is already present in the Steps panel and surrounding
  monitor chrome.
- Normal entries may render `#<seq> <role>` and a timestamp before their content.
  These are valuable debugging fields but poor default reading hierarchy.
- Thinking, tool use, and tool result each have their own label color, colored
  thick bar, disclosure marker, preview, and expansion state.
- Spec 11 adds an outer `N tool calls` group around those already-collapsible
  blocks, producing two levels of expansion and navigation.
- A successful tool exchange can occupy a group row, a tool-use row, a result row,
  and blank separator rows even though its useful default summary is often only
  `Read monitor_transcript.go` or `Run go test ./...`.
- Tool results are treated as independently important content even when they only
  confirm routine success.
- `shared.Theme.Chat.BlockCursor` places the full selected label on a primary-color
  background, giving navigation state more weight than assistant prose.
- Arbitrary JSON-looking tool output may be syntax highlighted even when it is log
  or command output that should remain verbatim.
- Expanded content is byte-bounded but not directly row-bounded, so a single
  expansion can still displace most of the conversation from a terminal viewport.

The existing folded and unfolded snapshots demonstrate the density problem. The
unfolded form uses separate rows and blank gaps for `Read`, its result, `Run`, and
its result:

```text
  ▌ ▾ 2 tool calls
  ▌ ▸ ◈ Read · config.toml

  ▌ ▸ ↳ result configuration loaded

  ▌ ▸ $ Run · go test ./internal/tui/monitor

  ▌ ▸ ↳ result PASS
```

The target representation is one quiet row per exchange:

```text
  ◈ Read    config.toml
  $ Run     go test ./internal/tui/monitor                 ▸
```

The full inputs and outputs still exist in `transcript.jsonl` and remain available
through a single expansion action.

---

## Zed design observations to adapt

This plan borrows hierarchy rather than attempting pixel-for-pixel replication.
Zed's current Agent Panel implementation establishes several useful patterns:

1. Assistant messages render as Markdown without a persistent role/timestamp
   header around every response.
2. Thinking is a muted `Thinking` disclosure. Expanded content is nested behind a
   thin guide and constrained so it cannot consume the entire panel.
3. Each tool call has a semantic label and icon. Routine calls can remain simple
   rows; confirmation, edit, and terminal operations may use card framing.
4. Tool input and output are hidden unless the call is expanded or needs operator
   confirmation.
5. Failure and cancellation are status treatments, not separate full-weight
   transcript messages.
6. Empty canceled calls can be omitted because they add no useful information.
7. File edits and aggregate review live on purpose-built surfaces rather than
   forcing every raw tool result into the conversational flow.

Terminal adaptations are necessary:

- Persistent `▸`/`▾` markers replace hover-only disclosure controls.
- A small selected marker replaces hover background and mouse focus.
- Fixed line caps plus head/tail elision replace inner scroll views and gradient
  fades.
- Existing output-file rows and review-diff views remain the detailed file
  surfaces; transcript rows do not attempt clickable IDE navigation.
- Unicode glyphs must always be accompanied by text or layout so color and icon
  support are never the sole carriers of meaning.

Reference material:

- Zed Agent Panel overview:
  <https://zed.dev/docs/ai/agent-panel#overview>
- Assistant and tool-entry rendering:
  <https://github.com/zed-industries/zed/blob/5a2039b2fd2e24b9267d2d379e23ed3c0c3d6739/crates/agent_ui/src/conversation_view/thread_view.rs#L6319-L6425>
- Thinking disclosure and constrained content:
  <https://github.com/zed-industries/zed/blob/5a2039b2fd2e24b9267d2d379e23ed3c0c3d6739/crates/agent_ui/src/conversation_view/thread_view.rs#L7432-L7539>
- Tool layout selection and details:
  <https://github.com/zed-industries/zed/blob/5a2039b2fd2e24b9267d2d379e23ed3c0c3d6739/crates/agent_ui/src/conversation_view/thread_view.rs#L8160-L8666>
- Semantic tool labels:
  <https://github.com/zed-industries/zed/blob/5a2039b2fd2e24b9267d2d379e23ed3c0c3d6739/crates/agent_ui/src/conversation_view/thread_view.rs#L9959-L10108>

---

## Goals

1. Make assistant prose the strongest and easiest content to scan.
2. Reduce a successful tool exchange to one row in the default view.
3. Replace group-plus-block expansion with one disclosure level per activity.
4. Pair tool uses and results without changing the durable transcript schema.
5. Hide routine successful output while keeping failures immediately legible.
6. Bound every expanded detail by both captured bytes and rendered rows.
7. Preserve transcript order, paging bounds, search/filter correctness, navigation,
   resize behavior, Gate context restoration, and live follow semantics.
8. Centralize semantic tool presentation so Claude SDK and ACP-backed agents share
   the same monitor experience.
9. Use semantic theme styles rather than ad hoc colors or styles in monitor files.
10. Reduce the responsibilities and size of `monitor_transcript.go` by separating
    normalization and view rendering along stable boundaries.

## Non-goals

1. No changes to `internal/transcript`, its JSONL format, or writer behavior.
2. No changes to `internal/runner`, `internal/harness`, or engine events.
3. No workflow TOML fields, feature flags, compatibility modes, or dual renderers.
   Jig is pre-v1: replace the old presentation in the same change.
4. No attempt to reproduce Zed shadows, gradients, hover-only controls, mouse
   affordances, proportional type, or inner graphical scroll containers.
5. No inline permission controls. Jig's Gate remains the single operator-input
   surface and must stay non-blocking.
6. No unbounded transcript indexing. Search remains bounded to the loaded page.
7. No speculative tool status. The UI must not claim completion, success, or
   failure unless the loaded transcript and step state support that claim.
8. No generic renderer framework for unrelated TUI screens. Extract only helpers
   whose semantics are genuinely shared by transcript item kinds.
9. No retrospective rewrite of historical proof files or completed Spec 11
   validation. This plan documents the intentional replacement.

---

## Repository constraints and invariants

The implementation must preserve the following repository rules:

- **File is truth, bus is liveness.** The Transcript panel continues to render
  finalized entries from `transcript.jsonl`; `StepOutput` remains only the
  provisional live tail.
- **Persistence-off remains first-class.** `RunDir == ""` must continue to produce
  the existing graceful placeholder. This feature adds no writer and must not
  attempt file I/O on the persistence-off path.
- **Bounded memory and reads.** `chatWindowMax` and opaque transcript page offsets
  remain the bound. Correlation may use only the entries loaded for the current
  page plus the existing bounded boundary context.
- **Width changes invalidate width-dependent Markdown only.** A resize must not
  destroy expansion, cursor, page, search, filter, follow, or Gate snapshot state.
- **Theme singleton only.** New styles belong in
  `internal/tui/shared/styles.go` and derive from the existing semantic palette.
  No `lipgloss.NewStyle()` calls may be added to monitor rendering files.
- **Verbatim non-prose.** Command output and tool results must not pass through
  Glamour merely because they happen to contain valid Markdown or JSON.
- **Comments explain why.** New comments should document non-obvious invariants
  such as scoped tool correlation and page-edge orphan behavior, not narrate
  straightforward loops.
- **Pre-v1 replacement.** Delete obsolete tool-group fields, helpers, and tests in
  the same change. Do not retain an old-renderer branch.

---

## Current code map

| File | Current responsibility | Planned responsibility |
|---|---|---|
| `internal/tui/monitor/monitor_transcript.go` | Loading, paging, grouping, render-plan construction, body rendering, Markdown, disclosure rendering, diffs, verbatim text, and file view | Loading, paging, page state, follow behavior, and orchestration only |
| `internal/tui/monitor/monitor_model.go` | Monitor state plus `chatItem`, `toolGroup`, and `renderItem` definitions | Monitor state plus stable item/navigation/cache keys; remove group-only state |
| `internal/tui/monitor/monitor_tool_summary.go` | Decode tool arguments and produce a label/preview | Single semantic classifier for action, detail, icon, layout class, and safe fallback |
| `internal/tui/monitor/monitor_search.go` | Filter raw entries while keeping consecutive tool runs atomic | Search/filter normalized items while keeping exact call/result exchanges atomic |
| `internal/tui/monitor/monitor_update.go` | Two-level group/block navigation and expansion | One-level activity navigation and expansion |
| `internal/tui/monitor/monitor_gate_context.go` | Snapshot group/block cursor and expansion maps | Snapshot the simplified activity cursor and expansion map |
| `internal/tui/monitor/monitor_layout.go` | Viewport sizing, Markdown renderers, cursor visibility | Keep layout; build detail renderer at the correct inset width and preserve caches |
| `internal/tui/monitor/monitor_view.go` | Breadcrumb panel titles, footer, follow state | Move selected-step status into compact surrounding chrome if body status is removed |
| `internal/tui/shared/styles.go` | Colored thinking/tool/result bars and block cursor | Neutral activity hierarchy, thin guide, selected marker, user prompt, semantic errors |

`monitor_transcript.go` is currently approximately 1,100 lines. Adding the new
normalization behavior in place would make loading, state derivation, and rendering
harder to reason about. The implementation should introduce:

- `monitor_transcript_items.go` for normalized item construction, tool pairing,
  expansion/navigation derivation, spacing policy, and pure detail bounds.
- `monitor_transcript_view.go` for `chatBody`, row/disclosure rendering, Markdown
  and verbatim section rendering, error callouts, and file-body rendering.

The exact move can be mechanical, but it must happen after characterization tests
exist so behavior is not silently lost during the split.

---

## Target visual hierarchy

The normal priority order is:

1. User-authored prompt or guidance.
2. Assistant prose and code.
3. Failures or operator-required action.
4. Current in-progress activity.
5. Routine completed tool activity.
6. Reasoning and raw input/output details.
7. Sequence numbers, timestamps, and transport-shaped internals, which are omitted
   from the default view.

Color follows state, not block type:

- Base foreground: assistant and user content.
- Muted foreground: completed activities, thinking headers, detail labels, guides,
  timestamps if ever exposed in a diagnostic surface, and truncation notices.
- Primary/accent: focused disclosure marker and current pending activity only.
- Warning: partial/truncated/interrupted state.
- Danger: failed tool or result error.
- Success color is not needed for every routine completed tool; absence of failure
  is sufficient and avoids turning the transcript into a field of green checks.

---

## Target transcript example

```text
╭─ 2f9c0a31 · feature › plan · running › Transcript · LIVE ─────────╮
│                                                                   │
│  Please inspect the monitor transcript and simplify its UI.       │
│                                                                   │
│  The current renderer gives internal activity too much visual     │
│  weight. I’ll trace the presentation model first.                 │
│                                                                   │
│  ◌ Thinking                                                     ▸ │
│  ⌕ Search   writeCollapsible                                      │
│  ◈ Read     monitor_transcript.go                                 │
│  ◈ Read     styles.go                                             │
│  $ Run      go test ./internal/tui/monitor                      ▸ │
│                                                                   │
│  The durable transcript format can stay unchanged. The monitor    │
│  can pair each call with its result at presentation time.         │
│                                                                   │
│  … Generating                                                     │
│  Next I’ll update the rendering tests and                         │
╰───────────────────────────────────────────────────────────────────╯
```

Selected activity:

```text
  › $ Run      go test ./internal/tui/monitor                     ▸
```

Expanded activity:

```text
  › $ Run      go test ./internal/tui/monitor                     ▾
      │ Input
      │ go test ./internal/tui/monitor
      │
      │ Output
      │ ok   jig/internal/tui/monitor  0.41s
```

Failed activity:

```text
  › × Run      go test ./internal/tui/monitor                     ▾
      │ Output
      │ --- FAIL: TestTranscriptItemPairing
      │ expected one visual item; got two
      │ exit status 1
```

Bounded long detail:

```text
      │ Output · 127 lines
      │ first line
      │ second line
      │ third line
      │ … 120 lines hidden …
      │ final line
      │ exit status 1
```

---

## Rendering contract by content kind

### User text

`RoleUser` plus `BlockText` represents genuine human guidance or resume input.
It must not be confused with `RoleUser` tool-result entries.

Default rendering:

- A subtle, compact prompt surface using a low-contrast border or background from
  `shared.Theme.Chat.UserMessage`.
- No `user` role label unless usability testing shows author identity is unclear.
- One blank row before and after the surface, except at the top/bottom boundary.
- Every `RoleUser` + `BlockText` is User guidance and renders with the same safe,
  themed prose renderer as assistant text; tool-result and system content remain
  verbatim.
- The surface must wrap to `transcriptInnerW` without horizontal overflow.

### Assistant text

Default rendering:

- Render directly through the existing themed Glamour path.
- Remove `#seq assistant` and timestamp headers.
- Do not add a colored side bar or assistant badge.
- Preserve code blocks, lists, links, emphasis, and wrapping.
- Tone down Markdown styles whose background or color competes with the transcript
  hierarchy. Syntax color remains appropriate inside actual code blocks.
- Consecutive assistant text chunks in the same logical turn should not acquire an
  empty row merely because the transport produced multiple entries.

### Thinking

Collapsed rendering:

```text
  ◌ Thinking                                                     ▸
```

Rules:

- Never show a one-line reasoning preview by default.
- Never display a character count as the normal disclosure affordance.
- Use muted text and a muted semantic icon.
- Use one expansion level.
- Expanded content uses a thin neutral guide, not a colored thick bar.
- Apply Markdown only to thinking content when expanded.
- Apply the row budget described in “Bounded detail policy.”
- If `Block.Truncated` is true, display `truncated at capture` rather than an
  inferred original character count that the transcript does not contain.
- Do not auto-expand completed thinking. The current event stream does not persist
  partial thinking entries, so simulating Zed's live auto-expanded reasoning would
  be misleading.

### Tool exchange

A matched `BlockToolUse` and `BlockToolResult` render as one item.

Collapsed routine success:

```text
  ◈ Read     monitor_transcript.go
  ⌕ Search   writeCollapsible
  ↗ Fetch    zed.dev
```

Collapsed pending/unknown:

```text
  … Run      go test ./...
```

Collapsed failure:

```text
  × Run      go test ./... · exit status 1                       ▸
```

Rules:

- The row contains semantic icon, action, compact detail, state marker when
  meaningful, and disclosure marker only if expandable content exists.
- Successful result content is hidden by default.
- Failure displays a short, sanitized first useful line in the row when it fits.
- Full input and output share the same disclosure.
- `Input` and `Output` are muted section labels, not independent navigation items.
- Tool input JSON is pretty-printed when valid. Tool output remains verbatim by
  default, including output that happens to be valid JSON.
- Image/resource content is out of scope because the current transcript block does
  not represent it.
- A matched result is never also rendered as a separate result row.

### Result-only tool block

A page can begin after the matching use, a backend may omit a use, or corrupt/stale
data may be incomplete. Render the orphan honestly:

```text
  ↳ Tool result                                                 ▸
```

If it is an error:

```text
  × Tool result · permission denied                             ▸
```

For a non-error result, use the same muted disclosure without a success claim:
its origin is unknown even if its visible result is non-error. Do not search
earlier pages automatically or perform an unbounded scan merely to improve the
label. Page-edge behavior must remain deterministic and bounded.

### Use-only tool block

If no result is loaded:

- While the selected step is running, label it pending with `…`.
- When the selected step is terminal, leave it neutral/unknown rather than marking
  it successful.
- If there is expandable input, keep the disclosure available.
- Do not synthesize a failure from absence alone; a tool result may be outside the
  loaded page or omitted by the backend.

### System text / command step output

Default rendering:

```text
  $ Command output · 84 lines                                   ▸
```

Rules:

- Replace always-expanded `writeVerbatim` output with a single disclosure item.
- Expanded output is verbatim and row-bounded.
- Preserve ANSI text only according to the existing safety behavior; do not
  interpret arbitrary terminal control sequences as monitor layout.
- Errors tied to step state may use danger treatment; ordinary stderr-like text
  cannot be assumed to mean failure without result state.

### Result error

`RoleResult` text is a terminal step failure summary, not normal assistant prose.

Default rendering:

```text
  × Agent failed · connection closed unexpectedly               ▸
```

Rules:

- Show the first useful line immediately.
- Use danger style plus text/glyph, never color alone.
- Long content expands behind a bounded disclosure.
- Do not render it as a normal assistant Markdown response.

### Retry, iteration, and generation boundaries

Keep orchestration boundaries because they communicate deterministic jig state:

```text
  ── Re-run 2
  ── Iteration 3
  ── Retry 2
```

Rules:

- Muted divider, no entry header.
- Emit only on a real increase from the preceding visible item.
- Preserve existing generation → iteration → attempt priority if several values
  advance at one boundary.
- In a filtered view, emit a divider only for a coordinate transition between
  adjacent visible items. If the first visible item starts in a later coordinate,
  emit its divider so the view does not hide that execution context; do not scan
  outside the loaded page or duplicate dividers around removed items.

### Live assistant tail

`StepOutput` is provisional text and must stay inexpensive to repaint.

Default rendering:

```text
  … Generating
  partial assistant text
```

Rules:

- Replace the bright `typing…` label with a muted activity indicator.
- Render partial text verbatim/wrapped, not through Glamour on every delta.
- Keep the existing `outputMaxLines` bound or replace it with an equivalently
  explicit live-tail row bound.
- `StepMessage` continues to reset the buffer before the finalized transcript
  entry is loaded, preventing duplicate final content.

---

## Presentation data model

### Why normalize before rendering

Raw transcript entries encode transport and persistence boundaries. Those are not
the same as visual conversation boundaries. Rendering directly from entries forces
`chatBody`, search, grouping, navigation, and spacing to rediscover relationships
independently. A normalized immutable page model provides one source of truth for:

- tool correlation;
- item identity;
- item ordering;
- search/filter atomicity;
- expandable navigation targets;
- status inference;
- spacing policy;
- detail sections;
- render-cache keys.

### Proposed types

Names may be adjusted during implementation, but responsibilities and invariants
should remain:

```go
type transcriptItemKind uint8

const (
    itemUserText transcriptItemKind = iota
    itemAssistantText
    itemThinking
    itemToolExchange
    itemToolResultOnly
    itemSystemOutput
    itemResultError
    itemBoundary
    itemUnsupported
)

type transcriptItemKey struct {
    kind  transcriptItemKind
    anchor blockKey
}

type transcriptBlockRef struct {
    key        blockKey
    role       transcript.Role
    block      transcript.Block
    generation int
    iteration  int
    attempt    int
}

type toolExchange struct {
    use       transcriptBlockRef
    result    *transcriptBlockRef
    summary   toolCallSummary
    state     toolDisplayState
    errorHint string
}

type transcriptItem struct {
    kind     transcriptItemKind
    key      transcriptItemKey
    primary  transcriptBlockRef
    tool     *toolExchange
    boundary string
}

type renderCacheKey struct {
    block   blockKey
    surface renderSurface
}
```

Key decisions:

- Store `transcript.Block` values in refs rather than pointers into a temporary
  filtered-entry slice. Strings and `json.RawMessage` remain cheap immutable views,
  while the item graph cannot retain a fragile pointer into a copied slice.
- Include item kind in `transcriptItemKey` so a boundary or result-only item cannot
  collide with a tool exchange anchored at the same `blockKey`.
- Give render cache entries a surface discriminator. Once a tool item contains
  both input and output, caching both under the tool-use `blockKey` would return the
  wrong content depending on which section rendered first.
- Keep width-dependent truncation out of normalized items. Summary detail may be
  stored in full bounded form and truncated at render time using visible cell
  width.

### Model state replacement

Delete Spec 11's group-only state:

```go
chatGroupHeaders
chatGroupExpand
chatGroupForBlock
toolGroup
chatItem.isGroup
renderGroupHeader
renderGroupGap
```

Replace it with one-level state:

```go
chatItems         []transcriptItem
chatNavItems      []transcriptItemKey
chatItemCursor    int
chatExpanded      map[transcriptItemKey]bool
chatExpandAll     bool
chatItemForBlock  map[blockKey]transcriptItemKey
chatLineRanges    map[transcriptItemKey]lineRange
chatRendered      map[renderCacheKey]string
```

Whether field names retain the `chat` prefix should follow existing monitor
conventions. Do not retain both old and new fields after migration.

### Value-receiver and map behavior

The monitor uses value receivers in several places while sharing map storage.
That is safe but subtle. The new implementation should prefer pointer receivers
for functions that intentionally mutate item state, caches, line ranges, expansion
maps, or viewports. Pure classification, pairing, spacing, and bounds helpers should
remain ordinary functions with no model receiver. This makes mutation visible at
the call site and reduces reliance on Go map reference semantics.

---

## Tool correlation algorithm

### Correlation key

Do not pair solely on `ToolUseID`. Scope it to the execution coordinates carried
by the entries:

```go
type toolCorrelationKey struct {
    generation int
    iteration  int
    attempt    int
    id         string
}
```

This prevents a stale or reused backend ID from correlating across a manual re-run,
loop iteration, or automatic retry.

### Deterministic pairing

For the currently loaded page:

1. Traverse entries and blocks in persisted order.
2. Convert each block to a `transcriptBlockRef` with entry execution coordinates.
3. Each non-empty-ID `tool_use` creates a `toolExchange` item at the use position
   and is appended to a FIFO queue keyed by `toolCorrelationKey`.
4. Each non-empty-ID `tool_result` attaches to the earliest preceding unmatched
   use in the same queue.
5. A matched result does not create another visual item. It enriches the item
   anchored at its matching tool use, so out-of-order results never reorder tool
   activity rows.
6. A result without a preceding loaded match creates a result-only item at its own
   position.
7. Empty IDs never pair. Render them as independent honest items rather than using
   fragile adjacency guesses.
8. Text, thinking, system, result, and unsupported blocks preserve their exact
   relative positions.

FIFO pairing handles defensive duplicate IDs deterministically. The expected
backend case remains one globally unique use followed by one result.

### Page boundaries

The existing `completeToolBoundaryContext` may load a bounded adjacent run of
tool-only entries. Keep that behavior, but do not rely on it for correctness:

- If the use and result are both loaded, pair them.
- If only the use is loaded, render use-only.
- If only the result is loaded, render result-only.
- Loading another page may produce a different local representation because the
  visible bounded dataset changed. This is acceptable and deterministic.
- Never scan the complete transcript to repair a visual label.
- Preserve `chatBoundaryContextMax` or an equivalent explicit bound.

### Tool display state

Infer only the following:

| Evidence | Display state |
|---|---|
| Matching result with `IsError=true` | failed |
| Matching result with `IsError=false` | completed |
| No result and selected step is running | pending |
| No result and selected step is terminal/non-running | unknown |
| Result-only with `IsError=true` | failed result |
| Result-only with `IsError=false` | result with unknown origin (no success claim) |

Do not infer rejected, canceled, permission-waiting, or edit-interrupted states;
those concepts are not explicit in the current transcript contract.

---

## Tool semantic classification

`monitor_tool_summary.go` remains the only place that interprets tool name and
arguments for display. Expand `toolCallSummary` or replace it with a clearer
presentation type containing:

```go
type toolLayoutClass uint8

const (
    toolLayoutQuiet toolLayoutClass = iota
    toolLayoutCommand
    toolLayoutEdit
    toolLayoutInteraction
    toolLayoutUnknown
)

type toolCallSummary struct {
    icon       string
    action     string
    detail     string
    class      toolLayoutClass
    expandable bool
}
```

Recommended mappings:

| Canonical kind | Action | Detail | Class |
|---|---|---|---|
| read | Read | compact path | quiet |
| glob | Find | pattern | quiet |
| grep | Search | pattern/query | quiet |
| websearch | Search web | query | quiet |
| webfetch | Fetch | host | quiet |
| edit | Edit | compact path | edit |
| write | Write | compact path | edit |
| notebookedit | Edit notebook | compact path | edit |
| bash | Run | command | command |
| task/subagent | Agent/Explore/Test/etc. | description | interaction |
| askuserquestion | Ask | first question | interaction |
| todo | Update tasks | bounded count | quiet |
| skill | Use skill | skill name | quiet |
| unknown | display tool name | primary safe argument | unknown |

Classification rules:

- Continue stripping MCP server prefixes from display names without losing the raw
  name in persisted data.
- Do not expose malformed raw JSON in a collapsed summary.
- Prefer a compact path with enough trailing components to disambiguate common
  filenames. The current basename-only `shortFile` may turn multiple `index.go`
  calls into indistinguishable rows; a cell-bounded `shortPath` should retain the
  last two or three components when width permits.
- Width truncation must use terminal cell width, not `utf8.RuneCountInString`, so
  CJK, emoji, and combining characters cannot overflow the panel.
- Sanitize newlines/control characters before placing argument-derived text in a
  one-line row.
- Unknown tools should degrade to a muted hammer/tool icon plus display name, not
  an unsupported block warning.
- Tool classification must not control execution, security, or permissions. It is
  presentation-only.

---

## Search and filter semantics

The current `filteredEntries` implementation preserves consecutive tool blocks as
one atomic group. Replace entry-level filtering with item-level filtering after
tool correlation.

### Searchable content

| Item | Search text |
|---|---|
| User/assistant/thinking text | raw block text |
| Tool exchange | raw name, semantic action/detail, raw input, matched output |
| Result-only tool | raw result content |
| System output | raw text |
| Result error | raw error text |
| Boundary | boundary label |

### Atomicity

- If query or filters match either side of a tool exchange, retain the whole tool
  item.
- Map every member block to the exchange's `transcriptItemKey` in
  `chatItemForBlock`.
- A search hit in result content navigates to the tool row and expands the Output
  section; a hit in input expands the Input section.
- If only one expansion bit exists per item, expanding the item reveals both
  sections. Do not recreate nested section navigation unless real usability data
  demands it.
- Search remains local to `chatItems` derived from the loaded bounded page.
- `role:user` may match a tool exchange because its result entry has `RoleUser`;
  retain the atomic exchange but continue to render it as a tool exchange, not
  User guidance.
- `tools` matches tool exchanges and result-only tool items.
- `reasoning` matches thinking only.
- `errors` matches failed tool exchanges, failed result-only tools, and result
  errors.
- Retry filtering uses the execution coordinates on item block refs.

### Search highlights

The existing `▶ match N/M · preview` row is visually loud. Retain it initially for
behavioral safety, but restyle it using the selected marker and muted preview. If a
later usability pass removes the row, it must still expose current hit index and
preview in the search/status surface.

---

## Expansion and navigation contract

### One level only

- `chatNavItems` contains every expandable thinking, tool, system-output, and
  result-error item in visual order.
- Assistant and user prose are not navigation targets unless a later interaction
  explicitly requires it.
- `n`/`N` move among `chatNavItems` or search hits as today.
- `enter`/`space` toggles `chatExpanded[currentKey]`.
- `o` toggles `chatExpandAll` as a read-only override and does not mutate individual
  expansion preferences.
- Collapsing all restores prior per-item expansion choices when `o` is toggled off,
  matching the existing override semantics.

### Stable identity

- A tool exchange is anchored at its `tool_use` block.
- A result-only item is anchored at its result block.
- Thinking/system/result items use their own block.
- Save the selected `transcriptItemKey` before rebuild and restore by key afterward.
- If filtering removes the selected key, choose the nearest remaining item by
  original order, falling back to index zero only when no better item exists.
- Expansion maps are pruned to keys present in the newly loaded page so memory does
  not grow with the complete transcript.

### Viewport behavior

- Navigating to a collapsed row makes the row visible with the existing margin.
- Navigating to an expanded item taller than the viewport anchors its header, not
  the bottom of its content.
- Manual expansion pauses auto-follow exactly as it does today.
- A streaming `StepMessage` reload preserves the selected key when still present.
- A resize preserves selection, expansion, page, offset, and follow state.

---

## Bounded detail policy

The current `chatExpandMax = 4096` limits bytes laid out but does not guarantee a
small terminal footprint. Replace or supplement it with a line-aware policy.

Initial tested constants:

```go
const (
    transcriptDetailMaxBytes = 4096
    transcriptDetailMaxLines = 12
    transcriptDetailTailLines = 3
)
```

These values are the initial interaction contract. Any later tuning must update
the related tests and ANSI snapshots; the policy must remain explicit and tested.

Algorithm:

1. Apply the existing capture-time `Block.Truncated` semantics first; the renderer
   never claims access to omitted persisted data.
2. Bound the display input by bytes without splitting UTF-8.
3. Split into logical lines.
4. If within the row limit, render all lines.
5. Otherwise render the first `max-tail-1` lines, an elision line, and the final
   `tail` lines.
6. Report hidden line count, not a guessed hidden character count.
7. Wrap each retained line to the available visible cell width. If wrapped rows
   would exceed the budget, apply the budget to rendered rows rather than source
   newlines.
8. Preserve a final error/exit-status line in the tail.

Use one pure helper returning a value such as:

```go
type boundedDetail struct {
    lines       []string
    hiddenLines int
    byteElided  bool
}
```

This helper is reusable for thinking, input, output, command steps, and result
errors, and can be exhaustively unit tested without ANSI styling.

---

## Spacing policy

Spacing should be a pure relationship between adjacent item kinds rather than an
unconditional separator after every raw entry.

Suggested policy:

| Previous → next | Blank rows |
|---|---:|
| activity → activity | 0 |
| thinking → tool | 0 |
| user prose → assistant/activity | 1 |
| assistant prose → activity | 1 |
| activity → assistant prose | 1 |
| assistant prose → assistant prose in same turn | 0 or Markdown-owned spacing |
| boundary → any content | 1 after divider only if needed |
| any content → result error | 1 |

Implement `spacingBefore(previous, current transcriptItem) int` as a pure helper.
Do not scatter blank-line decisions through individual render functions.

---

## Theme and styling plan

All new styles live under `Styles.Chat` in
`internal/tui/shared/styles.go`. Suggested semantic fields:

```go
Chat.UserMessage
Chat.ActivityIcon
Chat.ActivityAction
Chat.ActivityDetail
Chat.ActivityPending
Chat.ActivityError
Chat.DetailLabel
Chat.DetailGuide
Chat.ItemCursor
Chat.Truncation
```

Existing `Chat.Hint`, `Chat.CodeBlock`, and `Chat.CodeText` remain reusable.

Delete or stop using presentation-specific styles made obsolete by this change:

```go
Chat.Thinking
Chat.ToolCall
Chat.ToolResult
Chat.BlockCursor
Chat.BarThinking
Chat.BarToolCall
Chat.BarToolResult
Chat.BarError
```

Only delete a style after `rg` confirms it has no consumers outside the monitor.

Style rules:

- Activity icons, actions, details, and thinking default to muted tokens.
- The selected item uses a narrow prefix/marker rather than a background across the
  whole label.
- Error uses danger plus `×` and error text.
- Pending uses an accent or secondary token plus `…`; do not rely on animation.
- Detail guides use the dim border token and the thin `│` glyph.
- User message surface uses existing `bgLess`/`bgLeast`-style semantic tokens and
  must remain readable on the fixed dark canvas.
- Markdown headings should not use an H1 background pill inside ordinary assistant
  responses. Prefer bold/base or primary foreground.
- Inline code and fenced code may retain their background because code is the one
  content class where syntax styling improves comprehension.
- Links remain distinguishable without making all prose colorful.

---

## Readability improvements in the implementation

1. **Split responsibilities.** Loading/page state, item normalization, and view
   rendering should not remain in one 1,100-line file.
2. **Name by presentation semantics.** Prefer `transcriptItem`, `toolExchange`,
   `detailSection`, and `toolDisplayState` over group-oriented or transport-oriented
   names.
3. **Remove stale compatibility concepts.** Delete `toolGroup`, `isGroup`, group
   expansion maps, group gaps, and double-expansion branches together.
4. **Centralize spacing.** A pure spacing policy prevents blank-line regressions.
5. **Centralize detail rendering.** Thinking, input, output, command, and result
   errors should share row bounds and guide layout without duplicating loops.
6. **Keep classification pure.** Tool-name/argument interpretation remains isolated
   from monitor state and rendering styles.
7. **Use explicit mutation.** Prefer pointer receivers for rebuild/cache/viewport
   mutation; use pure functions elsewhere.
8. **Use cell-width helpers.** Do not use rune count when aligning or clipping
   terminal rows.
9. **Keep normalized values immutable.** Rebuild slices instead of partially
   mutating item graphs after search/filter changes.
10. **Render-plan clarity.** `chatBody` should remain a simple iterator over already
    resolved items; pairing, filtering, and status inference do not belong in the
    output loop.

---

## Reliability improvements

### Correct correlation

- Scope tool IDs by generation/iteration/attempt.
- Pair FIFO and only with preceding uses.
- Handle empty, duplicate, missing, and page-edge IDs without panic or fabricated
  state.
- Preserve block order for interleaved parallel calls.

### Stable state

- Key selection, expansion, line ranges, search targets, and cache entries by
  explicit stable value types.
- Restore selection by key after reload rather than relying on an old index.
- Prune page-local maps on page changes.
- Ensure Gate snapshot cloning covers the new expansion map and item key.

### Safe rendering

- Sanitize control characters in collapsed labels.
- Use visible cell width for clipping and padding.
- Preserve UTF-8 boundaries during byte elision.
- Keep tool output verbatim so Markdown cannot alter command/log meaning.
- Use distinct render cache surfaces for assistant prose, thinking, tool input,
  tool output, and file content.
- Never display a false original-size count when capture truncation removed data.

### Bounded work

- Item normalization is O(blocks in loaded page).
- Tool correlation uses maps/queues, not repeated scans.
- Tool summaries decode JSON once per page rebuild, not once per repaint.
- Width-dependent clipping occurs during rendering without reparsing tool JSON.
- Expanded content obeys byte and row limits.
- Search remains page-local.
- Resize invalidates only width-dependent render caches.

### Graceful degradation

- Unknown roles/blocks render a quiet unsupported placeholder rather than panic.
- Unknown tools retain their display name and a generic icon.
- Missing transcript files retain existing placeholders.
- Persistence-off performs no writes and no failed file reads.
- A corrupt or partial trailing transcript line remains the reader's concern and is
  skipped as today.
- Very narrow terminals preserve selected/state marker, action, and disclosure
  marker before optional detail. Clip detail using visible terminal cell width.

---

## Reusability improvements

Extract small semantic helpers, not a generic component framework:

| Helper | Reused by |
|---|---|
| `buildTranscriptItems` | reloads, page changes, search/filter rebuilds |
| `pairToolBlocks` or integrated item builder | Claude SDK and all ACP backends |
| `summarizeToolCall` | every tool exchange |
| `boundDetail` | thinking, input, output, command, result error |
| `writeDisclosureRow` | thinking, tool, command, result error |
| `writeDetailSection` | tool input/output and expanded text-like detail |
| `spacingBefore` | all adjacent transcript items |
| `truncateActivityDetail` | every collapsed one-line activity |
| `renderCacheKey` surfaces | all cached Markdown sections |

Avoid these abstractions:

- A generic “card” component accepting arbitrary callbacks and styles.
- A cross-screen transcript item interface with one implementation per block type.
- Reflection-based rendering.
- Storing Lip Gloss styles inside item/model values.
- A second transcript schema only for presentation.

---

## File-by-file implementation details

### `internal/tui/monitor/monitor_model.go`

- Add stable item, correlation, display-state, and cache key types that are part of
  monitor state.
- Replace group fields with `chatItems`, one-level navigation, one expansion map,
  member-to-item lookup, and surface-aware render cache.
- Update `gateContextSnapshot` fields to store the new selected item key and cloned
  expansion map.
- Replace `chatCollapseWidth` if previews disappear; keep only bounds still used.
- Add row-bound constants beside existing transcript bounds.
- Update comments to explain bounded page/state invariants.
- Do not place rendering functions or tool-classification logic in this file.

### `internal/tui/monitor/monitor_transcript.go`

- Retain `reloadTranscript`, page loading, boundary context, page pruning, follow,
  and page switching.
- Reset and prune the new one-level state on step/page changes.
- Call item rebuilding after loading/filter changes.
- Preserve saved item identity across same-step streaming reloads.
- Remove group accumulation and obsolete render branches once new item construction
  is active.
- Keep persistence-off and file-open failures graceful.

### `internal/tui/monitor/monitor_transcript_items.go` (new)

- Define pure item-building helpers if types do not belong in `monitor_model.go`.
- Flatten entries into safe block refs.
- Insert execution boundaries.
- Pair tool blocks with scoped FIFO correlation.
- Infer conservative display state.
- Build member-to-item lookup.
- Apply item-level search/filter visibility or expose the canonical items to
  `monitor_search.go`.
- Build navigation keys and restore cursor by identity.
- Implement pure spacing and detail-bound helpers.
- Contain no Lip Gloss styles and no file I/O.

### `internal/tui/monitor/monitor_transcript_view.go` (new)

- Move `chatBody` and presentation-only helpers here.
- Render user, assistant, thinking, tool, system, result-error, boundary, live-tail,
  diff, verbatim, and file surfaces.
- Implement one disclosure row and one inset detail-section path.
- Maintain line ranges using item keys.
- Use surface-aware Markdown caching.
- Keep width-sensitive summary clipping at render time.
- Keep raw tool output verbatim.
- Avoid recomputing semantic tool summaries.

### `internal/tui/monitor/monitor_tool_summary.go`

- Separate icon/action/detail fields rather than concatenating label punctuation.
- Add layout class.
- Improve compact path disambiguation.
- Add one-line control-character sanitization.
- Preserve safe fallback behavior for malformed and unknown tool inputs.
- Keep all helpers deterministic and side-effect-free.

### `internal/tui/monitor/monitor_search.go`

- Filter/search items rather than raw consecutive tool groups.
- Search every member of a tool exchange.
- Return item keys and matching member surfaces for navigation.
- Preserve filter semantics and page-local bounds.
- Remove group-boundary placeholder behavior that only existed to prevent adjacent
  groups from merging.

### `internal/tui/monitor/monitor_update.go`

- Replace group/block toggle branching with one item toggle.
- Preserve `chatExpandAll` as a read-only override.
- Restore cursor by item key after rebuild.
- Keep search-hit navigation precedence over ordinary `n`/`N` navigation.
- Pause follow on manual navigation/expansion as today.

### `internal/tui/monitor/monitor_gate_context.go`

- Snapshot and restore selected item key, expansion map, expand-all flag, page end,
  search, filters, scroll, follow, and seen sequence.
- Clone maps so Gate navigation cannot alias and mutate the saved state.
- Preserve behavior when the snapshotted item is no longer present after streaming;
  fall back deterministically to the nearest visible item.

### `internal/tui/monitor/monitor_layout.go`

- Recalculate inset renderer width for a thin-guide prefix instead of `"  ▌ "`.
- Invalidate the new surface-aware render cache on transcript width change.
- Keep file renderer behavior unchanged unless Markdown theme changes require a
  corresponding assertion.
- Continue using panel frame helpers; add no layout magic numbers.

### `internal/tui/monitor/monitor_view.go`

- Remove the status line from `chatBody` and add selected-step status to the
  breadcrumb or another compact existing chrome location so narrow single-panel
  mode still communicates state.
- Keep `Transcript · LIVE`, `PAUSED`, and unseen count semantics.
- Ensure title truncation preserves run identity, step ID, content label, and useful
  status in the existing breadcrumb priority order.

### `internal/tui/monitor/keys.go`

- Keep existing keys unless a tested conflict appears.
- Change help text from `n/N block` to `n/N activity` and from `o all` to a clearer
  `o details`/`o all details` label that fits compact help.
- Do not add a permanent metadata-mode key in this slice. Exact sequence/timestamp
  data remains available in the raw transcript and search internals.

### `internal/tui/shared/styles.go`

- Add semantic transcript activity styles using existing tokens.
- Remove obsolete bar/cursor styles after confirming no external consumers.
- Tone down Markdown H1/background treatment for conversational prose.
- Retain code-specific styling and fixed dark theme assumptions.
- Add no hard-coded colors outside `DefaultTheme` token construction.

---

## Test strategy

### Characterization fixture

Create one dense fixture containing:

- initial user text;
- assistant Markdown with list and fenced code;
- thinking;
- read use/result;
- two parallel uses followed by out-of-order results;
- successful command output;
- failed command output;
- unknown MCP tool;
- malformed tool input;
- use-only block;
- result-only block;
- capture-truncated result;
- system command output;
- result error;
- retry, iteration, and generation boundary;
- final assistant prose.

The default snapshot must prove routine internals no longer dominate the screen.
The expanded snapshot must prove all information remains inspectable within bounds.

### Item construction tests

Table-driven tests should cover:

1. One use plus one result → one item.
2. `use1`, `use2`, `result2`, `result1` → two items anchored in use order with
   correctly matched outputs.
3. Duplicate IDs → FIFO deterministic pairing.
4. Same ID across different retry/iteration/generation → no cross-scope pairing.
5. Empty ID → independent items.
6. Result before use → result-only plus later use-only.
7. Page starts with result → result-only.
8. Page ends with use → use-only.
9. Thinking/text between use and result → pair still succeeds without reordering
   intervening content; the tool row remains at use position.
10. Unknown/unsupported blocks preserve order, render as inspectable quiet items,
    and do not panic.

### Default rendering tests

Assert that ANSI-stripped default output:

- contains assistant prose;
- contains semantic one-line tool rows;
- contains a Thinking row but not reasoning content;
- contains no `#1 assistant`, normal timestamp, `N tool calls`, separate successful
  `result` row, raw input JSON, thick `▌` bar, or full-background selected label;
- shows a failed result hint;
- shows no successful result body;
- contains at most one blank line between consecutive activity rows;
- remains within `transcriptInnerW` visible cells.

### Expansion tests

- Enter expands a selected tool once and reveals both Input and Output.
- A second Enter collapses it.
- Thinking expansion reveals bounded Markdown behind a thin guide.
- Result-only, system output, and result error expand through the same interaction.
- `o` expands all details without mutating individual expansion preferences.
- Long input/output reports hidden lines and keeps the final tail.
- Capture truncation reports `truncated at capture`.
- Expanded JSON input is pretty; JSON-looking output remains verbatim.
- Input and output caches cannot collide.

### Navigation and viewport tests

- `n`/`N` visit each expandable item exactly once.
- Search hit navigation still takes precedence.
- Cursor remains on the same key after expand/collapse.
- Cursor remains stable after width change.
- Cursor remains stable after a same-step `StepMessage` reload.
- Tall expanded detail keeps its disclosure header visible.
- Manual activity navigation pauses follow.
- Following reloads the newest page and restores bottom behavior.

### Search/filter tests

- Query matches only tool input → tool row visible and expandable.
- Query matches only tool output → same tool row visible and Output is reachable.
- Error filter includes failed exchange and matched context.
- Tool filter includes exchange once, not call plus result.
- Reasoning filter includes thinking only.
- Role filters inspect all member roles.
- Retry filter respects scoped execution coordinates.
- Result-only page-edge items remain searchable.
- Clearing search/filter restores the same normalized canonical ordering.

### Gate-context tests

- Jump from a Gate to its step, expand an item, search/filter, scroll, return, and
  restore the original transcript state.
- Streaming while in Gate context does not corrupt the snapshot.
- Missing saved key falls back safely.
- Expansion maps are deep-cloned rather than aliased.

### Width and accessibility tests

- Wide split, minimum 40-cell transcript, and narrow single-panel layouts.
- Wide runes and emoji never overflow a row.
- Long command/path detail truncates before action/state.
- ANSI-stripped output still communicates pending, failed, and disclosure state.
- Selected marker remains visible without its color.

### Performance tests/checks

No benchmark is required unless implementation shows regressions, but tests and
review must confirm:

- page-local map sizes are proportional to loaded blocks;
- state maps are pruned on page change;
- JSON summary decoding happens during rebuild, not every `chatBody` repaint;
- a resize does not rebuild tool correlation;
- a stream burst still uses frame coalescing and avoids Glamour for partial tails;
- expand-all never lays out unbounded captured output.

---

## Manual acceptance scenarios

Generate ANSI snapshots through `JIG_UI_SNAPSHOT_DIR` for:

1. Default dense transcript at 120×40.
2. Same transcript with one command expanded.
3. Same transcript with all details expanded.
4. Minimum two-panel split near `transcriptMinInnerWidth`.
5. Narrow single Transcript panel.
6. Search hit inside a tool output.
7. Error filter view.
8. Running transcript with live assistant tail.
9. Paused transcript with unseen-entry count.
10. Persistence-off placeholder.

Human review questions:

- Does assistant prose dominate at first glance?
- Can the operator tell what the agent is doing without opening details?
- Do ten routine reads feel like a quiet activity list rather than ten alerts?
- Is failure visible before expansion?
- Is one expansion action sufficient?
- Can expanded content ever push the relevant header off screen?
- Does selected state remain obvious without a large colored background?
- Are narrow terminal rows still meaningful after truncation?
- Does the UI still feel like jig's monitor rather than an imitation graphical IDE?

---

## Ordered implementation tasks

Every substantive code task has an explicit test task. Estimates are wall-clock
minutes for a focused agent with full repository access.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add the dense target transcript fixture and failing default/expanded hierarchy assertions | `internal/tui/monitor/monitor_transcript_test.go` | 30 min |
| 2 | Add stable transcript item, correlation, display-state, expansion, line-range, and render-cache key types; remove group-only model fields | `internal/tui/monitor/monitor_model.go` | 30 min |
| 3 | Add table-driven tests for model-key equality, scoped correlation identity, and initialized one-level state | `internal/tui/monitor/monitor_transcript_test.go` | 20 min |
| 4 | Retain loading/paging/follow responsibilities and migrate reset/pruning/reload behavior to the new item state | `internal/tui/monitor/monitor_transcript.go` | 30 min |
| 5 | Add tests for page reload, page-state pruning, use-only/result-only boundaries, persistence-off, and cursor restoration | `internal/tui/monitor/monitor_transcript_test.go` | 30 min |
| 6 | Implement immutable item construction, scoped FIFO tool correlation, boundary insertion, spacing, and bounded-detail helpers | `internal/tui/monitor/monitor_transcript_items.go` | 45 min |
| 7 | Add exhaustive pairing, ordering, spacing, UTF-8, byte-bound, and row-bound tests | `internal/tui/monitor/monitor_transcript_items_test.go` | 45 min |
| 8 | Expand semantic tool summaries with icon/action/detail/layout class, compact paths, cell-safe labels, and control sanitization | `internal/tui/monitor/monitor_tool_summary.go` | 30 min |
| 9 | Add summary tests across Claude/Cursor/Codex/generic ACP names, malformed JSON, duplicate filenames, wide runes, and controls | `internal/tui/monitor/monitor_transcript_test.go` | 25 min |
| 10 | Implement conversation-first body rendering, one-level disclosures, detail sections, error callouts, live tail, diffs, and file view | `internal/tui/monitor/monitor_transcript_view.go` | 50 min |
| 11 | Add default/expanded/error/system/live-tail rendering tests and visible-width assertions | `internal/tui/monitor/monitor_test.go` | 45 min |
| 12 | Replace raw-entry/consecutive-group search filtering with normalized item search and member-to-item hit mapping | `internal/tui/monitor/monitor_search.go` | 35 min |
| 13 | Add atomic tool input/output search, role/error/retry filter, orphan, and page-local search tests | `internal/tui/monitor/monitor_search_test.go` | 35 min |
| 14 | Replace group/block toggles with one-level activity navigation and stable-key rebuild behavior | `internal/tui/monitor/monitor_update.go` | 25 min |
| 15 | Add navigation, expand-all override, streaming reload, follow pause/resume, and cursor visibility tests | `internal/tui/monitor/monitor_test.go` | 35 min |
| 16 | Migrate Gate context snapshots to selected item keys and one expansion map | `internal/tui/monitor/monitor_gate_context.go` | 20 min |
| 17 | Add deep-clone, streaming, missing-key fallback, and round-trip Gate context tests | `internal/tui/monitor/monitor_gate_context_test.go` | 25 min |
| 18 | Rebuild inset widths/cache invalidation for thin guides and surface-aware Markdown cache keys | `internal/tui/monitor/monitor_layout.go` | 20 min |
| 19 | Add resize/cache-surface/line-range tests, including tall details and narrow widths | `internal/tui/monitor/monitor_transcript_test.go` | 25 min |
| 20 | Move selected-step status from transcript body to compact breadcrumb chrome | `internal/tui/monitor/monitor_view.go` | 15 min |
| 21 | Add wide/narrow breadcrumb and transcript-body deduplication tests | `internal/tui/monitor/monitor_test.go` | 15 min |
| 22 | Rename transcript help labels for activity navigation and detail expansion | `internal/tui/monitor/keys.go` | 10 min |
| 23 | Update compact/full help assertions for the new terminology | `internal/tui/monitor/monitor_test.go` | 15 min |
| 24 | Add neutral semantic transcript styles, simplify Markdown hierarchy, and remove unused colored-bar styles | `internal/tui/shared/styles.go` | 30 min |
| 25 | Add ANSI-stripped semantic-state and Markdown/code readability assertions | `internal/tui/monitor/monitor_transcript_test.go` | 25 min |
| 26 | Delete obsolete Spec 11 group rendering/helpers and mechanically complete the responsibility split | `internal/tui/monitor/monitor_transcript.go` | 20 min |
| 27 | Run focused/full tests, gofmt, vet, build, and generate the manual ANSI acceptance matrix | `internal/tui/monitor` | 30 min |

Estimated total from the task estimates: **approximately 12 hours 40 minutes**.
Allow additional review time for visual tuning after ANSI snapshot inspection.

---

## Implementation checkpoints

### Checkpoint 1: Characterized and normalized

Complete tasks 1–9.

Exit criteria:

- Raw loaded pages normalize deterministically.
- Tool pairs and orphans are correct.
- Existing UI may still render through an adapter, but the new item model is fully
  tested.
- No schema, runner, or engine changes.

### Checkpoint 2: New rendering active

Complete tasks 10–15.

Exit criteria:

- Default view is conversation-first.
- One tool exchange equals one row and one expansion action.
- Search/filter and navigation operate on item identity.
- Old tool-group behavior is no longer user-visible.

### Checkpoint 3: State and visual hardening

Complete tasks 16–25.

Exit criteria:

- Gate snapshots, resize, caches, breadcrumb status, help, and semantic styles are
  correct.
- Narrow and non-color output remains legible.
- No strong color remains solely because content is a tool result or reasoning.

### Checkpoint 4: Cleanup and proof

Complete tasks 26–27.

Exit criteria:

- Obsolete group code and fields are deleted.
- Responsibility split is complete.
- All automated checks pass.
- ANSI captures pass human review.

---

## Verification commands

```bash
go test ./internal/tui/monitor -count=1
go test ./internal/tui/shared -count=1
go test ./...
gofmt -l -w .
go vet ./...
go build ./cmd/jig
```

For visual captures:

```bash
JIG_UI_SNAPSHOT_DIR=/tmp/jig-monitor-zed-style \
  go test ./internal/tui/monitor -run 'Test.*Transcript.*Snapshot' -count=1
```

The implementation should use a task-specific temporary directory rather than
assuming `/tmp/jig-monitor-zed-style` is safe to overwrite.

---

## Acceptance criteria

### Functional

- [ ] A matched successful tool use/result renders as one collapsed row.
- [ ] A matched failed tool use/result renders as one row with an immediately
      visible error indication.
- [ ] Input and output are revealed with one expansion action.
- [ ] Thinking content is hidden by default and bounded when expanded.
- [ ] System/command output is collapsed and bounded.
- [ ] Result errors are callouts, not assistant messages.
- [ ] User text is visually distinct from assistant text and tool-result role-user
      entries.
- [ ] Every `RoleUser` + `BlockText` renders as Markdown User guidance; tool-result
      and system content remain verbatim.
- [ ] Unknown roles and block types remain visible as inspectable unsupported
      transcript items.
- [ ] Retry/iteration/generation boundaries remain visible.
- [ ] Filtered views show execution dividers only for visible coordinate
      transitions, including an initial later coordinate.
- [ ] Search and filters preserve tool exchange atomicity.
- [ ] Paging remains bounded and page-edge orphans are honest.
- [ ] `n`/`N`, Enter/Space, `o`, search navigation, page keys, and follow keys work.
- [ ] Gate context round-trips all transcript state.
- [ ] Persistence-off remains graceful.

### Visual

- [ ] No normal `#seq role` or timestamp headers.
- [ ] No duplicate selected-step status row inside the body.
- [ ] No outer `N tool calls` group.
- [ ] No separate routine-success result row.
- [ ] No thick colored role bars.
- [ ] No full-label primary background cursor.
- [ ] Assistant prose is the dominant visual content.
- [ ] Consecutive activities use no blank separator rows.
- [ ] Color communicates pending/warning/error/selection rather than block type.
- [ ] Expanded detail uses a thin neutral guide and explicit section labels.
- [ ] Every rendered line fits the available visible cell width.
- [ ] Narrow activity rows preserve selected/state marker, action, and disclosure
      marker before optional detail.
- [ ] Narrow and ANSI-stripped views remain understandable.

### Reliability and performance

- [ ] Tool IDs cannot correlate across generation/iteration/attempt.
- [ ] Empty, duplicate, missing, and out-of-order IDs cannot panic.
- [ ] Render cache surfaces cannot collide.
- [ ] UTF-8 is never split during truncation.
- [ ] Expanded content is byte- and row-bounded.
- [ ] JSON summary decoding is not repeated per repaint.
- [ ] State maps remain bounded to the loaded page.
- [ ] Resize preserves semantic state and invalidates only width-dependent caches.
- [ ] Streaming frame coalescing remains intact.

---

## Risks and mitigations

### Risk 1: Search hits point at absorbed result blocks

When result content becomes part of a tool item, a hit keyed only by result
`blockKey` may have no rendered line range.

Mitigation:

- Build `chatItemForBlock` for every member.
- Store the matching surface in the hit.
- Navigate to and expand the owning item.
- Add input-only and output-only search tests.

### Risk 2: Pairing crosses execution attempts incorrectly

A backend or resumed session could reuse a tool ID.

Mitigation:

- Scope correlation by generation, iteration, and attempt.
- Use FIFO matching within the scope.
- Test repeated IDs across every execution coordinate.

### Risk 3: Page-edge tools lose labels or appear twice

Bounded pages may contain only one side of an exchange.

Mitigation:

- Treat unmatched blocks as first-class use-only/result-only items.
- Never scan unbounded history.
- Keep bounded boundary-context reads as an optimization, not a correctness
  dependency.

### Risk 4: Expanded detail still overwhelms the viewport

Byte bounds do not predict wrapped terminal rows.

Mitigation:

- Apply a rendered-row budget after wrapping.
- Preserve header and tail.
- Test narrow widths and long unbroken lines.

### Risk 5: State is lost during streaming or Gate navigation

Changing from group/block indices to items may invalidate saved indices.

Mitigation:

- Save stable item keys, not indices.
- Deep-clone expansion maps.
- Restore by key with deterministic fallback.
- Preserve existing streaming/Gate regression tests and add missing-key cases.

### Risk 6: Visual simplification hides important failures

Removing result rows could make errors less apparent.

Mitigation:

- Failed exchanges always show `×` plus a useful first line.
- Error filter maps to failed exchanges.
- Expanded Output remains one action away.
- Do not use muted success styling for an unmatched result marked error.

### Risk 7: Styling changes regress other TUI surfaces

`shared.Theme.Chat.Hint`, code styles, and Markdown config have consumers outside
the monitor.

Mitigation:

- Add new semantic fields instead of repurposing shared fields with different
  meanings.
- `rg` every style before deletion.
- Run all TUI and full repository tests.

### Risk 8: Current uncommitted work overlaps planned files

At plan creation time, the worktree already contains modifications in
`monitor_search.go` and `monitor_update.go`, along with broader ACP/Codex work.

Mitigation:

- Treat all existing changes as user-owned.
- Inspect diffs before implementation.
- Integrate the item-model changes into the current versions; never reset or
  overwrite those files.
- Keep commits narrowly scoped if the user later requests commits.

---

## Deferred enhancements

These could improve parity later but should not expand this implementation:

1. Persist explicit ACP tool kind, locations, and lifecycle status in the
   transcript. This would require coordinated harness, runner, transcript, and TUI
   changes and would raise risk to high.
2. Open a tool's referenced source file directly from its row.
3. Render image/resource tool content.
4. Inline permission cards. Jig should first reconsider the Gate architecture;
   duplicating actions is not acceptable.
5. Automatically fold very long bursts such as `12 reads`. Start with Zed-like
   one-row-per-call behavior, then add burst folding only if terminal captures show
   real density problems.
6. A diagnostic metadata mode for sequence numbers and timestamps. Raw JSONL is
   sufficient until operators demonstrate a frequent need inside the TUI.
7. Mouse hover/click affordances. Keyboard/focus behavior remains primary.

---

## Plan status

`status = ready`

The repository paths, current types, current tests, theme location, paging model,
search/filter implementation, live-tail behavior, and Gate snapshot behavior were
verified before writing this plan. No unresolved question blocks implementation;
line-budget constants and minor glyph choices may be tuned from the required ANSI
snapshot review without changing the architecture.
