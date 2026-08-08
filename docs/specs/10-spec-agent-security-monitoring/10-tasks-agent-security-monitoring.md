# 10-tasks-agent-security-monitoring.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/step/step.go` | Add `TotalCostUSD *float64` and `Usage *map[string]any` to `Result` (Unit 0). |
| `internal/runner/agent.go` | Read cost from `ResultMessage`; wire the Tier-1 guard via `WithCanUseTool`; add transcript-redaction filter (Units 0, 2). |
| `internal/runner/agent_test.go` | Unit tests for cost capture and blocked-and-redacted guard integration (Units 0, 2). |
| `internal/manifest/manifest.go` | Enrich `stepResultJSON` with cost fields so per-step cost is durable on disk (Unit 0). |
| `internal/engine/engine.go` | Add cost sum to `RunSnapshot`; handle `SecurityFinding` ctrl events; route critical findings to `enterRecovery` (Units 0, 5). |
| `internal/engine/engine_test.go` | Tests for `RunSnapshot` cost sum and critical-finding escalation (Units 0, 5). |
| `internal/engine/event.go` | Add `SecurityFinding` event type with `isEvent()` marker (Unit 1). |
| `internal/engine/journal.go` | Wire `SecurityFinding` into `eventKind` and `decoders`; fix 3 pre-existing missing decoders (`InputRequest`, `PromptRequest`, `AgentQuestion`) (Unit 1). |
| `internal/engine/journal_test.go` | Build the real exhaustiveness test; add round-trip cases for the 3 fixed events and `SecurityFinding` (Unit 1). |
| `internal/datastore/datastore.go` | Add `FindingsPath(runDir string) string` helper beside `TranscriptPath` (Unit 1). |
| `internal/datastore/datastore_test.go` | Extend tests to cover `FindingsPath` (Unit 1). |
| `internal/sentinel/finding.go` | New file: `Finding` type, `Redact` helper, `Fingerprint` function (Unit 1). |
| `internal/sentinel/sink.go` | New file: append-only JSONL writer/reader for `findings.jsonl`; no-ops on `""` path (Unit 1). |
| `internal/sentinel/guard.go` | New file: `Guard` struct, `Decision` type, thread-safe `Check(toolName, input)` (Unit 2). |
| `internal/sentinel/rules.go` | New file: starter ruleset — secret-in-write, non-allowlisted host, denied shell patterns (Unit 2). |
| `internal/sentinel/supervisor.go` | New file: detection supervisor — bus subscriber, window assembly, batching, budget enforcement (Unit 3). |
| `internal/sentinel/prefilter.go` | New file: deterministic prefilters for stuck-loop and exfil-pattern monitors (Unit 4). |
| `internal/sentinel/finding_test.go` | New file: redaction / fingerprint unit tests (Unit 1). |
| `internal/sentinel/sink_test.go` | New file: sink round-trip unit tests (Unit 1). |
| `internal/sentinel/guard_test.go` | New file: table-driven `Guard.Check` unit tests (Unit 2). |
| `internal/sentinel/supervisor_test.go` | New file: batching, dedup, truncation, and budget-degrade unit tests (Units 3–4). |
| `internal/tui/styles.go` | Add `theme.Security` sub-struct (severity row styles from `danger`/`warning`/`success` tokens) (Unit 5). |
| `internal/tui/monitor.go` | Add Security region rendering; handle `SecurityFinding` ctrl events in `Update` loop (Unit 5). |
| `internal/tui/monitor_test.go` | Render golden tests for the Security pane (Unit 5). |
| `internal/workflow/schema.go` | Add `SecurityConfig` and `StepSecurity` types; extend `Defaults` and `Step` with `Security` field (Unit 6). |
| `internal/workflow/load.go` | Cascade security config in `applyDefaults` (mirroring `model`/`effort` inheritance) (Unit 6). |
| `internal/workflow/validate.go` | Validate security config at load time; valid and invalid test cases (Unit 6). |
| `internal/workflow/workflow_test.go` | Add security-config valid/invalid decode test cases (Unit 6). |
| `examples/feature.toml` | Add `[defaults.security]` example; must still pass `jig validate` (Unit 6). |
| `examples/agents/monitors/prompt-injection.md` | New file: Haiku monitor agent — tools-off, structured output, injection detection (Unit 4). |
| `examples/agents/monitors/stuck-loop.md` | New file: Haiku monitor agent — tools-off, structured output, stuck-loop detection (Unit 4). |
| `examples/agents/monitors/exfil-pattern.md` | New file: Haiku monitor agent — tools-off, structured output, exfiltration detection (Unit 4). |
| `docs/workflow-schema.md` | Add security config section documenting `[defaults.security]` / `[step.security]` (Unit 6). |
| `docs/security-monitoring.md` | New file: end-to-end security-monitoring doc (tiers, findings format, redaction, escalation, cost) (Unit 6). |
| `docs/adr/NNNN-agent-security-monitoring.md` | New file: ADR recording out-of-band + raise-don't-kill decisions (Unit 6). |

