# Plan: Run Monitor — Per-Step Agent Transcript + Navigable Chat View

Status: **proposed** · Owner: TUI/engine · Scope: multi-phase epic

## Summary

Turn the run monitor into a navigable, master–detail view of a running (or
finished) workflow:

- The run view is a **step list**; `j`/`k` (and arrows) move a selection
  cursor between steps.
- Pressing **enter** on a step drills into that step's **chat chain** — the
  full agent conversation for that step, rendered as an ordered list of
  responses: assistant text, **reasoning**, tool calls **with inputs**, and
  tool **results**.
- The chain is durable and complete: every agent message is captured to a
  **per-step transcript file** on disk. The TUI renders from that file, not
  from the lossy live event bus.
- Large content (tool results, tool inputs, reasoning) is **collapsed to 80
  characters** with an expand affordance; full content lives on disk and is
  read in a bounded window on expand.

This document is the source of truth for the epic. Each `## Phase` is intended
to become one task; each `- [ ]` under **Work items** is a subtask.

## Motivation & design decisions

The monitor today (`internal/tui/monitor.go`) renders a single scrolling
viewport with a static step table plus a rolling 10-line tail of raw text for
*running* steps only. The engine event vocabulary (`internal/engine/event.go`)
carries `StepOutput{Delta}` (flat text) and `StepToolCall` (tool name, empty
detail) — there is **no** message boundary, no reasoning, and no tool output.
The agent runner (`internal/runner/agent.go`) already receives the SDK's
`AssistantMessage`/`UserMessage` (which carry `ThinkingBlock`, `ToolUseBlock`
with `Input`, and `ToolResultBlock` with `Content`) but discards all of it,
forwarding only `text_delta`.

Key decisions, and the issues they resolve:

1. **Durable per-step transcript file is the source of truth**, written
   directly by the runner, separate from the orchestration journal. Resolves:
   lossy `fanOut` drops (content never rides the drop-on-full channel), loss of
   transcript on monitor re-entry / run switching, unbounded TUI memory, and
   loop/retry ordering (entries are tagged with `iter`/`attempt`).
2. **The event bus carries only lightweight liveness signals** (`{RunID,
   StepID, Seq}` + an optional text delta for the live-typing tail). A dropped
   signal means "refresh is one seq stale," corrected on the next read.
3. **Capture the full message stream** (text + thinking + tool_use +
   tool_result), correlating calls to results by `ToolUseID`.
4. **Bounded on screen, full on disk.** Collapse large blocks to 80 chars in
   the TUI; expand triggers a bounded windowed read. A write-time hard cap
   (default 256 KiB per block, `truncated` flag) protects the write loop and
   disk from pathological outputs — this is separate from the 80-char render
   collapse.
5. **Master–detail navigation with an explicit mode** (`modeList` /
   `modeChat`) so `j`/`k` no longer collide with the viewport scroll keymap,
   and human-input overlays (review/prompt) keep keyboard precedence.

Not fixed by storage, handled in the TUI phases: keymap collision (Phase 4),
overlay precedence (Phase 4), parallel-step display (Phase 4/5), glamour
re-render cost (Phase 5).

## On-disk layout

Transcript lives beside the existing per-step `result.json`
(`internal/datastore/datastore.go`):

```
.jig/runs/<run-id>/
  journal.jsonl                     – orchestration events (unchanged)
  steps/
    <step-id>/
      result.json
      transcript.jsonl              – NEW: one Entry per line, append-only
  artifacts/
```

`.jig/` is already git-ignored. Retries and loop iterations **append** to the
same `transcript.jsonl` (never truncate); entries are distinguished by
`attempt` / `iter`.

## Transcript schema

One JSON object per line. Unversioned by design (see Phase 7): runs are
ephemeral, so there is no cross-version contract — the reader is best-effort.

```jsonc
{
  "seq": 1,                   // monotonic per step file, 1-based
  "ts": "2026-07-31T10:04:11Z",
  "iter": 0,                  // loop iteration (step.State.Iteration)
  "attempt": 0,               // retry attempt (step.State.Attempt)
  "role": "assistant",        // assistant | user | system | result
  "blocks": [
    { "type": "thinking",   "text": "..." },
    { "type": "text",       "text": "..." },
    { "type": "tool_use",   "tool_use_id": "toolu_1", "name": "Read",
      "input": { "file_path": "..." } },
    { "type": "tool_result","tool_use_id": "toolu_1", "is_error": false,
      "content": "...", "truncated": false }
  ]
}
```

