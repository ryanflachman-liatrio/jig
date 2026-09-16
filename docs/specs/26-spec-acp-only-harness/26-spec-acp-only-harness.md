# 26-spec-acp-only-harness.md

**Supersedes:** nothing directly, but narrows and closes out the deferrals in
`docs/specs/12-spec-acp-claude-harness/12-spec-acp-claude-harness.md` (Non-Goal
5: "Migrating the Tier-2 `MonitorAdapter` ... or the dead `tui` chat off the
SDK. Both stay on their direct-SDK path.") and
`docs/specs/14-spec-per-step-harness/14-implementation-plan.md` (which added
per-step `backend`/`transport` selection but explicitly left `MonitorAdapter`
and `tui/chat` outside the `Harness` seam). This spec's job is to finish what
those two left open: make ACP the *only* way jig talks to any agent backend,
anywhere in the tree.

## Introduction/Overview

jig's `internal/harness` package already has a working `Harness` abstraction
with four implementations — `ClaudeHarness` (native
`github.com/severity1/claude-agent-sdk-go`, alias `claudecode`), `AcpHarness`,
`CursorHarness`, and `CodexHarness` (the latter three all ACP-backed). Adding
a harness today means adding a fifth vendor-specific adapter behind the same
seam — which is exactly the cost this spec wants to stop paying. The
`Harness` interface itself is not the problem; **`ClaudeHarness` being one of
its members is.**

But `claudecode` is not confined to the harness seam. Research for this spec (recorded inline below) found the native SDK imported
in three independent places:

1. **`internal/harness/claude.go`** — `ClaudeHarness`, the per-step
   execution path selected via `internal/harness/select.go`'s
   `For(backend, transport)`. This is the only one of the three already
   routed through the `Harness` interface.
2. **`internal/runner/monitor.go`** — `MonitorAdapter`, the Tier-2
   security-classifier ("sentinel") fleet. Calls `claudecode.NewClient`
   directly (`monitor.go:43,84`), bypassing `Harness` entirely. Depends on
   `WithJSONSchema` (API-enforced structured output, `monitor.go:67`), a
   fully empty/deny-all tool surface (`monitor.go:55-61`), and
   `WithMaxTurns(1)` single-shot dispatch — no session reuse across calls
   (`monitor.go:84-88`).
3. **`internal/helpchat/*`** — the in-TUI help-chat assistant, a live
   feature wired into `internal/tui/monitor`. Uses
   `claudecode.CreateSDKMcpServer("jig-help", ...)`
   (`internal/helpchat/tools.go:38-49`) to register 10 **in-process Go
   closures** as tools (`workflow_snapshot`, `read_step_transcript`,
   `read_step_result`, `read_step_output`, `recover_step`, `reset_step`,
   `stop_step`, `resume_step`, `resolve_review`, `ask_user`) that close
   directly over live `*engine.Run` state, a `tea.Msg` dispatch function, and
   a blocking rendezvous channel for the `resolve_review`/`final_merge` and
   `ask_user` cases (`tools.go:288-323,378-398`).

There is also `internal/tui/chat/*`, which also imports `claudecode` but is
confirmed dead code — unreferenced by any live package — and can simply be
deleted, not migrated.

These three surfaces are not equally hard to migrate:

- **`ClaudeHarness` → deleted, `AcpHarness` becomes the only Claude path.**
  `AcpHarness` already exists, already advertises `CapPermissionCallback`
  (real, proven in spec 12 Unit 4) and `CapStructuredOutput` (prompt-injected
  and parsed — see below), and is already the selected harness for
  `cursor`/`codex` backends. This is a deletion-and-rewiring job.
- **`MonitorAdapter` → moderate.** `AcpHarness`'s "structured output"
  capability is a *prompt-injected schema, parsed out of free text*
  (`internal/harness/acp.go`'s `appendSchemaPrompt`), not the SDK's
  API-enforced `WithJSONSchema`. `MonitorAdapter`'s decoder
  (`decodeMonitorVerdict`, `monitor.go:138-155`) currently uses
  `json.Decoder.DisallowUnknownFields()` against a guaranteed schema-shaped
  response; moving to ACP means tolerating model output variance (stray
  prose around the JSON block, occasional malformed responses) since there
  is no wire-level enforcement. Spec 12 already flagged JSON-Schema
  structured output as a "load-bearing SDK behavior" fidelity risk when ACP
  was first introduced (12-spec, "Technical Considerations") — this spec is
  where that risk actually gets paid down for the one caller that depends on
  it most heavily (a security-classification gate).
- **`helpchat` → hard, a real architectural gap.** ACP's `McpServer` wire
  type is **out-of-process only**: `coder/acp-go-sdk`'s `McpServerStdio`
  struct (`command`, `args`, `env`, `name`) is the transport every agent must
  support, and it is the *agent* process (Zed's Claude adapter,
  `cursor-agent acp`, the Codex App Server) that spawns the named
  `command`/`args` as its own child and connects to it as an MCP client —
  jig itself never spawns anything named in that struct directly. There is
  no protocol analog to `CreateSDKMcpServer`'s in-process Go closures.
  `AcpHarness` today always sends an empty `McpServers` list
  (`internal/harness/acp_config.go` / `conn.go`) because nothing has ever
  needed to register a custom tool over ACP, and jig has no existing
  self-exec-subprocess or local-IPC precedent anywhere (`cmd/jig/main.go`
  dispatches only user-facing subcommands today). Migrating `helpchat`
  means **standing up a real local MCP server component** — a new hidden
  `jig mcp-serve` subcommand the agent process spawns, which phones home
  over a loopback TCP socket to the main jig process holding `*engine.Run`
  state — and redesigning the synchronous `ask_user`/`final_merge`
  rendezvous — currently a same-process Go channel — to survive that IPC
  boundary. Nothing in spec 12 or `docs/cursor-acp-research.md` claims this
  path exists; it was designed during this spec's grilling session (see
  Unit 3).

Given that spread, this mega-spec is written as five dependency-ordered
units, each independently demoable and each a plausible standalone SDD
workflow once split out. Units 1 and 2 remove the SDK from the two paths
where the migration is mechanical; Units 3 and 4 do the real design work for
`helpchat`; Unit 5 is the final closeout (dependency removal, doc updates,
grep-clean verification) that only becomes possible once 1-4 land.

## Goals

- Make `AcpHarness` the only Claude-targeting `Harness` implementation;
  delete `ClaudeHarness` and the now-unneeded `sdk` transport value.
- Move `MonitorAdapter`'s classification calls onto an ACP-backed session,
  with a decoder that tolerates ACP's prompt-injected (not wire-enforced)
  structured output without weakening the sentinel fleet's actual verdict
  correctness.
- Design and build an out-of-process MCP server that exposes `helpchat`'s 10
  tools, replacing `CreateSDKMcpServer`'s in-process registration, with a
  request/response contract for the blocking `ask_user`/`final_merge` cases
  that survives the new process boundary.
- Wire `helpchat` onto `AcpHarness` + the new MCP server component.
- Remove `github.com/severity1/claude-agent-sdk-go` from `go.mod` entirely,
  and delete dead `internal/tui/chat`, once nothing imports `claudecode`.
- Update `docs/ARCHITECTURE.md`, `CONTEXT.md`, and `AGENTS.md` so "backend"
  and "harness" vocabulary no longer implies a transport choice — ACP is not
  *a* transport among several, it is *the* transport.

## User Stories

**As a jig maintainer**, I want every agent backend jig talks to — Claude for
step execution, Claude for security classification, Claude for the help-chat
assistant — to go through the same ACP transport, so adding a new backend or
auditing the security boundary means touching one seam, not three.

**As a jig maintainer**, I want the sentinel fleet's ACP migration to be
honest about what it loses (wire-enforced schema) and what it keeps (the
same fail-open, disable-that-monitor-and-continue behavior every other
classifier failure already gets), so the security gate's actual guarantees
are documented, not assumed to be stricter than they really are.

**As a jig maintainer**, I want `helpchat`'s tool surface to survive the move
to an out-of-process MCP server without breaking the in-TUI recover/reset/
resume/ask-user flows a run operator depends on mid-incident.

**As a jig operator**, I want no observable change to how workflows run,
how the sentinel fleet gates risky steps, or how help-chat behaves, once this
migration lands — the payoff is internal (one transport, not three), not a
new feature.

## Demoable Units of Work

### Unit 1: Delete `ClaudeHarness`; `AcpHarness` becomes the only Claude path

**Purpose:** Close the one migration that has zero design risk — `AcpHarness`
already exists and already covers what per-step execution needs. This is the
visible, low-risk win and does not block or get blocked by Units 2-4.

**Functional Requirements:**
- Delete `internal/harness/claude.go` and `internal/harness/claude_test.go`.
- `internal/harness/select.go`'s `For(backend, transport)` shall drop the
  `sdk`/`""` transport branch; `backend == "claude"` resolves directly to
  `AcpHarness`.
- The `Transport` field is deleted entirely, not merely its `sdk` value.
  Confirmed during grilling: `TransportACP` is already the *only* value ever
  used or permitted for `cursor`/`codex` (`validate.go:232-233`,
  `select.go:18-27`), and once `TransportSDK` is gone it is the only value
  possible for `claude` too — nothing in `docs/plans/a4-cursor-harness-parity.md`
  or `docs/plans/open-goals.md` plans a second transport for any backend.
  A single-valued field conveys no information, so selection collapses to
  backend-only: remove `Transport` from `Step`/`Defaults` in
  `internal/workflow/schema.go` (`schema.go:94-95,106-111,381,458`), the
  transport-resolution branch in `internal/workflow/load.go`
  (`load.go:202-210`), the transport-validity checks in
  `internal/workflow/validate.go` (`validate.go:229-233`), and the
  transport parameter of `internal/harness/select.go`'s `For` (`select.go:8-29`,
  becomes `For(backend string) (Harness, error)`). Also remove `Transport`
  from `harness.SessionSpec` and update every caller (`AgentExecutor`,
  `internal/ops/control.go:149`) to the single-argument `For`.