### Notes

- Unit tests live beside the implementation file they test (e.g., `finding.go` and `finding_test.go` in `internal/sentinel/`).
- Run tests with `go test ./...`; use `-race` for supervisor and engine changes.
- Run `gofmt -l -w .` and `go vet ./...` at each unit boundary before committing.
- `go run ./cmd/jig validate examples/feature.toml` must exit 0 at every unit boundary.
- Persistence-off path (empty `runDir`) must remain a no-op throughout — verify it in every new writer.

## Tasks

### [x] 1.0 Unit 0 — Cost foundation: per-step and per-run cost accounting

**Prerequisite for the fleet budget (Unit 3).** `AgentExecutor.Execute` currently
discards `ResultMessage.TotalCostUSD`; `step.Result` has no cost field; `RunSnapshot`
has no run-level total. This unit wires up the cost recording path end-to-end and
exposes per-step and per-run cost in the TUI.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/runner -run TestCostCapture -v` → scripted SDK channel carrying `TotalCostUSD` yields `step.Result` with that value; a channel with no cost yields `nil` pointer (not `0.00`). `10-proofs/0.0-cost-capture.txt`.
- Test: `go test ./internal/engine -run TestRunSnapshotCost -v` → three steps with known costs sum to the expected `RunSnapshot` total; persistence-off writes no cost file. `10-proofs/0.1-run-sum.txt`.

#### 1.0 Tasks

- [x] 1.1 Add `TotalCostUSD *float64` and `Usage *map[string]any` as pointer fields to `step.Result` in `internal/step/step.go` (pointer = nil means "unknown/unreported", distinct from `$0.00`). Update the JSON tags.
- [x] 1.2 In `captureStream` (`internal/runner/agent.go`), read `m.TotalCostUSD` and `m.Usage` from the `*claudecode.ResultMessage` case and set them on `result` (both success and error branches). Add a nil-check so a `ResultMessage` without cost leaves the fields nil.
- [x] 1.3 Enrich `stepResultJSON` in `internal/manifest/manifest.go` with `TotalCostUSD *float64` and write it from `state.Result` in `writeResult`. Confirm the persistence-off path (no `runDir`) writes no cost file.
- [x] 1.4 Add `TotalCostUSD float64` to `RunSnapshot` in `internal/engine/engine.go`. In `scheduler.snapshot()`, sum `st.Result.TotalCostUSD` across all steps that have a non-nil cost.
- [x] 1.5 Display per-step cost (e.g., `$0.0042`) in the step-detail pane and per-run cost in the run header in `internal/tui/monitor.go`. Add any new style tokens to `internal/tui/styles.go` (derive from existing muted/dim tokens — no bare hex colors).
- [x] 1.6 Write `TestCostCapture` in `internal/runner/agent_test.go` and `TestRunSnapshotCost` in `internal/engine/engine_test.go`. Capture test output to `10-proofs/0.0-cost-capture.txt` and `10-proofs/0.1-run-sum.txt`.

---

### [x] 2.0 Unit 1 — Finding model, `findings.jsonl` sink, `SecurityFinding` event, and journal exhaustiveness guard

**Foundation for both tiers.** Defines the durable findings record and the
`SecurityFinding` ctrl bus event before any producer exists — mirroring the
transcript design. Also builds the exhaustiveness guard that the first draft assumed
existed and fixes three pre-existing silent-drop bugs (`InputRequest`, `PromptRequest`,
`AgentQuestion` have no `eventKind` case or decoder).

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/sentinel -run TestRedactionFingerprint -v` → `Finding` from a fake AWS-key match stores pattern name + redacted preview (not raw key); same fingerprint collapses duplicates, different call/rule/step is a fresh finding. `10-proofs/1.0-redaction-fingerprint.txt`.
- Test: `go test ./internal/sentinel ./internal/datastore -run TestFindingsSinkRoundtrip -v` → write three findings, read back in order; `""` path is a silent no-op. `10-proofs/1.1-sink-roundtrip.txt`.
- Test: `go test ./internal/engine -run TestSecurityFindingJournal -v` → `SecurityFinding` round-trips through the journal; the new exhaustiveness test fails if any `Event` union member lacks a kind or decoder; `InputRequest`, `PromptRequest`, `AgentQuestion` now round-trip. `10-proofs/1.2-event-exhaustiveness.txt`.

