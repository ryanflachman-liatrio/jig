# 25-tasks-tool-detail-sections.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/card.go` | Owns `Card`, `CardSection`, padding semantics, content-width calculation, and the single outer frame used by expanded exchanges. |
| `internal/tui/shared/card_test.go` | Holds table-driven card width, divider, header-only, styled-content, and explicit-padding tests; extend it for flush-body regression coverage. |
| `internal/tui/shared/truncation.go` | Expected slice-06-owned source for `MoreItems`, `EarlierItems`, and `ExpandHint`; this feature consumes but does not create a competing formatter. |
| `internal/tui/shared/truncation_test.go` | Expected slice-06-owned contract tests; useful for confirming the prerequisite and live-key behavior before Monitor integration. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Current tool-exchange card renderer and legacy `writeToolActivityDetails`/`writeItemDetail` path that must be re-hosted inside the card. |
| `internal/tui/monitor/monitor_tool_detail.go` | New focused owner for effective-activity merging, section construction, terminal-safe raw text, JSON/shell body rendering, and detail hint assembly. |
| `internal/tui/monitor/monitor_tool_detail_test.go` | New table-driven tests for section order, content-aware rendering, sanitization, bounds, and call/result detail merging. |
| `internal/tui/monitor/monitor_transcript_detail.go` | Owns the existing 4 KiB, 12-row, and three-tail-row detail bound that must receive the card content width. |
| `internal/tui/monitor/monitor_transcript_detail_test.go` | Existing UTF-8 and byte/row bound tests; extend for exact card-width wrapping and hidden-row reporting. |
| `internal/tui/monitor/monitor_layout.go` | Constructs width-baked Glamour renderers and invalidates render caches on transcript-width changes. |
| `internal/tui/monitor/monitor_transcript.go` | Owns `fenceJSON`, inset rendering, verbatim rendering, page replacement, and cache invalidation helpers reused by the section path. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing header, collapsed-card, persistence-off, error-meta, cache-refresh, and edit rendering regression coverage. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Existing card cache, line-range/navigation, bounded-page, and deterministic Monitor visual-proof fixtures. |
| `internal/tui/monitor/monitor_vertical_rhythm_test.go` | Protects item spacing and line accounting when expanded cards gain body rows. |
| `internal/tui/monitor/keys.go` | Authoritative per-item expand binding whose live help key is passed to slice 06's `ExpandHint`. |
| `docs/specs/25-spec-tool-detail-sections/25-proofs/` | New sanitized test logs, deterministic ANSI/HTML/PNG captures, limitation notes, and reviewer-first proof summaries produced during implementation. |

### Notes

- Tasks use a red-green-refactor sequence: add the focused failing assertion,
  implement the smallest coherent behavior, then run the focused package tests.
- Complete parent tasks in order. Task 2 depends on the slice-06 shared
  truncation helpers. If `internal/tui/shared` does not yet expose those
  helpers, record the missing prerequisite and pause Task 2 rather than adding
  a tool-detail-only formatter.
- Keep raw tool evidence in `transcript.jsonl`; sanitization changes only the
  terminal projection. Use fabricated inputs and paths in every test and proof.
- Generate ANSI and HTML deterministically behind `JIG_UI_SNAPSHOT_DIR`.
  Convert HTML to PNG with the repository's established local headless-Chrome
  process when available; otherwise record the exact environment limitation
  and retain the reproducible ANSI/HTML artifacts without fabricating a PNG.
- Run `gofmt` only on changed Go files. Required acceptance commands are the
  focused tests listed per task, `go test -race ./internal/tui/...
  ./internal/helpchat -count=1`, `go build ./cmd/jig`, `go test ./...`, `go vet
  ./...`, and `git diff --check`.

## Requirement-to-Test Traceability

