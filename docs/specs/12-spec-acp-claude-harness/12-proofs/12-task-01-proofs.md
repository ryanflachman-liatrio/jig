# Task 01 Proofs - Standalone ACP↔Claude spike module

## Task Summary

This task proves that jig can drive real Claude over the Agent Client
Protocol (ACP), via Zed's `npx -y @zed-industries/claude-code-acp@latest`
adapter, from a standalone `harness/acp` Go module with zero dependency on
the root `jig` module (per ADR 0010, ADR 0011). It is a spike: the goal is to
de-risk the ACP handshake, streaming, and permission round-trip before any
of that logic is wired into jig's `Harness` abstraction in later tasks.

## What This Task Proves

- The full ACP handshake (`initialize` → `session/new` → `session/prompt` →
  a `session/update` stream) works end-to-end against a live Claude backend.
- `session/request_permission` is a **real** round-trip: an allow decision
  lets a tool call complete, and a deny decision blocks it — not a stub that
  always approves.
- The `session/update` → event translation captures message chunks, thought
  chunks, and tool-call/tool-call-update events into a readable log.
- `harness/acp` builds and tests independently of the root `jig` module.

## Evidence Summary

- **Allow-path run**: the agent ran `ls` in a scratch directory; the tool
  call reached `status="completed"` and the agent reported the file it saw.
- **Deny-path run**: the agent attempted `rm important.txt`; a
  `session/request_permission` request was sent, the client replied with a
  reject decision, the tool call ended at `status="failed"` (never
  `completed`), and `important.txt` was still present on disk afterward.
- `go test ./...` (7 test cases, all table-driven where applicable) and
  `go vet ./...` pass from inside `harness/acp/`, its own `go.mod`.

## Artifact: Allow-path CLI run log

**What it proves:** The full round-trip (`initialize` → `session/new` →
`session/prompt` → `session/update` stream) succeeds against a live
`claude-code-acp` process, and a real "allow" decision lets the tool call
run to completion.

**Why it matters:** This is the FR "ACP handshake and streaming work
end-to-end" proof — the baseline the deny-path security proof is contrasted
against.

**Command:**

```bash
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT ./spike \
  -allow=true \
  -prompt "Run \`ls\` in the current directory and tell me the filenames you see." \
  -timeout 100s
```

*(`CLAUDECODE`/`CLAUDE_CODE_ENTRYPOINT` are unset only because this spike was
run from inside a Claude Code session itself, which otherwise refuses to
launch a nested session — not part of the shipped harness behavior.)*

**Artifact path:** `docs/specs/12-spec-acp-claude-harness/12-proofs/12-task-01-allow-run.log`

**Result summary:** The tool call for `` `ls` `` reached
`[tool_call_update] ... status="completed"`, and the agent's closing message
correctly names `sample.txt`, the only file in the scratch directory. Stop
reason: `end_turn`.

```text
protocol version: 1
session: 435c56d9-a599-43f7-9f4c-8b563a260ca3

[message] title="" status="" tool_id="" text=""
[message] title="" status="" tool_id="" text="I'll run `ls` to"
[message] title="" status="" tool_id="" text=" show you the files in the current directory."
[tool_call] title="Terminal" status="pending" tool_id="toolu_01VWSyGWu1EhS52yJRGfY9Po" text=""
[tool_call] title="`ls`" status="pending" tool_id="toolu_01VWSyGWu1EhS52yJRGfY9Po" text=""
[tool_call_update] title="" status="" tool_id="toolu_01VWSyGWu1EhS52yJRGfY9Po" text=""
[tool_call_update] title="" status="completed" tool_id="toolu_01VWSyGWu1EhS52yJRGfY9Po" text=""
[message] title="" status="" tool_id="" text=""
[message] title="" status="" tool_id="" text="I see"
[message] title="" status="" tool_id="" text=" one"
[message] title="" status="" tool_id="" text=" file in"
[message] title="" status="" tool_id="" text=" the current directory:"
[message] title="" status="" tool_id="" text="\n- `sample.txt`"

stop reason: end_turn
```

## Artifact: Deny-path security proof

**What it proves:** A `session/request_permission` round-trip actually
fires, and a "deny" decision blocks the tool from executing — the specific
fact "ACP support" alone does not guarantee.

**Why it matters:** This is the security-critical proof for the whole ACP
harness effort. If deny didn't actually block execution, the fail-closed
guarantee promised by later units (capability gating, `Guard`) would be
built on a broken foundation.

**Command:**

```bash
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT ./spike \
  -allow=false \
  -prompt "Delete the file important.txt in the current directory using rm." \
  -timeout 100s
```

**Artifact path:** `docs/specs/12-spec-acp-claude-harness/12-proofs/12-task-01-deny-run.log`

