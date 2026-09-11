# Implementation Plan: Restore Tier-2 security monitors (A6)

**Status:** Implemented — deterministic acceptance suite passed 2026-09-09;
live vendor smoke not run.
**Goal:** A6 in [open-goals.md](open-goals.md).
**Risk:** high — restoring the fleet activates external classifier calls and
touches run lifecycle, security controls, and recovery cost accounting.
**Dependencies:** existing Spec 10 sentinel types, transcript store, recovery
gates, and Specs 20/21 unfinished-run reopen.
**Related:** A16 owns migration of the monitor adapter onto the harness seam.

## Summary

Ship the three security classifiers inside the binary and repair the production
path from transcript activity to persisted findings, for new and reopened runs.
The main risk is activating a previously dormant subsystem whose runtime wiring,
tool restrictions, and configuration behavior do not yet match its documentation.

## Approach

Replace working-directory discovery with an explicit embedded roster in
`internal/runner`, passed to `sentinel.Supervisor` as parsed definitions rather
than file paths. First make classifier dispatch testable and explicitly tools-off;
then repair supervisor bounds, failure reporting, and restart accounting. Finally,
wire a run-owned signal channel before execution starts in both `Manager.Start`
and `Manager.Resume`, using each dispatched step's resolved security settings.
Prove the entire path with fake executors and classifier clients, including a
manager created outside the jig checkout, then separately exercise each real ACP
harness against an offline protocol fixture through `AgentExecutor` and the
engine to persisted findings. Keep the existing findings schema, human recovery
policy, and direct Claude SDK adapter.

## Findings from the current checkout

Code is the baseline; historical Spec 10 proofs describe an earlier tree.

| Evidence | Current behavior | Consequence for A6 |
|---|---|---|
| `cmd/jig/wire.go`: `newManager`, `discoverMonitors` | Reads `examples/agents/monitors` relative to process CWD; read errors return an empty roster. | Missing assets silently disable Tier-2. Restoring examples alone would still fail for an installed binary used in another repository. |
| Git history: `9ab1c24`, `9a768cb` | The first commit added the three prompts; the second removed them. | Recover their detector intent, but review the prompts against today's actual inputs. |
| `internal/engine/engine.go`: `Manager.Start`, `Manager.Subscribe` | Start copies `m.subs`, starts the scheduler, and only then subscribes the fleet. Subscribe explicitly does not attach to existing runs. | The current run never sends transcript signals to its own supervisor, even with a nonempty roster. |
| Same startup block | Installs a manager-wide subscriber and drains only its live channel; no run-ID filter. | Later runs can send events to a prior run's subscriber. Moving Subscribe earlier alone does not fix ownership or cleanup. |
| `internal/engine/resume.go`: `Manager.Resume` | Reconstructs the scheduler without starting a supervisor. | Reopened agent execution has no Tier-2 coverage. |
| `internal/runner/monitor.go`: `MonitorAdapter.Dispatch` | Reads/parses the prompt on every dispatch; uses default permission mode and a one-turn limit, but no explicit empty tool set. | Comments saying “tools-off” are not enforcement. Prompt text and transcript data currently share one query. |
| `internal/sentinel/supervisor.go`: `flushStep` | Discards dispatch errors with `continue`; ignores cost returned alongside an error. | Missing Claude, invalid output, and billed failures can remain invisible. |
| Supervisor configuration | Uses fixed batch/debounce constants, dispatches serially, and receives no resolved per-step security policy. | Documented tuning and per-step opt-outs are not all honored. |
| `boundWindow`, `renderWindow` | Reads all new entries before trimming; allows one oversized entry; successive windows have no overlap. | Restoring dispatch can send oversized inputs and miss temporal evidence across flushes. |
| `internal/sentinel/prefilter.go` | Stuck-loop and exfil prefilters exist but are not invoked by the supervisor. | Historical prompts incorrectly assume a supplied prefilter signal. |
| `internal/runner/agent.go`, `sentinel.RedactJSON` | Existing redaction targets selected write-tool inputs. | Do not assume every tool result or assistant block is already secret-free before sending it to a classifier. |
| Existing tests | Supervisor tests inject `StepSignal` directly; engine escalation tests inject findings. | Neither proves production manager → transcript → classifier → finding wiring. |

