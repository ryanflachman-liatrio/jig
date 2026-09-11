# 23-spec-run-notifications.md

## Introduction/Overview

Add opt-in notifications for A24 in `docs/plans/open-goals.md` so operators can leave jig's Monitor and still learn when a run needs human attention or finishes unsuccessfully. Workflow authors select reusable notification policies; operators enable and bind desktop, Slack, and generic webhook destinations. Delivery runs asynchronously within the owning jig process and never determines workflow success.

Source of intent: [answered questions](23-questions-1-run-notifications.md) and [accepted grilling decisions](23-grilling-decisions.md). The user accepted all twelve recommendations and confirmed the consolidated design by asking to continue. The decisions and this specification supersede contradictory selections in the original questions file.

## Goals

- Deliver actionable attention and terminal failure notifications from both TUI and headless execution; offer successful completion as an explicit event selection.
- Reuse project-local notification policies across workflows while keeping destination credentials and enablement operator-owned.
- Keep notification work independent of agent workers, scheduler progress, workflow results, and headless exit codes.
- Bound queues, requests, retries, message size, and shutdown while reporting delivery loss without exposing secrets.
- Preserve run policy across reopen while honoring current operator destination bindings and enablement.

## User Stories

- **As a local operator**, I want desktop notifications for unresolved human waits so that I can work in another application without leaving a run unattended.
- **As an operator running jig remotely**, I want Slack or webhook messages identifying the affected run so that I know when to return to it.
- **As a workflow author**, I want a reusable notification profile with clear overrides so that related workflows share policy without duplicating configuration.
- **As an operator**, I want control over destinations and secrets so that shared workflow files cannot select arbitrary outbound addresses or enable delivery on my behalf.
- **As an operator reopening a run**, I want a current summary of pending attention so that I can resume work without replaying historical alerts.

## Demoable Units of Work

### Unit 1: Configure reusable policy and inspect local readiness

**Purpose:** Let authors express a validated policy and let operators inspect its effective local destinations without sending messages.

**Functional Requirements:**

- **FR-01:** The system shall accept one optional root-level `[notification]` table in workflow TOML. Without it, notification policy is disabled. It shall support one optional `profile`, an `events` list, and a `routes` list. It shall reject notification tables on individual steps, defaults, and imported subworkflow modules; the root workflow policy covers all expanded steps, including module steps and fan-out children.
- **FR-02:** The system shall load project-local profiles from `<project-root>/.agents/jig/notification-profiles/*.toml`, using `workflow.RepoRoot` anchored at the root workflow's directory, with its existing fallback outside Git. Files shall contain `[[notification]]` entries with unique `@id` identifiers and the same `events` and `routes` fields as workflow policy. Profiles shall not reference other profiles. Unknown referenced profiles, duplicate IDs, unknown fields, and invalid event/alias syntax shall fail workflow validation. No global profile search or built-in profile is required.
- **FR-03:** Resolution shall be explicit workflow field → selected profile field → documented default. An explicit list replaces the complete inherited list; an explicit empty list clears it. Default `events` shall be `['attention_required', 'run_failed']`, and default `routes` shall be empty. Each route shall have a `destination` alias and optional `events`; omitted route events use the resolved policy events, explicit empty route events disable that route. Explicit route events must be a subset of resolved policy events. Duplicate routes shall not multiply delivery to an alias.
- **FR-04:** The system shall load operator bindings from `notifications.toml` under the existing resolved `.jig` root. Global and per-destination `enabled` values shall default to false. Destination types shall be `desktop`, `slack`, or `webhook`, with at most one configured destination of each type in this version. Network destinations shall obtain their complete URL through a named secret reference; generic webhooks may also reference a bearer-token secret. Workflow/profile TOML shall contain neither destination bindings nor credentials. Operator configuration shall only restrict delivery, never add events absent from workflow policy.
- **FR-05:** The system shall keep static validation independent of local notification readiness: `jig validate` checks workflow/profile syntax and semantics without requiring destination bindings or secret values. During execution, missing bindings, missing secrets, invalid local destinations, or unavailable desktop delivery shall disable the affected delivery and produce sanitized diagnostics; malformed/unreadable local configuration shall disable notifications for that invocation without preventing a valid workflow from running. A missing local config means disabled, without a warning. Intentionally disabled destinations shall not require secrets or generate missing-secret warnings.
- **FR-06:** The user shall be able to run `jig notifications check <workflow.toml>` to inspect resolved policy, destination aliases, enablement, and locally detectable readiness. This command shall make no network requests and display no desktop notifications. Exit 0 means the workflow policy is valid and all requested, enabled destinations pass local checks; exit 1 means a configuration/readiness problem; exit 2 means command usage error. Disabled or empty policy shall be reported explicitly and is not a readiness error. Output shall distinguish local readiness from verified delivery or guaranteed desktop visibility.

