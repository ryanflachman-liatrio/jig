# 23-tasks-clipboard-yank.md

## Planning Status

Phase 2 — complete task plan. The user explicitly requested sub-task generation after reviewing the parent tasks. Audit result is recorded separately in `23-audit-clipboard-yank.md`; implementation has not started.

Source: `23-spec-clipboard-yank.md`, FR-1 through FR-16. Clarifications are resolved in `23-questions-1-clipboard-yank.md`. The user accepted the current-unit/whole-source mapping and requested task planning for B1.

## Standards Evidence

Repository guidance was inspected before generating the parent tasks; the spec alone is not the standards source.

| Source file | Read | Standards extracted | Conflicts / decision |
|---|---|---|---|
| `AGENTS.md` | yes | Persistence-off is first-class; theme singleton; preserve backend/engine separation | No conflict; takes precedence over CLAUDE notes |
| `README.md` | yes | Go 1.25; documented build/validate commands; architecture/testing references | No conflict |
| `CLAUDE.md` | yes | File is truth; Bubble Tea v2 key/command conventions; prose versus verbatim rendering | Earlier Phase 1 read; old monolithic TUI paths resolved against current split packages |
| `CONTEXT.md` | yes | Transcript item is selection unit; Review/input queue vocabulary; backend-agnostic presentation | Earlier Phase 1 read; no conflict |
| `docs/TESTING.md` | yes | Direct model Update tests; table-driven cases; race checks for UI work | Earlier Phase 1 read; claims that TUI has no tests are stale; existing tests are current evidence |
| `go.mod` | yes | Go 1.25.12 minimum; Bubble Tea v2.0.8; existing ANSI dependency | `mise.toml` selects the compatible 1.25 toolchain series |
| `mise.toml` | yes | Go 1.25 toolchain series | No conflict |
| `/AGENTS.md`, `/Users/AGENTS.md`, `/Users/ryan/AGENTS.md`, `/Users/ryan/Repos/AGENTS.md` | not found | None | Repository-root guidance applies |
| `CONTRIBUTING.md`, `.github/pull_request_template.md` | not found | None | Use root guidance and existing conventions |
| `.pre-commit-config.yaml`, `.github/workflows/` | not found | No hook or CI policy found at these paths | Use documented build/test/vet/validation commands |
| `internal/tui/**/AGENTS.md`, `internal/tui/**/README.md` | not found | No additional package-local guidance found in file inventory | Use existing source and tests |

## Current Code Evidence and Planning Decisions

- Root `Update` handles cross-screen messages before dispatching to the active surface. Copy coordination must survive navigation and use a root-owned busy/request identity, rather than letting each surface independently overwrite the clipboard. Refuse an overlapping request with busy feedback; clear busy state on success or failure. No engine events carry copy data.
- `runs.Update` resolves selection from `visibleRows`; copy must respect Home's focused Runs panel and retain confirmation/editor precedence.
- Monitor already distinguishes transcript/file/review-overview content. File copy reads source bytes rather than `fileBody` output, which can contain Markdown rendering and placeholders.
- Selected transcript items remain page-local. Whole-transcript copying is a separate bounded snapshot export, sharing the block-body serializer but never building an all-history item graph.
- Review `document.meta.Content` contains the verified immutable round content. Its `lines` slice is split on newline; copied slices must retain original line terminators from source content rather than blindly joining display lines.
- Review diff rows expose file indices and hunks expose source ranges; reuse those mappings for file/hunk extraction. Malformed diffs keep the specified source fallback.
- Preserve the specified 256 KiB final-payload and 8 MiB raw-transcript limits. Errors never dispatch a partial payload. Keep header/provenance formatting deterministic and test exact payloads.
- New test names below are planned references, not claims that tests already exist or have passed. All proof fixtures and pasted content must be synthetic.

## Relevant Files

New files are explicitly marked; inspection-only dependencies are distinguished below. Keep implementation within these seams and record any necessary file additions in this inventory.