Baseline verification on 2026-09-09: `go test ./internal/sentinel
./internal/runner ./cmd/jig` passed. No live classifier calls were made. The pinned
SDK inspected locally is `github.com/severity1/claude-agent-sdk-go v0.6.22`.

## Completion evidence

The restored path is covered by named offline tests that keep vendor billing out
of CI:

- `TestBuiltinMonitorsPortableRoster`, `TestMonitorAdapterIsolationAndLifecycle`,
  and `TestMonitorAdapterStrictVerdictsPreserveCost` cover packaging, the direct
  SDK boundary, tools/settings/skills isolation, lifecycle, and strict verdicts.
- `TestRunOwnedSecurityObservesPersistedTranscript`,
  `TestRunOwnedSecurityDoesNotCrossRunsOrAccumulateSubscribers`,
  `TestResumeSecurityWaitsForNewActivityAndCarriesSpend`, and
  `TestForEachChildUsesResolvedTier2Policy` cover production signaling,
  ownership, reopen, accounting, and runtime child policy.
- `TestSupervisorRetainsBoundedOverlapAcrossBatches`,
  `TestSupervisorResumeCarriesBudgetAndDeduplication`, and the other A6
  supervisor/state tests cover bounds, redaction, failure visibility, and
  conservative budget degradation.
- `TestTier2ObservesEveryACPHarness`, `TestACPResumePathsRejectOrLoadExplicitly`,
  `TestTier2ACPLiveStopResumeUsesBackendCapability`,
  `TestTier2PersistedReopenAcrossEveryACPHarness`, and
  `TestTier2PolicyParityAcrossEveryACPHarness` pass named `claude-acp`,
  `cursor-acp`, and `codex-acp` rows through the real harness selectors and ACP
  connection paths. `TestTier2MixedACPHarnessRunKeepsStepWindowsIsolated`
  covers the parallel mixed-backend case and per-step opt-out.

The optional live Claude/classifier and ACP worker smoke procedure was not run;
fixture results do not establish semantic classifier accuracy or live-service
telemetry parity.

## Scope and decisions

### 1. Ship a fixed, embedded roster

Add these source assets:

- `internal/runner/monitors/prompt-injection.md`
- `internal/runner/monitors/stuck-loop.md`
- `internal/runner/monitors/exfil-pattern.md`

Add `internal/runner/monitors.go` with explicit `go:embed` paths and an explicit,
stable roster. Use `workflow.ParseAgentFileContent` once during construction;
require unique expected IDs, a nonempty prompt, and the intended model. Missing
files must fail the build; malformed content must return a construction error,
never a partial or empty successful roster.

Introduce `sentinel.MonitorSpec` containing model and prompt, and replace
`MonitorDef.File` with that spec. Change `MonitorDispatcher.Dispatch` to receive
the spec and the bounded input. Keep `MonitorDef.Monitor` as the stable finding
name. These are internal APIs: remove the file-path dispatch path and all
`discoverMonitors` code in the same change. Do not create temporary prompt files,
search paths, environment overrides, or duplicate example copies.

Expose a runner constructor such as `BuiltinMonitors() ([]sentinel.MonitorDef,
error)`. Change `newManager` to return an error and update its TUI, run, and control
command callers. A corrupt built-in roster is a broken build and must fail
visibly before starting a run. Missing runtime Claude/auth is a different case:
report a degraded monitor at runtime while the observed workflow continues.

### 2. Restore the three detector contracts

All prompts must treat the entire transcript as untrusted data, including text
that imitates delimiters, role names, security policy, or the classifier's own
output. Place classifier instructions in the SDK system prompt and supply the
window as data in the query. This separation improves the boundary; it is not a
claim that prompt injection is solved.

