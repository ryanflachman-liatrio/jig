# 25-tasks-transcript-card-primitive.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Read implementation/tests before changing behavior; use `internal/tui/shared` for shared presentation; preserve persistence-off and synthetic-proof contracts. | none |
| `README.md` | yes | The Monitor is backend-agnostic; the Go patch version comes from `go.mod`; repository documentation describes current supported behavior. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; child TUI packages must not import the root TUI package; finalized transcript content remains file-backed. | none |
| `docs/CONVENTIONS.md` | yes | Keep APIs narrow and ownership explicit; distinguish omitted values from explicit zero; bound render caches and measure representative performance before adding complexity. | none |
| `docs/TUI.md` | yes | Measure terminal cells with ANSI-aware helpers; define semantic styles in `Styles`/`DefaultTheme`; cache keys and invalidation must cover every rendering input. | none |
| `docs/TESTING.md` | yes | Use table-driven synthetic TUI cases; assert ANSI-aware dimensions across small/narrow/wide layouts; supplement model tests with recorded visual evidence. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | Keep manual titled-border composition centralized and width-matched with `lipgloss.Width`; truncate long titles without destabilizing layout. | none |
| `go.mod` | yes | Use Go 1.25.12 and the pinned Charm v2, Lip Gloss 2.0.5, Glamour 2.0.1, and ANSI 0.11.7 APIs; add no dependency for this slice. | none |
| `mise.toml` | yes | Select the Go 1.25 toolchain series. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy is present. | none |
| `.github/pull_request_template.md` | not found | No pull-request template is present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves all material choices: rounded cards remain the default at 40
columns and above, padding uses presence-aware pointers, tint uses an SGR
stabilization pass, and only tool exchanges receive header cards in this slice.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/card.go` | New owner of `CardState`, `CardSection`, `Card`, padding resolution, content wrapping, SGR tint stabilization, and `RenderCard`. |
| `internal/tui/shared/card_test.go` | New table-driven contract tests for geometry, padding, dividers, ANSI/Unicode labels, wrapping, state colors, tint coverage, and code-fence output. |
| `internal/tui/shared/panel.go` | Existing sole manual titled-border compositor and `TruncateTitle`; generalize the private edge builder while preserving `PanelTopEdge`. |
| `internal/tui/panel_test.go` | Existing panel/breadcrumb geometry tests; extend for styled-title truncation and regression coverage around compositor reuse. |
| `internal/tui/shared/styles.go` | Add semantic card styles in `Styles`/`DefaultTheme` and remove the four unused `Chat.Bar*` fields resolved by the spec. |
| `internal/tui/shared/styles_test.go` | Verify all card styles are initialized from the expected semantic palette roles. |
| `internal/tui/shared/palette.go` | Add the two documented jig-local recessed background tokens. |
| `internal/tui/shared/markdown.go` | Supplies the existing width-specific Chroma formatter used by the tinted code-fence proof; no production change is expected. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Current flat item renderer; split exchange-card rendering from unchanged orphan-result rendering and preserve existing detail writers. |
| `internal/tui/monitor/monitor_model.go` | Extend render surfaces/keys so cached cards identify every varying presentation input. |
| `internal/tui/monitor/monitor_transcript.go` | Owns page replacement, item-state pruning, selection restoration, persistence-off loading, and card-cache lifecycle. |
| `internal/tui/monitor/monitor_layout.go` | Owns width-dependent renderer rebuilds and resize invalidation. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Extend rendering tests for card state, width, unchanged item kinds/details, and structured-edit disclosure. |
| `internal/tui/monitor/monitor_transcript_items_test.go` | Extend page-local state, cache-bound, replacement, and persistence-off tests. |
| `internal/tui/monitor/monitor_test.go` | Existing transcript navigation, line-range, resize, refresh, paging, search/filter, and clipboard integration test seam. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Optional focused home for the synthetic 300-entry benchmark and card-specific integration cases if existing test files become difficult to navigate. |
| `docs/specs/25-spec-transcript-card-primitive/25-proofs/` | New sanitized test, benchmark, terminal-capture, screenshot, and environment evidence produced during implementation. |

### Notes

- Keep unit tests beside their owning package and use fabricated transcript entries; never read a real `.jig/` run for fixtures or proof artifacts.
- Use `lipgloss.Width` and the pinned `github.com/charmbracelet/x/ansi` wrapping/truncation APIs for visible-cell calculations; raw byte or rune counts are not valid layout evidence.
- `RenderCard` remains pure presentation. Monitor rendering must not perform filesystem, backend, or network work, and `RunDir == ""` must retain the current empty-state path.
- Format only changed Go files. Run focused shared/Monitor tests while iterating, then the root build, test, vet, targeted TUI race test, and whitespace checks recorded under Task 5.0.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-01.1 | 1.9, 2.3, 2.7 | `card_test.go` exact `lipgloss.Width` cases at normal and defensive widths. |
| FR-01.2 | 1.6–1.9 | `card_test.go` cap/label-style cases and existing `panel_test.go` regressions. |
| FR-01.3 | 1.3, 1.9, 5.2 | `card_test.go` state-border table plus adjacent-state Monitor screenshot. |
| FR-01.4 | 1.8–1.9 | `card_test.go` labeled and unlabeled divider table. |
| FR-01.5 | 1.7, 1.9 | `card_test.go`/`panel_test.go` ANSI, control-whitespace, grapheme, and zero-budget title cases. |
| FR-01.6 | 2.1–2.3, 2.7 | `card_test.go` newline, indentation, blank-line, ANSI, and hard-wrap cases. |
| FR-01.7 | 2.4–2.8 | Per-visible-cell SGR background-state assertions and styled gallery. |
| FR-01.8 | 1.2, 1.8–1.9, 5.3 | Width-40 rounded-frame tests plus narrow Monitor screenshot. |
| FR-01.9 | 3.8, 4.8 | `monitor_transcript_items_test.go` persistence-off empty-state regression. |
| FR-01.10 | 1.6, 1.9 | Existing/new panel tests and card cap tests using the shared compositor. |
| FR-01.11 | 1.2, 1.9 | `CardContentWidth`/`RenderCard` omitted, inherited, asymmetric, zero, and negative padding table. |
| FR-01.12 | 1.1, 1.3, 1.9 | State/style initialization table and invalid-state pending fallback. |
| FR-01.13 | 3.1, 3.7–3.8 | Exchange/use-only card tests plus unchanged orphan/non-exchange item tests. |
| FR-01.14 | 3.3, 3.6–3.8 | Header grammar, selected cue, expansion, structured-edit, and truncation regressions. |
| FR-01.15 | 3.2, 3.7 | Monitor display-state-to-card-state table including incomplete warning. |
| FR-01.16 | 3.5–3.8, 5.3 | Header-only two-row, detail-below-card, and disclosure tests plus captures. |
| FR-01.17 | 3.4, 3.7 | Selected/unselected prefix and full-row ANSI-aware width assertions. |
| FR-01.18 | 4.5–4.6 | Navigation/line-range tests for cached/fresh, tall, resized, and refreshed items. |
| FR-01.19 | 4.1–4.4, 4.7 | Cache identity, invalidation, freshness, pruning, and bounded-size tests. |
| FR-01.20 | 3.6, 3.8, 4.8 | Paging, normalization, search/filter, clipboard, detail-bound, and render-I/O regressions. |

## Tasks

### [x] 1.0 Establish the shared card contract, theme, and border geometry

Create the reusable `internal/tui/shared` card API and state-driven theme,
centralize panel/card border-bar composition, and demonstrate exact rounded
geometry with styled and Unicode labels at normal and defensive widths. Existing
panels and breadcrumbs must retain their current appearance and dimensions.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared ./internal/tui -run 'Test(RenderCard|CardContentWidth|Panel|Breadcrumb|TruncateTitle)' -v` passes table-driven cases for widths 40/60/90 and defensive widths, three-dash card caps, sharp labeled dividers, all divider rules, styled Unicode labels, embedded whitespace normalization, omitted/asymmetric/explicit-zero padding, all five states, invalid-state fallback, and muted-border override; this covers FR-01.1–FR-01.5, FR-01.8, and FR-01.10–FR-01.12.
- Test: the same focused output includes existing panel and breadcrumb cases plus new styled-title cases, demonstrating that the shared compositor and ANSI-aware `TruncateTitle` preserve the panel's one-dash cap, title styling, dimensions, grapheme boundaries, and valid escape sequences (FR-01.2, FR-01.5, FR-01.10).
- Terminal capture: `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-1-card-gallery.txt` records a synthetic gallery at 40, 60, and 90 columns with every state, header/meta combinations, labeled/unlabeled sections, and exact `lipgloss.Width` measurements; it demonstrates the reusable component without private run data.

