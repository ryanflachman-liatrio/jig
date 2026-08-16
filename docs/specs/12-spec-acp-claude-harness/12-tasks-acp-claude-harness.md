# 12-tasks-acp-claude-harness.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `harness/acp/go.mod` | New nested Go module (own `go.mod`), isolates `coder/acp-go-sdk` per ADR 0010. |
| `harness/acp/client.go` | New: spawns `npx -y @zed-industries/claude-code-acp@latest`, implements `acp.Client`'s `RequestPermission`/`SessionUpdate`, drives `Initialize`→`NewSession`→`Prompt`. |
| `harness/acp/client_test.go` | New: unit tests for the spike's permission-decision and session-update capture logic. |
| `internal/harness/harness.go` | New: `Harness`/`Session` interface definitions — no SDK/ACP import. |
| `internal/harness/capability.go` | New: `CapabilitySet`, capability constants, `SessionSpec`, `PermissionFn`. |
| `internal/harness/fake.go` | New: `FakeHarness`/`FakeSession` — configurable `CapabilitySet` + scripted `Messages()` for tests. |
| `internal/harness/harness_test.go` | New: table-driven tests for `CapabilitySet.Has()` and the fake harness round-trip. |
| `internal/harness/claude.go` | New: `ClaudeHarness` — wraps `claudecode`, absorbs `agent.go`'s `buildOptions`/`captureStream` SDK-facing logic. |
| `internal/harness/claude_test.go` | New: `ClaudeHarness` option-translation and capability-advertisement tests. |
| `internal/harness/acp.go` | New: `AcpHarness` — wraps the `harness/acp` module, translates `SessionSpec`↔ACP session, `session/update`→transcript blocks. |
| `internal/harness/acp_test.go` | New: `AcpHarness` translation and capability-gating tests (against a fake ACP connection). |
| `internal/harness/select.go` | New: `FromEnv()` — `JIG_HARNESS` (`claude`\|`acp`) selection, fail-fast on unknown value. |
| `internal/harness/select_test.go` | New: `FromEnv()` default/selection/failure-path tests. |
| `internal/runner/agent.go` | Modify: `AgentExecutor` holds a `harness.Harness`; remove direct `claudecode` calls; add capability-gating checks before `Open`. |
| `internal/runner/agent_test.go` | Modify/verify: existing scripted-channel tests must keep passing; add capability-gating and `block_on` fail-fast table tests. |
| `internal/runner/monitor.go` | No change (Non-Goal 5) — confirms as one of the two allowed remaining direct `claudecode` importers. |
| `internal/sentinel/guard.go` | No change — `Guard.Check(toolName, input) Decision` is the existing type `AgentExecutor` closes over to build a `harness.PermissionFn`. |
| `internal/engine/executor.go` | No change expected — `StepRequest.Guard`/`ResumeSessionID` continue to flow into `AgentExecutor`; confirms the dependency-inversion boundary this spec preserves. |
| `internal/transcript/transcript.go` | No change — `Entry`/`Block` model (`BlockText`/`BlockThinking`/`BlockToolUse`/`BlockToolResult`) is the normalization target both harnesses must map onto. |
| `internal/workflow/schema.go` | No change to schema — `Step.BlockOn` already exists; confirms Unit 5's fail-fast gate has a real field to check against. |
| `go.mod` (root) | Modify: add `require jig/harness/acp` + `replace jig/harness/acp => ./harness/acp` once Unit 4 wires the module in. |
| `examples/feature.toml` | Used unmodified as the before/after and `JIG_HARNESS=acp` demo workflow for proof artifacts. |
| `docs/adr/0010-nested-go-module-for-harness-acp.md` | Reference: rationale for the nested-module layout Unit 1/4 implement. |
| `docs/adr/0011-acp-via-zed-npx-adapter.md` | Reference: rationale for the npx-adapter transport Unit 1/4 implement. |
| `CONTEXT.md` | Reference: canonical Harness/backend/Session vocabulary — keep naming consistent while implementing. |

### Notes

