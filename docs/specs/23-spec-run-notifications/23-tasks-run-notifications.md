# 23-tasks-run-notifications.md

Spec: [`23-spec-run-notifications.md`](./23-spec-run-notifications.md)
Decisions: [`23-grilling-decisions.md`](./23-grilling-decisions.md)
Audit: `23-audit-run-notifications.md` (generated after sub-tasks)

## Task Planning Basis

The parent tasks map one-to-one to the specification's four demoable units. They
keep notification policy in `internal/workflow`, isolate delivery in a focused
service outside the scheduler and workers, expose diagnostics without changing
workflow results, and treat the saved secret-free policy separately from current
operator-owned bindings on reopen.

## Repository Standards Evidence

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | New schema fields require parse/default/validate coverage; persistence-off is first-class; engine stays harness-independent. | none |
| `README.md` | yes | Workflow validation and examples are executable product documentation. | none |
| `CLAUDE.md` | yes | Use focused `internal/` packages, table-driven tests, shared TUI action/style patterns, and file-as-truth semantics. | none |
| `docs/CONVENTIONS.md` | yes | Keep one concern per named file; prefer same-package TUI splits; use named dispatch over repeated large switch arms. | none |
| `docs/TESTING.md` | yes | Run focused/full/race tests, vet, formatting, and example validation. | Its package-status table is stale; existing tests and current code are authoritative. |
| `go.mod` / `mise.toml` | yes | Module path is `jig`; use the pinned Go 1.25 toolchain without an upgrade. | none |
| `CONTRIBUTING.md` | not found | No additional standards. | none |
| `.github/pull_request_template.md` | not found | No additional standards. | none |
| `.github/workflows/` | not found | No additional standards. | none |
| `.pre-commit-config.yaml` / lint config | not found | No additional standards. | none |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/workflow/schema.go` | Add the workflow-level notification policy to the validated workflow model and clone/restore paths. |
| `internal/workflow/notification.go` | New. Notification event, route, profile, presence-aware authoring, resolved-policy, and validation helpers. |
| `internal/workflow/notification_profiles.go` | New. Strict project-local `[[notification]]` profile loading rooted through `RepoRoot` with non-Git fallback. |
| `internal/workflow/load.go` | Resolve the root workflow's notification policy before module expansion and preserve structural decode behavior. |
| `internal/workflow/module.go` | Reject notification policy in imported modules and keep the root resolved policy across expansion. |
| `internal/workflow/notification_test.go` | New. Table-driven policy/profile/default/replacement/validation/module tests for FR-01–FR-03. |
| `internal/workflow/workflow_test.go` | Extend root unknown-key and complete workflow decode assertions where the existing tables are the better fit. |
| `internal/notification/message.go` | New. Internal logical notification and attention-descriptor values passed from normalization to dispatch. |
| `internal/notification/sender.go` | New. Narrow sender/result interface shared by network and desktop adapters. |
| `internal/notification/config.go` | New. Strict operator `notifications.toml` loading, destination validation, enablement, and per-run binding resolution. |
| `internal/notification/config_test.go` | New. Local configuration, secret resolution, HTTPS, duplicate/irrelevant-field, and sanitization cases. |
| `internal/notification/readiness.go` | New. Pure local readiness report used by `notifications check`, with no send capability. |
| `internal/notification/readiness_test.go` | New. Exit-relevant ready/not-ready/disabled behavior and zero-side-effect assertions. |
| `internal/notification/lifecycle.go` | New. Normalize engine lifecycle events into selected notification events and track/recheck unresolved waits by execution epoch. |
| `internal/notification/lifecycle_test.go` | New. Seven attention categories, terminal causes, suppression, deduplication, reset, reopen, and history cases. |
| `internal/notification/payload.go` | New. Build the versioned outbound metadata allowlist and bounded Slack/desktop renderings. |
| `internal/notification/payload_test.go` | New. JSON shape, fixed action text, Unicode/body bounds, control removal, and secret/content exclusion tests. |
| `internal/notification/dispatcher.go` | New. Process-wide queue, coalescing, priority, concurrency, retry scheduling, lifetime, and shutdown ownership. |
| `internal/notification/dispatcher_test.go` | New. Deterministic clock/fake sender tests for overflow, retries, pacing, fairness, shutdown, and cleanup. |
| `internal/notification/http.go` | New. Generic webhook and Slack HTTP senders with TLS, redirect, timeout, response, and retry classification rules. |
| `internal/notification/http_test.go` | New. Local TLS receiver coverage for headers, bodies, status handling, redirects, bearer auth, and bounded reads. |
| `internal/notification/desktop.go` | New. Desktop sender abstraction, command-runner seam, fixed title/body formatting, and availability reporting. |
| `internal/notification/desktop_darwin.go` | New. macOS `/usr/bin/osascript` adapter using fixed source and separate data arguments. |
| `internal/notification/desktop_linux.go` | New. Linux `notify-send` adapter with bounded execution and session/helper readiness checks. |
| `internal/notification/desktop_other.go` | New. Unsupported-platform readiness result without a build failure. |
| `internal/notification/desktop_test.go` | New. Fake-helper argument, injection, timeout, availability, and diagnostic cases. |
| `internal/notification/diagnostics.go` | New. Sanitized aggregate counters, bounded recent-history store, and non-blocking diagnostic subscriptions. |
| `internal/notification/diagnostics_test.go` | New. Aggregation, capacity, control stripping, reason codes, and secret-canary tests. |
| `internal/engine/event.go` | Add semantic run-completion causes and stable internal wait identity/coordinates without outbound sensitive content. |
| `internal/engine/observer.go` | New. Generic non-blocking lifecycle observer registration contract used at the composition boundary. |
| `internal/engine/engine.go` | Register fresh runs, publish journaled control events to the observer, report ingestion rejection, and propagate explicit cancellation causes. |
| `internal/engine/journal.go` | Marshal/decode the added lifecycle fields while retaining zero-value decoding for older records. |
| `internal/engine/resume.go` | Restore frozen notification policy, register a new execution epoch, and expose rehydrated unresolved parks only to live observation. |
| `internal/engine/engine_test.go` | Add completion-cause, observer ordering/non-blocking, cancellation, and scheduler-isolation coverage. |
| `internal/engine/journal_test.go` | Add round-trip and older-record decoding cases for completion causes and wait identity. |
| `internal/engine/resume_test.go` | Add both snapshot restoration paths, policy integrity, changed profile/binding, restored summary, and repeated-reopen tests. |
| `internal/headless/types.go` | Carry the notification diagnostic stream/store through the shared headless options without changing result envelopes. |
| `internal/headless/run.go` | Assign semantic timeout/interruption/policy-rejection causes and drain notification diagnostics independently of engine events. |
| `internal/headless/output.go` | Render sanitized notification diagnostics to stderr only, preserving stdout JSON/JSONL. |
| `internal/headless/run_test.go` | Add headless gate/timeout/interruption notification and stdout/exit-code regression coverage. |
| `internal/ops/control.go` | Ensure CLI resume/reset execution uses the same registered runtime and current bindings as fresh runs. |
| `cmd/jig/notifications.go` | New. Thin `jig notifications check` argument parsing, rendering, and exit-code mapping. |
| `cmd/jig/notifications_test.go` | New. Command usage, readiness output, no-send, and exit-code tests. |
| `cmd/jig/wire.go` | Replace manager-only construction with the process runtime that owns manager, dispatcher, diagnostics, and ordered shutdown. |
| `cmd/jig/wire_test.go` | Verify one shared dispatcher, injectable persistence-off bindings, and producers-before-drain shutdown order. |
| `cmd/jig/main.go` | Dispatch the `notifications` command and close the TUI runtime on every exit path. |
| `cmd/jig/run.go` | Use the process runtime for fresh headless execution and perform bounded notification drain after run settlement. |
| `cmd/jig/control.go` | Use and close the same process runtime for active resume/reset execution. |
| `cmd/jig/ops.go` | Add notifications-check help and usage text. |
| `internal/tui/root.go` | Accept the diagnostics store/stream and retain global diagnostics before a Monitor exists. |
| `internal/tui/root_cmds.go` | Add non-blocking diagnostic wait commands alongside engine-event waits. |
| `internal/tui/root_update.go` | Route diagnostics to the matching live Monitor without changing Gate focus or historical replay behavior. |
| `internal/tui/monitor/monitor_model.go` | Hold the bounded notification-diagnostic list and overlay/action state. |
| `internal/tui/monitor/monitor_notifications.go` | New. Notification diagnostics list/action rendering and bounded model updates. |
| `internal/tui/monitor/monitor_update.go` | Handle diagnostics action, navigation, close, and incoming diagnostic messages. |
| `internal/tui/monitor/monitor_view.go` | Composite the inspectable diagnostics surface without changing transcript or Gate layout state. |
| `internal/tui/monitor/keys.go` | Register the named `Notification diagnostics` command-palette action. |
| `internal/tui/monitor/msgs.go` | Add typed diagnostic messages that do not enter the engine event or transcript streams. |
| `internal/tui/monitor/monitor_test.go` | Add action discovery, bounded-list rendering, focus preservation, history, and transcript-isolation tests. |
| `internal/tui/shared/styles.go` | Add any diagnostic status styles through the existing semantic theme singleton only. |
| `.agents/jig/notification-profiles/operator.toml` | New. Valid reusable policy example with no destination URLs or credentials. |
| `examples/notifications-profiled.toml` | New. Valid workflow selecting the reusable profile. |
| `examples/notifications-override.toml` | New. Valid workflow demonstrating complete routes-list replacement. |
| `docs/workflow-schema.md` | Document author policy/profile syntax, defaults, validation, replacement, and metadata disclosure. |
| `docs/headless.md` | Document headless event suppression/classification, stderr diagnostics, shutdown, and exit invariants. |
| `docs/operations.md` | Document operator bindings, secrets, readiness checks, desktop prerequisites, troubleshooting, and reopen behavior. |
| `README.md` | Add the notification-check command to the concise command/documentation surface. |
| `docs/plans/open-goals.md` | Mark A24 complete and link this spec only after implementation proofs pass. |

### Notes

- `internal/engine` defines only a generic, non-blocking lifecycle observer and
  semantic completion/wait identity. It must not import `internal/notification`
  or own HTTP, retries, desktop helpers, local config, or diagnostic rendering.
- `cmd/jig/wire.go` owns one process runtime. It registers fresh/reopened runs
  before their first live event, stops run producers before closing admission,
  and then gives the shared dispatcher one total five-second drain budget.
- Local destination bindings are resolved once per fresh start or active reopen.
  They are never serialized, live-reloaded, or added to agent/command child
  environments by this feature.
- Automated network proofs use `httptest.NewTLSServer` and synthetic named
  secrets. Actual Slack sends require separate explicit authorization and are
  not part of automated or implementation proof collection.
- Use table-driven tests, injected fake clocks/command runners/senders, and
  model-driven Bubble Tea tests. Format only changed Go files; run focused
  tests before the full repository gates.

## Tasks

### [~] 1.0 Configure reusable notification policy and inspect local readiness

Add the strict root-workflow notification schema, project-local reusable
profiles, presence-aware replacement/default resolution, operator-owned local
bindings, and the side-effect-free `jig notifications check` command. The
completed slice lets an author validate inline/profiled policy and lets an
operator inspect requested aliases, enablement, and local readiness without
sending any notification.

**Covers:** FR-01, FR-02, FR-03, FR-04, FR-05, and FR-06.

#### 1.0 Proof Artifact(s)

- Test (FR-01–FR-03): `go test ./internal/workflow -run 'Test(Notification|LoadNotificationProfiles)' -count=1` passes table cases for inline and profiled policy, root-only placement, module expansion, profile lookup/duplicates/unknown fields, event and alias validation, full-list replacement, explicit empty lists, route subsets, and duplicate-route deduplication.
- Test (FR-04–FR-05): `go test ./internal/notification -run 'Test(LocalConfig|ResolveBindings|Readiness)' -count=1` passes table cases for disabled-by-default behavior, one destination per type, named-secret resolution, intentionally disabled destinations, missing config, malformed config, missing secrets, invalid HTTPS URLs, and sanitized diagnostics.
- Test (FR-06): `go test ./cmd/jig -run TestNotificationsCheck -count=1` passes exit-code and output cases for ready, not-ready, disabled/empty, and usage-error states while a spy sender records zero network or desktop calls.
- CLI (FR-01–FR-06): `23-proofs/1.0/policy-check.txt` and `23-proofs/1.0/shared-profile.txt` capture `jig validate` and `jig notifications check` for valid inline policy and two workflows sharing one profile, with one workflow replacing the complete routes list and a receiver-spy count of zero.

#### 1.0 Tasks

- [x] 1.1 Add a failing table-driven characterization suite in `internal/workflow/notification_test.go` for the complete root `[notification]` syntax, documented defaults, explicit empty lists, inline/profile replacement, route filtering/deduplication, and the valid two-workflow shared-profile fixture.
- [x] 1.2 Define notification events, routes, raw presence-aware fields, profiles, and a defensive fully resolved policy accessor in `internal/workflow/notification.go`; represent omitted lists distinctly from explicitly empty lists until resolution is complete.
- [x] 1.3 Implement strict loading of `<project-root>/.agents/jig/notification-profiles/*.toml` in `notification_profiles.go`: derive the project root with `RepoRoot` from the root workflow directory, fall back to that directory outside Git, sort files deterministically, reject unreadable/malformed files, unknown keys, missing/non-`@` IDs, duplicates across files, profile references, invalid events, and invalid aliases.
- [x] 1.4 Resolve root workflow policy exactly once in `load.go` with precedence explicit workflow field → selected profile field → default (`attention_required`, `run_failed`; empty routes); make whole-list replacement and explicit clearing observable, validate route-event subsets, and collapse repeated/overlapping destination routes without multiplying delivery.
- [x] 1.5 Reject `[notification]` under `[defaults]`, `[[step]]`, and every imported `[module]` file using strict decode/module validation; retain only the root policy while applying it to all expanded static steps and dynamic fan-out children. Add nested-module and forbidden-placement tests.
- [x] 1.6 Add `internal/notification/config.go` and table-driven tests for strict `<resolved-jig-root>/notifications.toml` parsing: global/per-destination enablement defaults false, unique aliases, at most one destination of each `desktop|slack|webhook` type, relevant fields per type, named URL/bearer secret references, HTTPS URL validation, and operator filtering that can only remove policy events/routes.
- [x] 1.7 Resolve secrets only for requested and enabled network destinations through an injected resolver using the existing `JIG_SECRET_<NORMALIZED_NAME>` convention; retain resolved URLs/tokens only in per-execution in-memory bindings and prove disabled destinations neither resolve nor warn.
- [x] 1.8 Make missing local config a silent disabled result; convert malformed/unreadable config, missing secrets, invalid local destinations, and unavailable desktop prerequisites into sanitized per-invocation readiness failures without invalidating an otherwise valid workflow. Strip controls and never include resolved values or raw external errors.
- [x] 1.9 Implement `readiness.go` as a pure inspection path that reports resolved policy, requested aliases, global/destination enablement, and locally detectable readiness while exposing no sender method and making no HTTP/helper calls.
- [x] 1.10 Add `cmd/jig/notifications.go` for `jig notifications check <workflow.toml> [--root PATH]`; return 0 for valid ready or explicitly disabled/empty policy, 1 for workflow/profile/config/readiness failure, and 2 for usage. State explicitly that readiness is neither verified delivery nor guaranteed desktop visibility.
- [x] 1.11 Register `notifications` in `main.go` and all help/unknown-command output, then add command tests using injected stdout/stderr, environment lookup, filesystem, and spy senders to prove output, exit codes, and zero side effects.
- [x] 1.12 Add the secret-free shared profile and two valid example workflows, run the focused workflow/notification/CLI suites, and capture the sanitized Task 1 CLI evidence without contacting any receiver or displaying a desktop notification.

### [ ] 2.0 Deliver bounded metadata notifications from normalized live-run state

Introduce the backend-independent lifecycle normalization seam and shared
process-owned notification service, then wire it through both TUI and headless
execution. Deliver the fixed metadata allowlist to generic HTTPS webhooks,
Slack incoming webhooks, and native macOS/Linux desktop helpers while deriving
attention only from unresolved state and terminal events from semantic run
settlement.

**Covers:** FR-07, FR-08, FR-09, FR-10, FR-11, and FR-12.

#### 2.0 Proof Artifact(s)

- Test (FR-07–FR-08): `go test ./internal/notification -run 'TestLifecycle|TestAttention|TestTerminal' -count=1` passes all seven attention categories and both terminal outcomes, plus suppression cases for validation alone, auto-handled/rejected headless gates, retry attempts, ordinary completion, deliberate cancellation, historical replay, and pre-run errors.
- Test (FR-09–FR-10): `go test ./internal/notification -run 'TestPayload|TestWebhook|TestSlack' -count=1` passes against local TLS receivers, proving the versioned fixed JSON allowlist, stable logical IDs, bearer handling, redirect refusal, TLS verification, request/body bounds, literal untrusted-name rendering, and Slack/generic success classification without real credentials.
- Integration test (FR-12): `go test ./internal/headless ./internal/tui -run TestRunNotifications -count=1` passes command-only success/failure and attention fixtures through both execution surfaces, proving equal event selection and that Monitor rendering, agent workers, and backend/transport selection do not own delivery.
- Capture (FR-09–FR-10, FR-12): sanitized `23-proofs/2.0/live-receiver.jsonl` and `23-proofs/2.0/tui-headless-parity.txt` show matching headless/TUI metadata and contain no prompts, errors, transcripts, tool arguments, diffs, paths, or secret canaries.
- Platform proof (FR-11): `23-proofs/2.0/desktop-macos.png` and `23-proofs/2.0/desktop-linux.png`, with OS/helper versions in `23-proofs/2.0/desktop-platforms.md`, show an actual native jig notification; fake-sender tests separately cover missing-helper/session and submission-error diagnostics without changing run outcomes.

#### 2.0 Tasks

- [ ] 2.1 Start with failing lifecycle and payload tests that enumerate all seven attention kinds, terminal success/failure, every FR-08 suppression, duplicate observations/routes, reset generations, sequential questions, and fresh versus reopened execution epochs.
- [ ] 2.2 Extend `engine.RunFinished` with a journal-safe semantic completion cause and extend attention request events with fixed internal wait identity/coordinates where existing fields (`RoundID`, question request ID) are insufficient. Populate every emission path and retain safe zero-value decoding for older journal records.
- [ ] 2.3 Replace undifferentiated cancellation with an explicit cause path: ordinary engine failures and failed steps remain failures; headless timeout and policy rejection settle as notifiable run failures; deliberate operator/TUI/process interruption settles as cancellation and is suppressed. Update engine, headless, ops, and TUI callers plus result/exit regression tests.
- [ ] 2.4 Add a generic lifecycle-observer interface in `internal/engine/observer.go` with run registration (`run ID`, immutable workflow/policy, snapshot callback, fresh/reopen flag, execution epoch), non-blocking publication after journal persistence, explicit enqueue-rejection reporting, and producer-stopped notification. Register before `Start` emits or `Resume` rehydrates any live park.
- [ ] 2.5 Thread the observer through scheduler control-event emission and recovery batches only; keep high-volume transcript liveness unchanged, never publish replay-created historical events, and test that a blocked/rejecting observer cannot delay scheduler or worker completion.
- [ ] 2.6 Implement `lifecycle.go` to normalize only selected `attention_required`, `run_failed`, and `run_succeeded` events. Track unresolved review, block-on input, from-user prompt, agent question, recovery, integration-conflict, and final-merge waits; resolve them from matching response/status/terminal transitions and recheck the registered run snapshot before delivery.
- [ ] 2.7 Enforce suppression in the normalizer: no validation-only, intermediate-attempt, ordinary-step-completion, automatically handled/rejected headless attention, deliberate cancellation/interruption, historical viewing, or pre-start/load failure notification. Keep recovery eligible and require explicit policy selection for success.
- [ ] 2.8 Define the generic webhook payload and fixed action vocabulary in `payload.go`; generate stable retry IDs, UTC timestamps, bounded/sorted attention descriptors and counts, omit attention fields for terminal messages, omit step ID for final merge, and allow only schema version, notification ID, event, timestamp, workflow, run ID, and approved attention metadata.
- [ ] 2.9 Add Unicode-safe/control-safe renderers that cap workflow/step identifiers at 128 characters and Slack/desktop bodies at 2,000 characters, preserve valid JSON under the 16 KiB request cap, escape Slack/desktop markup and mentions, and never format from raw errors, prompts, paths, outputs, or templates.
- [ ] 2.10 Implement the generic webhook and Slack HTTP senders with injected `http.Client`: HTTPS with nonempty host/no userinfo/no fragment, verified TLS, redirects rejected, three-second per-attempt cap, `application/json`, optional generic bearer header only, 4 KiB response reads, generic 2xx success, and Slack `200` plus literal `ok` success.
- [ ] 2.11 Implement platform desktop senders behind a common command-runner seam: fixed jig title and separate bounded data arguments to `/usr/bin/osascript` on macOS, bounded `notify-send` on Linux, explicit unsupported/unavailable readiness elsewhere, and no shell interpolation, click action, terminal bell, or SSH forwarding.
- [ ] 2.12 Replace manager-only construction in `cmd/jig/wire.go` with a process runtime that creates one dispatcher/diagnostic store, installs one generic engine observer, loads bindings once per registered fresh/reopened run, and exposes the same manager/service to TUI, `run`, `resume`, and applied `reset` paths.
- [ ] 2.13 Add command-only integration fixtures and local TLS captures proving matching headless/TUI event selection and fixed payloads, failure isolation, no backend/harness dependency, Slack classification, bearer handling, and no prohibited fields or secret canaries.
- [ ] 2.14 Capture actual synthetic desktop notifications on supported macOS and Linux hosts, record OS/helper versions and readiness output, save the required screenshots under `23-proofs/2.0/`, and keep fake-helper tests as the automated evidence for missing services and submission errors.

### [ ] 3.0 Bound concurrent delivery and expose sanitized failure diagnostics

Make the process-wide dispatcher resilient under fan-out, receiver outages,
rate limits, queue pressure, and shutdown. Add coalescing and stale-wait
filtering, observable loss at every lossy boundary, bounded retries and
scheduling, terminal-failure priority, bounded headless/TUI diagnostics, and a
fully functional persistence-off path that performs no accidental file I/O.

**Covers:** FR-13, FR-14, FR-15, FR-16, FR-17, and FR-18.

#### 3.0 Proof Artifact(s)

- Test (FR-13, FR-15): `go test ./internal/notification -run 'Test(Queue|Delivery|Retry|RateLimit|HealthyDestination)' -count=1` passes deterministic fault-injection cases for queue saturation, terminal-failure eviction/drop, timeouts, transient transport errors, 408/429/5xx retries, permanent 4xx/certificate/URL refusal, `Retry-After`, lifetime expiry, Slack pacing, and a healthy destination progressing while another backs off.
- Test (FR-14): `go test ./internal/notification -run TestAttentionCoalescing -count=1` passes a 100-request burst fixture proving the fixed window, per-run/destination grouping, deterministic ten-descriptor payload bound, resolved-wait removal before send/retry, distinct round/generation/epoch identities, and overlapping-route deduplication.
- Test (FR-16, FR-18): `go test ./internal/notification ./internal/engine -run 'Test(Shutdown|ConcurrentRuns|PersistenceOff|NotificationObserver)' -race -count=1` proves the total five-second shutdown ceiling, cancellation/discard accounting, dispatcher reuse across runs, no goroutine leaks, observable ingestion rejection, live in-memory delivery without a run directory, and eventual release of per-run deduplication state.
- Test (FR-17): `go test ./internal/headless ./internal/tui/monitor -run TestNotificationDiagnostics -count=1` proves diagnostics are bounded and aggregated, headless diagnostics stay on stderr without changing JSON/JSONL stdout or exit codes, and the Monitor's `Notification diagnostics` action is inspectable without stealing Gate focus or altering transcripts.
- Capture (FR-13–FR-18): sanitized `23-proofs/3.0/fault-injection.txt`, `23-proofs/3.0/race.txt`, `23-proofs/3.0/headless-stderr.txt`, `23-proofs/3.0/monitor-diagnostics.png`, and `23-proofs/3.0/secret-scan.txt` demonstrate visible loss reason codes and aggregate counts while finding neither secret canaries nor resolved destination URLs.

#### 3.0 Tasks

- [ ] 3.1 Build dispatcher tests first around injected fake clock/timer, sender, wait resolver, diagnostic sink, and deterministic notification-ID generator; avoid real sleeps, network access, desktop helpers, and global mutable test state.
- [ ] 3.2 Implement a single-owner dispatcher loop with capacity for 256 destination-expanded pending deliveries, at most four active sends globally and one per resolved destination binding, and an enqueue API that never waits for delivery or retry work.
- [ ] 3.3 Account for every rejected ingestion/destination enqueue. Under pressure, let an incoming `run_failed` evict the oldest pending non-failure; when only failures remain, drop the new failure. Emit bounded reason-coded diagnostics for both eviction and rejection without changing any engine result or exit status.
- [ ] 3.4 Coalesce attention for 500 milliseconds from first arrival by run, execution epoch, and resolved destination; sort by step ID/kind, emit at most ten descriptors with total/omitted counts, deduplicate overlapping routes, and keep later arrivals from extending the window indefinitely.
- [ ] 3.5 Before every initial send and retry, call the injected wait resolver to remove resolved or superseded identities; discard empty summaries. Prove new review rounds/questions, reset generations, and reopen epochs stay distinct while duplicate observation/rendering does not enqueue twice.
- [ ] 3.6 Implement scheduled retries that remain counted in the pending bound but occupy no active sender while delayed: three attempts total, one/two-second default delays, retry only transient transport failures plus 408/429/5xx, honor valid `Retry-After`, enforce one-second Slack start pacing, and abandon delays beyond the 30-second lifetime or remaining shutdown budget.
- [ ] 3.7 Cap each network/helper attempt at the lesser of three seconds, remaining message lifetime, and shutdown budget; keep a stalled destination from blocking healthy destinations and document that retries may duplicate an accepted external message.
- [ ] 3.8 Implement ordered shutdown in the process runtime: stop/cancel and await all run producers first, close dispatcher admission second, drain pending/in-flight/retries for one total five-second window, then cancel/discard the remainder and emit aggregate diagnostics. Completing one run in a live TUI must not close the shared dispatcher.
- [ ] 3.9 Implement `diagnostics.go` with fixed safe fields (alias, optional run ID, outcome/reason code, attempt, aggregate count), a last-100-entry ring, identical-failure aggregation, control stripping, non-blocking subscribers, and a startup/global history visible before a Monitor exists. Never retain raw response bodies/errors, URLs, headers, or secret values.
- [ ] 3.10 Wire headless diagnostics to stderr regardless of quiet mode while leaving stdout text/JSON/JSONL envelopes and all existing exit codes byte-for-byte compatible when delivery is disabled or failing; add focused writer/supervision regression tests.
- [ ] 3.11 Add a command-palette-only `Notification diagnostics` Monitor action and bounded themed list/overlay with navigation and close behavior. Feed it through typed TUI messages, preserve current Gate focus/queue and transcript state, and ensure an arriving failure never opens or focuses the surface automatically.
- [ ] 3.12 Support persistence-off through caller-injected policy/bindings with no config-root path joins or writes; an empty root plus no injection resolves disabled defaults. Release run policy, binding, wait, terminal-deduplication, and epoch state once no pending/in-flight work references it.
- [ ] 3.13 Run deterministic burst/fault/shutdown suites and `-race` across notification, engine, headless, and Monitor packages; capture sanitized queue-loss, retry, stderr, and TUI evidence under `23-proofs/3.0/` and scan it for secret canaries/resolved URLs.

### [ ] 4.0 Reopen from frozen policy and document the complete operator flow

Persist and integrity-check the fully resolved secret-free policy in immutable
run snapshots, restore it through both snapshot paths, bind it to current local
operator configuration for each new execution epoch, and emit exactly one
filtered summary of still-pending attention on active reopen. Complete the
workflow schema, headless/CLI help, examples, troubleshooting guidance, and A24
status only after the implementation and evidence are complete.

**Covers:** FR-19, FR-20, and FR-21.

#### 4.0 Proof Artifact(s)

- Test (FR-19): `go test ./internal/engine -run 'TestWorkflowSnapshot.*Notification|TestResume.*NotificationPolicy' -count=1` passes both `RestoreExpanded` and source-decoding restoration paths, checksum-corruption rejection, expanded-module coverage, old-snapshot disabled behavior, and a fixture where the source profile is changed/deleted after run start but does not alter restored policy.
- Test (FR-19–FR-20): `go test ./internal/notification ./internal/engine ./internal/tui -run 'Test(Reopen|History).*Notification' -count=1` proves current bindings/secrets apply on active reopen, one filtered restored summary is emitted per run/destination, repeated rendering/replay does not duplicate it, new waits still notify, repeated execution epochs remain distinct, and historical inspection creates no producer.
- Test/security scan (FR-19): `go test ./internal/notification ./internal/engine -run TestNotificationSecretsNeverPersist -count=1` passes, and `23-proofs/4.0/secret-scan.txt` records zero synthetic URL/token canary matches across workflow snapshots, journals, transcripts, diagnostics, and committed proofs.
- CLI/docs (FR-21): `for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow" || exit 1; done` succeeds; `23-proofs/4.0/docs-check.txt` captures the loop plus `jig notifications check` and help output matching `docs/workflow-schema.md` and `docs/headless.md`, while the documentation covers profile lookup/replacement, operator enablement, secrets, route filters, desktop prerequisites, disclosed metadata, best-effort loss/duplicates, bounded shutdown, and reopen behavior.
- Repository gates (FR-01–FR-21): `gofmt -w <each-changed-go-file>` followed by `gofmt -l <the-same-files>` printing nothing, `go vet ./...`, `go test ./... -count=1`, `go test ./... -race -count=1`, and `go build ./cmd/jig` pass; `docs/plans/open-goals.md` marks A24 complete with a link only after these proofs exist.

#### 4.0 Tasks

- [ ] 4.1 Add failing snapshot/reopen tests for resolved inline/profile policy, expanded modules, both restore paths, absent old policy, policy corruption, changed/deleted source profiles, changed operator bindings, mixed unresolved parks, repeated reopen epochs, and history-only inspection.
- [ ] 4.2 Extend `workflowSnapshot` with the canonical fully resolved notification policy plus its own SHA-256 integrity field; marshal no profile source, operator binding, enablement, URL, token, secret value, or notification runtime state. Verify the digest before restore and treat absent policy as disabled.
- [ ] 4.3 Thread the verified resolved policy through `RestoreExpanded` and the source-decoding `DecodeLocked` path without reloading current notification profiles or accepting policy from imported module sources. Keep existing root TOML/module digests and graph restoration semantics intact.
- [ ] 4.4 On each fresh start or active reopen, create a distinct execution epoch and resolve current operator config/bindings/secrets once for that epoch. Permit changed aliases to point at new destinations while keeping the event/routes policy frozen; never live-watch config or serialize the bound values.
- [ ] 4.5 Register the reopen observer before `rehydrateParks`, seed it from the restored unresolved review/input/prompt/question/recovery/integration/final-merge state, and coalesce exactly one initial summary per eligible run/destination. Avoid separate replay alerts, historical terminal reissue, and duplicate restored/live alerts for the same wait.
- [ ] 4.6 Prove subsequent genuinely new waits notify normally, repeated active reopens use distinct epochs, old snapshots do not acquire policy from present-day profiles, and `ReplayJournal`/Runs hydration creates no notification producer or delivery side effect.
- [ ] 4.7 Add synthetic URL/token canaries to snapshot, resume, diagnostics, transcript, journal, and proof tests; scan all persisted files and `23-proofs/` captures to prove only the secret-free resolved policy and fixed metadata are retained.
- [ ] 4.8 Update `docs/workflow-schema.md` with strict root/profile syntax, lookup/default/replacement semantics, routes, validation, snapshot behavior, and outbound metadata; keep all workflow examples valid and free of credentials.
- [ ] 4.9 Update `docs/headless.md`, `docs/operations.md`, CLI help, and `README.md` with enablement/bindings, named secret normalization, destination filters, desktop prerequisites, no-send readiness semantics, stderr diagnostics, best-effort loss/duplicates, shutdown, disclosure, and reopen/current-binding behavior. Keep B2 terminal bell explicitly separate.
- [ ] 4.10 Produce sanitized Task 4 reopen/check/setup captures, run `gofmt -w` on each changed Go file and verify `gofmt -l` prints nothing for the same set, then run focused tests, `go vet ./...`, `go test ./... -count=1`, `go test ./... -race -count=1`, `go build ./cmd/jig`, and the full `.agents/jig/*.toml` validation loop.
- [ ] 4.11 Only after every implementation proof and repository gate passes, mark A24 complete in `docs/plans/open-goals.md` with a link to this spec and create the final Task 4 proof index; do not claim actual Slack delivery without separately authorized evidence.