| File | Why it is relevant |
|---|---|
| `internal/tui/root.go`, `internal/tui/root_update.go`, `internal/tui/home.go` | Copy coordination, cross-screen result routing, focused Home dispatch and help |
| `internal/tui/shared/clipboard.go` (new), `internal/tui/shared/clipboard_test.go` (new) | Shared request/result contract, payload eligibility, bounds, OSC52 command seam |
| `internal/tui/clipboard_test.go` (new) | Root routing, busy handling, focus and confirmation regression tests |
| `internal/tui/clipboard.go` (new) | Root-owned admission/completion handling and nonmodal feedback coordination |
| `internal/tui/runs/keys.go`, `internal/tui/runs/update.go`, `internal/tui/runs/runs_test.go` | Selected run ID and availability/notice behavior |
| `internal/tui/runs/view.go` | Runs help and footer availability |
| `internal/tui/monitor/keys.go`, `internal/tui/monitor/monitor_update.go`, `internal/tui/monitor/monitor_model.go`, `internal/tui/monitor/monitor_view.go` | Content-specific dispatch, help, and copy feedback |
| `internal/tui/monitor/outputfiles.go`, `internal/tui/monitor/monitor_transcript_items.go` | Existing file eligibility and item selection/member mappings; inspect/reuse without changing pairing |
| `internal/tui/monitor/clipboard.go` (new), `internal/tui/monitor/clipboard_test.go` (new) | File snapshot reads, full transcript export, selected-item payloads and integration tests |
| `internal/transcript/reader.go`, `internal/transcript/transcript.go` | Existing durable format and reader-tolerance evidence; no schema or ordinary paging change planned |
| `internal/tui/review/model.go`, `internal/tui/review/update.go`, `internal/tui/review/keys.go` | Review selection modes, key precedence, contextual help |
| `internal/tui/review/document.go`, `internal/tui/review/diff.go`, `internal/tui/review/preview.go` | Immutable source data and source/block/hunk/file mappings |
| `internal/tui/review/clipboard.go` (new), `internal/tui/review/clipboard_test.go` (new) | Source-preserving extraction and model regressions |
| `internal/tui/monitor/monitor_gate.go` | Forward copy requests without emitting a review draft write |
| `internal/tui/diffview/diff.go`, `internal/tui/diffview/diff_test.go` | Add original-source file ranges including metadata, without changing existing hunk navigation |
| `internal/tui/root.go`, `internal/tui/review/view.go` | Existing root/review view and status integration; no additional panel |
| `internal/tui/clipboard_fixture_test.go` (new), `internal/tui/testdata/clipboard/` (new) | Reproducible synthetic run/review fixture and expected paste payloads for manual proof |
| `README.md`, `docs/clipboard.md` (new), `docs/plans/open-goals.md` | Discoverable operator instructions and accurate B1/T2 status |
| `docs/specs/23-spec-clipboard-yank/proofs/` (new artifacts) | Sanitized per-task test logs, source/paste comparisons and terminal evidence |

## Tasks

### [ ] 1.0 Copy run IDs and selected Monitor files through a shared OSC52 flow

Completion: Home Runs `y` copies the complete run ID; Monitor file `Y` copies the selected text artifact. Shared eligibility, final payload limits, request feedback, busy handling and cross-screen routing work end to end. File snapshots use bounded asynchronous reads, source formatting is preserved, unavailable keys stay unavailable, and editor/confirmation precedence is intact.