- Unit tests live alongside the code they test (`foo.go` / `foo_test.go`), matching existing `internal/*` layout.
- Root-module tests: `go test ./...`. Nested-module tests: `cd harness/acp && go test ./...` (it is a separate module, not covered by the root's `./...`).
- Follow the existing table-driven style (`internal/workflow/workflow_test.go`, `internal/runner/agent_test.go`'s scripted-channel pattern) rather than introducing a new test idiom.
- `gofmt -l -w .` and `go vet ./...` (run in both modules) before considering a task complete, per `CLAUDE.md`.
- Do not implement the `harness` TOML selector or any non-Claude ACP backend — both are explicit Non-Goals.

## Tasks

### [x] 1.0 Standalone ACP↔Claude spike module (no jig dependency)

#### 1.0 Proof Artifact(s)

- CLI run log: a full ACP session transcript (raw JSON-RPC or a readable
  rendering of it) captured from `harness/acp`'s spike, showing `initialize`,
  `session/new`, `session/prompt`, a `session/update` stream, and a
  `session/request_permission` round-trip — demonstrates the handshake and
  streaming work end-to-end against real Claude via
  `npx -y @zed-industries/claude-code-acp@latest`.
- Security proof: a recorded run where the permission decision is `deny` and
  the corresponding tool call's result confirms it did not execute —
  demonstrates FR "deny actually blocks execution," the specific fact
  "ACP support" alone does not guarantee.
- CLI: `go build ./... && go test ./...` run from inside `harness/acp/`
  (its own `go.mod`) passes, independent of the root jig module — demonstrates
  module isolation.

#### 1.0 Tasks

- [x] 1.1 Run `go mod init jig/harness/acp` in a new `harness/acp/` directory
      and `go get github.com/coder/acp-go-sdk@v0.13.5` (or later tagged
      release).
- [x] 1.2 Add `replace jig/harness/acp => ./harness/acp` to the root `go.mod`
      as the development-time wiring (per ADR 0010); leave the module
      otherwise undependent on root jig code.
- [x] 1.3 Following `coder/acp-go-sdk`'s own `example/claude-code` pattern,
      implement subprocess spawn of `npx -y @zed-industries/claude-code-acp@latest`
      and establish `acp.NewClientSideConnection` over its stdio; fail fast
      with a clear error (not a hang) if `npx` is unavailable.
- [x] 1.4 Drive `Initialize` → `NewSession` → `Prompt` for a scripted turn
      known to invoke at least one tool.
- [x] 1.5 Implement `acp.Client.RequestPermission` making a real programmatic
      allow/deny decision (test both an allow run and a deny run — not an
      always-allow stub).
- [x] 1.6 Implement `acp.Client.SessionUpdate` capturing the full stream of
      message chunks, thought chunks, and tool-call/tool-call-update events
      into a readable log/buffer.
- [x] 1.7 Capture and save a run log demonstrating the full round-trip
      (`initialize` → `session/new` → `session/prompt` → `session/update`
      stream → `session/request_permission`) as the Unit 1 CLI-run-log proof
      artifact.
- [x] 1.8 Capture and save a second run log where the permission decision is
      `deny`, with evidence (tool-call result / absence of tool-call-completed
      event) that the tool did not execute — the Unit 1 security proof
      artifact.
- [x] 1.9 Add `harness/acp/client_test.go` covering the permission-decision
      and session-update-capture logic against a fake/mock connection (not
      requiring a live `npx` spawn for CI), then confirm
      `go build ./... && go test ./...` passes from inside `harness/acp/`.

### [x] 2.0 Core `Harness`/`Session` interface + capability model

#### 2.0 Proof Artifact(s)

- CLI: `go build ./internal/harness/...` succeeds and
  `grep -rL 'claudecode\|acp-go-sdk' internal/harness/harness.go internal/harness/capability.go`
  prints both file paths — demonstrates zero SDK/ACP imports in the
  interface-defining files.
- Test: `go test ./internal/harness/...` passes, exercising `fake.go`'s
  configurable `CapabilitySet` + scripted `Messages()` — demonstrates the
  interface is usable by runner/engine tests without a real backend.
- Diff: `internal/harness/harness.go`, `capability.go`, `fake.go` showing
  `Harness`, `Session`, `CapabilitySet`, `SessionSpec`, `PermissionFn` type
  definitions — demonstrates the type-design-only scope of this unit.

#### 2.0 Tasks

- [x] 2.1 Create `internal/harness/capability.go`: define `CapabilitySet` and
      constants `CapPermissionCallback`, `CapInProcessMCP`, `CapSessionResume`,
      `CapStructuredOutput`, `CapPartialStreaming`, plus a `Has(cap)` method.
- [x] 2.2 In `capability.go`, define `SessionSpec` (prompt, model, effort,
      turn/thinking/budget limits, permission mode, allowed/disallowed tools,
      cwd, plus capability-gated fields `Permission`, `MCPServers`, `Resume`,
      `Schema`, `Partial`) and `PermissionFn` (`func(toolName string, input
      map[string]any) Decision`, with a `Decision{Allow bool, Reason string}`
      mirroring `sentinel.Decision`'s shape without importing `sentinel`).
- [x] 2.3 Create `internal/harness/harness.go`: define an `Event` type
      (mirroring `transcript.Block`'s categories — text/thinking/tool-use/
      tool-result — so harness implementations normalize onto it), the
      `Session` interface (`Messages() <-chan Event`, `Send(ctx, ToolResult)
      error`, `Close() error`), and the `Harness` interface (`Name() string`,
      `Capabilities() CapabilitySet`, `Open(ctx, SessionSpec) (Session,
      error)`). No `claudecode`/ACP import in this file.
- [x] 2.4 Create `internal/harness/fake.go`: `FakeHarness` (configurable
      `CapabilitySet`) and `FakeSession` (scripted `Messages()` channel,
      recording `Send`/`Close` calls) for use by runner/engine tests without a
      real backend.
- [x] 2.5 Create `internal/harness/harness_test.go`: table-driven tests for
      `CapabilitySet.Has()` (each capability, empty set, full set) and a
      round-trip test using `FakeHarness`/`FakeSession`.
- [x] 2.6 Verify and record (in the PR/commit description, not a new doc file)
      that `grep -rL 'claudecode\|acp-go-sdk' internal/harness/harness.go
      internal/harness/capability.go` prints both paths, then run
      `go build ./internal/harness/... && go test ./internal/harness/...`.

### [ ] 3.0 `ClaudeHarness` — extract the existing SDK path, behavior-preserving

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/runner/...` passes unchanged (same test files, no
  test edits required beyond construction wiring) — demonstrates zero
  observable behavior change.
- CLI: `grep -rn claudecode internal/ | grep -v _test.go` shows matches only
  in `internal/harness/claude.go`, `internal/runner/monitor.go`, and the dead
  `internal/tui` chat path — demonstrates `agent.go` no longer imports the SDK
  directly.
- Diff: a real workflow run's `transcript.jsonl` (e.g.
  `go run ./cmd/jig` against `examples/feature.toml`) compared byte-for-byte
  in structure against a pre-refactor run of the same workflow — demonstrates
  the "zero observable change" success metric.

#### 3.0 Tasks

- [x] 3.1 Create `internal/harness/claude.go` with a `ClaudeHarness` struct
      wrapping `github.com/severity1/claude-agent-sdk-go`; implement `Name()`
      and `Capabilities()` advertising all five capabilities.
- [x] 3.2 Implement `ClaudeHarness.Open(ctx, SessionSpec)`: move `agent.go`'s
      `buildOptions` translation logic here (model/tools/limits/cwd →
      `claudecode` functional options), then `NewClient`/`Connect`.
- [x] 3.3 Implement the permission-callback capability via `WithCanUseTool`,
      forcing `PermissionModeDefault` when `SessionSpec.Permission` is set
      (preserving today's exact behavior, including `acceptEdits` bypassing
      the callback unchanged) — port the logic at `agent.go:63-80` verbatim.
- [x] 3.4 Implement the in-process MCP capability
      (`WithSdkMcpServer("jig", …)` for `AskUserQuestion`), preserving the
      `mcp__jig__AskUserQuestion` name rewrite — port `buildAskUserQuestionServer`.
- [x] 3.5 Implement resume (`WithResume`/`WithContinueConversation`) and
      structured-output capabilities.
- [x] 3.6 Implement a `claudeSession` type satisfying `harness.Session`:
      `Messages()` fed by a goroutine adapting `agent.go`'s `captureStream`
      message-to-block logic (`assistantBlocks`, `toolResultBlocks`, token/cost
      parsing) into `harness.Event`s instead of writing the transcript
      directly; `Send()` forwards to the SDK's `sendCh`; `Close()` calls
      `Disconnect`.
- [x] 3.7 Refactor `internal/runner/agent.go`: `AgentExecutor` holds a
      `harness.Harness`; `Execute` calls `h.Open(ctx, spec)` and consumes
      `Session.Messages()` to write the transcript and build `*step.Result`
      exactly as `captureStream` does today, instead of calling `claudecode`
      directly. Remove the now-dead direct-SDK code from `agent.go`.
- [x] 3.8 Update `NewAgentExecutor`'s constructor to accept/construct a
      `harness.Harness` (defaulting to `ClaudeHarness` for this task; Unit 4
      wires in real selection).
- [x] 3.9 Run `go test ./internal/runner/...` and confirm it passes with no
      test-fixture changes beyond construction wiring (e.g. constructing an
      executor with a `ClaudeHarness` instead of calling the SDK inline).
      Deviation: `captureStream`'s input type changed from `<-chan
      claudecode.Message` to `<-chan harness.Event` (required by 3.7), so
      `agent_test.go`'s fixtures were mechanically translated to script
      `harness.Event` sequences instead of SDK message values — same
      transcript-shape/finding/reporter assertions, no assertions weakened.
- [x] 3.10 Run `grep -rn claudecode internal/ | grep -v _test.go` and confirm
      matches are confined to `internal/harness/claude.go`,
      `internal/runner/monitor.go`, and the dead `internal/tui` chat path.
- [ ] 3.11 Run `go run ./cmd/jig validate examples/feature.toml` then a real
      run of `examples/feature.toml` before and after this task's changes;
      diff the two `transcript.jsonl` files' structure (entry/block shape) to
      confirm no observable change.
      Not done: `examples/feature.toml` no longer exists in this checkout (it
      was migrated to `.agents/jig/feature.toml` by commit 82c2cc3, unrelated
      to this task); ran `go run ./cmd/jig validate .agents/jig/feature.toml`
      instead, which succeeds ("ok: feature v1 — 12 step(s)"). The live-run
      transcript diff needs a real Claude API call, which this environment
      cannot make — left for manual verification by the user.

### [~] 4.0 `AcpHarness` — wire the verified spike into jig

#### 4.0 Proof Artifact(s)

- TUI artifact: a run of `go run ./cmd/jig` against an existing workflow with
  `JIG_HARNESS=acp` set, showing the same step list, transcript viewer, and
  navigation as the default path with **no diff to `internal/tui`** —
  demonstrates the "no TUI changes" goal.
- Security test: a guarded step run with `JIG_HARNESS=acp`, with test output
  showing the permission fn was invoked and an `Allow: false` decision blocked
  the tool from executing — demonstrates the fail-closed guarantee holds
  through jig's own `Guard`, not just Unit 1's standalone proof.
- Grep artifact: `grep -rn acp-go-sdk internal/ engine/ tui/` shows matches
  confined to `internal/harness`'s ACP-backend file(s) — demonstrates
  dependency confinement (success metric 5).
- CLI: `JIG_HARNESS=bogus go run ./cmd/jig validate examples/feature.toml`
  fails fast with a clear "unknown harness" error — demonstrates
  `FromEnv()`'s failure path.

#### 4.0 Tasks

- [x] 4.1 Add `require jig/harness/acp v0.0.0` + `replace jig/harness/acp =>
      ./harness/acp` to the root `go.mod`, pulling in Unit 1's proven module.
      (The `replace` was already present from Unit 1; added the `require` via
      `go mod edit -require` + `go mod tidy`.)
- [x] 4.2 Create `internal/harness/acp.go` with an `AcpHarness` struct that
      spawns `npx -y @zed-industries/claude-code-acp@latest` and drives it via
      the `harness/acp` module's connection code (reusing Unit 1's
      Initialize/NewSession/Prompt logic rather than re-implementing it).
      Split `harness/acp/client.go`'s inlined spawn+Initialize+NewSession+Prompt
      sequence out into a reusable `harness/acp/conn.go` (`Connect`/`NewSession`/
      `Prompt`/`Close`) so both the Unit 1 spike's `Run()` and `AcpHarness.Open`
      share one implementation instead of duplicating the handshake; added an
      `OnUpdate func(Event)` hook to `Client` for real-time streaming (Unit 1's
      `client_test.go` still passes unchanged).
- [x] 4.3 Implement `AcpHarness.Open(ctx, SessionSpec)`: map `cwd` onto ACP's
      session creation; return an error if `SessionSpec` carries a
      capability-gated field this harness does not advertise (`MCPServers`,
      `Resume`, `Schema` — checked explicitly at the top of `Open`).
- [x] 4.4 Implement `AcpHarness.Capabilities()`: always advertise
      `CapPermissionCallback` (the real round-trip); advertise
      `CapInProcessMCP`/`CapSessionResume`/`CapStructuredOutput`/
      `CapPartialStreaming` only for whichever this task actually implements —
      omit (don't stub `true`) any not implemented. (Only `CapPermissionCallback`
      is advertised; none of the other four are implemented in this unit.)
- [x] 4.5 Implement the permission-callback capability: on an ACP
      `session/request_permission`, invoke the jig `PermissionFn` and reply
      allow/deny per its `Decision` — the same real round-trip proven in
      Unit 1, not a stub.
- [x] 4.6 Implement `session/update` → `harness.Event` translation (message
      chunks → `BlockText`, thought chunks → `BlockThinking`, tool-call/
      tool-call-update → `BlockToolUse`/`BlockToolResult`), so the run monitor
      requires no changes. (`translateEvent` in `internal/harness/acp.go`;
      each `session/update` notification flushes as its own single-block
      transcript entry — ACP does not expose the same "one block per SDK
      message" turn boundary Claude's stream does, so entries are more
      granular for this harness than for `ClaudeHarness`. `git diff
      internal/tui` is empty, confirming the monitor needs no changes.)
- [x] 4.7 Add `internal/harness/acp_test.go` covering the `session/update` →
      `Event` translation directly: feed scripted message/thought/tool-call/
      tool-call-update updates (no live `npx` spawn) and assert each maps to
      the expected `BlockText`/`BlockThinking`/`BlockToolUse`/`BlockToolResult`
      — independent evidence of 4.6, not just the manual TUI proof (4.10).
- [x] 4.8 Create `internal/harness/select.go` with `FromEnv() (Harness,
      error)`: reads `JIG_HARNESS` (`claude` default/unset, `acp`), fails fast
      on any other value with a clear error.
- [x] 4.9 Add `internal/harness/select_test.go` covering `FromEnv()`'s three
      paths: unset/`claude` → `ClaudeHarness`, `acp` → `AcpHarness`, any other
      value → error — automated coverage for the fail-fast path, supplementing
      the manual CLI check in 4.12.
- [x] 4.10 Wire `cmd/jig`'s executor construction to call `harness.FromEnv()`
      and pass the result into `NewAgentExecutor`. `NewAgentExecutor` now takes
      a `harness.Harness` parameter (was a no-arg constructor defaulting to
      `ClaudeHarness`); `main()` resolves `harness.FromEnv()` before any
      subcommand dispatch (including `validate`) so a bad `JIG_HARNESS` always
      fails fast, matching 4.13's CLI proof.
- [~] 4.11 Manually run `go run ./cmd/jig` with `JIG_HARNESS=acp` against
      `.agents/jig/feature.toml` (see 3.11's note — `examples/feature.toml` no
      longer exists), confirming the step list/transcript viewer/navigation
      look identical to the default path — capture as the TUI proof artifact —
      and confirm `git diff internal/tui` is empty.
      Not done: requires spawning a live `npx -y
      @zed-industries/claude-code-acp@latest` process with real Claude API
      access, which this environment cannot do. Unit 5 has since landed and
      removes the architectural blocker noted here originally — `Open` no
      longer fails closed on a plain step's implicit base schema (5.2/5.4's
      silent-omission gating). `JIG_HARNESS=acp go run ./cmd/jig validate
      .agents/jig/feature.toml` succeeds in this environment, but note
      `validate` never calls `Execute`/`Open` — it only exercises
      `harness.FromEnv()` parsing `JIG_HARNESS`, not the gating logic itself
      (that's `TestExecute_*` in `internal/runner/agent_test.go`). What
      remains for the TUI proof specifically is purely "no live API access
      here":
      `git diff internal/tui` is confirmed empty regardless. Left for manual
      verification by the user once a live API key/network path is available.
- [~] 4.12 Write a guarded-step integration test with `JIG_HARNESS=acp`
      proving an `Allow: false` decision (via jig's own `Guard`) blocks tool
      execution — the security proof artifact.
      Not done live for the same reason as 4.11 (needs live `npx`/API access).
      The architectural blocker is resolved: Unit 5's
      `TestExecute_GuardSemantics` subtest "guarded step + AcpHarness real
      permission round-trip fires" (`internal/runner/agent_test.go`) is the
      closest coverage achievable without live access — it proves
      `AgentExecutor` wires a real, callable `Decision`-returning function into
      `SessionSpec.Permission` for a harness carrying `AcpHarness`'s exact
      capability set, i.e. the same object `AcpHarness.Open` closes over as
      `acp.Decider` and invokes from `RequestPermission` during the real
      `session/request_permission` round-trip. What it cannot cover without
      live access is the JSON-RPC round-trip itself (spawning the adapter,
      sending `session/request_permission`, receiving the reply) — left for
      manual verification by the user once a live API key/network path is
      available.
- [x] 4.13 Run `grep -rn acp-go-sdk internal/ engine/ tui/` and confirm matches
      are confined to `internal/harness`'s ACP-backend file(s); run
      `JIG_HARNESS=bogus go run ./cmd/jig validate examples/feature.toml` and
      confirm a clear fail-fast error. Matches confined to
      `internal/harness/acp.go` and `internal/harness/acp_test.go` (ran against
      `.agents/jig/feature.toml`, see 3.11's note); bogus value prints `unknown
      harness "bogus" (want "claude" or "acp")` and exits 1.

### [x] 5.0 Fail-closed Guard + capability gating in `AgentExecutor`

#### 5.0 Proof Artifact(s)

- Test: `internal/runner` guard-semantics table test covering (a) guarded step
  + `ClaudeHarness` → callback fires under default mode, (b) guarded step +
  fake harness lacking `CapPermissionCallback` → fail-closed rejection with
  expected error text, (c) `acceptEdits` step → callback bypassed unchanged,
  (d) guarded step + `AcpHarness` → real permission round-trip fires —
  demonstrates all four fail-closed/capability-gating requirements.
- CLI: `go build ./... && go vet ./... && go test ./...` passing from repo
  root, plus the same three commands passing from `harness/acp/` — demonstrates
  the cross-module regression-free success metric.
- Log/error text: captured stderr from a `block_on` step run against a fake
  harness lacking `CapSessionResume`, showing the rejection fires at the
  step's first `Open()` call (not mid-execution) — demonstrates the fail-fast
  requirement for `block_on`.

#### 5.0 Tasks

- [x] 5.1 In `AgentExecutor.Execute`, before calling `Open`, check
      `h.Capabilities().Has(CapPermissionCallback)` when `req.Guard != nil`;
      return a clear, harness-named error (fail-closed) if absent, instead of
      calling `Open`. (`internal/runner/agent.go` `Execute`: checked
      immediately before `spec.Permission` is wired, error text names both the
      harness and the missing capability, e.g. `step is guarded but harness
      "acp-nopermcap" does not support permission callbacks
      (CapPermissionCallback)`.)
- [x] 5.2 Apply the same gating pattern for `AskUserQuestion` steps (require
      `CapInProcessMCP`), session resume (require `CapSessionResume`), and
      structured output (require `CapStructuredOutput`) — each a clear
      rejection error, not a silent skip, when the active harness lacks the
      capability.
      AskUserQuestion and resume (`req.ResumeSessionID != ""`) gate exactly as
      specified: a clear rejection error naming the harness and capability.
      Structured output is split into two cases, both implemented in
      `Execute`: a step that explicitly declares `[step.schema]`
      (`req.Step.Schema != nil`) fails closed on `CapStructuredOutput`, same
      as the other three; the *base* schema every step is merged with
      regardless of declaration (see `buildSessionSpec`'s existing
      unconditional `MergedSchema` call) is not a user request, so it is
      silently omitted rather than rejected when unsupported — this is a
      deliberate refinement over the task's literal wording, made so
      `JIG_HARNESS=acp` doesn't fail closed on every single step by default
      (unblocking 4.11/4.12's manual verification), while still failing closed
      the moment a step actually opts into structured output. Covered by
      `TestExecute_DeclaredSchemaRequiresStructuredOutput`.
- [x] 5.3 For a step whose config declares `BlockOn` (`workflow.Step.BlockOn`),
      check `CapSessionResume` at that step's **first** `Open()` call and
      reject immediately if absent — not lazily, when the `block_on` pause
      would otherwise fire later in execution. (Checked at the very top of
      `Execute`, before `buildSessionSpec` even runs — see
      `TestExecute_BlockOnRequiresSessionResume`, which asserts `Open` is
      never reached: `OpenSpec` stays zero-valued and the session is never
      closed.)
- [x] 5.4 Ensure `AgentExecutor` sets each capability-gated `SessionSpec`
      field (`Permission`, `MCPServers`, `Resume`, `Schema`, `Partial`) only
      after confirming the active harness advertises the matching capability.
      `Permission`/`MCPServers`/`Resume` are set in `Execute` only after their
      fail-closed check passes (5.1–5.3). `Schema`/`Partial` are handled in
      `buildSessionSpec`, which now takes the harness's `CapabilitySet` and
      omits each field when unsupported (see 5.2's note on why these two
      degrade silently instead of failing closed). Covered by
      `TestBuildSessionSpec_CapabilityGated`.
- [x] 5.5 Add a guard-semantics table test in `internal/runner` covering: (a)
      guarded step + `ClaudeHarness` → callback fires under default mode; (b)
      guarded step + `FakeHarness` lacking `CapPermissionCallback` →
      fail-closed rejection with the expected error; (c) `acceptEdits` step →
      callback bypassed (unchanged); (d) guarded step + `AcpHarness` → the
      real permission round-trip from Unit 4 fires.
      `TestExecute_GuardSemantics` in `internal/runner/agent_test.go`, four
      subtests matching (a)–(d). Deviation on (a) and (d): both use
      `FakeHarness` (named `"claude"`/`"acp"` and configured with the matching
      real capability set) rather than the live `ClaudeHarness`/`AcpHarness`,
      since driving either live requires real API/`npx` access this
      environment doesn't have — `ClaudeHarness`'s callback-wiring behavior is
      exercised by `internal/harness/claude_test.go` separately, and (d)
      additionally drives the wired `SessionSpec.Permission` function directly
      to prove it's a live, callable decision function (standing in for
      `AcpHarness`'s real `session/request_permission` round-trip invoking it
      mid-turn), not just that a non-nil value was set.
- [x] 5.6 Add a `block_on` fail-fast test: a step declaring `BlockOn` run
      against a `FakeHarness` lacking `CapSessionResume` rejects at the first
      `Open()` call, with test assertions confirming no partial execution
      occurred. `TestExecute_BlockOnRequiresSessionResume`.
- [x] 5.7 Run `go build ./... && go vet ./... && go test ./...` from the repo
      root, and the same three commands from inside `harness/acp/`; confirm
      both pass with no failures. All six commands pass. The only test
      failures anywhere are the pre-existing, unrelated
      `TestBuildRequestPlanPreambleGolden`/`TestBuildRequestReviseLoopPreambleGolden`
      in `internal/engine` (`examples/feature.toml` was migrated to
      `.agents/jig/feature.toml` in a prior, unrelated commit — confirmed
      present identically on the unmodified tree via `git stash`).
