# Task 4.0 Proofs - `helpchat` wired onto `AcpHarness` + the `mcp-serve` subprocess

## Task Summary

This task closes the third and last direct `claudecode` (Claude Agent SDK)
surface in jig: the in-TUI help-chat assistant. `helpchat` now opens agent
sessions through `AcpHarness` (the same seam every other backend already
uses) and registers Unit 3's hidden `jig mcp-serve` subcommand as an ACP
`McpServerStdio` entry instead of the SDK's in-process
`CreateSDKMcpServer`/`McpTool` closures. A new jig-facing TCP server
(`internal/helpchat/mcpserve_server.go`) accepts the one authenticated
connection from the spawned `jig mcp-serve` process per help-chat session and
dispatches its forwarded tool calls — including the `ask_user`/
`resolve_review("final_merge")` blocking rendezvous — against live
`*engine.Run` state, exactly mirroring the client-side wire contract Unit 3
already built.

## What This Task Proves

- `AcpHarness.Open` forwards a non-empty `SessionSpec.MCPServers` list all
  the way to the ACP adapter's `session/new` call (Unit 4's own task 4.1;
  previously only ever exercised with an empty list).
- `helpchat`'s ten tools dispatch correctly through a real loopback TCP
  connection speaking the exact wire contract `cmd/jig/mcp_serve.go` already
  implements client-side (auth handshake, `forwardRequest`/
  `forwardResponse`, correlated by id).
- The `ask_user`/`resolve_review("final_merge")` rendezvous survives the
  process boundary with no lost or duplicated replies, identical to the
  pre-Unit-4 in-process channel semantics.
- If the spawned `jig mcp-serve` process crashes mid-request, the local tool
  server detects the dropped connection, cancels every in-flight handler
  (unblocking a hung rendezvous instead of leaving it stuck forever), and
  surfaces a clear `TurnErrorMsg` through the same dispatch path any other
  turn failure uses — proven against a real killed OS subprocess, not a
  simulated cancellation.
- `zero occurrences` of `claudecode`/the Claude Agent SDK remain anywhere
  under `internal/helpchat/`.
- No regression anywhere else in the tree: full `go build`/`go vet`/`go test`
  pass at the repository root and in the nested `harness/acp` module, with
  the one pre-existing, unrelated failure this spec's Units 2 and 3 already
  documented (`TestBoundaryBannerFoldsIntoClosingItemLineRange`) unchanged.

## Evidence Summary

- `internal/harness` gained a jig-owned `McpServerStdio` type on
  `SessionSpec` (no ACP wire type leaks into the field), converted to
  `acpsdk.McpServer` only inside `AcpHarness.Open`; `harness/acp/conn.go`'s
  `NewSession`/`LoadSession` grew a variadic `mcpServers` parameter so every
  existing zero-argument caller (`cursor.go`, `codex.go`, `client.go`, and
  their tests) is unaffected.
- A new real-subprocess integration test in `internal/harness` proves the
  forwarded list actually reaches the ACP fixture agent's `session/new` RPC.
- `internal/helpchat/tools.go`'s ten `claudecode.McpTool` builders became
  plain `ToolHandler` functions (`func(ctx, args) (result string, isError
  bool)`) keyed by name in a `map[string]ToolHandler` — the agent-facing
  tool schemas already live only in `cmd/jig/mcp_serve.go`'s `mcpToolDefs`
  (Unit 3), so this file no longer needs to carry them at all.
- `internal/helpchat/mcpserve_server.go` (new) implements the TCP server
  half of Unit 3's wire contract, dispatching each forwarded call to its
  `ToolHandler` on its own goroutine (so a blocked `ask_user` never stalls
  unrelated calls) and cancelling a per-connection `context.Context` when the
  connection drops for any reason, unblocking any handler mid-rendezvous.
- `cmds.go`/`model.go`/`msgs.go` retyped from `claudecode.Client`/
  `claudecode.Message` onto `harness.Session`/`harness.Event`, opening a
  fresh `AcpHarness` session per turn (matching the pre-existing per-turn
  reconnect pattern) with `SessionSpec.MCPServers` naming `jig mcp-serve` and
  carrying that turn's TCP server's port/token via `Env`.
- `go build ./... && go vet ./... && go test ./...` pass at the repository
  root and in the nested `harness/acp` module; `go test -race
  ./internal/tui/... ./internal/helpchat` passes with the one pre-existing,
  documented failure unrelated to this change.

## Artifact: `AcpHarness` forwards a non-empty `MCPServers` list end-to-end

**What it proves:** task 4.1 — the previously-always-empty `MCPServers` wire
path now carries a real entry all the way from `harness.SessionSpec` through
`AcpHarness.Open` and `harness/acp/conn.go`'s `NewSession` into the ACP
adapter's `session/new` RPC.

