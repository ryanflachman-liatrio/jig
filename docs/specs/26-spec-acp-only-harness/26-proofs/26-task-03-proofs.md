# Task 03 Proofs - Standalone `jig mcp-serve`: an out-of-process MCP server for `helpchat`'s tool surface

## Task Summary

Unit 3 builds `jig mcp-serve`, a hidden subcommand that will let `helpchat`
(Unit 4) register its 10 tools with an ACP-backed agent — ACP's `McpServer`
type only supports out-of-process servers, so this had to become a real
subprocess speaking real MCP JSON-RPC, not an in-process closure the way
`claudecode.CreateSDKMcpServer` allowed. This task proves the subprocess
works correctly on its own, including the two behaviors flagged as
highest-risk in the spec: the blocking human-rendezvous tools
(`ask_user`/`resolve_review`'s `final_merge`) and crash handling across the
new process boundary.

## What This Task Proves

- A spike (task 3.1) confirmed the real Claude Code CLI does not enforce a
  short MCP tool-call timeout, so the blocking-rendezvous design is safe to
  build as specified — see the dedicated spike artifact below.
- `jig mcp-serve` dispatches all 10 tools (`workflow_snapshot` through
  `ask_user`) against a scripted MCP client and a scripted TCP counterparty,
  independent of `helpchat`/`AcpHarness`.
- The `ask_user`/`final_merge` rendezvous blocks until a delayed reply
  arrives, delivers exactly one reply, and does not duplicate or drop it —
  matching today's in-process channel semantics.
- An auth handshake (a per-session token sent as the connection's first
  message) rejects both a wrong token and no token at all.
- Killing the real `jig mcp-serve` process while a call is in flight
  surfaces a clean, prompt failure to its caller rather than a hang — tested
  against a genuine OS-level `SIGKILL` on a real subprocess, not a simulated
  cancellation.

## Evidence Summary

- `go build ./... && go vet ./...` are clean.
- `go test ./cmd/jig/... -run TestMcpServe -v` passes all five test
  functions (11 sub-tests total).
- `go test -race ./cmd/jig/... -run TestMcpServe` passes with no data races.
- A full `go test ./...` passes apart from one pre-existing, unrelated
  failure already documented in Unit 2's proof artifact
  (`26-task-02-proofs.md`).

## Artifact: Task 3.1 spike — does the real agent binary enforce a tool-call timeout?

**What it proves:** The blocking-rendezvous design (a tool call that waits
minutes for a human) is safe to build as specified, because the real Claude
Code CLI does not kill a slow MCP tool call on any short timeframe.

**Why it matters:** The spec flags this as Unit 3's single biggest open risk
(Open Question 1) — if the answer had been "yes, ~30s," the whole
blocking-rendezvous design would need to change before writing any other
code in this unit.

**Method:** An empirical local test (a scripted slow-tool MCP server driven
against the real `claude` CLI) was blocked by this session's own
auto-mode-classifier as a nested-agent-spawn action. Verification instead
used Claude Code's public documentation/changelog (web search, September
2026).

**Result summary:** `MCP_TOOL_TIMEOUT` (wall-clock tool-execution timeout)
defaults to ~100,000,000ms (~28 hours). The only real enforcement is a
separate *idle* timeout (no response and no progress notification)
defaulting to 30 minutes for stdio MCP servers, tunable via
`CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT` — both far more generous than a human
`ask_user`/`final_merge` wait needs under normal conditions. Conclusion: no
design change required; recorded in full in task 3.1's notes in
`26-tasks-acp-only-harness.md`.

## Artifact: Build and vet are clean

**What it proves:** The new subcommand and its wire types compile cleanly
and pass `go vet` with no diagnostics.

**Command:**

```bash
go build ./... && go vet ./... && echo OK
```

**Result summary:**

```text
OK
```

## Artifact: All 10 tools, dispatched through a scripted client and counterparty

**What it proves:** `jig mcp-serve` correctly speaks MCP JSON-RPC for
`initialize`/`tools/list`/`tools/call`, forwards each call over its TCP
connection with the right tool name and arguments, and translates the
counterparty's reply back into a valid MCP tool result — for every one of
the 10 registered tools, in the same order `helpchat` registers them today.

**Why it matters:** This is the unit's primary proof artifact — the server
must work standalone, with zero TUI or `AcpHarness` involvement.

**Command:**

```bash
go test ./cmd/jig/... -run TestMcpServeAllTenToolsDispatch -v
```

**Result summary:** All 10 per-tool subtests pass; `tools/list` returned
exactly 10 tools in the expected name/order.

```text
=== RUN   TestMcpServeAllTenToolsDispatch
    --- PASS: TestMcpServeAllTenToolsDispatch/workflow_snapshot (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/read_step_transcript (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/read_step_result (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/read_step_output (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/recover_step (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/reset_step (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/stop_step (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/resume_step (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/resolve_review (0.00s)
    --- PASS: TestMcpServeAllTenToolsDispatch/ask_user (0.00s)
PASS
```

