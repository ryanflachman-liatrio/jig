# 15-tasks-zed-style-monitor-transcript.md

## Task Planning Basis

This task list implements
[`15-spec-zed-style-monitor-transcript.md`](15-spec-zed-style-monitor-transcript.md).
The parent tasks map one-to-one to its four demoable units. Sub-tasks are
approved for detailed decomposition.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_model.go` | Owns current group/block state, stable keys, caches, and Monitor fields that must migrate to Transcript-item state. |
| `internal/tui/monitor/monitor_transcript.go` | Owns page loading, bounded edge context, follow behavior, and the current group/render-plan rebuild path. |
| `internal/tui/monitor/monitor_transcript_items.go` | New pure normalization, pairing, spacing, and detail-bound helper seam. |
| `internal/tui/monitor/monitor_transcript_items_test.go` | New table-driven coverage for pure normalized-item behavior. |
| `internal/tui/monitor/monitor_transcript_view.go` | New conversation-first renderer and one-level disclosure seam. |
| `internal/tui/monitor/monitor_transcript_test.go` | Existing and new transcript loading, bounds, cache, and snapshot coverage. |
| `internal/tui/monitor/monitor_tool_summary.go` | Existing semantic tool-name/input classifier that needs structured, cell-safe output. |
| `internal/tui/monitor/monitor_search.go` | Current raw-entry and consecutive-group search/filter behavior to migrate to item identity. |
| `internal/tui/monitor/monitor_search_test.go` | Search, filter, page-boundary, and role/error regression coverage. |
| `internal/tui/monitor/monitor_update.go` | Current two-level toggle/navigation and follow-pause behavior to simplify to one item level. |
| `internal/tui/monitor/monitor_gate_context.go` | Saves and restores group/block selection and expansion state for Gate context. |
| `internal/tui/monitor/monitor_gate_context_test.go` | Gate snapshot deep-copy and restoration regression coverage. |
| `internal/tui/monitor/monitor_layout.go` | Rebuilds width-dependent renderers and invalidates transcript cache on resize. |
| `internal/tui/monitor/monitor_view.go` | Renders Transcript chrome and must relocate redundant selected-step status. |
| `internal/tui/monitor/keys.go` | User-visible activity/disclosure key help terminology. |
| `internal/tui/monitor/monitor_test.go` | Model-driven Monitor interaction, rendering, navigation, and ANSI snapshot coverage. |
| `internal/tui/shared/styles.go` | Sole semantic theme location for new transcript hierarchy styles and obsolete style removal. |
| `internal/transcript/transcript.go` | Durable entry/block contract that must remain unchanged and provides pairing coordinates. |
| `internal/transcript/reader.go` | Existing bounded page reader whose behavior constrains page-edge pairing. |
| `CONTEXT.md` | Canonical Transcript-item, Tool-exchange, and incomplete-item vocabulary already settled in review. |

### Notes

- Use model-driven Bubble Tea tests: feed messages to `Update` and assert on
  model state or ANSI-stripped `View()` output; do not require a live terminal.
- Keep `internal/transcript`, runner, harness, engine, workflow TOML, and backend
  selection unchanged. The feature is a Monitor presentation replacement.
- Use `gofmt -l -w .`, `go vet ./...`, `go test ./... -count=1`, and
  `go build ./cmd/jig` as the repository quality gates. Generate snapshots only
  with synthetic, sanitized transcript fixtures.

## Requirement-to-Proof Traceability

| Requirement | Planned task | Observable proof |
| --- | --- | --- |
| Unit 1: bounded page-local immutable items | 1.2 | `TestBuildTranscriptItemsPageLocal` in `monitor_transcript_items_test.go` |
| Unit 1: scoped FIFO correlation | 1.3 | `TestPairToolBlocksScopedFIFO` table cases |
| Unit 1: use-order anchoring | 1.3 | `TestPairToolBlocksOutOfOrderResults` |
| Unit 1: truthful use-only/result-only states | 1.4 | `TestTranscriptItemIncompleteToolStates` |
| Unit 1: unsupported content remains inspectable | 1.5 | `TestBuildTranscriptItemsUnsupported` |
| Unit 1: visible execution dividers | 1.5 | `TestTranscriptItemBoundariesForVisibleCoordinates` |
| Unit 2: Markdown User guidance and assistant prose | 2.2 | ANSI-stripped default-render snapshot assertions |
| Unit 2: one-level disclosures by content kind | 2.3 | `TestMonitorTranscriptOneLevelDisclosure` |
| Unit 2: one row per successful exchange | 2.3 | dense fixture default snapshot |
| Unit 2: explicit failed exchange/result-only state | 2.4 | `TestMonitorTranscriptFailedActivity` |
| Unit 2: one expansion with pretty input/verbatim output | 2.4 | `TestMonitorTranscriptToolExchangeExpansion` |
| Unit 2: safe semantic tool summary/fallback | 2.5 | `TestSummarizeToolCall` table cases |
| Unit 2: cell-safe narrow rows | 2.6 | `TestMonitorTranscriptVisibleWidth` |
| Unit 3: member-aware atomic search/filter | 3.2 | input-only/output-only search tests |
| Unit 3: role-user tool-result atomicity | 3.2 | `TestTranscriptFilterRoleUserKeepsToolExchange` |
| Unit 3: one-level navigation and expand-all | 3.3 | `TestMonitorChatActivityNavigation` |
| Unit 3: stable selection/fallback across rebuilds | 3.3 | resize/reload/filter cursor-restoration tests |
| Unit 3: follow pause and bounded live behavior | 3.4 | follow-pause and stream-reload tests |
| Unit 3: deep-cloned Gate restoration | 3.5 | Gate context round-trip and alias tests |
| Unit 4: byte/row/tail/UTF-8 detail bounds | 2.6 | `TestBoundDetail` table cases |
| Unit 4: truthful truncation reporting | 2.6 | capture-truncation rendering test |
| Unit 4: semantic theme/no old bars | 2.7 | ANSI snapshot and style-consumer checks |
| Unit 4: resize cache-surface isolation | 3.6, 4.2 | cache-surface and width-change tests |
| Unit 4: obsolete Spec 11 removal | 4.1 | absence checks plus focused Monitor suite |
| Unit 4: persistence-off no-I/O placeholder | 4.3 | `TestMonitorTranscriptPersistenceOff` |

## Tasks

### [x] 1.0 Normalize bounded transcript pages into stable Transcript items

**Covers:** Spec Unit 1 — all functional requirements.

**Completion criteria:** The loaded transcript page is converted into immutable,
page-local items with scoped FIFO tool correlation, deterministic use-order
anchoring, truthful incomplete-item states, execution dividers, and no dropped
unknown content.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestTranscriptItem|TestToolPair|TestTranscriptPage' -count=1` passes, demonstrating scoped FIFO pairing, duplicate IDs, out-of-order results, and bounded page-edge use-only/result-only behavior.
- Test: `internal/tui/monitor/monitor_transcript_items_test.go` table cases demonstrate generation/iteration/attempt isolation, visible execution dividers, and unknown block preservation without a panic.
- Code review evidence: `internal/tui/monitor/monitor_transcript_items.go` contains no file I/O or unbounded transcript scan, demonstrating normalization remains page-local.