#### 2.0 Tasks

- [x] 2.1 Create `internal/sentinel/finding.go` with the `Finding` struct (fields: `Ts`, `RunID`, `StepID`, `Iteration`, `Tier`, `Monitor`, `Severity`, `Action`, `Detail`, `Evidence`, `Fingerprint`). Define `Tier`, `Severity`, and `Action` as typed string constants (`"guard"/"monitor"`, `"low/medium/high/critical"`, `"observed/blocked/escalated"`).
- [x] 2.2 Add `Redact(patternName, rawMatch string) string` helper to `internal/sentinel/finding.go` that returns `"<patternName>:…" + last4` and never stores the raw value. Add `NewFingerprint(stepID, monitor, evidenceKey string) string` using `crypto/sha256`.
- [x] 2.3 Create `internal/sentinel/sink.go` with an append-only JSONL `Writer` (flush-per-Append matching `transcript.Writer`) and a `Reader` (open→read-to-EOF→close). Both no-op silently when the path is `""`.
- [x] 2.4 Add `FindingsPath(runDir string) string` to `internal/datastore/datastore.go`, returning `filepath.Join(runDir, "findings.jsonl")`. Mirror the pattern of `TranscriptPath`.
- [x] 2.5 Add `SecurityFinding` event struct to `internal/engine/event.go` with fields: `RunID`, `StepID`, `Tier`, `Monitor`, `Severity`, `Action`, `Fingerprint`. Add the `isEvent()` marker method.
- [x] 2.6 In `internal/engine/journal.go`, add a `"security_finding"` case to `eventKind` and a decoder entry in `decoders` for `SecurityFinding`. Also add the three missing cases: `InputRequest → "input_request"`, `PromptRequest → "prompt_request"`, `AgentQuestion → "agent_question"` to both `eventKind` and `decoders`.
- [x] 2.7 In `internal/engine/journal_test.go`, write `TestEventExhaustiveness` that uses reflection or a hand-enumerated list of all `Event` union members, calls `eventKind` on each, and asserts: (a) the kind is a non-empty non-`"unknown"` string, and (b) the kind has a decoder entry in `decoders`. This test must fail if any event is missing from either map.
- [x] 2.8 Write `TestRedactionFingerprint` in `internal/sentinel/finding_test.go`, `TestFindingsSinkRoundtrip` in `internal/sentinel/sink_test.go`, and `TestSecurityFindingJournal` in `internal/engine/journal_test.go`. Capture outputs to proof files.

---

### [ ] 3.0 Unit 2 — Tier-1 deterministic tool-use firewall and transcript redaction

