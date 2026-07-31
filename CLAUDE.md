# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What jig is

**jig** is a Go CLI/TUI that puts a **deterministic orchestration layer around
non-deterministic agents.** You describe a workflow as a `.toml` file — a graph
of steps — and jig handles the routing between agents, shell commands, and
human review gates, so that running a chain of agents locally is *repeatable and
inspectable* instead of a one-shot prompt.

The core idea: everything *around* an agent is deterministic (the graph, the
I/O contract, the gates, the termination guarantee); the only
non-deterministic part is what happens *inside* a single agent context, and that
is bounded by its skill instructions, its input files, and its allowed tools.

Read [`docs/workflow-schema.md`](docs/workflow-schema.md) for the full workflow
spec — it is the source of truth for what a `.toml` workflow can express.
Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for how the code is laid out
and [`docs/TESTING.md`](docs/TESTING.md) for how to test.

## Goals

- **Deterministic orchestration.** The graph, data flow, and gates are fully
  specified and statically validated. A workflow is a DAG plus a small set of
  labeled, bounded back-edges — never a free-form state machine — so we keep
  static validation, visualization, and a termination guarantee.
- **Fail at parse time, not run time.** Dangling references, cycles, type
  mismatches in guards, unbounded loops, and missing files are caught by
  `jig validate` before any agent burns a token.
- **A pleasant local TUI.** A Bubble Tea interface for driving workflows and
  reviewing agent output — a streaming chat client plus a run monitor with a
  navigable per-step transcript and human-in-the-loop review steps.
- **Idiomatic, well-factored Go.** Small `internal/` packages with clear
  responsibilities; comments explain *why*, not *what*.

## Current state (read before assuming a feature exists)

The execution engine is real and runs workflows today. Verify against the code
before assuming a feature is missing — this list drifts.

- `internal/workflow` — the schema, TOML loader, and static validator (the most
  mature package; extensive tests).
- `internal/engine` — the scheduler/executor: traverses the DAG, dispatches
  steps to workers, drives loops and review gates, and emits events. Defines the
  `Executor` and `Reporter` interfaces that `runner` implements (dependency
  inversion keeps `engine` free of `os/exec` and SDK imports).
- `internal/runner` — the concrete executors: `AgentExecutor` (Claude Agent SDK)
  and `CommandExecutor` (shell). Both capture their output to the transcript.
- `internal/transcript` — the per-step `transcript.jsonl` store (append writer +
  windowed reader): the durable record of an agent/command conversation.
- `internal/datastore` — run/step persistence under `.jig/runs/<id>/` (path
  helpers, `result.json`, transcript paths).
- `internal/step` — pure data (`Status`, `State`, `Result`); imported widely,
  imports nothing.
- `internal/manifest` — run manifest (no tests yet).
- `internal/tui` — Bubble Tea: a streaming chat client plus a navigable
  master–detail **run monitor** (step list → per-step chat chain read from the
  transcript). See `docs/run-monitor-transcript-plan.md` for the epic.
- `cmd/jig` — entry point: `jig validate <file>` and the TUI.

**File is truth, bus is liveness.** The per-step `transcript.jsonl` (written
directly by the runner) is the durable source of truth for step output. The
engine event bus carries only lightweight liveness signals (`StepMessage{Seq}`);
a dropped event just means "one seq stale," corrected on the next read. The TUI
renders from disk, never from the lossy bus. Bulk content never rides the bus or
`journal.jsonl`.

**Persistence-off is a first-class path.** When there is no run dir,
`TranscriptPath`/`ArtifactDir`/`runDir` are `""` and every writer must no-op
gracefully. This is the path most engine/runner tests exercise — keep it working
when adding capture.

## Commands

```bash
# Build
go build ./cmd/jig                 # produces ./jig

# Run the TUI (streaming Claude chat)
go run ./cmd/jig

# Validate a workflow file
go run ./cmd/jig validate examples/feature.toml

# Test (workflow, engine, runner, transcript, datastore, tui are covered)
go test ./...
go test ./internal/workflow -run TestDecodeValid -v

# Format & vet before committing
gofmt -l -w .
go vet ./...
```

Go toolchain is pinned via [`mise.toml`](mise.toml) (Go 1.24); `go.mod`
declares `go 1.24.2`.

## Conventions

- **Module path is `jig`**; internal packages import as `jig/internal/...`.
- **`internal/` packages are the unit of design.** Keep each focused; the
  workflow schema, the loader, validation, conditions, and schema-JSON handling
  each live in their own file within `internal/workflow`.
- **Comments explain the non-obvious "why."** See the terminal-background note
  in `cmd/jig/main.go` for the house style — a comment earns its place by
  explaining a subtlety a future reader would otherwise trip on.
- **Validation is exhaustive and load-time.** New schema fields must be parsed,
  defaulted (if inheritable from `[defaults]`), and validated, with a test case
  for both the valid and the invalid path. A schema addition without a
  validation rule and a test is incomplete.
- **Table-driven tests** with `testdata`-style inline TOML strings; see
  `internal/workflow/workflow_test.go`.
- **Examples are documentation.** `examples/feature.toml` is the kitchen-sink
  reference exercising every construct — keep it valid (`go run ./cmd/jig
  validate examples/feature.toml`) when changing the schema.

## Gotchas

- Detect the terminal background **before** `tea.NewProgram` takes over stdin —
  querying it later races the Bubble Tea input reader and leaks OSC responses as
  garbled keystrokes (see `cmd/jig/main.go`).
- The TUI holds a *persistent* Claude client and streams partial messages; text
  deltas arrive as `content_block_delta` StreamEvents. Thinking / input-json
  deltas are intentionally ignored.
- Artifacts and engine records live under `.jig/` (outside the working tree) so
  they survive worktree switches — see the run-directory convention in the
  schema doc.
- glamour bakes its word-wrap width in at construction — rebuild the renderer
  **and invalidate the per-block render cache** on `WindowSizeMsg`, or text wraps
  to the old width (see `monitor.go` `rebuildRenderer`).
- Only render **prose** through glamour. Non-markdown content (command output,
  tool results) gets mangled — route it through a verbatim path instead. In the
  monitor, transcript `text` blocks branch on entry role: assistant → markdown,
  system → verbatim.
- `monitorModel` uses **value receivers but shares maps** (render/expand caches):
  a map write inside a value-receiver method persists because maps are reference
  types. The caches are invalidated wholesale on width change, not mutated field
  by field.

## The .toml workflow at a glance

```toml
[workflow]        # name, version, description
[defaults]        # model/effort/limits inherited by every step
[[step]]          # one node: type = "agent" | "command" | "review"
  [step.schema]   # producer's structured-output contract
  [step.validate] # deterministic gate (exit code / schema / file checks)
  [step.loop]     # bounded back-edge (guaranteed to terminate)
```

Full field reference: [`docs/workflow-schema.md`](docs/workflow-schema.md).