Dependencies: none. Covers FR-1, FR-3–FR-6 and the applicable portions of FR-10, FR-14, FR-15. This is the first slice of spec Unit 1, with delivery infrastructure demonstrated through real copy targets.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared ./internal/tui/runs ./internal/tui/monitor -run 'TestClipboard' -v`; planned `TestClipboardRunID`, `TestClipboardFilePayload`, `TestClipboardEligibilityAndLimits`, and `TestClipboardFileSnapshot` assert exact payloads for Markdown/JSON/JSONL, empty/invalid/binary/missing/unreadable files, sanitization, exact-limit/over-limit behavior, appends and short reads. Maps FR-1, FR-3–FR-5.
- Test: `go test ./internal/tui -run 'TestClipboard' -v`; planned `TestClipboardRequestRouting`, `TestClipboardBusy`, and `TestClipboardFocusPrecedence` assert captured target identity through navigation, off-handler reads, one pending request, reset after errors, no clipboard reads/helpers, and no clipboard dispatch from unavailable or intercepted keys. Maps FR-4, FR-6, FR-14, FR-15.
- UI/paste: synthetic run ID and artifact copied with the actual keys in a configured terminal; record exact expected and pasted text, key sequence, target, terminal/multiplexer setup, and emitted feedback in `proofs/23-task-1.0-clipboard-basics.md`. This demonstrates delivery rather than merely rendering a success notice. Maps FR-1, FR-3, FR-4, FR-15.

#### 1.0 Tasks

- [ ] 1.1 Add the shared clipboard request/result types and final-payload preparation in `shared/clipboard.go`. A request carries captured target identity/label and a payload loader; results carry request identity, payload or error, and omission metadata. Define the 262,144-byte payload limit, strip terminal escape/control sequences while retaining tabs/CR/LF, reject invalid UTF-8/NUL file text and empty sanitized content, and provide an injectable OSC52 command seam defaulting to `tea.SetClipboard`. Never include source text in notices or logs. Cover eligibility, limit boundaries, Unicode and escape/control cases in `TestClipboardEligibilityAndLimits` (FR-4, FR-5).
- [ ] 1.2 Add root-owned request admission in `clipboard.go`, `root.go`, and `root_update.go` before active-screen message dispatch. Admit only one request, assign a monotonically increasing identity, refuse overlaps with busy feedback, and run the loader as a command. Match completions by identity, ignore stale completions, and sequence OSC52 emission before releasing the busy slot; clear errors without emission. Preserve target labeling across screen changes and show status through existing themed notice/footer regions without modifying input editors. Add routing/busy tests for delayed completion, navigation, repeated keys and errors; prove the loader is not executed by the synchronous key handler (FR-4–FR-6).
- [ ] 1.3 Add Runs `y` binding and selection-derived availability in `runs/keys.go`, `runs/update.go`, `runs/view.go`, and Home help. Capture the visible row ID, emit the shared request, and keep `Y` unavailable. Test empty lists, filtering/reordering, exact ID without newline, focused Home dispatch, and no copy behind delete/leave/help/palette overlays or text inputs (FR-1, FR-14, FR-15).
- [ ] 1.4 Implement Monitor file `Y` in `monitor/clipboard.go` using the selected file identity/path, with no `y` binding for that content. In the command, open and inspect the file, require a regular file, reject a source larger than the existing 256 KiB file cap, and read only its captured initial length with a bounded reader; reject short reads/errors. Do not use rendered `fileBody` or placeholders. Test Markdown/JSON/JSONL, CRLF/final-newline preservation, appends, source replacement after open, shrinking files and deterministic injected read failures; NUL detection covers the entire bounded content (FR-3, FR-5, FR-6).
- [ ] 1.5 Wire Monitor/Home contextual copy hints and existing notices, respecting focused regions and search/filter/overlay ownership. Copy completion must not change scroll/follow/selection. Verify shared OSC52 dispatch with a command fake and a separate assertion against the pinned `tea.SetClipboard` command message; do not query the real clipboard in automated tests. Complete the task 1 planned test commands (FR-4, FR-10, FR-14, FR-15).
- [ ] 1.6 Add `TestClipboardFixture` in `internal/tui/clipboard_fixture_test.go`, activated only by a test-specific destination argument/environment value, to create a synthetic persisted run using existing persistence helpers and fixture data. The default test run skips the manual export path. Document an exact `mktemp -d` destination, fixture invocation, TUI launch from that fixture workspace and key/paste steps. It must operate only inside that explicit temporary destination, require no agent credentials, and start no agent backend. Extend it in later tasks for transcript/review evidence. Save task 1 test output, sanitized captures, expected/pasted content and terminal setup in the specified proof file; do not mark manual delivery verified if it cannot be exercised.

### [ ] 2.0 Copy the selected step's entire recorded transcript

Completion: Transcript `Y` exports a bounded recorded snapshot beyond the loaded page, in durable block order with required provenance. It includes all recorded block kinds and execution coordinates regardless of filters and collapse, identifies durable truncation/skipped records, and never changes the loaded view or silently substitutes a partial page.

Dependencies: 1.0. Covers FR-2, FR-4–FR-6, FR-8–FR-10, FR-15; completes the whole-content portion of spec Unit 1. Establish the shared block-body serializer for use by task 3.0.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestClipboardTranscript' -v`; planned `TestClipboardTranscriptSnapshot` and `TestClipboardTranscriptPayload` compare exact export text from synthetic multi-page, multi-attempt/generation/iteration records and assert each decoded block appears once in source order. Include filtered thinking, unmatched tool members, unknown blocks, durable truncation, and exclusion of other steps/children/artifacts. Maps FR-2, FR-8, FR-9.
- Test: same command; planned `TestClipboardTranscriptBoundsAndErrors` exercises 8 MiB raw scan boundaries, 256 KiB serialized payload boundaries, malformed complete records with feedback count, incomplete trailing writes, append/short-read failures, empty and persistence-off sources. Planned `TestClipboardTranscriptPreservesView` compares cursor, filter/search, page, scroll, follow and expansion before/after dispatch and completion. Maps FR-4–FR-6, FR-10, FR-15.
- UI/paste: load the tail of a synthetic multi-page transcript, enable a filter, press `Y`, and compare pasted export with the fixture showing older/filtered blocks. Record commands, expected content and feedback in `proofs/23-task-2.0-transcript-all.md`. Maps FR-2, FR-4, FR-10.