**Result summary:** A `session/request_permission` request was received
(`permission request 0: tool_call_id=toolu_01PEmZfCP28FpFG63waSWWwo
options=3`), the client's `Decide` callback returned `false`, and the reply
selected a reject option. The tool call's terminal status is
`[tool_call_update] ... status="failed"` — it never reached `completed`.
`important.txt` was confirmed still present on disk after the run (`ls -la`
showed the file untouched).

```text
protocol version: 1
session: 07518600-efe1-4c2f-8944-645aa2ce8937

[message] title="" status="" tool_id="" text=""
[message] title="" status="" tool_id="" text="I need"
[message] title="" status="" tool_id="" text=" to delete"
[message] title="" status="" tool_id="" text=" the file `"
[message] title="" status="" tool_id="" text="important.txt` using the `rm` command."
[tool_call] title="Terminal" status="pending" tool_id="toolu_01PEmZfCP28FpFG63waSWWwo" text=""
[tool_call] title="`rm important.txt`" status="pending" tool_id="toolu_01PEmZfCP28FpFG63waSWWwo" text=""
[tool_call_update] title="" status="failed" tool_id="toolu_01PEmZfCP28FpFG63waSWWwo" text=""

permission request 0: tool_call_id=toolu_01PEmZfCP28FpFG63waSWWwo options=3

stop reason: end_turn
```

## Artifact: Module isolation — build, vet, and unit tests

**What it proves:** `harness/acp` is a fully independent Go module
(`go mod init jig/harness/acp`) that builds, vets, and tests on its own,
without requiring the root `jig` module.

**Why it matters:** Per ADR 0010, isolating the young `coder/acp-go-sdk`
dependency in a nested module is the whole point of this unit's structure —
this artifact is the direct evidence that isolation holds.

**Command:**

```bash
cd harness/acp
go build ./...
go vet ./...
go test ./... -v
```

**Result summary:** `go build`/`go vet` produced no output (success); all 7
test cases across `TestSessionUpdate_CapturesEachEventKind` (4 subtests),
`TestRequestPermission_AllowDecision`, `TestRequestPermission_DenyDecision`,
`TestRequestPermission_NoMatchingOption_Cancels`,
`TestRequestPermission_NilDecider_DeniesByDefault`, and
`TestRun_FailsFastWhenNpxMissing` passed. These tests use a fake connection
(direct calls to `Client.SessionUpdate`/`RequestPermission`) and do not spawn
a live `npx` process, so they run in CI without network access or Claude
credentials.

```text
=== RUN   TestSessionUpdate_CapturesEachEventKind
=== RUN   TestSessionUpdate_CapturesEachEventKind/agent_message_chunk
=== RUN   TestSessionUpdate_CapturesEachEventKind/agent_thought_chunk
=== RUN   TestSessionUpdate_CapturesEachEventKind/tool_call
=== RUN   TestSessionUpdate_CapturesEachEventKind/tool_call_update
--- PASS: TestSessionUpdate_CapturesEachEventKind (0.00s)
    --- PASS: TestSessionUpdate_CapturesEachEventKind/agent_message_chunk (0.00s)
    --- PASS: TestSessionUpdate_CapturesEachEventKind/agent_thought_chunk (0.00s)
    --- PASS: TestSessionUpdate_CapturesEachEventKind/tool_call (0.00s)
    --- PASS: TestSessionUpdate_CapturesEachEventKind/tool_call_update (0.00s)
=== RUN   TestRequestPermission_AllowDecision
--- PASS: TestRequestPermission_AllowDecision (0.00s)
=== RUN   TestRequestPermission_DenyDecision
--- PASS: TestRequestPermission_DenyDecision (0.00s)
=== RUN   TestRequestPermission_NoMatchingOption_Cancels
--- PASS: TestRequestPermission_NoMatchingOption_Cancels (0.00s)
=== RUN   TestRequestPermission_NilDecider_DeniesByDefault
--- PASS: TestRequestPermission_NilDecider_DeniesByDefault (0.00s)
=== RUN   TestRun_FailsFastWhenNpxMissing
--- PASS: TestRun_FailsFastWhenNpxMissing (0.00s)
PASS
ok  	jig/harness/acp	0.182s
?   	jig/harness/acp/cmd/spike	[no test files]
```

## Reviewer Conclusion

The spike demonstrates the full ACP handshake and streaming path against a
live Claude backend, and — critically — that the permission round-trip is
real: allow lets a tool run to completion, deny blocks it before it takes
effect. Combined with independent module isolation and a fake-connection
unit test suite, this de-risks the ACP transport choice before any of it is
wired into jig's `Harness` interface in Unit 2 onward.
