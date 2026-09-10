# 23-spec-clipboard-yank.md

## Introduction/Overview

Add context-sensitive clipboard actions to jig's Transcript, Review, and Runs surfaces: `y` copies the current selectable unit and `Y` (Shift+Y) copies the entire current content source. Operators can paste messages, output artifacts, diagnostics, review evidence, and run identifiers without leaving the TUI.

Source: `docs/plans/open-goals.md` B1, overlapping T2. The user accepted the interaction table in conversation on 2026-09-10; `23-questions-1-clipboard-yank.md` records that resolution. This specification supersedes T2's collapse-dependent copy suggestion: an item's full recorded content is copied regardless of its expansion state.

## Goals

- Provide the same small-unit/whole-source key convention across the scoped surfaces.
- Copy source content without terminal styling, panel chrome, or viewport clipping.
- Include transcript history outside the loaded page when explicitly copying the whole selected step.
- Keep clipboard work bounded and responsive, with clear empty, unavailable, and oversized outcomes.
- Preserve navigation, text entry, confirmations, review drafts, and workflow execution behavior.

## User Stories

- **As an operator**, I want to copy the message or tool exchange I selected so that I can paste complete evidence into an issue or discussion.
- **As an operator**, I want to copy the entire content source shown in the Transcript panel so that switching from messages to `output.json`, `output.md`, or diagnostics naturally selects what I copy.
- **As a reviewer**, I want to copy a line, selected range, Markdown block, or diff hunk so that I can discuss the exact change without reconstructing it from the terminal display.
- **As an operator**, I want to copy a run ID so that I can reference the correct run in a command or conversation.

## Demoable Units of Work

### Unit 1: Copy run identifiers and whole Monitor content

**Purpose:** Deliver useful clipboard actions with a common terminal delivery and feedback path.

**Functional Requirements:**

- **FR-1:** In the Runs list, `y` shall copy the selected row's complete run ID without a trailing newline. `Y` shall be unavailable. An empty list shall produce no clipboard write.
- **FR-2:** With the Transcript panel focused on a step's messages, `Y` shall copy that step's entire recorded transcript in durable source order, including prior pages, generations, iterations, and attempts. It shall ignore presentation filters, search highlighting, and expansion state. It shall not copy other steps, a family's children implicitly, or artifact files alongside messages.
- **FR-3:** With the Transcript panel showing a selected output file, `Y` shall copy that file's text. This includes Markdown, JSON, JSONL diagnostics, and other supported text artifacts. `y` shall be unavailable in file views. A separate copy target for hidden `result.json` is not required; availability follows the existing file-selection surface.
- **FR-4:** All enabled copy actions shall deliver through Bubble Tea's OSC52 system-clipboard command. The implementation shall neither read the clipboard nor invoke local clipboard helpers. Feedback shall say that the copy was requested, identify the target and payload byte count, and shall not claim verified terminal delivery.
- **FR-5:** Missing, unreadable, binary, empty, invalid-text, or oversized sources shall produce a concise reason and no clipboard write. A file-view placeholder is not content to copy. Rejection shall preserve the existing clipboard. Limits and text eligibility are defined below.
- **FR-6:** Disk access and transcript export shall run outside the model's synchronous key handler. Each action shall capture its target identity at dispatch. Navigation during the read shall not redirect the action to a different target; feedback shall identify the captured target. Concurrent copies shall be serialized or refused with a busy indication so completion order cannot overwrite a newer request unexpectedly.

**Proof Artifacts:**

- Model/command test output demonstrating exact run-ID and Markdown/JSON/JSONL payloads and no clipboard dispatch on rejected inputs.
- A transcript fixture spanning multiple pages, retries, and generations, with expected copied text and passing assertions demonstrating whole-step scope and source ordering.
- Terminal capture plus pasted synthetic content demonstrating an OSC52 request reaches a configured terminal's clipboard; record terminal and multiplexer configuration.

### Unit 2: Copy a selected transcript item

**Purpose:** Let operators copy one meaningful message or tool exchange using existing item navigation.

**Functional Requirements:**