**The hard prevention boundary.** A synchronous, LLM-free `Guard.Check` inside
`AgentExecutor` blocks dangerous tool calls before execution and feeds the denial
reason back to the agent. A redaction filter at the transcript-write path scrubs
secrets from `transcript.jsonl` even for denied calls (closing the leak surface the
first draft left open). Requires a runtime probe to determine the correct SDK
interception seam under `acceptEdits`.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/sentinel -run TestGuardRules -v` → table-driven `Guard.Check`: `Edit` writing PEM key → deny; `Edit` writing Go code → allow; `WebFetch` to non-allowlisted host → deny; `Bash go test` → allow; `rm -rf /` → escalate. `10-proofs/2.0-guard-rules.txt`.
- Test: `go test ./internal/runner -run TestBlockedAndRedacted -v` → scripted SDK channel; agent attempts a secret-write; call is denied, agent receives reason, `blocked` finding is produced, transcript entry is redacted (no raw key on disk). `10-proofs/2.1-blocked-and-redacted.txt`.
- Probe artifact: short recorded result of the `acceptEdits` seam probe and which seam (`WithCanUseTool` or forced `default` mode) was chosen. `10-proofs/2.2-seam-probe.md`.
- Demo: a throwaway workflow whose implement step is prompted to write a fake key; run it; show the block in `findings.jsonl` and the redacted `transcript.jsonl`. `10-proofs/2.3-demo.md`.

#### 3.0 Tasks

- [ ] 3.1 Create `internal/sentinel/guard.go` with a `Decision` type (fields: `Allow bool`, `Action string`, `Reason string`) and a `Guard` struct holding the ruleset and outbound allowlist. The `Check(toolName string, input map[string]any) Decision` method must be stateless and safe for concurrent callers.
- [ ] 3.2 Create `internal/sentinel/rules.go` implementing the starter ruleset in `Guard.Check`: (a) secret-in-write rule — match `Write`/`Edit`/`Bash` heredoc payloads against AWS `AKIA…`, GCP/GitHub tokens, PEM `-----BEGIN … PRIVATE KEY-----`, and high-entropy strings (entropy threshold ≥ 4.5 bits/char over ≥16 chars) → `deny`; (b) non-allowlisted outbound host — `WebFetch`/`Bash` curl/wget to a host not in the allowlist → `deny`; (c) denied shell pattern — `rm -rf` outside worktree, `chmod 777`, raw curl/wget → `escalate`. Each rule emits a `Finding` via `Redact`.
- [ ] 3.3 Write a short standalone probe (`internal/sentinel/seam_probe_test.go` or a `TestMain` helper) that runs `AgentExecutor.Execute` with `permission_mode = "acceptEdits"` against a scripted channel and checks whether the `WithCanUseTool` callback fires for an `Edit` tool call. Record the result in `10-proofs/2.2-seam-probe.md`. If the callback does not fire, update `buildOptions` in `internal/runner/agent.go` to force `permission_mode = "default"` when the guard is active.
- [ ] 3.4 In `AgentExecutor.Execute` (`internal/runner/agent.go`), register the guard via `claudecode.WithCanUseTool(callback)` when a non-nil `Guard` is provided in the request (extend `engine.StepRequest` if needed, or pass via a runner option). The callback must produce a `Finding` and emit a `SecurityFinding` ctrl event via `rep` on deny/escalate; return `NewPermissionResultDeny(reason)` to block the tool.
- [ ] 3.5 In `captureStream` (`internal/runner/agent.go`), before appending any `AssistantMessage` block that contains a `tool_use` input (and before appending any `UserMessage` / command output), run the block's text through `guard.Redact`-style scanning and substitute redacted previews. This must only activate when the guard is non-nil; with no guard the transcript is byte-identical to today.
- [ ] 3.6 Wire the deny/escalate `SecurityFinding` ctrl event through `rep.ev` or a new `Reporter.Finding` method so it rides the `ctrl` (not `live`) channel. Produce a `Finding` with `Tier: "guard"`, `Action: "blocked"` for deny and `Action: "escalated"` for escalate, using `Redact` to strip raw secrets from the evidence.
- [ ] 3.7 Write `TestGuardRules` in `internal/sentinel/guard_test.go` (table-driven). Write `TestBlockedAndRedacted` in `internal/runner/agent_test.go` using a scripted SDK channel. Build the demo artifact. Capture proof outputs.

---

### [ ] 4.0 Unit 3 — Detection supervisor (Tier-2 fleet harness)

**Out-of-band fleet infrastructure.** A bus subscriber (mirroring the TUI) that
consumes `StepMessage` liveness signals, reads dual-bounded transcript windows off
disk, batches/debounces per step, deduplicates findings by fingerprint, and runs
tools-off Haiku classifier agents via `runner.AgentExecutor` with a persistent
client. Enforces a per-run dollar budget; degrades to Tier-1-only on exhaustion
without blocking the observed run.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/sentinel -run TestSupervisorBatching -v` → fake bus + seeded transcript; stub monitor flags on a marker string; exactly one deduped finding; burst of signals collapses to bounded monitor calls; oversized `tool_result` is truncated to window bounds. `10-proofs/3.0-supervisor.txt`.
- Test: `go test ./internal/sentinel -run TestSupervisorBudgetDegrade -v` → summed monitor cost ≥ per-run ceiling stops LLM dispatch and emits the `degraded-to-tier1` finding without blocking. `10-proofs/3.1-budget-degrade.txt`.

