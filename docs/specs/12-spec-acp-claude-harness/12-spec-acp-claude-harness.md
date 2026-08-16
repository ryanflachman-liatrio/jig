# 12-spec-acp-claude-harness.md

**Supersedes:** `docs/specs/harness-abstraction/harness-abstraction.md` (Spec A)
and `docs/specs/cursor-harness-implementation/spec.md` — both pre-research
drafts. This spec keeps their foundational `Harness`/capability design (still
valid) but changes the proof target and delivery shape: prove the **Agent
Client Protocol (ACP)** works at all, standalone, against **Claude** (the
backend jig already trusts) before wiring anything into jig's engine — and
build the ACP plumbing as its **own Go module** so the wire-protocol
dependency stays isolated until it has earned its way into the main tree.
Cursor CLI (and any other ACP-native backend) is explicitly deferred to a
follow-up spec once this one proves the seam.

## Introduction/Overview

jig is bound to the pre-1.0 Claude Agent SDK
(`github.com/severity1/claude-agent-sdk-go`, alias `claudecode`) through direct
imports in `internal/runner/agent.go`. This spec introduces ACP — an open,
JSON-RPC-based standard for orchestrator-to-agent communication — as an
**alternative transport to the same backend jig already runs (Claude)**, so
the entire ACP integration (handshake, streaming updates, permission
round-trip) can be verified end-to-end without also betting on a second
vendor's CLI in the same slice.

Verification happens in two phases, matching how the work will actually be
built:

1. **A standalone module, no jig dependency.** Using
   `github.com/coder/acp-go-sdk` (the official-adjacent, actively maintained
   Go SDK — 219 stars, Apache-2.0, tagged releases, built by Coder) and
   following the pattern in its own `example/claude-code`, spawn
   `npx -y @zed-industries/claude-code-acp@latest` (Zed's official Node
   adapter that bridges ACP to the Claude Agent SDK) as a subprocess and drive
   a real prompt/tool-call/permission round-trip. This is a spike: its only
   job is to prove ACP's `session/request_permission` genuinely gates tool
   execution and that `session/update` carries everything jig's transcript
   model needs — before any jig code depends on it.
2. **Wire the proven plumbing into jig** behind the same jig-owned
   `Harness`/capability seam already designed in the superseded drafts, so
   `JIG_HARNESS=acp` runs existing workflows through ACP→Claude with no TUI
   changes (the run monitor is transcript-only and backend-agnostic already).

This ordering directly answers the two hazards research surfaced for ACP in
general:

1. **"ACP support" does not imply enforcement.** `session/request_permission`
   is a synchronous JSON-RPC request an agent *can* fire before every tool
   call, but the spec does not force an implementation to actually
   round-trip it (a real, shipping ACP agent — GitHub Copilot CLI, public
   preview — has been documented auto-approving instead). Phase 1's spike
   exists specifically to produce **direct, first-party evidence** — not
   vendor marketing — that the Claude adapter's permission round-trip really
   blocks tool execution, before jig's fail-closed Guard is allowed to trust
   it.
2. **No official Go SDK for ACP.** `coder/acp-go-sdk` is the best available
   (materially more active than the alternatives found in research —
   `ironpark/acp-go` at ~30 stars, `joshgarnett/agent-client-protocol-go`
   with no tag since Sept 2025) but is still a young, single-vendor-backed
   dependency for jig's core orchestration path. Isolating it in its own Go
   module (its own `go.mod`) means the dependency tree, and the risk if it
   proves unmaintained, stays contained and swappable without touching
   `internal/harness`'s consumers.

## Goals

- Build a standalone, jig-independent Go module that uses
  `coder/acp-go-sdk` to drive Claude over ACP (via
  `npx @zed-industries/claude-code-acp`), proving the full turn lifecycle —
  `initialize` → `session/new` → `session/prompt` → streamed
  `session/update` → a real `session/request_permission` round-trip that
  actually gates a tool call.
- Define a jig-owned `Harness`/`Session` interface and an explicit,
  pre-`Open`-queryable `CapabilitySet` in `internal/harness`, so
  `AgentExecutor` depends on a jig type instead of calling the Claude SDK
  directly.