Block types and fields:

| type          | fields |
|---------------|--------|
| `text`        | `text` |
| `thinking`    | `text` (from `ThinkingBlock.Thinking`); may be empty/redacted |
| `tool_use`    | `tool_use_id`, `name`, `input` (raw JSON of `ToolUseBlock.Input`) |
| `tool_result` | `tool_use_id`, `content` (string; structured content JSON-encoded), `is_error`, `truncated` |

Readers **must** tolerate unknown block `type` values (skip or render as
`[unsupported block: <type>]`) and skip unparseable lines, so a transcript from
a different build degrades gracefully instead of crashing.

---

## Phase 1 — Transcript store package (`internal/transcript`)

**Goal:** a standalone, fully unit-tested package that defines the schema and
provides an append writer and a windowed reader. No engine/runner/TUI imports;
pure data + file I/O, mirroring the `internal/step` / `internal/datastore`
style.

**Dependencies:** none. Ships first.

**New files:**
- `internal/transcript/transcript.go` — `Entry`, `Block`, block-type constants,
  schema version constant.
- `internal/transcript/writer.go` — append writer.
- `internal/transcript/reader.go` — windowed reader.
- `internal/transcript/transcript_test.go` — table-driven round-trip tests.

**Also:**
- `internal/datastore/datastore.go` — add
  `TranscriptPath(runDir, stepID string) string` returning
  `steps/<stepID>/transcript.jsonl`; add a test in `datastore_test.go`.

**Work items:**
- [ ] Define `Entry` and `Block` structs with the JSON tags above; `omitempty`
      on optional block fields. `Input` as `json.RawMessage`; `content` as
      `string`.
- [ ] Define constants: `SchemaVersion = 1`; block types (`BlockText`,
      `BlockThinking`, `BlockToolUse`, `BlockToolResult`); roles.
- [ ] `Writer`: `Create(path) (*Writer, error)` (O_APPEND|O_CREATE|O_WRONLY,
      0o644); `Append(e Entry) (seq int, err error)` — assigns `Seq` (monotonic,
      resumed from existing line count on open so retries continue the sequence),
      stamps `Ts` (UTC, second precision), sets `V`, writes one line; `Close()`.
      Buffer writes (bufio) and flush per Append so a concurrent reader sees
      whole lines.
- [ ] Write-time hard cap: a `MaxBlockBytes` (default 256 KiB) applied to
      `text` / `thinking` / `content`; on overflow, truncate and set
      `Truncated: true`. Cap is a package var/option so callers can tune it.
- [ ] `Reader`: `Open(path) (*Reader, error)`; `Count() (int, error)`;
      `Window(offset, limit int) ([]Entry, error)`; `Tail(n int) ([]Entry, error)`.
      Skip malformed lines and unknown block types without erroring.
- [ ] Reader is safe to call repeatedly while the writer appends (open, read to
      current EOF, close) — no long-lived shared handle.

**Acceptance criteria:**
- Append N entries, read them back identically (round-trip) including all block
  types and `iter`/`attempt`.
- A block over `MaxBlockBytes` is stored truncated with `truncated=true`.
- Reader ignores a corrupt/partial trailing line (simulating a crash mid-write)
  and an unknown block type.
- `Window(offset, limit)` returns the correct slice; `Tail(n)` returns the last
  n.

**Tests:** `transcript_test.go` (round-trip, truncation, corrupt line tolerance,
unknown block type, window/tail bounds). `datastore_test.go` (TranscriptPath).

**Risks/notes:** monotonic seq on reopen requires counting existing lines —
acceptable (files are per-step, not huge). Keep the reader allocation-light for
Phase 5's frequent reads.

---

## Phase 2 — Engine contract: liveness event, Reporter, plumbing

**Goal:** give the runner a way to (a) tell the scheduler which transcript path
to write and its iter/attempt, and (b) emit a lightweight "transcript advanced"
signal the TUI can react to. Keep bulk content **off** the event bus and out of
`journal.jsonl`.

**Dependencies:** Phase 1 (for the path helper; the event itself is
independent).

**Modified files:**
- `internal/engine/event.go` — add `StepMessage` liveness event.
- `internal/engine/executor.go` — extend `Reporter`; extend `StepRequest`.
- `internal/engine/engine.go` — tag/fan-out the new event; plumb path +
  iter/attempt into `StepRequest` at dispatch.
- `internal/engine/journal.go` — add `step_message` kind + decoder (so it round-
  trips, even though it carries no bulk content).
