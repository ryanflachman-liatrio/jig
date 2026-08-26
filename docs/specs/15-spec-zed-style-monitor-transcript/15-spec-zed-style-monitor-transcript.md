# 15-spec-zed-style-monitor-transcript.md

## Introduction/Overview

The Run Monitor currently presents durable transcript blocks as a dense event log:
tool calls and results receive repeated labels, bars, previews, and nested group
controls that compete with assistant prose. This feature replaces that presentation
with a conversation-first Transcript panel inspired by the hierarchy of Zed's
Agent Panel, while retaining jig's terminal-first, keyboard-first interaction
model and durable transcript format.

The primary goal is to make a step's conversation easy to scan while preserving
the evidence an operator needs to inspect tool inputs, outputs, reasoning,
failures, retries, and Gate context. The Monitor will normalize only the loaded,
bounded page into stable Transcript items; it will not change runner behavior,
the JSONL schema, or engine events.

## Goals

- Make user guidance and assistant prose visually primary in the per-step
  Transcript panel.
- Represent a matched tool use and result as one quiet, expandable Tool exchange.
- Keep incomplete, malformed, and page-edge transcript data inspectable without
  inventing success, failure, or provenance.
- Preserve bounded reads, paging, search/filter behavior, follow behavior, resize
  state, and Gate-context restoration.
- Bound every expanded detail by captured bytes and rendered terminal rows.

## User Stories

- **As a workflow operator**, I want routine agent tool activity summarized in a
  compact list so I can understand the conversation without reading an event log.
- **As a workflow operator**, I want one action to reveal a tool call's input and
  output so I can inspect evidence when an activity matters.
- **As a workflow operator**, I want failures and incomplete activity to be
  explicit but conservative so I do not mistake missing transcript data for a
  completed operation.
- **As a workflow operator**, I want search, filtering, paging, and Gate jumps to
  preserve my selected Transcript item so I can investigate a long-running step
  without losing context.

## Demoable Units of Work

### Unit 1: Normalize loaded transcript pages into stable items

**Purpose:** Convert durable transcript blocks into the page-local presentation
units that make conversation-oriented rendering and interaction reliable.

**Functional Requirements:**

- The system shall build immutable Transcript items only from the currently loaded
  bounded transcript page and its existing bounded edge context.
- The system shall pair non-empty-ID tool uses and results only when their tool ID,
  generation, iteration, and attempt all match; duplicate IDs shall pair FIFO.
- The system shall anchor a matched Tool exchange at its tool-use position, so
  out-of-order results enrich the correct row without changing activity order.
- The system shall render a result without a loaded matching use as a result-only
  item with unknown origin, and a use without a loaded matching result as pending
  only while its step runs and unknown afterwards.
- The system shall retain unknown roles and block types as quiet, expandable
  unsupported items rather than omitting them or panicking.
- The system shall insert generation, iteration, and retry dividers only for
  visible execution-coordinate transitions; a first visible item from a later
  coordinate shall retain its divider.

**Proof Artifacts:**

- Test: table-driven item-construction tests demonstrate scoped FIFO pairing,
  duplicate IDs, out-of-order results, page-edge use-only/result-only items, and
  unknown block preservation.
- Test: page-local boundary tests demonstrate that the Monitor never performs an
  unbounded history scan to repair an incomplete exchange.

### Unit 2: Render a quiet, inspectable conversation transcript

**Purpose:** Replace event-log chrome with a readable hierarchy that makes prose
and meaningful state more prominent than routine activity.

**Functional Requirements:**

- The system shall render `RoleUser` plus `BlockText` as Markdown User guidance in
  a subtle user surface, distinct from role-user tool-result blocks.
- The system shall render assistant text as themed Markdown without normal role,
  sequence, timestamp, colored-bar, or full-label cursor chrome.
- The system shall render thinking, Tool exchanges, result-only items, system
  output, terminal result errors, and unsupported content as one-level
  disclosures when they have inspectable details.
- The system shall render a matched successful Tool exchange as one collapsed
  semantic row and shall never render its matching result as a second routine row.
- The system shall render a failed Tool exchange or failed result-only item with
  a text-and-glyph error indication and a sanitized useful error hint before
  expansion; color alone shall not convey failure.
- The system shall reveal both Input and Output sections through one Tool exchange
  expansion, pretty-print valid JSON input, and keep tool output and command
  output verbatim even when they resemble Markdown or JSON.