#### 1.0 Tasks

- [x] 1.1 Add a dense synthetic transcript fixture and characterization assertions in `monitor_transcript_test.go`; include interleaved text/thinking, out-of-order results, incomplete tools, execution boundaries, malformed input, and unsupported content.
- [x] 1.2 Define stable Transcript-item, item-key, block-reference, correlation-key, display-state, cache-key, and line-range types in `monitor_model.go`; replace group-only model fields without retaining compatibility state.
- [x] 1.3 Create `monitor_transcript_items.go` with pure page flattening and scoped FIFO correlation keyed by generation, iteration, attempt, and tool ID; anchor exchanges at uses and emit independent empty-ID/incomplete items.
- [x] 1.4 Implement conservative Tool-exchange display-state derivation for matched success/error, running use-only, terminal use-only, and result-only cases; never infer an unsupported lifecycle state.
- [x] 1.5 Add pure visible-boundary insertion, unsupported-item construction, item-member lookup, and item spacing helpers; keep them free of styles, file I/O, and model mutation.
- [x] 1.6 Add table-driven `monitor_transcript_items_test.go` coverage for pair order, duplicate/empty/reused IDs, page edges, interleaved blocks, boundaries, unsupported blocks, and item identity.
- [x] 1.7 Migrate transcript reload/page-switch pruning in `monitor_transcript.go` to rebuild item state from the bounded page while preserving persistence-off and saved-key restoration behavior.