- `internal/engine/journal_test.go` — round-trip test for the new event.

**Work items:**
- [ ] `event.go`: add
      ```go
      type StepMessage struct {
          RunID, StepID string
          Seq           int    // transcript entry seq this refers to
          Iteration     int
      }
      func (StepMessage) isEvent() {}
      ```
      Keep `StepOutput` (used for the live-typing tail) and `StepToolCall`
      (may be deprecated once tool_use is captured via transcript — mark, don't
      remove yet).
- [ ] `executor.go`: add to `Reporter` interface
      `Message(seq, iteration int)` (liveness only — no payload). Keep
      `Output(delta string)` for the live tail.
- [ ] `executor.go`: extend `StepRequest` with
      `TranscriptPath string` and `Iteration, Attempt int`.
- [ ] `engine.go` `dispatch` (~line 585): set
      `req.TranscriptPath = datastore.TranscriptPath(s.runDir, st.ID)` when
      `s.runDir != ""`; set `req.Iteration = state.Iteration`,
      `req.Attempt = state.Attempt`.
- [ ] `engine.go` `reporter`: add `Message(seq, iteration int)` calling
      `r.ev(StepMessage{Seq: seq, Iteration: iteration})`; add a
      `case StepMessage:` to the tagging switch (~line 570) to stamp
      `RunID`/`StepID` and fan out.
- [ ] `journal.go`: add `step_message` to `eventKind` and a decoder entry.

