# 25-tasks-message-framing.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/card.go` | Owns `finishRow`/`sgrResetsBackground`, the CC-9 tint-over-glamour logic that must be extracted into a shared helper rather than duplicated for the bubble. |
| `internal/tui/shared/card_test.go` | Existing tint/reset test coverage; must keep passing unchanged after the extraction proves no behavior drift. |
| `internal/tui/shared/tint.go` (new) | New home for the extracted `TintRow` helper (`sgrSequence`, `sgrResetsBackground`, background-reapply logic), consumed by both `card.go` and the bubble renderer. |
| `internal/tui/shared/tint_test.go` (new) | Unit tests for `TintRow` in isolation: reset params (`0`, empty, `49`) vs. literal-zero color components. |
| `internal/tui/shared/styles.go` | Adds `Theme.Chat.UserBubble` background style (reusing `hexBBQ`/`bgLeast`); removes the now-dead `Theme.Chat.UserGuidance` field and initializer. |
| `internal/tui/monitor/monitor_transcript_view.go` | Houses the dead `writeUserGuidance` function, the last consumer of `UserGuidance`; removed (file deleted) once the label is gone. |
| `internal/tui/monitor/monitor_model.go` | Adds the `chatTextCollapseBytes = 4096` constant beside `chatExpandMax`/`chatWindowMax`; adds the `oversized bool` field to `transcriptItem`. |
| `internal/tui/monitor/monitor_transcript_items.go` | `buildTranscriptItems`/`itemKindForBlock` compute and thread the oversized flag for user-role text blocks at construction time, per the Technical Considerations note (no re-reading blocks in the view). |
| `internal/tui/monitor/monitor_transcript_items_test.go` | Tests for the oversized-flag threading in item construction (boundary at the 4096-byte threshold, role scoping). |
| `internal/tui/monitor/monitor_transcript_items_view.go` | `writeTranscriptItem`'s `transcriptItemText` arm: builds the tinted bubble and padding rows, dispatches to the summary row or full markdown, and updates `itemHasDetail`. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing item-rendering test file; extended/adjusted for the new bubble output and the removal of the `User` label assertions. |
| `internal/tui/monitor/monitor_transcript_bubble_test.go` (new) | Unit 1 proof tests: no literal `User`, equal offsets, glamour-reset survival, edge-trim survival, selection prefix on every bubble row. |
| `internal/tui/monitor/monitor_transcript_collapse.go` (new) | Summary-row construction: heading-derived label, human-readable byte size, line count, ANSI-aware truncation — kept out of the view file since it is pure text formatting with its own test surface. |
| `internal/tui/monitor/monitor_transcript_collapse_test.go` (new) | Unit 2 proof tests: threshold trigger with a counting fake renderer, label derivation, narrow-width truncation, non-user-role/under-threshold exemptions, `itemHasDetail` boundary. |
| `internal/tui/monitor/monitor_transcript.go` | `renderMarkdown`/`chatRendered` cache boundary; confirms the collapsed path never calls this function and expansion populates it exactly once. |
| `internal/tui/monitor/monitor_transcript_expand_test.go` (new) | Expansion/cache-discipline proof tests: toggle renders full body once, `chatRendered` only ever holds the true render, re-collapsing restores the summary row. |
| `internal/tui/monitor/monitor_search.go` | Read-only reference: confirms collapsed items remain selectable from a search hit without requiring expansion (no code change expected; verified by test). |
| `internal/tui/monitor/monitor_search_test.go` | Adds/extends a case proving a search hit on a collapsed oversized user item selects it without expanding. |
| `docs/specs/25-spec-message-framing/25-proofs/25-task-01-proofs.md` (new) | Proof artifact capture for Task 1.0 (`TintRow` extraction). |
| `docs/specs/25-spec-message-framing/25-proofs/25-task-02-proofs.md` (new) | Proof artifact capture for Task 2.0 (bubble framing), including the screenshot/capture proof. |
| `docs/specs/25-spec-message-framing/25-proofs/25-task-03-proofs.md` (new) | Proof artifact capture for Task 3.0 (collapse trigger and summary row). |
| `docs/specs/25-spec-message-framing/25-proofs/25-task-04-proofs.md` (new) | Proof artifact capture for Task 4.0 (expansion and cache discipline). |