| Requirement | Planned Task(s) | Planned Test Evidence |
| --- | --- | --- |
| FR-05.1 | 1.2, 1.4, 3.6 | Monitor section test asserts one complete card; visual fixture shows the integrated exchange. |
| FR-05.2 | 1.2, 1.3 | Section-builder table asserts Locations/Input/Output/Content and repeated-content ordering. |
| FR-05.3 | 1.2, 1.3 | Section-builder table asserts absent and empty sources emit no divider. |
| FR-05.4 | 1.2, 1.3 | Styled-divider and source-contract assertions verify `TranscriptLabel` use and style ownership. |
| FR-05.5 | 1.1, 1.5, 2.6 | Width tables and resize tests assert `CardContentWidth`-derived wrapping. |
| FR-05.6 | 1.1 | Shared card table asserts explicit zero-left-padding geometry. |
| FR-05.7 | 1.4, 1.5, 3.3 | Collapsed exchange test asserts exactly a header-only two-row card. |
| FR-05.8 | 2.3, 2.6 | JSON table asserts highlighted valid values and verbatim invalid fallback at current width. |
| FR-05.9 | 2.4 | Bash table asserts command-only shell rendering and missing/non-string fallback. |
| FR-05.10 | 2.2, 2.3 | Verbatim cases assert logical line preservation without Markdown reflow. |
| FR-05.11 | 2.2 | Hostile-control table asserts source ANSI/CSI/OSC/simple escapes cannot survive the display sanitizer. |
| FR-05.12 | 2.5 | Detail-bound table asserts UTF-8-safe 4 KiB, 12-row, three-tail-row behavior at card width. |
| FR-05.13 | 2.1, 2.5 | Truncation tests assert shared pluralization and live-key hint presence only when rows are hidden. |
| FR-05.14 | 3.1 | Paired-activity table asserts call-side and result-side fields survive a non-mutating merge. |
| FR-05.15 | 3.2 | Structured-edit test asserts current new-code content is inside the exchange card and no computed diff appears. |
| FR-05.16 | 3.2 | Empty completed-edit test asserts the exact adapter-no-detail `Edit` section. |
| FR-05.17 | 3.3 | Error regression asserts the hint appears once in header Meta and never in a body section. |
| FR-05.18 | 3.4 | Cache lifecycle table asserts invalidation and one active variant per loaded exchange. |
| FR-05.19 | 2.6, 3.4 | Resize tests assert renderer rebuild, cache clearing, changed wrapping, and preserved view state. |
| FR-05.20 | 3.5 | Multi-section line-range/navigation tests assert exact ranges and correct `n`/`N` targets. |
| FR-05.21 | 3.3, 3.5 | Persistence-off, orphan, and non-tool regression cases assert established behavior. |

## Tasks

### [ ] 1.0 Render expanded tool details as card-owned sections

Deliver the shared-card geometry and Monitor section-construction path that
makes an expanded exchange one visual unit. Preserve fixed section ordering,
omit empty sections, derive body width from the actual card, retain flush-body
support, and keep collapsed cards header-only. Covers FR-05.1 through FR-05.7.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared ./internal/tui/monitor -run 'Test(RenderCard|ToolDetailSections)' -count=1` passes table-driven cases for divider order, empty-section omission, removal of the legacy nested `│ ` prefix, exact narrow/normal terminal-cell widths, explicit zero left padding, and two-row collapsed cards; this demonstrates FR-05.1 through FR-05.7.
- Terminal capture: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-1-section-gallery.txt`, generated by a deterministic synthetic gallery test at 40, 60, and 90 columns, records zero-, one-, and multi-section cards plus measured row widths; this demonstrates the card grammar and geometry without real run data.
- Proof summary: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-01-proofs.md` records the exact commands, outcomes, artifact paths, requirement coverage, and any limitations for reviewer reproduction.

#### 1.0 Tasks

- [ ] 1.1 Add failing table-driven cases to `internal/tui/shared/card_test.go`
  that render default-padded and explicit-zero-left-padding sections at widths
  40, 60, and 90; assert every row's terminal-cell width and that flush content
  begins immediately after the left border (FR-05.5, FR-05.6).
- [ ] 1.2 Add failing Monitor cases in
  `internal/tui/monitor/monitor_tool_detail_test.go` for fixed
  Locations/Input/Output/Content divider order, repeated Content source order,
  empty-section omission, `TranscriptLabel` styling on divider labels, one
  outer frame, and absence of the legacy six-space/`│ ` detail prefix. Add a
  source-contract assertion that the new section path introduces neither
  inline hex colors nor package-level style variables (FR-05.1 through
  FR-05.4).
- [ ] 1.3 Implement a pure tool-detail section builder in
  `monitor_tool_detail.go`. Return styled `[]shared.CardSection`, preserve
  source order, omit empty inputs, and keep tool interpretation out of
  `internal/tui/shared` (FR-05.2 through FR-05.4).
- [ ] 1.4 Change `renderToolExchangeCard` to accept the built sections and
  render/cache the complete card. Remove the expanded tool-detail append below
  the exchange card while retaining `writeItemDetail` only for non-tool item
  kinds that still use the flat detail presentation (FR-05.1, FR-05.7).
- [ ] 1.5 Replace the old `m.transcriptInnerW-8` detail calculation with
  `shared.CardContentWidth` derived from the exchange's actual available card
  width. Add narrow-width and collapsed-card assertions proving no divider or
  body row appears while collapsed (FR-05.5 through FR-05.7).
- [ ] 1.6 Run the Task 1 focused tests, generate
  `25-proofs/25-task-1-section-gallery.txt` from synthetic fixtures, inspect the
  output for divider/padding geometry, and write
  `25-proofs/25-task-01-proofs.md` with commands, results, FR-05.1–FR-05.7
  mapping, and limitations.

### [ ] 2.0 Add bounded, content-aware, terminal-safe section bodies

Deliver syntax-highlighted JSON and bash command bodies, verbatim fallback for
literal output, source-controlled terminal-sequence sanitization, card-width
bounding, and the shared slice-06 truncation vocabulary. Covers FR-05.8 through
FR-05.13.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestToolDetail(JSON|Bash|Verbatim|Bounds|Sanitization|Truncation)' -count=1` passes cases for highlighted valid JSON, literal invalid JSON, extracted shell commands without a JSON wrapper, missing-command fallback, preserved multiline output, Unicode-safe 4 KiB/12-row/3-tail bounds, source ANSI/OSC removal, and conditional shared expand hints; this demonstrates FR-05.8 through FR-05.13.
- Terminal capture: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-2-content-gallery.txt`, generated from fabricated payloads, shows highlighted JSON, highlighted shell input, plain multiline output, and bounded content with the live-key truncation hint; this demonstrates the content grammar and safe display behavior.
- Proof summary: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-02-proofs.md` records the exact commands, outcomes, artifact paths, requirement coverage, and any limitations for reviewer reproduction.

