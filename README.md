# jig

A deterministic orchestration layer around non-deterministic agents.

jig lets you describe an agentic workflow as a `.toml` file — a graph of steps —
and handles the routing between agents, shell commands, and human review gates.
The result is a **repeatable, inspectable** way to run a chain of agents locally,
with a terminal UI for driving the workflow and reviewing output.

The bet: everything *around* an agent should be deterministic (the graph, the
data flow, the gates, a guaranteed termination), so the only non-deterministic
part left is what happens *inside* a single agent context — bounded by its skill
instructions, its input files, and the tools you allow it.

## Status

Early, but the core loop runs end to end. What works today:

- **Workflow schema + validator** (`internal/workflow`) — write a `.toml`
  workflow and `jig validate` catches dangling references, cycles, type
  mismatches in guards, unbounded loops, and missing files *before* anything
  runs.
- **Execution engine** (`internal/engine` + `internal/runner`) — traverses the
  DAG, dispatches agent and command steps, drives bounded loops and
  human-in-the-loop review gates, and assembles a deterministic step-context
  preamble for each agent.
- **Streaming chat + run-monitor TUI** (`internal/tui`) — a Bubble Tea interface
  that streams responses from the Claude Agent SDK and renders a navigable
  per-step transcript.

## Install & run

Requires Go 1.25 (see [`mise.toml`](mise.toml)).

```bash
go build ./cmd/jig            # build ./jig

go run ./cmd/jig                                  # launch the TUI
go run ./cmd/jig validate examples/feature.toml   # validate a workflow
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
skill         = "skills/fix"
inputs        = ["@triage"]
allowed_tools = ["Read", "Edit", "Write", "Bash"]   # mutating -> runs in a git worktree

  [step.validate]
  command = "go test ./..."                         # deterministic gate

[[step]]
id          = "approve"
type        = "review"                              # human-in-the-loop
depends_on  = ["fix"]
review      = "diff"
output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when           = "approve == 'revise'"            # bounded back-edge
  goto           = "fix"
  max_iterations = 3
```

Three step types — `agent`, `command`, `review` — wired into a DAG by
`depends_on`, with typed guards (`when`), schema-enforced producer output,
deterministic gates, and bounded loops. See
[`examples/feature.toml`](examples/feature.toml) for a kitchen-sink workflow
that exercises every construct.

## Documentation

- [`docs/workflow-schema.md`](docs/workflow-schema.md) — the full workflow spec (source of truth).
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — how the code is laid out and why.
- [`docs/TESTING.md`](docs/TESTING.md) — testing strategy and conventions.
- [`AGENTS.md`](AGENTS.md) — cross-tool orientation for AI coding assistants
  (backend selection, pre-v1 policy).
- [`CLAUDE.md`](CLAUDE.md) — Claude Code–oriented notes (imports / extends AGENTS.md).
