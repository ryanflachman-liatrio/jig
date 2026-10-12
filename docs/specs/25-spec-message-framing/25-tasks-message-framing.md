# 25-tasks-message-framing.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Prefer the correct design pre-v1 and remove the retired mechanism in the same change; use `internal/tui/shared` for shared presentation; preserve persistence-off; do not add compatibility wrappers speculatively. | none |
| `CLAUDE.md` | yes | Follow the shared instructions in `AGENTS.md`; use the referenced docs (Architecture, Conventions, TUI, Testing) as source of truth. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; child TUI packages must not import the root TUI package; finalized transcript content remains file-backed. | none |
| `docs/CONVENTIONS.md` | yes | Keep APIs narrow, distinguish nil pointer padding from explicit zero, bound caches, measure before adding complexity. | none |
| `docs/TUI.md` | yes | Semantic styles live in `Styles`/`DefaultTheme`; measure with `lipgloss.Width`; ANSI-aware wrapping only; cache identity must include every rendering input. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic fixtures; ANSI-aware width assertions; supplement model tests with visual evidence; targeted TUI race check for TUI changes. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | Manual titled-border composition is a card/panel concern; the bubble deliberately omits a border and does not participate. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | Card SGR stabilization (`sgrResetsBackground`, `sgrSequence.ReplaceAllStringFunc`) is the reference for the bubble tint; factor, do not copy. | none |
| `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md` | yes | `trimStructuralBlankEdges` raw-bytes trim is the exact discipline slice 09 depends on; tinted padding rows survive it because `\x1b` is non-whitespace. | none |
| `go.mod` | yes | Go 1.25.12 and pinned `charm.land/*/v2` (Lip Gloss, Bubbles, Glamour) plus `github.com/charmbracelet/x/ansi` are already available; slice 09 adds no dependency. | none |
| `mise.toml` | yes | Selects the Go 1.25 toolchain series. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy is present. | none |
| `.github/pull_request_template.md` | not found | No pull-request template is present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves every material choice: bubble is borderless, tint reuses
`hexBBQ` via `bgLeast`, collapse discipline is a byte threshold equal to
`chatExpandMax`, summary label falls back from first ATX heading to
`"Message"`, and the retired `Chat.UserGuidance` field is deleted in the same
change per the pre-v1 policy.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/bubble.go` *(new)* | New owner of `MessageBubble`, `RenderMessageBubble`, and `MessageBubbleContentWidth`. Reuses the SGR stabilization pass factored out of `card.go`. |
| `internal/tui/shared/bubble_test.go` *(new)* | Table-driven geometry, content-width, tint stabilization, and content-shape tests at widths 40/60/90. |
| `internal/tui/shared/card.go` | Factors the private SGR stabilization pass (`sgrSequence.ReplaceAllStringFunc` and `sgrResetsBackground`) so `bubble.go` can call the same helpers. Card behavior is byte-for-byte unchanged. |
| `internal/tui/shared/styles.go` | Adds `Chat.UserBubble` and initializes it from `bgLeast` in `DefaultTheme`. Removes `Chat.UserGuidance` and its initializer in the same change. |
| `internal/tui/shared/styles_test.go` | Existing style assertions; extend to prove `Chat.UserBubble` is initialized from `bgLeast` and that `Chat.UserGuidance` is gone. |
| `internal/tui/monitor/monitor_model.go` | Adds `chatUserBubbleCollapseBytes` (`= 4096`) next to the existing chat budgets and adds `transcriptRenderBubble` to the `transcriptRenderSurface` enum. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Splits the `transcriptItemText` arm into assistant and user branches; adds `renderUserMessageBubble` and `collapseUserTextItem` helpers; updates `itemHasDetail` to include collapsed user text items. |
| `internal/tui/monitor/monitor_message_framing_test.go` *(new)* | Bubble rendering, alignment, collapse/summary label, expand-invokes-glamour, and tinted-padding-survives-edge-trim tests. |
| `internal/tui/monitor/monitor_message_framing_cache_test.go` *(new)* | Cache lifecycle, line-range, expand-all, persistence-off, and default-expand-unaffected tests. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing render tests; update any fixture that expected the literal `"User"` label to the new bubble form; add regressions proving assistant rendering did not change. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Existing card cache / line-range tests; extend the render seam if adding a `textRenderer` interface for FR-09.12's counting proof. |
| `internal/tui/monitor/clipboard.go` | Not modified. Slice 09 confirms clipboard already consumes the rendered slice; a regression test asserts an expanded user bubble copies as its glamour body and a collapsed one copies as its summary row. |
| `docs/TUI.md` | Documents the message-framing contract (borderless tinted user bubble with lazy markdown) if current guidance would otherwise be incomplete. Update only sentences that become inaccurate. |
| `docs/specs/25-spec-message-framing/25-proofs/` *(new)* | Stores sanitized proof summaries, deterministic captures, and command outputs. |

### Notes

- Execute the parent tasks in numeric order. Task 2 consumes the primitive
  from Task 1; Task 3 consumes the collapse discipline from Task 2; Task 4
  validates the fully integrated behavior.
- Reuse `chatItemRendered`, `chatItemLineRanges`, `chatItemExpand`, and
  `chatItemExpandAll`. Do not add a parallel state model.
- Tests and proof generators must use fabricated content. Never copy a
  real `.jig/` transcript, prompt, credential, or private local path into
  repository artifacts.
- Format only changed Go files with `gofmt -w`. Follow the changed-file
  workflow already used by slices 04, 07, and 08.
- The Monitor test seam that allows swapping the glamour renderer for a
  counting fake may need to be introduced by Task 2. If the seam already
  exists (Task 2 sub-task 2.7 checks), reuse it verbatim rather than
  duplicating.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-09.1 | 1.1, 1.6 | `bubble_test.go` field-and-defaults tables. |
| FR-09.2 | 1.2, 1.6 | `TestRenderMessageBubbleGeometry` totality cases. |
| FR-09.3 | 1.2, 1.6 | `TestRenderMessageBubbleContentWidth` at widths 0..90. |
| FR-09.4 | 1.3, 1.4, 1.6 | `TestRenderMessageBubbleTintStabilization` per-cell SGR walk. |
| FR-09.5 | 1.1, 1.6 | `bubble_test.go` regression: no `Card*` styles referenced. |
| FR-09.6 | 1.5, 1.6 | `TestRenderMessageBubbleContent` embedded-newline and hard-wrap cases. |
| FR-09.7 | 2.4, 2.9 | `TestUserBubbleReplacesLiteralUserLabel`. |
| FR-09.8 | 2.4, 2.9 | Existing assistant-render regression + `TestUserBubbleAssistantAlignment`. |
| FR-09.9 | 2.4, 2.5, 2.9 | `TestUserBubbleReplacesLiteralUserLabel` plus zero-width defensive case. |
| FR-09.10 | 2.1, 2.6, 2.9 | Named-constant assertion + `TestUserBubbleCollapsedSummary`. |
| FR-09.11 | 2.6, 2.9 | `TestUserBubbleSummaryLabelFromHeading`. |
| FR-09.12 | 2.7, 2.9 | Counting-fake renderer + `TestUserBubbleCollapsedSummary`. |
| FR-09.13 | 2.4, 2.8, 2.9 | `itemHasDetail` predicate test and interaction test. |
| FR-09.14 | 2.6, 2.7, 2.9 | `TestUserBubbleExpandRendersMarkdown`. |
| FR-09.15 | 2.1, 2.6 | Compile-time constant reference test. |
| FR-09.16 | 3.1, 3.5 | `TestUserBubbleCacheLifecycle`. |
| FR-09.17 | 3.2, 3.5 | Same, with width and page-replacement rows. |
| FR-09.18 | 3.3, 3.5 | `TestUserBubbleLineRangesMatchRenderedRows`. |
| FR-09.19 | 3.4, 3.5 | Page-replacement regression. |
| FR-09.20 | 3.6, 3.5 | `TestUserBubblePersistenceOff`. |
| FR-09.21 | 3.7, 3.5 | `TestUserBubbleExpandAllRespectsPerItem`. |
| FR-09.22 | 3.7, 3.5 | `TestUserBubbleDefaultExpandUnaffected`. |

## Tasks

### [ ] 1.0 Introduce the shared tinted message-bubble primitive

Add a small pure primitive to `internal/tui/shared` that renders a
borderless, background-tinted block whose top and bottom padding rows are
full-width tinted whitespace and whose content rows wrap through
`ansi.Hardwrap` with SGR stabilization matching `card.go`'s existing pass.
The primitive knows nothing about transcript items, roles, or Monitor
state. This parent task covers FR-09.1 through FR-09.6.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared -run 'TestRenderMessageBubble' -count=1 -v` passes, exercising geometry, content-width, per-cell SGR stabilization, embedded newlines, hard-wrap, indentation, and empty-content-with-padding cases at widths 0, 40, 60, and 90 for FR-09.1 through FR-09.6.
- Proof document: `docs/specs/25-spec-message-framing/25-proofs/25-task-01-proofs.md` records the focused command outcome, explains synthetic fixtures, and maps each proof to FR-09.1 through FR-09.6.

