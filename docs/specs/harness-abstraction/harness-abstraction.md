# harness-abstraction.md

> **Spec A of a 3-way split.** This is the **foundational seam extraction** only:
> introduce a jig-owned `Harness` abstraction, refactor `AgentExecutor` behind it,
> and normalize the Claude Agent SDK shapes that leak into jig-owned types — while
> keeping the Claude SDK as the **sole** implementation. There is **zero
> user-visible behavior change**. The `harness` TOML selector + a real second
> backend (**Spec B**) and migrating the Tier-2 security monitor + resolving the
> dead frontend chat (**Spec C**) are deliberately deferred. **Claude Code CLI
> (`claude -p`)** is the design/validation target here — used to pressure-test the
> interface, **not built** in this spec.

## Introduction/Overview

jig is a deterministic orchestration layer around non-deterministic agents. Today
the workflow engine is hard-wired to one agent backend: the Claude Agent SDK
(`github.com/severity1/claude-agent-sdk-go`, imported as `claudecode`). The
engine already has the right dependency-inversion seam — `engine.Executor`
(`internal/engine/executor.go:13`), implemented by the runner and registered on
`runner.Mux` in `cmd/jig/main.go` — but that seam routes by **step type**
(command / agent / review), so every agent step funnels into the single
757-LOC `AgentExecutor` (`internal/runner/agent.go`), which is saturated with SDK
calls. Worse, SDK-shaped data leaks *past* the seam into jig-owned types
(`step.Result`, `engine.StepRequest`), so swapping backends would today require
touching cost/token accounting and the security layer, not just the runner.

This spec introduces a **jig-owned `Harness` interface** that the runner depends
on instead of calling the SDK directly, and **normalizes** the SDK shapes that
currently leak, so nothing downstream of the harness reads an SDK field. The
Claude Agent SDK becomes one concrete `Harness` implementation behind that
interface. Because the abstraction is exercised (as a design target) against a
*second, differently-shaped* backend — the Claude Code CLI — the interface is
forced to express every SDK-specific concept as either a **normalized value** or
an **optional capability**, not as a leaked SDK type.

The proof that this is a pure seam extraction is already latent in the codebase:
`internal/transcript` (Entry/Block: `BlockText` / `BlockThinking` / `BlockToolUse`
/ `BlockToolResult`) is a jig-owned normalized message model, and the **run
monitor** (`internal/tui/monitor.go`) reads *only* the transcript with **zero SDK
imports**. A backend swap therefore requires no run-monitor changes — so after
this refactor, the run monitor's token/cost figures must be **byte-for-byte
identical**. That is the acceptance test for "zero behavior change."

## Goals

- **Define a jig-owned `Harness` interface** that fully covers the agent client
  lifecycle and option set `AgentExecutor` uses today, so the runner depends on a
  jig type rather than on `claudecode`.
- **Model backend-specific features as capabilities**, via a thin core interface
  plus optional capability interfaces (detected by Go type assertion) and a
  runtime capability descriptor, so a future harness lacking a feature degrades
  gracefully instead of breaking the engine, the security layer, or the
  block_on/recovery flows.
- **Extract the Claude Agent SDK behind the interface** as the sole concrete
  `Harness`, preserving every load-bearing behavior — most critically the
  `WithCanUseTool` permission callback (the Tier-1 guard) and the in-process
  `WithSdkMcpServer` for `AskUserQuestion`.
- **Normalize the SDK shapes that leak** into `step.Result` and
  `engine.StepRequest` so no code outside the Claude harness reads an SDK field —
  while keeping the run monitor's token/cost numbers identical.
- **Prove the interface is not Claude-SDK-shaped** by showing, on paper, how the
  Claude Code CLI would slot in as a second harness through the capability
  interfaces without changing the core interface.

## User Stories

**As a jig maintainer**, I want the agent runner to depend on a jig-owned
`Harness` interface instead of calling the Claude SDK directly, so that adding a
second backend later (Spec B) is a matter of writing one new implementation, not
re-plumbing the runner, the cost accounting, and the security layer.

**As a jig maintainer**, I want SDK-specific concepts (session ids, usage maps,
permission callbacks, in-process MCP tools) to be expressed as normalized values
or optional capabilities, so a backend that lacks one of them (e.g. a CLI that
only does allow-list tool approval) degrades gracefully rather than forcing an
interface redesign.