#### 4.0 Tasks

- [ ] 4.1 Create `internal/sentinel/supervisor.go` with a `Supervisor` struct. The constructor accepts `(live, ctrl <-chan engine.Event)` channels (from `Manager.Subscribe()`), a findings sink, a set of monitor definitions, and a per-run dollar budget. It must not start when persistence is off or security is disabled.
- [ ] 4.2 Implement the `StepMessage` consumption loop: on each signal, fetch newly-appended entries for that step via `transcript.Reader.Window`/`Tail` from the last-seen cursor position. Maintain a per-step cursor map (last seq classified) so reads advance rather than re-scan.
- [ ] 4.3 Implement dual-bounded window assembly: given the raw entries since the cursor, truncate to `min(entryCountCap, tokenCeiling)` entries, oldest-first, where token count is estimated as `len(text)/4`. The truncation must not split a single entry.
- [ ] 4.4 Implement per-step batching/debouncing: accumulate incoming `StepMessage` signals in a buffer; flush when `len(buffer) >= N` or `time.Since(lastFlush) >= T` (defaults: N=5 entries, T=500ms). After flushing, deduplicate the window's `Fingerprint` set against previously-seen fingerprints for that step; skip dispatch for already-seen fingerprints.
- [ ] 4.5 Implement monitor dispatch: for each active monitor definition, call `runner.AgentExecutor.Execute` with `persistence_off` (empty `TranscriptPath`, empty `runDir`), structured output matching the monitor's schema (`severity`, `flagged`, `detail`), and tools disabled. Hold one persistent `claudecode.Client` per supervisor (not per dispatch) to amortize connection overhead.
- [ ] 4.6 Enforce per-run dollar budget: accumulate each monitor call's `Result.TotalCostUSD` against the ceiling. When exhausted, stop dispatching to LLM monitors and append exactly one `degraded-to-tier1` `Finding` (Severity: `"low"`, Action: `"observed"`). The observed run must not be blocked or slowed.
- [ ] 4.7 Write `TestSupervisorBatching` and `TestSupervisorBudgetDegrade` in `internal/sentinel/supervisor_test.go` using a stub monitor and a fake bus. Capture proof outputs.

---

### [ ] 5.0 Unit 4 — Monitor roster (Tier-2 detectors)

**Populate the fleet.** Three tools-off, structured-output Haiku agent files under
`examples/agents/monitors/`, each prompted to treat all transcript content as
untrusted data: `prompt-injection` (watches `tool_result` blocks for hijack
patterns), `stuck-loop` (deterministic prefilter + LLM confirmation for ambiguous
cases), and `exfil-pattern` (secret/PII read followed by outbound call, requires
the entry window from Unit 3).

#### 5.0 Proof Artifact(s)

