# 25-spec-inline-arg-formatting.md

## Introduction/Overview

The Monitor transcript's collapsed tool-exchange row shows one "primary"
argument, chosen by a per-kind switch in `summarizeActivity`
(`internal/tui/monitor/monitor_tool_summary.go`). For tool kinds jig has a
mapping for (`read`, `edit`, `grep`, ...), that single value is a good
preview. For everything else — MCP tools, custom tools, any backend-specific
tool — the fallback (`primaryToolArg`) picks one value from a fixed key list
with no guarantee it is the informative one, and the value is clipped
after the fact rather than budgeted for. This feature adds a fair-share
inline argument formatter: given a tool's arguments and an available width,
it renders as many `key=value` pairs as fit, reserving a minimal footprint
for every key still to come so one long value cannot consume the whole row
and hide the keys after it.

## Goals

- Give every unrecognized tool exchange a multi-argument preview instead of
  one arbitrary value or nothing.
- Budget the preview's width up front so a long value yields space to
  shorter, more informative sibling arguments rather than starving them.
- Give known tool kinds with useful secondary arguments (e.g. `grep`'s
  `path`, `case`, `gitignore`) a populated Meta slot without disturbing
  their existing curated primary detail.
- Produce deterministic output across renders despite Go's randomized map
  iteration order.
- Prevent secret-looking argument values from reaching the transcript via
  their key name, independent of jig's existing content-based secret
  redaction.

## User Stories

- **As an operator watching a run**, I want an unfamiliar or MCP tool's
  collapsed row to show its key arguments so I can tell what it is doing
  without expanding every card.
- **As an operator watching a run**, I want a long argument value (a big
  JSON blob, a long path) to never crowd out the other arguments on the
  same row, so the preview stays informative under width pressure.
- **As an operator**, I want a `grep` exchange to show its scope (`path`,
  flags) alongside the pattern it already shows, without cluttering the
  primary title.
- **As an operator**, I want a credential-shaped argument (`token`,
  `password`, `secret`, `api_key`, ...) to never show its value in the
  transcript, even if it slipped past upstream redaction.

## Demoable Units of Work

### Unit 1: Fair-share inline formatter

**Purpose:** Implement the core formatting algorithm as a pure,
width-aware function, independent of how any caller uses it.

**Functional Requirements:**
- The system shall accept a set of tool arguments and an available width
  (in display cells) and return a single-line `key=value, key=value`
  preview that does not exceed that width.
- Before allocating width to a key, the system shall reserve a minimal
  footprint (separator + key name + `=` + a short value stand-in) for
  every key still pending, so a long value cannot starve the keys that
  follow it.
- The system shall order keys deterministically: a short fixed priority
  list (`path`, `file_path`, `command`, `pattern`, `query`, `url`) first,
  in that order, when present; all remaining keys sorted lexicographically
  after them. Repeated calls on the same input shall always produce the
  same order.
- The system shall format scalar values compactly: strings quoted with
  embedded newlines and tabs escaped (`\n`, `\t`) rather than emitted raw;
  numbers and booleans via their normal literal form; `null` as `null`.
- The system shall summarize array values as `[N items]` and object values
  as `{N keys}` rather than rendering their contents.
- When the width budget is exhausted before all keys are placed, the
  system shall append a trailing ellipsis rather than cutting a token off
  silently.
- The system shall exclude a fixed set of internal/noise argument keys
  from the preview (at minimum the equivalent of jig's existing partial-
  JSON/intent-marker fields already filtered elsewhere in the tool
  pipeline).
- For a key whose name matches a secret-shaped heuristic (case-insensitive
  match on `token`, `key`, `password`, `secret`, or a name ending in one of
  those words, e.g. `api_key`), the system shall render `key=<redacted>`
  instead of the actual value, regardless of remaining width budget.
- Given no arguments (or only excluded/noise arguments), the system shall
  return an empty string, not a placeholder such as `(no args)`.
- The formatted output shall always be a single line, even when a string
  value's escaped form is long.