- The system shall use a semantic, presentation-only tool classifier with safe
  fallback labels for unknown or malformed tool calls; classification shall not
  affect execution, security, or permissions.
- The system shall preserve selected/state marker, action, and disclosure marker
  before optional detail on narrow terminals, clipping detail by visible terminal
  cell width.

**Proof Artifacts:**

- ANSI-stripped snapshot: a dense transcript demonstrates prose-first hierarchy,
  one successful row per Tool exchange, absence of old group/bar chrome, and
  readable non-color state markers.
- Test: expanded rendering tests demonstrate one action exposes paired Input and
  Output, successful output remains hidden by default, and failed output remains
  discoverable.
- Test: narrow-width and wide-rune assertions demonstrate every emitted row fits
  the Transcript inner width.

### Unit 3: Preserve search, navigation, live follow, and Gate context

**Purpose:** Move Monitor interactions from raw blocks and Spec 11 groups to
stable Transcript-item identity without losing existing investigation workflows.

**Functional Requirements:**

- The system shall search all member content of a Tool exchange, including raw
  name, semantic summary, input, output, roles, and execution coordinates, while
  retaining the matched exchange as one item.
- The system shall allow `role:user` to retain a Tool exchange through a
  role-user result while continuing to render that item as tool activity rather
  than User guidance.
- The system shall make each expandable Transcript item a single navigation
  target; Enter and Space shall toggle it, and the existing expand-all override
  shall not erase individual expansion choices.
- The system shall restore selection by stable item key after a same-step reload,
  filtering, expansion, and width change; when a key is absent, it shall select
  the nearest remaining visible item deterministically.
- The system shall pause live follow after manual activity navigation or
  expansion, retain bounded page state, and preserve existing live-tail behavior.
- The system shall snapshot and restore selected item, expansion choices, search,
  filters, scroll, follow, page position, and seen sequence during a Gate context
  jump without map aliasing.

**Proof Artifacts:**

- Test: input-only and output-only searches demonstrate navigation to and
  expansion of the owning Tool exchange.
- Test: navigation, resize, reload, and follow tests demonstrate stable item keys
  and bounded page-local state.
- Test: Gate round-trip tests demonstrate deep-cloned state restoration and safe
  fallback when a saved item is no longer loaded.

### Unit 4: Bound details and complete the presentation migration

**Purpose:** Ensure expanded transcript evidence remains useful in small terminal
viewports and remove the obsolete two-level group renderer completely.

**Functional Requirements:**

- The system shall bound expanded thinking, tool input, tool output, command
  output, and result-error content to 4,096 display bytes, 12 rendered rows, and
  a three-row tail while preserving UTF-8 boundaries and final error/status lines.
- The system shall report hidden line counts and capture-time truncation honestly;
  it shall not claim an unavailable original size.
- The system shall use semantic styles from the shared theme singleton, with thin
  detail guides and a narrow selected marker instead of type-colored thick bars
  and full-background block cursors.
- The system shall invalidate only width-dependent Markdown/detail caches on
  resize and shall distinguish cache surfaces so input and output cannot collide.
- The system shall remove obsolete Spec 11 tool-group state, rendering branches,
  tests, and help terminology in the same change.
- The system shall continue to show the graceful persistence-off placeholder and
  shall perform no transcript file I/O when the run directory is empty.

**Proof Artifacts:**

- Test: byte, row, UTF-8, and tail-preservation cases demonstrate the explicit
  detail bounds at narrow and normal Transcript widths.
- Test: cache-surface, persistence-off, and obsolete-group regression tests
  demonstrate safe migration behavior.
- Command output: focused monitor tests, full test suite, formatting, vet, and
  build output demonstrate repository conformance.

## Non-Goals (Out of Scope)

1. **Transcript and execution changes**: This feature does not change
   `internal/transcript` JSONL format, persistence behavior, runner, harness,
   engine events, workflow TOML, or backend selection.
2. **Graphical-IDE replication**: This feature does not add mouse controls,
   hover-only affordances, gradients, shadows, proportional type, inner scroll
   views, clickable source navigation, image/resource rendering, or inline
   permission controls.
3. **Unbounded or diagnostic features**: This feature does not index a complete
   transcript, scan historical pages to resolve pairings, add a metadata-mode key
   for sequence/timestamp fields, or automatically fold activity bursts.

