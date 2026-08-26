# mobile support.md

## Introduction/Overview

jig's existing terminal user interface is designed around a full-width desktop
terminal, but operators also use it in narrow terminals such as Termux. At
small widths, panels, status information, gates, and streaming output can
collide, wrap against stale dimensions, or become difficult to reach. This
feature adds a tested cell-width-responsive presentation contract across the
existing TUI surfaces without changing workflow execution, transcript meaning,
or persistence.

The primary goal is graceful degradation: every supported surface remains
usable at narrow widths, and below the minimum tested width it remains safe and
predictable rather than overflowing or silently dropping the operator's current
state. The feature also makes the display boundary safe for untrusted agent,
tool, and command content by removing terminal control sequences and
hyperlinks before that content is rendered.

## Goals

- Make the selector, detail, runs, monitor, and standalone chat surfaces
  cell-width responsive, with deterministic rendering checks at fixed widths
  including at least 120, 80, 60, 40, and a below-minimum width.
- Preserve the operator's logical state across `WindowSizeMsg` events and
  streaming updates: selection, focus, scroll position or anchor, gate drafts,
  approvals, current output, and streaming lifecycle must remain usable after a
  resize.
- Guarantee that navigation, status, pending gates and approvals, current
  output, and security context remain visible or reachable on every surface;
  narrow layouts may stack, compact, scroll, or show one focused panel at a
  time.
- Sanitize untrusted displayed content at the final rendering boundary so
  control sequences, OSC hyperlinks, and other terminal escape payloads cannot
  change the terminal or impersonate jig UI, while leaving existing transcript
  persistence and persistence-off behavior unchanged.
- Add deterministic UI, width/Unicode, lifecycle, security, race, and critical
  journey evidence without changing the Go baseline or upgrading dependencies
  unless an implementation blocker is separately approved.

## User Stories

- As an operator using jig in a narrow terminal, I want the current screen and
  its navigation hints to remain usable so that I can drive a workflow without
  switching to a desktop terminal.
- As an operator watching a run, I want step status, the selected step's
  current output, gates, approvals, and security findings to remain visible or
  reachable so that a compact terminal does not hide an important decision.
- As a reviewer answering a gate, I want the gate, its draft input, and the
  surrounding step context to survive resizing so that I do not lose work or
  approve the wrong step.
- As an operator watching a streaming agent, I want a resize during streaming
  to preserve the live session and current output so that resizing is only a
  presentation change.
- As a security-conscious user, I want command output and agent-provided text
  rendered as inert terminal content so that hostile escape sequences cannot
  alter my terminal or mimic trusted interface elements.
- As a maintainer, I want responsive behavior expressed through direct model
  tests and width invariants so that regressions can be diagnosed without a
  physical Android device or an interactive terminal session.

## Demoable Units of Work

### Unit 1: Responsive single-surface layout contract

**Purpose:** Make the selector, detail, runs, and standalone chat surfaces
adapt their content to terminal cell width, with stable chrome and reachable
content at narrow sizes.

**Functional Requirements:**

- The system shall use the terminal's cell width, not byte length or rune count,
  for layout, truncation, wrapping, panel sizing, and overflow decisions.
- The system shall recompute all affected component dimensions from each
  `WindowSizeMsg`; no viewport, markdown renderer, cached line, or wrapped body
  may continue using a stale width after the message is handled.
- The selector shall retain a visible workflow cursor, filtering/navigation
  controls, and a footer or equivalent reachable hint at every tested width.
  Its list may compact or scroll vertically, but a narrow width shall not
  disable selection or filtering.
- The detail surface shall retain the workflow identity, step/status content,
  and navigation back to the surrounding flow at every tested width. Wide
  multi-column content shall stack or become scrollable rather than render
  beyond the terminal edge.
- The runs surface shall retain run identity and status, the selected run, and
  navigation/scroll controls at every tested width. Long rows shall be
  truncated or wrapped using cell width without changing their underlying data.
- The standalone chat surface shall keep the message history and input region
  reachable at every tested width. The input region shall not be pushed off
  screen by a long message, and a streaming response shall continue to update
  while the layout is resized.
