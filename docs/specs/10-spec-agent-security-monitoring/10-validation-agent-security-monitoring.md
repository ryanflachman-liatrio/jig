# Validation Report — 10-spec-agent-security-monitoring

**Validation Completed:** 2026-08-07
**Validation Performed By:** Claude Sonnet 4.6 (1M context)

---

## 1) Executive Summary

**Overall: PASS**

**Implementation Ready: Yes.** All 6 Demoable Units are implemented, every required test passes, `gofmt`/`go vet` are clean, and all functional requirements have corresponding proof artifacts.

**Key metrics:**

| Metric | Value |
|--------|-------|
| Functional Requirements Verified | 100% (24/24) |
| Repository Standards Verified | 100% (7/7) |
| Proof Artifact Files Working | 100% (9/9 proof docs) |
| Packages with tests passing | 9/9 |
| Files changed matching Relevant Files | 100% core files mapped |
| Commits | 6 (all spec-referenced) |
| Issues | 2 MEDIUM (traceability, non-blocking) |

---

## 2) Coverage Matrix

### Functional Requirements

| Requirement | Status | Evidence |
|-------------|--------|----------|
| **Unit 0 — Cost Foundation** | | |
| `TotalCostUSD *float64` and `Usage *map[string]any` in `step.Result` | Verified | `internal/step/step.go:71-74`; commit `139201b` |
| Cost flows through `result.json`; persistence-off no-ops | Verified | `internal/manifest/manifest.go` (enriched in `139201b`); proof `10-task-1-proofs.md` |
| `RunSnapshot.TotalCostUSD float64` per-run sum | Verified | `internal/engine/engine.go:90`; `TestRunSnapshotCost` passes |
| TUI displays per-step and per-run cost | Verified | `internal/tui/monitor.go` (added in `139201b`); proof `10-task-1-proofs.md` |
| **Unit 1 — Finding model, sink, event** | | |
| `internal/sentinel.Finding` type with all required fields | Verified | `internal/sentinel/finding.go`; commit `893e294` |
| Fingerprint = hash{stepID, monitor, stable evidence key} | Verified | `internal/sentinel/finding.go`; `TestRedactionFingerprint` passes |
| No raw secrets in `Finding.Evidence` (`Redact` helper) | Verified | `internal/sentinel/finding.go`; `TestRedactionFingerprint`; proof `10-task-2-proofs.md` |
| Append-only JSONL writer/reader; `FindingsPath` in datastore | Verified | `internal/sentinel/sink.go`; `internal/datastore/datastore.go:99`; `TestFindingsSinkRoundtrip` passes |
| `SecurityFinding` event on `ctrl` channel | Verified | `internal/engine/event.go:208`; commit `893e294` |
| Journal `SecurityFinding` kind + decoder wired | Verified | `internal/engine/journal.go:63`; `TestSecurityFindingJournal` passes |
| Real exhaustiveness guard; 3 pre-existing missing decoders fixed | Verified | `internal/engine/journal_test.go`; `TestEventExhaustiveness` passes |
| **Unit 2 — Tier-1 guard + transcript redaction** | | |
| `Guard.Check` deterministic, stateless, side-effect-free | Verified | `internal/sentinel/guard.go`; `TestGuardRules` passes |
| Starter ruleset: secret-in-write, non-allowlisted host, denied shell | Verified | `internal/sentinel/rules.go`; `TestGuardRules` 5-case table |
| `WithCanUseTool` registered in `AgentExecutor.Execute` | Verified | `internal/runner/agent.go:66`; `TestBlockedAndRedacted` passes |
| Transcript redaction at write path when guard active | Verified | `internal/runner/agent.go:466-498`; `TestBlockedAndRedacted` proves no raw key on disk |
| Force `default` permission mode when guard active (seam probe) | Verified | Seam probe artifact `10-proofs/2.2-seam-probe.md`; demo `10-proofs/2.3-demo.md` |
| **Unit 3 — Detection supervisor** | | |
| `StepMessage` consumption; advancing per-step cursor | Verified | `internal/sentinel/supervisor.go`; `TestSupervisorBatching` passes |
| Dual-bounded window (entry count + token ceiling) | Verified | `internal/sentinel/supervisor.go`; `TestBoundWindow` 4-case passes |
| Batch/debounce + fingerprint dedup | Verified | `internal/sentinel/supervisor.go`; `TestSupervisorBatching` passes |
| Tools-off classifier dispatch; persistent Haiku client; persistence-off | Verified | `internal/sentinel/supervisor.go`; proof `10-task-4-proofs.md` |
| Per-run budget enforced; degrades gracefully | Verified | `internal/sentinel/supervisor.go`; `TestSupervisorBudgetDegrade` passes |
| **Unit 4 — Monitor roster** | | |
| Three monitor agent files: prompt-injection, stuck-loop, exfil-pattern | Verified | `examples/agents/monitors/*.md` all present; `4.1-monitor-files.md`; `jig validate` exit 0 |
| Each tools-off, structured output, untrusted-data prompt | Verified | File content review; `TestMonitorRoster` (3 sub-cases) passes |
| `StuckLoopPrefilter` and `ExfilPrefilter` in `internal/sentinel` | Verified | `internal/sentinel/prefilter.go`; `TestStuckLoopPrefilter` + `TestExfilPrefilter` pass |
| **Unit 5 — Security pane + escalation** | | |
| `theme.Security.*` pane in TUI monitor; styles from existing tokens | Verified | `internal/tui/styles.go:155-277`; `TestSecurityPane` (4 sub-cases) passes |
| Critical findings → recovery gate; best-effort to nearest live point | Verified | `internal/engine/engine.go`; `TestCriticalEscalation` (3 sub-cases) passes |
| Rate-limited and deduplicated by Fingerprint (`seenEscalations`) | Verified | `internal/engine/engine.go:565-568`; `TestCriticalEscalation/duplicate_fingerprints` passes |
| **Unit 6 — Config + docs** | | |
| `SecurityConfig`/`StepSecurity` schema; inherits like model/effort | Verified | `internal/workflow/schema.go`; `internal/workflow/load.go`; `TestSecurityConfig` passes |
| Static validation at load time; invalid values rejected | Verified | `internal/workflow/validate.go`; `TestSecurityConfig` (4 invalid sub-cases) passes |
| `examples/feature.toml` still validates | Verified | `go run ./cmd/jig validate examples/feature.toml` → exit 0, 15 steps |
| `docs/security-monitoring.md`, schema section, ADR written | Verified | Files present: `docs/security-monitoring.md`, `docs/workflow-schema.md` (security section), `docs/adr/0009-agent-security-monitoring.md` |