**Proof Artifacts:**
- Test: table-driven unit tests in the monitor package covering — one long
  value plus three short keys (all four keys present in output); budget
  exhaustion (ellipsis appended); array and object arguments (rendered as
  counts); string values with embedded `\n`/`\t` (escaped, still one
  line); empty/noise-only arguments (empty output); a secret-shaped key
  (value redacted); output width never exceeding the supplied budget
  across varied inputs; stable key ordering across repeated calls on an
  unordered `map[string]json.RawMessage` input.

### Unit 2: Wire into the fallback and the Meta slot

**Purpose:** Make the formatter the actual fallback for unmapped tool
kinds, and let known kinds surface secondary arguments in Meta, without
touching the curated per-kind primary details that are already good.

**Functional Requirements:**
- When a tool exchange has no per-kind mapping in `summarizeActivity`, the
  system shall use the fair-share formatter's output as the collapsed
  row's argument preview, replacing the current single-value
  `primaryToolArg` fallback.
- The system shall thread an available width into `summarizeActivity` (or
  its caller) so the formatter budgets against real panel width rather
  than being clipped after the fact.
- For known tool kinds that have useful secondary arguments beyond their
  curated primary detail (starting with `grep`'s `path`, `case`,
  `gitignore`), the system shall populate the status line's Meta slot
  (`internal/tui/shared/status_line.go`'s `StatusLine.Meta`) using the
  fair-share formatter over just those secondary arguments, leaving the
  existing curated primary detail (`Description` slot) untouched.
- The existing `sanitizeToolSummary` pass shall still run over any string
  that reaches a rendered row, in addition to (not instead of) the new
  escaping done inside the formatter.
- The change shall not alter the collapsed-row output for any tool kind's
  primary detail that already has a per-kind mapping.

**Proof Artifacts:**
- Test: an unmapped/MCP-style tool activity produces a multi-key preview
  containing more than one argument, where a prior test on the same fixture
  showed only one.
- Test: a `grep` activity with `pattern`, `path`, and `case` arguments
  keeps `pattern` as the existing primary detail and shows `path`/`case`
  in Meta.
- Test: rendering the same unmapped-tool fixture at a narrow width and a
  wide width shows a shorter and longer preview respectively, both within
  their supplied width — demonstrating the budget is applied before
  render, not clipped after.

## Non-Goals (Out of Scope)

1. **The expanded JSON tree view**: jig's existing expanded-card rendering
   (`prettyToolInput` → `fenceJSON` → Chroma highlighting) is unchanged;
   this feature only affects the collapsed-row preview.
2. **Replacing the per-kind mapping in `summarizeActivity`**: the curated
   single-value detail for known kinds (`read`, `edit`, `write`, `bash`,
   etc.) stays exactly as it is; this feature is the fallback path plus an
   additive Meta source for select kinds.
3. **A preview for `bash`**: its single `command` argument is already the
   correct and complete preview; it is not touched.
4. **Changing or extending jig's existing content-based secret redaction**
   (`internal/runner`'s `redactSecrets`, which replaces known configured
   secret values wherever they appear in text). This feature adds a
   separate, key-name-based heuristic local to the formatter; it does not
   modify or duplicate the upstream mechanism.
5. **Persisting or logging the formatted preview** anywhere beyond the
   transcript row it renders into.

## Design Considerations

No specific visual design requirements identified beyond following the
existing status-line grammar. The preview renders into the `Description`
slot (for the fallback case) or the `Meta` slot (for known-kind secondary
arguments) of `shared.StatusLine`/`RenderStatusLine`
(`internal/tui/shared/status_line.go`), which already documents "slice 15
(inline argument previews in Description)" as a named consumer of that
slot. No new visual chrome, color, or layout primitive is introduced.

## Repository Standards

- Go conventions per `docs/CONVENTIONS.md`: table-driven tests, explicit
  error handling only at real boundaries, no speculative abstraction.
- TUI/width math conventions per `docs/TUI.md`: all width measurement goes
  through `lipgloss.Width`-based helpers, matching the rest of the monitor
  package (e.g. `shared.TruncateTitle`, `monitor_diff_render.go`'s
  `contentWidth` handling).
- Keep the formatter as a small, pure, independently testable function in
  the `monitor` package alongside `monitor_tool_summary.go`, matching how
  `shortFile`/`shortHost`/`primaryToolArg` are already organized there.
- `sanitizeToolSummary` (already applied to every collapsed-row field)
  continues to run over the formatter's output; the new formatter does not
  replace it.

## Technical Considerations

- **Go map iteration order is randomized**; `decodeToolArgs`
  (`monitor_tool_summary.go`) already unmarshals tool arguments into
  `map[string]json.RawMessage`. The formatter must establish its own
  deterministic key order internally (priority list, then lexicographic
  sort of the remainder) rather than iterating the map directly, or the
  preview will visibly reorder between renders.
- Width must be a real input to the formatter, not a post-hoc clip. The
  panel already threads content width to sibling rendering code (e.g.
  `m.transcriptInnerW` in `monitor_transcript_items_view.go`,
  `contentWidth` in `monitor_diff_render.go`); `composeToolHeader`
  (`monitor_transcript_items_view.go`) already receives `*Model` and can
  read the same width. `summarizeActivity`/`summarizeToolCall`
  (`monitor_tool_summary.go`) currently take no width parameter — their
  signatures need to accept one so the formatter can budget for real.
- Argument values arrive as `json.RawMessage`; each value must be
  unmarshaled lazily, at the point the formatter decides to spend budget
  on that key, so a large `content` value is never fully decoded just to
  be summarized as a count.
- The secret-key heuristic is implemented locally in the new formatter
  rather than in `internal/runner`'s `redactSecrets`, because that
  function replaces exact, configured secret string values wherever they
  occur in text (`internal/runner/command.go:196`) — a content-match
  mechanism. It has no concept of argument key names and is not the right
  home for a name-based heuristic that must catch secret-shaped values
  `redactSecrets` was never configured to know about.
- `HIDDEN_ARG_KEYS`-equivalent filtering: identify and reuse whatever
  jig-side internal/intent-marker keys are already excluded elsewhere in
  the tool-call pipeline, rather than inventing a new list from scratch.

## Security and Data Considerations

- Tool arguments are agent-controlled and may contain secrets that
  upstream redaction missed, or control characters. The preview shortens
  exposure relative to the full expanded view, which is neutral-to-
  positive, but the formatter's own escaping (newline/tab escaping, no raw
  control characters) must hold even before `sanitizeToolSummary` runs a
  second pass.