#### 2.0 Tasks

- [ ] 2.1 Implement the deterministic block-body serializer in `monitor/clipboard.go`. Text/thinking emit recorded text; tool blocks emit a type/name/ID/status header and readable normalized activity JSON retaining decoded fields; unsupported blocks emit a type label and decoded JSON. Include `[recorded content truncated]` for flagged blocks. Whole-export headers identify role, block type, sequence, generation, iteration and attempt. Establish exact fixture strings for all formats and preserve source text endings independently of presentation rendering (FR-2, FR-8, FR-9).
- [ ] 2.2 Implement bounded transcript snapshot reading in the Monitor copy command: capture an opened file's initial size/end offset, reject a source exceeding 8,388,608 bytes, and stream that prefix in complete JSONL records. Ignore an incomplete trailing record, count skipped malformed complete records, preserve file/block order and stop with an error if serialized/sanitized output exceeds 262,144 bytes. Bound per-record allocation by the remaining raw budget. Never use all-history indexing/item pairing or chase appends; reject unexpected short reads and I/O errors without dispatching partial text (FR-2, FR-5, FR-6, FR-8).
- [ ] 2.3 Connect Transcript `Y` using captured run/step/path, independently of loaded page/filter/search/disclosure state. Restrict export to that step's recorded source; no implicit child/family/other-file traversal. Report unavailable for empty run dir/missing source and identify successful exports as recorded snapshots with byte count and skipped-record count when nonzero. An all-invalid/empty source must not clear the clipboard (FR-2, FR-4, FR-5, FR-10).
- [ ] 2.4 Add the planned transcript payload/snapshot/bounds/state tests. Cover both raw and final byte-limit boundaries, malformed JSON, incomplete tail, growing/shrinking snapshots, all block kinds, generations/attempts/iterations, filtered-out thinking, independent tool records, and no mutation of page/navigation state. Use controlled readers for timing/failure cases instead of sleeps; test changing selected step while a request is pending (FR-2, FR-5, FR-6, FR-8–FR-10).
- [ ] 2.5 Extend the synthetic fixture with a multi-page transcript and exact expected export; document how to reach the tail and filter an item type. Run the task 2 tests and capture the specified paste comparison and unchanged view in `proofs/23-task-2.0-transcript-all.md`. State any durable omissions explicitly (FR-2, FR-4, FR-10, FR-15).

### [ ] 3.0 Copy the full selected transcript item with stable navigation

Completion: Transcript `y` copies the selected text/thinking item or tool exchange in full regardless of disclosure/wrapping. Item copy uses only loaded members, labels missing counterparts/unsupported content and existing truncation, handles stale selections, and works from captured content without persistence.