#### 1.0 Tasks

- [x] 1.1 Add `internal/tui/shared/card.go` with documented `CardState` constants (`CardPending`, `CardRunning`, `CardSuccess`, `CardWarning`, `CardError`), `CardSection`, and `Card` fields from the spec; use `*int` for both padding fields and keep renderer state local to the call.
- [x] 1.2 Implement one private padding resolver used by both `CardContentWidth(width int, padLeft, padRight *int)` and `RenderCard`: nil left defaults to one, nil right inherits the resolved left, explicit negative values clamp to zero, narrow frames reduce effective padding only as needed to retain one content cell, and widths zero/negative return no output.
- [x] 1.3 Add `Styles.Card` with the eight required semantic styles in `internal/tui/shared/styles.go`, initialize pending/running from primary, success from dim, warning from warning, error from danger, muted from the existing subdued panel border, and tint styles from the new palette tokens; invalid `CardState` values must select pending presentation.
- [x] 1.4 Add `hexToolNeutralBg = "#1A191F"` and `hexToolErrorBg = "#2A1A1E"` to `internal/tui/shared/palette.go` with comments identifying them as jig-local additions rather than Charmtone tokens.
- [x] 1.5 Remove only the now-unused `Chat.BarThinking`, `Chat.BarToolCall`, `Chat.BarToolResult`, and `Chat.BarError` fields, their obsolete comment, and their `DefaultTheme` assignments; confirm no caller remains with `rg 'Bar(Thinking|ToolCall|ToolResult|Error)'`.
- [x] 1.6 Generalize the private border-bar construction in `panel.go` to accept cap length, left/right corner or tee glyphs, and an already-styled label; keep `PanelTopEdge`'s signature, one-dash cap, `Theme.Panel.Title` styling, width, and empty-title behavior unchanged while cards request a three-dash cap.
- [x] 1.7 Replace `TruncateTitle`'s rune loop with the pinned ANSI-aware truncation helper, normalize embedded control whitespace before calculating the one-line label, return no label for a zero budget, and preserve caller styling plus grapheme boundaries for wide characters, combining marks, and emoji.
- [x] 1.8 Implement top, divider, and bottom card bars: join nonempty header/meta with styled ` · `, preserve supplied label styles, color only border glyphs, draw labeled first-section dividers, suppress an unlabeled first rule, and keep every unlabeled bar continuous with no internal space gap.
- [x] 1.9 Add table-driven `card_test.go`, `styles_test.go`, and panel regression cases covering every Task 1 proof condition and measuring every row with `lipgloss.Width`; render the sanitized 40/60/90 state gallery and record it in `25-proofs/25-task-1-card-gallery.txt`.