- Any capability assumptions in `internal/runner/agent_test.go` that assumed
  `ClaudeHarness`'s wider capability set (it advertised more `Cap*` flags
  than `AcpHarness` does today) shall be reconciled: either close the gap on
  `AcpHarness`'s advertised capabilities, or adjust the test's expectations
  with a documented reason.
- Delete dead `internal/tui/chat/*` in the same unit — unreferenced by any
  live package, confirmed by research, no migration needed.
- Update `docs/ARCHITECTURE.md` (lines referencing "Claude SDK, Claude ACP,
  Cursor ACP, and Codex ACP" adapters), `CONTEXT.md` ("Harness and backend"
  section, `ClaudeHarness` listed as a Go type), and `AGENTS.md` ("Backend
  selection" section's backend/transport table) to drop the `sdk` row.

**Proof Artifacts:**
- Grep artifact: zero occurrences of `claudecode`/`claude-agent-sdk-go` in
  `internal/harness/`.
- `go build ./... && go vet ./... && go test ./...` pass with no
  `ClaudeHarness` references anywhere in the tree.
- A real workflow run with `backend = "claude"` (no `transport` set) produces
  a normal transcript via `AcpHarness`, with no TUI changes required (per
  spec 12's existing "transcript is the normalization boundary" precedent).

### Unit 2: Migrate `MonitorAdapter` to ACP

**Purpose:** Remove the second `claudecode` surface — the Tier-2 sentinel
classifier fleet — while being explicit about the enforcement guarantee that
changes.

**Functional Requirements:**
- `internal/runner/monitor.go`'s `MonitorAdapter.Dispatch` shall open an ACP
  session (via `AcpHarness`, or a narrower ACP-backed classifier session type
  if `AcpHarness`'s full step-execution surface is more than this caller
  needs) instead of `claudecode.NewClient`.
- The empty/deny-all tool surface (`monitor.go:53-61`, today
  `AllowedTools=[]`/`DisallowedTools=[]`/`SettingSources=[]` plus a
  `WithCanUseTool` that unconditionally denies) shall have an ACP-path
  equivalent: `AllowedTools=[]` on `SessionSpec` plus a `PermissionFn` that
  denies everything, since ACP has no wire-level "no tools at all" flag.
- The JSON-Schema constraint (`monitor.go:67`, `WithJSONSchema`) shall move
  to `AcpHarness`'s existing prompt-injected-schema mechanism
  (`appendSchemaPrompt` in `internal/harness/acp.go`).
  `decodeMonitorVerdict` (`monitor.go:138-155`) shall be relaxed from strict
  `DisallowUnknownFields` decoding of a guaranteed-shaped value to a decoder
  that tolerates surrounding prose and extracts the JSON block, matching how
  `appendSchemaPrompt`'s counterpart already parses `AcpHarness`'s structured
  output for step execution.
- On a decode failure (malformed or missing JSON where the SDK path would
  never have produced one), `Dispatch` shall retry **exactly once**,
  re-issuing the same classification prompt on a fresh ACP session, before
  giving up. If the retry also fails to decode, `Dispatch` returns a normal
  error — **not** a special "fail closed" path. Confirmed during grilling:
  `internal/sentinel/supervisor.go:289-304` already treats *every*
  `Dispatch` error (timeout, connection failure, anything) as fail-open —
  `s.disableMonitor(mon)` plus an `ActionObserved` health finding
  (`finding.go:34-35`: "noted the event but let it proceed"), then the fleet
  loop continues. `ActionBlocked` is reserved for Tier-1's rule-based gate
  (`internal/sentinel/rules.go`), which stays the only fail-closed layer.
  A decode failure must get the *same* fail-open treatment every other
  classifier failure already gets — introducing a stricter, unprecedented
  mode for this one cause would be new, undocumented behavior, not
  preserved behavior. The single retry exists only to absorb harmless
  model-sampling noise before that normal fail-open path kicks in, and must
  fit inside the existing ~30s timeout budget shared by
  `sentinel.Supervisor.DispatchTimeout` (`supervisor.go:24,91-92`) and
  `MonitorAdapter.timeout` (`monitor.go:44`) — it does not apply to
  connection/timeout-level errors, which keep today's single-attempt
  semantics.
- Single-shot dispatch semantics (`WithMaxTurns(1)` equivalent — no
  multi-turn state, fresh session per `Dispatch` call) shall be preserved:
  each `Dispatch` opens and closes its own ACP session, matching
  `monitor.go:84-88`'s existing connect-per-call pattern.
- `internal/runner/monitor_test.go`'s `fakeMonitorClient` shall be replaced
  or extended with fakes appropriate to the ACP-backed path, including at
  least one test asserting a single decode-retry followed by a normal
  fail-open `Dispatch` error (not a panic, not a silent "no finding").

**Proof Artifacts:**
- Grep artifact: zero occurrences of `claudecode` in `internal/runner/monitor.go`.
- Test: a scripted verdict with extra prose around the JSON block still
  decodes correctly (proves the tolerant decoder works).
- Test: a scripted response that fails to decode twice in a row (first
  attempt and retry) surfaces as a normal `Dispatch` error, which
  `Supervisor` handles via its existing `ActionObserved`/disable-monitor
  path — not a panic, not a silent pass, and not a new fail-closed branch.
- `go test ./internal/runner/... ./internal/sentinel/...` passes.

### Unit 3: Out-of-process MCP server for `helpchat`'s tool surface

**Purpose:** Design and build the missing piece — `helpchat`'s 10 tools
currently exist only as in-process Go closures
(`claudecode.CreateSDKMcpServer`, `internal/helpchat/tools.go:38-49`). ACP has
no in-process tool registration analog, so this unit stands up a real
out-of-process MCP server exposing the same tools, decoupled from any
particular harness or session — pure component design, no `helpchat` rewiring
yet.

**Design settled during grilling:**
- **Who spawns what.** `coder/acp-go-sdk`'s `McpServerStdio` struct
  (`command string`, `args []string`, `env []EnvVariable`, `name string`)
  is the transport every ACP agent must support. The *agent* process (not
  jig) spawns `command`/`args` as its own child and speaks MCP-over-stdio to
  it. So jig supplies a `command` the agent can spawn — a new hidden `jig
  mcp-serve` subcommand — not a listener jig itself starts directly.
- **Phoning home.** `jig mcp-serve` speaks real MCP JSON-RPC over its own
  stdio to the agent on one side, and on the other side connects back to a
  **loopback TCP socket** the main jig process opens, forwarding each tool
  call as a simple request/response over that connection. TCP loopback was
  chosen over a Unix domain socket or named pipe because jig has no
  existing local-IPC precedent to reuse (`cmd/jig/main.go` dispatches only
  user-facing subcommands today; `harness/acp` splits `process_unix.go`/
  `process_other.go`, implying non-Unix platform support matters) and
  loopback TCP is portable across all of them without a platform-specific
  fallback. It also does not foreclose a possible future per-step
  containerized runner, which would need to reach the host over a published
  port rather than a bind-mounted socket file anyway.
- **Lifecycle.** The loopback listener and its `mcp-serve` subprocess are
  spawned fresh per help-chat session (matching `AcpHarness`'s existing
  one-`Open()`-per-call convention, and matching that `McpServers` is
  already passed per-`session/new`-call today with no session-spanning
  reuse mechanism in the SDK surface jig uses) — not a long-lived process
  reused across sessions.
- **Auth.** The main jig process generates a random per-session token,
  passed to `mcp-serve` via `EnvVariable` alongside the port. `mcp-serve`
  sends the token as the first message on the TCP connection; the main
  process closes the connection on any mismatch. Loopback binding alone is
  not treated as sufficient isolation — any other local process could
  otherwise connect.
- **Known, unresolved risk — not verified in this spec.** Nothing in ACP's
  wire protocol or `coder/acp-go-sdk`'s connection layer imposes a call
  timeout that would kill a multi-minute human-wait inside a tool call
  (confirmed by reading `connection.go`'s `SendRequest`/`waitForResponse`
  and jig's own `acp.go`/`cursor.go`/`codex.go`, none of which wrap `Prompt`
  in a `context.WithTimeout`). But whether the *agent binary itself*
  (Claude Code CLI, Cursor CLI) enforces its own MCP tool-call timeout
  internally could not be determined from SDK source alone, and this spec
  does not add a proof artifact to verify it empirically. **Whoever
  implements Unit 3 should check this early** — if the real agent binary
  kills a slow `ask_user` call, the blocking-rendezvous design in this unit
  does not work as specified and needs revisiting before Unit 4 proceeds.

**Functional Requirements:**
- The system shall implement a local MCP server, exposed via the new hidden
  `jig mcp-serve` subcommand (stdio transport to the agent, matching what
  ACP's `McpServer` type universally supports without requiring
  agent-advertised HTTP/SSE/ACP-channel capability), exposing the same 10
  tools `helpchat` registers today: `workflow_snapshot`,
  `read_step_transcript`, `read_step_result`, `read_step_output`,
  `recover_step`, `reset_step`, `stop_step`, `resume_step`, `resolve_review`,
  `ask_user`.
- `jig mcp-serve` shall accept the loopback port and per-session auth token
  via environment variables (populated through the `McpServerStdio.Env`
  config jig passes when opening the session), authenticate on connect, and
  forward each MCP tool call as a request over that TCP connection rather
  than holding `*engine.Run`/`tea.Msg` state itself — tools that today close
  directly over that state (`tools.go:288-323`) move to the main process's
  side of the connection, which does hold it.
- The blocking rendezvous cases — `resolve_review`'s `final_merge` decision
  and `ask_user` (`tools.go:311-317,378-398`), both currently a synchronous
  Go channel wait in-process — shall have an equivalent synchronous
  request/response contract over the TCP connection: `jig mcp-serve`'s tool
  handler blocks on a reply exactly as today's channel-based handler does,
  but the reply now crosses a process boundary instead of a goroutine
  boundary.
- The server's lifecycle (per-session spawn, ready-to-accept-connections
  signal, clean shutdown when the help-chat session ends, and crash
  handling mid-conversation) shall be explicit and testable independent of
  `helpchat`/`AcpHarness` — this unit's proof artifacts exercise the server
  standalone.

**Proof Artifacts:**
- Standalone test: `jig mcp-serve`, run in isolation against a scripted
  MCP client and a scripted TCP counterparty standing in for the main jig
  process, correctly dispatches all 10 tools and returns expected
  responses, without any TUI or `AcpHarness` involvement.
- Standalone test: a scripted `ask_user`/`final_merge` rendezvous — request
  sent, response delayed, response delivered — completes correctly and
  matches today's in-process channel semantics (request blocks until
  answered, no lost or duplicated replies).