Dependencies: 1.0 and 2.0's shared serializer. Covers FR-4, FR-5, FR-7–FR-10, FR-15; matches spec Unit 2.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestClipboardItem' -v`; planned `TestClipboardItemPayloads` asserts exact text/thinking, use/result, paired input-before-output, structured/non-text descriptor, unknown-type and truncation payloads. `TestClipboardItemPageBoundary` proves missing counterparts are labeled with no history read or external resource fetch. Maps FR-7–FR-9.
- Test: same command; planned `TestClipboardItemStateAndAvailability` covers collapse/expand/resize equivalence, Unicode/control sanitization, bounds, stale/absent selection, no-run-directory content and untouched navigation. Verify item copy is unavailable for files/review overviews and accurately labeled in help. Maps FR-4, FR-5, FR-7, FR-9, FR-10, FR-15.
- UI/paste: copy the same selected exchange collapsed and expanded and compare pasted content; record an unchanged cursor/follow/scroll view in `proofs/23-task-3.0-transcript-item.md`. Maps FR-7, FR-10, FR-15.

#### 3.0 Tasks

- [ ] 3.1 Resolve `y` from the Monitor's current selected transcript item key and loaded members, validating member indices before reading them. Capture immutable block/activity data before deferred work, deep-copying referenced slices/JSON where needed to avoid live-update races. Text/thinking use their full recorded source; matched exchanges emit use then result with the task 2 serializer. Do not read disk or fetch linked tool resources (FR-7, FR-8).
- [ ] 3.2 Add labels for missing tool counterparts, unsupported decoded content and durable truncation; distinguish missing/stale selection from an empty eligible item. Apply common payload rejection and OSC52 handling. Item copy must work with no run directory and be unavailable on Monitor files, Steps focus and generated review overviews (FR-5, FR-8–FR-10).
- [ ] 3.3 Add type-specific small-copy hints from the same availability logic as dispatch. Handle `y` only after existing text-entry/overlay logic and preserve item cursor, page, search/filter, expansion, follow mode and viewport position. Test collapse/expand/resize equivalence, page-edge pairs, interleaved tool records, malformed/stale selection and concurrent source refresh against captured payloads (FR-7–FR-10, FR-15).
- [ ] 3.4 Complete `TestClipboardItemPayloads`, `TestClipboardItemPageBoundary`, and `TestClipboardItemStateAndAvailability`; extend synthetic fixtures with text, thinking and a tool exchange. Save the test output and same-item collapsed/expanded paste comparison in `proofs/23-task-3.0-transcript-item.md` (FR-4, FR-7–FR-10, FR-15).

### [ ] 4.0 Copy Review lines, ranges, preview blocks, hunks and current documents

Completion: Review `y`/`Y` follows the accepted mode-specific mapping using immutable round content. Exact ranges and source terminators survive pan/fold/preview changes; parsed multi-file diffs copy only the current file with `Y`; malformed diffs fall back to source semantics. All review edits, decisions, selection state and confirmations retain existing behavior.

Dependencies: 1.0 shared copy flow; integrate after 3.0 for sequential verification. Covers FR-4–FR-6, FR-11–FR-15; implements spec Unit 3's Review behavior.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui/review -run 'TestClipboardReview' -v`; planned `TestClipboardReviewSource`, `TestClipboardReviewPreview`, and `TestClipboardReviewDiff` assert current line, reversed/forward range, range spanning files, source terminators, no-final-newline text, Markdown source block, folded hunk, current-file headers/all hunks, cursor outside hunk/file, and malformed-diff fallback. Maps FR-11–FR-13.
- Test: same command; planned `TestClipboardReviewStateAndPrecedence` and `TestClipboardReviewEligibility` prove unchanged draft/comments/verdict/reviewed flags/selection, no submission, immutable snapshots despite working-file changes, unavailable selection behavior, payload rejection, and comment/summary/discard-confirmation precedence. Maps FR-4–FR-6, FR-14, FR-15.
- UI/paste: browse a synthetic two-file patch, yank a folded hunk and then its file diff; copy a source range and Markdown preview block. Compare pasted output against originals and capture contextual hints in `proofs/23-task-4.0-review.md`. Maps FR-11–FR-15.

#### 4.0 Tasks