### [x] 2.0 Make card bodies wrap and tint safely across styled content

Complete the card body renderer so multiline, indented, blank, unbroken, and
ANSI-styled content wraps without truncation while the optional state tint
covers every visible cell and remains contained to each card row.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared -run 'TestRenderCard(Content|Tint|Width)' -v` passes synthetic cases for embedded newlines, hard-wrapped words, trailing-space removal, leading indentation, internal blank lines, caller foreground styles, empty sections, and exact row widths at normal and defensive sizes; this covers FR-01.1 and FR-01.6.
- Test: background-state assertions walk every rendered visible cell after `CSI m`, `CSI 0 m`, `CSI 49 m`, combined SGR parameters, explicit inner backgrounds, and jig's `CodeBlockFormatter` output, then check a sentinel after the card; this demonstrates complete tint restoration, preserved foreground styling, and no row-to-row leakage for FR-01.7.
- Terminal capture: `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-2-styled-gallery.txt` records plain text and a Glamour/Chroma Go fence inside neutral and error-tinted cards, including the terminal dimensions and per-row width/background audit.

#### 2.0 Tasks

- [x] 2.1 Split each supplied section line on embedded newlines, trim trailing whitespace before wrapping, and preserve explicit internal blank lines plus leading indentation; empty sections with neither a requested divider nor an explicit line must add no body row.
- [x] 2.2 Wrap each logical line with the pinned ANSI-aware hard-wrap API to `CardContentWidth`, including long unbroken words, without imposing a new content budget or truncating caller content.
- [x] 2.3 Compose each body row as border + resolved left padding + styled content + fill + resolved right padding + border, then enforce the requested visible width for normal and defensive sizes without allowing ANSI byte counts to enter the geometry math.
- [x] 2.4 Implement a final SGR stabilization pass for tinted rows that restores the card background after full/default resets (`CSI m`, `CSI 0 m`) and background reset (`CSI 49 m`), including combined parameters, without mistaking zero components inside RGB parameters for attribute resets.
- [x] 2.5 Preserve intentional inner backgrounds until their reset, preserve foreground/emphasis sequences, tint borders/padding/fill/blank rows, close every tinted row with a default-background reset, and ensure `Tint: false` emits no card background.
- [x] 2.6 Add an ANSI-state test helper that tracks the effective background at every visible cell rather than searching each line for one escape sequence; assert complete neutral/error tint coverage and a plain sentinel after the rendered card to prove containment.
- [x] 2.7 Render a Go fence through a deterministic Glamour renderer configured with `shared.CodeBlockFormatter`, place its output in neutral and error cards, and cover synthetic resets, combined SGR, foreground preservation, explicit inner backgrounds, blank lines, indentation, unbroken words, and exact normal/defensive widths in `card_test.go`.
- [x] 2.8 Generate the sanitized styled gallery at recorded terminal dimensions and save its output plus per-row width/background audit as `25-proofs/25-task-2-styled-gallery.txt`.

### [x] 3.0 Render Monitor tool exchanges as truthful header cards

Convert exactly the normalized tool-exchange item to a tinted, header-only
card while preserving the current activity grammar, selection cue, expansion
behavior, detail writers, structured-edit output, and write-time truncation
notice. Orphan results and every non-exchange item keep their existing form.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test.*(ToolExchange|HeaderCard|StructuredEdit|Orphan|PersistenceOff)' -v` demonstrates success/error/running/incomplete state mapping, use-only exchanges, unchanged orphan-result rendering, unchanged text/system/thinking/unsupported rendering, and an empty persistence-off transcript; this covers FR-01.9 and FR-01.13–FR-01.16.
- Test: ANSI-aware width assertions show every top and bottom card row plus its selected/unselected outside prefix fits `transcriptInnerW` at narrow and wide layouts, with the prefix applied once and no synthetic body row; this covers FR-01.16 and FR-01.17.
- Test: existing structured-edit and expanded-detail assertions continue to show new-code output and truncation notices below the card, demonstrating that per-item and expand-all disclosure semantics remain unchanged (FR-01.14, FR-01.16, FR-01.20).