- Every single-surface layout shall reserve space for its footer or equivalent
  navigation hint before sizing the main body. The footer shall not be clipped
  by the bottom edge at the tested heights.
- When a width is too small for the preferred layout, the system shall switch to
  a documented compact layout or vertical scroll path. It shall never pass a
  negative or impossible dimension to a component and shall never emit a line
  wider than the terminal width.
- The implementation shall preserve the current theme singleton and existing
  panel/focus styling conventions. New styles shall be added to the shared TUI
  theme rather than created as ad-hoc colors at call sites.

**Proof Artifacts:**

- **Rendering fixture:** fixed-width renders of selector, detail, runs, and
  chat at the width matrix demonstrate that each surface retains its identity,
  navigation, status, and usable content without horizontal overflow.
- **Model test:** direct `Update`/`View` tests demonstrate that resize messages
  rebuild component dimensions and preserve cursor, selected item, focus,
  scroll anchor, draft input, and streaming state.
- **Width invariant test:** a table containing ASCII, CJK, emoji, combining
  marks, long unbroken tokens, and ANSI-styled trusted chrome demonstrates that
  every rendered line is within the requested cell width.

### Unit 2: Narrow run monitor with gates and security context

**Purpose:** Define the compact behavior for the highest-risk surface: the run
monitor, where step navigation, transcript output, human approvals, and
security findings compete for limited terminal space.

**Functional Requirements:**

- The monitor shall retain a reachable step list, selected-step transcript,
  step/run status, footer navigation, and any pending gate at every tested
  width.
- In the preferred width, the monitor may render its existing multi-panel
  layout. When both panels cannot satisfy their configured minimum widths, it
  shall use a deterministic narrow layout: show the focused panel full-width or
  stack the panels according to the existing monitor contract, and provide a
  key-driven way to move between the step list and transcript. The selected
  step and its status shall remain associated with the transcript when panels
  are switched.
- A pending gate shall remain visible and actionable in the narrow layout. The
  gate may occupy a full-width region, become a compact scrollable region, or
  be the focused region while the operator switches between steps and
  transcript. Narrow rendering shall not discard queued entries, approval
  choices, textarea drafts, or the identity of the step awaiting input.
- The monitor shall preserve the existing input queue semantics: multiple
  pending entries remain ordered, the active entry and total remain identifiable,
  and the existing answer, cancel, and navigation actions route to the correct
  step.
- The monitor shall retain security context for the selected or running step.
  Security status, findings, escalation/recovery state, or an explicit
  reachable security indicator shall remain visible in the status/context path;
  compact rendering may abbreviate labels, but shall not silently remove the
  distinction between normal, warning, and escalated states.
- Transcript content shall remain readable through vertical scrolling and shall
  preserve the current output anchor. If the monitor is following the bottom
  while a step streams, a resize shall keep it following the bottom; if the
  operator has scrolled away, a resize shall preserve the nearest logical
  position and shall not force a jump to the newest output.
- Selecting a different step shall continue to reload that step's transcript
  and reset only the per-step presentation state required by the existing
  monitor contract. A width change shall not change the selected step or lose
  transcript content.
- The monitor shall render unavailable or empty transcripts, persistence-off
  runs, in-progress runs, completed runs, and security-escalated runs without
  requiring a run directory that does not exist.
- Focus and key routing shall remain unambiguous in compact mode. A key meant
  for a focused gate or editor shall not also select a step or scroll the
  transcript, and the footer shall describe the controls available in the
  current layout.

**Proof Artifacts:**

- **Critical journey golden:** a compact monitor render with a running step,
  current transcript output, a pending approval, and a security warning
  demonstrates that all four kinds of context remain visible or reachable.
- **Model test:** a sequence of resize, focus switch, step selection, gate
  arrival, gate draft editing, and gate submission messages demonstrates that
  routing and state survive transitions between preferred and compact layouts.
- **Persistence-off test:** a monitor model with an empty run directory
  demonstrates that compact rendering still works and does not introduce a
  disk dependency.