- Extract today's direct Claude Agent SDK usage into a `ClaudeHarness`
  (behavior-preserving reference implementation) so the interface is proven
  against the backend jig already runs, with zero observable change.
- Wire the verified standalone module into `internal/harness` as `AcpHarness`
  (ACP-mediated Claude, via the npx-spawned adapter), implementing a real
  fail-closed permission round-trip and mapping `session/update` into jig's
  existing `internal/transcript` block model.
- Make the harness selectable via `JIG_HARNESS` (`claude` | `acp`), so the
  existing TUI/run monitor drives an ACP-backed run with **no TUI code
  changes** — the run monitor already reads only the transcript.

## User Stories

**As a jig maintainer**, I want to prove ACP actually works — including that
its permission round-trip is a real veto, not just a documented one — against
a backend I already trust, before committing any jig engine code to depend on
it.

**As a jig maintainer**, I want the ACP wire-protocol dependency
(`coder/acp-go-sdk`) isolated in its own Go module, so if it proves
unmaintained or the protocol churns, the blast radius is one module, not the
whole `internal/harness` tree.

**As a jig maintainer**, I want the agent runner to depend on a jig-owned
`Harness` interface instead of calling the Claude SDK directly, so that a
future ACP-native backend (Cursor CLI, Gemini CLI) is a new subprocess target
for `AcpHarness`, not a re-plumb of the runner or security layer.

**As a jig operator**, I want to run a workflow with `JIG_HARNESS=acp` and see
the same TUI, the same transcript shape, and the same fail-closed security
guarantee for guarded steps as the default Claude SDK path.

## Demoable Units of Work

### Unit 1: Standalone ACP↔Claude spike module (no jig dependency)

**Purpose:** Prove ACP works — handshake, streaming, and a *real* permission
veto — against Claude, in complete isolation from jig's codebase. This is the
evidence the rest of the spec depends on.

**Functional Requirements:**
- The system shall create a new, independently-versioned Go module (its own
  `go.mod`, e.g. `harness/acp/go.mod`) depending on
  `github.com/coder/acp-go-sdk`, with zero dependency on jig's `internal/`
  packages.
- Following the SDK's own `example/claude-code` pattern, the module shall
  spawn `npx -y @zed-industries/claude-code-acp@latest` as a subprocess and
  establish an ACP client-side connection over its stdio
  (`acp.NewClientSideConnection`), performing `Initialize` → `NewSession` →
  `Prompt`.
- The module shall implement `acp.Client`'s `RequestPermission` to make a
  real, programmatic allow/deny decision (not just auto-approve), and
  `SessionUpdate` to capture the full stream of message chunks, thought
  chunks, and tool-call/tool-call-update events for a scripted turn that is
  known to invoke at least one tool.
- The module shall demonstrate, with a recorded run, that a `RequestPermission`
  **deny** decision actually prevents the corresponding tool from executing
  (not merely that the callback fired) — the specific fact Copilot CLI's
  documented auto-approve bug shows cannot be assumed from "ACP support"
  alone.

**Proof Artifacts:**
- CLI run log: a full ACP session transcript (raw JSON-RPC or a readable
  rendering of it) showing `initialize`, `session/new`, `session/prompt`,
  a `session/update` stream, and a `session/request_permission` round-trip.
- Security proof: a run where the permission decision is `deny` and the
  corresponding tool call's result confirms it did not execute — the
  first-party evidence Goal 1 requires.
- `go build`/`go test` pass for this module independently of the root jig
  module.

### Unit 2: Core `Harness`/`Session` interface + capability model

**Purpose:** Establish the jig-owned seam both the existing Claude path and
the new ACP path will run through. Type-design only; no runner behavior
changes yet.

