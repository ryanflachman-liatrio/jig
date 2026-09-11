# OMP-Parity Transcript Presentation

Epic for reshaping the run monitor's Transcript panel to match the look and feel
of [oh-my-pi (omp)](https://github.com/can1357/oh-my-pi), a TypeScript coding
agent whose transcript rendering is the reference target for this work.

> **Naming note.** The repo's `sdd-write-epic` skill writes
> `docs/epics/<epic_name>/<epic_name>.md`. This document is `epic.md` at the
> operator's explicit request; the slice files live in `slices/`.

---

## Executive Summary

**Problem.** jig's Transcript panel renders every conversation item as an
indented text row with a `│ `-prefixed detail block. Every row carries equal
visual weight, so a forty-step run reads as a uniform wall of text: a failed
tool call, a finished read, and a running agent are distinguishable only by a
short colored word. There is no surface that groups a tool call with its output,
no way to see *what changed* in an edit, and the block cursor shifts content two
columns sideways as it moves.

**Desired outcome.** The Transcript panel adopts omp's presentation grammar: the
unit of display is a **full-width rounded card** whose header lives inside the
top border, whose border and background tint are driven by execution state, and
whose body is a bounded, purpose-built rendering of that tool's content (a real
diff for edits, a numbered code cell for reads, a tree for grouped calls).
Finished successful work recedes; running and failed work asserts itself.

**Why this needs multiple specs.** The work spans four separable layers that
different specs can own independently:

1. a new **presentation primitive** (the card) in `internal/tui/shared`,
2. a **content grammar** (header slots, truncation vocabulary, glyph presets)
   that every renderer consumes,
3. **per-content renderers** (diffs, code cells, grouped reads) that each carry
   their own algorithmic work, and
4. **structural** changes to the item model (grouping, spacing, selection).

Attempting these as one specification would produce a single change touching
`internal/tui/shared/styles.go`, `internal/tui/shared/panel.go`,
`internal/tui/shared/icons.go`, six files in `internal/tui/monitor`, and a new
diff-computation dependency — with no intermediate demoable state.

---

## Goals

- **G1.** A tool call and its result render as one bounded card with a
  state-colored border, so scanning a transcript reveals failures and running
  work without reading text.
- **G2.** Finished successful work is visually quieter than running or failed
  work (measurable: success border uses the dim token, never accent).
- **G3.** Moving the block cursor never changes the horizontal position of any
  content.
- **G4.** An `edit` tool call shows what changed — a real diff with a line-number
  gutter and intra-line highlighting — not just the resulting file.
- **G5.** Consecutive same-kind tool calls collapse into one navigable unit that
  still names each target.
- **G6.** Every truncation affordance states the hidden quantity and the key that
  reveals it, using one consistent wording.
- **G7.** The panel degrades cleanly under `TERM=linux` / no-Nerd-Font terminals
  via an explicit glyph preset, not mojibake.

## Non-Goals

- **NG1.** Adopting omp's **native-scrollback retirement** architecture
  (`transcript-container.ts`: append-only stable-row ledgers, freeze-on-drift,
  two-phase offer/acknowledge, pinned-frontier watchdog). That design exists
  because omp does not own a scroll region; jig has a Bubble Tea viewport beside
  a Steps panel. Adopting it would fight the layout. See
  [`slices/04-vertical-rhythm-and-block-edges.md`](slices/04-vertical-rhythm-and-block-edges.md)
  for the narrow parts that *are* worth taking.
- **NG2.** OSC 133 shell-integration prompt zones (`user-message.ts:25-29`) —
  meaningful for a full-terminal REPL, not a panel.
- **NG3.** omp's proportional viewport allocator (1 row per block, surplus
  newest-first, `N more transcript blocks active`). It compensates for having no
  scrollbar; jig has one.
- **NG4.** Inline terminal graphics (Kitty/Sixel/iTerm image protocols).
- **NG5.** Changing the transcript **wire format** (`internal/transcript`) or the
  `toolcall.Activity` contract. This epic is presentation-only; every slice reads
  data that the harness already produces.
- **NG6.** Reworking the Steps panel, Gate panel, or review workspace.

---

## Shared Context and Constraints

### Where the transcript is actually rendered