- **FR-7:** In message view, `y` shall copy the currently selected transcript item. Text and thinking items shall copy their recorded text; a matched tool exchange shall include both recorded input and output. Collapse, wrapping, scrolling, and rendering bounds shall not shorten the copied content.
- **FR-8:** Item resolution shall use the existing loaded-page item model. Use-only and result-only tool items shall copy only the available member and identify the missing counterpart; copy shall not scan history to repair pairing. Whole-transcript copying shall use ordered durable blocks rather than pairing across pages.
- **FR-9:** Unsupported items shall copy a labeled representation of their decoded durable block rather than disappearing. Existing durable truncation shall be explicitly labeled; copying shall not imply reconstruction of data already omitted by the writer. A stale or absent selection shall produce no clipboard write and a clear unavailable indication.
- **FR-10:** `y` and `Y` shall leave cursor, scroll position, follow mode, filters, search state, and expansion state unchanged. Copying selected loaded content shall work without a run directory; whole recorded-transcript copying without a durable source shall report unavailable rather than fall back to a partial page.

**Proof Artifacts:**

- Table-driven payload tests covering prose, thinking, matched and unmatched tool members, unsupported blocks, durable truncation, Unicode, and expanded/collapsed equivalence.
- Model tests and a short UI capture demonstrating selecting an item, requesting copy, and retaining navigation state; include persistence-off and stale-selection cases.

### Unit 3: Copy review evidence and expose accurate contextual help

**Purpose:** Apply the same convention to existing review selections without changing review decisions or adding a selection mode.

**Functional Requirements:**

- **FR-11:** In review source mode, `y` shall copy the explicit source range when range-selection mode is active; otherwise it shall copy the current source line. Reversed ranges shall copy in document order, and horizontal panning shall not clip source text. `Y` shall copy the current document.
- **FR-12:** In a parsed review diff, `y` shall copy an explicit range when active; otherwise it shall copy the full hunk containing the cursor, including its original `@@` header and patch lines even if folded. `Y` shall copy the full current file's diff, including file headers and all hunks. For a multi-file patch, the cursor determines the current file; an explicit range may span files and still copies exactly that source range. A cursor outside any hunk shall make hunk copy unavailable; a cursor outside any file shall make file-diff copy unavailable. An unparsed diff shall use ordinary source line/document behavior with matching help.
- **FR-13:** In review Markdown preview, `y` shall copy the source Markdown corresponding to the current preview block, using existing source-line mappings. `Y` shall copy the current document. Preview styling shall not enter the payload.
- **FR-14:** Copying shall not change the active document, range, fold state, comments, reviewed flags, draft, or verdict and shall not submit a review. Browse/range copy keys shall not intercept typing in comment/summary editors, search inputs, or existing confirmation prompts. In particular, `y` shall continue to answer an active confirmation.
- **FR-15:** Footer/help shall derive copy availability from the same conditions as dispatch and describe the actual target, such as `y copy message`, `y copy exchange`, `y copy range`, `y copy hunk`, `Y copy file`, or `Y copy transcript`. Whole-transcript help shall explain that all recorded history and filtered-out items are included. Disabled actions shall not be advertised as available. Existing palette actions, if exposed, shall follow the same dispatch/availability rules; expanding the palette is not required.
- **FR-16:** Operator documentation shall explain the mapping, source-content semantics, limits, existing durable truncation, and OSC52 terminal/tmux prerequisites. The B1/T2 tracking entries shall be updated during implementation to accurately describe delivered scope and deferred file-line selection; this Phase 1 document does not mark either goal implemented.

**Proof Artifacts:**

- Review model tests proving source line/range, Markdown block, folded hunk, multi-file patch scope, and malformed-diff fallback payloads.
- Regression tests demonstrating editor/confirmation precedence, unchanged review drafts, and agreement between help and dispatch.
- UI captures of Transcript file mode and Review diff mode showing contextual copy hints, plus pasted synthetic source demonstrating no gutters, colors, clipping, or panel chrome.

## Non-Goals (Out of Scope)