### Notes

- Unit tests live beside the code they test in `internal/tui/shared` and
  `internal/tui/monitor`, following the existing transcript/card test layout.
- Use `go test ./internal/tui/shared/... ./internal/tui/monitor/... -race` for
  focused runs during implementation; run root `go build ./cmd/jig`,
  `go test ./...`, and `go vet ./...` before closing out the spec
  (`docs/TESTING.md`, `AGENTS.md`).
- Format changed files with `gofmt -w <files>`; check with `gofmt -l <files>`
  before committing.
- Only prose goes through glamour — the collapsed summary row and the
  verbatim system/result path stay outside the markdown renderer
  (`CLAUDE.md`/`docs/TUI.md` rule).
- Do not solve the SGR-reset tinting problem twice: Task 1.0's `TintRow`
  helper is a hard prerequisite for Task 2.0 and must land first.

## Tasks

### [x] 1.0 Extract shared row-tinting helper from `card.go` (CC-9 reuse)

#### 1.0 Proof Artifact(s)

- Test: `internal/tui/shared` existing `card_test.go` suite passes unchanged
  after `finishRow`/`sgrResetsBackground` move behind a new exported helper,
  demonstrating no behavior drift from the extraction.
- Test: a new `TintRow` unit test in `internal/tui/shared` proves a
  background-reset SGR sequence (`0`, empty, `49`) inside a row is followed by
  re-applied background color, and an RGB/palette-indexed color component
  containing a literal `0` is left untouched, demonstrating the parameter-aware
  rule survives the extraction intact.
- CLI: `go test ./internal/tui/shared/...` passes, demonstrating the package
  builds and its tests are green post-extraction.

#### 1.0 Tasks

- [x] 1.1 Create `internal/tui/shared/tint.go` and move `sgrSequence`,
      `sgrResetsBackground`, and the reset-reapply loop out of `card.go`'s
      `finishRow` into an exported `TintRow(row, background string) string`
      that applies the same background-wrap-and-reapply behavior to an
      arbitrary row.
- [x] 1.2 Update `Card.finishRow` in `card.go` to call `shared.TintRow` with
      the card's resolved background color (state-dependent for
      `CardError`), preserving its current early return when `c.Tint` is
      false.
- [x] 1.3 Write `internal/tui/shared/tint_test.go` covering: a bare reset
      (`\x1b[0m`), an empty-parameter reset (`\x1b[m`), background-only reset
      (`\x1b[49m`), a non-reset SGR left untouched, and an extended color
      sequence (`\x1b[38;2;0;0;0m`) whose zero components are not treated as
      resets.
- [x] 1.4 Run `go test ./internal/tui/shared/...` and `gofmt -l` on changed
      files; capture the passing output in
      `docs/specs/25-spec-message-framing/25-proofs/25-task-01-proofs.md`.

### [x] 2.0 User bubble framing replaces the `User` label (Unit 1)

#### 2.0 Proof Artifact(s)

- Test: rendering a user-role text item contains no literal `User` substring
  and its content rows and both padding rows (above/below) carry the bubble
  background escape sequence, demonstrating FR-09.1/FR-09.2.
- Test: an assistant-role text item and a user-role text item begin their
  first content rune at the same column offset, and the assistant item has no
  background escape, demonstrating FR-09.3.
- Test: a user bubble whose content includes a fenced code block (glamour
  output containing a background-resetting SGR sequence) has no visible cell
  between the first and last content column lacking the bubble background
  after stabilization, demonstrating FR-09.10 via the `TintRow` helper from
  Task 1.0.
- Test: a rendered user bubble passed through `trimStructuralBlankEdges`
  retains its top and bottom tinted padding rows, demonstrating FR-09.9.
- Test: a selected user bubble places the selection bar prefix on every bubble
  row (content and both padding rows), matching the `prefixCardRows` pattern,
  demonstrating FR-09.13.
- Test: `Theme.Chat.UserBubble`'s rendered background escape resolves to the
  `hexBBQ` RGB value (`#2D2C36`) and no new palette constant is introduced in
  `palette.go`, demonstrating FR-09.11.
