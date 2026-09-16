# 26-tasks-acp-only-harness.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/harness/claude.go` | `ClaudeHarness` implementation; deleted in Unit 1. |
| `internal/harness/claude_test.go` | Tests for `ClaudeHarness`; deleted in Unit 1. |
| `internal/harness/select.go` | `For(backend, transport)`; drops the `sdk` branch and the `transport` parameter in Unit 1. |
| `internal/harness/select_test.go` | Table-driven tests for `For`; updated for the single-argument signature and dropped `sdk` cases. |
| `internal/runner/agent.go` | `AgentExecutor.forHarness` field/type, `Execute`, `SupportsSessionResume`, `persistSessionID` all take `(backend, transport)`; collapse to `backend`-only in Unit 1. |
| `internal/runner/agent_test.go` | Exercises `ClaudeHarness`'s wider `Cap*` set vs. `AcpHarness`'s; reconcile per Unit 1's functional requirement. |
| `internal/runner/fake.go` | `NewAgentExecutorFixed`/test doubles wired to the two-argument `forHarness` signature. |
| `internal/ops/control.go` | Line ~149 calls `harness.For(wfStep.Backend, wfStep.Transport)`; updates to single-argument `For`. |
| `internal/datastore/session.go` | `SessionInfo.Transport` field persisted alongside `Backend`; removed once `Transport` is gone. |
| `internal/datastore/session_test.go` | Asserts `SessionInfo.Transport`; updated to drop the field. |
| `internal/workflow/schema.go` | `TransportSDK`/`TransportACP` constants, `validTransport`, `Step.Transport`, `Defaults.Transport` (lines ~94-111, 378-381, 454-458); `Transport` removed entirely. |
| `internal/workflow/load.go` | Transport-resolution branch (lines ~202-210: step → defaults → backend default); removed. |
| `internal/workflow/validate.go` | Transport-validity checks (lines ~229-233); removed along with the `Transport` field. |
| `internal/workflow/*_test.go` (schema/load/validate) | Fixtures and assertions referencing `transport = "sdk"`/`"acp"`; updated to drop the field. |
| `internal/harness/security_integration_test.go` | Builds `workflow.Step{..., Transport: "acp", ...}`; updated to drop the field. |
| `internal/tui/chat/*` | Confirmed dead code, unreferenced by any live package; deleted wholesale in Unit 1. |
| `docs/ARCHITECTURE.md` | Line ~32 lists "Claude SDK, Claude ACP, Cursor ACP, and Codex ACP" adapters; drops the SDK row (Unit 1) and is fully ACP-only by Unit 5. |
| `CONTEXT.md` | "Harness and backend" section (lines ~136-141) defines `Transport` and lists `ClaudeHarness`; updated in Unit 1, `Transport` vocabulary dropped entirely in Unit 5. |
| `AGENTS.md` | "Backend selection" section's backend/transport table (lines ~58-81) drops the `sdk` row in Unit 1 and the `transport` concept in Unit 5. |
| `internal/runner/monitor.go` | `MonitorAdapter.Dispatch`, `monitorOptions`, `decodeMonitorVerdict`, `drainMonitorChannel`; rewritten onto an ACP-backed session in Unit 2. |
| `internal/runner/monitor_test.go` | `fakeMonitorClient` and decode-path tests; replaced/extended with ACP-backed fakes and the tolerant-decoder + retry-then-fail-open tests in Unit 2. |
| `internal/harness/acp.go` | `appendSchemaPrompt`, `extractJSONFromText`, `acpMaxStructuredAttempts`; the prompt-injected schema mechanism `MonitorAdapter` reuses in Unit 2; `Open`/`NewSession` call site extended to accept `SessionSpec.MCPServers` in Unit 4. |
| `internal/harness/capability.go` | `SessionSpec` struct; gains an `MCPServers` field (capability-gated or otherwise) in Unit 4. |
| `internal/sentinel/supervisor.go` | Lines ~289-304: existing fail-open handling (`disableMonitor`, `ActionObserved`) that every `Dispatch` error already gets; Unit 2 must not special-case decode failures outside this path. Read-only reference, not expected to change. |
| `internal/sentinel/finding.go` | `ActionObserved`/`ActionBlocked` semantics referenced by Unit 2's proof artifact. Read-only reference. |
| `internal/sentinel/supervisor_test.go`, `internal/sentinel/supervisor_a6_test.go` | Existing fail-open coverage; extend if `MonitorAdapter`'s new error shape needs an assertion here too. |
| `cmd/jig/main.go` | Switch-based subcommand dispatch (lines ~33-55); gains a hidden `mcp-serve` case in Unit 3. |
| `cmd/jig/mcp_serve.go` (new) | New hidden `jig mcp-serve` subcommand: stdio MCP server to the agent, TCP client back to the main jig process. |
| `cmd/jig/mcp_serve_test.go` (new) | Standalone tests: 10-tool dispatch, `ask_user`/`final_merge` rendezvous, auth rejection, crash handling. |
| `internal/helpchat/tools.go` | `BuildMcpServer`, the 10 `claudecode.McpTool` builders (`buildWorkflowSnapshot` … `buildAskUser`), `okResult`/`errResult`; rewritten as MCP tool handlers on the TCP server side of Unit 3/4's contract. |
| `internal/helpchat/cmds.go` | `connectCmd`/`queryCmd` build `claudecode.NewClient` with `WithSdkMcpServer`; rewritten to open an `AcpHarness` session with `SessionSpec.MCPServers` naming `jig mcp-serve` in Unit 4. |
| `internal/helpchat/model.go` | `Model.client`/`msgChan` typed as `claudecode.Client`/`claudecode.Message`; retyped onto `harness.Session`/`harness.Event` in Unit 4. |
| `internal/helpchat/msgs.go` | `claudecode.Client`/`claudecode.Message`-typed message structs; retyped in Unit 4. |
| `internal/helpchat/actions.go`, `internal/helpchat/gate_view.go`, `internal/helpchat/prompt.go` | Consumers of the above types/dispatch mechanism; updated only as needed to compile against the new types. |
| `internal/helpchat/helpchat_test.go` | Exercises the SDK-backed flow end-to-end; rewritten against `AcpHarness` + a scripted local MCP server in Unit 4. |
| `internal/tui/monitor/monitor_model.go`, `internal/tui/monitor/monitor_update.go` | Import `internal/helpchat` directly; expected to need only MCP-server lifecycle wiring, not redesign (spec's Design Considerations). |
| `internal/tui/monitor/monitor_test.go` | Existing help-chat wiring coverage in the monitor package; extended if lifecycle hookup needs assertions here. |
| `go.mod`, `go.sum` | Line 18: `github.com/severity1/claude-agent-sdk-go v0.6.22`; removed entirely in Unit 5. |
| `docs/specs/12-spec-acp-claude-harness/12-spec-acp-claude-harness.md` | Non-Goal 5 marked resolved/closed, pointing at this spec, in Unit 5. |
| `docs/plans/open-goals.md` | A6 entry ("isolated direct-SDK classifier") marked resolved/closed in Unit 5. |
| `README.md` | "Status" section's "Claude SDK/ACP, Cursor ACP, and Codex ACP are supported" line; updated to drop the SDK/ACP split in Unit 5. |

### Notes

- Unit tests live alongside the code they test (`monitor.go`/`monitor_test.go`, `select.go`/`select_test.go`, etc.), matching this repo's existing convention.
- Use `go test ./...`, `go vet ./...`, and `(cd harness/acp && go test ./... && go vet ./...)` per [AGENTS.md](../../../AGENTS.md)'s Commands section; the nested ACP module is not covered by root `go test ./...`.
- Format changed Go files with `gofmt -w <files>`; do not reformat unrelated files.
- `go test -race ./internal/tui/... ./internal/helpchat` and `go test -race ./internal/engine ./internal/runner ./internal/harness` per [docs/TESTING.md](../../TESTING.md) are the relevant focused race-detector invocations for Units 1, 2, and 4.
- Use synthetic fixtures for all monitor/help-chat proof artifacts; never copy real run data, prompts, or credentials into tests or docs, per AGENTS.md's "Sensitive local state" constraint.

## Tasks

### [x] 1.0 Delete `ClaudeHarness`; `AcpHarness` becomes the only Claude path

#### 1.0 Proof Artifact(s)

- Grep: `grep -rn "claudecode\|claude-agent-sdk-go" internal/harness/` returns
  nothing, demonstrates the SDK is gone from the harness seam.
- CLI: `go build ./... && go vet ./... && go test ./...` passes with zero
  `ClaudeHarness` references anywhere in the tree, demonstrates the deletion
  is load-bearing-clean.
- Manual: a real workflow run with `backend = "claude"` (no `transport` set)
  produces a normal transcript via `AcpHarness`, demonstrates no regression
  in step execution or the TUI.

#### 1.0 Tasks

- [x] 1.1 Delete `internal/harness/claude.go` and `internal/harness/claude_test.go`.
- [x] 1.2 Delete `internal/tui/chat/*` (confirmed dead code, unreferenced by
      any live package); run `go build ./...` to confirm nothing broke.
- [x] 1.3 Collapse `internal/harness/select.go`'s `For(backend, transport)`
      to `For(backend string) (Harness, error)`: `backend == "claude"`/`""`
      resolves directly to `NewAcpHarness()`; drop the `sdk`/`""` transport
      branch and the now-dead transport-validity error paths for
      `cursor`/`codex`. Update `internal/harness/select_test.go`'s table to
      drop `transport` from test cases and the `sdk` cases entirely.
- [x] 1.4 Update every caller of the two-argument `For`/`forHarness`:
      `internal/runner/agent.go` (`AgentExecutor.forHarness` field type,
      `NewAgentExecutor`, `NewAgentExecutorFixed`, `Execute`,
      `SupportsSessionResume`), `internal/runner/integration_resolver.go`,
      `internal/runner/mux.go`, `internal/telemetry/reporter.go`, and
      `internal/ops/control.go:149`, to the single-argument signature.
      Scope grew beyond the two named files: the `backend, transport`
      signature also threaded through `engine.SessionResumeSupport`,
      `runner.Mux.SupportsSessionResume`, and `telemetry.MetricMux` — all
      updated to single-argument.
- [x] 1.5 Remove `Transport` from `internal/workflow/schema.go`
      (`TransportSDK`/`TransportACP` constants, `validTransport`,
      `Step.Transport`, `Defaults.Transport`), the transport-resolution
      branch in `internal/workflow/load.go` (~202-210), and the
      transport-validity checks in `internal/workflow/validate.go`
      (~229-233). Updated `workflow_test.go`'s fixtures/assertions
      (renamed `TestDecodeBackendTransport` → `TestDecodeBackend`).
      Scope grew beyond `internal/workflow`: `Transport` was also a field on
      `manifest.StepTerminal`/`provenanceJSON`, `runexport.RunSummary`/
      `StepSummary`, and `telemetry.stepLabels`, and was read in
      `internal/ops/doctor.go` and `internal/tui/detail/view.go` — all
      updated, plus every workflow TOML under `.agents/jig/` and
      `examples/` that set `transport = "..."` (the field is rejected as an
      unknown key once removed from the schema). Deleted
      `.agents/jig/mixed-transport.toml` (its entire purpose — demonstrating
      SDK+ACP mixing — is obsolete).
- [x] 1.6 Remove `Transport` from `internal/datastore/session.go`'s
      `SessionInfo` and `internal/runner/agent.go`'s `persistSessionID`;
      updated `internal/datastore/session_test.go` and
      `internal/engine/resume_test.go`/`handlers.go`/`engine.go`, which also
      read/wrote it.
- [x] 1.7 Update `internal/harness/security_integration_test.go`'s
      `workflow.Step{..., Transport: "acp", ...}` fixtures to drop the field
      (7 occurrences across inline TOML fixtures and struct literals).
- [x] 1.8 Reconciled by closing the gap on `AcpHarness`: discovered during
      implementation that `ClaudeHarness` advertised `CapSessionResume` but
      `AcpHarness` did not, and `AcpHarness.Open` unconditionally rejected
      `spec.Resume` — a real regression (`.agents/jig/feature.toml` uses
      `block_on` on `backend = "claude"` steps today). `CursorHarness`/
      `CodexHarness` already wire `spec.Resume` through `conn.LoadSession`;
      applied the identical pattern to `AcpHarness.Open` and added
      `CapSessionResume` to its advertised capabilities. Also fixed a real
      bug this surfaced: `harness/acp/conn.go`'s `Connect` (the Zed/Claude
      adapter path) never populated `Conn.SupportsLoadSession` from the
      Initialize response, unlike `ConnectCursor`/`ConnectCodex` — fixed to
      match. Updated `internal/harness/security_integration_test.go`'s
      three live-ACP resume tests (previously hardcoding `claude-acp` as the
      one backend that could NOT resume) to expect resume parity across all
      three backends; all now pass against real (fixture) ACP round trips.