**Proof Artifacts:**

- CLI output for valid inline and profiled policies plus `notifications check` demonstrates resolution and disabled-by-default behavior without any receiver requests.
- Table-driven validation output demonstrates unknown profiles/events, duplicate profile IDs, forbidden placement, missing local secrets, replacement versus inheritance, and explicit empty-list behavior.
- A two-workflow fixture sharing a profile demonstrates reuse, with one overriding its full routing list.

### Unit 2: Deliver metadata notifications for live runs

**Purpose:** Provide useful local and remote alerts from actual workflow lifecycle transitions.

**Functional Requirements:**

- **FR-07:** The system shall expose exactly `attention_required`, `run_failed`, and `run_succeeded`. Attention shall cover unresolved review, `block_on` input, `from='user'` input, agent questions, recovery, integration conflict, and final merge. A validation result alone shall not generate attention. Automatically handled/rejected headless gates shall not generate attention alerts. Terminal events shall follow actual run settlement, including final merge handling; successful completion shall require explicit selection.
- **FR-08:** The system shall suppress notifications for intermediate failed attempts, ordinary step completion, deliberate user cancellation, historical viewing, and pre-run load/start errors. A headless policy rejection or timeout that causes run failure shall generate `run_failed`, not attention; deliberate user interruption shall not. Recovery requests remain eligible attention. Terminal classification shall use the semantic completion cause rather than treating every `RunFinished{Failed: true}` as interchangeable.
- **FR-09:** The system shall publish fixed, bounded metadata only: payload schema version, logical notification ID, event kind, UTC timestamp, workflow name, run ID, and attention descriptors containing step ID when applicable and a fixed request-kind/action description. Attention summaries may include total and omitted counts. No raw errors, prompts, transcripts, tool arguments, diffs, paths, arbitrary output, or user templates shall be included. Slack and desktop formatting shall derive solely from this metadata.
- **FR-10:** The system shall send Slack incoming-webhook messages to the operator-bound channel and generic HTTPS POSTs with `Content-Type: application/json`; generic webhooks may include `Authorization: Bearer <resolved-secret>`. It shall use the fixed versioned payload below for generic receivers. It shall not follow redirects, disable TLS verification, send custom headers, or sign requests. Success shall mean HTTP 2xx for generic webhooks and Slack's documented successful response for Slack.
- **FR-11:** The system shall support local macOS and Linux desktop notifications on the execution host, with a fixed jig title and bounded metadata text. Missing helper/session support and detectable submission errors shall be diagnosed without changing run outcomes. Delivery acceptance shall not be presented as proof that a banner was displayed or read. Windows support, SSH forwarding, click actions, and terminal bell are excluded.
- **FR-12:** Notification dispatch shall be shared across the runs belonging to one jig process and wired into both TUI and headless execution, including active reopen. Agent processes, command workers, and Monitor rendering shall not own delivery/retry lifecycles. The dispatcher shall observe normalized lifecycle state independently of backend/transport selection. A slow destination shall not make an agent worker or scheduler wait for network or desktop I/O.

**Proof Artifacts:**

- A local TLS receiver capture from command-only successful/failing workflows demonstrates event selection, fixed JSON, bearer handling, and equal headless/TUI behavior without real service credentials.
- Slack adapter receiver assertions demonstrate its request shape, literal handling of untrusted names, and documented success/error classification.
- A screenshot or short recording on each supported desktop platform demonstrates actual native notification appearance, with OS/helper versions recorded. Stubbed calls alone do not prove desktop display.
- Attention scenario tests demonstrate all seven request categories and suppression for auto-handled gates, retry attempts, cancellation, and history views.

### Unit 3: Bound delivery and make failures inspectable

**Purpose:** Keep alerts useful under concurrent fan-out, service outages, and process shutdown.

**Functional Requirements:**