#### 3.0 Tasks

- [x] 3.1 Refactor the combined tool switch arm just enough to share use/result activity resolution and existing summary grammar, while dispatching `transcriptItemToolExchange` to the card path and retaining the current flat orphan `transcriptItemToolResult` path.
- [x] 3.2 Map `toolDisplaySuccess` to `CardSuccess`, `toolDisplayError` to `CardError`, `toolDisplayRunning` to `CardRunning`, and both incomplete/unknown states to `CardWarning`; invalid or incomplete values must never silently acquire a success border.
- [x] 3.3 Build the exact existing exchange header from its expansion marker, semantic activity label, preview, failed hint, running text, and incomplete text; apply selected label emphasis/cue without replacing the state-derived border style.
- [x] 3.4 Calculate the selected or unselected outside prefix once, subtract its `lipgloss.Width` once from `transcriptInnerW`, skip card rendering when no meaningful frame remains, and prefix every returned card row consistently so ANSI styling cannot cause overflow.
- [x] 3.5 Render tool exchanges as `Tint: true`, header-only cards with zero sections; emit exactly the card's top and bottom rows with no synthetic empty body row and preserve the existing newline-based line accounting.
- [x] 3.6 Leave `writeToolActivityDetails`, `writeItemDetail`, `writeNewCodeCards`, the structured-edit default expansion, write-time truncation notice, and per-item/expand-all toggles below the header card with their current widths and content.
- [x] 3.7 Add table-driven Monitor cases for paired success/failure, running use-only, terminal incomplete use-only, selected/unselected prefixes, narrow/wide widths, and no synthetic body row; use ANSI-aware assertions for border color and total row width.
- [x] 3.8 Add explicit regressions proving orphan results and text/system/thinking/unsupported items retain their existing flat presentation, expanded details and structured edits remain visible, clipboard payloads remain undecorated, and persistence-off loading produces the existing empty state without entering card rendering.