- Standalone test: an unauthenticated or wrong-token connection attempt is
  rejected.
- Crash-handling test: killing `jig mcp-serve` mid-request surfaces a clear
  error to the caller rather than hanging.

### Unit 4: Wire `helpchat` onto `AcpHarness` + Unit 3's MCP server

**Purpose:** Replace `helpchat`'s direct `claudecode` usage with an
ACP-backed session that registers Unit 3's `jig mcp-serve` subcommand via
`SessionSpec`'s `MCPServers` field, closing the third and last `claudecode`
surface.

**Functional Requirements:**
- `internal/helpchat/{cmds.go,tools.go,model.go,msgs.go}` shall open
  sessions via `AcpHarness` instead of `claudecode.NewClient`. Before
  opening the session, it shall: bind a loopback TCP listener, generate a
  per-session auth token, and populate `SessionSpec.MCPServers` with a
  single `McpServerStdio` entry naming `command: "jig"`, `args: ["mcp-serve"]`,
  and `env` carrying the port and token — mirroring how
  `internal/harness/acp_config.go`/`conn.go` today always send an empty
  `McpServers` list; this unit is what makes that list non-empty for the
  first time.
- `AcpHarness.Open` shall be extended (if not already sufficient) to accept
  and forward a non-empty `MCPServers` list to ACP's `session/new` call,
  which today always sends `McpServers: []acpsdk.McpServer{}`
  (`internal/harness/acp_config.go`/`internal/harness/acp.go` /
  `harness/acp/conn.go`).