**Functional Requirements:**
- The system shall define a jig-owned `Harness` interface (`Name()`,
  `Capabilities() CapabilitySet`, `Open(ctx, SessionSpec) (Session, error)`)
  and a `Session` interface (`Messages() <-chan Event`, `Send(ctx, ToolResult)
  error`, `Close() error`) in a new `internal/harness` package, with no
  `claudecode` (or ACP module) import in the interface-defining file.
  **Vocabulary** (see `CONTEXT.md`): **Harness** names jig's Go type
  (`ClaudeHarness`, `AcpHarness`); **backend** names the vendor/model/CLI it
  targets (Claude, Cursor, Gemini) — the two are not synonyms, since one
  Harness could in principle target more than one backend later.
- A `Session` is live for exactly one `AgentExecutor.Execute` call — one
  `Open()` per step-execution, not a long-lived handle reused across steps.
  Mid-turn human input (an `AskUserQuestion` answer, a queued tool result)
  goes to the *same* live `Session` via `Send()`, exactly as today's in-process
  MCP flow already does (`agent.go`'s `buildAskUserQuestionServer`) — no
  reconnect. Resuming a stopped step (`block_on`, Stop/Resume) is a **separate,
  pre-existing** mechanism: `AgentExecutor` calls `Open()` again with
  `SessionSpec.Resume` set to the prior conversation ID, and the harness
  reconnects — this already happens today via `WithResume` +
  `WithContinueConversation` (`agent.go:53-58,83-84`) and this spec preserves
  that cost exactly, it does not introduce or worsen it.
- The system shall define `CapabilitySet` as an explicit set queryable
  **before** `Open` is called — never inferred via runtime type assertion
  after the fact — covering at minimum: `CapPermissionCallback` (per-call
  Tier-1 firewall), `CapInProcessMCP` (the `AskUserQuestion` tool),
  `CapSessionResume`, `CapStructuredOutput`, `CapPartialStreaming`.
- The system shall define `SessionSpec` (jig-owned session options: prompt,
  model, effort, turn/thinking/budget limits, permission mode, allowed/
  disallowed tools, cwd, plus capability-gated fields: `Permission`,
  `MCPServers`, `Resume`, `Schema`, `Partial`) replacing the SDK's
  functional-option list.
- The system shall define `PermissionFn` — a jig-owned callback type in
  `internal/harness` (e.g. `func(toolName string, input map[string]any)
  Decision`) — as the type of `SessionSpec.Permission`. `AgentExecutor`
  constructs a `PermissionFn` by closing over the step's `*sentinel.Guard`
  (`req.Guard.Check`); `internal/harness` itself never imports `sentinel`,
  mirroring the existing `engine`/`runner` dependency-inversion idiom.
- A harness that receives a capability-gated `SessionSpec` field it did not
  advertise MUST return an error from `Open` rather than silently ignore it
  (defensive symmetry against false-capability escalation).

**Proof Artifacts:**
- Compilation artifact: `internal/harness/harness.go` + `capability.go`
  compile with zero SDK/ACP imports.
- Fake harness: `internal/harness/fake.go` (configurable `CapabilitySet` +
  scripted `Messages()`) used by runner/engine tests without any real backend.

### Unit 3: `ClaudeHarness` — extract the existing SDK path, behavior-preserving

**Purpose:** Prove the interface against the backend jig already runs today,
with zero observable behavior change, before adding the ACP path alongside it.

**Functional Requirements:**
- The system shall provide `ClaudeHarness`, wrapping
  `github.com/severity1/claude-agent-sdk-go`, translating `SessionSpec` into
  the SDK's functional options and lifecycle (`NewClient`/`Connect`/
  `QueryStream`/`ReceiveMessages`/`Disconnect`).
- `ClaudeHarness` shall implement the permission-callback capability via
  `WithCanUseTool`, preserving today's guard coupling exactly: forcing
  `PermissionModeDefault` when a `Permission` fn is present so the callback
  actually fires (`acceptEdits` still bypasses it, unchanged).
- `ClaudeHarness` shall implement the in-process MCP capability
  (`WithSdkMcpServer("jig", …)` for `AskUserQuestion`, preserving the
  `mcp__jig__AskUserQuestion` name rewrite), resume, and structured output,
  advertising all five capabilities.
- `AgentExecutor` (`internal/runner/agent.go`) shall hold a `harness.Harness`
  and contain **zero** direct `claudecode` calls; `agent.go:38-114`'s
  message-capture logic moves behind `ClaudeHarness`, returning a finished
  `*step.Result` the executor writes to the transcript exactly as today.