- Test: `go test ./internal/sentinel -run TestMonitorRoster -v` → supervisor stub harness; injection payload in `tool_result` → `prompt-injection` finding; repeated identical tool call → `stuck-loop` finding; secret-read-then-outbound → `exfil-pattern` finding. `10-proofs/4.0-roster.txt`.
- Fixture: the three monitor agent files, each validated to parse as an `agent_file` triple (`go run ./cmd/jig validate`). `10-proofs/4.1-monitor-files.md`.

#### 5.0 Tasks

- [ ] 5.1 In `internal/sentinel/prefilter.go`, implement `StuckLoopPrefilter(entries []transcript.Entry) bool` — returns true when the same tool name + normalized input appears ≥ 3 times in the window, or when the consecutive `IsError` count ≥ 3. Implement `ExfilPrefilter(entries []transcript.Entry) bool` — returns true when a `tool_result` block contains a secret-shaped string (reuse the guard's entropy/pattern detector) followed by a `tool_use` block whose name is `WebFetch` or a curl/wget `Bash`.
- [ ] 5.2 Write `examples/agents/monitors/prompt-injection.md` — tools-off, Haiku, structured output (`flagged bool`, `severity string`, `detail string`). The prompt must spotlight all transcript content as untrusted/potentially attacker-controlled, watch only `tool_result` blocks for injection patterns, and check whether the following assistant turn acts on injected text.
- [ ] 5.3 Write `examples/agents/monitors/stuck-loop.md` — tools-off, Haiku, same structured output schema. The prompt uses the `StuckLoopPrefilter` signal (passed as context) and confirms only ambiguous cases; it must emit `flagged=false` cheaply when the prefilter is false.
- [ ] 5.4 Write `examples/agents/monitors/exfil-pattern.md` — tools-off, Haiku, same structured output schema. The prompt requires the window to span both the secret-read entry and the outbound call entry; it must coordinate its verdict with the Tier-1 host-allowlist rule context passed from the supervisor.
- [ ] 5.5 Write `TestMonitorRoster` in `internal/sentinel/supervisor_test.go` using the supervisor stub harness, seeded transcript fixtures, and the three monitor agent files. Capture proof outputs. Produce `10-proofs/4.1-monitor-files.md` listing each file path and confirming `jig validate` accepts it as an `agent_file` triple.

---

### [ ] 6.0 Unit 5 — Security pane and human escalation

**Make findings visible and critical findings actionable.** The TUI run monitor
gains a Security region that lists findings by severity as `SecurityFinding` ctrl
events arrive (content read from `findings.jsonl`, verbatim path for redacted
previews). Critical findings escalate to the human through the existing recovery gate
(`RecoveryRequest`), rate-limited and deduplicated by fingerprint; a completed step
cannot be parked (best-effort to the nearest live decision point).

#### 6.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestSecurityPane -v` → feeding `SecurityFinding` events populates the Security pane; render golden for low/high/critical rows. `10-proofs/5.0-security-pane.txt`.
- Test: `go test ./internal/engine -run TestCriticalEscalation -v` → critical finding on running step → `StatusAwaitingRecovery` → `Run.Recover(abort)` tears down cleanly; critical finding on completed step → records only (does not attempt to resurrect); non-critical finding → no parking; duplicate fingerprints → park once. `10-proofs/5.1-escalation.txt`.

#### 6.0 Tasks

- [ ] 6.1 Add a `Security` sub-struct to `Styles` in `internal/tui/styles.go` with fields: `CriticalRow`, `HighRow`, `MediumRow`, `LowRow`, `Header` lipgloss styles. Build them in `DefaultTheme()` from the existing `danger`, `warning`, `success`, and `fgMuted` tokens — no bare hex colors.
- [ ] 6.2 Add a security findings slice (`[]engine.SecurityFinding`) to `monitorModel` in `internal/tui/monitor.go`. In the `Update` loop, handle `engine.SecurityFinding` from the ctrl channel: read the full finding detail from `findings.jsonl` (file is truth) rather than from the event fields.
- [ ] 6.3 Render the Security region in `monitorModel.View()`: a panel listing each finding as a single line (`[SEVERITY] monitor: detail`), styled by severity via `theme.Security.*`. Route redacted-secret previews through the verbatim path (not glamour). The region should be visible only when at least one finding exists.
- [ ] 6.4 In `internal/engine/engine.go`, handle critical `SecurityFinding` escalation: when a `SecurityFinding` with `Severity == "critical"` arrives, call `s.enterRecovery(stepID)` if the step is in a running/blockable state; if the step is already terminal, record the finding only; if the run is done, surface the finding with no recovery action.
- [ ] 6.5 Implement rate-limiting and fingerprint deduplication of escalation: maintain a `seenEscalations map[string]bool` keyed on `Fingerprint`. A fingerprint that has already triggered `enterRecovery` must not trigger it again for the same step.
- [ ] 6.6 Write `TestSecurityPane` in `internal/tui/monitor_test.go` and `TestCriticalEscalation` in `internal/engine/engine_test.go`. Capture proof outputs.

---

### [ ] 7.0 Unit 6 — Configuration, opt-out, and documentation

**Make the layer configurable and documented.** Hardcoded default-on config in the
engine (no file required, security is on with zero workflow config); workflows
tune/opt-out via `[defaults.security]` → `[step.security]` inheriting like
`model`/`effort`; statically validated at load time. Full docs: `workflow-schema.md`
update, new `docs/security-monitoring.md`, and an ADR recording the out-of-band +
raise-don't-kill decisions.

#### 7.0 Proof Artifact(s)

- Test: `go test ./internal/workflow -run TestSecurityConfig -v` → valid config loads and a zero-config workflow has security on (baked default); invalid ruleset/allowlist entry is rejected at load time. `10-proofs/6.0-config-validate.txt`.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0. `10-proofs/6.1-validate-ok.txt`.
- Docs: `workflow-schema.md` security section, `docs/security-monitoring.md`, and `docs/adr/NNNN-agent-security-monitoring.md`, reviewed and complete.

#### 7.0 Tasks

- [ ] 7.1 Add `SecurityConfig` struct to `internal/workflow/schema.go` with fields: `Enabled *bool`, `Tier1Enabled *bool`, `Tier2Enabled *bool`, `OutboundAllowlist []string`, `FleetBudgetUSD float64`, `ConcurrencyCap int`, `BatchSize int`, `DebounceMs int`. Add `StepSecurity` (subset of per-step overrideable fields). Add a `Security SecurityConfig` field to `Defaults` and a `Security StepSecurity` field to `Step`.
- [ ] 7.2 In `internal/workflow/load.go`, cascade `SecurityConfig` in `applyDefaults`: if a step's `Security` field is unset, inherit from `wf.Defaults.Security` (same precedence as `model`/`effort`). Define the engine-side hardcoded default-on config as a package-level constant or init var in `internal/sentinel`.
- [ ] 7.3 In `internal/workflow/validate.go`, validate `SecurityConfig`: `FleetBudgetUSD` must be ≥ 0; `ConcurrencyCap` must be ≥ 1 when set; `OutboundAllowlist` entries must be valid hostnames (simple string check). Add one `TestDecodeValid` case and at least two `TestDecodeInvalid` cases in `internal/workflow/workflow_test.go`.
- [ ] 7.4 Add a `[defaults.security]` block to `examples/feature.toml` demonstrating opt-out and per-step override. Run `go run ./cmd/jig validate examples/feature.toml` and capture the exit-0 output to `10-proofs/6.1-validate-ok.txt`.
- [ ] 7.5 Write the `docs/security-monitoring.md` document covering: two tiers and when each fires, the `findings.jsonl` record format, the redaction guarantee (findings *and* transcript), the escalation policy (best-effort, raise-don't-kill), cost accounting and fleet budget, and the monitor roster.
- [ ] 7.6 Update `docs/workflow-schema.md` with a `[defaults.security]` / `[step.security]` reference section. Write `docs/adr/NNNN-agent-security-monitoring.md` recording the out-of-band + raise-don't-kill decisions (consistent with ADR 0003 format). Capture test output to `10-proofs/6.0-config-validate.txt`.