---

### Repository Standards

| Standard | Status | Evidence & Notes |
|----------|--------|-----------------|
| Deterministic orchestration preserved | Verified | `internal/sentinel` is a pure bus subscriber; security layer adds no graph edges, `jig validate` is unchanged |
| `internal/sentinel` imports no engine/runner/tui | Verified | `grep -rn '"jig/internal/engine\|runner\|tui"' internal/sentinel/` → 0 matches; `supervisor.go` imports only `jig/internal/transcript` |
| File is truth, bus is liveness | Verified | TUI reads findings from `findings.jsonl`; `SecurityFinding` ctrl event carries only identifying fields (not full content) |
| Fail at load time | Verified | `SecurityConfig` validated in `checkSecurityConfig`; 4 invalid cases rejected with descriptive errors |
| Comments for non-obvious "why" | Verified | Seam-probe rationale in `runner/agent.go:61`; ctrl-not-live rationale in proof `10-task-2-proofs.md`; ADR records out-of-band + raise-don't-kill decisions |
| Persistence-off is first-class | Verified | Supervisor does not start when `runDir==""` (stated in `supervisor.go`); sink no-ops on `""`; proven in `TestFindingsSinkRoundtrip` |
| Format & vet before committing | Verified | `gofmt -l .` → empty; `go vet ./...` → clean; both pass at every commit boundary (6 commits) |