`monitor_transcript.go` is **not** the live render path, despite being the file
the work was originally scoped against. `chatBody()` short-circuits at
`internal/tui/monitor/monitor_transcript.go:661`:

```go
if len(m.chatItems) > 0 {
    body := m.itemTranscriptBody()
    ...
    return body
}
```

`chatItems` is built by `buildTranscriptItems(page.Entries, …)` and is empty only
when `chatEntries` is empty. Therefore the render-plan machinery below that
branch is unreachable whenever there is anything to draw. The live renderer is
`itemTranscriptBody()` in
`internal/tui/monitor/monitor_transcript_items_view.go:16`.

Dead code confirmed in `monitor_transcript.go` (see
[`slices/00-remove-dead-render-plan.md`](slices/00-remove-dead-render-plan.md)):

| Symbol | Lines | Status |
|---|---|---|
| `rebuildActiveState`, `chatRenderPlan` construction | `435–640` | unreachable |
| render-plan iteration in `chatBody` | `781–842` | unreachable |
| `writeGroupHeader` | `962` | unreachable |
| `writeBlock` | `999` | unreachable |
| `writeCollapsible` | `1118` | unreachable |
| `withBar` | `1173` | unreachable |
| `collapseLine` | `1185` | unreachable |
| `writeDiff`, `hunkAt`, `writeRawDiff` | `1225–1275` | **zero callers**, test-only |

The **live** surface of `monitor_transcript.go` is: paging/follow state, empty
states, `writeReviewOverview`, search/filter chrome, page markers, the live tail,
`renderMarkdown` / `renderInsetMarkdown`, `writeVerbatim`, `fenceJSON`,
`jsonlToMarkdown`, `expandView`, `stripBlankEdges`, `stripSGR`, and `fileBody`.

### Repository standards every slice must honor

- **Pre-v1 breaking changes are expected.** `AGENTS.md:17-22`: "Do not preserve
  deprecated env vars, dual code paths, or migration wrappers 'just in case.'
  When replacing a mechanism, delete the old one in the same change." This
  directly authorizes slice 00.
- **All styles live in `internal/tui/shared/styles.go`.** Per `CLAUDE.md`, never
  add a bare package-level `var xStyle = lipgloss.NewStyle()`. Add a field to the
  appropriate sub-struct in `Styles`, set it in `DefaultTheme()` from the
  existing semantic tokens, reference it as `theme.X`. Do not pass styles as
  parameters or store them in component structs.
- **Never hardcode a hex color on a style.** Every style derives from the
  Charmtone tokens in `internal/tui/shared/palette.go`.
- **The theme is dark-only** (Charmtone "Pantera"). There is no
  `lipgloss.HasDarkBackground()` in v2 and no light variant to maintain.
- **Layout math uses `GetVerticalFrameSize()` / `lipgloss.Width`,** never magic
  numbers.
- **Comments explain the non-obvious "why,"** not the what.
- **Table-driven tests** with inline fixtures, per
  `internal/workflow/workflow_test.go` style.

### Invariants that must survive every slice

- **File is truth, bus is liveness.** The panel renders from
  `transcript.jsonl` on disk, never from the engine event bus. No slice may
  introduce a render path that depends on having observed a live event.
- **Persistence-off is a first-class path.** When `RunDir == ""`,
  `loadChatTail` sets an empty page and every renderer must no-op gracefully.
  This is the path most engine/runner tests exercise.
- **`monitorModel` uses value receivers but shares maps.** The render and expand
  caches (`chatItemRendered`, `chatItemExpand`, `chatItemLineRanges`) are
  reference types, so writes inside value-receiver methods persist. Caches are
  invalidated **wholesale** on width change (`rebuildRenderer`), never mutated
  field by field.
- **Per-step view state resets on step change.** `reloadTranscript`
  (`monitor_transcript.go:38`) clears expand/render/line-range maps because `seq`
  restarts per step file and cached renders would collide.
- **glamour bakes wrap width in at construction.** Any renderer that uses
  `m.renderer` / `m.insetRenderer` / `m.fileRenderer` must be rebuilt and its
  cache invalidated on `WindowSizeMsg`.
- **Only prose goes through glamour.** Command output and tool results get a
  verbatim path; glamour mangles non-markdown.
