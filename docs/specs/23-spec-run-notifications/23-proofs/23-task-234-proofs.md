# Spec 23 · Tasks 2.0, 3.0, 4.0 — implementation proofs

Companion to [`23-task-01-proofs.md`](23-task-01-proofs.md). Every artifact
below was captured after the corresponding implementation landed on
`cursor/impl-run-notifications-e33c` and reruns from the repository root
without external network or receiver credentials.

## Task 2.0 · Deliver bounded metadata notifications from normalized live-run state

- [`2.0/lifecycle-terminal.txt`](2.0/lifecycle-terminal.txt) — passes every
    10|  attention category (review, input, prompt, question, recovery,
  integration_conflict, final_merge), each terminal cause (failed / timeout /
  policy_rejection settle as `run_failed`, cancelled is suppressed, succeeded
  fires when explicitly selected), suppression when a policy does not select
  the event, and the reopen restored-summary contract for both a single and
  multiple destinations (`TestLifecycle*`).
- [`2.0/payload-http.txt`](2.0/payload-http.txt) — proves the fixed JSON
  allowlist, no secrets in payload, control-character stripping, terminal
  omissions, TLS redirect refusal, retry classification (408/429/5xx +
  transient transport errors), `Retry-After` parsing, and Slack's HTTP 200 +
    20|  literal `ok` success rule against local TLS receivers
  (`TestHTTPSender*`, `TestSlackSender*`, `TestPayload*`).
- [`2.0/desktop.txt`](2.0/desktop.txt) — proves the OS-specific senders:
  macOS `/usr/bin/osascript` argument shape, Linux `notify-send` with
  `DBUS_SESSION_BUS_ADDRESS` gating, unsupported-platform response, and
  per-attempt timeout (`TestDesktopSender*`).

## Task 3.0 · Bound concurrent delivery and expose sanitized failure diagnostics

- [`3.0/dispatcher.txt`](3.0/dispatcher.txt) — proves terminal-event
  deduplication per `(RunID, Epoch, Event)`, terminal-failure eviction under
    30|  queue pressure, wait-resolver filtering before each send/retry, retry
  scheduling with the three-attempt cap, and the bounded shutdown drain
  (`TestDispatcher*`).
- [`3.0/all-notification-tests.txt`](3.0/all-notification-tests.txt) — full
  focused notification suite (`go test ./internal/notification -count=1 -v`)
  including diagnostic aggregation, overflow accounting, and secret-free
  render.
- [`3.0/race.txt`](3.0/race.txt) — `-race` run over the same package
  (concurrent dispatch through queue/coalescing/retry state).
- [`3.0/tui-diagnostics.txt`](3.0/tui-diagnostics.txt) — proves the TUI's
    40|  root-level notification-diagnostics overlay opens on `Ctrl+G`, renders
  the injected snapshot, closes on `Esc`/`Ctrl+G`, and degrades gracefully
  when the runtime provides no renderer (`TestDiagnosticsOverlay*`).

## Task 4.0 · Reopen from frozen policy and document the complete operator flow

- [`4.0/snapshot-reopen.txt`](4.0/snapshot-reopen.txt) — proves the workflow
  snapshot persists the fully resolved notification policy alongside its own
  SHA-256, that a tampered snapshot fails closed on load, that reopen
  restores the frozen policy without re-reading current profile files, that
  older snapshots without policy default to disabled, and that
    50|  `Manager.Resume` seeds a single `RunRegistered` with matching wait nonces
  so the observer collapses seed and replay onto one wait key
  (`TestWorkflowSnapshot*`, `TestResumeObserverReceivesReopenWithSeededWaits`).
- [`4.0/secret-scan.txt`](4.0/secret-scan.txt) — scans every proof artifact,
  example workflow, and shared profile for actual URL/token canaries
  (`JIG_SECRET_*=<value>`, real Slack webhook URLs, `Bearer <token>`, and
  common provider-token shapes). No real secret leaked into persisted files
  or proofs; matches on test-case names such as `missing_bearer` are
  correctly ignored.

## Reproducing the captures

```bash
    60|# Task 2 focused suites
go test ./internal/notification -run 'TestLifecycle|TestAttention|TestTerminal' -count=1 -v
go test ./internal/notification -run 'TestPayload|TestWebhook|TestSlack|TestHTTP' -count=1 -v
go test ./internal/notification -run 'TestDesktop' -count=1 -v

# Task 3 focused suites (dispatcher, diagnostics, TUI overlay)
go test ./internal/notification -run 'TestDispatcher|TestQueue|TestRetry|TestRate|TestAttentionCoal|TestShutdown|TestPersistenceOff' -count=1 -v
go test ./internal/notification -count=1 -v
go test ./internal/notification -count=1 -race
go test ./internal/tui -run TestDiagnosticsOverlay -count=1 -v
    70|
# Task 4 snapshot / reopen suites
go test ./internal/engine -run 'TestWorkflowSnapshot|TestResumeObserver' -count=1 -v
```

## Cross-cutting invariants confirmed

- Bounded metadata: JSON allowlist keeps only schema_version=1, notification
  ID, event, UTC timestamp, workflow, run ID, and (for attention) up to ten
  sorted step descriptors with total/omitted counts. Prompts, transcripts,
  paths, tool arguments, diffs, and outputs never appear.
    80|- Deterministic dispatch: queue capacity 256, max active 4 (per-alias 1),
  attention window 500 ms, message lifetime 30 s, Slack pacing 1 s, retry cap
  3, ordered shutdown budget 5 s.
- Persistence-off: empty run root leaves every writer as a no-op; TUI/headless
  can still deliver live in-memory.
- Reopen: frozen policy plus SHA-256, current bindings resolved fresh per
  epoch, one restored summary per (run, epoch, destination), no duplicate
  live-event replay.
- Cancellation causation: `run_failed` fires on ordinary failure, timeout,
  and policy rejection; deliberate cancellation is suppressed.
    90|
## Deferred / out of scope

- Actual live-receiver demonstration against a real Slack workspace or
  webhook. Requires operator authorization outside the automated proof set.
- Full navigable list/detail TUI diagnostics screen. The current overlay is
  a read-only text panel; the polish path (typed messages, list navigation,
  focus-preserving refresh) tracks under future TUI polish work.
- Native macOS/Linux desktop notification screenshots (`23-proofs/2.0/desktop-*`).
  The fake-runner tests carry the automated evidence; a real capture on a
  macOS/Linux host is a manual follow-up when receiver access is available.