**As an operator**, I want this refactor to change *nothing* I can observe — the
same transcripts, the same run-monitor token/cost figures, the same workflow
outcomes — so I can adopt it with confidence that it is a pure internal seam
extraction.

## Demoable Units of Work

Dependency-ordered. Each unit is a candidate implementation increment and is
independently reviewable. Units A1–A2 define the abstraction; A3 moves the runner
onto it; A4 normalizes the leaks; A5 is the paper proof against the CLI.

### Unit A1: The core `Harness` interface + capability model

**Purpose:** Establish the jig-owned seam the runner will depend on. This is the
type-design unit; no runner behavior changes yet.

**Functional Requirements:**
- The system shall define a jig-owned `Harness` interface covering the agent
  client lifecycle `AgentExecutor` uses today: create client, connect, stream/
  receive messages, query (streaming and one-shot), and disconnect — expressed in
  jig-owned terms (no `claudecode` types in the interface signature).
- The interface shall carry the full option set the runner sets today: `model`,
  `fallback_model`, `effort`, `max_turns`, `max_thinking_tokens`,
  `max_budget_usd`, `permission_mode`, `allowed_tools`, `disallowed_tools`,
  `json_schema`, `resume`, `continue_conversation`, `cwd`, and
  `include_partial_messages` — as a jig-owned options struct, not the SDK's
  functional-option list.
- The system shall model backend-variable features as **optional capability
  interfaces** discovered by Go type assertion, and a **runtime capability
  descriptor** a caller can consult before invoking a feature. The two features
  that MUST be capabilities (because non-SDK backends commonly lack them) are:
  (1) the **per-call permission callback** used by the Tier-1 sentinel guard, and
  (2) **in-process MCP tools** (the `AskUserQuestion` server). Structured output
  (JSON Schema) and session resume shall also be expressible as capabilities but
  are broadly supported and map cleanly.
- The core interface shall be **thin**: a harness that implements only the core
  lifecycle + options must be constructible and usable for a plain agent step;
  capabilities are additive, never required by the core.

**Proof Artifacts:**
- Compilation artifact: the `Harness` interface, options struct, capability
  interfaces, and capability descriptor compile as a package with no `claudecode`
  import in the interface-defining file.
- Design note (records the packaging decision — see Open Questions): where the
  interface lives (`internal/harness` vs `internal/engine`) and whether the
  descriptor is a struct of bools, a set of type-assertions, or both.

### Unit A2: The `ClaudeSDK` harness — sole concrete implementation

**Purpose:** Implement the interface once, wrapping the Claude Agent SDK, with all
load-bearing behaviors intact.

**Functional Requirements:**
- The system shall provide one concrete `Harness` that wraps
  `github.com/severity1/claude-agent-sdk-go`, translating the jig options struct
  into the SDK's functional options and the SDK's lifecycle
  (`NewClient`/`Connect`/`ReceiveMessages`/`QueryStream`/`Query`/`Disconnect`).
- The Claude harness shall implement the **permission-callback capability** by
  registering `WithCanUseTool`, preserving today's guard semantics **exactly**:
  when a Tier-1 guard is active it forces `PermissionModeDefault` (the only mode
  in which the SDK fires the callback — `acceptEdits` auto-approves writes and
  bypasses it), and calls `Guard.Check` before each tool executes
  (`internal/runner/agent.go:60-70`).
- The Claude harness shall implement the **in-process MCP capability** by
  registering `WithSdkMcpServer("jig", …)` for the `AskUserQuestion` tool when the
  step enables it (`agent.go:47-49`), preserving the tool-name rewrite to
  `mcp__jig__AskUserQuestion`.
- The Claude harness shall implement the **resume capability** (`WithResume` +
  `WithContinueConversation`) and the **structured-output capability** (JSON
  Schema), matching current behavior.
- The Claude harness shall report a capability descriptor advertising all of the
  above as present.

**Proof Artifacts:**
- Test run: the existing agent-step test suite (`internal/runner/...`) passes
  **unchanged** against the runner-on-Harness build.
- Test: with a guard active, the harness forces `PermissionModeDefault` and the
  callback fires before a tool call; with `acceptEdits` the callback is bypassed —
  the pre-refactor guard behavior, re-proven through the harness.

### Unit A3: Refactor `AgentExecutor` onto the `Harness`