### [x] 4.0 Preserve navigation and bound cached card rendering

Use the existing item-render cache for header cards with complete render
identity and explicit replacement/resize/step invalidation. Keep line ranges
derived from emitted output so navigation remains stable across taller cards,
page replacement, filtering, refresh, expansion, and resize.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test.*(CardCache|LineRange|TranscriptNavigation|PageReplacement|Resize)' -v` demonstrates successive-card navigation, a tall expanded exchange, cached/fresh line-range equality, resize behavior, and preservation of a manually selected item during same-page refresh; this covers FR-01.18.
- Test: same-key running-to-success and running-to-failure replacements, styled-header changes, selection/expansion toggles, step changes, and repeated resizes produce fresh output while `chatItemRendered` retains at most one current card variant per loaded item; this covers FR-01.19.
- Test: page-size, search/filter membership, clipboard payload, detail limits, persistence-off behavior, and render-path I/O assertions remain unchanged under a 300-entry synthetic page; this covers FR-01.9 and FR-01.20.
- Benchmark: `go test ./internal/tui/monitor -run '^$' -bench BenchmarkTranscriptCardPage -benchmem` reports cached repeated-render time and allocations for 300 synthetic entries, with hardware/toolchain recorded in `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-4-benchmark.txt` and compared with the 100 ms repaint budget.

#### 4.0 Tasks