**Proof Artifacts:**
- Test run: existing `internal/runner` agent-step tests pass unchanged.
- Grep artifact: `claudecode` imported only by `internal/harness/claude.go`
  (plus the already-deferred `internal/runner/monitor.go` and dead `tui`
  chat) — zero times in `agent.go`.
- Full-run artifact: a real workflow run produces a `transcript.jsonl`
  structurally identical to a pre-refactor run of the same workflow.

### Unit 4: `AcpHarness` — wire the verified spike into jig

**Purpose:** Turn Unit 1's proof into a real, selectable jig backend, and
prove the fail-closed Guard survives a genuinely different transport.

**Functional Requirements:**
- The system shall provide `AcpHarness` in `internal/harness`, depending on
  the Unit 1 module — module path `jig/harness/acp`, directory `harness/acp`
  — (via a Go module `require` — a `replace jig/harness/acp => ./harness/acp`
  directive during development, a real version once published/tagged) to
  spawn `npx @zed-industries/claude-code-acp` and speak ACP to it (see
  ADR 0010 for why this dependency is isolated in its own module, and
  ADR 0011 for why the ACP path goes through Zed's npx adapter rather than a
  custom Go bridge).
- Resetting a run mid-flight through `AcpHarness` requires **no ACP-specific
  handling**: context cancellation → `Session.Close()` kills the ACP
  subprocess exactly as it does for any other harness, and the replayed step
  gets a fresh `Open()` like any other harness. `Reset`'s existing semantics
  (`CONTEXT.md`) are harness-agnostic by construction.
- `AcpHarness.Open` shall map `SessionSpec` onto ACP's session creation
  (`cwd`, `mcpServers` where `CapInProcessMCP`-gated fields are present) and
  shall reject `Open` if a `SessionSpec` carries a capability-gated field
  `AcpHarness` does not advertise.
- `AcpHarness` shall implement the permission-callback capability as the
  **same real, synchronous** `RequestPermission` round-trip proven in Unit 1:
  on receiving the request, invoke the jig `PermissionFn` and reply
  allow/deny per its `PermissionDecision`.
- `AcpHarness` shall translate the `session/update` stream (message chunks,
  thought chunks, tool-call/tool-call-update) into jig's normalized
  `Event`/transcript blocks (`BlockText`, `BlockThinking`, `BlockToolUse`,
  `BlockToolResult`), so the run monitor requires no changes.
- `AcpHarness.Capabilities()` shall advertise `CapPermissionCallback` (real,
  per above) and shall advertise `CapInProcessMCP`, `CapSessionResume`,
  `CapStructuredOutput`, `CapPartialStreaming` only if this unit implements
  each — any not implemented in this slice MUST be omitted (not stubbed to
  `true`), so the fail-closed gate in Unit 5 correctly rejects steps needing
  them.
- The system shall select `AcpHarness` via `JIG_HARNESS=acp`
  (`internal/harness/select.go`, `FromEnv()`), failing fast at startup on an
  unknown value; `JIG_HARNESS=claude` (or unset) stays the default.

**Proof Artifacts:**
- TUI artifact: running jig's existing TUI/run monitor against a workflow
  with `JIG_HARNESS=acp` produces a normal-looking run — same step list, same
  transcript viewer, same navigation — proving the "wire it into the TUI"
  goal, with **no changes to `internal/tui`**.
- Security test: a guarded step run with `JIG_HARNESS=acp` proves the
  permission fn is invoked and an `Allow: false` decision actually blocks
  the tool from executing — the same guarantee Unit 1 proved standalone, now
  reproduced through jig's own `Guard`.
- Grep artifact: the ACP module is imported only within
  `internal/harness`'s ACP-backend file(s), not by `engine`, `runner`'s other
  executors, or `tui`.

### Unit 5: Fail-closed Guard + capability gating in `AgentExecutor`

**Purpose:** Make the fail-closed invariant explicit, generic across
harnesses, and tested — not implicit in each harness's own behavior.

