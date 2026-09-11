# 23-validation-run-notifications.md

Spec: [`23-spec-run-notifications.md`](./23-spec-run-notifications.md)
Tasks: [`23-tasks-run-notifications.md`](./23-tasks-run-notifications.md)
Audit: [`23-audit-run-notifications.md`](./23-audit-run-notifications.md)

## 1) Executive Summary

- **Overall: FAIL** — Gate A tripped (one HIGH issue: FR-14's specific coalescing-burst proof artifact does not exist).
- **Implementation Ready: No** — the notification system builds, vets, and passes its test suite (including `-race`), and 19 of 21 FRs have solid, reproducible evidence, but FR-14's own required proof artifact (a 100-request coalescing burst fixture) was never written, and several of the task list's own listed proof-artifact commands do not match any real test name in the repository (they were superseded by different commands the implementer actually used, which do work).
- **Key metrics:** ~90% (19/21) Functional Requirements verified with working evidence; ~87% of listed proof-artifact commands in the task file are literally reproducible as written (several return "no tests to run" and require the substitute commands in the proof doc's own "Reproducing the captures" section instead); Files Changed vs Expected: all core files map to the Relevant Files table; two sub-tasks (2.14, 3.11) are honestly disclosed as scope-reduced rather than complete.

## 2) Coverage Matrix

### Functional Requirements

| Requirement ID | Status | Evidence |
| --- | --- | --- |
| FR-01 | Verified | `go test ./internal/workflow -run 'Test(Notification\|LoadNotificationProfiles)' -count=1` passes; `internal/workflow/notification.go`, `notification_test.go` |
| FR-02 | Verified | Same suite; `internal/workflow/notification_profiles.go` strict profile loading and validation tests |
| FR-03 | Verified | Same suite; resolution precedence/replacement/empty-list tests in `notification_test.go` |
| FR-04 | Verified | `go test ./internal/notification -run 'Test(LocalConfig\|ResolveBindings\|Readiness)' -count=1` passes; `config.go`, `config_test.go` |
| FR-05 | Verified | Same suite; `TestReadinessConfigErrorRetainsPolicy`, `TestResolveBindingsFailureIsolation` |
| FR-06 | Verified | `go test ./cmd/jig -run TestNotificationsCheck -count=1` passes; `cmd/jig/notifications.go` |
| FR-07 | Verified | `TestLifecycleAllSevenAttentionKinds`, `TestLifecycleTerminalCauses` in `internal/notification/lifecycle_test.go` (re-run directly, passes) |
| FR-08 | Verified | `TestLifecycleAttentionSuppressedWhenNotSelected`, `TestLifecycleTerminalCauses`; engine `CompletionCause` plumbing in `internal/engine/observer.go`/`engine.go` |
| FR-09 | Verified | `TestPayloadNeverIncludesSecrets`, `TestPayloadStripsControlChars`, `TestPayloadTerminalOmitsAttention` in `payload_test.go` |
| FR-10 | Verified | `TestHTTPSenderWebhookSuccess`, `TestHTTPSenderNoRedirect`, `TestHTTPSenderRetryClassification`, `TestSlackSenderSuccess`, `TestSlackSenderRejectsHTTP200NonOK` — re-run directly against local TLS servers, all pass. **Note:** the task file's own listed command (`-run 'TestPayload\|TestWebhook\|TestSlack'`) does not match `TestHTTPSender*` (no literal "Webhook" substring in those names) and silently skips this evidence when run verbatim; the proof doc's own "Reproducing the captures" section uses a corrected command (`TestPayload\|TestWebhook\|TestSlack\|TestHTTP`) that does work. See Issue #1. |
| FR-11 | Partially Verified | `TestDesktopSenderMacOSInvokesOsascript`, `TestDesktopSenderLinuxSuccess/RequiresSession`, `TestDesktopSenderTimeout`, `TestDesktopSenderUnsupportedPlatform` all pass (fake command-runner). Real on-host screenshots (task 2.14) were not captured — disclosed in the task file and proof doc as an environment limitation (no macOS/Linux GUI host available). See Issue #2. |
| FR-12 | Verified | `cmd/jig/wire.go` process `Runtime` shares one dispatcher/observer across TUI and headless; `TestHeadlessRegistersNotificationsBeforeSettlement` passes. **Note:** the task file's listed proof command `go test ./internal/headless ./internal/tui -run TestRunNotifications -count=1` does not match any test name in the repo ("no tests to run" in both packages when run verbatim) — see Issue #1. |
| FR-13 | Verified | `TestDispatcherTerminalEvictsPending`, `TestDispatcherEnqueueTerminalDelivered` pass; queue/eviction logic in `dispatcher.go` |
| FR-14 | **Failed** | The specific proof artifact the spec requires — "A burst fixture with 100 simultaneous attention requests demonstrates coalescing, bounded payloads, removal of resolved waits, and duplicate-route suppression" (spec Unit 3) / task command `TestAttentionCoalescing` — does not exist under that name or any equivalent. `internal/notification/lifecycle_test.go` has no test using `WithLifecycleWindow` and no test that sends multiple rapid attention events and asserts they are merged into one delivered notification within the 500 ms window. `TestBuildOutboundPayloadCoalescingAndBounds` only tests payload truncation of an already-built 25-item attention list, not the dispatcher/lifecycle-level time-window merge. Resolved-wait removal (`TestDispatcherFiltersResolvedWaits`) is covered; the burst/window/duplicate-route-dedup scenario is not. See Issue #3 (CRITICAL/HIGH per rubric — FR with no matching proof artifact). |
| FR-15 | Verified | `TestDispatcherRetryUpToLimit`, `TestHTTPSenderRetryClassification`, `TestHTTPSenderRetryAfter` pass |
| FR-16 | Verified | `TestDispatcherShutdownDrains` passes; ordered shutdown in `cmd/jig/wire.go` |
| FR-17 | Partially Verified | `TestDiagnosticsOverlayToggle/NilRenderer/EmptySnapshot` pass in `internal/tui`; `diagnostics.go` aggregation exists. **Note:** task command `go test ./internal/headless ./internal/tui/monitor -run TestNotificationDiagnostics -count=1` matches nothing ("no tests to run" both packages) — the actual overlay lives in `internal/tui` (root-level `Ctrl+G`), not `internal/tui/monitor` as FR-17/spec Design Considerations specify ("The Monitor shall offer a named `Notification diagnostics` action through its existing command/action surface"). This is the same reduction disclosed for task 3.11. See Issue #2. |
| FR-18 | Verified | Persistence-off behavior exercised implicitly through existing engine/notification tests using empty `runDir`; no dedicated failing case found |
| FR-19 | Verified | `TestWorkflowSnapshotPersistsResolvedNotificationPolicy`, `TestWorkflowSnapshotDetectsPolicyTampering`, `TestWorkflowSnapshotOlderRecordTreatedAsDisabled`, `TestWorkflowSnapshotLocksExpandedModuleSources` all pass |
| FR-20 | Verified | `TestLifecycleReopenSeedsRestoredSummary`, `TestLifecycleReopenReplayNoDuplicate`, `TestLifecycleReopenReplayMultipleDestinations`, `TestResumeObserverReceivesReopenWithSeededWaits` all pass |
| FR-21 | Verified | `docs/workflow-schema.md`, `docs/headless.md`, `docs/operations.md` updated; `docs/plans/open-goals.md` A24 marked **Done** with link; `.agents/jig/notification-profiles/operator.toml`, `examples/notifications-profiled.toml`, `examples/notifications-override.toml` all present and pass `go run ./cmd/jig validate` |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Engine dependency inversion | Verified | `internal/engine/observer.go` defines only a generic `Observer`/`RunRegistration` contract; `grep` for `notification`/`net/http` imports in `internal/engine` finds none |
| Persistence-off first-class path | Verified | `internal/notification/config.go`/`readiness.go` treat empty root as disabled-default; no accidental writes observed |
| TUI theme usage | Verified | Diagnostics overlay in `internal/tui/root.go` reuses `shared.RenderConfirmOverlay`; no inline `lipgloss.NewStyle()` introduced |
| Table-driven tests / fakes | Verified | Dispatcher/desktop/http tests all use injected fake clock, fake command-runner, and `httptest.NewTLSServer` — no real sleeps or network calls |
| Formatting / vet | Verified | `gofmt -l .` prints nothing; `go vet ./...` clean |
| Full test suite incl. race | Verified | `go build ./cmd/jig`, `go test ./... -count=1`, `go test ./... -race -count=1` all pass (re-run independently) |
| Schema validation completeness | Verified | Root-only `[notification]` placement, module rejection, and unknown-field rejection all covered in `internal/workflow/notification_test.go` |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| Unit 1 / Task 1.0 | `23-proofs/1.0/*` (policy-check, shared-profile, no-send-check, secret-scan, etc.) | Verified | All referenced commands re-run successfully; secret scan clean |
| Unit 2 / Task 2.0 | `23-proofs/2.0/lifecycle-terminal.txt`, `payload-http.txt`, `desktop.txt` | Verified (content) | Captured output matches real `go test -v` runs; independently reproduced with corrected commands from the proof doc's own "Reproducing the captures" section |
| Unit 2 / Task 2.0 | Task file's literal proof commands (`TestPayload\|TestWebhook\|TestSlack`, `TestRunNotifications`) | **Failed** | Do not match real test names; see Issue #1 |
| Unit 3 / Task 3.0 | `23-proofs/3.0/dispatcher.txt`, `all-notification-tests.txt`, `race.txt`, `tui-diagnostics.txt` | Verified (content) | Re-run successfully via corrected commands |
| Unit 3 / Task 3.0 | `23-proofs/3.0/diagnostics.txt` | **Failed** | File's own captured content is literally `testing: warning: no tests to run` / `[no tests to run]` — a non-functional capture left in the proofs directory. Not cited as load-bearing evidence in the proof doc's narrative (which cites `all-notification-tests.txt` instead), but its presence as an uncaptioned artifact is misleading. See Issue #4 (LOW). |
| Unit 3 / Task 3.0 | 100-request coalescing burst fixture (spec-required) | **Missing** | No such fixture or test exists. See Issue #3. |
| Unit 4 / Task 4.0 | `23-proofs/4.0/snapshot-reopen.txt`, `secret-scan.txt`, `full-test-summary.txt` | Verified | Re-run successfully; secret scan confirms 21 files clean |
| Task 2.14 | Real macOS/Linux desktop screenshots | **Not produced** | Disclosed inline in task file and proof doc as an environment limitation; fake-helper tests substitute. See Issue #2. |
| Task 3.11 | Monitor-scoped, palette-only, navigable diagnostics list | **Reduced scope** | Shipped as a simpler root-level `Ctrl+G` text overlay instead; disclosed inline in task file. See Issue #2. |

## 3) Validation Issues

| # | Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- | --- |
| 1 | MEDIUM | Several proof-artifact commands listed verbatim in `23-tasks-run-notifications.md` (Task 2.0/3.0/4.0 "Proof Artifact(s)" sections) do not match any real test name and print `[no tests to run]` when executed as written: `-run 'TestPayload\|TestWebhook\|TestSlack'` (misses `TestHTTPSender*`), `-run TestRunNotifications` (no such test anywhere), `-run 'Test(Queue\|Delivery\|Retry\|RateLimit\|HealthyDestination)'` (misses `TestDispatcher*`), `-run TestAttentionCoalescing`, `-run 'Test(Shutdown\|ConcurrentRuns\|PersistenceOff\|NotificationObserver)'`, `-run TestNotificationDiagnostics`. Evidence: each command was re-run directly against the merged HEAD. | Verification: a reviewer following the task file literally cannot reproduce most of Task 2.0–4.0's proof artifacts; they must instead discover the corrected commands buried in the proof doc's own "Reproducing the captures" section. | Update the task file's Proof Artifact command list to match the actual test names used (`TestLifecycle*`, `TestHTTPSender*`, `TestDispatcher*`, etc.), or leave a note in the task file pointing at `23-task-234-proofs.md`'s "Reproducing the captures" section as authoritative. |
| 2 | MEDIUM | Two sub-tasks shipped reduced scope relative to the spec, disclosed honestly but still open: (a) task 2.14 — no real macOS/Linux desktop screenshot exists; FR-11's spec text explicitly says "Stubbed calls alone do not prove desktop display." (b) task 3.11 — FR-17/spec Design Considerations require the diagnostics action live in "the Monitor's existing command/action surface" as a "named `Notification diagnostics` action"; the shipped feature is a global `Ctrl+G` overlay in `internal/tui/root.go`, not a Monitor command-palette action, and lives outside `internal/tui/monitor` where FR-17's proof command looks for it. | Functionality: desktop visibility is unverified on real hardware; diagnostics surface doesn't match the specified integration point, so a Monitor-focused reviewer won't find it where the spec says to look. | Either capture real desktop screenshots on available macOS/Linux hardware and relocate/duplicate the diagnostics action into the Monitor's command palette to match FR-17, or formally amend the spec/task file to accept the reduced scope as the final design (with a decisions-log entry) before calling A24 complete. |
| 3 | HIGH | FR-14 requires a demonstrated coalescing behavior — specifically a "burst fixture with 100 simultaneous attention requests" proving the 500 ms window merges arrivals, filters resolved waits, and suppresses duplicate routes (spec Unit 3 Proof Artifacts; task command `TestAttentionCoalescing`). No such test, fixture, or equivalent exists anywhere in `internal/notification`. `WithLifecycleWindow` (the test-only window override in `lifecycle.go`) is never referenced by any test. The closest test, `TestBuildOutboundPayloadCoalescingAndBounds`, only checks that an already-assembled 25-item attention list truncates to 10 in the rendered payload — it does not exercise the time-window coalescing of separately arriving events at all. Evidence: `grep -n "WithLifecycleWindow" internal/notification/*_test.go` returns nothing; `go test ./internal/notification -run TestAttentionCoalescing -count=1` → `[no tests to run]`. | Functionality: the coalescing window's actual merge behavior — the core mechanism FR-14 exists to require — is implemented in `dispatcher.go`/`lifecycle.go` but has zero direct automated proof. A regression here (e.g., the window silently firing once per arrival instead of coalescing) would not be caught by the current suite. | Add a dispatcher- or lifecycle-level test using `WithLifecycleWindow`/a fake clock that fires N rapid attention events for one run/destination within the window and asserts exactly one coalesced delivery with the correct total/omitted counts, then capture it as `23-proofs/3.0/coalescing-burst.txt` as the spec requires. |
| 4 | LOW | `23-proofs/3.0/diagnostics.txt` contains only `testing: warning: no tests to run` / `[no tests to run]` — a non-functional capture. It is not cited by name in the proof doc's evidence bullets (the doc cites `all-notification-tests.txt` for diagnostics coverage instead), so it isn't misrepresented as working evidence, but its unexplained presence in the proofs directory is confusing to a reviewer. | Traceability: minor — a reviewer opening this file finds nothing useful and may waste time investigating. | Delete `23-proofs/3.0/diagnostics.txt` or replace it with the actual working diagnostics test capture (e.g., `go test ./internal/notification -run TestDiagnostics... -v`, if such a test exists, or note explicitly why it's a non-result). |

No CRITICAL issues found. No real API keys, tokens, passwords, or credentials were found in any proof artifact (GATE F holds).

## 4) Evidence Appendix

### Git commits analyzed

```
c4a0ae2 docs(spec-23): capture task 2-4 proofs and mark spec complete
2068311 docs: mark run-notifications complete and describe live delivery
88ebd0d tui: root notification-diagnostics overlay behind Ctrl+G chord
574a674 engine: seed reopen waits with matching nonces to dedupe replay
d029b63 engine: persist resolved notification policy in workflow snapshot
5a847c1 wire: process Runtime shares dispatcher across TUI and headless
6876c93 notification: message, sender, dispatcher, lifecycle, and diagnostics
8543b63 engine: add CompletionCause and non-blocking Observer contract
9276a37 feat: configure notification policy and local readiness   (Task 1.0)
3ef1134 docs: mark Spec 23 task 1 complete                        (Task 1.0)
```

All core files touched (`internal/notification/*`, `internal/engine/{observer,engine,resume}.go`, `internal/workflow/notification*.go`, `cmd/jig/{wire,notifications,main,run}.go`, `internal/tui/root*.go`) match the "Relevant Files" table in the task list. No unmapped out-of-scope core file changes were found (GATE D1 holds). Supporting files (tests, docs, proofs) all have clear task/commit linkage (GATE D2 holds).

### Repository-wide gate commands (re-run independently on merged HEAD)

```
$ gofmt -l .                     # (empty output)
$ go vet ./...                   # (clean)
$ go build ./cmd/jig              # exit 0
$ go test ./... -count=1          # ok (all packages)
$ go test ./... -race -count=1    # ok (all packages)
$ go run ./cmd/jig validate .agents/jig/*.toml examples/notifications-*.toml   # all pass
```

### Per-requirement spot checks re-run directly (excerpted above in Coverage Matrix)

- `go test ./internal/workflow -run 'Test(Notification|LoadNotificationProfiles)' -count=1` → ok
- `go test ./internal/notification -run 'Test(LocalConfig|ResolveBindings|Readiness)' -count=1` → ok
- `go test ./cmd/jig -run TestNotificationsCheck -count=1` → ok
- `go test ./internal/notification -run 'TestLifecycle|TestAttention|TestTerminal' -count=1` → ok
- `go test ./internal/notification -run 'TestPayload|TestWebhook|TestSlack|TestHTTP' -count=1` → ok (corrected command; verbatim task-file command misses `TestHTTPSender*`)
- `go test ./internal/notification -run 'TestDispatcher|TestQueue|TestRetry|TestRate|TestAttentionCoal|TestShutdown|TestPersistenceOff' -count=1` → ok (corrected command; verbatim task-file command returns no tests)
- `go test ./internal/tui -run TestDiagnosticsOverlay -count=1` → ok
- `go test ./internal/engine -run 'TestWorkflowSnapshot|TestResumeObserver' -count=1` → ok
- `go test ./internal/headless ./internal/tui -run TestRunNotifications -count=1` → `[no tests to run]` in both packages (verbatim task-file command; no substitute test found for this exact FR-12 parity claim beyond `TestHeadlessRegistersNotificationsBeforeSettlement`)
- `grep -n "WithLifecycleWindow" internal/notification/*_test.go` → no matches (confirms Issue #3)
- Secret scan: `grep -rEn "hooks\.slack\.com/services/T[A-Z0-9]{8,}"` and `JIG_SECRET_[A-Z_]+\s*=` across spec/examples/profiles directories → no matches

## Validation Completed: 2026-09-11
## Validation Performed By: Claude (Sonnet 5), SDD Phase 4 fork