### [x] 2.0 Render the conversation-first Transcript and bounded disclosures

**Covers:** Spec Unit 2 and Unit 4 rendering/detail functional requirements.

**Completion criteria:** The Monitor renders prose-first content, one-row Tool
exchanges, conservative errors/incomplete states, semantic fallback labels, and
bounded detail sections through the shared theme without the obsolete group/bar
presentation.

#### 2.0 Proof Artifact(s)

- Snapshot: `JIG_UI_SNAPSHOT_DIR=<new-temp-dir> go test ./internal/tui/monitor -run 'Test.*Transcript.*Snapshot' -count=1` produces sanitized 120×40 default and expanded captures demonstrating one successful row per Tool exchange, prose-first hierarchy, and no old group/bar chrome.
- Test: `go test ./internal/tui/monitor -run 'TestMonitor.*(Collapse|Expand|Render|Width|Detail)' -count=1` passes, demonstrating one disclosure reveals paired Input/Output, verbatim output, explicit errors, row/byte/tail bounds, and cell-width-safe narrow rows.
- Test: `go test ./internal/tui/shared -count=1` passes after semantic Transcript theme styles replace obsolete monitor-only styles.

#### 2.0 Tasks

- [x] 2.1 Split rendering responsibility by moving conversation body and transcript presentation helpers into `monitor_transcript_view.go`; retain loading, paging, and follow orchestration in `monitor_transcript.go`.
- [x] 2.2 Render user text blocks as subtle Markdown User guidance and assistant text as header-free themed Markdown; preserve verbatim rendering for system, tool-result, and command output.
- [x] 2.3 Implement one-level disclosure rows and item-keyed line ranges for thinking, Tool exchanges, result-only tools, system output, terminal result errors, and unsupported content; remove outer tool-group and separate routine-success-result rendering.
- [x] 2.4 Implement paired Input/Output detail sections behind one exchange expansion, including pretty JSON input, verbatim output, sanitized error hints, and explicit text/glyph failure state.
- [x] 2.5 Expand `monitor_tool_summary.go` into a pure icon/action/detail/layout classifier; sanitize controls, use compact cell-safe paths, retain raw names privately, and supply a generic unknown-tool fallback.
- [x] 2.6 Add pure byte-then-rendered-row detail bounding with the 4,096-byte, 12-row, three-tail-row contract; preserve UTF-8 and final status/error tails and report capture truncation honestly.
- [x] 2.7 Add semantic `Styles.Chat` fields in `shared/styles.go` and migrate active Monitor rendering to them; obsolete group styles remain isolated for Task 4.1 removal.
- [x] 2.8 Add default, expanded, failed, unsupported, system-output, live-tail, wide-rune, narrow-width, and ANSI-stripped rendering assertions in `monitor_test.go` and `monitor_transcript_test.go`.

### [x] 3.0 Migrate search, navigation, live follow, and Gate context to item identity

**Covers:** Spec Unit 3 — all functional requirements.