- [x] 4.1 Add a dedicated card render surface and extend `transcriptRenderKey` (or an owned immutable identity it contains) to cover item identity, available card width, effective expansion, selection, display state, and final styled header content.
- [x] 4.2 Add a focused helper that retrieves or renders only the exchange header card; on a miss, remove any prior card variants for that item before storing the current one so cache size remains bounded by the loaded page.
- [x] 4.3 Clear exchange-card output during every `setChatPage` replacement because a same-key activity/result may change, while retaining the selected item key and explicit expansion choice through the existing rebuild path.
- [x] 4.4 Invalidate affected card entries when `transcriptInnerW` changes in `rebuildRenderer`, and retain the existing whole-map reset on step changes; do not couple card-cache storage to the Markdown block cache.
- [x] 4.5 Keep `chatItemLineRanges` outside cached output, recalculate each range from the actual bytes emitted on every render, include both header rows and expanded details, and exclude structural inter-item spacing.
- [x] 4.6 Extend navigation tests to walk successive two-row cards, jump across a tall expanded exchange, render from both cache states, resize, replace a page, and preserve a manually selected item during same-step refresh.
- [x] 4.7 Add cache lifecycle cases for same-key running-to-success and running-to-failure replacement, header-content change, selection and expansion toggles, repeated width changes, page pruning, and step changes; assert output freshness and a cache bound of at most one card entry per loaded exchange.
- [x] 4.8 Exercise a full 300-entry synthetic page through repeated render, paging, normalization, search/filter, expansion, and clipboard paths; assert existing page/detail bounds and absence of render-time filesystem/network/backend activity.
- [x] 4.9 Add `BenchmarkTranscriptCardPage` for cached repeated rendering of the 300-entry page with allocation reporting, then record command, Go version, OS/architecture, CPU, result, and comparison with the 100 ms repaint budget in `25-proofs/25-task-4-benchmark.txt` without adding a timing assertion to CI tests.

### [x] 5.0 Demonstrate the integrated visual states and run acceptance checks

Produce reviewer-safe component and Monitor evidence at realistic dimensions,
verify failed/running work is visually prominent beside quiet success, and run
the repository checks applicable to the completed Go/TUI change.

#### 5.0 Proof Artifact(s)

- Screenshot: `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-5-monitor-states.png` shows adjacent failed and successful exchanges in the real Monitor projection with danger and dim borders; its companion notes record terminal size, `transcriptInnerW`, selection, expansion state, and synthetic fixture source (FR-01.3, FR-01.13–FR-01.17).
- Screenshots: narrow and wide Monitor captures in `docs/specs/25-spec-transcript-card-primitive/25-proofs/` show aligned rounded frames, readable shortened headers, retained selection cues, and expanded details below the header card (FR-01.8, FR-01.14, FR-01.16–FR-01.18).
- CLI: captured outputs for `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, targeted `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and `git diff --check` demonstrate the repository's applicable acceptance gates. Any unavailable terminal screenshot is recorded as a limitation rather than replaced by a component-only claim.

#### 5.0 Tasks

- [x] 5.1 Build a deterministic synthetic Monitor fixture containing adjacent successful, failed, running, and incomplete exchanges plus an expanded structured edit; ensure every path, command, and output is fabricated.
- [x] 5.2 Capture the integrated Monitor at the primary review size with failed and successful cards adjacent; save `25-task-5-monitor-states.png` and companion notes containing terminal dimensions, `transcriptInnerW`, selected item, expansion state, fixture source, and observed state colors.
- [x] 5.3 Capture narrow and wide Monitor views from the same fixture and verify rounded alignment, header shortening, selection cues, state prominence, and details remaining below the card; store both captures under `25-proofs/`.
- [x] 5.4 Run and record the focused shared and Monitor tests from Tasks 1–4, including the 300-entry benchmark and `go test -race ./internal/tui/...`; distinguish ordinary pass, intentional skip, assertion failure, and environment/toolchain failure.
- [x] 5.5 Format only the changed Go files with `gofmt -w <changed-go-files>`, confirm `gofmt -l <changed-go-files>` is empty, and capture clean results for `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, and `git diff --check`.
- [x] 5.6 Review the final diff against every FR and non-goal: confirm no detail-section conversion, grouping/header-grammar redesign, storage/harness/backend change, new dependency, light-theme work, or unrelated slice-00 cleanup entered the change; record any unavailable visual proof as a precise limitation.