**Functional Requirements:**
- `AgentExecutor.Execute` shall reject a step with `req.Guard != nil` before
  calling `Open` when `h.Capabilities().Has(CapPermissionCallback)` is false,
  returning a clear, harness-named error (fail-closed, not warn-and-degrade).
- The same gating pattern shall apply to `AskUserQuestion` steps (require
  `CapInProcessMCP`), session resume (`CapSessionResume`), and structured
  output (`CapStructuredOutput`) — each rejected with a clear error, not
  silently skipped, when the active harness lacks the capability.
- **`block_on` is core to jig's deterministic-workflow guarantee, so its
  capability check MUST be fail-fast, not lazy.** For a step whose config
  declares `block_on`, `AgentExecutor` shall check `CapSessionResume` at that
  step's **first** `Open()` call and reject immediately if absent — not wait
  until the `block_on` pause actually fires later in the step's execution,
  which would otherwise let a step run to the point of needing a resume the
  active harness cannot perform, losing in-flight work.
- `AgentExecutor` shall set each capability-gated `SessionSpec` field only
  after confirming the harness advertises the matching capability.

**Proof Artifacts:**
- Guard-semantics tests: (a) guarded step + `ClaudeHarness` → callback fires
  under default mode; (b) guarded step + a fake harness lacking
  `CapPermissionCallback` → fail-closed rejection with the expected error;
  (c) `acceptEdits` step → callback bypassed (unchanged); (d) guarded step +
  `AcpHarness` → the real permission round-trip from Unit 4 fires.
- `go build ./... && go vet ./... && go test ./...` pass across both modules
  (root jig module and the nested `harness/acp` module).

## Non-Goals (Out of Scope)

1. **Cursor CLI, Gemini CLI, or any other ACP-native backend.** This spec
   proves ACP against Claude only. Other backends are a follow-up spec once
   this seam is proven — the interface is designed to admit them, but they
   are not built here.
2. **ACP v2.** The draft, breaking v2 protocol is out of scope; `AcpHarness`
   targets ACP v1 (what `coder/acp-go-sdk` and `@zed-industries/claude-code-acp`
   ship today).
3. **The `harness` TOML selector.** No `harness` field is added to
   `workflow.Step`/`Defaults`; selection stays `JIG_HARNESS` env-var only,
   confined to `select.go`.
4. **Publishing the standalone `harness/acp` module** to a real module proxy
   or separate repository. It lives nested in this repo with its own
   `go.mod`, wired via `replace` for this slice; extracting it to a separate
   repo is a future option, not required here.
5. **Migrating the Tier-2 `MonitorAdapter`** (`internal/runner/monitor.go`)
   or the dead `tui` chat off the SDK. Both stay on their direct-SDK path.
6. **A remote/HTTP ACP transport.** Only the local subprocess + stdio
   transport (what `coder/acp-go-sdk` and the Claude adapter actually support)
   is in scope.
7. **Any user-visible behavior change to the existing Claude SDK path**
   beyond the internal seam extraction — same transcripts, same run-monitor
   figures, same workflow outcomes for `JIG_HARNESS=claude` (the default).

## Design Considerations

No UI/UX change. The run monitor (`internal/tui/monitor.go`) reads only the
transcript and must require no changes for Unit 4's TUI proof artifact to
hold; its rendered token/cost figures for the Claude SDK path are the
acceptance signal for "zero behavior change" on Unit 3. ACP-path token/cost
figures are new territory (ACP's `usage_update`, if the Claude adapter emits
it, has no jig analog yet) — populate `step.Result` fields as `nil`/zero
where ACP does not report them, matching the existing "nil cost pointer means
not reported" convention rather than inventing a value.

## Repository Standards

- **Consumer-defined interfaces / dependency inversion**, mirroring
  `engine.Executor`: `Harness` lives in its own package; `engine` continues
  to import neither the SDK nor `harness`'s concrete backends.
- **Thin core interface + explicit capability set**, not runtime type
  assertion and not a heavyweight agent framework — matches the codebase's
  existing idiom and the already-drafted `harness-abstraction` design.