- **`chatItemLineRanges` drives scroll-to-item.** Any renderer that changes how
  many lines an item occupies must keep the line accounting in
  `itemTranscriptBody` correct, or `n`/`N` block navigation desynchronizes.

### Current jig budgets (the numbers slices will revise)

| Constant | Value | Location |
|---|---|---|
| `chatCollapseWidth` | 80 | `monitor_model.go:387` |
| `chatExpandMax` | 4096 | `monitor_model.go:391` |
| `chatWindowMax` | 300 | `monitor_model.go:396` |
| `chatBoundaryContextMax` | 16 | `monitor_model.go:400` |
| `outputMaxLines` | 10 | `monitor_model.go:403` |
| `transcriptDetailBytes` | 4096 | `monitor_transcript_detail.go:11` |
| `transcriptDetailRows` | 12 | `monitor_transcript_detail.go:12` |
| `transcriptDetailTailRows` | 3 | `monitor_transcript_detail.go:13` |

### omp reference checkout

Findings were gathered from a shallow clone of `can1357/oh-my-pi` at
`/tmp/omp-review` (not retained). All `file:line` references to omp in the slice
documents are relative to `packages/coding-agent/src/` unless noted. The clone
was at the repository default branch as of 2026-09-11.

---

## Cross-Cutting Decisions

| # | Decision | Status | Notes |
|---|---|---|---|
| **CC-1** | The unit of transcript display is a **card**, not an indented row. | **Decided** | Drives slices 01, 05, 07, 08. |
| **CC-2** | Execution state is carried by **border color + background tint + status glyph**, never by appending words like `" failed"` to a label. | **Decided** | Replaces `items_view.go:84-90`. |
| **CC-3** | Success **recedes**: dim border, near-invisible tint. Accent is reserved for running/pending; red for error. | **Decided** | omp `output-block.ts:73-81`. |
| **CC-4** | State transitions are **color changes, not glyph changes**, so rows do not twitch as steps finish. | **Decided** | omp `task/render.ts:963-979`. Conflicts with jig's current `○ → ● → ✓` in the Steps panel; this epic changes the **Transcript** only. |
| **CC-5** | A tool call and its result are always **one block**. | **Decided** — already true | `transcriptItemToolExchange` in `monitor_transcript_items.go` already implements this; omp spells it `mergeCallAndResult: true` on all 30 renderers. |
| **CC-6** | Card chrome is built on **`shared.PanelTopEdge`** (`shared/panel.go:158`), extended for a 3-dash cap and `├───┤` section dividers — not a second border implementation. | **Decided** | ADR 0001 already covers manual border-title compositing. |
| **CC-7** | Every new glyph goes through a **symbol map with a `unicode` / `ascii` preset**, not a bare const. | **Assumed** | Slice 14 establishes it; slices landing earlier must not hardcode glyphs. If slice 14 is deferred, earlier slices accrue a migration debt. |
| **CC-8** | Truncation wording is **one vocabulary** across the whole panel: `… N more <items>` appended for head windows, `… N earlier <items>` prepended for tail windows, both followed by the expand hint derived from the **live keybinding**. | **Decided** | Slice 06 owns it; slices 05/07/08 consume it. |
| **CC-9** | Background tints require **SGR-reset stabilization** — nested `\x1b[0m` inside glamour/Chroma output punches holes in a lipgloss `.Background()`. | **Blocking for slice 01** | omp solves it at `output-block.ts:86-93`. Must be proven with one card before tints are used anywhere. Resolution owner: slice 01 spec. |
| **CC-10** | Diff **computation** is new work; `go-gitdiff` only *parses* patches. | **Decided** | See slice 07 for the three options and the recommendation. |
| **CC-11** | The expand model stays **per-item plus global**, as today (`enter`/`space` toggles one item, `o` toggles all). omp has only the global `ctrl+o`. | **Decided** — keep jig's | jig's per-item cursor is strictly more capable; do not regress it to match omp. |
| **CC-12** | No slice may change the transcript wire format or `toolcall.Activity`. | **Decided** | See NG5. |

---

## Slice Breakdown

Each slice has a dedicated document under [`slices/`](slices/) with omp
references, current jig state, functional requirements, and acceptance evidence.
The table is the index; the files are the specification inputs.