- CLI: `go build ./cmd/jig` succeeds after `Theme.Chat.UserGuidance` and its
  now-dead consumer (`writeUserGuidance` in `monitor_transcript_view.go`) are
  removed, demonstrating FR-09.12 leaves no dangling references.
- Screenshot/capture: a checked-in proof text file showing a rendered user
  bubble above untinted assistant prose at the same horizontal offset,
  demonstrating the end state.

#### 2.0 Tasks

- [x] 2.1 In `internal/tui/shared/styles.go`, add `UserBubble lipgloss.Style`
      to the `Chat` struct (with a short "why" comment: background-only tint,
      no foreground override, reused for bubble content and padding rows) and
      initialize it as `lipgloss.NewStyle().Background(bgLeast)` (the existing
      `hexBBQ` token); remove the `UserGuidance` field and its initializer.
- [x] 2.2 Delete `internal/tui/monitor/monitor_transcript_view.go`
      (`writeUserGuidance` has no remaining callers once 2.1 lands).
- [x] 2.3 In `monitor_transcript_items_view.go`, replace the
      `item.role == transcript.RoleUser` branch of the `transcriptItemText`
      case: compute the bubble's content width from `m.transcriptInnerW`
      less the prefix width, right-pad each rendered markdown row to that
      width, and wrap each row through `shared.TintRow` using
      `shared.Theme.Chat.UserBubble`'s resolved background.
- [x] 2.4 Emit one fully tinted blank padding row (via `TintRow` on a
      space-filled row of content width) immediately before and after the
      bubble's content rows.
- [x] 2.5 Extend the selection-prefix composition so the cursor-bar prefix is
      applied to every bubble row (content and both padding rows), following
      `prefixCardRows`; verify the unselected two-space prefix still applies
      per row identically to the assistant branch.
- [x] 2.6 Confirm (add a regression test if not already covered) that the
      assistant branch is untouched: same top-margin-trim behavior, same
      leading offset, no background escape.
- [x] 2.7 Write `monitor_transcript_bubble_test.go` covering the six Proof
      Artifact tests above (no-`User`-label + padding tint, equal offsets,
      fenced-code tint survival, edge-trim survival, selection prefix on all
      rows, and `UserBubble`'s background resolving to `hexBBQ` with no new
      palette constant added).
- [x] 2.8 Capture the screenshot/capture proof and the `go build ./cmd/jig`
      output in
      `docs/specs/25-spec-message-framing/25-proofs/25-task-02-proofs.md`.

### [x] 3.0 Oversized user text collapses into a dim summary row (Unit 2 — trigger and rendering)

#### 3.0 Proof Artifact(s)

- Test: a user-role text block whose raw `block.Text` exceeds
  `chatTextCollapseBytes` (4096 bytes) renders exactly one dim summary row of
  the form `<label> · <size> · <n> line(s)`, and a counting fake markdown
  renderer records zero `Render` calls for that block on first paint,
  demonstrating FR-09.4/FR-09.5/FR-09.14.
- Test: a block beginning with a markdown heading (e.g. `# Session update`)
  summarizes with label `Session update`; a heading-less oversized block
  summarizes with the generic label `User input`, demonstrating FR-09.7.
- Test: at a narrow panel width the summary row is ANSI-aware truncated with a
  trailing ellipsis and never exceeds the available content width,
  demonstrating FR-09.8.
- Test: an oversized assistant-role text block (over 4096 bytes) still renders
  full markdown with no summary row, and oversized system/result/thinking/tool
  items are unaffected by the collapse check, demonstrating FR-09.16.
- Test: `itemHasDetail` reports true for an oversized user-role text item (so
  it shows the collapsed/expanded marker) and false for a user-role text item
  at or under the threshold, demonstrating FR-09.15.

#### 3.0 Tasks