- Values for keys whose names suggest secrets (`token`, `key`, `password`,
  `secret`, or names ending in those words such as `api_key`) shall never
  have their value rendered — only `key=<redacted>` — independent of
  whether the value happens to also be caught by `redactSecrets`
  upstream.
- Do not commit any tool-argument content (real or synthetic secret-
  shaped values) from manual testing into proof artifacts; use synthetic
  fixture values only.

## Success Metrics

1. **Unmapped-tool coverage**: every collapsed row for a tool kind without
   a per-kind mapping shows at least one `key=value` pair when the tool
   has any non-noise arguments, versus today's single-arbitrary-value or
   empty result.
2. **Width safety**: 100% of unit test cases assert the formatted preview
   never exceeds the supplied width budget.
3. **Determinism**: 100% of unit test cases assert identical output across
   repeated calls with the same input map.
4. **No regression**: existing per-kind primary-detail tests for `read`,
   `edit`, `grep`, `bash`, etc. continue to pass unchanged.

## Open Questions

No open questions at this time. The three ambiguities the source slice
document flagged (key ordering strategy, whether known kinds get a full
multi-key preview vs. curated-detail-plus-Meta, and where the secret-key
heuristic belongs) are resolved above: priority list + sorted remainder;
curated detail plus Meta; and a formatter-local heuristic distinct from
`internal/runner`'s content-based `redactSecrets`.