### Unit 3: Streaming-safe resize and renderer lifecycle

**Purpose:** Ensure responsive rendering is a presentation update rather than a
session, execution, or persistence interruption.

**Functional Requirements:**

- A `WindowSizeMsg` shall not cancel, restart, duplicate, or reorder an active
  agent or command execution, and shall not create a second streaming session.
- Partial output received before, during, and after a resize shall be rendered
  in order. A renderer rebuild shall invalidate width-dependent caches while
  preserving the underlying transcript entries and the current stream state.
- Markdown/prose renderers shall be rebuilt when their cell width changes, and
  their cached rendered blocks shall be invalidated at the same boundary.
  Verbatim command output, tool results, and other non-markdown content shall
  continue through a verbatim path rather than being passed through a markdown
  renderer merely because the terminal is narrow.
- Resize handling and stream delivery shall be safe under concurrent message
  arrival. The model shall not expose partially rebuilt maps, stale dimensions,
  or data races to `View`.
- Logical state shall be kept separate from width-dependent presentation state.
  Rebuilding a renderer, viewport, or compact layout shall not rewrite the
  durable transcript or change workflow/engine state.

**Proof Artifacts:**

- **Lifecycle test:** a direct model test injects partial output around multiple
  width changes and demonstrates ordered output, one active stream, preserved
  follow/scroll behavior, and completion after the final delta.
- **Race result:** the TUI and relevant package tests run under the race detector
  and demonstrate no race during resize, streaming, gate dispatch, and view
  rendering.
- **Renderer cache test:** a width-change test demonstrates that rendered prose
  changes to the new width while transcript data and verbatim blocks remain
  unchanged.

### Unit 4: Safe display boundary for untrusted terminal data

**Purpose:** Prevent agent output, command output, tool data, filenames, and
security messages from executing terminal controls or visually impersonating
jig chrome while retaining the existing raw transcript record.

**Functional Requirements:**

- The system shall treat all agent text, model reasoning, command stdout/stderr,
  tool inputs/results, transcript fields, filenames, and security messages as
  untrusted display data unless the value is generated by jig's own renderer.
- Before untrusted data is sent to a terminal-rendering path, the system shall
  remove or neutralize control characters and escape sequences that can move the
  cursor, clear or rewrite the screen, change terminal modes, submit input, or
  otherwise alter terminal behavior. This includes CSI/ESC sequences, OSC
  sequences, BEL-triggering payloads, and OSC 8 hyperlinks.
- The sanitizer shall remove hyperlink semantics and shall not allow an
  untrusted string to inject a styled line, border, footer, or status element
  that is visually indistinguishable from trusted jig chrome.
- Sanitization shall preserve safe printable text, meaningful line breaks, and
  enough whitespace for readable output. It shall be deterministic and safe for
  malformed, truncated, and very large inputs.
- Sanitization shall occur at the display boundary, after transcript data is
  read and before wrapping, markdown rendering, or terminal composition. The
  raw persisted transcript shall remain unchanged by this feature, including
  its current persistence-off no-op behavior.
- The sanitizer shall not strip ANSI sequences that jig itself applies after
  untrusted content has been made inert; trusted theme styling and panel borders
  remain controlled by jig's renderer.
- The implementation shall include focused tests and fuzz/property coverage
  for control-sequence variants, malformed UTF-8, embedded hyperlinks, long
  inputs, Unicode width edge cases, and strings split across transcript blocks.

**Proof Artifacts:**

- **Security render fixture:** hostile transcript and command-output samples
  demonstrate that no control sequence or hyperlink survives into the final
  terminal payload, while safe text and line breaks remain readable.
- **Sanitizer test:** table-driven cases demonstrate neutralization of cursor,
  erase, title, hyperlink, bell, and malformed escape payloads.
- **Fuzz/property result:** bounded fuzzing demonstrates that sanitizer and
  width helpers terminate, do not panic, and never return a rendered line wider
  than the requested cell width.
- **Persistence test:** a raw transcript fixture demonstrates that rendering
  sanitization does not rewrite the stored JSONL entry and that an empty run
  directory still produces no writer errors.