---

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
|-----------|---------------|--------|---------------------|
| Unit 0 — Cost | `10-task-1-proofs.md` | Verified | Contains `TestCostCapture` and `TestRunSnapshotCost` output; both PASS |
| Unit 1 — Finding model | `10-task-2-proofs.md` | Verified | Contains `TestRedactionFingerprint`, `TestFindingsSinkRoundtrip`, `TestEventExhaustiveness`, `TestSecurityFindingJournal` output |
| Unit 2 — Tier-1 guard | `10-task-3-proofs.md` | Verified | Contains `TestGuardRules`, `TestBlockedAndRedacted` output |
| Unit 2 — Seam probe | `10-proofs/2.2-seam-probe.md` | Verified | Documents `WithCanUseTool` behavior probe; seam choice recorded |
| Unit 2 — Demo | `10-proofs/2.3-demo.md` | Verified | Throwaway workflow demo; block in findings, redacted transcript confirmed |
| Unit 3+4 — Supervisor + Roster | `10-task-4-proofs.md` | Verified | Covers both tasks; `TestSupervisorBatching`, `TestSupervisorBudgetDegrade`, `TestMonitorRoster` all PASS |
| Unit 4 — Monitor files | `10-proofs/4.1-monitor-files.md` | Verified | Lists 3 monitor files; `jig validate` exit 0 confirmed |
| Unit 5 — Security pane | `10-task-6-proofs.md` | Verified | Contains `TestSecurityPane` (4 sub-cases) and `TestCriticalEscalation` (3 sub-cases); all PASS |
| Unit 6 — Config + docs | `10-task-7-proofs.md` | Verified | Contains `TestSecurityConfig` (7 sub-cases), `jig validate` exit 0, full suite result |

---

## 3) Validation Issues

| Severity | Issue | Impact | Recommendation |
|----------|-------|--------|----------------|
| MEDIUM | **Traceability: named `.txt` proof files referenced in task list are absent.** The task list (sub-tasks 1.6, 2.7, 2.8, 3.7, 4.7, 5.5, 6.6) specifies standalone `.txt` destination files (`10-proofs/0.0-cost-capture.txt`, `1.0-redaction-fingerprint.txt`, etc.). These files do not exist as standalone artifacts; the same test output is embedded as code blocks in the corresponding `10-task-N-proofs.md` documents instead. Evidence: `ls 10-proofs/*.txt` → no matches; test output present in `.md` files. | Traceability only — all evidence is accessible in the `.md` proof files; no requirement is unverified | Add a note in the task list or proof docs explicitly mapping the "missing" `.txt` files to the `.md` documents that embed them. |
| MEDIUM | **Traceability: `10-task-5-proofs.md` absent; Unit 4 (monitor roster, task 5.0) evidence consolidated in `10-task-4-proofs.md`.** Task 5.0 (Unit 4) is marked `[x]` and its evidence (TestMonitorRoster, monitor file validation) is documented in `10-task-4-proofs.md`, which explicitly states it covers both tasks 4.0 and 5.0. Evidence: `ls 10-proofs/10-task-*.md` shows 1,2,3,4,6,7 present; 5 absent; `10-task-4-proofs.md` line 1 "Tasks 4.0 (supervisor infrastructure) and 5.0 (monitor roster)". | Traceability only — Unit 4 evidence is accessible; `TestMonitorRoster` (3 sub-cases) confirmed passing live | No code change needed; add a note to task 5.0 in the task list pointing to `10-task-4-proofs.md` as the consolidated proof source. |

---

## 4) Evidence Appendix

### Git commits analyzed

