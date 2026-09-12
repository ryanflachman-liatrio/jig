# 25-spec-truncation-vocabulary.md

## Introduction/Overview

Consolidate the Monitor Transcript panel's truncation affordances behind a
single vocabulary of small, testable helpers in `internal/tui/shared`, replace
every hand-rolled `"… %d …"` phrasing at the call sites, teach the affordance
to name the key that reveals the hidden content, and give the transcript
detail bound a tail-anchored mode so a running step's growing output pins its
newest rows to the bottom instead of stranding the operator on a stale head.
The vocabulary must be picked once and referenced by later slices (05 tool
detail bodies, 07 diff bodies, 08 grouped-read bodies) so those slices
consume this contract rather than inventing their own phrasing.

Source: [OMP transcript parity, slice 06](../../epics/omp-transcript-parity/slices/06-truncation-vocabulary.md).
Depends on: none. Slice 00 (`1ab9ba9`) removed the unreachable render-plan
path, so the surviving live call sites are the four documented in Design
Considerations below and the Steps-panel live-tail path in `writeStreamingOutput`.
Slices 05, 07, and 08 are documented as consumers of this contract, per epic
cross-cutting decision CC-8; this specification does not authorize any of
their work.

## Goals

- **G-06.a** Every truncation indicator drawn by the Monitor's transcript
  presentation is produced by one shared helper pair (head-anchored and
  tail-anchored) with correct pluralization for `n = 1` and `n > 1`.
- **G-06.b** Every collapsible-content indicator names the key that reveals it,
  and the key text is derived from the live Monitor keybinding so a rebind
  updates the hint without editing the transcript renderer.
- **G-06.c** The transcript detail bound exposes an explicit anchor mode
  (`head` for settled content, `tail` for running content). A running tool
  exchange's detail body pins its newest rows to the bottom with a prepended
  `… N earlier lines` marker; a settled exchange keeps the current head+tail
  behavior with an appended `… N more lines` marker.
- **G-06.d** The Steps panel's live streaming-output tail advertises the
  lines it has dropped through the same vocabulary, so a running step's log
  is no longer silently head-truncated.
- **G-06.e** The vocabulary is grep-lockable: a repository test can prove
  that no future call site reintroduces the retired phrasings (`lines
  hidden`, hand-built `"… %d "` prefixes outside the helper) inside the
  Monitor package.
- **G-06.f** The shared helpers, and every call site that consumes them,
  style their output as a hint (`shared.Theme.Chat.Hint`) — never as
  content and never with a bare `lipgloss.NewStyle()` at the call site.

## User Stories

- **As an operator staring at a bounded expanded tool detail**, I want the
  hidden-lines notice to name the key that expands the item so a truncated
  body is not a dead end when I have not memorised `enter` and `o`.
- **As an operator watching a running step's live output**, I want the
  newest streamed rows pinned to the bottom of the pane, and I want the
  dropped-earlier-lines count visible above them, so I never wonder whether
  a rolling log has hidden the row that matters.
- **As an operator whose expand key is remapped**, I want the hint to
  reflect my binding rather than the shipped default so I do not read a
  wrong instruction and give up.
- **As an implementer of a later epic slice (05/07/08)**, I want one
  documented helper pair to call so my slice does not have to invent
  wording, styling, or an expand-key convention. The vocabulary is fixed
  before my slice begins.
- **As a reviewer of a future diff renderer or grouped-read tree**, I want
  a grep-based regression test that fails if the retired literals reappear
  so drift is caught in the same commit that introduces it.

## Demoable Units of Work

### Unit 1: Shared truncation and expand-hint helpers

**Purpose:** Establish the one place every Monitor truncation indicator is
formatted, with correct pluralization, live-key derivation for the expand
hint, and consistent hint styling. This unit is entirely inside
`internal/tui/shared` and has zero Monitor coupling.

**Functional Requirements:**

- **FR-06.1** The shared package shall expose two head/tail pluralization
  helpers with the signatures `MoreItems(n int, singular, plural string) string`
  and `EarlierItems(n int, singular, plural string) string`. Each returns the
  ellipsis-prefixed count phrase using `singular` when `n == 1` and `plural`
  otherwise. The returned string is unstyled text; styling happens once at
  render time via the section-4 rule in FR-06.6.