- [x] 3.1 In `monitor_model.go`, add
      `chatTextCollapseBytes = 4096` beside `chatExpandMax`/`chatWindowMax`,
      with a comment explaining the measurement is on raw block bytes because
      rendered size is unknowable without the render this budget exists to
      skip (per the spec's Technical Considerations).
- [x] 3.2 Add an `oversized bool` field to the `transcriptItem` struct in
      `monitor_model.go` with a comment naming its scope (user-role text
      items only).
- [x] 3.3 In `monitor_transcript_items.go`, set `oversized` at construction
      time in the non-tool-use/tool-result branch of `buildTranscriptItems`
      (or `itemKindForBlock`'s call site): true only when
      `kind == transcriptItemText && role == transcript.RoleUser &&
      len(block.Text) > chatTextCollapseBytes`.
- [x] 3.4 Update `itemHasDetail` in `monitor_transcript_items_view.go` to
      return `item.kind != transcriptItemText || item.oversized`.
- [x] 3.5 Create `monitor_transcript_collapse.go` with: a heading-label
      extractor (first line matching a markdown ATX heading, trimmed of `#`
      and whitespace) falling back to `"User input"`; a human-readable byte
      size formatter; a raw-newline line counter; and a
      `buildCollapseSummary(text string, width int) string` that composes
      `<label> · <size> · <n> line(s)` and ANSI-aware truncates it
      (`ansi.Truncate`, matching the existing `monitor_view.go` pattern) to
      `width` with a trailing ellipsis.
- [x] 3.6 Wire `buildCollapseSummary` into the `transcriptItemText` /
      `RoleUser` branch from Task 2.0: when `item.oversized && !expanded`,
      render the summary row (styled with `Theme.Chat.Hint`) as the bubble's
      sole content row instead of calling `renderMarkdown`; when expanded,
      fall through to the Task 2.0 full-markdown bubble path.
- [x] 3.7 Write `monitor_transcript_collapse_test.go` covering the five Proof
      Artifact tests above, using a counting fake renderer (or a call-count
      wrapper around `renderMarkdown`) to assert zero render invocations on
      first paint for an oversized block.

### [ ] 4.0 Expand toggle renders the full bubble; collapse bypasses the markdown cache (Unit 2 — expansion and cache discipline)

#### 4.0 Proof Artifact(s)

- Test: toggling the existing expand control (`m.chatItemExpand[item.key]` or
  expand-all) on a collapsed item renders the full markdown body inside the
  Unit 1 bubble (renderer invoked exactly once), and toggling again collapses
  it back to the summary row, demonstrating FR-09.6.
- Test: after expansion, `chatRendered` (keyed by `blockKey`) holds the real
  markdown render for that block and never held the summary text at any point
  (inspected via the counting fake renderer plus a direct cache-map
  assertion), demonstrating FR-09.5's cache-discipline half and the Technical
  Considerations note that the summary is never written under the `blockKey`
  markdown surface.
- Test: a transcript search hit landing on a collapsed item selects it without
  requiring expansion, demonstrating the Technical Considerations search
  interplay note continues to hold.
- CLI: `go test ./internal/tui/monitor/... -race` and
  `go test ./... && go vet ./...` pass at the repository root, demonstrating
  no regressions in the wider `internal/tui` suite (Success Metric 3).

#### 4.0 Tasks

- [ ] 4.1 Confirm (from Task 3.0's wiring) that the expanded branch calls
      `m.renderMarkdown(item.primary.key, block.Text)` exactly as the
      always-rendered Unit 1 path does, so `chatRendered` population on
      expansion is identical to the non-collapsible case; add a direct
      assertion on the `chatRendered` map contents (not just renderer call
      count) confirming the summary string is never present as a value.
- [ ] 4.2 Add a toggle-round-trip test in `monitor_transcript_expand_test.go`:
      expand via `m.chatItemExpand[item.key] = true` renders full markdown
      once; toggling back to `false` restores the single summary row without
      a second render call.
- [ ] 4.3 Extend `monitor_search_test.go` with a case constructing an
      oversized collapsed user item, producing a search hit against its raw
      block text, and asserting the hit selects the item without requiring
      `chatItemExpand` to be set.
- [ ] 4.4 Run `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, and
      `gofmt -l` on all changed files at the repository root; capture output
      in `docs/specs/25-spec-message-framing/25-proofs/25-task-04-proofs.md`
      confirming Success Metrics 1–3 (grep for zero literal `User` labels
      across the test corpus, 0-then-1 render-call proof, and unchanged
      pre-existing suites apart from the removed `User`-label assertions).
