# Architecture

This is a navigation map of the current implementation, not an implementation
plan. Start with [AGENTS.md](../AGENTS.md); use the
[schema](workflow-schema.md) for author-facing behavior and
[graph engineering](GRAPH_ENGINEERING.md) for scheduler invariants.

## Modules and entry points

- The root Go module is `jig`; packages import `jig/internal/...`.
- `harness/acp` is a nested Go module, used through the root module's local
  `replace`. It needs its own test/vet invocation.
- `workflow-rs` is a separate Rust workflow-model crate. It is not used by
  `cmd/jig` and must not be assumed to have parity with the Go loader. Consult
  its [local guidance](../workflow-rs/Claude.md) for work there.
- `cmd/jig/main.go` dispatches `init`, `validate`, `run`, `status`, `logs`,
  `doctor`, `resume`, `reset`, `prune`, `export`, and `notifications`. With no
  subcommand it starts the Home/Monitor TUI.
- `cmd/jig/wire.go` composes the manager, runner, harness, security monitors,
  notification lifecycle/dispatcher, and optional telemetry. Headless and TUI
  paths share this composition.

Dependency versions live in the two `go.mod` files, not this map.

## Package ownership

| Package | Responsibility and boundary |
|---|---|
| `internal/workflow` | TOML loading, authoring files, profiles/defaults, module expansion, typed conditions and output contracts, graph validation. No agent execution. |
| `internal/engine` | Manager and per-run scheduler, readiness, budgets, retries/routes, fan-out, parks, review/integration, reset/reopen, journal event vocabulary. Defines `Executor` and `Reporter`. |
| `internal/runner` | Concrete agent, command, and check execution, input delivery, validation, artifact capture, transcript writes. Uses the harness seam for agents. |
| `internal/harness` | Claude SDK, Claude ACP, Cursor ACP, and Codex ACP lifecycle/capability adapters; normalizes vendor output. |
| `harness/acp` | ACP process, connection, protocol, diagnostics, and platform-specific transport support. |
| `internal/step` | Shared step status/state/result data. Keep it independent of orchestration and presentation. |
| `internal/datastore` | Run paths, snapshots, artifacts, fan-out manifests, session/review storage, retention, and file operations. |
| `internal/manifest` | Synchronous journal writer and terminal `result.json` materialization; accepts pre-encoded events without importing engine. |
| `internal/transcript` | Append-only normalized conversation records and bounded/windowed readers. |
| `internal/toolcall`, `internal/interaction` | Shared tool activity and question contracts across runner/harness/presentation. |
| `internal/review` | Immutable review documents, anchors, drafts, and submissions; separate from their TUI presentation. |
| `internal/sentinel` | Deterministic tool guard and security-monitor contracts. |
| `internal/headless`, `internal/ops` | Headless policy and operational inspection/control over the same run model. |
| `internal/notification` | Frozen run policy, operator bindings, bounded delivery, diagnostics. Delivery failure must not change run outcomes. |
| `internal/telemetry` | OTel/Prometheus adapters and executor/reporter wrappers. Engine/runner/Monitor stay free of exporter SDK imports. |
| `internal/runexport` | Bounded, sanitized export of run evidence. Raw local data is not a safe shareable artifact. |
| `internal/scaffold` | Embedded project initialization templates and scaffolding. |
| `internal/tui` | Root composition, Home navigation, overlays, event subscriptions, and child model routing. |
| `internal/tui/*` | Monitor, review, selector, runs, detail/chart, palette, chat, question, diffview, preferences, and shared presentation primitives. |
| `internal/helpchat` | Interactive help-agent model/tools; do not confuse this or standalone chat with Monitor transcript rendering. |

The engine does not import runner, harness, or Bubble Tea. It does currently
execute Git operations in `worktree.go`; “engine has no `os/exec`” is not an
accurate description. Agent/command execution still belongs behind `Executor`.
Child TUI packages must not import the root `internal/tui` package. Put shared
presentation primitives in `internal/tui/shared` without creating import cycles.