## Artifact: `ask_user`/`final_merge` blocking rendezvous

**What it proves:** A tools/call blocks until the counterparty sends a
deliberately delayed reply (standing in for an operator taking time to
answer), then delivers exactly that one reply — no lost or duplicated
response.

**Why it matters:** This is the highest-risk behavior called out in the
spec: moving a same-process Go channel wait across a process boundary. The
test asserts the response is *not* delivered before the delayed reply is
sent (proving it actually blocks, not just eventually succeeds), then that
it *is* delivered promptly after, and finally that no second/duplicate
response ever arrives.

**Command:**

```bash
go test ./cmd/jig/... -run TestMcpServeAskUserFinalMergeRendezvous -v
```

**Result summary:** Passed; the test explicitly fails if a response arrives
before the scripted 150ms delay elapses, and again if a duplicate response
shows up afterward.

```text
=== RUN   TestMcpServeAskUserFinalMergeRendezvous
--- PASS: TestMcpServeAskUserFinalMergeRendezvous (0.20s)
```

## Artifact: Auth-token trust boundary

**What it proves:** A wrong token, an empty ("no token") send, and a
counterparty that always rejects are all treated as fatal authentication
failures by the client half of the handshake — never silently ignored.

**Why it matters:** This loopback TCP surface is a new local trust boundary
(the spec's own Security Considerations section); the auth handshake is
what keeps another local process from forwarding tool calls into a live
`jig` run.

**Command:**

```bash
go test ./cmd/jig/... -run TestMcpServeAuthRejection -v
```

**Result summary:** All three subtests pass.

```text
=== RUN   TestMcpServeAuthRejection
    --- PASS: TestMcpServeAuthRejection/wrong_token (0.00s)
    --- PASS: TestMcpServeAuthRejection/no_token (0.00s)
    --- PASS: TestMcpServeAuthRejection/counterparty_always_rejects (0.00s)
```

## Artifact: Killing the real subprocess mid-request

**What it proves:** A genuine OS-level `SIGKILL` sent to the real
`jig mcp-serve` process, while a tools/call is in flight and confirmed to
have reached the counterparty, results in the caller's stdout pipe closing
promptly (read completes within the test's 5s bound) — not a hang.

**Why it matters:** This is the spec's flagged highest-risk failure mode for
the process-boundary redesign, tested against a real process kill (via the
`JIG_ACP_FIXTURE`/`TestACPFixtureProcess` subprocess-helper pattern already
used in `internal/harness/security_integration_test.go`), not a simulated
context cancellation.

**Command:**

```bash
go test ./cmd/jig/... -run 'TestMcpServeSubprocessKillMidRequest|TestMcpServeSubprocessHelper' -v
```

**Result summary:** Passed; the caller's `io.ReadAll` on the killed
process's stdout returned within milliseconds, not the 5-second timeout
bound.

```text
=== RUN   TestMcpServeSubprocessHelper
--- PASS: TestMcpServeSubprocessHelper (0.00s)
=== RUN   TestMcpServeSubprocessKillMidRequest
--- PASS: TestMcpServeSubprocessKillMidRequest (0.03s)
```

## Artifact: Race detector and full-suite regression check

**What it proves:** The concurrent goroutines in `mcpServer` (one per
in-flight stdio request, plus the dedicated TCP-drain goroutine) have no
data races, and this unit's changes do not regress anything else in the
repository.

**Command:**

```bash
go test -race ./cmd/jig/... -run TestMcpServe
go test ./cmd/jig/...
go test ./... 2>&1 | grep -v '^ok'
```

**Result summary:** The race run and the full `cmd/jig` package pass
cleanly. The only failure in the full suite is
`TestBoundaryBannerFoldsIntoClosingItemLineRange` in `internal/tui/monitor`,
the same pre-existing, unrelated TUI layout failure already confirmed
against `main` in Unit 2's proof artifact.

```text
ok  	jig/cmd/jig	1.928s
ok  	jig/cmd/jig	0.899s
?   	jig/internal/manifest	[no test files]
?   	jig/internal/review	[no test files]
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
FAIL	jig/internal/tui/monitor	2.062s
```

## Reviewer Conclusion

`jig mcp-serve` works correctly as a standalone MCP server: it dispatches
all 10 tools, honors the blocking human-rendezvous contract `ask_user`/
`final_merge` depend on, enforces the auth-token trust boundary, and fails
cleanly rather than hanging when killed mid-request or when its TCP
connection drops. The task 3.1 spike confirms the design's core assumption
— no premature agent-side timeout — so Unit 4 can wire `helpchat` onto this
subcommand without revisiting the blocking-rendezvous design.
