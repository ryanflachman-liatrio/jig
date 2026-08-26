# Plan: Codex ACP Concurrency Diagnostics

## Status

Planned. This document is intentionally implementation-ready for a fresh
agent context.

## Problem

The SDD workflow's five parallel Codex ACP research steps can leave the next
`synthesize_research` step without a durable terminal result. The same
synthesis prompt succeeds with `max_parallel = 1`, including a 28,668-byte
input and structured output, so the input shape is not the deterministic
trigger.

The current per-step `acp-diagnostics.log` records lifecycle boundaries and
adapter stderr, but it cannot distinguish local resource exhaustion from a
Codex ACP/App Server concurrency defect or an upstream concurrency limit.

## Goal

Produce privacy-safe, machine-readable, run-local resource telemetry for
Codex ACP sessions and an opt-in threshold probe. A failed run must identify
whether the parent process disappeared, the adapter exited, the prompt RPC
failed, or local resource pressure grew with concurrent sessions.

## Non-goals

- Changing the workflow schema or engine scheduling semantics.
- Persisting prompts, model responses, credentials, or tool payloads in
  diagnostics.
- Treating a temporary serial setting as the permanent solution.
- Retrying or hiding ACP failures automatically.

## Design decisions

| Topic | Decision |
| --- | --- |
| Artifact location | Keep per-step artifacts under `.jig/runs/<run>/steps/<step>/`. |
| Event format | Write structured JSON Lines lifecycle events, separate from raw adapter stderr. |
| Privacy | Record prompt byte count, not prompt text; use owner-readable files (`0600`). |
| Scope of measurement | Sample the full ACP process group, not only its `npx` parent. |
| Measurement failure | Record the sampling error in telemetry and continue running the agent. |
| Platform support | Implement macOS first; unsupported platforms write an explicit unavailable snapshot rather than failing the step. |
| Test execution | The live parallel probe is opt-in and requires the operator's existing Codex login. |

## Implementation tasks

### 1. Define a testable resource-sampling contract

**Files:** `harness/acp/metrics.go`, `harness/acp/metrics_test.go`

- Add a `ResourceSnapshot` with timestamp, root PID, process-group process
  count, aggregate RSS, open file-descriptor count, and an optional capture
  error.
- Define a small sampler interface/function used by `Conn`, so unit tests can
  provide fixed snapshots without spawning processes.
- Define JSON serialization and ensure zero/unavailable values are explicit.

### 2. Implement platform resource sampling

**Files:** `harness/acp/metrics_darwin.go`, `harness/acp/metrics_other.go`

- On macOS, inspect the ACP process group created by `configureProcess` and
  collect group membership and aggregate RSS from system process data.
- Count file descriptors without following paths or capturing process command
  arguments beyond the already-known adapter command.
- On unsupported systems, return an unavailable snapshot with a meaningful
  capture error.
- Do not add a third-party metrics dependency for this diagnostic feature.

### 3. Separate structured events from adapter stderr

**Files:** `harness/acp/diagnostics.go`, `harness/acp/diagnostics_test.go`

- Replace mixed text diagnostics with `acp-diagnostics.jsonl` for lifecycle
  events and `acp-adapter.stderr.log` for raw adapter stderr.
- Preserve the bounded stderr tail used to enrich initialization failures.
- Open artifacts with `0600`; preserve persistence-off no-op behavior when no
  diagnostic path is supplied.
- Test lifecycle JSON, stderr separation, bounded tails, permissions, and
  concurrent writes.

### 4. Capture snapshots at ACP lifecycle boundaries

**File:** `harness/acp/conn.go`

- Emit lifecycle and resource snapshots at adapter start, initialization, new
  session, configuration completion, prompt start, prompt completion, prompt
  failure, close request, and process exit.
- Include PID, process group, prompt byte count, stop reason, and command exit
  status where available.
- Do not log session prompts or structured output.
- Ensure a missing final event is meaningful: the final durable snapshot should
  identify the last observed lifecycle boundary before the host disappeared.

### 5. Keep runner integration persistence-safe

**Files:** `internal/harness/capability.go`, `internal/harness/codex.go`,
`internal/runner/agent.go`

- Evolve `SessionSpec.DiagnosticsPath` into the artifact-path contract needed
  by the split diagnostics files, or add a narrowly named diagnostics directory
  field if that is clearer.
- Derive paths only when `TranscriptPath` is non-empty.
- Leave Claude, Cursor, and persistence-off execution unchanged.

### 6. Add an opt-in Codex ACP concurrency threshold probe

**File:** `harness/acp/codex_parallel_integration_test.go`

- Gate execution behind `JIG_CODEX_ACP_INTEGRATION=1`.
- Read `JIG_CODEX_ACP_PARALLELISM` (default 1) and launch that many
  research-like, structured-output sessions concurrently.
- Wait for every session to return a terminal result and close before launching
  one synthesis-shaped, structured-output turn.
- Retain the probe diagnostics directory on failure and print only its path;
  never print prompts or raw stderr to test output.
- Run the probe manually at parallelism 2, 3, 4, and 5.

### 7. Verify and interpret the threshold matrix

**Files:** `.agents/jig/sdd.toml`, run artifacts under `.jig/runs/`

- Keep the workflow at `max_parallel = 1` while gathering diagnostics.
- Run the opt-in probe at 2, 3, 4, and 5 sessions, then run the real workflow
  at the first failing level and at one level below it.
- If a five-way run fails, compare peak process-group RSS/FD counts and the
  final lifecycle event with the successful serial baseline.
- Test a temporary 30–60 second command gate between fan-out and synthesis only
  after finding a failure threshold. A delay that removes the failure suggests
  a service rate-window or shared-service cooldown; a persistent threshold
  suggests an adapter or local resource defect.

## Acceptance criteria

- Every persisted Codex ACP step has structured lifecycle telemetry and a
  separate stderr artifact.
- Telemetry never contains prompt text, model output, credentials, or tool
  inputs.
- A failed step can be classified as adapter failure, parent-process loss, RPC
  failure, or local resource pressure from its final event sequence.
- The opt-in probe identifies the lowest failing concurrency level, if one
  exists.
- `go test ./...`, `go vet ./...`, and `go test ./...` in `harness/acp/` pass.

## Risk

Medium. The change is isolated to ACP subprocess diagnostics and does not
alter workflow schema, engine dispatch, or TUI behavior, but process metrics
are platform-specific and the live probe consumes authenticated model turns.