- **FR-13:** The dispatcher shall have a bounded process-wide pending queue and bounded active sends. Enqueue shall not wait for external delivery. Overflow shall be counted and diagnosed at every lossy notification boundary, including engine-to-dispatcher ingestion. Under pressure, an incoming terminal failure shall evict the oldest pending non-failure before it is dropped; if only failures remain, drop the new entry with a diagnostic. Notification loss shall never change workflow scheduling, result artifacts, or exit codes.
- **FR-14:** The system shall coalesce attention arrivals within a fixed window by run and destination. Before each send/retry, it shall recheck the referenced waits and remove resolved ones; an empty summary shall be discarded. New rounds/questions, reset generations, and reopened execution epochs shall be distinguishable from repeated rendering or duplicate observations. Overlapping routes shall enqueue at most one logical notification per destination. Already submitted messages shall not be recalled or edited.
- **FR-15:** Network delivery shall use bounded request timeouts, retry counts, and message lifetime. Retry only transient transport failures, HTTP 408/429, and HTTP 5xx; do not retry certificate/URL/configuration errors or other HTTP 4xx. Honor valid `Retry-After` without sending earlier than requested; abandon the message if that delay exceeds its remaining lifetime or shutdown budget. Rate limiting/backoff shall not occupy an active sender while sleeping. Retries may duplicate a previously accepted message; do not promise exactly-once delivery.
- **FR-16:** The dispatcher shall stop accepting new messages only after run producers have stopped during process shutdown, then drain for at most five seconds total, including in-flight work and retries. At the deadline it shall cancel remaining sends, discard pending work, and emit bounded diagnostics. Ordinary single-run completion in a still-running TUI shall not close a dispatcher used by other runs. No daemon or durable resend backlog shall be created.
- **FR-17:** The system shall expose sanitized delivery diagnostics including destination alias, run ID when available, outcome/reason code, attempt count, and aggregate dropped counts. Headless execution shall write these to stderr without altering existing stdout JSON/JSONL contracts. The TUI shall expose an inspectable, bounded notification-diagnostic list without taking Gate focus or altering transcripts; use existing theme styles. Repeated identical failures shall be aggregated, and a bounded in-memory recent history shall prevent diagnostic growth.
- **FR-18:** Persistence-off execution shall support live notifications with injected in-memory policy/bindings and shall not write files or accidentally resolve paths relative to the current directory. Empty local config root shall mean disabled defaults unless bindings are supplied by the caller. Notification state and terminal deduplication entries shall be released when no pending/in-flight delivery needs them.

**Proof Artifacts:**

- Fault-injection output demonstrates timeouts, 429 with retry delay, permanent 4xx, transient 5xx, cancellation, overflow priority, and a healthy destination progressing while another backs off.
- A burst fixture with 100 simultaneous attention requests demonstrates coalescing, bounded payloads, removal of resolved waits, and duplicate-route suppression.
- Shutdown and race-test output demonstrates the total five-second bound, independent concurrent runs, no leaked delivery goroutines, and persistence-off behavior.
- Captured stderr/TUI diagnostics demonstrate loss visibility and absence of secret canaries while ordinary result/exit behavior remains identical with delivery disabled or failing.

### Unit 4: Reopen with saved policy and document the complete operator flow

**Purpose:** Make notification behavior predictable across process restarts and provide a reproducible setup and troubleshooting path.

**Functional Requirements:**

- **FR-19:** The system shall persist the fully resolved, secret-free notification policy in the immutable workflow snapshot and restore it through every restoration path, including expanded modules. Reopening shall not reread current profile files to replace that policy. Current operator enablement, bindings, and secret values shall be resolved for the new execution epoch; destinations may therefore change when an operator changes a binding. Snapshot validation shall protect the resolved notification policy from unnoticed corruption using the repository's integrity conventions.
- **FR-20:** Active reopen shall seed notification state from restored unresolved waits and produce one initial attention summary per run/destination, subject to policy and current bindings. It shall not reissue historical terminal events or generate both a restored summary and separate replay alerts for the same waits. Subsequent genuinely new waits shall notify normally. Historical inspection shall create no notification producer.
- **FR-21:** Documentation and valid examples shall explain policy/profile lookup, replacement semantics, destination enablement, named secrets, per-destination filters, local desktop requirements, readiness checks, metadata disclosure, best-effort loss/duplicates, bounded shutdown, and reopen behavior. Update `docs/workflow-schema.md`, headless documentation, CLI help, and A24's status/link when implementation is actually complete; do not claim completion from this spec alone. B2 terminal bell remains separate.