- [ ] 4.1 Add source-offset slicing in `review/clipboard.go` against immutable `document.meta.Content`. Resolve current line or explicit normalized inclusive range according to Review mode, preserving original CRLF/LF and final newline presence; no source re-read or viewport-based clipping. Implement document `Y`, validate absent/empty selections, and test reversed ranges, horizontally panned content and changed working files (FR-5, FR-11, FR-14).
- [ ] 4.2 Extend `diffview.Presentation` with original-source file spans and file ownership of metadata rows, then expose them through Review diff presentation. Existing code maps hunks/body rows but leaves file metadata unassigned; do not infer file boundaries from only the final hunk. Preserve original `diff --git`, index/mode/rename, `---`/`+++`, hunk and no-newline-marker text; handle git-style and plain multi-file unified patches, header-only changes and hunk-only snippets without manufacturing headers. Keep existing rendering/navigation behavior and add parser range tests for source boundaries, CRLF and adjacent files (FR-12).
- [ ] 4.3 Implement parsed diff `y` as explicit range or current full hunk; `Y` uses the current file span including all its original metadata/hunks. Preserve explicit cross-file ranges, include folded content, and report unavailable outside a hunk/file. Parse failures retain source line/document copying and matching labels. Test file-header cursors, fold boundaries, multiple hunks/files and malformed patches with exact payload assertions (FR-12, FR-15).
- [ ] 4.4 Implement Markdown preview `y` using the selected preview block's existing source-line mapping; `Y` uses the immutable document. Cover fences, headings, multi-line blocks, source/preview toggles and unavailable blocks; payloads retain Markdown source and common limits (FR-5, FR-13).
- [ ] 4.5 Add Review copy availability/bindings after editor and confirmation handling but before browse/range navigation. Forward Review copy requests through Monitor's Gate without emitting the unconditional `DraftChangedMsg` currently scheduled for workspace keys; distinguish copy outcomes explicitly rather than relying on printable-key matching. Ensure completion feedback does not target a different pending review or persist copy data. Test unchanged draft/selection/folds/comments/verdict, no submission or draft-write command, summary/comment typing, discard confirmation, and switching/closing the review during a pending request (FR-6, FR-14).
- [ ] 4.6 Update Review help/status and run all task 4 review tests plus affected diffview tests. Extend the synthetic fixture with an immutable review round containing source, Markdown and a multi-file patch; document exact opening/focus/navigation steps. Save source/range/block/hunk/file paste comparisons and captures in `proofs/23-task-4.0-review.md` (FR-11–FR-15).

### [ ] 5.0 Verify the complete operator flow and publish clipboard guidance

Completion: Every enabled/disabled mapping agrees across root dispatch, focused surface, footer/help, and existing palette behavior if exposed. Operator documentation describes the complete flow, source semantics, limits and terminal prerequisites; B1/T2 status reflects actual delivered evidence. Required checks pass and a synthetic end-to-end paste exercise demonstrates the final integration.

Dependencies: 1.0–4.0. Covers FR-14–FR-16 and integrated acceptance of FR-1–FR-13. This closes spec Unit 3's help/documentation requirements and the spec's success metrics.

#### 5.0 Proof Artifact(s)

- Test: `go test ./internal/tui/... -run 'TestClipboard' -v`; planned `TestClipboardHelpDispatchMatrix` and `TestClipboardRootIntegration` cover each mapping, disabled contexts, overlay/editor/confirmation priority, delayed results across Home/Monitor/Review transitions, and preservation of review/monitor state. Maps FR-1–FR-15; detailed payload assertions remain in their owning parent tasks.
- Documentation proof: `docs/clipboard.md` with a README link includes the exact mapping, 256 KiB payload/8 MiB scan limits, full-history/filter and durable truncation semantics, OSC52/tmux prerequisites and honest delivery feedback. Check documented keys/limits against the mapping-test fixtures and constants; capture the comparison and B1/T2 tracking diff in `proofs/23-task-5.0-operator-flow.md`. Maps FR-16 to a reproducible documentation verification artifact.
- Quality output: record `go test ./...`, `go test ./internal/tui/... -race`, `go vet ./...`, `go build ./cmd/jig`, formatting checks on changed Go files, and validation of each repository workflow under `.agents/jig/*.toml`. Record exact commands, outcomes and any pre-existing failure separately in the same proof file; do not claim pass with outstanding required failures.
- Terminal/paste proof: repeat the run-ID → artifact → transcript item/all → Review copy journey with synthetic content using `go run ./cmd/jig` and a documented fixture/run setup. Include exact expected/pasted outputs and captures of focused content/help. Record terminal and tmux/SSH conditions actually tested; leave untested environments explicit. A terminal screenshot or unit-test log alone does not prove clipboard delivery. Maps FR-1–FR-4, FR-7, FR-11–FR-16.

#### 5.0 Tasks