| # | Slice ID | Title | Depends on |
|---|---|---|---|
| 00 | `remove-dead-render-plan` | [Remove the unreachable render-plan path](slices/00-remove-dead-render-plan.md) | None |
| 01 | `transcript-card-primitive` | [The transcript card primitive](slices/01-transcript-card-primitive.md) | 00 |
| 02 | `status-line-header-grammar` | [Four-slot status-line header grammar](slices/02-status-line-header-grammar.md) | 01 |
| 03 | `selection-affordance` | [Non-shifting selection affordance](slices/03-selection-affordance.md) | None |
| 04 | `vertical-rhythm-and-block-edges` | [Vertical rhythm and block edge trimming](slices/04-vertical-rhythm-and-block-edges.md) | 01 |
| 05 | `tool-detail-sections` | [Tool detail bodies as card sections](slices/05-tool-detail-sections.md) | 01, 02 |
| 06 | `truncation-vocabulary` | [One truncation vocabulary](slices/06-truncation-vocabulary.md) | None |
| 07 | `diff-rendering` | [Real diffs with a fused gutter](slices/07-diff-rendering.md) | 01, 06 |
| 08 | `tool-call-grouping` | [Tool-call grouping and the read tree](slices/08-tool-call-grouping.md) | 02, 06 |
| 09 | `message-framing` | [Message framing: bubbles, not labels](slices/09-message-framing.md) | 04 |
| 10 | `inline-thinking` | [Inline thinking and the streaming pulse](slices/10-inline-thinking.md) | 09 |
| 11 | `turn-metadata-row` | [Per-step metadata row](slices/11-turn-metadata-row.md) | 04 |
| 12 | `boundary-banners` | [Centered boundary banners](slices/12-boundary-banners.md) | None |
| 13 | `liveness-and-spinners` | [Liveness: phase-locked spinners](slices/13-liveness-and-spinners.md) | 02 |
| 14 | `glyph-presets` | [Glyph presets and ASCII fallback](slices/14-glyph-presets.md) | None |
| 15 | `inline-arg-formatting` | [Fair-share inline argument formatting](slices/15-inline-arg-formatting.md) | 02 |

---

## Dependency and Delivery Order

```
00 remove-dead-render-plan ──┐
                             ├─→ 01 transcript-card-primitive ──┬─→ 02 status-line-header-grammar ──┬─→ 05 tool-detail-sections
                             │                                  │                                   ├─→ 13 liveness-and-spinners
                             │                                  │                                   ├─→ 15 inline-arg-formatting
                             │                                  │                                   └─→ 08 tool-call-grouping
                             │                                  ├─→ 04 vertical-rhythm ──┬─→ 09 message-framing ──→ 10 inline-thinking
                             │                                  │                        └─→ 11 turn-metadata-row
                             │                                  └─→ 07 diff-rendering
03 selection-affordance      (independent)
06 truncation-vocabulary     (independent; 07 and 08 consume it)
12 boundary-banners          (independent)
14 glyph-presets             (independent; see CC-7)
```

**Recommended first slice: 00.** It is pure deletion authorized by the pre-v1
policy, removes roughly 450 lines, and makes every subsequent diff smaller and
easier to review. It has no design risk.

**Recommended second slice: 03.** Smallest visible win in the epic — a
one-expression fix to the selection prefix removes a jarring horizontal jump.
Independent of the card work, so it can land while slice 01's tint question
(CC-9) is still being resolved.

**Safe parallelism.** Slices **03**, **06**, **12**, and **14** touch disjoint
code and can proceed concurrently with the 00 → 01 → 02 spine. Slices **07** and
**08** are the two largest bodies of work and are independent of each other once
01/02/06 have landed; they can run in parallel by different implementers.

**Serialization hazard.** Slices 01, 02, 05, and 08 all edit
`monitor_transcript_items_view.go`. Running them concurrently will conflict.
Sequence them, or agree on the function boundaries first.

---