- **The transcript is the normalization boundary.** Reuse
  `internal/transcript`'s existing Entry/Block model; do not invent a
  parallel normalized model for ACP.
- **Table-driven tests** with inline fixtures, covering happy path and
  degradation (capability absent → fail-closed) per harness.
- **Comments explain the non-obvious "why"** — especially why ACP "support"
  does not imply "permission enforcement," and why Unit 1's standalone proof
  exists before any jig code trusts the round-trip.
- **Persistence-off (`runDir == ""`) stays a first-class no-op path**,
  unchanged by this refactor for both harnesses.
- **Nested Go modules are new to this repo.** `harness/acp/go.mod` is the
  first; wire it into the root module via a `replace` directive in the root
  `go.mod` during development, and document the pattern (module path
  convention, why it's isolated) since it's a precedent for future backends.

## Technical Considerations

- **Standalone module dependency:** `github.com/coder/acp-go-sdk`
  (`go get github.com/coder/acp-go-sdk@v0.13.5` or later), Apache-2.0,
  219 stars, tagged releases — the most active Go ACP implementation found
  in research, though still young and single-vendor-backed. Confined to the
  nested `harness/acp` module; the root jig module depends on it transitively
  only through `internal/harness`'s ACP-backend file(s).
- **The Claude bridge is Zed's own adapter, not jig's:**
  `npx -y @zed-industries/claude-code-acp@latest` is a Node package that
  itself wraps the (TypeScript) Claude Agent SDK and re-exposes it over ACP.
  This means the ACP path talks to Claude through one extra process hop and
  one extra maintained dependency (Zed's adapter) versus jig's existing
  direct Go SDK call — acceptable for this proof-of-plumbing slice, revisit
  if that hop proves costly (latency, npx cold-start, Node availability) in
  Unit 1's spike.
- **`npx` / Node.js availability is a new runtime dependency** for the ACP
  path only — `JIG_HARNESS=claude` (default) has no such requirement. Unit 1
  must fail with a clear error if `npx` is unavailable rather than hanging.
- **ACP wire protocol:** JSON-RPC 2.0 over newline-delimited stdio to a
  subprocess. Handshake: `initialize` (negotiate `protocolVersion`,
  client/agent capabilities) → `session/new` (cwd, optional `mcpServers`) →
  `session/prompt` (turn start; completion reported via async
  `session/update` notifications, not the RPC return) → `session/cancel` as
  needed.
- **Permission model:** `session/request_permission` is an agent-initiated
  JSON-RPC *request* (not a notification), carrying `sessionId` and a
  `toolCall`; the client must reply with an allow/deny outcome. Unit 1 exists
  specifically to produce first-party proof (not vendor claims) that the
  Claude adapter's round-trip is a genuine synchronous veto before Unit 4
  lets jig's Guard rely on it.
- **Reference implementations for protocol fidelity:** when in doubt about
  ACP semantics not fully covered by `coder/acp-go-sdk`'s docs, cross-check
  against Zed's own canonical implementations — the TypeScript/Node SDK
  (used by `@zed-industries/claude-code-acp` itself) and the Rust reference
  implementation (`agent-client-protocol` crate) — both under the
  `agentclientprotocol`/`zed-industries` GitHub orgs.
- **Load-bearing SDK behaviors that must survive the wrap (Claude SDK
  path):** `WithCanUseTool` + `PermissionModeDefault` forcing;
  `WithSdkMcpServer` in-process `AskUserQuestion`; `WithResume`/
  `WithContinueConversation`; JSON-Schema structured output;
  `include_partial_messages` streaming.
- **Deliberate deviation from "just adopt the standard protocol wholesale":**
  ACP does not guarantee the fail-closed security invariant jig depends on —
  jig's own Guard/capability-gating layer stays the source of truth, with
  ACP treated as a transport underneath it, proven first in isolation
  (Unit 1) before it is trusted (Unit 4/5).

## Security Considerations

- **The Tier-1 Guard must not weaken for either backend.** Unit 5's
  fail-closed gate is the enforcement point; Unit 4's `AcpHarness` must
  reuse the *real* permission round-trip proven in Unit 1 (not a stub that
  always allows), re-verified by a dedicated test routed through jig's own
  `Guard`.
- **Per-backend verification is a first-class requirement, not an
  afterthought.** "The agent advertises ACP support" is explicitly
  insufficient evidence of enforcement (per the documented Copilot CLI
  auto-approve bug); `CapPermissionCallback` on `AcpHarness` is only set
  `true` because Unit 1's standalone test and Unit 4's re-verification both
  prove the round-trip blocks execution on deny — not because a vendor's
  docs say ACP is supported.
- **Subprocess trust boundary:** both `harness/acp` (standalone) and
  `AcpHarness` (wired-in) spawn and communicate with local subprocesses
  (`npx`, then the Node adapter, then presumably Claude's own credentials)
  over stdio; no new network endpoints are introduced by jig itself. Existing
  Claude credentials/environment are passed through the subprocess
  environment, not persisted or logged by jig.
- **`AskUserQuestion` stays in-process for the Claude SDK harness only** in
  this slice; if `AcpHarness` does not implement `CapInProcessMCP`, such
  steps are rejected (Unit 5), not silently run without the tool.

## Success Metrics

1. **ACP is proven, standalone, before it's trusted:** Unit 1's spike module
   demonstrates a full turn and a real permission-deny-blocks-execution
   result against Claude, independent of jig.
2. **Behavior identical on the default path:** `JIG_HARNESS=claude` (or
   unset) produces transcripts and run-monitor token/cost figures identical
   to today's pre-refactor behavior.
3. **A real second path works, wired into the existing TUI:**
   `JIG_HARNESS=acp` runs an existing workflow end-to-end through jig's TUI
   with no `internal/tui` changes, producing a well-formed transcript.
4. **Guard is fail-closed and backend-verified, not backend-assumed:** the
   permission round-trip test for `AcpHarness`, run through jig's own
   `Guard`, passes.
5. **Dependencies confined:** `claudecode` is imported only by
   `internal/harness/claude.go` (+ the explicitly deferred `monitor.go` /
   dead `tui` chat); `coder/acp-go-sdk` is imported only within the nested
   `harness/acp` module and `internal/harness`'s ACP-backend file(s).
6. **No regressions:** `go build/vet/test ./...` pass in both modules,
   including persistence-off paths and the unchanged run monitor.

## Open Questions

Non-blocking; record and resolve during implementation.

1. ~~**Nested module path/versioning scheme**~~ — **Resolved** during the
   grill-with-docs session on this spec: module path `jig/harness/acp`,
   directory `harness/acp`, wired via a root-`go.mod` `replace` directive
   during development (ADR 0010). Whether it ever gets tagged/published
   independently is still a later decision, not this spec's.
2. **`npx` cold-start latency** — spawning `@zed-industries/claude-code-acp`
   via `npx -y` re-resolves the package on first run; whether jig should
   document a pre-pull step (e.g. `npm install -g` or a pinned version) is
   left to Unit 1's findings.
3. **Credential sourcing for the Claude adapter** — assume it inherits
   whatever Claude credentials/environment the adapter itself expects
   (same as running `claude-code-acp` directly), unless Unit 1 finds
   otherwise.
4. **Token/cost accounting for the ACP path** — left as zero/nil this
   slice per Design Considerations; revisit if cost tracking for
   non-direct-SDK backends becomes a requirement.
5. **Whether `AcpHarness` should support session resume/structured
   output/partial streaming in this slice** — Unit 4 only requires
   advertising what's actually implemented; implement opportunistically if
   the Claude adapter makes it easy, but do not block the slice on it.

## Related Records

- **`CONTEXT.md`** — "Harness abstraction" section: canonical definitions of
  Harness, backend, Session, Capability/`CapabilitySet`, `PermissionFn`.
- **ADR 0010** (`docs/adr/0010-nested-go-module-for-harness-acp.md`) — why
  `coder/acp-go-sdk` is isolated in its own Go module.
- **ADR 0011** (`docs/adr/0011-acp-via-zed-npx-adapter.md`) — why the ACP path
  goes through Zed's npx adapter rather than a custom Go bridge.
