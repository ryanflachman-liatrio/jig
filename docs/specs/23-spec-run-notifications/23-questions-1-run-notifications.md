# 23 Questions Round 1 - Run Notifications

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

> Resolved: the subsequent [grilling decisions](23-grilling-decisions.md) record all twelve accepted recommendations and final design confirmation. They supersede conflicting selections below. The resulting [specification](23-spec-run-notifications.md) is the implementation source of truth; original answers are preserved for context.

Source: `docs/plans/open-goals.md`, A24 — Notifications (desktop / Slack / webhook on gate or failure). B2 separately mentions terminal bell / optional notify.

## 1. Destinations and execution surfaces

Which destinations should this first version support? For desktop notifications, specify the required operating systems. The recommendation is macOS and Linux, with Slack/webhooks available in both TUI and headless runs; desktop delivery requires an available local desktop session.

- [x] (A) Desktop, Slack incoming webhook, and generic JSON webhook
- [ ] (B) Slack incoming webhook and generic JSON webhook; defer desktop
- [ ] (C) Desktop only
- [ ] (D) Terminal bell only, covering B2 instead of the full A24 goal
- [ ] (E) Other (describe, including platforms or TUI/headless restrictions)

**Current best-practice context:** The [Freedesktop notification specification](https://specifications.freedesktop.org/notification/latest-single/) describes a session-scoped D-Bus service; it is not a cross-platform desktop API. [Slack incoming webhooks](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/) accept JSON messages for a configured channel. These are living documents consulted on 2026-09-10.

**Recommended answer(s):** (A), macOS and Linux desktop support; both TUI and headless execution; terminal bell deferred.

**Why these are recommended:**

- Covers the three destinations named in A24 while keeping delivery one-way, without remote gate approval or an OAuth installation service.
- (B) is smaller if remote monitoring is the priority; (C) excludes unattended remote use; (D) primarily addresses the separate B2 item.

## 2. Events and message contents

What should trigger notifications, and what information may leave the machine?

- [ ] (A) Every new human-attention request and terminal run failure; send workflow name, run ID, step ID when applicable, event kind, and timestamp only
- [ ] (B) Same as (A), plus every failed step attempt, including automatically retried attempts
- [ ] (C) Same as (A), plus successful run completion
- [ ] (D) Same as (A), plus human prompt text and failure details
- [x] (E) Other (describe event coverage and allowed contents)

**Human Input**
I think that this should be a configurable table in toml under a [notification]. For right now lets make this at the workflow level and we may want to consider how to override this per step? We also may need a sort of notification profile similar to how we package up agent configuration into an agent profile that we are able to reference from many different workflows and steps

Human-attention requests in the current engine include review, agent input/question, user input, recovery, integration conflict, and final merge. A deterministic validation result (`GateResult`) does not itself mean a human is waiting. Under (A), repeated rendering does not notify again; a genuinely new review round or input request does. Viewing historical runs does not send notifications.

**Recommended answer(s):** (A).

**Why these are recommended:**

- Covers actionable waits and failed runs without generating alerts for transient retry attempts.
- Metadata identifies the affected run without exporting prompts, transcripts, diffs, file contents, or raw errors. (D) improves remote diagnosis but materially increases the information sent externally.
- (C) can be added if completion alerts are part of the intended outcome.

## 3. Configuration ownership and secrets

Who controls enabling notifications and choosing destinations?

- [x] (A) Operator-owned local configuration, disabled by default; destination secrets supplied through named environment-variable references; no notification fields in workflow TOML
- [ ] (B) Workflow TOML declares event/destination selections, with secrets resolved through named environment-variable references
- [ ] (C) Invocation flags select events/destinations, with secrets resolved through named environment-variable references
- [ ] (D) Local configuration contains destination URLs directly, with restricted file permissions
- [ ] (E) Other (describe)

**Current best-practice context:** [Slack's guidance](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/) treats webhook URLs as secrets and says not to put them in public version control. jig currently has local TUI preferences in `.jig/tui.json`, but notification configuration must also serve headless execution. The repository's TOML-only backend selection rule remains unchanged; notification secret references are a separate concern.

**Recommended answer(s):** (A), using a dedicated local notification configuration under the resolved `.jig` root, with one destination per enabled channel initially.

**Why these are recommended:**

- Keeps destinations under the operator's control and avoids putting secret URLs in shared workflows or command arguments.
- (B) makes policy portable with workflows but lets workflow authors influence outbound delivery. (C) is explicit but repetitive. (D) is simple locally but requires more care around configuration backups and sharing.

## 4. Delivery guarantees and restart behavior

How reliable must delivery be when a destination is unavailable or jig exits/restarts?

- [x] (A) Best effort during the owning process lifetime: bounded asynchronous queue, bounded timeout/retries, visible sanitized delivery diagnostics, no durable resend backlog; notify once for each still-pending attention request when actively reopening an unfinished run
- [ ] (B) Durable pending-delivery records and retry after restart, accepting possible duplicate messages after uncertain delivery
- [ ] (C) Best effort as in (A), but never alert for attention requests restored during reopen
- [ ] (D) Required delivery: notification failure can fail or block the workflow
- [ ] (E) Other (describe)

Under (A), delivery never changes workflow results or exit codes, process shutdown has a bounded drain, and historical journal playback never sends alerts. Queue overflow is diagnosed; delivery is not guaranteed during overload or process death. Persistence-off runs still support live delivery.

**Recommended answer(s):** (A).

**Human Input**
This should probable be its own channel outside of the goroutine used for the agent process to allow the agent process to finish. It should also allow many different running agent processes to push messages to that same channel so that we can handle timeouts/retries all from one location rather than each agents goroutine being responsible for that lifecycle.

**Why these are recommended:**

- Keeps this a bounded operator-attention feature and makes reopened waits discoverable. (C) is quieter but may leave an operator unaware of restored waits.
- (B) adds a persistent delivery lifecycle and duplicate-handling requirements; choose it if missed notifications are unacceptable. (D) couples workflow execution to external service availability.

## Repository context for the eventual spec

- `internal/engine/event.go` provides typed attention requests, `StepStatus`, `RunError`, and `RunFinished`.
- `internal/engine/engine.go` separates live and control subscriptions, but both fan-out functions currently drop on full buffers. A reliable-delivery design cannot simply assume control subscriptions are lossless.
- `internal/headless/run.go` already subscribes through the manager. Notification dispatch should be owned by run execution rather than Monitor rendering to avoid duplicate delivery and cover headless runs.
- `internal/tui/prefs/prefs.go` stores local TUI preferences, including persistence-off behavior. It is not currently a general notification configuration facility.
- Follow `AGENTS.md` and `CLAUDE.md`: focused internal packages, engine independent of harness/SDK implementations, persistence-off support, theme singleton for TUI changes, and validation/tests/docs for any new schema fields.
- `docs/TESTING.md` provides table-driven testing conventions, but its package-status table is stale; existing engine, runner, headless, and TUI tests are present.
- Suggested eventual evidence: deterministic command-only workflows producing attention/failure events, a local HTTP receiver capturing sanitized payloads, desktop observation on supported platforms, and failure/overflow/reopen tests. Exact acceptance criteria depend on the answers above.

Original clarification round resolved through the linked grilling decisions. Phase 1 specification created; implementation has not started.