## Shared Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **Background tints punch through** where glamour/Chroma emit `\x1b[0m` (CC-9). | Cards render with holes; looks broken, not subtle. | Prove with a single tinted card containing a fenced code block **before** slice 01 commits to tints. Fall back to border-only state signaling if unsolvable in lipgloss. |
| **Full-width cards waste horizontal space** in a two-panel layout. The Transcript panel is already inside a bordered panel; a card inside it is a second border. | Nested borders look cluttered; content width shrinks by 4 more columns. | Slice 01 must prototype at realistic `transcriptInnerW` (~60–90 cols) before committing. Consider a borderless tinted-block variant (omp has one: `applyBg` without a frame) for narrow widths. |
| **Line-range accounting drifts** — multi-line cards break `chatItemLineRanges`, desynchronizing `n`/`N` navigation and scroll-to-item. | Cursor jumps to the wrong place; silent and confusing. | Every slice that changes an item's rendered height must extend the line-range tests in `monitor_transcript_items_view_test.go`. |
| **Diff computation dependency** (CC-10) adds a module or hand-rolled Myers. | Supply-chain surface, or a correctness burden. | Slice 07 evaluates three options and records the choice as an ADR. |
| **Render cost per frame.** Cards do more per-line work (padding to width, tint stabilization) inside a 100 ms throttled repaint. | Sluggish scrolling on long transcripts. | Reuse the existing per-item render cache (`chatItemRendered`); key it on the same inputs omp hashes (width, expanded, state, content). Benchmark at `chatWindowMax = 300`. |
| **Glyph regressions in CI capture / non-Unicode terminals.** | Test snapshots full of replacement characters. | Slice 14; and per CC-7, earlier slices must not hardcode glyphs. |
| **Scope creep into the Steps panel.** CC-4's "no glyph change" rule contradicts the Steps panel's `○ → ● → ✓`. | Unbounded change. | Explicitly Transcript-only (NG6). Steps panel indicator semantics are out of scope. |
| **Losing the "resulting source" view** that `writeNewCodeCards` deliberately provides. | Operators who prefer seeing final state lose it. | Slice 07 keeps it as the fallback when `OldText == nil` (file creation), which is the case the current comment was really serving. |

---

## Research Carried Forward

Source: `github.com/can1357/oh-my-pi`, shallow clone reviewed 2026-09-11.
Paths below are relative to `packages/coding-agent/src/` unless prefixed.

### Presentation primitives
- `tui/output-block.ts:66-200` — `renderOutputBlock`, the universal card.
  `outputBlockContentWidth = width - 2 - padL - padR`. Border by state at
  `:73-81`; background stabilization at `:86-93`; label composition at `:155-174`.
- `tui/status-line.ts:32-54` — `renderStatusLine`, the universal header row.
- `tui/code-cell.ts:114-268` — `renderCodeCell` / `renderMarkdownCell`.
- `tui/tree-list.ts:33-90`, `tui/utils.ts:80-90` — tree connectors, 3-column.
- `modes/components/dynamic-border.ts` — full-width rule, cached by width.
- `modes/components/overlay-box.ts` — `fit()`, the ANSI-aware pad-or-truncate.

### Content renderers
- `modes/components/diff.ts:16-267` — indent visualization, intra-line word diff,
  batch-highlighted context, duplicate-gutter suppression.
- `tools/render-utils.ts:395-405` — `formatCodeFrameLine`, the fused gutter.
- `edit/renderer.ts:776-784` — `formatDiffStatsSuffix` → `⟦+12/-3⟧`.
- `modes/components/read-tool-group.ts:330-889` — read grouping.
- `tools/default-renderer.ts:37-141` — the generic fallback card.
- `tools/json-tree.ts:53-92` — `formatArgsInline` fair-share budget.
- `crates/pi-edit/src/diff_string.rs:69-71, 180-237` — wire format and
  enclosing-block context injection.

### Vocabulary and theme
- `modes/theme/symbols.ts` — ~290 symbol keys × 3 presets; status icons
  `:369-379`, tree `:387-391`, box `:399-419`, separators `:421-435`, spinners
  `:1379-1392`, per-tool glyphs `:621-645`.
- `modes/theme/theme-class.ts:145-165` — light/dark by measured status-bar
  luminance, not filename.
- `tools/render-utils.ts:88-108` — `PREVIEW_LIMITS`; `:276-298` — expand hint and
  `formatMoreItems`; `:307-317` — `previewWindowRows()`.
- `docs/theme.md` (omp) — the 60-token color contract, including the three
  `tool*Bg` state tints.

### Structure
- `modes/components/transcript-container.ts:128-134` — `trimBlankEdges` with the
  raw-ANSI blank test. `:532-545` — the one-blank-line join.