- [x] 1.9 Updated `docs/ARCHITECTURE.md` (package table), `CONTEXT.md`
      ("Harness and backend" section — dropped `ClaudeHarness` from the
      Harness list; full `Transport` vocabulary removal stays Unit 5's job
      per this task list), and `AGENTS.md` ("Backend selection" section —
      rewrote to a single-column backend table and `harness.For(backend)`).
      Also updated `docs/workflow-schema.md` (schema authoring reference),
      `docs/observability.md` and `docs/operations.md` (dropped the
      `transport` telemetry label), and `docs/workflow-parsing-pipeline.md`
      (mermaid diagrams) — all live, current-behavior docs that would have
      been actively wrong otherwise. Left historical/dated documents
      (`docs/agent-guidance-review.md`, `docs/cursor-acp-research.md`,
      `docs/plan-*.md`) untouched.
- [x] 1.10 Ran `gofmt -w` on all changed files, then
      `go build ./... && go vet ./... && go test ./...` (root) and
      `(cd harness/acp && go build ./... && go vet ./... && go test ./...)`
      (nested module) — all pass. Also ran the `-race` invocations from
      docs/TESTING.md for the affected packages. Manually ran a real
      `backend = "claude"` (no `transport`) workflow end-to-end via
      `jig run`: step succeeded through `AcpHarness`, transcript captured
      normally (see proof artifact for full output).