**Purpose:** Move the 757-LOC runner off direct SDK calls and onto the interface,
so the SDK import lives only in the Claude harness.

**Functional Requirements:**
- `AgentExecutor` shall obtain a `Harness` (constructed/injected — the exact
  wiring is an implementation choice) and drive the agent step through the
  interface and capabilities, with **no direct `claudecode` calls remaining in
  `agent.go`** for the agent-step path.
- Where a capability is required by the step (guard → permission callback;
  `AskUserQuestion` → in-process MCP; resume; structured output), the runner shall
  consult the capability (type assertion / descriptor) and use it when present;
  when a harness lacks it, the runner shall follow the documented degradation for
  that capability rather than assuming it exists.
- The message-stream capture that maps SDK message/block types into
  `transcript.Block` (`assistantBlocks` / `toolResultBlocks`) shall be driven from
  the harness's normalized message model, so transcript output is unchanged.
- **Out of scope for A3 (stays on its current direct-SDK path):** the Tier-2
  `MonitorAdapter` (`internal/runner/monitor.go`) and the dead frontend chat
  (`internal/tui/client.go` + `internal/tui/chat.go`). These keep importing
  `claudecode` directly in Spec A (deferred to Spec C).

**Proof Artifacts:**
- Grep artifact: `claudecode` is imported by `agent.go` **zero** times after the
  refactor; it remains imported only by the Claude harness file(s), and (still, by
  design) by `internal/runner/monitor.go` and the dead `tui` chat files.
- Full-run artifact: running a real workflow (e.g. this `sdd-spec` workflow)
  end-to-end produces a `transcript.jsonl` identical in structure to a pre-refactor
  run of the same workflow.

### Unit A4: Normalize the leaked SDK shapes

**Purpose:** Stop SDK-shaped data at the harness boundary so no downstream code
reads an SDK field — the crux of "swappable backend."

**Functional Requirements:**
- `step.Result` shall carry a **jig-owned** representation of the fields it today
  documents as SDK-shaped (`internal/step/step.go:67-83`): `Subtype`
  ("why did the turn end"), `TotalCostUSD`, token `Usage`, and `SessionID`. The
  harness shall populate these from its own normalized model; the field comments
  must no longer cite "the SDK's `ResultMessage`."
- `Result.TokenCount()` shall sum tokens from a **jig-owned** usage
  representation, not by reaching into SDK usage-map keys
  (`input_tokens` / `output_tokens` / `cache_creation_input_tokens` /
  `cache_read_input_tokens`). The mapping from a backend's native usage shape into
  the jig representation shall live inside that backend's harness.
- `engine.StepRequest` shall express `ResumeSessionID` + `Message` (session
  resume) and `Guard *sentinel.Guard` (the permission callback) in terms of
  harness capabilities, so the request struct's meaning does not depend on the
  Claude SDK. (`Guard` may remain a `sentinel` type; the requirement is that the
  runner routes it through the permission-callback **capability**, not a hard SDK
  assumption.)
- **Acceptance invariant:** the run monitor (`internal/tui/monitor.go`), which
  reads only the transcript and has zero SDK imports, shall require **no changes**,
  and its per-step token and cost figures shall be **identical** before and after
  this unit.

**Proof Artifacts:**
- Diff artifact: `internal/tui/monitor.go` is unchanged by this spec.
- Comparison artifact: cumulative per-step token and cost figures from the run
  monitor on a fixed recorded run are identical pre- and post-normalization
  (the zero-behavior-change guarantee for cost/token accounting).
- Grep artifact: outside the Claude harness package, no source file references the
  SDK usage-map keys or `claudecode` result types.

### Unit A5: Claude Code CLI capability-fit proof (design only)

**Purpose:** Demonstrate the interface is not Claude-SDK-shaped, without building a
second backend.

**Functional Requirements:**
- The system shall include a **capability-matrix design note** mapping each
  `Harness` interface method and capability to how the **Claude Code CLI**
  (`claude -p`, subprocess + stream-json transport) would satisfy or degrade it —
  specifically: MCP over **stdio/HTTP** transport (not in-process), tool approval
  as an **allow-list / guardrail** (not a synchronous per-call callback), and
  session resume + structured output mapping cleanly.