- **FR-06.2** The shared package shall expose
  `ExpandHint(expanded, hasMore bool, keyHelp string) string` returning the
  empty string when `expanded == true`, when `hasMore == false`, or when
  `strings.TrimSpace(keyHelp) == ""`. When it returns a hint it shall wrap
  the effective key text in ASCII brackets and the fixed verb `Expand`,
  producing the literal `"[<key>: Expand]"` (for example `"[enter: Expand]"`).
- **FR-06.3** The shared package shall expose a small combinator
  `HintLine(more, hint string) string` (or an equivalent inline convention
  documented at the call site) that joins the count phrase and the expand
  hint with exactly one ASCII space when both are non-empty and yields the
  count phrase alone when the hint is empty. The combinator shall never
  emit a trailing separator, a leading space, or a double space.
- **FR-06.4** The helpers shall not perform any styling. Callers shall style
  the returned string exactly once by wrapping the whole hint line in
  `shared.Theme.Chat.Hint.Render(...)`. The helpers shall not import
  `lipgloss` at package scope; ASCII text is the entire output surface.
- **FR-06.5** `MoreItems`, `EarlierItems`, and `ExpandHint` shall each be
  covered by a table-driven test at `n = 0`, `n = 1`, `n = 2`, and one
  large `n` (for example `n = 240`), covering singular/plural switching,
  `expanded == true` short-circuit, `hasMore == false` short-circuit, empty
  `keyHelp` short-circuit, and the ASCII bracket contract. The pluralization
  helpers shall be independent of theme setup so the tests can run without
  constructing a `Model`.

**Proof Artifacts:**