## Workflow construction and execution

`workflow.Load` reads authoring TOML and resolves paths. `decodePrepared`
rejects unknown keys, resolves skill/agent/output/review assets, applies agent
profiles, and fills defaults. Root notification policy is resolved and
subworkflow calls expand into namespaced steps. Final validation operates on
that expanded graph. The engine executes this graph; it does not start a nested
scheduler for each module. `DecodeLocked` uses captured module sources when
restoring a persisted run.

Five author-facing step kinds exist: `agent`, `command`, `check`, `review`, and
`subworkflow`. A `foreach` family remains one node in the validated graph;
runtime children live in the scheduler registry and settle through its barrier.

One scheduler goroutine owns each run's mutable state. Workers return results
and reports through messages. External callers use `Run` operations and
snapshots. The manager registry has its own synchronization; single ownership
of run state is not a claim that the entire process uses no mutexes.

## Durable truth and live observation

For orchestration events, `scheduler.emit` encodes and writes the journal
before publishing to subscribers. A journal error cancels scheduling rather
than presenting unrecorded progress. `manifest.Writer` is called synchronously;
it is not a background event subscriber. Recovery batches have their own
transaction path in `emitBatch`.

Content follows a separate path: harness → runner → `transcript.jsonl`.
`StepMessage` signals progress by sequence number. Live deltas and tool signals
may be lossy; finalized content and operational decisions must remain
recoverable. Do not move whole transcripts or review documents onto the bus.

Display replay may synthesize a useful orphaned-run view. Strict reopen must
use durable history and required snapshots, reject interior corruption, and
hold the scheduler lease. A successful UI reconstruction is not evidence that
a run is safe to resume.

Representative store layout (use datastore helpers for actual paths):

```text
.jig/
  runs/<run-id>/
    workflow.json          locked workflow and module sources
    journal.jsonl          orchestration history
    artifacts/             captured producer outputs
    steps/<step-id>/
      result.json          materialized result
      input.md             effective dispatch prompt
      session.json         durable backend conversation identity
      transcript.jsonl     finalized conversation content
  worktrees/               run-owned Git workspaces and reader views
```

Additional review, fan-out, and input snapshots belong to their datastore
owners. `.jig` normally lives under the project root and outside step worktrees;
it is not inherently outside the repository directory. Resolve run-owned
paths explicitly so a changed execution directory cannot redirect writes.

## Repository integration and review

A persisted Git run has a run branch. Mutating steps receive private worktrees
based on the integrated run state. Read-only dispatches receive separate
execution views, refreshed per dispatch; they do not enter the mutation
integration lifecycle. These are Git isolation mechanisms, not OS security
sandboxes. Persistence-off/non-Git execution uses the configured fallback CWD.

Step changes are squash-integrated into the run branch; a changed contribution
has a step-addressable commit. Final landing is human-gated. Integration
conflicts park for operator action; the optional agent resolver is explicitly
operator-invoked. Reset requires an unfinished, quiescent run, invalidates the
target and its downstream dependents, and replays independent survivor commits.
Stopping one worker, retrying an attempt, routing another iteration, resetting
a generation, and reopening a crashed run are distinct operations.

Review documents are immutable evidence. Markdown preview and diff gutters are
projections: comments and verdicts use document identity and logical source
lines, never terminal rows. `internal/tui/review` handles interaction;
`internal/review` and datastore own durable review contracts.

## Further reading

- [TUI engineering](TUI.md), [Go conventions](CONVENTIONS.md), [testing](TESTING.md).
- [Operations](operations.md), [headless execution](headless.md),
  [observability](observability.md), [security monitoring](security-monitoring.md).
- [ADRs](adr/README.md) and [engine design history](engine-design.md) explain
  earlier decisions. Verify API sketches and historical guarantees against
  the current implementation before using them.
