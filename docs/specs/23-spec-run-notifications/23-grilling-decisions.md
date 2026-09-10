# A24 Run Notifications — Grilling Decisions

## Accepted round 1

The user accepted all seven recommendations in chat. These decisions supersede conflicting selections in `23-questions-1-run-notifications.md`.

1. **Authority:** Workflow TOML selects policies and named destination aliases. Operator-owned local configuration binds credentials and explicitly enables delivery. Workflows cannot provide arbitrary destination URLs or override operator disablement.
2. **Profiles:** Reusable profiles contain event selections, destination aliases, and content policy. Destination bindings remain operator-owned. Workflow-level references are in scope; per-step overrides are deferred.
3. **Events:** Expose `attention_required`, `run_failed`, and `run_succeeded`. Default to attention and failure when enabled. Attention represents actual unresolved human waits, including recovery and integration conflict; automatically handled headless gates do not generate attention alerts. Attempt failures, cancellation notifications, and pre-run error notifications are deferred.
4. **Contents:** Workflow name, run ID, step ID when applicable, event kind, timestamp, and a fixed action description. No raw errors, prompts, transcripts, diffs, or arbitrary templates. Workflow names themselves may be sensitive; document exact outbound fields.
5. **Dispatcher:** One shared asynchronous dispatcher per jig process serves concurrent runs and steps. Bounded concurrency isolates stalled destinations. Agent workers do not own retries. Normal shutdown permits up to five seconds to drain; remaining work is abandoned. No cross-process daemon or post-exit delivery.
6. **Delivery:** Best effort, bounded retries, occasional duplicates permitted. Coalesce simultaneous attention into per-run summaries; discard queued attention notifications whose waits have resolved; prioritize terminal failures under queue pressure; diagnose drops. No durable outbox or exactly-once promise.
7. **Desktop:** Native macOS and Linux notifications on the execution host. Slack and generic JSON webhooks serve remote runs. Defer Windows, SSH-to-client forwarding, terminal bell, and clickable actions. Missing desktop services produce delivery diagnostics without changing run outcomes.

## Repository findings

- Workflow loading rejects unknown TOML keys and resolves agent profiles before defaults (`internal/workflow/load.go`). Notification-profile composition still needs a clear contract rather than implicitly copying zero-value merge behavior.
- Project-local agent profiles are loaded from `.agents/jig/profiles/` relative to the loader base directory (`internal/workflow/load_profiles.go`). Notification profiles need a distinct namespace and documented lookup base.
- Reopened runs use immutable saved workflow snapshots (`internal/engine/resume.go`). Notification policy snapshotting must distinguish portable policy from current operator bindings and secrets.
- Engine control subscribers can drop on full buffers. Notification overflow diagnostics must cover the ingestion boundary, not only outbound delivery queues.
- Headless policy may resolve or reject gates immediately. Raw request events alone are insufficient evidence that a human remains needed.

## Accepted round 2

The user accepted all five recommendations in chat.

8. **Composition:** One project-local notification profile, in a separate directory from agent profiles, with explicit workflow-level overrides. Explicit fields replace inherited fields, including whole lists; an empty list disables that selection. No multiple-profile merging or per-step overrides.
9. **Routing:** Per-destination event filters, expressed as event-to-destination rules in notification policy. Operators can disable destinations but cannot broaden the workflow's event selection. Content remains the fixed metadata policy accepted in round 1.
10. **Validation:** Malformed workflow/profile configuration fails validation. Missing or unusable local destination bindings or secret values disable affected delivery with sanitized diagnostics; the workflow continues. Provide a notification configuration check so operators can inspect readiness separately from workflow execution.
11. **Generic webhooks:** Operator-owned HTTPS destinations, secret references, optional bearer authentication, and a fixed versioned JSON metadata payload. No arbitrary headers, custom payload templates, or request signing in this scope.
12. **Reopen:** Save resolved notification policy in the immutable run snapshot. On active reopen, use that saved policy with current operator enablement and destination/secret bindings. Emit one summary of still-pending human waits per reopened run, subject to the enabled policy. Do not resend historical failures or notify during historical viewing.

## Consistency check and implementation constraints

- Destination secrets and resolved secret-bearing URLs must not enter workflow snapshots, profiles, transcripts, diagnostics, or proof artifacts.
- Whole-list replacement applies to routing rules as well as event selections; absence means inherit, whereas an explicit empty list means clear. The schema must preserve this distinction.
- Repeated references or overlapping rules must not cause multiple deliveries of the same logical event to the same destination. Network retries can still produce duplicates.
- A reopened run's policy is frozen, but its destination can change because current operator bindings are authoritative. This is deliberate, and the documentation must make it clear.
- A coalesced attention summary must be filtered by destination and refreshed against unresolved waits before delivery/retry. An already delivered external message cannot be recalled when its wait resolves.
- Agent process completion and workflow completion are separate from notification draining. The dispatcher's shutdown deadline must cover its total drain, not five seconds per destination.
- Optional delivery diagnostics must preserve headless structured-output and exit-code contracts. Configuration checking must distinguish local readiness from proof of actual delivery.
- Concrete schema names, queue capacities, retry timing, summary windows, diagnostic placement, desktop adapter mechanisms, and test seams remain specification work within the accepted behavior; they do not introduce new product scope.

## Interview status

All twelve questions are resolved. No additional blocking product choices were identified in the consistency check. The user confirmed the consolidated design with "continue" after the final design check. The interview is complete.

Phase 1 produced [23-spec-run-notifications.md](23-spec-run-notifications.md), including explicit schema, timing/capacity defaults, platform mechanisms, and observable proof requirements within the accepted scope. No implementation has been performed. The next SDD phase is task planning and its mandatory audit.