| Monitor | Required evidence | False-positive controls / severity |
|---|---|---|
| `prompt-injection` | Instruction redirection inside tool-result content followed by an assistant action consistent with that redirection. | Quoted attacks, legitimate instructions being analyzed, and ignored payloads are not successful hijacks. High for observed redirection; critical only with concrete dangerous follow-through. |
| `stuck-loop` | Repeated identical tool calls or consecutive errors plus lack of corrective progress. | Intentional polling and adapting retries are benign. Usually medium; do not invent budget/turn data absent from the input. |
| `exfil-pattern` | A sensitive read followed by an outbound action whose destination or payload indicates unauthorized disclosure. | Synthetic data and ordinary authenticated API use need context. A recognizable host alone is not proof of safety. Confirmed blocked attempts are low; demonstrated attempted disclosure can be critical. |

Use the exact existing output contract: `flagged`, `severity`, `detail`, with
all fields required and no extras. An unflagged verdict uses `severity = "low"`
and empty detail. Require brief evidence references, never copied credentials.
Assign stable entry sequence/block references in rendered input so findings can
point to something the operator can inspect.

Wire `StuckLoopPrefilter` as an actual dispatch gate: false skips that classifier;
true supplies explicit trusted prefilter context. Do not gate exfil dispatch on
the existing raw-secret matcher: redacted transcripts would create false
negatives. For A6, run exfil and injection on each eligible nonempty window;
defer a redaction-aware exfil prefilter. Do not claim allowlist blocking occurred
unless the window contains actual deny evidence. Pass the step's effective
outbound allowlist as context, without equating an allowed host with safe data use.

### 3. Enforce a bounded classifier invocation

Keep `MonitorAdapter` on the direct SDK path. The worker backend continues to be
selected by workflow TOML; a Cursor or Codex worker can still have a Claude SDK
monitor. Document the separate Claude prerequisite and billing. Do not inherit
worker credentials, worker environment secrets, or worker tools into the monitor.

Observing every implemented ACP worker is explicitly in A6: Claude ACP
(`AcpHarness`), native Cursor ACP (`CursorHarness`), and Codex ACP
(`CodexHarness`). Completion requires the per-harness matrix below. Shared
`acpSession` normalization is useful implementation reuse, but is not evidence
that all three connection, session, and event paths are correctly wired.

Extract option construction and inject a narrow client factory for tests.
Production behavior must include:

- Explicitly empty SDK tools, no jig question or other MCP tools, default
  permission mode, and a deny callback as defense in depth. Verify SDK argument
  construction distinguishes an empty tool list from omitted defaults.
- Explicitly empty setting sources and skills, so project/user customization
  does not turn a classifier into a normal coding session. Verify the exact
  empty-slice behavior in the pinned SDK.
- System prompt from the embedded definition, the existing structured-output
  schema, a single-turn limit, and no monitor transcript/artifact writes by jig.
  Describe this last property as jig persistence-off; do not promise the vendor
  CLI writes no session data without separately verifying that behavior.
- A finite dispatch timeout (initial proposal: 30 seconds), cancellation-aware
  message draining, and deterministic disconnect on success or error.
- A send channel kept alive through the query/result exchange and closed once
  on teardown. Test the lifecycle instead of copying the current close-before-
  query sequence without verification.
- Strict local verdict validation: reject missing/extra fields, wrong types,
  unknown severity, malformed JSON, and streams with no result. Preserve known
  cost even when the verdict is invalid or the SDK reports an error.

Redact detected secret patterns from every rendered input field and from returned
detail before transmission/persistence. Add a general text redaction helper using
the existing detectors; `RedactJSON` alone is insufficient for arbitrary text.
Keep redaction markers meaningful to the exfil classifier. This is pattern-based
protection, not a promise to recognize every secret or PII value.

### 4. Own monitoring within one run

Add `internal/engine/security.go` for run monitor setup and policy helpers. Prefer
a dedicated buffered `chan sentinel.StepSignal` on the scheduler over registering
an extra manager-wide subscriber. Create the channel/supervisor before launching
the scheduler; extend the existing worker reporter's `StepMessage` handling to
offer a signal non-blockingly after the transcript append has been reported.
Keep normal TUI/headless event fan-out unchanged.