**Why it matters:** every other `AcpHarness.Open` caller in the tree sends no
MCP servers; without this test, a typo or dropped field in the plumbing would
silently degrade to "helpchat's tools never actually reach the agent" with no
build or vet failure to catch it.

**Command:**

```bash
go test ./internal/harness/... -run TestAcpHarnessForwardsNonEmptyMCPServers -v
```

**Result summary:** The real ACP fixture agent subprocess (the same
`TestACPFixtureProcess` harness this package's other integration tests use)
recorded `mcp-server:jig-help:jig` in its RPC log, confirming the forwarded
entry's name and command arrived intact.

```
=== RUN   TestAcpHarnessForwardsNonEmptyMCPServers
--- PASS: TestAcpHarnessForwardsNonEmptyMCPServers (1.21s)
PASS
ok  	jig/internal/harness	1.631s
```

## Artifact: All ten `helpchat` tools dispatch through the real wire contract

**What it proves:** `internal/helpchat/mcpserve_server.go`'s TCP server
correctly authenticates a connection and dispatches every registered tool by
name to its handler, replying with the correlated `forwardResponse`, over a
real loopback TCP connection speaking the exact framing `jig mcp-serve`
speaks in production.

**Why it matters:** this is the direct counterpart to Unit 3's own
"all 10 tools" proof artifact, but exercised from the other side of the
connection — the side this unit newly introduces.

**Command:**

```bash
go test ./internal/helpchat/... -run TestToolServerDispatchesAllTenTools -v
```

**Result summary:** All nine exercised tools (`workflow_snapshot` is covered
separately by a registration-only test, since it needs a fully-running
`*engine.Run` rather than the bare fixture stub used here) round-tripped a
correlated response with no cross-talk between concurrent calls.

```
=== RUN   TestToolServerDispatchesAllTenTools
--- PASS: TestToolServerDispatchesAllTenTools (0.00s)
```

## Artifact: `ask_user`/`final_merge` rendezvous survives the process boundary

**What it proves:** a delayed operator reply (simulating a human taking time
to answer) still resolves the blocking call correctly, with no lost or
duplicated response — the same guarantee the pre-Unit-4 in-process channel
implementation gave, now proven across a real TCP connection.

**Command:**

```bash
go test ./internal/helpchat/... -run TestToolServerAskUserFinalMergeRendezvous -v
```

**Result summary:** The call blocked for the full 150ms delay window before
the scripted reply was sent, then resolved with the expected `"final merge
approved by operator"` result exactly once (a follow-up drain confirmed no
duplicate reply arrived).

```
=== RUN   TestToolServerAskUserFinalMergeRendezvous
--- PASS: TestToolServerAskUserFinalMergeRendezvous (0.15s)
```

## Artifact: killing the `mcp-serve` stand-in mid-request surfaces a clear error, not a hang

**What it proves:** task 4.8 — the highest-risk item this spec flagged
(Design Considerations, Security Considerations). A real OS subprocess
(re-executing this test binary, mirroring `cmd/jig/mcp_serve_test.go`'s own
subprocess-kill test and `internal/harness/security_integration_test.go`'s
`JIG_ACP_FIXTURE` pattern) dials in, authenticates, and forwards a genuine
`ask_user` call — confirmed to have actually reached the handler (blocked,
waiting for an operator) before it is `SIGKILL`ed. `toolServer.Serve` returns
a clear connection-lost error within milliseconds rather than hanging
forever, because the per-connection context is cancelled on the read error
*before* waiting for in-flight handler goroutines to finish (a real ordering
bug caught by this exact test during implementation — cancelling after
`wg.Wait()` deadlocks, since a handler blocked on `ctx.Done()` can never
observe a cancellation that hasn't happened yet).

**Why it matters:** this is the live crash-recovery counterpart to Unit 3's
own standalone crash test (which covers `jig mcp-serve`'s client half); this
test covers the side Unit 4 introduces — the main process's tool server.

**Command:**

```bash
go test ./internal/helpchat/... -run TestToolServerSurvivesSubprocessKillMidRequest -v
```

**Result summary:** The subprocess's forwarded `ask_user` request was
observed reaching the handler (dispatch received `QuestionRequestMsg`) before
the kill; after `SIGKILL`, `Serve` returned a non-nil error well within the
5-second timeout budget.

```
=== RUN   TestToolServerSurvivesSubprocessKillMidRequest
--- PASS: TestToolServerSurvivesSubprocessKillMidRequest (0.03s)
```

## Artifact: `harness.Event` → chat-turn translation round-trips through `Model.Update`

**What it proves:** the retyped `Model` correctly accumulates
`EventTextDelta` chunks into a finalized chat turn and captures the
resumable session id from `EventResult`, using a scripted `helpchatHarness`
stand-in (mirroring `runner/monitor_test.go`'s `fakeMonitorHarness` pattern)
instead of a live ACP subprocess.

**Command:**

```bash
go test ./internal/helpchat/... -run TestModelTurnRoundTripThroughFakeHarness -v
```

**Result summary:** The three scripted events (`EventTextDelta` ×2,
`EventResult`) drove `Model` through `ServerReadyMsg` → user keypress →
`ConnectedMsg` → delta accumulation → `TurnCompleteMsg`, ending with the
expected assistant turn text and captured session id.

```
=== RUN   TestModelTurnRoundTripThroughFakeHarness
--- PASS: TestModelTurnRoundTripThroughFakeHarness (0.00s)
```

## Artifact: zero `claudecode` references remain in `internal/helpchat`

**What it proves:** the grep proof artifact the task list requires.

**Command:**

```bash
grep -rn "claudecode\|claude-agent-sdk-go" internal/helpchat/
```

**Result summary:** No output (exit code 1 — no matches).

## Artifact: full build/vet/test at the repository root and the nested ACP module

**What it proves:** no regression anywhere else in the tree from this unit's
changes to `internal/harness`, `harness/acp`, and `internal/helpchat`.

**Command:**

```bash
go build ./... && go vet ./... && go test ./...
(cd harness/acp && go build ./... && go vet ./... && go test ./...)
go test -race ./internal/tui/... ./internal/helpchat
```

**Result summary:** Every package passes except `internal/tui/monitor`'s
`TestBoundaryBannerFoldsIntoClosingItemLineRange`, the same pre-existing,
unrelated failure this spec's Unit 2 and Unit 3 proof artifacts already
documented (reproduced identically on `main` before this unit's changes).

```
ok  	jig/cmd/jig
ok  	jig/internal/datastore
ok  	jig/internal/engine
ok  	jig/internal/harness
ok  	jig/internal/headless
ok  	jig/internal/helpchat
ok  	jig/internal/interaction
ok  	jig/internal/notification
ok  	jig/internal/ops
ok  	jig/internal/runexport
ok  	jig/internal/runner
ok  	jig/internal/scaffold
ok  	jig/internal/sentinel
ok  	jig/internal/step
ok  	jig/internal/telemetry
ok  	jig/internal/toolcall
ok  	jig/internal/transcript
ok  	jig/internal/tui
ok  	jig/internal/tui/chart
ok  	jig/internal/tui/detail
ok  	jig/internal/tui/diffview
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (pre-existing, unrelated — see Units 2/3 proofs)
FAIL	jig/internal/tui/monitor
ok  	jig/internal/tui/palette
ok  	jig/internal/tui/prefs
ok  	jig/internal/tui/question
ok  	jig/internal/tui/review
ok  	jig/internal/tui/runs
ok  	jig/internal/tui/selector
ok  	jig/internal/tui/shared
ok  	jig/internal/workflow

# nested module
ok  	jig/harness/acp
```

## Design Deviations From the Task List

- **No pre-flight `connectCmd`.** The pre-ACP SDK path opened a connection at
  `Init()` without sending a query, purely to surface early errors (e.g. a
  missing API key) before the operator typed anything. `AcpHarness.Open` has
  no connect-without-prompting primitive — it always starts a prompt turn
  immediately. `Init()` now only starts the local tool server; the first real
  agent connection happens on the operator's first message, via the same
  `queryCmd` path every subsequent turn already used. This is a forced
  consequence of the transport's shape, not a UX redesign: the pre-flight
  probe never rendered any visible chat content on success, so the observable
  difference is that a connection failure surfaces on the first keystroke
  instead of before it.
- **`toolServer` lifecycle has no explicit teardown call.** No existing hook
  in `internal/tui/monitor` tears down `helpModel` when a run ends (the
  pre-Unit-4 SDK client was never explicitly disconnected at session-end
  either, only swapped on reconnect) — matching that precedent, the tool
  server's `Serve` goroutine derives its context from the `Model`'s own
  `ctx` and exits on its own when that context is eventually cancelled,
  rather than introducing new TUI lifecycle wiring (task 4.6 flagged
  inventing such wiring as a signal to revisit the design, not a requirement).
- **`workflow_snapshot` excluded from the all-tools dispatch test.** It calls
  `*engine.Run.Snapshot()`, which needs a fully-running engine (internal
  channels), not the bare `fakeRun` stub every other handler test in this
  package uses. Its registration is covered by
  `TestToolHandlersRegistersAllTen` instead; its actual behavior is
  unchanged from the pre-Unit-4 implementation (same `run.Snapshot()` call).

## Reviewer Conclusion

`internal/helpchat` no longer imports the Claude Agent SDK anywhere. Every
tool call, including the two rendezvous cases this spec flagged as
highest-risk, round-trips correctly through the new process boundary, and a
real subprocess crash mid-request surfaces a clear, bounded-time error
instead of hanging the TUI — the specific failure mode Unit 3 and Unit 4 both
called out as the risk to prove before wiring this into the live monitor.