## Design Considerations

The Monitor remains a dark, terminal-first Bubble Tea surface. User guidance and
assistant prose are the reading priority; failures and pending activity are next;
routine activity and reasoning are quieter. Icons must be accompanied by text or
layout so ANSI-stripped output remains understandable.

Each routine Tool exchange is one semantic row: icon, action, compact detail, and
an optional disclosure marker. There are no outer `N tool calls` groups, separate
successful-result rows, thick colored role bars, or full-label selection
backgrounds. A selected activity uses a narrow marker. Expanded details use muted
section labels and a thin guide. Spacing is determined from adjacent item kinds,
so consecutive activities do not acquire blank separator rows.

The feature adapts interaction hierarchy—not pixel-level styling—from Zed's
Agent Panel, which presents streaming tool activity and separates review-oriented
file changes from conversation flow. Zed also documents that features vary with
external-agent integration; jig therefore uses only the durable transcript fields
it actually owns and avoids speculative tool states. [Zed Agent Panel](https://zed.dev/docs/ai/agent-panel)

## Repository Standards

- Keep the transcript contract intact: file is truth and the event bus is
  liveness; persistence-off with an empty run directory must remain graceful.
- Keep engine free of runner/harness concerns and keep this feature within
  `internal/tui/monitor` and `internal/tui/shared` presentation boundaries.
- Use the singleton in `internal/tui/shared/styles.go`; do not introduce ad hoc
  `lipgloss.NewStyle()` calls in monitor rendering files.
- Use comments only to explain non-obvious invariants, especially scoped
  correlation and bounded page-edge behavior.
- Follow existing Go formatting, focused model-driven TUI test patterns, and run
  `go test ./...`, `gofmt -l -w .`, `go vet ./...`, and `go build ./cmd/jig`.
- Preserve user-owned worktree changes; do not reset or overwrite unrelated
  modifications in Monitor files.

## Technical Considerations

The implementation should separate loading/paging/follow orchestration from pure
item normalization and view rendering. The normalized item model owns scoped tool
correlation, state inference, search/filter atomicity, item identity, spacing,
and bounded-detail policy. Rendering consumes those values, uses surface-aware
caches, and keeps non-prose data out of Glamour.

The page and state maps must remain proportional to the loaded window. A resize
must preserve semantic state and invalidate width-dependent render caches only;
it must not rebuild pairings or discard page/search/Gate state. All row clipping
must use terminal cell width rather than rune count.

Latest-standards research completed:

- Zed's living Agent Panel documentation was consulted on 2026-08-26. It confirms
  the intended reference hierarchy—streaming tool indicators, thread navigation,
  external-agent capability variation, and separate change-review surfaces. The
  feature deliberately adapts those ideas to a keyboard terminal rather than
  emulating IDE controls. [Zed Agent Panel](https://zed.dev/docs/ai/agent-panel)
- The current Go package documentation for Glamour v2 was consulted on
  2026-08-26; it lists a tagged v2 package release published 2026-06-12. Jig uses
  its existing pinned Charm v2 stack and themed renderer rather than adding a new
  rendering dependency. [Glamour v2 package documentation](https://pkg.go.dev/charm.land/glamour/v2)

## Security Considerations

The Monitor renders durable agent and command content, which can include sensitive
paths, command output, and tool arguments. This feature must not send transcript
content outside the local process, add credentials, or broaden backend access.
Collapsed summaries must sanitize control characters and malformed argument data;
expanded command/tool output remains verbatim but must not be interpreted as
terminal layout or Markdown. Proof artifacts and ANSI snapshots must not commit
real tokens, credentials, or sensitive transcript output.

## Success Metrics

1. **Tool density**: every matched successful use/result pair occupies one default
   semantic activity row, with no outer group or separate success-result row.
2. **Evidence access**: one expansion action reveals both input and output, and
   all expanded content respects the 4,096-byte/12-row/3-tail-row contract.
3. **Interaction preservation**: focused tests pass for paging, search/filter,
   navigation, resize, follow, Gate restoration, and persistence-off; the full
   repository test suite, formatter, vetter, and build pass.

## Open Questions

No open questions at this time. The initial detail bounds and glyph choices are
explicitly testable; later visual tuning may refine them only with updated tests
and ANSI snapshots, without changing the feature's architecture or scope.