Compute eligibility from the dispatched `workflow.Step`, on the scheduler
goroutine, then capture immutable policy in the reporter. This automatically
covers runtime foreach children and avoids concurrent reads of `s.states` or the
fan-out maps. Eligible means an agent step with effective `enabled` and
`tier2_enabled` true, a registered roster, and a nonempty run directory.
Respect explicit step overrides over defaults, including a step that explicitly
enables Tier-2 when its default is false. Command/check/review-only workflows
must not start paid classifier calls.

Include generation, iteration, and attempt in the internal signal/window
coordinates so reset/retry boundaries cannot create a false temporal sequence.
Reject wrong-run signals defensively. On lifecycle boundaries discard stale
pending work; a delayed verdict from an older execution may be recorded but must
not park the replacement execution. Preserve current best-effort recovery for a
critical finding against the still-current, nonterminal step.

Use the same setup in `Manager.Resume`, after successful reconstruction and before
workers can execute. Monitoring context ends on cancellation or scheduler exit,
including normal completion. Cleanup must close the findings writer, disconnect
clients, and finish before releasing run ownership; it must not wait for a full
classifier timeout. Do not close a signal channel while reporters can still send.

Tier-2 remains retrospective: a short run may finish before a verdict arrives.
Do not introduce a final synchronous classification gate to guarantee a verdict.

### 5. Make supervisor behavior match the restored service

Introduce a supervisor options struct carrying effective batch size, debounce,
budget, and dispatch timeout. Honor the existing TOML batch/debounce settings,
with today's 5 signals / 500 ms as defaults. Keep serial classification for A6:
one invocation is always within any valid `concurrency_cap`. Document the cap as
an upper bound, not a promise of parallel execution; a worker pool is deferred.

Read a bounded recent tail using the transcript reader rather than materializing
all entries since the cursor. Keep at most 20 entries and 32,000 bytes of final
rendered transcript input, including labels and truncation markers. Clip a single
oversized block at a valid UTF-8 boundary. Track the last observed sequence to
skip unchanged input, but include bounded prior context in a new window so a
read/injection just before the previous flush can be paired with the next action.
Never join different execution coordinates. Evidence outside that tail can be
missed; record this limitation rather than claiming a full-history scan.

On a non-cancellation dispatch failure, emit one deduplicated low-severity,
`action = "observed"` health finding for that classifier and run, using stable
names such as `monitor-unavailable/<id>`. Disable that classifier for the
remaining process lifetime so missing auth does not produce paid retries on every
window. Continue the other classifiers and Tier-1. Use bounded, redacted error
categories; do not persist raw SDK errors that may include transcript content.
Cancellation during normal teardown is not an outage finding.

Report transcript-read and required state/sink failures through the existing
security event surface with a clear degradation reason. Never report “healthy”
when the fleet could not observe input. If findings cannot be persisted, the
operator event must explicitly say persistence failed. Preserve the existing
finding schema and budget-exhausted finding name; no new gate or TUI layout.

### 6. Preserve budget meaning across reopen

Reattaching on `Manager.Resume` must not silently give the same run a fresh fleet
budget. Add a versioned `security-monitor-state.json` under the run directory,
owned by the supervisor, plus a datastore path helper. Store cumulative known
spend, budget-degraded state, and whether a paid invocation is in flight; never
store prompts, credentials, or transcript content in this file.

Atomically persist the in-flight marker before a paid call and the updated spend
and cleared marker after its result. Count known cost on both success and error.
If a finite-budget run reopens with an unresolved in-flight call, corrupt state,
or missing legacy accounting, its remaining budget is unknown: persist a single
health finding and keep Tier-2 disabled for that run. The workflow and Tier-1
continue. Do not invent an exact residual budget. Unlimited-budget (`0`) runs
may resume classification with an explicit unknown-cost health note.

On an ordinary successful reopen, resume with saved spend/degradation and seed
the finding fingerprint set from existing findings. Initialize observation at
the current transcript end, retaining only a bounded context tail for the first
new activity. Do not reclassify all historical output or immediately re-escalate
an old finding when the operator merely reopens a parked run. Resetting a step
does not reset the run's fleet spend.