**Acceptance criteria:**
- `StepMessage` round-trips through `MarshalEnvelope`/`UnmarshalEnvelope`.
- Existing engine tests still pass (adding a Reporter method must not break test
  executors — they receive the interface and simply don't call `Message`).
- `dispatch` sets a non-empty `TranscriptPath` only when persistence is enabled
  (`runDir != ""`), preserving the `root == ""` test path.

**Tests:** journal round-trip; an engine test asserting `StepRequest` carries the
transcript path + iter/attempt for a looped step (extend an existing loop test).

**Risks/notes:** confirm no test implements `Reporter` (only engine's concrete
`reporter` does) so adding a method is a safe, non-breaking change. Do not route
transcript content through `emit()`/`journal.jsonl`.

---

## Phase 3 — Runner: rich capture to the transcript store

**Goal:** the `AgentExecutor` captures the complete message stream — text,
reasoning, tool calls with inputs, tool results — into the per-step transcript
via the Phase 1 writer, and emits Phase 2 liveness signals. Enable extended
thinking. Derive the output artifact from the transcript rather than a parallel
buffer.

**Dependencies:** Phases 1 and 2.

**Modified files:**
- `internal/runner/agent.go` — the core change.
- `internal/runner/agent_test.go` (new) — capture logic against a fake message
  channel.
- Possibly `internal/runner/mux.go` — no change expected (passes Reporter
  through).

**Work items:**
- [ ] Open a `transcript.Writer` at `req.TranscriptPath` when non-empty; close
      on return. When empty (persistence off), fall back to in-memory no-op so
      tests without a run dir still work.
- [ ] Enable extended thinking in the client options
      (`claudecode.Option` for thinking) alongside `WithIncludePartialMessages`.
      Verify the configured model surfaces `ThinkingBlock`.
- [ ] Expand the `for msg := range msgChan` type switch:
  - [ ] `*claudecode.AssistantMessage`: iterate `Content`; map `TextBlock`→text,
        `ThinkingBlock`→thinking (handle empty/redacted; keep `Signature`
        awareness), `ToolUseBlock`→tool_use (name + `Input` as raw JSON). Append
        one `Entry{Role: "assistant", ...}`; call `rep.Message(seq, iter)`.
  - [ ] `*claudecode.UserMessage`: extract `ToolResultBlock`s from `Content`
        (which is `string` or `[]ContentBlock`); JSON-encode structured content
        to string; set `is_error`. Append `Entry{Role: "user", ...}`; call
        `rep.Message`.
  - [ ] `*claudecode.StreamEvent`: keep forwarding `text_delta` via
        `rep.Output(delta)` for the live-typing tail **only** (ephemeral; the
        finalized `AssistantMessage` is authoritative).
  - [ ] `*claudecode.ResultMessage`: on error, append a `result` entry with the
        error and return a failed `step.Result`; on success, finalize.
- [ ] Stamp every `Entry` with `req.Iteration` / `req.Attempt`.
- [ ] Output artifact: when `req.Step.Output != ""`, derive it from the
      concatenated final assistant `text` blocks (replaces the parallel
      `strings.Builder` at agent.go:75) so there is one capture path and no drift.
- [ ] Reconciliation: the transcript file is truth; `rep.Output` deltas are a
      preview for the current, not-yet-finalized bubble and must not be persisted.

**Acceptance criteria:**
- Given a scripted fake `msgChan` (assistant with thinking+text+tool_use, then a
  user tool_result, then result), the written `transcript.jsonl` contains the
  expected ordered entries with correct block types and `tool_use_id`
  correlation.
- Tool result exceeding `MaxBlockBytes` is stored truncated.
- With `req.Step.Output` set, the artifact equals the concatenated final
  assistant text.
- No transcript writes occur when `req.TranscriptPath == ""`.

**Tests:** `agent_test.go` drives `Execute` with a fake channel (inject via a
small seam — e.g. a package-level `receiveMessages` func var, or a fake client)
so no live SDK is needed. Assert transcript contents + `rep.Message` calls.

**Risks/notes:** **verify empirically** that the SDK yields one
`AssistantMessage` per assistant turn (not only a final `ResultMessage`), and
whether tool results arrive as `UserMessage` with `ToolResultBlock` vs.
`ToolUseResult`. Thinking may be redacted/signed — never assume plaintext.
Buffer writes so a large tool_result doesn't stall the receive loop.

---

## Phase 4 — TUI: navigable step list (modes + selection)

**Goal:** master-list interaction. Explicit `modeList` / `modeChat` state;
`j`/`k` select steps in the list; `enter` opens a step's chat; `esc` backs out
one level. Human-input overlays keep keyboard precedence. No content rendering
yet (Phase 5) — this phase is navigation + selection skeleton.

**Dependencies:** none (can proceed in parallel with 1–3 against the existing
model). Consumes Phase 5's reader later.

**Modified files:**
- `internal/tui/monitor.go` — model fields, key handling, list rendering,
  footer.
- `internal/tui/monitor_test.go` (new, or extend `root_test.go`).

**Work items:**
- [ ] Add to `monitorModel`: `mode monitorMode` (`modeList` default | `modeChat`);
      `cursor int`; `chatStep string`.
- [ ] `modeList` key handling — intercept **before** the `m.vp.Update` fall-
      through (monitor.go:154): `j`/`down` and `k`/`up` move `cursor` within
      `[0, len(steps))`; `enter` sets `mode = modeChat`, `chatStep =
      steps[cursor].id`, rebuilds content, `GotoTop`; `esc`/`q` →
      `showRunsMsg{}`.
- [ ] `modeChat` key handling: `esc`/`h`/`left` → back to `modeList` (restore
      list content); `j`/`k`/`ctrl+d`/`ctrl+u`/pgup/pgdn scroll the viewport
      (keep fall-through).
- [ ] Overlay precedence: keep `pendingPrompt` / `pendingReview` early-returns
      first in `Update` (monitor.go:98–140). If a gate arrives while in
      `modeChat`, force `mode = modeList` and show the overlay so input is never
      ambiguous.
- [ ] `listBody()` (split from `body()`): render the step table with a `>` /
      bold cursor (mirror `runs.go:133–154`); show per-step running indicator +
      a message count (from the transcript reader once Phase 5 lands; a running
      counter of `StepMessage` seqs until then).
- [ ] Keep the selected row visible: nudge `m.vp.YOffset` when the cursor nears
      the top/bottom edge of the viewport.
- [ ] Update `footerView()` with mode-specific hints (`j/k select · enter open`
      vs `esc back · j/k scroll`).

**Acceptance criteria:**
- `j`/`k` move the cursor and do not scroll in `modeList`; they scroll in
  `modeChat`.
- `enter` on a step enters `modeChat` for that step; `esc` returns to
  `modeList`; a second `esc` returns to the runs list.
- A `ReviewRequest` while in `modeChat` pops the overlay and freezes navigation;
  digit keys still select the verdict.

**Tests:** feed a `tea.KeyMsg` sequence; assert `cursor`, `mode`, and emitted
messages (`showRunsMsg`). Reuse the `root_test.go` harness style.

**Risks/notes:** the viewport's default keymap binds `j`/`k`; interception order
is the whole ballgame. Keep cursor-visible math simple (top/bottom margin of a
few rows).

---

## Phase 5 — TUI: chat chain rendering (collapse/expand)

**Goal:** render a step's transcript in `modeChat` as an ordered list of
responses read from `transcript.jsonl` — text, reasoning, tool calls (name +
input), tool results — with **80-char collapse** and expand, iteration
separators, and lazy markdown. Bounded on screen, windowed reads from disk.

**Dependencies:** Phases 1 (reader), 2 (liveness event), 4 (modes). Can be
developed against fixture `transcript.jsonl` files before Phase 3 is live.

**Modified files:**
- `internal/tui/monitor.go` — `chatBody()`, per-block rendering, expand state,
  live-tail handling, reader integration.
- `internal/tui/styles.go` — styles for thinking (dim/italic), tool call, tool
  result, error result, "expand" hint.
- `internal/tui/monitor_test.go` — rendering + collapse/expand + windowing.

**Work items:**
- [ ] On entering `modeChat` and on each `StepMessage` for `chatStep`, read the
      transcript via `transcript.Reader` (windowed — see below). Do **not** hold
      the whole transcript in the model; keep only the visible window + expand
      set.
- [ ] Render each entry as a numbered response block. Per block type:
  - [ ] `text` → markdown via glamour, **lazily rendered and cached** (reuse the
        `turn.go` `rendered`-cache pattern; re-render only on width change).
  - [ ] `thinking` → dim/italic, prefixed (e.g. `🧠 reasoning`), collapsed to 80
        chars by default.
  - [ ] `tool_use` → `⚙ <name>(<input>)` with the input JSON collapsed to 80
        chars.
  - [ ] `tool_result` → `↳ result` with content collapsed to **80 characters**
        on one line; error results styled distinctly (`is_error`); `truncated`
        entries show a `… (truncated at write)` marker.
- [ ] **Collapse/expand:** default-collapse `thinking`, `tool_use` input, and
      `tool_result` to 80 chars with an affordance (e.g. `▸`/`▾` and a
      `[N chars]` / `[N lines]` hint). An `expand` set keyed by
      `(seq, blockIndex)`; a key (e.g. `enter` / `space` / `tab`) toggles the
      block under a block cursor, or `o` to expand all in view. On expand, do a
      **bounded windowed read** of the full content (cap the rendered expansion,
      e.g. head+tail with `… N KB elided`, so a 256 KiB block never lays out in
      full).
- [ ] Iteration/attempt separators: when `iter`/`attempt` change between
      entries, render `── iteration 2 ──` / `── retry 1 ──`.
- [ ] Live tail: while `chatStep` is running, append the ephemeral `rep.Output`
      delta buffer as a "typing…" bubble below the finalized entries; discard it
      when the finalized `AssistantMessage` for that seq is read from disk
      (reconciliation).
- [ ] Windowing: only render entries within a viewport-sized window; page as the
      user scrolls. Never slurp the whole file.

**Acceptance criteria:**
- A fixture transcript renders in order with correct styling per block type.
- A `tool_result` longer than 80 chars shows exactly 80 chars + expand hint
  collapsed; expanding shows a bounded full view (head+tail elision for very
  large content).
- Iteration boundaries render separators.
- Expanding/collapsing does not re-render finalized markdown from scratch
  (cache hit) except on width change.
- Rendering a 10k-entry fixture stays responsive (windowed read, no full slurp).

**Tests:** golden-ish assertions on `chatBody()` for a fixture transcript;
collapse-at-80 boundary test; expand toggle test; window bounds test.

**Risks/notes:** glamour bakes wrap width at construction — rebuild + invalidate
cache on `WindowSizeMsg` (see `turn.go` doc comment). Keep the live-tail as
plain styled text (no glamour) to avoid per-delta thrash.

---

## Phase 6 — Command-step capture & non-agent fallback

Status: **done**

**Goal:** parity for non-agent steps so `enter` is never a dead end.

**Dependencies:** Phases 1–3, 5.

**Modified files:**
- `internal/runner/command.go` — write stdout/stderr to the transcript.
- `internal/tui/monitor.go` — drill-in fallback for `review` / `command` /
  no-transcript steps.

**Work items:**
- [x] `command.go`: write the combined stdout/stderr as a `text` block in a
      `role: "system"` entry in the same transcript store (one entry per
      execution, tagged with `iter`/`attempt`), then signal `rep.Message` so an
      open chat view refreshes. Skipped when persistence is off or output is
      empty; written on both success and failure.
- [x] TUI: `chatBody` renders system/text output **verbatim** (not through
      glamour) so terminal output isn't reflowed as prose. For a `review` step,
      the drill-in shows the retained diff/choices (a new `reviews` map keeps the
      last `ReviewRequest` per step after the verdict clears `pendingReview`);
      for a step with no transcript, a graceful `no output yet` placeholder.

**Acceptance criteria:**
- `enter` on a command step shows its output; on a review step shows the diff;
  on an empty step shows a graceful placeholder.

**Tests:** extend `command_test.go` to assert transcript writes; a TUI test for
each fallback.

**Risks/notes:** decide whether command output uses the same 80-char collapse
(recommended: yes, for consistency). *Resolved:* command output is a `text`
block, so it renders in full (verbatim, no collapse) — the 80-char collapse
stays scoped to thinking/tool_use/tool_result, matching how a reader expects to
read a command's log. The write-time byte cap still bounds pathological output.

---

## Phase 7 — Retention, security, schema versioning, docs

Status: **done**

**Goal:** make the persisted transcript a first-class, safe, documented format.

**Dependencies:** Phase 1 (schema) at minimum; land before/with the first real
write in a shared environment.

**Modified files:**
- `internal/datastore/datastore.go` (or a new `internal/datastore/retention.go`)
  — pruning.
- `docs/ARCHITECTURE.md`, `docs/engine-design.md` — document the transcript
  store, schema, and the "file is truth / bus is liveness" split.
- This file — mark phases complete as they land.

**Work items:**
- [x] Confirm `.jig/` git-ignore covers transcripts (it does today) and add a
      test/assertion or doc note so it isn't accidentally un-ignored. *Done:*
      documented in the security/PII posture note in `ARCHITECTURE.md`.
- [x] Retention policy: prune runs older than N days or keep the last N runs
      (config knob); a `jig` housekeeping path or a documented manual step.
      *Done:* `datastore.Prune`/`Prunable` + a `jig prune --max-age/--keep-last
      --dry-run` subcommand; only terminal runs are candidates.
- [x] Security/PII note in docs: transcripts persist **raw tool output and
      reasoning** (possible secrets in command output) to disk under `.jig/`.
- [x] Schema versioning: **dropped by decision.** Runs are ephemeral and a
      `.jig/` dir is not expected to survive a jig upgrade, so there is no
      cross-version contract worth maintaining. The `v`/`SchemaVersion` field was
      removed entirely; the reader is best-effort only (skips unparseable lines,
      drops unknown fields, renders unknown block types as placeholders). A stale
      transcript from a different build degrades gracefully; prune after
      upgrading.
- [x] Update `docs/ARCHITECTURE.md` package list to include `internal/transcript`
      and the transcript file in the run-dir layout.

**Acceptance criteria:**
- Retention removes old runs without touching live ones.
- Docs describe the schema, the storage split, and the security posture.
- A line with unknown fields and an unknown block type is read without panicking
  (best-effort-render smoke test); versioning was dropped by decision.

**Risks/notes:** keep retention conservative by default (never delete a run
that isn't terminal).

---

## Cross-cutting acceptance for the epic

- Starting a run, drilling into a running agent step, and watching responses
  (text + reasoning + tool call + result) stream in, with large results
  collapsed to 80 chars and expandable.
- Navigating away and back (and switching between two runs) preserves every
  step's full chain (read from disk).
- Killing and restarting `jig` and reopening a finished run still shows the
  full chain.
- No panics / unbounded memory on a long run with large tool outputs.

## Suggested build order

1. Phase 1 (store) — foundation, standalone.
2. Phase 2 (engine contract) — small, unblocks 3 and 4.
3. Phase 4 (TUI navigation) — parallelizable with 1–3 against the current model.
4. Phase 3 (runner capture) — needs 1 + 2.
5. Phase 5 (chat rendering) — needs 1, 2, 4; developable on fixtures before 3.
6. Phase 6 (command/non-agent parity).
7. Phase 7 (retention/security/docs) — land with the first real write.

## Open questions to resolve before/while building

- **SDK message shape:** does the SDK emit one `AssistantMessage` per assistant
  turn, and do tool results arrive via `UserMessage.Content` `ToolResultBlock`
  or `UserMessage.ToolUseResult`? Verify in Phase 3 spike.
- **Thinking enablement:** which client option enables extended thinking, and
  does the target model return `ThinkingBlock` (vs. redacted)?
- **`MaxBlockBytes` value:** default 256 KiB — confirm against expected tool
  output sizes.
- **Expand affordance keybinding:** block-cursor + toggle key vs. expand-all;
  finalize in Phase 5.