**Proof Artifacts:**

- A reopen fixture changes/deletes a source profile after starting a run and changes the operator binding before reopen; receiver captures demonstrate frozen policy, current binding, and one filtered restored summary.
- History-view and repeated-reopen tests demonstrate no historical sends and the documented new-epoch behavior.
- Snapshot/diagnostic/proof scans using synthetic secret canaries demonstrate that credentials and full URLs were not persisted.
- Validated examples and captured setup/check output demonstrate an operator can configure the feature from documentation.

## Non-Goals (Out of Scope)

1. **Remote control:** No Slack buttons, webhook callbacks, remote approvals, or inbound listener.
2. **Reliable messaging service:** No cross-process dispatcher, daemon, durable outbox, delivery after jig exits, exactly-once promise, or escalation/reminder schedules.
3. **Expanded authoring:** No per-step notification overrides, multiple-profile composition, profile inheritance, global profile directory, custom message templates, arbitrary headers, HMAC signing, or secret fields in workflow TOML.
4. **Additional events/content:** No intermediate attempt notifications, cancellation notifications, pre-run failure notifications, transcripts, diagnostic excerpts, or arbitrary artifact export.
5. **Additional presentation:** No Windows desktop support, SSH-to-client forwarding, terminal bell, notification click actions, or changes to existing Gate behavior.
6. **Backend changes:** Backend/transport selection remains workflow-TOML-only and notifications introduce no backend-selection environment override.

## Design Considerations

Notification titles shall identify jig and the event; bodies shall identify the workflow/run and summarize required attention with fixed wording. Render names as literal text so workflow-controlled strings cannot create Slack mentions, links, terminal escapes, or desktop markup. A summary should say, for example, `3 steps need attention`, followed by bounded identifiers; the full local run remains the place to inspect details.

The Monitor shall offer a named `Notification diagnostics` action through its existing command/action surface. Its bounded list shall show status and reason codes without interrupting work or moving focus when a delivery fails. Global configuration errors must remain inspectable even before a Monitor exists. The implementation should reuse existing action/list patterns and theme tokens; no notification settings editor is required.

## Repository Standards

- Follow `AGENTS.md`, `CLAUDE.md`, and `CONTEXT.md`: focused internal packages, pure engine-facing contracts, backend-independent TUI, and persistence-off support.
- New schema fields require parsing, presence-aware defaults/inheritance, exhaustive validation, valid/invalid tests, documentation, and valid examples. Use strict unknown-key rejection.
- Use Go 1.25 as pinned by the repository (`go.mod` currently specifies 1.25.12); no toolchain upgrade is required for this feature.
- Preserve engine dependency inversion: no HTTP client, desktop subprocess, harness, or SDK dependency in the scheduler. Wire concrete notification services at the application composition boundary.
- Use table-driven tests, deterministic fake clocks, local TLS test servers, and fake desktop senders for automated proofs. Desktop visual proof remains a platform check.
- Run relevant tests, then `go test ./...`, `go vet ./...`, race tests for affected concurrent packages, and existing workflow example validation. Format only changed Go files. `docs/TESTING.md` has useful conventions but a stale package-status table; existing test files are authoritative.
- Maintain spec/task/proof traceability in subsequent phases. This phase creates documentation only.

## Technical Considerations

### Concrete authoring contract

These schema details make the accepted decisions implementable; they are design choices within the confirmed scope.

Profile file, `.agents/jig/notification-profiles/operator.toml` under the project root:

```toml
[[notification]]
id = "@operator"
events = ["attention_required", "run_failed"]
routes = [
  { destination = "desktop", events = ["attention_required"] },
  { destination = "team-alerts", events = ["run_failed"] },
]
```

Root workflow policy:

```toml
[notification]
profile = "@operator"
# Omitted fields inherit. routes = [] explicitly disables all routing.
```

Inline policy, without a profile:

```toml
[notification]
events = ["attention_required", "run_failed", "run_succeeded"]
routes = [{ destination = "ops" }]
```

Operator configuration at `<resolved-jig-root>/notifications.toml`:

```toml
enabled = true

[[destination]]
id = "desktop"
type = "desktop"
enabled = true

[[destination]]
id = "team-alerts"
type = "slack"
enabled = true
url_secret = "slack-notifications"

[[destination]]
id = "ops"
type = "webhook"
enabled = true
url_secret = "ops-notifications"
bearer_secret = "ops-notifications-token"
```