- The note shall show that every SDK-specific concept in the current runner is, in
  the new interface, either **normalized** (session id, usage, message/block
  model) or an **optional capability** (permission callback, in-process MCP), such
  that the CLI's degradations require **no change to the core interface** — only a
  different capability descriptor.
- The note shall reference the real precedent: Claude Code's CLI emits a
  `capabilities` string array in its `system/init` event for exactly this
  feature-detection pattern ("feature-detect instead of comparing versions; ignore
  unknown values").

**Proof Artifacts:**
- Design artifact: `docs/specs/harness-abstraction/harness-capability-matrix.md`
  (or equivalent) — a table of interface method/capability × {ClaudeSDK, ClaudeCLI}
  showing native-support vs degradation, demonstrating no core-interface change is
  needed to admit the CLI.

## Non-Goals (Out of Scope)

1. **The `harness` TOML selector.** No `harness` field is added to
   `workflow.Step` / `Defaults` / `AgentProfile`, and no placement in the
   `step > agent_file > profile > [defaults]` precedence chain. Deferred to
   **Spec B**. (Note the loader rejects unknown keys, so adding the field is a
   deliberate schema+validation change belonging to B.)
2. **Building a second concrete backend.** The Claude Code CLI is a *design
   target* here, not an implementation. Deferred to **Spec B**.
3. **Migrating the Tier-2 `MonitorAdapter`** (`internal/runner/monitor.go`, the
   separate single-turn / tools-off SDK path) through the Harness. It stays on its
   own direct-SDK path in Spec A. Deferred to **Spec C**.
4. **Resolving the dead frontend chat** (`internal/tui/client.go` +
   `internal/tui/chat.go`, unreachable except from `chat_test.go`). It stays on its
   direct-SDK path in Spec A. Deferred to **Spec C**.
5. **Any user-visible behavior change** — no new flags, no changed transcripts, no
   changed run-monitor output, no changed workflow outcomes.

## Design Considerations

No UI/UX change. The run monitor (`internal/tui/monitor.go`) is explicitly a
**fixed point**: it must not change, and its rendered token/cost figures are the
observable acceptance signal for zero behavior change.

## Repository Standards

- **Consumer-defined interfaces / dependency inversion (as with `engine.Executor`).**
  The `Harness` interface is defined where it is consumed and implemented
  separately; the exact package (`internal/harness` vs `internal/engine`) is an
  open packaging question (below). Engine/runner stay free of vendor lock beyond
  the one concrete harness.
- **The transcript is the normalization boundary.** Reuse the existing
  `internal/transcript` Entry/Block model as the proven target; do not invent a
  parallel normalized model. The run monitor already depends only on it.
- **Thin consumers, capability by type assertion.** Follow idiomatic Go: a small
  core interface plus optional interfaces detected via type assertion, mirroring
  how the codebase already keeps the SDK out of `engine`.
- **Tests are table-driven with inline fixtures**, covering both the happy path
  and the degradation path (a harness that lacks a capability).
- **Comments explain the non-obvious "why"** — especially the guard/permission-mode
  coupling (`acceptEdits` bypasses the callback) and why usage/session/subtype are
  normalized rather than passed through.
- **Persistence-off (`runDir == ""` / no transcript path) stays a first-class
  no-op path**, unchanged by the refactor.

## Technical Considerations

- **The seam exists but routes by step type.** `runner.Mux` dispatches
  command/agent/review; the Harness lives *inside* the agent path, one level below
  the existing `Executor` seam — this spec does not change `Executor` or the Mux.
- **Load-bearing SDK behaviors that must survive the wrap:**
  - `WithCanUseTool` permission callback, with the `PermissionModeDefault`-forcing
    guard coupling (`agent.go:60-70`) — the Tier-1 firewall depends on it.
  - `WithSdkMcpServer` in-process `AskUserQuestion` tool + the
    `mcp__jig__AskUserQuestion` name rewrite (`agent.go:47-49`).
  - `WithResume`/`WithContinueConversation` session resume; JSON-Schema structured
    output; `include_partial_messages` streaming.
- **Normalization must be lossless for accounting.** `Result.TokenCount()` and the
  run monitor's cumulative per-step cost/token tracking depend on the usage buckets
  and `TotalCostUSD`; the jig-owned usage representation must preserve exactly what
  those consumers read today. A `nil` cost pointer must still mean "not reported"
  (distinct from a reported `$0.00`).
- **Capability degradation is a first-class requirement, not a future nicety.** The
  interface must let the engine/security-layer/block_on/recovery flows ask "does
  this harness support X?" and choose a documented fallback — because the CLI
  design target already lacks the in-process MCP and synchronous-callback shapes.
- **Deliberate deviation from external guidance:** industry consensus is to own a
  thin interface over vendor SDKs/CLIs rather than adopt a heavyweight agent
  framework (whose opinionated agent loop would fight jig's own engine/DAG). This
  spec follows that consensus — the abstraction is a thin seam, not a framework.
- **Two SDK paths stay direct in Spec A** by design: `internal/runner/monitor.go`
  (Tier-2) and the dead `tui` chat. The grep proof must *expect* those imports to
  remain.

## Security Considerations

- **The Tier-1 sentinel guard must not weaken.** The permission-callback capability
  must reproduce the exact guard semantics — including forcing
  `PermissionModeDefault` so the callback actually fires, and rejecting/escalating
  via `Guard.Check` before each tool executes. A regression here would silently
  disable the tool firewall, so it is covered by an explicit test (Unit A2).
- **`AskUserQuestion` stays in-process for the Claude harness.** No new external
  transport is introduced in Spec A; the CLI's stdio/HTTP MCP transport is
  design-only. Findings persistence (`FindingsPath`) is unchanged.
- **No new secrets, credentials, or external endpoints** are added. The refactor
  moves existing calls behind an interface; it does not broaden the trust boundary.

## Success Metrics

1. **SDK confined to one place:** after the refactor, `claudecode` is imported by
   the Claude harness package (and, by explicit deferral, `runner/monitor.go` +
   the dead `tui` chat) — and **not** by `internal/runner/agent.go` (A3).
2. **Behavior is identical:** a full workflow run produces a `transcript.jsonl`
   identical in structure to a pre-refactor run, and the run monitor's cumulative
   per-step token/cost figures match exactly (A3, A4).
3. **Guard preserved:** the Tier-1 permission-callback test passes through the
   harness — callback fires under default mode, is bypassed under `acceptEdits`
   (A2).
4. **Interface is backend-neutral:** the capability matrix shows the Claude Code
   CLI slotting in via capabilities with **no core-interface change** (A5).
5. **No regressions:** `go build/vet/test ./...` pass, including persistence-off
   paths and the untouched run monitor.

## Design Decisions & Rationale

Resolved by the upstream `scope_assess` + `clarify` steps for this workflow.

1. **This spec is slice (A) only — foundational seam extraction, zero behavior
   change.** The full effort sized as too large and was split three ways; A is the
   smallest reviewable slice and de-risks B and C by landing a clean,
   behavior-preserving foundation first.
2. **Claude Code CLI is the design/validation target, not a built backend.** Same
   vendor, different transport (subprocess + stream-json vs in-process SDK), so it
   exercises the degradation model (stdio MCP, allow-list tool approval) at the
   lowest risk — enough to prove the interface isn't Claude-SDK-shaped without
   committing to a second vendor's semantics.
3. **Thin interface + optional capabilities, not a framework.** Matches idiomatic
   Go and the codebase's existing `Executor` inversion; avoids importing a
   heavyweight agent loop that would fight jig's engine/DAG.
4. **Normalize, don't merely wrap.** SDK shapes on `step.Result` /
   `engine.StepRequest` are converted to jig-owned representations at the harness
   boundary, because a wrapper that still exposes SDK fields would not actually let
   a second backend swap in.

## Open Questions

Non-blocking; record and resolve during implementation.

1. **Package location of the `Harness` interface** — `internal/harness` (new
   package) vs `internal/engine` (alongside `Executor`). Leaning toward a dedicated
   package so the SDK dependency is isolated, but either satisfies the goals.
2. **Shape of the capability descriptor** — a struct of bools, a set of interface
   type-assertions, or both (a descriptor for cheap queries + type-assertions for
   invocation). The CLI precedent (`capabilities` string array) suggests a
   string/enum set is also viable.
3. **Naming of the concrete Claude harness type** (e.g. `ClaudeSDKHarness` /
   `SDKHarness` / `claudesdk.Harness`) — cosmetic, settle in review.
4. **How the runner obtains its `Harness`** (constructor injection vs a small
   factory) — an implementation choice constrained only by "no SDK import in
   `agent.go`."