#### 2.0 Tasks

- [ ] 2.1 Verify the slice-06 prerequisite exposes shared pluralized
  `MoreItems`/`EarlierItems` and conditional `ExpandHint` behavior and that the
  hint accepts the authoritative `keys.Toggle.Help().Key`. If absent, record
  the dependency blocker and stop this parent task without creating a local
  truncation formatter (FR-05.13 and Non-Goal 5).
- [ ] 2.2 Add failing table-driven sanitization tests covering SGR, CSI cursor
  movement, OSC hyperlinks/titles, simple escape sequences, C0 controls,
  printable Unicode, tabs, CRLF, and multiline text. Implement a bounded,
  display-only sanitizer in `monitor_tool_detail.go` that removes unsafe source
  controls before jig-owned styling and leaves durable activities untouched
  (FR-05.10, FR-05.11).
- [ ] 2.3 Add failing JSON cases for valid object/array input, output, and raw
  content plus invalid JSON fallback. Implement JSON rendering by applying the
  display sanitizer, `fenceJSON`, and the inset Glamour/Chroma path at the card
  content width; keep invalid/plain values on the verbatim path (FR-05.8,
  FR-05.10).
- [ ] 2.4 Add failing bash cases for a string `command`, multiline commands,
  missing/non-string commands, and invalid input. Implement shell-fenced input
  for extractable bash commands in the `Input` section, while preserving the
  generic JSON-or-verbatim fallback when extraction fails (FR-05.9).
- [ ] 2.5 Extend `boundTranscriptDetail` tests with long ASCII, Unicode, styled
  JSON, and shell bodies at known card widths. Apply the 4 KiB/12-row/3-tail
  bounds to every section body and append the slice-06 styled
  `… N more line(s)` plus conditional live-key expand hint inside the affected
  section (FR-05.12, FR-05.13).
- [ ] 2.6 Update `rebuildRenderer` so the inset renderer uses the normal card
  content width rather than the retired four-cell new-code inset assumption.
  Add a resize test proving narrow-to-wide reconstruction changes wrapping and
  clears width-dependent cached cards (FR-05.5, FR-05.8, FR-05.12).