Use the existing named-secret resolution convention from `cmd/jig/wire.go`: for example, `slack-notifications` resolves `JIG_SECRET_SLACK_NOTIFICATIONS`. Only this operator-side configuration can request notification secret resolution. Secrets are not passed to agent processes by the notification feature. Destination aliases use the existing identifier character convention without the profile `@` prefix. Require unique aliases and reject irrelevant fields (such as bearer credentials on desktop or Slack bindings).

### Payload and delivery bounds

The generic webhook schema shall be:

```json
{
  "schema_version": 1,
  "notification_id": "opaque-logical-id",
  "event": "attention_required",
  "timestamp": "2026-09-10T15:00:00Z",
  "workflow": "review-change",
  "run_id": "example-run",
  "attention": [
    { "step_id": "review", "kind": "review", "action": "Review required" }
  ],
  "attention_count": 1,
  "omitted_count": 0
}
```

Terminal notifications omit attention fields. Run-level final merge descriptors omit `step_id`. Request kinds are `review`, `input`, `prompt`, `question`, `recovery`, `integration_conflict`, and `final_merge`; the action text is fixed per kind. Internal wait identities are used for deduplication and are not exposed as arbitrary request data. IDs remain stable across retries of a logical notification; stale-wait filtering may reduce its attention contents. An ID does not prove receiver deduplication.

Adopt these bounded defaults for implementation and testing:

| Control | Contract |
|---|---|
| Pending deliveries | 256 process-wide, including delayed retries; capacity is measured after destination expansion |
| Active sends | At most 4 process-wide and 1 per destination |
| Request/helper timeout | 3 seconds per attempt, capped by remaining lifetime/shutdown budget |
| Retry budget | 3 attempts total; delays of 1 and 2 seconds unless a valid `Retry-After` requires longer |
| Message lifetime | 30 seconds from first enqueue, including queue wait and retries |
| Attention window | 500 milliseconds from the first arrival; later arrivals do not indefinitely extend it |
| Slack pacing | At least 1 second between starts to the configured Slack destination, including retries |
| Shutdown drain | 5 seconds total for the whole dispatcher |
| Recent diagnostics | Last 100 entries; repeated identical failures aggregated |
| Attention summary | At most 10 descriptors, sorted by step ID then kind; total/omitted counts describe the rest |
| Text and bodies | Workflow name and displayed step IDs capped at 128 Unicode characters; generic request at most 16 KiB; response reads at most 4 KiB; Slack/desktop bodies at most 2,000 Unicode characters |

Truncation shall preserve valid Unicode and valid JSON and use fixed truncation wording. Truncated descriptors count as displayed descriptors; omitted counts refer to descriptors left out, not truncated characters. These bounds are fixed in this version rather than adding user-tunable policy fields. A rate-limit delay longer than remaining lifetime results in a diagnosed drop, not an early retry.

### Integration seams and state

Use a focused `internal/notification` service with injected sender, clock, secret resolver, and diagnostic sink. Shared application wiring in `cmd/jig/wire.go` shall construct it for headless/TUI entry paths. Engine events in `internal/engine/event.go` are inputs to normalization, but pending interaction state and terminal cause must be consulted before deciding what to send.

The existing control bus uses drop-on-full fan-out. A notification subscriber cannot silently inherit those losses: use a narrow observer with observable enqueue rejection or add notification-scoped drop accounting at the existing boundary. Do not introduce HTTP calls into the scheduler or make notification availability a condition of engine progress. Ingestion loss must be diagnosed even if terminal delivery cannot be reconstructed; durable catch-up is outside scope.

Final run settlement, including policy-driven headless termination, needs a normalized cause so manual cancellation is distinguishable from actual failure. Extend a narrow engine/application lifecycle seam as needed while preserving existing headless results. Wait identities must include the current request/round and execution epoch where relevant; matching only step status is insufficient for sequential questions or reset/reopen.

Snapshot work must cover both `RestoreExpanded` and source-decoding restore paths in `internal/engine/resume.go`. Persist the resolved policy and its integrity metadata rather than only the profile name. Old snapshots without notification policy are treated as notification-disabled; they must not acquire policy by reading current profiles. This is absence-of-feature behavior, not a second legacy notification implementation.