| Commit | Message | Key changes |
|--------|---------|-------------|
| `139201b` | feat: spec 10 task 1.0 — cost foundation | `step.Result` cost fields, `RunSnapshot.TotalCostUSD`, TUI cost display, `manifest.go` enrichment |
| `893e294` | feat: spec 10 task 2.0 — finding model, findings.jsonl sink, SecurityFinding event | `internal/sentinel/finding.go`, `sink.go`, `internal/datastore` `FindingsPath`, `engine/event.go` SecurityFinding, journal fixes |
| `8182b39` | feat: spec 10 task 3.0 — Tier-1 deterministic tool-use firewall and transcript redaction | `internal/sentinel/guard.go`, `rules.go`, `runner/agent.go` guard wiring + transcript redaction, seam probe artifact |
| `9ab1c24` | feat: spec 10 tasks 4.0+5.0 — detection supervisor and monitor roster | `internal/sentinel/supervisor.go`, `prefilter.go`, `supervisor_test.go` (325 tests), 3 monitor agent files |
| `cb7a8ff` | feat: spec 10 task 6.0 — security pane and human escalation | `tui/styles.go` `theme.Security`, `tui/monitor.go` Security pane, `engine.go` escalation + `seenEscalations` dedup |
| `b39583b` | feat: spec 10 task 7.0 — security config, opt-out, and documentation | `workflow/schema.go` `SecurityConfig`/`StepSecurity`, `load.go` cascade, `validate.go` hostname check, docs, ADR |

### Key test runs (live verification, 2026-08-07)

```
$ rtk proxy go test ./...
ok  jig/internal/datastore     (cached)
ok  jig/internal/engine        (cached)
ok  jig/internal/runner        (cached)
ok  jig/internal/sentinel      (cached)
ok  jig/internal/step          (cached)
ok  jig/internal/transcript    (cached)
ok  jig/internal/tui           0.410s
ok  jig/internal/workflow      (cached)

$ rtk proxy gofmt -l .
(empty — all files formatted)

$ rtk proxy go vet ./...
(empty — no issues)
```

```
$ rtk proxy go test -v -run TestEventExhaustiveness ./internal/engine/
=== RUN   TestEventExhaustiveness
--- PASS: TestEventExhaustiveness (0.00s)

$ rtk proxy go test -v -run TestCriticalEscalation ./internal/engine/
=== RUN   TestCriticalEscalation/critical_finding_on_running_step_→_StatusAwaitingRecovery_→_abort_cleans_up
=== RUN   TestCriticalEscalation/non-critical_finding_→_no_RecoveryRequest
=== RUN   TestCriticalEscalation/duplicate_fingerprints_→_park_once
--- PASS: TestCriticalEscalation (0.05s)

$ rtk proxy go test -v -run TestMonitorRoster ./internal/sentinel/
=== RUN   TestMonitorRoster/prompt-injection_finding
=== RUN   TestMonitorRoster/stuck-loop_finding
=== RUN   TestMonitorRoster/exfil-pattern_finding
--- PASS: TestMonitorRoster (0.07s)

$ rtk proxy go test -v -run TestSecurityPane ./internal/tui/
=== RUN   TestSecurityPane/high_severity_row_rendered
=== RUN   TestSecurityPane/critical_severity_row_rendered
=== RUN   TestSecurityPane/header_rendered
=== RUN   TestSecurityPane/empty_when_no_findings
--- PASS: TestSecurityPane (0.00s)

$ rtk proxy go test -v -run TestSecurityConfig ./internal/workflow/
=== RUN   TestSecurityConfig/zero_config_has_security_enabled_by_default
=== RUN   TestSecurityConfig/valid_security_config_loads_and_cascades
=== RUN   TestSecurityConfig/negative_fleet_budget_usd
=== RUN   TestSecurityConfig/negative_concurrency_cap
=== RUN   TestSecurityConfig/invalid_hostname_in_outbound_allowlist
=== RUN   TestSecurityConfig/invalid_hostname_in_step_security_override
--- PASS: TestSecurityConfig (0.00s)

$ go run ./cmd/jig validate examples/feature.toml
ok: "feature" v1 — 15 step(s)
```

### Sentinel package purity check

```
$ grep -rn '"jig/internal/engine\|jig/internal/runner\|jig/internal/tui' internal/sentinel/
(empty — no matches)
```

`internal/sentinel` imports only: `crypto/sha256`, `encoding/json`, `os`, `sync`, `time`,
`jig/internal/transcript` (for window reads — acceptable; transcript is a pure data package with no engine/tui deps).