1. New line cursors or text-range selection in Monitor file/message views; mouse selection or mouse-first interaction.
2. Clipboard history, paste commands, clipboard reads, local clipboard backends, configuration selectors, or automatic terminal configuration changes.
3. Copying all runs, all workflow steps, all review documents, or all files from a multi-file diff in one action.
4. Screenshot/screendump export, copying only rendered screen rows, or a separate collapsed-summary copy action.
5. New global keybindings, key remapping, standalone Chat support, or special copy actions on the Monitor Steps panel or generated review overview.
6. Changing transcript persistence/schema, tool pairing rules, engine behavior, or adding a new secret-redaction policy.

## Design Considerations

The primary model is **current unit / entire current source**:

| Active content | `y` | `Y` |
|---|---|---|
| Runs list | Selected run ID | Unavailable |
| Step messages | Selected full item | All recorded messages for selected step |
| Monitor output/diagnostic file | Unavailable | Selected file text |
| Review source | Explicit range or current line | Current document |
| Parsed review diff | Explicit range or current hunk | Current file's diff |
| Review Markdown preview | Current block's source | Current document |

Copy applies to the focused content surface. Users must see an explicit selection for small-unit copying. A viewport's first visible line is never an implicit selection. Use existing theme/status/help conventions and nonmodal feedback; do not add a new panel or shift layout. Copy success feedback means `Copy requested: <target> (<N> bytes)`; unavailable and read/limit errors name the target without dumping its contents.

## Repository Standards

- Follow `AGENTS.md`, `CLAUDE.md`, and the vocabulary in `CONTEXT.md`. Actual TUI packages are split into `internal/tui/monitor`, `review`, `runs`, and `shared`; use current code as evidence where older documentation still names monolithic files.
- Use Go 1.25 and the pinned Charm v2 dependencies. Handle `tea.KeyPressMsg`, return commands/messages, and keep root rendering under the existing `tea.View` lifecycle.
- Use the theme singleton in `internal/tui/shared`; no ad-hoc colors. Reuse existing key-binding/help conventions and ownership boundaries.
- Keep transcript files as truth and the event bus as liveness. Copying shall not add bulk payloads to engine events or journal records.
- Persistence-off remains supported. No harness/backend or workflow TOML changes are required.
- Use table-driven tests and direct model `Update` tests with synthetic fixtures. `docs/TESTING.md` has stale package-coverage statements; existing TUI test files provide current conventions.
- Implementation validation includes relevant package tests, `go test ./...`, `go test ./internal/tui/... -race`, `go vet ./...`, formatting of changed Go files, build, and repository-required workflow validation. Phase 1 changes documentation only.

## Technical Considerations

### Clipboard payload contract

- Copy source text rather than stripping styles from the rendered viewport. Preserve Markdown fences, indentation, JSON syntax, diagnostic JSONL records, diff prefixes, and source line endings. Do not add a trailing newline to run IDs. For a line/range/block slice, preserve the source terminator when present.
- Remove terminal escape sequences and unsafe terminal control characters from text payloads; preserve tabs and line endings. Reject invalid UTF-8 and NUL-containing file content as non-text. Do not reinterpret tool output as Markdown. Sanitization must not turn an otherwise empty payload into a clipboard-clearing request.
- A single ordinary text/thinking item copies its text without an added role header. Tool members use a short header identifying block type, tool name/ID and recorded status, followed by the normalized activity as readable JSON containing all decoded fields. For a paired exchange, emit input member then result member. This retains structured/non-text descriptors without fetching linked or external content.
- Whole-transcript export emits each complete decoded block in file/block order, once, with a header identifying role, block type, sequence, generation, iteration, and attempt. Separate block records with a blank line; use the same body serializer as item copy. This makes ordering and provenance observable without merging or deduplicating tool snapshots.
- Mark `Truncated` blocks with `[recorded content truncated]`. Whole-transcript export includes all recorded block types, including thinking and items hidden by filters. It cannot recover content omitted before persistence.
- A hunk copy contains the original hunk header/body. A whole-file diff includes the original file-level headers/metadata and hunks. These are evidence text; an arbitrary selected range is not promised to be an independently applicable patch.

