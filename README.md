# jig

A deterministic orchestration layer around non-deterministic agents.

jig lets you describe an agentic workflow as a `.toml` file — a graph of steps —
and handles the routing between agents, shell commands, and human review gates.
The result is a **repeatable, inspectable** way to run a chain of agents locally,
with a terminal UI for driving the workflow and reviewing output.

The graph, data flow, and gates are explicit; automatic repetition is bounded.
Agent output and concurrent completion order can vary. Human gates and steps
without deadlines can wait indefinitely, so graph bounds alone do not promise
wall-clock completion.

## Status

Early, but the core loop runs end to end. What works today:

- **Workflow schema + validator** (`internal/workflow`) — write a `.toml`
  workflow and `jig validate` catches dangling references, cycles, type
  mismatches in guards, unbounded loops, and missing files *before* anything
  runs.
- **Execution engine** (`internal/engine` + `internal/runner`) — traverses the
  DAG, dispatches agent, command, and check steps, drives bounded loops and
  human-in-the-loop review gates, and assembles a deterministic step-context
  preamble for each agent.
- **Home + run-monitor TUI** (`internal/tui`) — a Bubble Tea interface for
  workflows and runs, with a backend-agnostic transcript and document review.
- **Workflow composition and recovery** — subworkflow modules, dynamic fan-out,
  typed checks, retry/resource policies, run snapshots, and crash reopen.
  Claude SDK/ACP, Cursor ACP, and Codex ACP are supported.

## Install & run

Requires the Go patch version declared in [`go.mod`](go.mod);
[`mise.toml`](mise.toml) selects the Go 1.25 series.

```bash
go build ./cmd/jig            # build ./jig

jig init                                        # create a workflow in an empty repository
go run ./cmd/jig                                  # launch the TUI
go run ./cmd/jig validate .agents/jig/feature.toml   # validate a workflow
go run ./cmd/jig notifications check examples/notifications-profiled.toml # local readiness, no sends
```

## A workflow, briefly

```toml
[workflow]
name    = "bugfix"
version = "1"

[defaults]
permission_mode = "acceptEdits"

[[step]]
id            = "fix"
type          = "agent"
skill         = "../skills/implement"              # relative to .agents/jig/
allowed_tools = ["Read", "Edit", "Write", "Bash"]   # mutating -> runs in a git worktree

  [step.validate]
  command = "go test ./..."                         # deterministic gate

[[step]]
id          = "approve"
type        = "review"                              # human-in-the-loop
depends_on  = ["fix"]
output_type = { enum = ["approve", "revise"] }
[[step.review]]
source      = "diff"
label       = "Code changes"

[[step.route]]
when           = "approve == 'revise'"            # bounded back-edge
goto           = "fix"
max_iterations = 3
```

Five step types — `agent`, `command`, `check`, `review`, `subworkflow` — wired
into a DAG by `depends_on`, with typed guards (`when`), schema-enforced producer output,
deterministic gates, and bounded loops. See
[`.agents/jig/feature.toml`](.agents/jig/feature.toml) for a feature pipeline and
[`.agents/jig/sdd.toml`](.agents/jig/sdd.toml) for module-based orchestration.

## Sharing a run

`jig export RUN_ID --destination ./run-report.zip` packages one inactive run
into a self-contained, offline ZIP — structural diagnostics by default,
sanitized conversation text with explicit `--include-text` opt-in. See
[`docs/operations.md`](docs/operations.md#export-a-run) for the full archive
contract, limits, and privacy notes; sanitization is best-effort and never a
guarantee of anonymity.

## Documentation

- [`docs/workflow-schema.md`](docs/workflow-schema.md) — the full workflow spec (source of truth).
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — how the code is laid out and why.
- [`docs/operations.md`](docs/operations.md) — the non-TUI CLI: status, logs, resume, reset, export.
- [`docs/TESTING.md`](docs/TESTING.md) — testing strategy and conventions.
- [`docs/CONVENTIONS.md`](docs/CONVENTIONS.md) — Go design and concurrency guidance.
- [`docs/TUI.md`](docs/TUI.md) — Charm v2 interaction and rendering guidance.
- [`docs/GRAPH_ENGINEERING.md`](docs/GRAPH_ENGINEERING.md) — graph, retry, and recovery invariants.
- [`docs/clipboard.md`](docs/clipboard.md) — TUI clipboard (`y` / `Y`) mapping, byte limits, and terminal prerequisites.
- [`AGENTS.md`](AGENTS.md) — cross-tool orientation for AI coding assistants
  (backend selection, pre-v1 policy).
- [`CLAUDE.md`](CLAUDE.md) — Claude Code entry point to the shared guidance.