- [ ] 5.1 Build the table-driven `TestClipboardHelpDispatchMatrix` over Runs, Monitor messages/files/overview, Review source/range/preview/diff, empty and busy states. Cover both `y` and `Y`, focus, overlay/editor/confirmation priority, and contextual hint availability. Include root integration cases that complete delayed requests after navigation or active-review changes. Do not add unrelated palette actions; if copy is exposed through existing palette enumeration, assert identical availability/dispatch (FR-1–FR-15).
- [ ] 5.2 Write `docs/clipboard.md` and link it from README. Document each mapping, underlying source formatting, collapse independence, filtered-out/history inclusion, immutable review snapshots, truncation/malformed-record notices, the two byte limits, busy/error handling and conditional OSC52 terminal/tmux support. Describe `Copy requested` accurately and provide troubleshooting without clipboard probes/helper fallback or changing terminal settings (FR-16).
- [ ] 5.3 Add `TestClipboardDocumentationContract` in `internal/tui/clipboard_test.go` to check the documentation mapping/limit table against independently declared expected keys/targets and the implemented constants; assert the README link and B1/T2 tracking references exist. Keep full prose review in the proof artifact rather than asserting incidental wording. Update B1/T2 status only to the actual implemented/verified state and explicitly retain deferred Monitor file-line selection (FR-16).
- [ ] 5.4 Run final relevant tests, `go test ./...`, `go test ./internal/tui/... -race`, `go vet ./...`, and `go build ./cmd/jig`. Check changed Go files with `gofmt -l` and format only those requiring it. Validate repository workflows using `for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow" || exit 1; done`. Record exit statuses and any pre-existing failures separately; do not change unrelated code merely to obtain a clean run.
- [ ] 5.5 Run the documented synthetic fixture setup and final TUI copy journey. Save exact fixture-generation/TUI/paste steps, expected and pasted files, terminal/multiplexer conditions and screenshots under the proof directory. Compare expected and pasted text with `diff -u` using recorded explicit paths. Manual delivery requires a real configured terminal and paste observation; if unavailable, record the outstanding proof honestly and leave the affected proof task incomplete rather than treating OSC52 command emission as delivery. No backend login or real private run data is needed.
- [ ] 5.6 Assemble `proofs/23-task-5.0-operator-flow.md` with links to earlier parent proofs, the final test/check results and documentation comparison. Check all proof text/captures for credentials/private clipboard content, verify FR coverage against the matrix, and record outstanding validation limitations. Mark completed task checkboxes only when their acceptance evidence is present.

## Requirement-to-Proof Coverage

| Requirement | Parent task(s) | Planned test / observable verification |
|---|---|---|
| FR-1 | 1.0, 5.0 | `TestClipboardRunID`, help/dispatch matrix, run-ID paste |
| FR-2 | 2.0 | `TestClipboardTranscriptSnapshot`, `TestClipboardTranscriptPayload`, multi-page paste |
| FR-3 | 1.0 | `TestClipboardFilePayload`, file paste |
| FR-4 | 1.0–5.0 | `TestClipboardRequestRouting`, OSC52 command seam assertions and terminal paste |
| FR-5 | 1.0–4.0 | Eligibility/limit/file-snapshot/transcript-error/review rejection tests |
| FR-6 | 1.0, 2.0, 4.0, 5.0 | Routing/busy/file/transcript snapshot tests and root integration |
| FR-7 | 3.0 | `TestClipboardItemPayloads`, `TestClipboardItemStateAndAvailability` |
| FR-8 | 2.0, 3.0 | `TestClipboardTranscriptPayload`, `TestClipboardItemPageBoundary` |
| FR-9 | 2.0, 3.0 | Transcript/item unknown, truncated, missing and stale cases |
| FR-10 | 2.0, 3.0 | `TestClipboardTranscriptPreservesView`, `TestClipboardItemStateAndAvailability` |
| FR-11 | 4.0 | `TestClipboardReviewSource`, review source paste |
| FR-12 | 4.0 | `TestClipboardReviewDiff`, hunk/current-file paste |
| FR-13 | 4.0 | `TestClipboardReviewPreview`, preview-source paste |
| FR-14 | 1.0, 4.0, 5.0 | Focus/precedence/review-state/root integration tests |
| FR-15 | 1.0–5.0 | Per-surface availability tests and `TestClipboardHelpDispatchMatrix` |
| FR-16 | 5.2–5.6 | `TestClipboardDocumentationContract`, documented mapping/limit comparison, tracking diff and operator walkthrough |

## Next Checkpoint

Sub-tasks were authorized and generated. Consult `23-audit-clipboard-yank.md` for the mandatory planning audit result. Implementation may begin only after a passing audit; the next workflow request is `Continue SDD with implementation for B1.`