- `modes/components/transcript-outline.ts:146-214` — the dotted selection outline.
- `modes/components/usage-row.ts:35-97` — the per-turn metadata row.
- `modes/components/compaction-summary-message.ts:57-76` — centered banners.
- `modes/components/chat-transcript-builder.ts:267-494` — message→component
  dispatch, grouping, deferred usage, displacement.

### Tensions and caveats recorded honestly

- **No golden files exist in the omp repo.** The fixtures under
  `cli/gallery-fixtures/` are renderer *inputs*, not expected output. Verbatim
  examples in the slice documents were reconstructed from the render code paths.
  `omp gallery --plain --width 80` produces byte-exact goldens but requires
  `bun`, which was not available during this research. **Any slice claiming
  byte-level parity should generate real goldens first.**
- **omp is internally inconsistent** in two places worth not copying blindly:
  `default-renderer.ts:134` hardcodes `more lines` instead of pluralizing, and at
  least five different truncation phrasings coexist in the codebase
  (`… N more lines`, `… +N more`, `… (N earlier lines)`, `… N more`, bare `…`).
  Slice 06 should pick **one**, not reproduce the spread.
- **omp's light/dark machinery is irrelevant** to jig (dark-only theme), so its
  60-token palette should be mined for *semantics*, not copied wholesale.
- **omp's 1-column global gutter with a `tight` toggle** is a full-terminal
  concern. jig's panel already owns its inset; do not add a second one.

---

## Deferred Work

| Item | Why deferred | Trigger to reconsider |
|---|---|---|
| Mermaid diagrams rendered as ASCII in transcript markdown (`tui-adapters.ts:181-221`). | Large dependency, narrow payoff for workflow transcripts. | If workflows begin emitting architecture diagrams as agent output. |
| OSC 8 hyperlinks on file paths (`tui/hyperlink.ts`). | Terminal-capability detection and a settings surface jig does not have. | When jig gains a general terminal-capability probe. |
| Color-swatch rendering for hex literals (`markdown.ts:1593-1652`). | Delightful, unrelated to workflow orchestration. | Never, unless requested. |
| Enclosing-block context injection in diffs (tree-sitter, `diff_string.rs:180-237`). | Requires a parser per language. | If operators report that diff hunks lack enough context to judge an edit. |
| Todo-style strikethrough reveal animation (`tools/todo.ts:989-1013`). | jig has no todo tool. | If a checklist-shaped step type is added. |
| Nerd Font glyph preset. | Slice 14 delivers `unicode` + `ascii`; nerd adds a third table with no current demand. | If an operator asks, or if a Nerd Font is made a documented prerequisite. |
| Per-tool bespoke renderers beyond edit/read/grep. | omp has 30; jig's tool surface is smaller and backend-dependent. | Per-tool, when a specific tool's output proves unreadable in the generic card. |

---

## Epic Completion Criteria

The epic is complete when all of the following hold against a real multi-step run
with at least one failed step, one edit, and three consecutive reads:

1. **EC-1.** Every tool exchange renders as a bounded card; no transcript item
   uses the `│ `-prefixed detail style.
2. **EC-2.** A screenshot at 80 columns lets a reader identify the failed step
   and the running step without reading any text.
3. **EC-3.** Holding `n` through the transcript produces no horizontal movement
   of any content.
4. **EC-4.** An `edit` exchange whose `Activity.Content[].Diff.OldText` is
   non-nil renders added/removed lines with a line-number gutter; the
   file-creation case (`OldText == nil`) still renders the resulting source.
5. **EC-5.** Three consecutive `read` calls render as one item naming all three
   paths.
6. **EC-6.** Every truncation affordance in the panel matches the single
   vocabulary from slice 06, verified by a grep-based test.
7. **EC-7.** `go test ./...` passes, `gofmt -l -w .` is clean, and `go vet ./...`
   is clean.
8. **EC-8.** The panel renders legibly with the ASCII glyph preset active.
9. **EC-9.** `internal/tui/monitor/monitor_transcript.go` contains no unreachable
   render path (slice 00 stays landed).
10. **EC-10.** Scrolling a 300-entry transcript remains responsive within the
    existing 100 ms throttled frame budget.