## Non-Goals (Out of Scope)

- Android-specific commands, Termux package installation, shared storage,
  notification integration, or device-specific filesystem behavior.
- Remote sessions, synchronization between devices, hosted execution, or new
  authentication and authorization flows.
- Changes to workflow execution, engine scheduling, runner semantics, harness
  selection, transcript schema, journal semantics, or persistence retention.
- Redaction, encryption, disabling, or new retention rules for raw persisted
  command output, model reasoning, or transcript data. Existing `.jig/`
  persistence and `jig prune` behavior remain the contract.
- A new mobile-only screen, alternate application mode, or separate Android
  client.
- A promise that every full-width desktop composition remains simultaneously
  visible below the compact-layout threshold. Compact layouts may stack, scroll,
  or show one focused panel at a time as long as the visibility guarantees and
  navigation contract are met.
- Broad dependency modernization, a Go-version change, or unrelated TUI
  redesign. A dependency or baseline change requires separate approval based on
  a demonstrated implementation blocker.

## Design Considerations

The feature is a presentation contract, not a new execution path. The model
should maintain a clear distinction between logical state and derived layout
state. Logical state includes the selected workflow/run/step, focus, gate queue
and drafts, transcript position, stream state, and security state. Derived state
includes panel dimensions, compact-mode choice, wrapped lines, markdown
renderers, and render caches; derived state is rebuilt on width changes.

The implementation should use a small, explicit layout policy shared by the
surfaces rather than scattered terminal-width comparisons. The policy should
define the preferred layout, compact fallback, minimum tested width, and the
behavior below that width. A practical proof matrix is 120, 80, 60, 40, and 20
columns; the exact constants may be adjusted during implementation if the
resulting policy and tests preserve the guarantees above. Heights should also
include short and constrained cases so footer, gate, and input regions cannot
become inaccessible.

Narrow layouts should prioritize information in this order: current gate or
approval action, current step/run status, current output/security context,
navigation, then secondary decoration. Content may be abbreviated or moved
behind scrolling, but an operator must be able to discover where it went and
return to it. Focus changes must be visible through existing theme conventions,
not color alone where a compact layout makes color less reliable; a title,
marker, or footer hint should reinforce the focused region.

The monitor's existing width-based single-panel fallback is the starting point
for the narrow monitor behavior. Other surfaces should adopt the same principles
without coupling their models to monitor-specific state. The panel helper
remains presentation-only, and callers remain responsible for fitting bodies to
their inner dimensions.

Sanitization must happen before any renderer that can interpret terminal or
markup syntax. Markdown rendering is appropriate for prose only; command output,
tool results, and other verbatim blocks must stay verbatim after terminal
controls are neutralized. The sanitizer should be independently testable so
future rendering changes do not silently weaken the security boundary.

## Repository Standards

- Keep the change in the existing `internal/tui` presentation layer unless a
  small shared helper is necessary; do not make `internal/engine`,
  `internal/runner`, `internal/transcript`, or `internal/workflow` depend on
  Bubble Tea rendering concerns.
- Follow the Bubble Tea v2 model contract and test models directly by feeding
  `tea.Msg` values to `Update` and asserting on the returned model and `View()`
  string. Do not require a live terminal or agent service for unit tests.
- Use `lipgloss.Width` and related cell-aware helpers for all terminal geometry;
  do not use `len`, byte counts, or rune counts for visible width.
- Keep all lipgloss styles in `internal/tui/styles.go`, derive new styles from
  the existing semantic theme tokens, and use the shared input-textarea helper
  for gate/editor components.
- Rebuild glamour renderers and invalidate width-dependent per-block caches on
  resize. Render prose through glamour only; preserve verbatim rendering for
  command output and tool results.
- Preserve the repository's file-is-truth rule: the TUI reads durable
  `transcript.jsonl` content, while the event bus carries liveness signals only.
  Do not put bulk output on the bus as part of this feature.
- Preserve persistence-off as a first-class path. Empty run directories and
  empty transcript paths must remain safe no-op inputs for writers and render
  as unavailable/empty content without errors.