- `helpchat` shall accept the incoming authenticated TCP connection from the
  spawned `jig mcp-serve` process and dispatch its forwarded tool-call
  requests against live `*engine.Run` state and the existing `tea.Msg`
  dispatch mechanism, replying over the same connection — the counterpart
  to Unit 3's client-side contract.
- The existing help-chat conversational flow (multi-turn, tool calls
  triggering TUI actions like `recover_step`/`reset_step`, the blocking
  `ask_user` question) shall behave identically from the operator's
  perspective — same prompts answered, same recover/reset/resume actions
  available — with the new process hop (TUI → ACP session → Claude adapter,
  and separately TUI ↔ Unit 3's MCP server ↔ tool handlers) invisible to the
  user.
- `internal/tui/monitor/{monitor_model.go,monitor_update.go,monitor_test.go}`,
  which currently import `internal/helpchat` directly, shall need at most
  wiring changes (new MCP server lifecycle hookup), not TUI redesign — the
  run monitor's existing "transcript is the boundary" precedent (spec 12)
  should hold here too if Unit 3 is implemented correctly.

**Proof Artifacts:**
- Manual/scripted run: help-chat session in the TUI exercises at least one
  tool call per registered tool (`workflow_snapshot` through `ask_user`),
  confirmed to produce the same TUI state transitions as today's SDK-backed
  path.
- Grep artifact: zero occurrences of `claudecode` in `internal/helpchat/`.
- `go test ./internal/helpchat/... ./internal/tui/monitor/...` pass.

### Unit 5: Remove the SDK dependency; final grep-clean closeout

**Purpose:** The only unit that can't start until 1, 2, and 4 are all done —
delete the dependency itself and verify nothing remains.

**Functional Requirements:**
- `go.mod`/`go.sum` shall drop `github.com/severity1/claude-agent-sdk-go`
  (currently `go.mod:18`) entirely.
- A repo-wide grep for `claudecode`/`claude-agent-sdk-go` shall return zero
  matches outside of this spec's own history/docs.
- `docs/ARCHITECTURE.md`, `CONTEXT.md`, and `AGENTS.md` shall be updated to
  describe ACP as the only transport — drop "Transport: the protocol used to
  reach it: SDK or ACP as supported" language in `CONTEXT.md`'s "Harness and
  backend" section entirely, since Unit 1 removes `Transport` as a field.
- `docs/specs/12-spec-acp-claude-harness/12-spec-acp-claude-harness.md`'s
  Non-Goal 5 and `docs/plans/open-goals.md`'s A6 entry (tracking
  `MonitorAdapter` as an "isolated direct-SDK classifier") shall be marked
  resolved/closed, pointing at this spec.

