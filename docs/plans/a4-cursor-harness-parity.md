# Implementation Plan: Cursor harness parity (A4)

**Status:** Planned — goal A4 (P0); implementation has not started.
**Research date:** 2026-09-09; repository baseline `eaea2c3`.
**Risk:** **medium** — production changes belong primarily to the ACP transport
and harness packages, but incorrect callback routing or replay handling can
silently answer a question, duplicate history, or hang recovery.
**Depends on:** Existing `interaction` question types, runner capability gates,
Specs 20/21 recovery and durable session storage, and the native Cursor ACP CLI.
**Complements:** A5 Claude ACP resume and T1 monitor streaming polish; neither is
part of this delivery.

## Summary

Make Cursor workflow steps support session continuation, structured user
questions, and incremental output through jig's existing harness contracts.
The main risk is bridging Cursor's extension requests into the pinned Go ACP SDK
without confusing questions with permissions or replayed history with new output.

## Approach

First establish deterministic Cursor wire fixtures and adapt its extension method
names at the transport boundary. Then implement question translation, safe
session loading, and streaming previews using the existing `acpSession`, runner,
and Gate surfaces. Advertise each capability only alongside its working path and
tests; finish with recovery, headless, and real-Cursor acceptance proofs. Keep
vendor-specific wire types in `harness/acp`, translate them to jig-owned types in
`internal/harness`, and keep the engine and TUI backend-agnostic.

## Current behavior and verified evidence

| Area | Current implementation | Consequence for A4 |
|---|---|---|
| `internal/harness/cursor.go` | Advertises only permission callbacks and structured output; rejects `SessionSpec.Resume`; always calls `NewSession`. | Adding capability bits alone would make runner checks pass without delivering continuation or questions. |
| `harness/acp/conn.go:ConnectCursor` | Spawns `cursor-agent acp`, initializes and authenticates, but does not retain `AgentCapabilities.LoadSession`. | Reuse `Conn.LoadSession`, after storing the negotiated flag. |
| Cursor subprocess lifetime | Uses `os.Stderr` directly and omits `configureProcess`, while shared `Conn.Close` calls process-group termination on Unix. | Stop/Resume needs correct cleanup, and startup errors must not corrupt the TUI. |
| `harness/acp/client.go` | Handles standard updates, permissions, and Claude form elicitation; no Cursor extension dispatcher. | Cursor questions need their own wire adapter, not `newACPElicitor`. |
| `internal/harness/acp.go` | Already turns ACP message/thought chunks into `EventText`/`EventThinking`, with block boundaries. It does not emit `EventTextDelta`. | Durable chunk assembly works, but the runner's live preview path is not fed. |
| `internal/runner/agent.go` | Gates `block_on`, resume, and `AskUserQuestion` by capability; sets `Partial`; persists early session IDs; sends only `EventTextDelta` to `Reporter.Output`. | Preserve these generic paths and prove Cursor reaches them. |
| `internal/interaction/question.go` | Supports stable field/option IDs, single/multi-select, required answers, decline, and cancellation. | No new question schema is needed. |
| Engine / monitor / ops | Existing recovery, pending-question correlation, Gate input, streaming tail, and CLI preflight use generic contracts. | Primarily integration tests, not new Cursor branches. |