- Match existing Go conventions: small focused packages, comments explaining
  non-obvious reasons, table-driven tests where the shape fits, and no live
  model calls in tests.
- Keep examples and documentation valid, run `gofmt`, `go vet ./...`, and the
  repository's normal test suite before handoff. Do not alter the Go 1.25
  baseline or dependency versions unless a separately approved blocker is
  recorded.

## Technical Considerations

- `WindowSizeMsg` handling must be idempotent. Reapplying the same dimensions
  should not duplicate content, advance a cursor, reopen a gate, or append to a
  transcript.
- Viewport resizing should clamp to valid bounds while preserving a logical
  anchor. The implementation should distinguish following the newest streaming
  output from an operator-selected historical position.
- Width-dependent caches must be keyed or invalidated by the effective inner
  cell width, not just the outer terminal width. A monitor panel width change
  and a chat message-area width change may differ at the same terminal width.
- Terminal strings can contain ANSI styling added by jig as well as untrusted
  escape payloads. Width calculations must measure rendered cells, and
  sanitization must run before trusted styles are composed so it cannot mistake
  application styling for user content.
- Unicode handling must be defensive. Combining marks, wide East Asian
  characters, emoji sequences, invalid UTF-8, and newline boundaries must not
  cause negative padding, slicing through a multibyte sequence, or an oversized
  line. Prefer existing Charm width-aware helpers where they cover the required
  behavior.
- Renderer rebuilds should not read the complete transcript into memory. Keep
  the existing windowed transcript reads and block-size limits; only the
  presentation cache is invalidated.
- Concurrency boundaries must remain explicit. Bubble Tea model mutations should
  happen through the event loop, and background stream/gate channels should
  continue using the existing message/command handoff. The race test is part of
  the acceptance evidence, not an optional manual check.
- No service, schema, persistence, or dependency redesign is needed. If the
  current Bubble Tea v2 APIs cannot satisfy a requirement without an upgrade,
  stop and record the blocker for separate approval rather than silently
  changing the baseline.

## Security Considerations

The TUI displays content produced by agents and commands that may be malicious,
accidental, or simply contain terminal syntax. The final display boundary must
neutralize terminal control sequences and hyperlinks before content is passed to
wrapping, markdown, or terminal composition. This protects the terminal from
screen manipulation, input injection, mode changes, title spoofing, and visual
impersonation of jig status or approval controls.

The feature does not introduce credentials, authentication, authorization, or
new outbound connections. No secrets, transcript samples containing real
credentials, or device-specific tokens may be committed. Security tests should
use synthetic payloads and should verify both the rendered output and the
absence of surviving control semantics.

Raw transcript persistence remains intentionally unchanged. Transcripts may
contain sensitive command output, file contents, and model reasoning on local
disk under `.jig/`; this feature only makes the display inert. Tests must not
assume that display sanitization redacts the durable record, and any future
redaction or retention policy belongs in a separate specification.

## Success Metrics

- At least 100% of the fixed-width rendering matrix (120, 80, 60, 40, and a
  below-minimum case) renders without a line wider than the requested terminal
  width for all in-scope surfaces and representative monitor states.
- All in-scope surfaces retain a reachable navigation action and visible screen
  or context identity at every matrix width; monitor scenarios retain reachable
  status, transcript, gate/approval, and security context.
- Resize/lifecycle tests demonstrate zero execution restarts, duplicate stream
  sessions, lost gate drafts, lost transcript entries, or unexpected scroll
  jumps across repeated width changes.
- Sanitization tests demonstrate that representative CSI, OSC, OSC 8, BEL, and
  malformed escape payloads produce no terminal control behavior in the final
  render, while safe text remains readable.
- `go test ./...`, relevant focused tests, `go test -race ./...`, `go vet ./...`,
  and formatting checks pass without a Go-version or dependency change.
- Existing persistence and persistence-off tests continue to pass, including
  proof that display sanitization does not rewrite raw transcript records.

## Open Questions

No open questions at this time.