**Proof Artifacts:**
- `grep -rn "claudecode\|claude-agent-sdk-go" --include="*.go" .` (excluding
  this spec's directory) returns nothing.
- `go build ./... && go vet ./... && go test ./...` pass with the dependency
  removed from `go.mod`.
- `go mod tidy` produces no diff (confirms nothing transitively still needs
  it).

## Non-Goals (Out of Scope)

1. **Adding a fifth backend.** This spec closes out existing surfaces; it
   does not add a new vendor/CLI target.
2. **Changing ACP protocol version or the underlying `coder/acp-go-sdk` /
   `harness/acp` nested module design.** Those are settled by spec 12
   (ADR 0010, ADR 0011) and unaffected here.
3. **Rearchitecting `sentinel`'s policy model.** Unit 2 changes *how*
   `MonitorAdapter` talks to its backend, not what the sentinel fleet
   decides or how findings are consumed downstream.
4. **A network/remote MCP transport for Unit 3's server.** Local stdio only,
   matching every other ACP subprocess in this repo.
5. **Redesigning `helpchat`'s conversational UX.** Unit 4 preserves existing
   behavior; any UX changes to help-chat are a separate spec.
6. **Guaranteeing bit-for-bit identical sentinel verdict text.** Unit 2
   explicitly trades wire-enforced schema for prompt-injected schema; exact
   verdict wording may shift with model sampling variance the way any
   prompt-based approach does. What must not shift is that a `Dispatch`
   failure — decode failure included — gets the same fail-open treatment
   every other classifier failure already gets, no stricter and no
   laxer.
7. **A new ADR for the TCP-loopback + hidden-subcommand IPC pattern.**
   Considered and declined during grilling — the reasoning stays in this
   spec (Unit 3) rather than a separate ADR, unlike ADR 0010/0011's
   precedent for the nested `harness/acp` module.
8. **Empirically verifying whether the real agent binary (Claude Code CLI,
   Cursor CLI) enforces its own MCP tool-call timeout.** Considered and
   declined during grilling as a proof artifact for this spec; carried
   forward as a known, unresolved risk in Unit 3 instead (see that unit's
   "Known, unresolved risk" note).

## Design Considerations

No TUI redesign is required if Units 3-4 are scoped correctly: the run
monitor already reads only the transcript (spec 12's existing invariant), and
`helpchat`'s tool-triggered TUI actions should route through the same
`tea.Msg` dispatch mechanism they use today — Unit 3's IPC boundary sits
*behind* that dispatch call, not in front of it. If Unit 3's design ends up
requiring TUI-visible changes (e.g. a visible "connecting to help-chat tools"
state), that is a signal the IPC design leaked further than intended and is
worth revisiting before Unit 4 starts.

## Repository Standards

- **Consumer-defined interfaces / dependency inversion**, matching the
  existing `Harness` seam: `MonitorAdapter` and `helpchat` should depend on
  `harness.Harness`/`AcpHarness`, not reach past it to ACP wire types.
- **Capability gating stays explicit, not inferred.** If `MonitorAdapter`'s
  classifier session needs a capability `AcpHarness` doesn't already
  advertise correctly for this use case (e.g. its no-tools mode), that gap
  gets a named capability check, not a silent assumption.
- **Table-driven tests** with inline fixtures for both the tolerant decoder
  (Unit 2) and the MCP server's request/response contract (Unit 3).
- **Comments explain the non-obvious "why"** — especially why
  `MonitorAdapter`'s schema enforcement is now advisory rather than
  wire-guaranteed, and why that is still acceptable for the security gate
  (the fail-open, disable-and-continue behavior on decode failure is the
  same treatment every other classifier failure already gets — Tier-1
  rules remain the actual fail-closed layer).
- **Nested/standalone components get their own lifecycle tests** before
  integration, matching spec 12's Unit 1 standalone-spike precedent — Unit 3
  is this spec's version of that pattern.

## Technical Considerations

- **`AcpHarness`'s structured output is prompt-injected, not wire-enforced.**
  Confirmed in `internal/harness/acp.go`'s `appendSchemaPrompt`: the schema is
  appended to the prompt text and the response is parsed for a JSON block,
  not passed as an API-level constraint the way `claudecode.WithJSONSchema`
  is. This is the central risk Unit 2 has to design around, not paper over.
- **ACP's `McpServer` type is out-of-process only.** `coder/acp-go-sdk`'s
  `types_gen.go` `McpServer` supports `stdio` (universal) or `http`/`sse`/an
  `acp`-channel variant gated behind agent-advertised capability — there is
  no in-process closure registration. This is why Unit 3 exists as a
  standalone component rather than a small `helpchat` code change.
- **The blocking rendezvous pattern (`ask_user`, `final_merge`) is the
  highest-risk part of Units 3-4.** Today it's a same-process Go channel;
  moving it across a process boundary introduces real failure modes (server
  crash while a request is in flight, slow IPC changing perceived
  responsiveness) that the in-process version never had. Unit 3's
  crash-handling proof artifact exists specifically to catch this before
  Unit 4 integrates it into the live TUI.
- **`AcpHarness.Open`'s current always-empty `McpServers` list** (in
  `internal/harness/acp_config.go` / `conn.go`) is untested with a non-empty
  value — Unit 4 is the first caller to exercise that path, so treat it as
  new integration surface, not an already-proven capability.
- **No timeout enforcement found anywhere in the ACP round trip.**
  `coder/acp-go-sdk`'s `SendRequest`/`waitForResponse` (`connection.go:625-661`)
  applies no internal deadline — it blocks on the caller's `ctx`. jig's own
  `acp.go`/`cursor.go`/`codex.go` never wrap `Prompt` in a
  `context.WithTimeout` either. The one gap: whether the agent binary's own
  MCP client enforces a tool-call timeout is not visible from SDK source —
  flagged as an unresolved risk in Unit 3, not assumed away.

## Security Considerations

- **`MonitorAdapter`'s enforcement guarantee changes and must be documented,
  not silently weakened or silently strengthened.** Before: a malformed
  classifier response was structurally impossible (SDK enforced the
  schema). After: it's possible, gets one retry, and on a second failure
  becomes a normal `Dispatch` error handled by `Supervisor`'s existing
  fail-open path (disable that monitor, `ActionObserved`, continue) — the
  same treatment every other classifier failure already gets. Unit 2's
  proof artifact (two-failures-in-a-row test) is a security-relevant test
  documenting the real guarantee, not a claim that decode failure is
  somehow now fail-closed.
- **Unit 3's MCP server is a new local trust boundary, and its loopback TCP
  side is a new local network surface — the first in the repo that isn't
  pure subprocess-stdio.** It runs with whatever privileges its tool
  handlers need (recover/reset/resume actions on live runs) — same
  privilege level as today's in-process closures, but now reachable by
  anything that can connect to the loopback port unless authenticated. The
  per-session random token (sent as the connection's first message,
  mismatch closes the connection) is the control that keeps this from being
  an open local surface; loopback binding alone is not treated as
  sufficient. Scope the listener's and `jig mcp-serve`'s process lifetime
  tightly to one help-chat session, matching `AcpHarness`'s existing
  one-session-per-`Open()` pattern, so neither outlives its purpose.
- **No new externally-reachable network endpoints.** The loopback socket
  never leaves the local machine; the agent-facing side of `jig mcp-serve`
  stays stdio, consistent with every other ACP subprocess jig already
  spawns (spec 12's existing "Subprocess trust boundary" precedent).

## Success Metrics

1. **One transport, not three.** `claudecode`/`claude-agent-sdk-go` no longer
   appears anywhere in the tree; `go.mod` no longer depends on it.
2. **No regression in step execution.** Workflows using `backend = "claude"`
   run identically through `AcpHarness` post-Unit-1, with no TUI changes.
3. **Sentinel fleet's failure semantics are unchanged, not stricter or
   laxer.** `MonitorAdapter`'s ACP-backed classifier retries once on an
   undecodable response, then surfaces a normal `Dispatch` error handled by
   `Supervisor`'s existing fail-open path — identical to how every other
   classifier failure is handled today; no change to the sentinel fleet's
   downstream consumption of verdicts.
4. **`helpchat` behaves identically to the operator**, including the
   blocking `ask_user`/`final_merge` flows, despite the new process
   boundary underneath it.
5. **`go build/vet/test ./...` pass** across the root module and the nested
   `harness/acp` module throughout, at the end of every unit.

## Open Questions

Resolved during the grilling session on this spec: whether `Transport`
survives as a field (removed entirely, Unit 1); `MonitorAdapter`'s failure
semantics (fail-open with one retry, matching existing architecture, Unit
2); the IPC mechanism and lifecycle for Unit 3's MCP server (loopback TCP +
hidden `jig mcp-serve` subcommand, spawned per help-chat session, with a
per-session auth token). What remains open:

1. **Whether the real agent binary enforces its own MCP tool-call
   timeout.** Explicitly not verified in this spec (see Unit 3's "Known,
   unresolved risk" note) — check this early during Unit 3 implementation,
   since it could invalidate the blocking-rendezvous design for `ask_user`.
2. **Token/cost accounting for `helpchat` and `MonitorAdapter` once
   ACP-only** — spec 12 already left ACP-path cost figures as nil/zero for
   step execution; confirm the same convention applies here rather than
   inventing new tracking.

## Related Records

- **`docs/specs/12-spec-acp-claude-harness/12-spec-acp-claude-harness.md`** —
  introduced `AcpHarness`, the `Harness`/`Capability` seam, and explicitly
  deferred `MonitorAdapter`/`tui/chat` (Non-Goal 5) — this spec picks up that
  deferral.
- **`docs/specs/14-spec-per-step-harness/14-implementation-plan.md`** — added
  per-step `backend`/`transport` selection; this spec removes the `sdk`
  transport value it introduced defaulting logic around.
- **`docs/plans/open-goals.md`** — A6 tracks `MonitorAdapter` as an "isolated
  direct-SDK classifier"; this spec resolves it. T1 references `tui/chat` as
  a design precedent only (already dead code, deleted in Unit 1).
- **`docs/plans/a4-cursor-harness-parity.md`** — unrelated to this spec's
  scope (Cursor capability parity, not SDK removal); worth reconciling
  separately since it may already be stale relative to `cursor.go`'s current
  capabilities.
- **`CONTEXT.md`** — "Harness and backend" section; drop `Transport` as a
  vocabulary term in Unit 5, per Unit 1's resolution.
- **ADR 0010, ADR 0011** — govern the nested `harness/acp` module and the
  Zed npx-adapter choice; unaffected by this spec, referenced for context.