Before dispatch, stop if a positive budget is exhausted. After dispatch, account
and degrade before the next call. The existing ceiling is based on reported
completed-call cost and may overshoot by one serial invocation; do not advertise
an exact prepaid billing cap. Unreported cost after a launched call makes finite
budget accounting uncertain and must disable further dispatch visibly.

## Ordered implementation tasks

Each task is scoped to one package (or an individual documentation file).
Estimates are focused agent wall-clock minutes with a warm toolchain, excluding
live-service delays. Tests are separate tasks so the wiring proof cannot be
mistaken for successful compilation.

| # | Task | Area | Estimate |
|---|---|---|---|
| 1 | Replace file-based monitor definitions with `MonitorSpec`; define supervisor options and execution coordinates; migrate package-local stubs. | `internal/sentinel` | 25 min |
| 2 | Recover/revise the three detector prompts; add the explicit embedded roster and construction validation. | `internal/runner` | 35 min |
| 3 | Add roster tests for exact IDs, nonempty parsed content, model choice, malformed definitions, and operation from an unrelated CWD. | `internal/runner/monitors_test.go` (new) | 20 min |
| 4 | Refactor `MonitorAdapter` to consume specs, inject the client factory, enforce classifier options/lifecycle, and validate verdicts and costs. | `internal/runner` | 45 min |
| 5 | Add option, client-lifecycle, timeout, cancellation, malformed-verdict, and error-with-cost tests. | `internal/runner/monitor_test.go` (new) | 35 min |
| 6 | Add general text redaction and tests covering all rendered field types, returned detail, and retained redaction markers. | `internal/sentinel` | 30 min |
| 7 | Add the monitor-state path helper with an explicit empty-run-dir no-op test. | `internal/datastore` | 15 min |
| 8 | Add atomic versioned spend/in-flight state and restore behavior, including finite-budget uncertainty policy. | `internal/sentinel/monitor_state.go` (new) | 35 min |
| 9 | Test normal restore, missing/corrupt state, interrupted calls, write failures, budget carryover, and persistence-off. | `internal/sentinel/monitor_state_test.go` (new) | 30 min |
| 10 | Update supervisor windows, overlap, prefilter context, batch/debounce, health findings, and accounting integration. | `internal/sentinel` | 60 min |
| 11 | Extend supervisor tests for bounded reads/rendering, temporal boundaries, deduplication, configured timing, failure isolation, and budget/error paths. | `internal/sentinel/supervisor_test.go` | 45 min |
| 12 | Add run-owned security setup and reporter signal routing; enforce resolved step policy and current-execution escalation; wire Start and Resume and cleanup. | `internal/engine` | 60 min |
| 13 | Add end-to-end engine tests with a transcript-writing fake executor and recording dispatcher, including lifecycle and reopen cases below. | `internal/engine/security_test.go` (new) | 60 min |
| 14 | Add test-only ACP subprocess fixtures for each actual harness connection path, including initialize/authentication, session creation/load, tool updates, cancellation, and launch-argument assertions. | `internal/harness` | 45 min |
| 15 | Add backend-specific raw ACP event fixtures and normalization assertions for tool inputs/results, incremental updates, failures, partial metadata, and loaded-session replay. | `internal/harness` | 30 min |
| 16 | Add the per-harness Tier-2 integration matrix below using real harness selection, `AgentExecutor`, engine lifecycle, the embedded roster, and recording classifiers. Include policy, mixed-backend, reopen, and teardown cases. | `internal/harness/security_integration_test.go` (new, external `harness_test` package) | 75 min |
| 17 | Register the embedded roster unconditionally; remove discovery; propagate construction errors through `newManager`, main, run, and control helpers. | `cmd/jig` | 25 min |
| 18 | Add production-wiring tests outside the source checkout, including malformed roster failure, no Claude launch on construction, and all manager entry points. | `cmd/jig/wire_test.go` (new), package `cmd/jig` | 30 min |
| 19 | Rewrite roster location, SDK prerequisites, bounded retrospective coverage, health findings, classifier isolation, reopen behavior, and verified ACP worker coverage. | `docs/security-monitoring.md` | 25 min |
| 20 | Correct security config descriptions: batch trigger versus window cap, effective per-step overrides, serial cap semantics, zero-budget meaning. | `docs/workflow-schema.md` | 15 min |
| 21 | Add an implementation-status note correcting stale roster/persistent-client claims and the “zero budget disables Tier-2” statement; preserve historical context. | `docs/adr/0009-agent-security-monitoring.md` | 10 min |
| 22 | Record per-harness completion evidence and mark A6 done only after every required matrix row and the other acceptance checks pass. | `docs/plans/open-goals.md` | 5 min |