### Bounds and live sources

- Set the MVP maximum clipboard text payload to **256 KiB (262,144 bytes), before OSC52 base64 encoding**, aligned with the existing Monitor file-read cap. Allow exactly the limit and reject larger payloads; do not silently truncate. Check the final serialized/sanitized payload, including added headers.
- File copying shall use bounded reads, not only a pre-read size check, so a growing file cannot cause unbounded allocation. Capture an opened source and its initial size; copy that prefix and do not chase appends. An unexpected short read or I/O failure rejects the request.
- Bound raw transcript scanning separately at **8 MiB per action** to cap work even when many records yield little copied text. Reject larger transcript snapshots explicitly. This is an implementation default for this MVP, not a claim about terminal capacity.
- Whole-transcript copy captures an end offset on the opened file when its command starts, then streams only that prefix. Ignore an incomplete trailing record as the transcript reader already does; identify the result as a recorded snapshot. Skip malformed complete records consistently with reader tolerance, but report the skipped-record count in feedback so omissions are not silent. Do not allocate a whole-history item graph or mutate the loaded page.
- Selected-item and review copies use captured in-memory source data. Review content comes from the immutable review-round document, not a fresh read of a working file that may have changed.
- Empty sources and sources unavailable with persistence disabled shall leave the clipboard unchanged. A request with any read/size error shall never dispatch an earlier partial payload.

### Integration and current research

Expected seams are Monitor content selection (`selectedContent`, `selectedOutputFile`, transcript item selection), Review source/preview/diff mappings, Runs row selection, and a small shared copy command/result facility. Keep source serialization testable independently of terminal delivery; do not add engine or harness dependencies to accomplish copy.

Research checked 2026-09-10:

- [Bubble Tea release notes](https://github.com/charmbracelet/bubbletea/releases), a maintained upstream source: v2 provides OSC52 clipboard commands. The installed `charm.land/bubbletea/v2@v2.0.8/clipboard.go` was also inspected and defines `SetClipboard(string) Cmd`. Use this command path rather than direct stdout writes; no dependency upgrade is needed for this API.
- [tmux Clipboard guide](https://github.com/tmux/tmux/wiki/Clipboard), a maintained upstream document: clipboard propagation depends on tmux and terminal support/settings. Document operator prerequisites and distinguish request emission from confirmed delivery. Do not query or alter the user's clipboard/settings to infer success.

No material conflict with current repository patterns remains. OSC52 compatibility is conditional on the operator's terminal chain; the application limit does not guarantee that every terminal accepts a payload of that size.

## Security Considerations

Clipboard writes occur only after an explicit operator copy action. Preserve the content the operator selected, including recorded diagnostics or thinking, without introducing an automatic redaction policy; documentation must make full-history behavior clear. Never log clipboard payloads or persist them in drafts, transcripts, or engine events. Use synthetic content in proofs and never commit real credentials or private clipboard contents. Payload data must not be interpolated into shell commands, and reading the clipboard or fetching tool-linked resources is out of scope.

## Success Metrics

1. Every enabled mapping in the design table has an exact-payload automated assertion; every unavailable mapping has a no-dispatch assertion.
2. Item payloads remain identical across collapse, resize, and viewport changes; whole-transcript export includes earlier pages and filtered-out blocks within the stated limits.
3. Review copies preserve source ranges/hunks and leave draft, verdict, selection, and navigation state unchanged.
4. Boundary, growing-file, empty, missing, binary, malformed-record, stale-selection, and persistence-off tests demonstrate bounded behavior and explicit feedback.
5. A manual paste proof confirms OSC52 delivery in a documented supported terminal; automated command tests alone are not reported as end-to-end clipboard proof. Document any untested SSH/tmux environment instead of claiming universal compatibility.

## Open Questions

No blocking questions remain. The user accepted the interaction and OSC52 scope in conversation. The 256 KiB payload cap and 8 MiB transcript scan cap are concrete MVP defaults chosen here to bound work; they are not terminal capability guarantees. Feedback wording may be refined during implementation while preserving target identification, byte count, and request-versus-delivery semantics.