#### 1.0 Tasks

- [ ] 1.1 Add `internal/tui/shared/bubble.go` declaring `MessageBubble` with
  the six exported fields (`Content string`, `Width int`, `PadLeft *int`,
  `PadRight *int`, `PadTop *int`, `PadBottom *int`, `Style lipgloss.Style`)
  and a doc comment describing nil-vs-zero semantics, borderless intent,
  and the caller-owned style contract (FR-09.1, FR-09.5).
- [ ] 1.2 Factor the private padding resolver in `card.go` (`cardPadding`)
  and `CardContentWidth` into two internal helpers `resolveHorizontalPadding`
  and `contentWidthAt` that both `Card` and `MessageBubble` call. Add
  `MessageBubbleContentWidth(width int, left, right *int) int` and confirm
  `CardContentWidth` output is byte-for-byte identical to today across the
  existing card test table (FR-09.3).
- [ ] 1.3 Factor `card.go`'s SGR stabilization logic
  (`sgrSequence.ReplaceAllStringFunc` and `sgrResetsBackground`) into two
  reusable helpers in a new unexported file section (or a `internal/tui/shared/sgr.go`
  file) callable by `card.go`'s `finishRow` and `bubble.go`'s row
  stabilization. Confirm no card test regresses (FR-09.4).