The current [Cursor ACP documentation](https://cursor.com/docs/cli/acp) describes
`session/load`, streamed updates, and blocking `cursor/ask_question` requests.
It also documents a distinct plan-approval request. This supersedes the blanket
“no elicitation callback” comment in `cursor.go`; Cursor questions are a vendor
extension, not Claude's ACP form elicitation.

Local primary-source inspection adds details the implementation must preserve:

- The installed `cursor-agent` symlink targets `2026.08.11-e8db854`.
  Its `2996.index.js` advertises `loadSession: true`, implements history replay,
  defines the literal `cursor/ask_question` method, and sends question/option IDs.
- That bundle's question handler can fall back to `session/request_permission`
  if the extension is unimplemented. A generic permission decider can then
  choose an answer accidentally. Supporting the native callback is therefore a
  correctness requirement, not just a different way to render a prompt.
- Cursor's bundled `8096.index.js` sends extension names verbatim. In contrast,
  `acp-go-sdk@v0.13.5/extensions.go` dispatches `ExtensionMethodHandler` only for
  names beginning with `_`. Simply adding that interface to `Client` will not
  receive Cursor's bare method names.
- The pinned SDK's `SendRequest` waits for preceding notifications to finish
  before returning its response. This supplies the load/replay boundary needed
  below; cover it with a wire-level test rather than assuming timing.

The [ACP session setup contract](https://agentclientprotocol.com/protocol/v1/session-setup)
requires checking negotiated load support and replaying history before the load
response. jig must consume that replay without appending it to its existing
transcript. A synchronous load that forwards more than the harness's 32 buffered
events can otherwise deadlock before `Open` returns a stream to consume.

These are code and documentation findings, not a successful live acceptance
run. No paid model turn, authentication change, or CLI upgrade was performed to
write this plan. The inspected version is a candidate test baseline, not a
claimed minimum supported version.

## Scope and invariants

### Required outcomes

1. Cursor `block_on`, Stop/Resume, interrupted-worker recovery, and unfinished
   question parks can continue the same conversation through existing APIs.
2. An enabled native Cursor question reaches the existing Gate, returns exact
   option IDs, and remains correlated across concurrent requests and steps.
3. Assistant preview text reaches `Reporter.Output` before prompt completion;
   final transcript text appears exactly once.
4. Missing support, expired sessions, invalid questions, cancellation, and
   subprocess failures produce explicit bounded outcomes.
5. Claude SDK, Claude ACP, and Codex retain their current capability contracts.

### Explicit boundaries

- Backend/transport remain TOML-only. No new workflow fields, backend-selection
  environment variables, authentication fields, or alternate CLI execution mode.
- Continue using native `cursor-agent acp` and existing login. The official docs
  use the `agent` entrypoint; an executable rename/discovery redesign is separate
  unless acceptance testing proves the current command is unavailable.
- No generic MCP question server, text scraping, or permission-based question
  inference. `AskUserQuestion` remains jig's existing opt-in for installing
  `SessionSpec.Question`, including through `profile = "@interactive"`.
- No new plan-review UI, tool allowlist enforcement project, model/effort
  configuration, cost estimation, or general Cursor extension dashboard.
- No A5 capability change, no T1 transcript-renderer redesign, and no new durable
  transcript or session format. Thinking stays out of the assistant preview.
- Persistence-off remains valid. New optional diagnostic writers must create no
  files when their directory is empty.
- Recovery retains scheduler ownership, worktree validation, immutable workflow
  snapshots, and the distinction between resume and an explicit fresh retry.

## Design decisions

### D1. Adapt Cursor's wire names only at its connection boundary

Keep the pinned Go SDK. Add a Cursor-specific `io.Reader` adapter in proposed
`harness/acp/cursor_wire.go`, installed only around Cursor's stdout before
`NewClientSideConnection` consumes it. Normalize the two supported request names
`cursor/ask_question` and `cursor/create_plan` into private underscore-prefixed
names that the SDK can dispatch. This is adaptation of a verified wire mismatch,
not a second protocol client or a speculative legacy path.

Requirements for the adapter:

- Parse one JSON-RPC envelope at a time with a bounded buffer consistent with
  the SDK's 10 MiB frame bound; preserve IDs and params as `json.RawMessage`.
  Do not decode numeric IDs through `float64`.
- Rewrite only the top-level method on recognized requests with an ID. Preserve
  responses, notifications, cancellation, unrelated methods, and payload strings.
- Keep response IDs untouched; responses require no reverse transformation.
- Surface truncated, malformed, and oversized framing as connection errors;
  never silently drop bytes or read the entire stream into memory.
- Prefer a pull-based reader with no extra goroutine or pipe ownership. If a
  helper goroutine becomes necessary, its shutdown must be covered explicitly.

Add `Client.HandleExtensionMethod` with Cursor callbacks installed only by
`ConnectCursor`. Unknown request methods receive method-not-found through the
SDK; unknown notifications retain the SDK's ignore behavior. Do not dispatch
all `cursor/*` names to a generic allow handler.

### D2. Keep question decoding and application semantics in separate layers

Define typed wire request/response structs and a context-aware question callback
in proposed `harness/acp/cursor_extensions.go`. This nested module must not import
`jig/internal/interaction` or any other root-module package.

In proposed `internal/harness/cursor_question.go`, translate the wire request to
`interaction.QuestionRequest` and invoke `SessionSpec.Question`:

| Cursor data | jig mapping |
|---|---|
| `toolCallId` | `QuestionRequest.ID`; reject an empty ID rather than inventing unstable correlation. |
| Optional title | `QuestionRequest.Message`. |
| Question ID and prompt | `QuestionField.ID` and `Prompt`; preserve array order. |
| Option ID and label | `QuestionOption.Value` and `Label`; labels are display text, never response keys. |
| `allowMultiple` | Single-select when false/absent; multi-select when true. |

Use `Required = true` and `AllowCustom = false`: this delivery supports complete
selection answers or an explicit decline/cancel, with no invented free-text
encoding. Validate requests with `QuestionRequest.Validate`; validate responses
with `QuestionResponse.Validate` and additionally reject duplicate selected IDs.
Unknown optional request fields may be ignored; malformed required fields may
not be coerced into a partial question.

Encode `ActionAccept` as the native answered outcome, with one answer per field
in request order. Encode `ActionDecline` as skipped, and `ActionCancel` or a
cancelled request context as cancelled. Invalid payloads produce invalid-params;
invalid callback responses fail the request without selecting an option.

Always install the transport handler. When the workflow did not enable
questions (`spec.Question == nil`), return a native skipped outcome explaining
that questions are disabled for this step. Returning method-not-found would
activate Cursor's permission fallback. In particular, `@autonomous` must never
silently pick the first option through `PermissionFn`.

Duplicate active request IDs on the same connection are invalid. Different IDs
may wait concurrently; use per-connection state and release entries after
completion/cancellation. Do not hold a registry mutex while calling the user
callback. The existing engine already correlates pending requests by step and
request ID and supports concurrent questions.

### D3. Answer unsupported plan approval explicitly

Handle `cursor/create_plan` solely to return a native rejected outcome explaining
that Cursor plan approval is unsupported in jig; use cancelled during shutdown.
Do not map it to a permission grant or an ordinary question, and do not claim a
new plan-review capability. The inspected Cursor bundle has a local fallback for
unimplemented plan extensions, so an explicit negative result is preferable to
method-not-found for this known blocking method.

Todo, task, and image extensions remain outside the delivery. Do not claim their
effects were performed when declining or ignoring unsupported traffic.

### D4. Negotiate loading and suppress historical replay before normalization

Populate `Conn.SupportsLoadSession` from Cursor's initialize response. In
`CursorHarness.Open`, follow the existing Codex new/load branch:

1. Connect and authenticate using the existing local login.
2. For a new conversation, call `NewSession` with `spec.Cwd`.
3. For `spec.Resume`, call `LoadSession` with exactly that ID and working
   directory. Retain the original ID after successful load.
4. Emit `EventSessionID` before starting the new prompt, then run only
   `spec.Prompt` through the shared session code.

Never fall back from a failed load to `NewSession`. A server without negotiated
load support must not receive a load RPC. Return a clear Cursor/session-load
error and close the process on any open-stage failure.

Implement replay suppression in shared `Conn.LoadSession` / `Client.SessionUpdate`
because it is part of loading an already-recorded jig conversation. During load,
consume historical content before it reaches `Client.events`, `OnUpdate`, or
`acpSession`'s text/tool accumulators. Use synchronized per-connection replay
state; do not temporarily swap an unsynchronized callback. Restore normal
delivery on both success and failure. Retain load response configuration via
the existing `setSessionConfig` path.

The SDK notification barrier establishes when replay has drained. Do not use a
sleep, matching text heuristics, a larger event buffer, or a retained full-history
copy. Test more than 32 replay events, a failed load after partial replay, and
the first fresh chunk immediately after the load response. This shared fix also
needs a Codex load regression test; it does not enable Claude ACP resume.

Historical replay is intentionally not used to reconstruct missing crash-time
transcript text. `transcript.jsonl` remains the record jig actually persisted;
backend context continuation and transcript backfill are different features.

The static `Capabilities()` contract describes jig's implemented adapter support,
not whether every installed Cursor version or saved session is usable. Runtime
load negotiation remains mandatory. Existing recovery handles load failure by
re-parking without repeatedly offering the same failed resume; test that behavior.
Read-only `doctor`/ops preflight must not spawn Cursor just to refine a capability.

### D5. Reuse the streaming preview contract

Add a `partial` setting to `acpSession`, populated from `SessionSpec.Partial` by
the ACP constructors. For an incoming assistant message chunk:

- Continue emitting `EventText` for durable accumulation.
- When partial output was requested, also emit `EventTextDelta` containing that
  chunk for the runner's existing live-tail path.
- Keep `EventAssistantEnd` at meaningful boundaries: before tool activity and
  at prompt completion, not after every token.

Thought chunks remain `EventThinking`; do not mirror them into assistant prose.
Do not mirror locally generated schema-retry/user messages as assistant previews.
`Partial = false` disables previews while preserving final text. The runner's
existing redaction, stream block coalescing, and transcript writer remain the
authority; preview events must not become transcript records.

Initialize the new setting consistently for Claude ACP and Codex so the shared
type has one interpretation. Their already-advertised streaming bits remain
unchanged, with tests covering the newly connected preview behavior. The monitor
continues to use `StepOutput` and refresh from the transcript; A4 does not promise
T1's future per-block markdown finalization UX.

### D6. Make cancellation and process ownership reliable

Call `configureProcess` before starting Cursor so shared `Close` and context
cancellation terminate the correct process group. Reap the process on initialize,
authentication, load, and prompt failures. Reuse the existing synchronized bounded
diagnostic sink for stderr, threading `spec.DiagnosticsDir` into the Cursor
connection API; do not forward arbitrary stderr into the TUI or grow a buffer
without bounds. Update the existing internal API rather than retaining an unused
compatibility wrapper.

Question handlers must observe both the inbound RPC context and the session/open
context, so Stop cancels a callback even while the server is still connected.
Explicit close must cancel pending callbacks before waiting for process exit.
Use cancellation-aware event sends in shared `acpSession` and a single owner for
closing its event channel; a full event channel during shutdown must not leak a
sender or cause a send-on-closed panic. Normal execution retains lossless ordering.

Exercise process cleanup with a fake peer that spawns a child. Keep platform
specific assertions beside the existing Unix/other process helpers. Startup
failure, slow callbacks, and disconnect must finish within test deadlines.

## Ordered implementation tasks

Estimates are focused implementation minutes, excluding live model latency and
upstream investigation. Each row owns one package or file; adjacent test rows
ship with the behavior they cover. New paths are marked **new**. Dependency order
is intentional: do not advertise capabilities before their implementation and
deterministic tests are ready.

| # | Task | Area | Estimate |
|---|---|---|---:|
| 1 | Record inspected Cursor version, SDK mismatch, wire contracts, and acceptance procedure; correct stale “no questions/resume” research claims. | `docs/research/cursor-acp.md` | 25 min |
| 2 | Build a deterministic stdio Cursor peer with scenarios for handshake/auth, streaming, question/plan requests, replay, invalid load, cancellation, and child-process cleanup. No login or model access. | **new** `harness/acp/testdata/cursor-peer/main.go` | 50 min |
| 3 | Implement the bounded Cursor method-name reader from D1. | **new** `harness/acp/cursor_wire.go` | 35 min |
| 4 | Test split reads, multiple frames, exact request-only rewriting, large numeric/string IDs, payload preservation, EOF, malformed/oversized frames, and unchanged cancellation/notification traffic. | **new** `harness/acp/cursor_wire_test.go` | 30 min |
| 5 | Add typed Cursor extension contracts, callback dispatch, disabled-question behavior, and explicit plan rejection. | **new** `harness/acp/cursor_extensions.go` | 40 min |
| 6 | Test dispatch through the real SDK using raw bare Cursor method names; prove permission callbacks are never used to answer the handled question request. | **new** `harness/acp/cursor_extensions_test.go` | 35 min |
| 7 | Add synchronized replay suppression before event capture and forwarding. | `harness/acp/client.go` | 20 min |
| 8 | Test historical message/thought/tool suppression, restored delivery, and concurrent state access. | `harness/acp/client_test.go` | 25 min |
| 9 | Wire Cursor reader/callbacks/diagnostics/process ownership; retain negotiated load support; bracket `LoadSession` replay; keep failed-open cleanup deterministic. | `harness/acp/conn.go` | 50 min |
| 10 | Exercise initialize→authenticate→new/load order, unsupported/expired sessions, replay larger than the harness buffer, immediate fresh output, process children, and empty diagnostics paths. Add shared Codex load coverage. | `harness/acp/conn_test.go` | 50 min |
| 11 | Translate native questions to/from `interaction`, validate IDs/selections, and manage callback lifetime/correlation. | **new** `internal/harness/cursor_question.go` | 35 min |
| 12 | Add table-driven single/multi-question tests, labels differing from IDs, duplicate labels, malformed fields, duplicate active IDs, invalid responses, decline/cancel/nil callback, and concurrent requests. | **new** `internal/harness/cursor_question_test.go` | 35 min |
| 13 | Add partial preview emission and cancellation-aware stream ownership. | `internal/harness/acp.go` | 35 min |
| 14 | Test pre-completion preview timing, partial on/off, exact durable text, thought separation, schema retries, tool boundaries, cancellation under channel pressure, and one terminal result. | `internal/harness/acp_test.go` | 40 min |
| 15 | Initialize Codex's shared partial/session-lifetime settings without changing its capability set. | `internal/harness/codex.go` | 10 min |
| 16 | Verify Codex still resumes and emits previews only when requested. | `internal/harness/codex_test.go` | 20 min |
| 17 | Wire Cursor questions and new/load behavior; advertise the three implemented capabilities and replace stale comments/rejections. | `internal/harness/cursor.go` | 30 min |
| 18 | Test real `CursorHarness.Open` against the fake peer, including early session ID, failed load without new-session fallback, and all five capability bits. | `internal/harness/cursor_test.go` | 40 min |
| 19 | Verify Cursor's real capability selection enables `block_on`, resume, question callback installation, and partial previews; exercise `captureStream` persistence/redaction exactly once and with persistence off. Retain unsupported-harness negative cases. | `internal/runner/agent_test.go` | 35 min |
| 20 | Add fake-peer integration coverage using the real Cursor harness/runner/engine: Stop/Resume, `block_on`, crash reopen, outstanding question reopen, failed resume, and concurrent steps. Use an external test package to avoid an import cycle. | **new** `internal/harness/cursor_workflow_test.go` | 60 min |
| 21 | Verify Cursor-shaped fields work in the generic Gate and streaming tail; selecting duplicate labels returns the correct distinct IDs, multi-select has no custom answer, and resolved/cancelled entries clear. | `internal/tui/monitor/monitor_test.go` | 25 min |
| 22 | Prove an enabled Cursor question produces the existing `gate_question` failure/exit 3 and cancels the waiting callback without stdin reads. | `internal/headless/run_test.go` | 25 min |
| 23 | Add Cursor durable-session resume-preflight coverage and retain missing-session/worktree failures; do not claim CLI resume supports human question parks. | `internal/ops/control_test.go` | 20 min |
| 24 | Add opt-in live acceptance tests, version/evidence capture, deadlines, and prerequisite failure messages. | **new** `internal/harness/cursor_integration_test.go` | 45 min |
| 25 | Document backend-specific question capabilities, Cursor continuation, runtime load rejection, and existing execution-time capability validation. | `docs/workflow-schema.md` | 20 min |
| 26 | Add a self-contained Cursor smoke workflow with separate interactive-question and `block_on` steps; validate both paths without combining the mutually exclusive constructs. | **new** `examples/cursor-parity-smoke.toml` | 20 min |
| 27 | Replace contradictory legacy research with a short historical note linking the verified research and this plan. | `docs/cursor-acp-research.md` | 10 min |
| 28 | Record live results and tested CLI version, including any upstream limitations. | `docs/research/cursor-acp.md` | 20 min |
| 29 | Mark A4 done only after the acceptance checklist passes; leave A5 and T1 open. | `docs/plans/open-goals.md` | 5 min |

Tasks 19–23 should primarily add integration coverage. If they expose an engine,
runner, or TUI defect, fix it in that package with a focused regression test;
do not introduce a backend-name conditional to bypass the generic contract.

## Validation and proof strategy

### Deterministic acceptance matrix

| Behavior | Observable proof |
|---|---|
| True continuation | Fake peer records load with the original ID and cwd; the resumed prompt contains the operator message, not the rebuilt initial task. No new-session RPC occurs. |
| Large history | At least 100 replay updates complete before `Open` returns; no replay text/tool enters a fresh transcript entry, preview, or structured-result parse. |
| Runtime incompatibility | Missing `loadSession` rejects without a load RPC; expired/cwd-invalid load closes the process and does not start a fresh conversation. |
| Early durability | `session.json` is readable before the fake prompt is released; abrupt restart recovers that session through existing manager behavior. |
| Stop/reopen | Stop drains callbacks/processes; reopened stopped, interrupted, and question parks retain coordinates and session identity. Failed resume re-parks with resume disabled. |
| Native question | A bare wire request reaches the Gate; submitted IDs return on the same JSON-RPC ID; permission callback count stays zero for this request. |
| Disabled/invalid question | Disabled questions get skipped; malformed/duplicate IDs or invalid answers never select a default, enter permission fallback, or hang. |
| Concurrency | Two question IDs and two Cursor steps can answer out of order with no cross-delivery; cancellation clears the affected waiters. |
| Plan request | Explicit rejection/cancellation is returned without displaying a review gate or reporting acceptance. |
| Live text | Fake peer withholds prompt completion until a preview is observed; final transcript and output artifact contain the assistant text exactly once. |
| Error/cleanup | Startup, disconnect, load failure, full event buffer, and cancellation during a question finish under deadlines; child processes are reaped. |
| Headless | Enabled question yields `gate_question`/exit 3 and closes the connection; existing CI policies remain unchanged. |
| Persistence off | No transcript/session/diagnostic files are created; question, preview, and cleanup behavior still completes. |

Use channels/barriers to assert ordering, not sleep-based token timing. Tests
must use the pinned SDK's actual connection dispatcher for extension/replay cases;
calling the handler directly cannot detect the bare-method routing problem.

Run focused tests during development, then the repository checks below from the
root. The nested module is separate: root `go test ./...` does not run its tests.

```bash
go test ./internal/harness ./internal/runner ./internal/engine ./internal/headless ./internal/ops ./internal/tui/monitor
go -C harness/acp test ./...
go test ./...
go test -race ./internal/harness ./internal/runner ./internal/engine ./internal/headless ./internal/tui/monitor
go -C harness/acp test -race ./...
go vet ./...
go -C harness/acp vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/feature.toml
go run ./cmd/jig validate examples/cursor-parity-smoke.toml
go run ./cmd/jig run examples/headless-smoke.toml --ci
```

Run `gofmt` on changed Go files and validate any additional changed examples.
Do not upgrade the SDK or Cursor CLI as an incidental test fix.

### Live Cursor release gate

Use proposed `JIG_CURSOR_ACP_INTEGRATION=1` solely as an opt-in test switch,
following existing integration-test conventions. It is not a backend selector.
Normal tests must never start an installed agent or consume model quota.

With a logged-in CLI and a temporary workspace:

1. Record CLI version, executable path, OS, SDK version, and negotiated load
   support. Capture sanitized fixture/proof data, not credentials or unrelated
   local conversation history.
2. Start a session with a unique nonce and request a streamed response. Observe
   preview delivery before completion, then close the process.
3. Open a new process with that session ID and ask for the remembered nonce
   without repeating it. Verify context continuity and no replay duplication.
4. Exercise one single-select and one multi-select question through the native
   request, plus decline and cancellation. Assert the callback actually fired;
   a prose question or a run that never asks is not a passing proof.
5. In jig's TUI, exercise `block_on`, Stop/Resume, and an interrupted run reopened
   through Specs 20/21; include an outstanding question and its durable answer.
6. Run an enabled-question workflow headlessly and verify exit 3 with cleanup.

If the installed version fails a contract, preserve the failing evidence and
identify whether the problem is jig's adapter or upstream behavior. Do not mark
A4 complete, silently downgrade semantics, or assume that passing fake-peer tests
proves live Cursor parity. Streaming/resume may land independently, but a missing
live question proof leaves the corresponding capability/goal incomplete.

## Risks and mitigation

| Risk | Mitigation / release condition |
|---|---|
| Cursor's nonstandard extension names bypass the SDK | Cursor-only bounded reader plus raw-wire dispatch tests; no hand-written JSON-RPC client. |
| Unsupported question fallback becomes an automatic answer | Always handle the known question extension, including when disabled; assert no permission callback is used. |
| Replay deadlocks or pollutes transcript/schema output | Suppress before capture/normalization; test over channel capacity and both successful and failed load. |
| Static capability overstates an old CLI's runtime support | Document tested version, negotiate each load, preserve explicit failure/recovery; no silent fresh-session fallback. |
| Session persists but its workspace/context is unavailable | Preserve existing worktree and session checks; show load failure rather than manufacture a resumed run. |
| Shared ACP changes regress other backends | Exercise Claude ACP/Codex preview behavior and Codex load replay; keep A5's resume capability off. |
| Stop leaves callbacks or subprocess children alive | Context-linked callbacks, cancellation-aware sends, process-group setup, and deadline/race tests. |
| A4 grows into T1 or a new review system | Use existing preview/Gate contracts; explicit unsupported plan response; no new renderer or durable schema. |

## Completion checklist

- [ ] Cursor advertises and honors all three A4 capabilities, while retaining
      its existing permission and structured-output support.
- [ ] Session resume uses the saved ID/cwd and fails closed on unsupported or
      expired sessions; replay is consumed without duplicate content or deadlock.
- [ ] Single/multi-select questions use the native callback and exact stable IDs;
      decline, disabled questions, invalid input, concurrency, and cancellation
      cannot be mistaken for a permission decision or a selected answer.
- [ ] Preview text arrives before completion; final transcript/output appears
      exactly once; thinking and retry prompts do not leak into assistant preview.
- [ ] Stop, crash reopen, unfinished question reopen, ops preflight, headless gate
      failure, and persistence-off pass the deterministic acceptance matrix.
- [ ] Both Go modules pass appropriate tests, race checks, vet, build, formatting,
      workflow validation, and the existing headless smoke proof.
- [ ] Live Cursor acceptance has recorded evidence and a tested CLI version.
- [ ] Docs describe backend-specific limitations accurately, A4 links to this
      plan and is marked done only after proof, and A5/T1 remain separate.