**Completion criteria:** Search, filters, navigation, expansion, reload, resize,
follow, and Gate round trips operate on stable Transcript-item keys while retaining
page-local bounds and deterministic fallback behavior.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test.*(Search|Filter)' -count=1` passes, demonstrating input/output search maps to one Tool exchange, `role:user` keeps role-user results atomic, and filtered execution dividers remain truthful.
- Test: `go test ./internal/tui/monitor -run 'Test.*(Navigation|Resize|Follow|GateContext)' -count=1` passes, demonstrating stable selection, expand-all behavior, follow pausing, deep-cloned Gate snapshots, and deterministic missing-key fallback.
- Test: `go test ./internal/tui/monitor -race -count=1` passes, demonstrating the state transition changes do not introduce a race under the repository's TUI test suite.

#### 3.0 Tasks

- [x] 3.1 Replace raw-entry/consecutive-group filtering in `monitor_search.go` with normalized-item visibility, member-aware query matching, item-keyed hits, and matching-surface metadata.
- [x] 3.2 Preserve atomic Tool exchanges for input/output/name/role/error/retry matches, including `role:user` matches through role-user results; ensure filtered visible-coordinate dividers are neither fabricated nor duplicated.
- [x] 3.3 Replace group/block cursor and toggle branches in `monitor_update.go` with one-level item navigation, item expansion, expand-all override, saved-key restoration, and deterministic nearest-visible fallback.
- [x] 3.4 Preserve existing manual-navigation follow pause, same-step stream reload, page switching, and live-tail coalescing while migrating those paths to stable item keys.
- [x] 3.5 Migrate `monitor_gate_context.go` snapshots to selected item keys and a deep-cloned one-level expansion map; restore search, filters, scroll, follow, page position, and seen sequence safely.
- [x] 3.6 Update `monitor_layout.go`, `monitor_view.go`, and `keys.go` for thin-guide inset widths, surface-aware cache invalidation, compact selected-step chrome, and activity/detail help terminology.
- [x] 3.7 Add search/filter, navigation, resize, reload, follow, and Gate-context regression tests in their existing package-local test files, including cache-key collision and missing-saved-key cases.

### [~] 4.0 Complete migration cleanup and verify the Monitor end to end

**Covers:** Spec Unit 4 migration, persistence-off, and repository-conformance requirements.

**Completion criteria:** Obsolete Spec 11 group state and tests are removed;
responsibilities are split into focused monitor files; persistence-off remains
graceful; focused and full repository verification pass with sanitized acceptance
captures.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -count=1` passes, including persistence-off, cache-surface, and obsolete-group regression coverage.
- Command output: `go test ./... -count=1`, `gofmt -l -w .`, `go vet ./...`, and `go build ./cmd/jig` complete successfully, demonstrating repository quality gates.
- Snapshot: sanitized default, one-detail-expanded, all-details-expanded, narrow, search-output-hit, error-filter, live-tail, paused, and persistence-off captures demonstrate the manual acceptance matrix without credentials or private transcript data.

#### 4.0 Tasks

- [~] 4.1 Remove obsolete `toolGroup`, group header/gap render kinds, group expansion maps, double-expansion branches, and group-only tests after all item-based paths are active.
- [x] 4.2 Verify state maps and caches are pruned to the loaded page, summary JSON is decoded during rebuild rather than repaint, and resize invalidates only width-dependent render surfaces.
- [x] 4.3 Add and run persistence-off, corrupt/partial input, bounded edge-context, and no-unbounded-scan regression cases; confirm Monitor behavior remains backend-agnostic and transcript-schema-free.
- [ ] 4.4 Generate synthetic, sanitized ANSI acceptance captures for default, one-detail, all-details, narrow, search-output-hit, error-filter, live, paused, and persistence-off scenarios.
- [ ] 4.5 Run focused Monitor/shared tests, `go test ./... -count=1`, `gofmt -l -w .`, `go vet ./...`, and `go build ./cmd/jig`; attach the exact outputs to the parent proof artifact during implementation.