### [x] 2.0 Migrate `MonitorAdapter` to ACP

#### 2.0 Proof Artifact(s)

- Grep: `grep -rn "claudecode" internal/runner/monitor.go` returns nothing,
  demonstrates the sentinel classifier no longer uses the SDK.
- Test: `TestDecodeMonitorVerdict_TolerantOfSurroundingProse` (or equivalent)
  in `internal/runner/monitor_test.go` passes, demonstrates the relaxed
  decoder still extracts a valid verdict.
- Test: `TestMonitorAdapter_DecodeRetryThenFailOpen` (or equivalent) in
  `internal/runner/monitor_test.go` passes, demonstrates a two-failures-in-a-row
  decode surfaces as a normal `Dispatch` error, not a panic or a new
  fail-closed branch.
- CLI: `go test ./internal/runner/... ./internal/sentinel/...` passes,
  demonstrates the sentinel fleet's fail-open handling is unchanged.

#### 2.0 Tasks

- [x] 2.1 Design `MonitorAdapter`'s ACP-backed session boundary: decide
      whether `Dispatch` opens an `AcpHarness` session directly via
      `harness.Harness`/`SessionSpec`, or a narrower ACP-backed classifier
      session type, per the "consumer-defined interfaces" repository
      standard (`MonitorAdapter` depends on `harness.Harness`, not ACP wire
      types). Chose a narrow local `monitorHarness` interface (one `Open`
      method matching `harness.Harness`'s signature) over the full
      `harness.Harness` interface, mirroring the pre-existing `monitorClient`
      pattern this file already used for the SDK client — `*harness.AcpHarness`
      satisfies it with no adapter shim, and `MonitorAdapter` still depends
      only on `harness.SessionSpec`/`harness.Session`/`harness.Event`, never
      on ACP wire types.
- [x] 2.2 Replace `monitorOptions`' empty/deny-all tool surface
      (`Tools`/`AllowedTools`/`DisallowedTools`/`SettingSources` plus
      `WithCanUseTool`) with the `SessionSpec` equivalent: `AllowedTools=[]`
      plus a `PermissionFn` that unconditionally denies.
- [x] 2.3 Replace `claudecode.WithJSONSchema(monitorJSONSchema)` with
      `SessionSpec.Schema` so `AcpHarness`'s `appendSchemaPrompt` injects the
      same schema into the prompt.
- [x] 2.4 Rewrite `decodeMonitorVerdict` to tolerate prose surrounding the
      JSON block (reuse or mirror `internal/harness/acp.go`'s
      `extractJSONFromText`) instead of `json.Decoder.DisallowUnknownFields`
      against a guaranteed-shaped value. Add a comment explaining why
      enforcement is now advisory, not wire-guaranteed, and why that is
      still acceptable (Tier-1 rules remain the fail-closed layer). Added a
      local `extractMonitorJSON` mirroring `extractJSONFromText` (small
      intentional duplication — the harness version is unexported and this
      is runner's only caller); `decodeMonitorVerdict` runs input through it
      before the still-strict `DisallowUnknownFields` verdict-shape check.
- [x] 2.5 Implement the single-retry rule in `Dispatch`: on a decode
      failure, re-issue the same classification prompt on a fresh ACP
      session exactly once; if the retry also fails to decode, return a
      normal error (no new fail-closed branch). Ensure both attempts fit
      inside the existing ~30s `MonitorAdapter.timeout`/
      `sentinel.Supervisor.DispatchTimeout` budget. Connection/timeout-level
      errors keep today's single-attempt semantics (no retry). Implemented
      via `attempt`'s `(launched, retryable, err)` return: a retry fires only
      when the first attempt both launched (session opened) and failed with
      a decode-shaped error (verdict-shape validation failure, empty
      `Structured`, or an `AcpHarness`-internal "structured output" exhaustion
      error); both attempts share the same `dispatchCtx` deadline so the
      shared timeout budget is enforced for free.
- [x] 2.6 Preserve single-shot dispatch semantics: each `Dispatch` call opens
      and closes its own session, no session reuse across calls. `attempt`
      always opens a fresh session via `a.newHarness()` and `defer
      sess.Close()`s it, including on the retry path (a second, independent
      `Open` call).
- [x] 2.7 Replace `internal/runner/monitor_test.go`'s `fakeMonitorClient`
      with (or extend it alongside) fakes appropriate to the ACP-backed
      path: a tolerant-decode test with prose around the JSON block, and a
      test asserting exactly one decode-retry followed by a normal
      `Dispatch` error on a second decode failure. Replaced with
      `fakeMonitorHarness`/`fakeMonitorSession`; added
      `TestDecodeMonitorVerdictTolerantOfSurroundingProse` and
      `TestMonitorAdapterDecodeRetryThenFailOpen`, plus retry-boundary
      coverage in `TestMonitorAdapterTimeoutAndConnectFailure` for open
      failure, timeout, a dropped connection, a non-decode agent error, and
      `AcpHarness`'s own exhausted-structured-output error — asserting each
      case's exact `Open`-call count (1 for non-decode failures, 2 for decode
      failures).
- [x] 2.8 Confirm cost/usage stays nil/zero on the ACP-backed
      `MonitorAdapter` path, matching spec 12's existing convention for the
      ACP step-execution path — no new cost/usage tracking added (resolves
      Open Question 2). `drainMonitorEvents` still passes through
      `Event.TotalCostUSD` if a harness ever reports one, but `AcpHarness`
      never populates it, so `MonitorResult.CostKnown` is always `false` in
      practice; asserted directly in
      `TestMonitorAdapterIsolationAndLifecycle`.
- [x] 2.9 Run `gofmt -w` on changed files, then
      `go test ./internal/runner/... ./internal/sentinel/...` and confirm
      `internal/sentinel/supervisor.go`'s existing fail-open path
      (`disableMonitor`, `ActionObserved`) handles the new `Dispatch` error
      shape with no changes needed there. Both packages pass with zero
      changes to `internal/sentinel`; also ran the docs/TESTING.md `-race`
      invocation (`go test -race ./internal/engine ./internal/runner
      ./internal/harness`) and the full root `go test ./...` — the one
      failure (`TestBoundaryBannerFoldsIntoClosingItemLineRange` in
      `internal/tui/monitor`) is pre-existing and unrelated, reproduced
      identically on `main` before this unit's changes.

### [x] 3.0 Out-of-process MCP server for `helpchat`'s tool surface

#### 3.0 Proof Artifact(s)

- Test: a standalone `jig mcp-serve` test dispatching all 10 tools
  (`workflow_snapshot` through `ask_user`) against a scripted MCP client and
  scripted TCP counterparty passes, demonstrates the server works
  independent of `helpchat`/`AcpHarness`.
- Test: a scripted `ask_user`/`final_merge` rendezvous (request sent,
  response delayed, response delivered) completes correctly with no lost or
  duplicated replies, demonstrates the cross-process blocking contract
  matches today's in-process channel semantics.
- Test: an unauthenticated or wrong-token TCP connection attempt is rejected,
  demonstrates the auth-token trust boundary holds.
- Test: killing `jig mcp-serve` mid-request surfaces a clear error to the
  caller rather than hanging, demonstrates crash handling is explicit.

#### 3.0 Tasks

- [x] 3.1 **Spike first, before building the rest of this unit:** check
      whether the real agent binary (Claude Code CLI at minimum) enforces
      its own MCP tool-call timeout on a deliberately slow tool response.
      Record the finding in this task's notes; if the binary kills a slow
      `ask_user` call, stop and revisit the blocking-rendezvous design
      before continuing 3.2+ (per the spec's flagged Unit 3 risk and Open
      Question 1). **Finding:** an empirical local run (a scripted stdio MCP
      server with a deliberately slow tool, driven against the real `claude`
      CLI) was blocked by this session's own auto-mode classifier as a
      nested-agent-spawn action, so verification instead used Claude Code's
      public documentation/changelog (via web search, September 2026):
      `MCP_TOOL_TIMEOUT` (the wall-clock tool-execution timeout) defaults to
      ~100,000,000ms (~28 hours) — nowhere near killing a multi-minute
      operator wait. The only real enforcement is a separate *idle* timeout
      (no response and no progress notification) defaulting to 30 minutes
      for stdio MCP servers, tunable via `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT`
      (0 disables it) and resettable by progress notifications — both far
      more generous than the blocking-rendezvous design needs. Conclusion:
      the real agent binary does not kill a slow `ask_user`/`final_merge`
      call under normal conditions; the blocking-rendezvous design proceeds
      as specified without revision. If an operator's response time ever
      threatens the 30-minute stdio idle window in practice, `mcp-serve` can
      send MCP progress notifications while blocked to reset it — noted here
      for awareness, not implemented in this unit (no proof artifact
      requires it, per the spec's Non-Goal 8).
- [x] 3.2 Define the request/response wire contract for the loopback TCP
      side: message framing, the 10 tool names/argument shapes mirrored
      from `internal/helpchat/tools.go`'s existing `claudecode.McpTool`
      definitions, and the auth handshake (per-session random token sent as
      the connection's first message). Documented as a package-doc comment
      at the top of `cmd/jig/mcp_serve.go`: agent-facing stdio is real MCP
      JSON-RPC 2.0, newline-delimited (one JSON message per line — verified
      via the MCP spec's transport docs that this is *not* LSP-style
      Content-Length framing, which the initial design draft mistakenly
      assumed); the jig-facing loopback TCP side is jig-owned
      newline-delimited JSON (`authMessage`/`authAck` for the handshake,
      `forwardRequest`/`forwardResponse` correlated by `id` for tool calls,
      chosen over Content-Length framing for symmetry with the agent-facing
      side and because `encoding/json` never emits raw newlines inside an
      encoded value, so line-delimiting is safe for arbitrary tool
      arguments/results).
- [x] 3.3 Implement `cmd/jig/mcp_serve.go`: a hidden `jig mcp-serve`
      subcommand wired into `cmd/jig/main.go`'s switch dispatch, speaking
      real MCP JSON-RPC over its own stdio (the 10 tools as MCP tool
      definitions) and forwarding each tool call as a request over a TCP
      connection to the port/token supplied via environment variables.
      `mcp-serve` is the TCP *client* (dials out), matching Unit 4's design
      that the main jig process binds the listener before spawning this
      subcommand.
- [x] 3.4 Implement the blocking rendezvous contract for `resolve_review`'s
      `final_merge` decision and `ask_user`: the tool handler blocks on a
      reply from the TCP connection exactly as today's channel-based
      handler blocks on a Go channel. `handleToolCall`'s `select` on the
      per-request response channel has no timeout by design, matching
      task 3.1's spike finding that the real agent binary does not enforce
      a tool-call timeout in the range this needs.
- [x] 3.5 Implement lifecycle: ready-to-accept-connections signal, clean
      shutdown when the session ends, and explicit crash handling (a
      request in flight when the process is killed surfaces a clear error,
      not a hang). "Ready to accept" is implicit in the auth handshake
      itself (agent-facing stdio only starts once `authenticate` returns);
      clean shutdown is stdin EOF (agent ended the session) draining
      in-flight goroutines via a `sync.WaitGroup` before closing the TCP
      connection; crash handling covers both directions — a dropped TCP
      connection fails every still-pending tools/call with a clear error
      (`failAllPending`) instead of hanging the agent, and
      `TestMcpServeSubprocessKillMidRequest` confirms that killing the real
      `mcp-serve` process itself while a call is in flight closes its
      stdout promptly rather than hanging its caller.
- [x] 3.6 Implement auth: reject connections that send no token or the
      wrong token, closing the connection. The rejection itself is the main
      jig process's responsibility (Unit 4 implements that server side);
      Unit 3's job is the client half (`authenticate` sending the token as
      the first message and failing clearly if no ack arrives) plus the
      test-side scripted counterparty
      (`startRejectingFakeJigServer`/`fakeJigServer.acceptLoop`'s
      token-mismatch branch) that stands in for that future server logic to
      prove the client half handles rejection correctly, per this unit's
      "standalone, no `AcpHarness`" scope.
- [x] 3.7 Write `cmd/jig/mcp_serve_test.go`: standalone tests using a
      scripted MCP client and a scripted TCP counterparty (standing in for
      the main jig process) covering all 10 tools, the `ask_user`/
      `final_merge` rendezvous (request → delayed response → delivery, no
      lost/duplicated replies), wrong-token rejection, and mid-request crash
      handling. No TUI or `AcpHarness` involvement in this unit's tests.
      Implemented as `TestMcpServeAllTenToolsDispatch` (table-driven, all 10
      tools plus a `tools/list` name/order check),
      `TestMcpServeAskUserFinalMergeRendezvous`, `TestMcpServeAuthRejection`
      (wrong token / no token / always-rejecting counterparty subtests), and
      `TestMcpServeSubprocessKillMidRequest` — the last one re-executes this
      test binary as a real OS subprocess via
      `exec.Command(os.Args[0], "-test.run=^TestMcpServeSubprocessHelper$")`,
      mirroring the `JIG_ACP_FIXTURE`/`TestACPFixtureProcess` subprocess
      pattern already used in
      `internal/harness/security_integration_test.go`, so the kill is a real
      `SIGKILL` on a real process, not a simulated cancellation.
- [x] 3.8 Run `gofmt -w` on changed files, then
      `go test ./cmd/jig/... -run TestMcpServe` (or the equivalent focused
      invocation) and `go vet ./...`. Also ran `go test -race
      ./cmd/jig/... -run TestMcpServe`, `go test ./cmd/jig/...` (full
      package), and a full root `go build ./... && go vet ./... && go test
      ./...` — all pass; the one failure in the full suite
      (`TestBoundaryBannerFoldsIntoClosingItemLineRange` in
      `internal/tui/monitor`) is the same pre-existing, unrelated failure
      already documented in Unit 2's proof artifact.

### [x] 4.0 Wire `helpchat` onto `AcpHarness` + Unit 3's MCP server

#### 4.0 Proof Artifact(s)

- Manual/scripted: a help-chat session in the TUI exercises at least one
  tool call per registered tool (`workflow_snapshot` through `ask_user`),
  demonstrates identical TUI state transitions to today's SDK-backed path.
- Grep: `grep -rn "claudecode" internal/helpchat/` returns nothing,
  demonstrates the third and last SDK surface is closed.
- CLI: `go test ./internal/helpchat/... ./internal/tui/monitor/...` passes,
  demonstrates no regression in help-chat or the run monitor.
- Test: killing `jig mcp-serve` mid-conversation during a live help-chat
  session surfaces a clear, recoverable error in the TUI rather than a
  hang, demonstrates the process-boundary failure mode the spec flags as
  highest-risk is verified against the real integration, not only Unit 3's
  standalone harness.

#### 4.0 Tasks

- [x] 4.1 Added `MCPServers []McpServerStdio` to
      `internal/harness/capability.go`'s `SessionSpec`, where
      `McpServerStdio` is a new jig-owned type (`Name`/`Command`/`Args`/
      `Env map[string]string`) so no ACP wire type leaks into the field.
      `internal/harness/acp.go`'s `Open` converts it to `[]acpsdk.McpServer`
      via a new `toACPMcpServers` helper and forwards it to
      `harness/acp/conn.go`'s `NewSession`/`LoadSession`, which grew a
      variadic `mcpServers ...acpsdk.McpServer` parameter (nil/empty still
      sends `[]acpsdk.McpServer{}`, so every existing zero-argument caller —
      `cursor.go`, `codex.go`, `client.go`, and their tests — is unaffected).
      Added `TestAcpHarnessForwardsNonEmptyMCPServers` in
      `internal/harness/security_integration_test.go` (extending the
      existing ACP fixture-subprocess pattern: `fixtureAgent.NewSession` now
      records any forwarded MCP server's name/command to the RPC log) —
      proves the previously-untested non-empty path reaches the adapter's
      real `session/new` RPC, not just the Go-level conversion.
- [x] 4.2/4.3 `internal/helpchat/cmds.go` rewritten: `startServerCmd` binds
      the loopback listener + generates the per-session token (in a new
      `internal/helpchat/mcpserve_server.go`'s `toolServer`), and `queryCmd`
      opens each turn's session via a narrow `helpchatHarness` interface
      (`Open(ctx, SessionSpec) (Session, error)`, mirroring
      `runner/monitor.go`'s `monitorHarness` pattern) satisfied by
      `*harness.AcpHarness`, with `SessionSpec.MCPServers` naming
      `command: "jig", args: ["mcp-serve"]` and `env` carrying
      `JIG_MCP_PORT`/`JIG_MCP_TOKEN` (the exact env var names
      `cmd/jig/mcp_serve.go`'s `runMcpServe` already reads). `model.go`'s
      `Model.client`/`msgChan` and `msgs.go`'s message structs retyped from
      `claudecode.Client`/`claudecode.Message` to `harness.Session`/
      `harness.Event`. **Scope/design note beyond the task text:** the
      pre-ACP `connectCmd` pre-flighted a connection at `Init()` without
      querying, to surface early errors before the operator typed anything;
      `AcpHarness.Open` has no connect-without-prompting primitive (it
      always starts a prompt turn immediately), so `Init()` now only starts
      the tool server, and the first real agent connection happens on the
      operator's first message via the same per-turn `queryCmd` path every
      subsequent turn already used — see the proof artifact's "Design
      Deviations" section for the full reasoning.
- [x] 4.4 Implemented `internal/helpchat/mcpserve_server.go`'s `toolServer`:
      binds a loopback listener, authenticates the one connection from the
      spawned `jig mcp-serve` process (duplicating
      `cmd/jig/mcp_serve.go`'s unexported `authMessage`/`authAck`/
      `forwardRequest`/`forwardResponse` wire types verbatim, since a
      `package main` command cannot be imported — matching Unit 2's
      precedent of intentionally duplicating `extractJSONFromText` for the
      same reason), and dispatches each forwarded call on its own goroutine
      against a `map[string]ToolHandler`. Reworked
      `internal/helpchat/tools.go`'s 10 `claudecode.McpTool` builders into
      plain `ToolHandler` functions (`func(ctx, args) (result string,
      isError bool)`) — tool names/descriptions/schemas now live only in
      `cmd/jig/mcp_serve.go`'s `mcpToolDefs` (Unit 3), so this file no
      longer carries them. **Real bug caught during implementation:** the
      first version of `Serve`'s shutdown path called `wg.Wait()` before
      cancelling the per-call `context.Context`, which deadlocks forever if
      any handler is blocked on `ctx.Done()` (ask_user, final_merge) when
      the connection drops — fixed to cancel first, matching the comment
      now in `mcpserve_server.go`; caught by
      `TestToolServerSurvivesSubprocessKillMidRequest` hanging before the
      fix.
- [x] 4.5 `actions.go`/`gate_view.go`/`prompt.go` needed no changes — none
      of the three referenced `claudecode` or the retyped fields.
- [x] 4.6 Confirmed: `monitor_model.go`/`monitor_update.go` needed zero
      changes (no TUI redesign, no new lifecycle wiring). `helpModel`'s
      construction/toggle call sites (`toggleHelpChat`) already compile and
      behave identically against the retyped `Model`; the tool server's
      `Serve` goroutine derives its context from the `Model`'s own `ctx` and
      tears itself down when that context is eventually cancelled, so no new
      "connecting to help-chat tools" TUI state or teardown hook was needed.
- [x] 4.7 Rewrote `internal/helpchat/helpchat_test.go`: `BuildMcpServer`/
      schema-introspection tests replaced with
      `TestToolHandlersRegistersAllTen` (registration) and
      `TestToolServerDispatchesAllTenTools` (a real loopback TCP round trip
      through the exact `jig mcp-serve` wire contract, via a new
      `fakeMcpServeClient` test double); added
      `TestToolServerAskUserFinalMergeRendezvous` (delayed-reply, no
      lost/duplicated response, through the real process boundary) and
      `TestFinalMergeGate_ContextCancelledUnblocks` (the crash-recovery unit
      test at the handler level) alongside the pre-existing
      `TestFinalMergeGate_ChannelRendezvous`. Added
      `TestModelTurnRoundTripThroughFakeHarness` using a scripted
      `fakeHelpchatHarness`/`fakeHelpchatSession` (mirroring
      `runner/monitor_test.go`'s `fakeMonitorHarness` pattern) to prove the
      `harness.Event` → chat-turn translation end to end without a live ACP
      subprocess.
- [x] 4.8 Added `TestToolServerSurvivesSubprocessKillMidRequest`: a real OS
      subprocess (this test binary re-executed via
      `exec.Command(os.Args[0], "-test.run=...")`, mirroring
      `cmd/jig/mcp_serve_test.go`'s own subprocess-kill test and
      `internal/harness/security_integration_test.go`'s `JIG_ACP_FIXTURE`
      pattern) dials the tool server, authenticates, and forwards a genuine
      `ask_user` call, confirmed to have reached the handler before it is
      `SIGKILL`ed; `toolServer.Serve` is asserted to return a clear
      connection-lost error within a bounded timeout rather than hanging.
      This is the live counterpart to Unit 3's own client-side crash test,
      covering the side (the main process's tool server) Unit 4 introduces.
- [x] 4.9 Confirmed: `internal/helpchat` never reads `Event.TotalCostUSD` or
      `Event.Usage` (grepped — zero references), matching spec 12's
      existing convention; no new cost/usage tracking added.
- [x] 4.10 Ran `gofmt -w` on all changed files, then
      `go build ./... && go vet ./... && go test ./...` (root) and
      `(cd harness/acp && go build ./... && go vet ./... && go test ./...)`
      (nested module) — all pass. Also ran
      `go test -race ./internal/tui/... ./internal/helpchat` per
      docs/TESTING.md — passes with the one pre-existing, unrelated failure
      already documented in Units 2 and 3's proof artifacts
      (`TestBoundaryBannerFoldsIntoClosingItemLineRange`), reproduced
      identically and unaffected by this unit's changes. A manual TUI
      walkthrough was not performed (no interactive terminal in this
      environment); the scripted end-to-end tests above exercise the same
      code paths a manual walkthrough would.

### [ ] 5.0 Remove the SDK dependency; final grep-clean closeout

#### 5.0 Proof Artifact(s)

- Grep: `grep -rn "claudecode\|claude-agent-sdk-go" --include="*.go" .`
  (excluding this spec's directory) returns nothing, demonstrates the
  migration is complete repo-wide.
- CLI: `go build ./... && go vet ./... && go test ./...` passes with
  `github.com/severity1/claude-agent-sdk-go` removed from `go.mod`,
  demonstrates the dependency removal is safe.
- CLI: `go mod tidy` produces no diff, demonstrates nothing transitively
  still needs the dependency.
- Diff: `docs/ARCHITECTURE.md`, `CONTEXT.md`, and `AGENTS.md` updated to
  describe ACP as the only transport, demonstrates docs no longer imply a
  transport choice.

#### 5.0 Tasks

- [ ] 5.1 Run `grep -rn "claudecode\|claude-agent-sdk-go" --include="*.go" .`
      (excluding this spec's directory) and confirm zero matches; fix any
      stragglers found.
- [ ] 5.2 Remove `github.com/severity1/claude-agent-sdk-go` from `go.mod`;
      run `go mod tidy` and confirm no diff beyond the removal itself.
- [ ] 5.3 Update `docs/ARCHITECTURE.md` and `AGENTS.md` to describe ACP as
      the only transport (drop any remaining SDK/ACP split language).
- [ ] 5.4 Update `CONTEXT.md`'s "Harness and backend" section to drop the
      `Transport` vocabulary term entirely (already removed as a field in
      Unit 1).
- [ ] 5.5 Update `README.md`'s "Status" section line about "Claude SDK/ACP,
      Cursor ACP, and Codex ACP are supported" to drop the SDK/ACP split.
- [ ] 5.6 Mark `docs/specs/12-spec-acp-claude-harness/12-spec-acp-claude-harness.md`'s
      Non-Goal 5 and `docs/plans/open-goals.md`'s A6 entry as
      resolved/closed, pointing at this spec.
- [ ] 5.7 Run `gofmt -w` on any changed files, then
      `go build ./... && go vet ./... && go test ./...` and
      `(cd harness/acp && go test ./... && go vet ./...)`.