Estimated effort: approximately 14 hours plus final repository verification and
any SDK integration investigation. Deliver in three reviewable slices: embedded
roster/adapter (1–5), supervisor behavior/state (6–11), production lifecycle and
documentation (12–22). The internal interface migration may temporarily require
working across slices; keep each committed slice compiling. Do not mark A6 fixed
after the asset slice alone.

## Acceptance and verification

### Deterministic checks required for completion

1. **Portable roster:** construct production monitors after changing into a temp
   directory with no `examples/` tree. Assert exactly the three stable IDs and
   parsed prompts. A same-named local markdown file cannot override a built-in.
2. **Production path:** a fake agent executor appends actual transcript entries
   via the requested transcript path and calls `Reporter.Message`. All applicable
   recording dispatchers run and findings reach `findings.jsonl` plus subscribed
   security events. Do not inject `StepSignal` directly for this test.
3. **Ownership:** two managers/runs using the same step ID never share windows,
   findings, or budgets. Repeated starts do not accumulate private manager
   subscribers. Immediate first-message activity is observed.
4. **Policy:** table-test security off, Tier-2 off, per-step opt-outs and explicit
   opt-ins, persistence-off, command-only execution, and foreach child policy.
   All disabled cases must make zero classifier calls and create no monitor
   session artifacts.
5. **Recovery:** a persisted unfinished run reopens and new agent transcript
   activity is classified. Prior history alone causes no call. Spend and budget
   exhaustion survive reopen/reset; uncertain finite-budget accounting produces
   visible degradation without stopping the workflow.
6. **Isolation:** explicit empty tools/settings/skills, deny callback, system/data
   separation, timeout cancellation, and cleanup are asserted through the injected
   SDK client. Test both result success and failure with reported cost.
7. **Evidence:** a read/injection and subsequent action split across two batches
   remain visible within the bounded tail. No cross-generation/iteration/attempt
   pairing. Huge tool output remains within the final render bound. Secret
   fixtures do not survive in captured classifier input or persisted detail.
8. **Failure visibility:** one broken classifier produces one bounded health
   finding while other classifiers still work. Invalid structured output cannot
   become an unflagged success. State/write failures do not silently renew a
   budget or claim durable evidence was saved.
9. **Escalation:** a critical current-execution finding uses existing recovery;
   duplicate or stale-execution findings do not park a replacement attempt. An
   already-terminal step stays terminal.
10. **Teardown:** cancellation and normal completion stop the fleet, close its
    sink, and release run ownership without late writers or leaked goroutines.
    A deliberately slow dispatcher does not delay ordinary step scheduling.
11. **Every ACP harness:** complete every required row of the matrix below;
    neither a generic fake executor nor one shared `acpSession.onEvent` unit test
    substitutes for the individual harness integration paths.

Use channels/barriers and bounded test deadlines to control asynchronous tests.
Prompt fixtures and stub verdicts prove packaging and plumbing, not real LLM
classification accuracy. Keep normal tests offline and free of vendor billing.

### Required per-harness verification matrix

Use explicit TOML backend/transport pairs in the fixtures and pass them through
the normal workflow loader and `harness.For` selection. Run the real
`AcpHarness.Open`, `CursorHarness.Open`, or `CodexHarness.Open` and shared
`AgentExecutor` capture path. Replace only external ACP processes with scripted
protocol peers and the classifier's external service with recording verdict
stubs. For example, test-scoped executable fixtures on `PATH` can stand in for
`npx` and `cursor-agent`; assert the arguments select the expected adapter. Keep
all such controls test-only: no workflow flags or process-wide backend override.