- [ ] 2.7 Run the Task 2 focused tests, generate
  `25-proofs/25-task-2-content-gallery.txt` from fabricated JSON, shell,
  verbatim, truncated, and hostile-control payloads, inspect both styled and
  ANSI-stripped output, and write `25-proofs/25-task-02-proofs.md` with exact
  commands, FR-05.8–FR-05.13 mapping, and limitations.

### [ ] 3.0 Complete Monitor lifecycle integration and acceptance evidence

Integrate call/result evidence, structured edits, completed-edit fallback,
complete-card caching, resize invalidation, line-range/navigation accounting,
and persistence/orphan regressions. Produce the real Monitor review capture and
run the repository quality gates. Covers FR-05.14 through FR-05.21 and verifies
the complete feature.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestToolDetail(EffectiveActivity|StructuredEdit|EmptyEdit|Cache|Resize|Navigation|Persistence|Orphan)' -count=1` passes paired use/result merging, in-card new-code, no-detail edit fallback, cache lifecycle, width rebuild, expanded line-range navigation, persistence-off, and unmatched-result cases; this demonstrates FR-05.14 through FR-05.21.
- Test: `go test -race ./internal/tui/... ./internal/helpchat -count=1` passes the TUI race suite, demonstrating that the changed rendering/cache paths preserve existing ownership expectations.
- Terminal capture and screenshot: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-3-monitor-details.txt` and `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-3-monitor-details.png` show adjacent expanded JSON, bash, output, and edit cards in the real Monitor; the notes record terminal size, `transcriptInnerW`, selected item, expansion state, observed wrapping, and use only synthetic data.
- CLI quality evidence: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-3-quality-checks.txt` records outcomes from `gofmt -l <changed-go-files>`, `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, and `git diff --check`; this demonstrates repository-level build, test, vet, format, and whitespace acceptance.
- Proof summary: `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-03-proofs.md` records exact commands, outcomes, artifact paths, full FR-05.1 through FR-05.21 coverage, and any remaining limitations for reviewer reproduction.

#### 3.0 Tasks

- [ ] 3.1 Add failing paired-exchange cases where call-side input/locations and
  result-side output/content are split across distinct activities. Implement a
  non-mutating effective-activity merge that preserves each populated field,
  gives terminal result status/output/content precedence where appropriate,
  and does not change `toolcall.Activity` or transcript persistence (FR-05.14).
- [ ] 3.2 Add failing structured-edit and empty-completed-edit cases. Move the
  existing `New code · <path>` rendering into card content without computing a
  diff, and render the exact adapter-no-detail message as an `Edit` section
  only when no other detail exists (FR-05.15, FR-05.16).
- [ ] 3.3 Extend error and collapsed regressions to prove `toolErrorHint`
  remains only in header Meta, a collapsed exchange stays header-only, and
  unmatched results plus text/system/thinking/unsupported items retain their
  established render paths (FR-05.7, FR-05.17, FR-05.21).
- [ ] 3.4 Extend cache lifecycle and resize tests so complete-card bodies refresh
  on page replacement, step change, expansion change, and width change; assert
  repeated rendering remains bounded to one active card variant per loaded
  exchange and meaningful selection/scroll state survives resize (FR-05.18,
  FR-05.19).
- [ ] 3.5 Extend line-range, block-navigation, search, vertical-rhythm,
  persistence-off, and orphan-result tests with multi-section cards at narrow
  and wide widths. Assert `chatItemLineRanges` exactly cover rendered rows and
  `n`/`N` lands on the intended exchange (FR-05.20, FR-05.21).
- [ ] 3.6 Add an opt-in deterministic visual-proof fixture using
  `JIG_UI_SNAPSHOT_DIR` and fabricated adjacent JSON, bash, output, and edit
  exchanges. Generate the Task 3 ANSI/HTML artifacts, convert the primary frame
  to `25-task-3-monitor-details.png` when headless Chrome is available, record
  dimensions/selection/expansion/wrapping in the notes, and visually inspect
  that one card owns each expanded body (FR-05.1 through FR-05.21).
- [ ] 3.7 Run `gofmt` on the changed Go files, focused Monitor tests, the
  targeted TUI race suite, root build/test/vet, and `git diff --check`; capture
  exact outcomes in `25-proofs/25-task-3-quality-checks.txt`. Write
  `25-proofs/25-task-03-proofs.md` with full requirement coverage and create a
  limitation note for any unavailable screenshot or blocked check rather than
  claiming it passed.