Load local enablement/bindings when each run starts or actively reopens, and keep that run's resolved bindings for its execution epoch. Changes apply to subsequent starts/reopens; live configuration watching is out of scope. Never serialize these runtime bindings. Shared dispatcher destination scheduling must distinguish resolved bindings when concurrent runs were started under different operator configurations.

### Current external guidance and platform choice

Research checked on 2026-09-10; living documentation unless otherwise noted:

- [Slack incoming webhooks](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/) documents JSON posting, channel binding, secret-bearing URLs, and response errors. Use a fixed plain-text Block Kit representation with a safely escaped fallback; do not permit workflow-controlled markup or channel override. Slack adapter tests should verify the documented `200`/`ok` success response.
- [Slack rate limits](https://docs.slack.dev/apis/web-api/rate-limits/) documents incoming-webhook pacing and `429`/`Retry-After`. The one-second pacing and bounded deferred retries above apply this guidance without adding durable delivery.
- [Freedesktop notification specification](https://specifications.freedesktop.org/notification/latest-single/) defines session D-Bus notification delivery. For Linux, use `notify-send` as the bounded local helper and document that libnotify tooling and a notification-capable desktop session are prerequisites. This avoids adding a platform-specific service implementation to the engine.
- [Apple's archived notification scripting guide](https://developer.apple.com/library/archive/documentation/LanguagesUtilities/Conceptual/MacAutomationScriptingGuide/DisplayNotifications.html) documents `display notification`. Use `/usr/bin/osascript` with a fixed script and separate data arguments; platform proof must verify it on the actual supported macOS environment because this is archived guidance, not a current visibility guarantee.
- [Go HTTP client documentation](https://pkg.go.dev/net/http) documents explicit client timeout and redirect policy. Use APIs already available in the repository's Go version; do not adopt newer toolchain-only APIs. Request cancellation and bounded response reading enforce the limits above.

The repository's file-as-truth principle remains intact: notifications are a best-effort side effect of normalized lifecycle state, not a replacement for run records or transcripts. No unresolved conflict with external guidance requires another product decision.

## Security Considerations

- Destination URLs and bearer tokens are secrets, including URLs for internal generic receivers. Resolve them only from operator-selected named secrets; do not persist them in workflow snapshots, profiles, events, transcripts, or notification diagnostics.
- Validate HTTPS URLs with nonempty hosts and without embedded userinfo or fragments. Disable redirects and preserve TLS verification. Trusted operator-owned internal HTTPS receivers remain allowed; workflow authors cannot choose arbitrary hosts through notification fields.
- Never interpolate workflow-derived text into shell/AppleScript source. Use fixed scripts and separate arguments; escape desktop markup and Slack formatting, and remove terminal control characters from displayed diagnostics.
- Use an explicit outbound metadata allowlist. Workflow names and step IDs may themselves reveal project information; document their disclosure. Do not include raw HTTP error strings or response bodies in diagnostics because they may echo credentials or private content.
- Do not add notification secrets to child environments. The feature does not claim to sandbox agents already running with the operator's OS permissions; operator-owned configuration is an authority boundary in the product, not an OS security boundary against arbitrary local code.
- Readiness checking performs no sends. Proofs shall use synthetic secrets and local receivers. Actual Slack sends during later validation require explicit user authorization; desktop visual checks should use synthetic metadata.

## Success Metrics

1. **Behavior coverage:** Every FR has an observable proof; all seven attention categories, both terminal outcomes, headless auto-handling, cancellation, and history suppression are covered.
2. **Isolation:** With a stalled receiver and saturated notification queue, command/agent completion and existing workflow result/exit semantics remain unchanged; drops are visible.
3. **Bounded resource use:** Tests demonstrate the queue, active-send, attempt, lifetime, payload, diagnostic, and total shutdown limits with deterministic clocks where practical.
4. **Policy correctness:** Inline and profile resolution, empty-list suppression, destination filtering, operator disablement, and secret-free snapshot restoration pass valid and invalid cases.
5. **Operator evidence:** Local TLS receiver captures and actual macOS/Linux desktop observations demonstrate the supported delivery surfaces; notification readiness checks never contact receivers.

## Open Questions

No blocking open questions remain. The twelve accepted decisions and final confirmation establish scope. Concrete schema names, timing/capacity bounds, and desktop helpers above are explicit implementation defaults for review; changing them requires updating this specification and its acceptance evidence together, rather than leaving implicit choices to later phases.