Place the integrated tests in an external `harness_test` package so they can
import engine and runner without adding those dependencies to production harness
code. Reuse the existing event assertions in `internal/harness/acp_test.go` and
connection fixtures in the nested `harness/acp` module where practical. Preserve
the repository's confinement of ACP protocol implementation to these packages.

| Worker selection | Real path exercised | Required lifecycle coverage |
|---|---|---|
| `backend = "claude"`, `transport = "acp"` | `AcpHarness` → Zed Claude ACP connection → `acpSession` → runner capture | Fresh execution; `Manager.Resume` followed by recovery retry in a fresh session. While `CapSessionResume` is absent, assert session continuation is rejected with the existing actionable capability error; never silently downgrade a requested continuation to retry. |
| `backend = "cursor"`, `transport = "acp"` | `CursorHarness` → native Cursor connection/authentication → `acpSession` → runner capture | Fresh execution; live Stop/Resume; persisted-run reopen and supported session load; replayed historical notifications during load must not produce new findings or spend. |
| `backend = "codex"`, `transport = "acp"` | `CodexHarness` → Codex ACP adapter connection/configuration → `acpSession` → runner capture | Fresh execution; live Stop/Resume; persisted-run reopen and supported session load; replayed historical notifications must not produce new findings or spend. |

Capabilities above reflect the checkout inspected for this amendment. Check the
actual advertised capabilities and negotiated load-session support when
implementing. If a harness gains resume support before A6 lands, require the
positive continuation cases as well. If an advertised capability is refused by
the peer, assert explicit failure and zero phantom monitor activity. Adding a
missing harness capability remains outside A6; observing supported new execution
after recovery is required for all three.

For **each** ACP row, require named subtests and evidence for:

1. **Complete observation chain:** raw peer notifications produce durable
   transcript entries; `Reporter.Message` causes the run-owned supervisor to
   read them; captured classifier input contains the expected evidence; verdicts
   persist with the correct run/step/monitor IDs and reach `SecurityFinding`
   subscribers. Keep the fixture worker alive behind a barrier until the finding
   is observed, so the test does not depend on final-turn timing. Do not directly
   inject harness events, transcript entries, signals, or findings in this test.
2. **All detectors:** use separate synthetic injection-followed-by-action,
   repeated-tool/error-loop, and sensitive-read-followed-by-outbound fixtures.
   Assert injection/exfil classifiers receive the ordered evidence and stuck-loop
   classification runs only after its prefilter fires. Use the embedded roster
   with recording dispatchers so arbitrary test monitor definitions cannot hide
   a missing shipped classifier.
3. **Backend event shapes:** exercise tool starts, incremental input/title
   updates, terminal result content and failure status, fragmented assistant
   text, and content-only results. Include each backend's representative raw
   notification shapes even where translation shares code. Preserve tool identity,
   ordering, and observable arguments/output through the final monitor input;
   repeated update notifications must not become invented repeated tool calls.
   Title-only/absent-input cases must remain explicitly unknown, not fabricated
   commands or successful exfiltration evidence. If necessary evidence is not
   available from a backend, document that detector limitation in the row's proof.
4. **Secret handling and bounds:** known synthetic secrets in arguments, result
   content, and classifier detail are redacted at the monitor boundary; oversized
   outputs obey the same bound for every backend. The redacted evidence remains
   usable by the exfil classifier without claiming unavailable original values.
5. **Policy parity:** default-on, security-off, Tier-2-off, per-step override,
   persistence-off, and foreach-child cases have the expected dispatch counts.
   Tier-1 may be disabled while Tier-2 remains enabled; this must still observe
   the transcript. Conversely, Tier-1 permission findings alone cannot satisfy a
   Tier-2 assertion. Disabled cases must make zero classifier calls.