- Test: `go test ./internal/tui/shared -run 'TestTruncation(MoreItems|EarlierItems|ExpandHint|HintLine)' -v` — a table-driven fixture exercises `n = 0/1/2/240`, both `expanded` states, both `hasMore` states, empty and non-empty `keyHelp`, and combinator behaviors. Demonstrates FR-06.1 through FR-06.5.
- Test: a source-shape assertion in the same file uses `os.ReadFile` on `internal/tui/shared/truncation.go` and asserts the file does not import `charm.land/lipgloss/v2` at package scope. Demonstrates FR-06.4.
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-1-shared-gallery.txt` records the helper output for representative inputs (`MoreItems(1,"line","lines")`, `MoreItems(240,"line","lines")`, `EarlierItems(3,"file","files")`, `ExpandHint(false,true,"enter")`, `ExpandHint(true,true,"enter")`, `ExpandHint(false,false,"enter")`, `ExpandHint(false,true," ")`) verbatim. The `.notes.txt` records the calling test's file and function so the artifact is reproducible.

### Unit 2: Retire the current Monitor phrasings and grep-lock the vocabulary

**Purpose:** Replace the four hand-rolled hint phrasings that draw inside the
Monitor's transcript presentation today with calls to the shared helpers, and
add a repository test that fails if the retired literals reappear in
`internal/tui/monitor`.

**Functional Requirements:**

- **FR-06.6** The following call sites in
  `internal/tui/monitor/monitor_transcript_items_view.go` shall emit their
  hidden-line notice through `shared.MoreItems(hidden, "line", "lines")`
  combined with `shared.ExpandHint(expanded, hidden > 0, m.keys.Toggle.Help().Key)`
  via the shared combinator, wrapped once in `shared.Theme.Chat.Hint.Render`:
  - the `writeItemDetail` `hidden > 0` branch, currently
    `fmt.Sprintf("… %d lines hidden", hidden)`;
  - the `writeNewCodeCards` `hidden > 0` branch, currently
    `fmt.Sprintf("… %d lines hidden", hidden)`.
  The write-time capture-truncation notice
  (`"… capture truncated at write"`) is a fixed labelled message rather than
  a quantitative indicator; this specification keeps its literal wording
  intact but routes it through `shared.Theme.Chat.Hint.Render` from a single
  shared helper `shared.CaptureTruncatedHint()` so the phrasing lives beside
  the other hint contracts. The `expandView` inner marker
  (`"… %d KB elided …"` in `monitor_transcript.go`) is out of scope per
  slice 06 (byte-elision inside a single blob is a distinct concept; see
  Non-Goals).
- **FR-06.7** The `writeItemDetail` and `writeNewCodeCards` call sites shall
  derive the expand-key text from the Monitor's live `Toggle` binding
  (`m.keys.Toggle.Help().Key`) so a rebind of that key produces the rebind
  in the rendered hint. No renderer shall hardcode the literal string
  `"enter"`, `"space"`, or `"o"` for this purpose.
- **FR-06.8** The Monitor package shall carry an executable regression test
  (a Go test using `os.ReadFile`, not a shell command) proving that after
  the change:
  - `internal/tui/monitor/*.go` (excluding `_test.go` files) contains no
    occurrence of the substring `"lines hidden"`;
  - no non-test file in the Monitor package contains a bare `"… "` string
    literal followed by a `%d` conversion outside of `expandView` (the KB
    elision marker's `"\n… %d KB elided …\n"` is the sole permitted
    exception and shall be exempted by an explicit allowlist in the test);
  - `writeItemDetail` and `writeNewCodeCards` each contain a call to
    `shared.MoreItems` and `shared.ExpandHint` (proving the retirement is
    complete rather than merely inline-renamed).
  The allowlist and the retired-literal list live inside the test file, so
  a reviewer can inspect both without reading a fixture.
- **FR-06.9** Existing tests that assert the retired literals shall be
  updated to the new wording in the same commit as the source retirement.
  This includes `monitor_transcript_card_test.go` (which today asserts
  `strings.Contains(expanded, "lines hidden")`), any
  `monitor_transcript_items_view_test.go` assertion that inspects the
  hidden-line notice, and any assertion that reads
  `"… capture truncated at write"` verbatim. No dual-wording compatibility
  branch shall be introduced.

**Proof Artifacts:**

- Test: `go test ./internal/tui/monitor -run 'TestTruncationVocabularyRetirement' -v` — the grep-based Monitor regression file described in FR-06.8. Demonstrates FR-06.6 (call-site retirement), FR-06.7 (live-key derivation is present at the two sites), FR-06.8 (grep lock).
- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailHiddenLinesHint|TestWriteNewCodeCardsHiddenLinesHint' -v` — behavioral fixtures render a bounded exchange whose detail exceeds `transcriptDetailRows`, assert the rendered body contains `"… 4 more lines [enter: Expand]"` (exact spacing) after `stripANSI`, and assert the hint disappears when the exchange is expanded and `hidden` becomes zero. Covers FR-06.1, FR-06.2, FR-06.3, FR-06.7.
- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailHintRespectsToggleRebind' -v` — a fixture that mutates `m.keys.Toggle` via `keybind.NewBinding(keybind.WithKeys("ctrl+o"), keybind.WithHelp("ctrl+o","expand"))`, re-renders, and asserts the hint reads `"[ctrl+o: Expand]"`. Demonstrates FR-06.7.
- Test: `go test ./internal/tui/monitor -run 'TestTranscriptCardPreservesHiddenLinesNotice|TestSyntheticExchangePageBoundedDetail' -v` — the existing card-cache regression whose 300-entry expand assertion moves from `"lines hidden"` to `"more lines"`, proving the retirement did not delete the notice's *presence*, only its wording (FR-06.9).
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-2-monitor-vocabulary.txt` — an 80-column synthetic exchange whose Input detail exceeds the row bound; ANSI capture shows the new hint line, and a `.notes.txt` records the `Toggle.Help().Key`, the item's `displayState`, and the terminal geometry.

### Unit 3: Anchor mode for boundTranscriptDetail and the live-tail indicator

**Purpose:** Change how running-step transcript detail is bounded so its
newest rows are pinned to the bottom (with a prepended earlier-lines
marker), keep settled-step behavior unchanged (head+tail with an appended
more-lines marker), and add the same shared-vocabulary indicator to the
Steps-panel live streaming output that already drops leading lines
silently.

**Functional Requirements:**

- **FR-06.10** `boundTranscriptDetail` in
  `internal/tui/monitor/monitor_transcript_detail.go` shall accept an
  explicit anchor argument via an exported enum
  `detailAnchor { detailAnchorHead, detailAnchorTail }` (package-private —
  the type is Monitor-internal). Its signature shall become
  `boundTranscriptDetail(content string, width int, anchor detailAnchor)
  (shown string, hidden int)`. Existing per-anchor semantics:
  - `detailAnchorHead` — preserve the current behavior: byte-limit first,
    then keep the first `transcriptDetailRows - transcriptDetailTailRows`
    rows and the final `transcriptDetailTailRows` rows, drop the middle,
    report the number of hidden rows.
  - `detailAnchorTail` — byte-limit first, then keep the final
    `transcriptDetailRows` rows and drop all earlier rows. `hidden` is the
    number of dropped earlier rows.
  The 4 KiB byte limit, the 12-row overall bound, and the 3-tail-row
  behavior for head anchoring shall not change values; only the anchor
  branch is new.
- **FR-06.11** `writeItemDetail` and `writeNewCodeCards` shall thread an
  anchor value through to `boundTranscriptDetail`. The anchor value shall
  be `detailAnchorTail` when the enclosing item's `displayState` is
  `toolDisplayRunning`, and `detailAnchorHead` in every other case
  (`toolDisplaySuccess`, `toolDisplayError`, `toolDisplayWarning`,
  `toolDisplayUnknownResult`, non-tool detail callers). The item's
  `displayState` shall reach the two writers as an explicit function
  parameter, not through a hidden model field, so the branch is testable
  without exercising the whole model.
- **FR-06.12** When `anchor == detailAnchorTail` and `hidden > 0`, the
  hidden-line notice shall be **prepended** to the section body using
  `shared.EarlierItems(hidden, "line", "lines")` combined with the
  slice-06 expand hint (see FR-06.6). When `anchor == detailAnchorHead`
  and `hidden > 0`, the notice shall be **appended** using
  `shared.MoreItems(...)` combined with the same hint. Prepend and append
  use the same six-space (or four-space for `writeNewCodeCards`) indent
  and the same `shared.Theme.Chat.Hint` styling as the surrounding body.
- **FR-06.13** The Steps-panel `writeStreamingOutput` in
  `internal/tui/monitor/monitor_steps.go` shall prepend a
  `shared.EarlierItems(dropped, "line", "lines")` notice (without an
  expand hint — there is no expand affordance for a Steps-panel
  streaming section) whenever the number of accumulated non-empty output
  lines exceeds `outputMaxLines`. The notice shall be styled with the
  same `shared.Theme.Question` hint style already used for the row
  header there, or with `shared.Theme.Chat.Hint` — the choice shall be
  recorded in the implementing task's proof notes so the color-token
  ownership is auditable; adding a new style token is out of scope for
  this slice.
- **FR-06.14** The rendered line count of a tool-exchange item shall stay
  reflected in `chatItemLineRanges` after the anchor change: adding a
  prepend line and removing (potentially) middle lines changes the total
  row count, so the range computation in `itemTranscriptBody` must remain
  correct. A regression test shall assert that expanded tool exchanges
  land on the intended item under `n`/`N` navigation with a running item
  present.
- **FR-06.15** Neither anchor mode shall change the transcript wire
  format, the `toolcall.Activity` contract, the durable
  `transcript.jsonl` content, or the write-time capture-truncated flag.
  The anchor change is a display projection; the durable record is
  authoritative regardless.

**Proof Artifacts:**

- Test: `go test ./internal/tui/monitor -run 'TestBoundTranscriptDetail(HeadAnchor|TailAnchor|BothPreserveUTF8)' -v` — a table-driven fixture exercises 16-row content with a distinctive first row and a distinctive last row at width 80. Head-anchor asserts the first `12-3` rows and the last `3` rows survive with `hidden == 4`. Tail-anchor asserts the last `12` rows survive with `hidden == 4` and the distinctive last row is present. Both cases assert `utf8.ValidString(shown)`. Extends `monitor_transcript_detail_test.go` (which currently only covers head+tail). Demonstrates FR-06.10.
- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailAnchorFromDisplayState' -v` — table-driven fixture over `toolDisplayRunning`, `toolDisplaySuccess`, `toolDisplayError`, `toolDisplayWarning`, `toolDisplayUnknownResult`. Each row asserts (a) the anchor selection is correct, (b) the marker is prepended for the tail case and appended for the head case, and (c) the shared vocabulary is used. Demonstrates FR-06.11, FR-06.12.
- Test: `go test ./internal/tui/monitor -run 'TestWriteStreamingOutputEarlierLinesIndicator' -v` — a Steps-panel fixture that seeds a `stepOutput` buffer with more than `outputMaxLines` non-empty rows for a `StatusRunning` step, invokes the Steps view, and asserts the rendered pane contains `… N earlier lines` on the row immediately before the first tail row, and that the tail rows are the newest `outputMaxLines` non-empty rows. Demonstrates FR-06.13.
- Test: `go test ./internal/tui/monitor -run 'TestChatItemLineRangesStableAcrossAnchorMode' -v` — renders a synthetic exchange twice (running vs. success `displayState`) and asserts `chatItemLineRanges` covers the whole item body in both cases so `n`/`N` navigation lands on the intended anchor row. Demonstrates FR-06.14.
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-3-anchor-gallery.txt` shows three cards at 80 columns: (a) a settled tool exchange with a head-anchored detail and an appended `… N more lines [enter: Expand]` marker, (b) a running tool exchange with a tail-anchored detail and a prepended `… N earlier lines [enter: Expand]` marker, (c) the Steps-panel streaming output with a prepended `… N earlier lines` marker. The `.notes.txt` records the `displayState` values, `Toggle.Help().Key`, and terminal geometry.

## Non-Goals (Out of Scope)

1. **Reworking `expandView`'s KB-elision marker** (`"\n… %d KB elided …\n"`
   at `monitor_transcript.go:676`). That marker reports a *byte* elision
   inside a single blob, not a hidden-rows count; its wording is already
   clear, and unifying it with the row-count vocabulary would obscure a
   different concept. The grep-lock in FR-06.8 exempts it via an explicit
   allowlist.
2. **Retuning the 4 KiB, 12-row, or 3-tail-row detail budgets.** Budget
   changes belong with the slices that render into cards (05, 07, 08).
   This slice only adds an anchor selector; the numbers remain untouched.
3. **A general keybinding-hint framework.** `ExpandHint` reads a single
   key-help string from the caller. There is no attempt to build a
   registry of "actions that have expand hints"; slice 06 solves the
   truncation-vocabulary case and stops there.
4. **Retiring the two-space unselected gutter or changing prefix width.**
   Selection prefix width is slice 03's contract (see
   `docs/specs/25-spec-selection-affordance`); this slice consumes the
   two-cell prefix without redesigning it.
5. **Changing the page markers** (`[` load older, `]` load newer). Slice
   00 removed the render-plan-era on-screen "── earlier messages
   available ──" text; only the key bindings remain. No page-marker
   wording is in scope, and no page-marker vocabulary shall be
   reintroduced by this slice.
6. **Owning the wording used by later epic slices' bespoke bodies** (05
   tool-detail sections, 07 diff rendering, 08 grouped reads). This
   slice publishes the helpers; each consumer's spec calls them.
7. **Consolidating the Security-pane "`… %d more finding`" phrasing in
   `monitor_view.go:112`.** The Security pane is not the Transcript
   panel; a unified security-pane vocabulary is out of scope until the
   Security-pane redesign is scheduled.
8. **The Steps panel's row header, layout, or scroll behavior.** Only
   the streaming-output tail's *dropped-lines indicator* is in scope
   (FR-06.13); nothing else on the Steps panel changes.

## Design Considerations

### Where the wording lives today

Slice 06's epic slice document enumerates the sites the render-plan
removal (slice 00) has left behind:

| Site | Wording today | File:line |
|---|---|---|
| tool detail (`writeItemDetail`) | `… %d lines hidden` | `monitor_transcript_items_view.go:241` |
| new-code card (`writeNewCodeCards`) | `… %d lines hidden` | `monitor_transcript_items_view.go:307` |
| write-time capture truncation | `… capture truncated at write` | `monitor_transcript_items_view.go:127` |
| KB-elision inside `expandView` | `… %d KB elided …` | `monitor_transcript.go:676` |

The page markers referenced in the slice document
(`── earlier messages available · [ load older ──`) are already gone.
The slice's "live tail path" reference to
`monitor_transcript.go:849-859` is stale — that block is in the removed
render-plan region. The surviving live-tail affordance is the
Steps-panel streaming output in `monitor_steps.go:238-262`, which
already keeps the last `outputMaxLines` non-empty rows but prints no
indicator for what it dropped.

### The wrong-end problem

`boundTranscriptDetail` keeps a head + tail with the middle elided:

```
headRows := transcriptDetailRows - transcriptDetailTailRows   // 12 - 3 = 9
kept := append([]string{}, rows[:headRows]...)
kept = append(kept, rows[len(rows)-transcriptDetailTailRows:]...)
```

For a *settled* result this is a defensible choice — the settled head
usually is the informative part. For **live streaming output** the
operator wants the newest rows: a 9-row head of a growing log goes
stale by definition. The fix is an explicit anchor mode; the recorded
per-anchor policy is:

- **Settled** (`toolDisplaySuccess`, `toolDisplayError`,
  `toolDisplayWarning`, `toolDisplayUnknownResult`) → head anchor,
  appended `… N more lines`.
- **Running** (`toolDisplayRunning`) → tail anchor, prepended
  `… N earlier lines`.

An enum keeps future modes (for example, `detailAnchorMiddle` for a
grouped-read body) additive without changing existing call sites.

### The expand hint

`m.keys.Toggle.Help().Key` is the per-item `Toggle` binding's live
key. Per epic decision CC-11, jig keeps the per-item toggle (`enter`
/ `space`) *and* the expand-all (`o`); the hint should advertise the
per-item key because it acts on the focused item. Using
`Help().Key` (the primary key that the Charm bindings expose in the
footer) yields `enter` for the default binding; if an operator
rebinds `Toggle`, the hint follows the rebind.

The expand hint's bracket glyphs are ASCII `[` and `]` rather than
omp's `⟦⟧`. ASCII brackets need no glyph-preset entry (slice 14) and
match omp's own ASCII-preset test assertion (`"[Ctrl+O: Expand]"`),
so the choice does not create migration debt for later slices.

### One vocabulary, honestly picked

omp is internally inconsistent about truncation wording: at least
five phrasings coexist (`… N more lines`, `… +N more`, `…
(N earlier lines)`, `… N more`, bare `…`), and
`default-renderer.ts:134` hardcodes `more lines` without pluralizing.
This slice picks:

- **Head anchor**: `"… <n> more <singular|plural>"` (appended).
- **Tail anchor**: `"… <n> earlier <singular|plural>"` (prepended).

with a single space, one ASCII ellipsis (`…`), correct pluralization
via `singular`/`plural` arguments (`"line"/"lines"`, `"file"/"files"`,
`"match"/"matches"` are the initial expected callers), and no
alternate short forms.

### Cache identity is unaffected

`transcriptRenderKey` includes `state`, so an item whose
`displayState` transitions from `running` to `success` already
produces a new key and evicts the previous cache entry. The anchor
change piggybacks on the existing state-based invalidation; no new
cache key component is required. Selection changes still discriminate
on the header slot (FR-02.18) but not on the anchor.

### omp reference — the pieces this slice ports

`packages/coding-agent/src/tools/render-utils.ts:295-298` is omp's
canonical `formatMoreItems(remaining, itemType)`; jig's
`MoreItems`/`EarlierItems` port the shape (ASCII ellipsis, singular
switch on `n === 1`, prefix rather than suffix ellipsis) into Go
without importing the pluralization library.

`render-utils.ts:276-280` is `formatExpandHint(theme, expanded,
hasMore)`; jig's `ExpandHint` ports the two never-lie invariants
(empty when expanded, empty when nothing hidden) and swaps omp's
theme.fg(dim) wrap for jig's convention of styling once at the
render site.

`tui/code-cell.ts:165-176` is the omp source for
"prepend for tail, append for head." Jig's
`writeItemDetail`/`writeNewCodeCards` port the position rule while
staying inside the existing indent contract (six spaces for
`writeItemDetail`, four for `writeNewCodeCards`).

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md),
  [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md),
  [Testing](../../TESTING.md), and the domain terms in
  [CONTEXT.md](../../../CONTEXT.md).
- New shared code lives in `internal/tui/shared`; the shared package
  must not import `internal/tui/monitor` (ARCHITECTURE.md; the
  helpers accept the key-help string as a parameter rather than
  reading the Monitor's binding table).
- Style tokens come from `shared.Theme.Chat.Hint` (already defined,
  `fgDim` foreground). Do not introduce a new package-level
  `var ...Style = lipgloss.NewStyle()` or a new hex constant in
  either package (`AGENTS.md`; `TUI.md`).
- Pre-v1 discipline: delete each retired literal in the same commit as
  its replacement. Do not gate the new wording behind an environment
  variable, feature flag, or dual code path (`AGENTS.md`).
- Cell measurement uses `lipgloss.Width` after `stripANSI`. Do not
  count runes or bytes to reason about visible width (`TUI.md`).
- Table-driven synthetic transcript fixtures (`syntheticExchange`,
  `newMonitorWithSteps`, `setChatPage`) drive Monitor tests; the
  Steps-panel test builds a synthetic `Model.stepOutput` entry. Do
  not read real `.jig/` data for fixtures or proofs (`AGENTS.md`;
  `TESTING.md`).
- Format only changed Go files with `gofmt -w`. Run the change-specific
  checks in `docs/TESTING.md` and the root acceptance commands
  (`go build ./cmd/jig`, `go test ./...`, `go vet ./...`,
  `git diff --check`, plus `go test -race ./internal/tui/...` for
  the TUI package touched by Unit 2 and Unit 3). The nested ACP
  module is unaffected (no imports cross that boundary).

## Technical Considerations

### Package layout

- `internal/tui/shared/truncation.go` — the three exported helpers and
  their combinator, string-only, no `lipgloss` import.
- `internal/tui/shared/truncation_test.go` — table-driven pluralization
  and short-circuit tests plus the source-shape assertion.
- `internal/tui/monitor/monitor_transcript_detail.go` — introduce
  `detailAnchor` (unexported), extend `boundTranscriptDetail` signature.
- `internal/tui/monitor/monitor_transcript_detail_test.go` — extend the
  existing UTF-8 and byte-first cases with head- and tail-anchor
  fixtures.
- `internal/tui/monitor/monitor_transcript_items_view.go` — thread the
  anchor through `writeItemDetail` and `writeNewCodeCards`, replace
  both `… %d lines hidden` sites, route the write-capture literal
  through `shared.CaptureTruncatedHint()`, and use the shared expand
  hint. `writeToolActivityDetails` calls `writeItemDetail`; the
  anchor decision uses `item.displayState`.
- `internal/tui/monitor/monitor_transcript_items_view_test.go` — add
  the behavioural fixtures listed in Unit 2 and Unit 3 proofs.
- `internal/tui/monitor/monitor_steps.go` — prepend the
  `EarlierItems` notice inside `writeStreamingOutput` when
  `len(recent) > outputMaxLines` after the accumulation, before the
  loop that prints the tail rows.
- `internal/tui/monitor/monitor_steps_test.go` (may not yet exist as a
  focused file) — add the Steps-panel streaming-output regression.
- `internal/tui/monitor/monitor_transcript_vocabulary_test.go` (new) —
  the grep-lock regression from FR-06.8, living in its own small file
  so the retired-literals allowlist is easy to review.

### Signature and enum choice

`boundTranscriptDetail` is only called from two Monitor writers today,
so adding a new argument is a tractable in-file rename. The enum uses
a package-private type:

```
type detailAnchor int

const (
    detailAnchorHead detailAnchor = iota
    detailAnchorTail
)
```

An enum (rather than a bool) makes future additions
(`detailAnchorMiddle`, `detailAnchorLastError`) additive; it also
reads at the call site (`boundTranscriptDetail(text, w, detailAnchorTail)`)
rather than requiring the reader to remember which bool argument is
which.

### Live-key derivation

The Monitor already exposes its bindings on `m.keys` (see
`internal/tui/monitor/keys.go`). `m.keys.Toggle` is the per-item
toggle binding, and `Help()` returns the key's display text. The
call site therefore threads `m.keys.Toggle.Help().Key` into
`ExpandHint`:

```
hint := shared.ExpandHint(expanded, hidden > 0, m.keys.Toggle.Help().Key)
line := shared.Theme.Chat.Hint.Render(shared.HintLine(shared.MoreItems(hidden, "line", "lines"), hint))
b.WriteString("      " + line + "\n")
```

The rebind test proves that mutating `m.keys.Toggle` via a fresh
`keybind.NewBinding(...)` yields the new key text in the rendered
hint. The shared helpers never touch `keys`; the coupling stays at
the Monitor's writer.

### The Steps panel edge

`writeStreamingOutput`'s current shape:

```
if len(recent) > outputMaxLines {
    recent = recent[len(recent)-outputMaxLines:]
}
b.WriteString("\n  " + shared.Theme.Question.Render("▸ "+s.id) + "\n")
for _, l := range recent {
    b.WriteString("    " + l + "\n")
}
```

The change: capture `dropped := len(recent) - outputMaxLines` before
the slice, then, after the row header and before the loop, prepend

```
b.WriteString("    " + shared.Theme.Chat.Hint.Render(shared.EarlierItems(dropped, "line", "lines")) + "\n")
```

when `dropped > 0`. No expand affordance is offered here (the Steps
panel has no per-item toggle for a streaming buffer), so
`ExpandHint` is not called.

### Charm dependency versions

No new dependencies. `keybind` bindings are already used everywhere
in the Monitor; `lipgloss` is used only at the Monitor call sites,
not in the shared helper.

### Deliberate deviation from external guidance

omp's helper is TypeScript and uses `Number.isFinite` guards for
`Infinity`/`NaN`; the Go port takes an `int`, so those guards are
unnecessary. omp uses the theme to wrap the hint in a dim
foreground; jig wraps at the render site via
`shared.Theme.Chat.Hint.Render`, matching jig's existing convention
(styles owned by `internal/tui/shared/styles.go`, never at the
helper).

## Security Considerations

Truncation vocabulary is presentation-only: the helpers format
integers and static text, and the anchor mode is a display projection.
No new untrusted input surface is introduced. Durable
`transcript.jsonl` content is not modified; the write-time
capture-truncated flag (`block.Truncated`) is unchanged. Proofs use
fabricated fixtures (`syntheticExchange`, synthetic stepOutput
buffers) — never real `.jig/` data — per repository standards.

## Success Metrics

1. The three shared helpers exist in `internal/tui/shared/truncation.go`
   with the signatures specified in FR-06.1–FR-06.3 and are covered
   by the table-driven fixture from FR-06.5. The source-shape
   assertion (no `lipgloss` import) passes.
2. `internal/tui/monitor` contains no occurrence of `"lines hidden"`
   in non-test source files, and the retired-literal grep-lock test
   (FR-06.8) passes on the changed tree.
3. `m.keys.Toggle`'s `Help().Key` value flows through into the
   rendered hint. Rebinding it in a test changes the rendered hint
   without editing any Monitor writer (FR-06.7).
4. `boundTranscriptDetail(content, width, detailAnchorTail)` returns
   the last `transcriptDetailRows` rows with `hidden` equal to the
   dropped-earlier-lines count; `detailAnchorHead` produces the
   pre-slice-06 head+tail with the same 4 KiB byte cap. Both cases
   remain valid UTF-8 (FR-06.10).
5. A running tool exchange's rendered detail contains the prepended
   `… N earlier lines [enter: Expand]` marker on the row immediately
   above the newest content; a settled exchange's detail contains
   the appended `… N more lines [enter: Expand]` marker on the row
   immediately below the last content row (FR-06.11, FR-06.12).
6. The Steps-panel streaming output for a running step whose
   captured non-empty lines exceed `outputMaxLines` shows a
   prepended `… N earlier lines` marker; the tail rows are the
   newest `outputMaxLines` (FR-06.13).
7. `chatItemLineRanges` for a running expanded tool exchange remains
   internally consistent so `n`/`N` navigation lands on the intended
   item (FR-06.14).
8. `go build ./cmd/jig`, `go test ./...`, `go vet ./...`,
   `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`
   (empty), and `git diff --check` all pass on the recorded
   validation machine. These are implementation acceptance targets,
   not results claimed by this Phase 1 document.

## Open Questions

The following are non-blocking. Each is recorded so an implementer or
reviewer can act on it deterministically without re-deriving the
answer.

1. **Q-06.1** Should the expand hint advertise the per-item `Toggle`
   key (`enter`) or the `ExpandAll` key (`o`)? *Recommendation:
   per-item — the focused affordance is the item under the cursor,
   and `enter` is more discoverable than `o`. The helper accepts a
   key string, so a future consumer that wants `o` can pass it
   without changing the helper.*
2. **Q-06.2** Should settled output also become tail-anchored, or is
   head+tail correct for a completed step? *Recommendation: keep
   head+tail when settled. The beginning of a completed result is
   usually the informative part; the head-plus-3-tail contract
   preserves both a summary and a final-status view.*
3. **Q-06.3** Should the Steps-panel streaming indicator use
   `shared.Theme.Chat.Hint` (dim foreground) or `shared.Theme.Question`
   (the existing color of the panel's `"▸ <step id>"` header)?
   *Recommendation: `Chat.Hint`, so the drop notice is quieter than
   the section header. The implementer records the final choice in
   the Task 3 proof notes so a future maintainer can audit both.*
4. **Q-06.4** Should the write-time capture-truncated hint also
   surface an expand key? *Recommendation: no — capture truncation
   is a wire-level fact about the durable record; there is nothing
   further to reveal. `CaptureTruncatedHint()` is therefore a
   fixed-wording helper without a hint slot.*