- [ ] 1.4 Implement `RenderMessageBubble(b MessageBubble) string`: resolve
  padding (defaults `PadTop = PadBottom = 1`), compute content width via
  `MessageBubbleContentWidth`, split `Content` on `\n`, right-trim each
  logical line, `ansi.Hardwrap` at content width, and emit
  `PadTop` full-width tinted whitespace rows + one row per wrapped line
  (`PadLeft` cells of whitespace + wrapped content padded to content width
  + `PadRight` cells of whitespace) + `PadBottom` full-width tinted
  whitespace rows. Every row shall pass through the shared stabilization
  pass with the caller's `Style` supplying the background (FR-09.2,
  FR-09.4, FR-09.6).
- [ ] 1.5 Handle degenerate inputs deterministically: `Width <= 0` returns
  `""`; `Width < 3` returns `Width` cells of tinted whitespace (matching
  `RenderCard`'s current defensive behavior); empty `Content` with any
  padding value greater than zero still emits the padding rows; oversized
  or negative padding values clamp so at least one content cell remains
  (FR-09.2, FR-09.6).
- [ ] 1.6 Add `internal/tui/shared/bubble_test.go` with
  `TestRenderMessageBubbleGeometry`, `TestRenderMessageBubbleContentWidth`,
  `TestRenderMessageBubbleTintStabilization`, and
  `TestRenderMessageBubbleContent`. Assert every emitted row with
  `lipgloss.Width` at widths 40/60/90, walk visible cells with an
  SGR-state helper to prove tint coverage, and cover embedded newlines,
  hard-wrapped unbroken words, leading indentation, internal blank lines,
  and empty content with padding.
- [ ] 1.7 Run the focused shared suite `go test ./internal/tui/shared -run
  'TestRenderMessageBubble' -count=1` and record the sanitized output and
  FR mapping in `25-proofs/25-task-01-proofs.md`.

### [ ] 2.0 Replace the "User" label with a bubble render and lazy summary path

Route `transcriptItemText` items with `role == transcript.RoleUser` through
the new bubble primitive, adopt the byte-threshold collapse discipline for
outsized bodies, and prove that a collapsed body does not invoke the glamour
renderer. Assistant prose is unchanged. This parent task covers FR-09.7
through FR-09.15.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestUserBubble(ReplacesLiteralUserLabel|AssistantAlignment|CollapsedSummary|ExpandRendersMarkdown|SummaryLabelFromHeading|TintedPaddingSurvivesEdgeTrim)' -count=1 -v` passes, demonstrating the retired `"User"` label, aligned assistant prose, size threshold, summary format, lazy render, and slice-04 padding survival for FR-09.7 through FR-09.15.
- Terminal capture: `docs/specs/25-spec-message-framing/25-proofs/25-task-02-bubble-gallery.txt` records synthetic short-user, long-user, and adjacent-assistant renders at widths 60 and 90 with per-row `lipgloss.Width` measurements.
- Screenshot: `docs/specs/25-spec-message-framing/25-proofs/25-task-02-bubble-gallery.png` shows the same gallery embedded in an 80-column Monitor projection so the tint and alignment are visually verifiable.
- Proof document: `docs/specs/25-spec-message-framing/25-proofs/25-task-02-proofs.md` records the focused test result, embeds the screenshot, and maps evidence to FR-09.7 through FR-09.15.

#### 2.0 Tasks

- [ ] 2.1 In `monitor_model.go`, declare
  `chatUserBubbleCollapseBytes = 4096` in the same `const` block as
  `chatExpandMax`, `chatWindowMax`, `chatBoundaryContextMax`, and
  `outputMaxLines`, with a comment naming its purpose (deferred first
  paint for outsized user text) and its intentional equality with
  `chatExpandMax` (FR-09.10, FR-09.15).
- [ ] 2.2 In `monitor_model.go`, add `transcriptRenderBubble` to the
  `transcriptRenderSurface` enum immediately after
  `transcriptRenderReadGroup`. Do not renumber existing values (FR-09.16).
- [ ] 2.3 In `internal/tui/shared/styles.go`, add
  `Chat.UserBubble lipgloss.Style` to the `Chat` sub-struct and initialize
  it in `DefaultTheme` as
  `lipgloss.NewStyle().Background(bgLeast)`. Remove
  `Chat.UserGuidance` from the struct and its `DefaultTheme` initializer
  in the same change. Grep-verify no other caller of `UserGuidance` remains
  under `internal/`.
- [ ] 2.4 In `monitor_transcript_items_view.go`, split the
  `transcriptItemText` arm into an assistant branch (unchanged) and a
  user branch that calls a new
  `renderUserMessageBubble(item transcriptItem, prefix string, selected,
  expanded bool) string` helper. Retain the existing selection-prefix
  landing behavior for the assistant branch (`strings.TrimLeft(rendered,
  "\n")`) exactly as written today (FR-09.7, FR-09.8).
- [ ] 2.5 Implement `renderUserMessageBubble`: compute
  `available := m.transcriptInnerW - lipgloss.Width(prefix)`; when
  `available < 3` return `""` so the scratch buffer receives no bytes and
  slice 04's zero-height guard drops the item; otherwise assemble the
  bubble input `content` and call
  `shared.RenderMessageBubble(shared.MessageBubble{Content: content, Width:
  available, Style: shared.Theme.Chat.UserBubble})` and prefix every row
  with `prefix` on newline (mirror `prefixCardRows`) (FR-09.9).
- [ ] 2.6 Compute `content` from the block text through a pure helper
  `userBubbleContent(block transcript.Block, expanded bool, contentWidth
  int, renderMarkdown func(blockKey, string) string,
  keyOfBlock blockKey) (string, bool)` that returns the bubble body and
  a `collapsed` flag. When `len(block.Text) <= chatUserBubbleCollapseBytes
  || expanded`, it invokes `renderMarkdown(keyOfBlock, block.Text)` and
  returns `false`; otherwise it composes and returns
  `<label> · <size> · <n> <line|lines>` and returns `true`. Label
  detection scans logical lines for a leading ATX heading (up to six `#`
  followed by at least one space); size formatting uses `<n> B` below
  1024 and `<n.n> KiB` otherwise; line count is
  `strings.Count(block.Text, "\n") + 1`. The composed row is truncated
  through `shared.TruncateTitle(row, contentWidth)` before being handed
  to the bubble (FR-09.10, FR-09.11, FR-09.14).
- [ ] 2.7 Route the `renderMarkdown` seam through a small unexported
  interface `textRenderer interface{ Render(string) (string, error) }`
  in `monitor_model.go` (with a compile-time
  `var _ textRenderer = (*glamour.TermRenderer)(nil)` line), so
  `TestUserBubbleCollapsedSummary` can install a counting fake. Keep
  production wiring unchanged (`m.renderer` still assigned from the
  glamour construction path). If a suitable seam already exists,
  document that fact in the proof doc and skip the interface addition
  (FR-09.12).
- [ ] 2.8 Update `itemHasDetail(item transcriptItem) bool` so it returns
  `true` when `item.kind == transcriptItemText` and either
  `item.role == transcript.RoleUser && len(<primary block text>) >
  chatUserBubbleCollapseBytes` — mirroring `renderUserMessageBubble`'s
  collapse decision. Non-user text items and short user text items remain
  non-toggleable (FR-09.13).
- [ ] 2.9 Add `internal/tui/monitor/monitor_message_framing_test.go`
  with the six tests listed in the Requirement-to-Test Traceability
  table. Use a counting-fake `textRenderer` to prove FR-09.12; use slice
  04's `isStructuralBlank` predicate to prove FR-09.9 padding survival;
  cover heading-detection cases including no heading, an ATX heading
  after leading whitespace (which does not count), and an oversized
  heading truncated with `…`.
- [ ] 2.10 Emit the synthetic bubble gallery. Run
  `go test ./internal/tui/monitor -run
  'TestUserBubble(ReplacesLiteralUserLabel|AssistantAlignment|CollapsedSummary|ExpandRendersMarkdown|SummaryLabelFromHeading|TintedPaddingSurvivesEdgeTrim)'
  -count=1 -v` and
  `JIG_UI_SNAPSHOT_DIR=docs/specs/25-spec-message-framing/25-proofs go
  test ./internal/tui/monitor -run TestUserBubbleGallery -count=1`
  (adding the gallery test alongside `TestReadGroupGallery` if the
  existing helper is reusable), then write `25-task-02-proofs.md`
  embedding the PNG and mapping evidence to FR-09.7 through FR-09.15.

### [ ] 3.0 Cache identity, line ranges, and lifecycle integrity

Wire the bubble render into the existing item-render cache and
line-range accounting so navigation, filter, page replacement, and
same-step resize behave identically to a tool exchange card. Ensure
`chatItemExpandAll`, `prunePageState`, `reloadTranscript`, and
persistence-off remain untouched by the new surface. This parent task
covers FR-09.16 through FR-09.22.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run
  'TestUserBubble(CacheLifecycle|LineRangesMatchRenderedRows|ExpandAllRespectsPerItem|PersistenceOff|DefaultExpandUnaffected)' -count=1 -v` passes, demonstrating cache identity, exact line-range accounting, expand-all semantics, empty-state preservation, and default-expand non-interference for FR-09.16 through FR-09.22.
- Race test: `go test -race ./internal/tui/... -count=1` passes with the
  new surface active, showing that the added map interactions introduce
  no race detected by the exercised suite.
- Proof document: `docs/specs/25-spec-message-framing/25-proofs/25-task-03-proofs.md` records the exact command outcomes and FR mapping.

#### 3.0 Tasks

- [ ] 3.1 In `renderUserMessageBubble`, key the rendered bytes under
  `transcriptRenderKey{itemKey: item.key, surface:
  transcriptRenderBubble, width: available, expanded: expanded,
  selected: selected, state: 0, header: ""}`. On a miss, iterate
  `chatItemRendered` once and delete any entry with matching `itemKey`
  and `surface == transcriptRenderBubble` before writing the new value,
  so per-item cache stays bounded by the loaded page (FR-09.16).
- [ ] 3.2 Verify `setChatPage` already clears
  `chatItemRendered` wholesale (`monitor_transcript.go:164`) and
  therefore invalidates the new surface for free; verify
  `rebuildRenderer`'s width-driven cache reset also removes bubble
  entries; add a targeted comment in `renderUserMessageBubble` naming
  the two invalidation seams so a future reader is not tempted to
  invalidate manually (FR-09.17).
- [ ] 3.3 Confirm `itemTranscriptBody`'s existing
  `chatItemLineRanges[transcriptLineKey{itemKey: item.key}] =
  lineRange{start: start, end: line - 1}` accounting works unchanged
  for a bubble item because bubble rows are contained inside the
  per-item scratch buffer (slice 04) and end with an explicit `"\n"`.
  Add regression coverage for both the collapsed and expanded row
  counts (FR-09.18).
- [ ] 3.4 Confirm `prunePageState` already handles the new surface
  because it iterates `chatItemRendered` keys and filters by
  `itemKey` presence in `loadedItems`, then add a fixture-based test
  that plants a bubble entry keyed on a phantom `blockKey`, replaces
  the page, and asserts the entry is gone (FR-09.19).
- [ ] 3.5 Add `internal/tui/monitor/monitor_message_framing_cache_test.go`
  containing `TestUserBubbleCacheLifecycle`,
  `TestUserBubbleLineRangesMatchRenderedRows`,
  `TestUserBubbleExpandAllRespectsPerItem`,
  `TestUserBubblePersistenceOff`, and
  `TestUserBubbleDefaultExpandUnaffected`, using synthetic transcript
  entries and `newMonitorWithSteps` / `setChatPage` as elsewhere in the
  Monitor test suite (FR-09.16, FR-09.18, FR-09.19, FR-09.20,
  FR-09.21, FR-09.22).
- [ ] 3.6 In `TestUserBubblePersistenceOff`, set `RunDir = ""`, call
  `reloadTranscript`, then `chatBody`, and assert the empty-state
  message renders and that no `transcriptRenderBubble` entry is ever
  written to `chatItemRendered`. Confirm `RenderMessageBubble` performs
  no I/O (FR-09.20).
- [ ] 3.7 In `TestUserBubbleExpandAllRespectsPerItem` and
  `TestUserBubbleDefaultExpandUnaffected`, seat one user bubble and one
  structured-edit tool exchange, toggle expand-all twice, and assert
  that the per-item expand map is untouched (slice 08's semantics) and
  that `defaultExpandEditCodeItems` did not auto-expand the user bubble
  (FR-09.21, FR-09.22).
- [ ] 3.8 Run the focused suite and
  `go test -race ./internal/tui/... -count=1`; record command outputs
  in `25-task-03-proofs.md` with the FR mapping.

### [ ] 4.0 Retire the "User" label, prove regression safety, and record acceptance evidence

Finish the change by updating every fixture that expected the retired
`"User"` label, running the full repository check set, and recording
sanitized acceptance evidence. Confirm the final diff contains only
intended changes and matches every non-goal listed in the spec.

#### 4.0 Proof Artifact(s)

- Regression commands: `go build ./cmd/jig`, `go test ./...`,
  `go vet ./...`, `go test -race ./internal/tui/... -count=1`, the
  explicit changed-file `gofmt -l` command in sub-task 4.4, and
  `git diff --check` complete successfully; their outputs are recorded
  under `docs/specs/25-spec-message-framing/25-proofs/25-task-04-acceptance/`.
- Grep-lock evidence: an output of
  `rg --hidden --follow -n 'UserGuidance|"User"' internal/`
  captured under
  `25-proofs/25-task-04-acceptance/user-label-scan.txt` shows the
  retired symbol and literal are gone from every non-test call site
  (test files may reference `"User"` only to assert its absence, and
  such lines are enumerated in the proof document).
- Terminal smoke: `docs/specs/25-spec-message-framing/25-proofs/25-task-04-monitor-smoke.md`
  records terminal dimensions and the exact keyboard interactions
  observed against a synthetic persisted transcript containing one
  short user bubble, one long collapsed user bubble, and one
  assistant reply — expanding the collapsed bubble, resizing narrow
  and wide, and confirming the tint survives.
- Proof document: `docs/specs/25-spec-message-framing/25-proofs/25-task-04-proofs.md` records requirement coverage, links every acceptance artifact, and confirms the final diff does not leak into non-goals such as system-message tinting, reaction badges, OSC 133, thinking bubbles, per-step metadata rows, or wire-format changes.

#### 4.0 Tasks

- [ ] 4.1 Update every Monitor test fixture that expected the literal
  string `"User"` on a transcript row. Rewrite each `wantBody` to the
  new tinted-bubble form (a top padding row, one or more content rows,
  a bottom padding row). Do not weaken any assertion; if an existing
  test's expected `lineRange` changes, add a comment explaining the
  new geometry and re-verify against
  `TestUserBubbleLineRangesMatchRenderedRows`.
- [ ] 4.2 Review `docs/TUI.md` against the shipped behavior. If the
  file currently claims the transcript labels operator input as
  `User`, update the sentence to describe the tinted bubble with the
  lazy summary discipline. Do not restate implementation details or
  alter unrelated slice guidance.
- [ ] 4.3 Confirm no clipboard or search regression: the existing
  `clipboard.go` capture path already consumes the rendered item
  slice, and no new field is needed. Add a targeted regression that a
  collapsed bubble copies the summary row bytes (not the expanded
  body) and an expanded bubble copies the glamour output.
- [ ] 4.4 Run the full check set: `go test ./internal/tui/...` focused
  suites first, then `go test -race ./internal/tui/... -count=1`,
  `go build ./cmd/jig`, `go test ./...`, and `go vet ./...`. Run
  `gofmt -l` over the explicit paths
  `internal/tui/shared/{bubble.go,bubble_test.go,card.go,styles.go,styles_test.go}`
  and `internal/tui/monitor/{monitor_model.go,monitor_transcript_items_view.go,monitor_message_framing_test.go,monitor_message_framing_cache_test.go}`.
  Record actual outcomes without describing blocked checks as passing.
- [ ] 4.5 Record command outputs under
  `25-proofs/25-task-04-acceptance/{build,test,vet,race-tui,gofmt,git-diff-check,user-label-scan}.txt`.
  Run `git diff --check` and a credential scan over `25-proofs/` that
  records patterns/categories checked and the zero-match outcome, never
  a matched secret. Write `25-task-04-proofs.md` with a complete
  FR-09.1–FR-09.22 evidence map plus an explicit final-diff check
  against every non-goal.
- [ ] 4.6 Perform the synthetic terminal smoke at recorded dimensions:
  render a page containing (i) a short user bubble, (ii) an assistant
  reply, (iii) a >4 KiB user bubble that starts as a summary row, and
  (iv) an assistant reply. Toggle the long bubble's expansion, resize
  narrow (`transcriptInnerW ≤ 60`) and wide (`transcriptInnerW ≥ 100`),
  and confirm the tint is contiguous, the label is absent, the summary
  format is `<label> · <size> · <n> lines`, and expansion produces
  glamour prose. Save sanitized observations in
  `25-proofs/25-task-04-monitor-smoke.md`.