6. **Lifecycle and isolation:** exercise the row's supported recovery paths,
   execution-coordinate changes, current versus stale critical verdicts,
   classifier failure, and cancellation. Verify budget carryover and deduplication
   on reopen, no late findings into a replacement execution, and closed worker
   and classifier connections after teardown. Prove classification works with
   partial previews both enabled and disabled: durable events are its input.

Also run one mixed-backend workflow with all three ACP selections and
`max_parallel > 1`, unique evidence markers, and a per-step Tier-2 opt-out.
Assert enabled steps are observed, the opted-out step is not, and windows,
findings, and execution coordinates never cross steps. All enabled workers share
one run fleet budget. Assert worker selection is still TOML-driven and monitor
dispatch still uses the separate Claude SDK adapter. A missing classifier runtime
must produce the specified health finding while Cursor/Codex workers continue.
This is a wiring/isolation check, not a claim to resolve A7's live concurrency
reliability issues.

Record test names and pass/fail results per row. A skipped offline row is
incomplete verification and blocks marking A6 done. Keep the existing Claude SDK
adapter tests as the non-ACP regression baseline; A16 does not defer any of this
worker-observation coverage.

### Repository verification

```bash
go test ./internal/sentinel ./internal/harness ./internal/runner ./internal/engine ./internal/datastore ./cmd/jig
go test -race ./internal/sentinel ./internal/harness ./internal/runner ./internal/engine ./cmd/jig
go test ./...
go vet ./...
go build -o /tmp/jig-a6 ./cmd/jig
go run ./cmd/jig validate .agents/jig/feature.toml
go run ./cmd/jig validate examples/headless-smoke.toml
```

Format changed Go files with `gofmt`; validate any other workflow fixtures added
by implementation. Use a generated temporary output path instead of overwriting
an existing `/tmp/jig-a6` when executing the illustrative build command above.
Run `go test ./...` and `go test -race ./...` separately with `harness/acp` as
the working directory when adding or changing its fixtures/connection code;
the repository-root commands do not traverse that nested Go module.

Add an opt-in, bounded live adapter smoke test or documented developer procedure
using the operator's existing Claude login and synthetic transcript fixtures.
Check all three prompt/verdict contracts and one adversarial “invoke a tool”
fixture, recording actual cost and runtime versions. This is release confidence
for SDK behavior, not a deterministic CI assertion about model judgment. Report
explicitly if it was not run; do not claim stub tests establish semantic accuracy.

Extend the opt-in smoke procedure to each actual ACP worker backend, using its
existing operator login and the separate Claude classifier login. Record the
worker/adapter versions, backend/transport, observed tool evidence, monitor call
and finding, and supported reopen result per backend. Use synthetic local data
with a finite fleet budget and timeout. Distinguish a live row that was not run
from a passing offline fixture row; neither fixture coverage nor one successful
backend establishes live telemetry parity for the other two.

## Boundaries and follow-up work

- **A16 remains separate:** migrating the classifier adapter to another harness
  is deferred; verifying Tier-2 observation through every existing ACP worker
  harness is required here. No new workflow backend fields, API keys, or
  environment-based backend selection.
- No user-installable monitor/plugin discovery, scope-drift detector, persistent
  client pool, classifier concurrency pool, or full-history security index.
- No new Security panel design or doctor command expansion. Reuse findings and
  security events; missing Claude becomes visible through the restored path.
- No exact crash-proof accounting of vendor charges when a process dies between
  billing and local persistence. The conservative uncertainty policy is deliberate.
- No promise of comprehensive secret/PII detection or synchronous prevention by
  retrospective classifiers. Broad transcript redaction remediation remains
  separate; A6 protects the classifier input/output boundary it activates.
- Historical Spec 10 proofs remain historical. Update current operational docs
  and add new evidence instead of rewriting old “Verified” reports.

The design choices above are resolved recommendations for implementation. If the
pinned SDK cannot enforce an empty tool surface or cannot return a verdict under
the proposed one-turn lifecycle, resolve that adapter constraint before enabling
the production fleet; do not substitute prompt-only tool restrictions.
