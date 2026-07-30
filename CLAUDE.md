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
  reviewing agent output — currently a streaming chat client, growing toward a
  full run monitor with human-in-the-loop review steps.
- **Idiomatic, well-factored Go.** Small `internal/` packages with clear
  responsibilities; comments explain *why*, not *what*.

## Current state (read before assuming a feature exists)

Implemented:
- `internal/workflow` — the full schema, TOML loader, and static validator
  (this is the most mature package; it has the test suite).
- `internal/tui` — a working Bubble Tea streaming chat client against the
  Claude Agent SDK.
- `cmd/jig` — entry point: `jig validate <file>` and the bare TUI.

Placeholders (empty dirs, not yet implemented — do not assume behavior):
- `internal/engine`, `internal/runner`, `internal/step`, `internal/manifest`,
  `internal/datastore` — the execution engine that will traverse the DAG,
  manage git worktrees, run command steps, and invoke agents. **The schema
  describes runtime behavior that the engine does not yet perform.**

When asked to "run a workflow," remember the executor is not built yet; the
schema + validator + chat TUI are what exist.

## Commands

```bash
# Build
go build ./cmd/jig                 # produces ./jig

# Run the TUI (streaming Claude chat)
go run ./cmd/jig

# Validate a workflow file
go run ./cmd/jig validate examples/feature.toml

# Test (only internal/workflow has tests today)
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
- Artifacts and engine records are designed to live under `.jig/` (outside the
  working tree) so they survive worktree switches — see the run-directory
  convention in the schema doc.

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
