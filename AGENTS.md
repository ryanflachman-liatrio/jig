# Working on jig

Cross-tool instructions for coding agents in this repository. jig is a Go
CLI/TUI that coordinates agents, commands, deterministic checks, and human
reviews through validated TOML workflow graphs.

## Start here

Read the relevant implementation and tests before changing behavior. The code
and executable tests establish what ships; the schema documents the authoring
contract. If they disagree, identify and reconcile the discrepancy within the
task. Specs, plans, proofs, and ADRs explain intent and history; their presence
does not prove that a feature exists or that an old design still applies.

| When working on | Read |
|---|---|
| Package boundaries or runtime wiring | [Architecture](docs/ARCHITECTURE.md) |
| Go implementation or refactoring | [Go conventions](docs/CONVENTIONS.md) |
| Workflow syntax, defaults, paths, or validation | [Workflow schema](docs/workflow-schema.md) and `internal/workflow` |
| Scheduling, graph changes, retries, or recovery | [Graph and workflow engineering](docs/GRAPH_ENGINEERING.md) |
| Terminal interaction, layout, or rendering | [TUI engineering](docs/TUI.md) |
| Any behavior change or verification | [Testing](docs/TESTING.md) |
| Domain naming | [Vocabulary](CONTEXT.md) |
| CLI automation or operational controls | [Headless contract](docs/headless.md), [operations](docs/operations.md) |
| Security, export, or telemetry | [Security](docs/security-monitoring.md), [observability](docs/observability.md), `internal/runexport` |
| Rust workflow crate | [Rust guidance](workflow-rs/Claude.md); this crate is separate from the Go runtime |

Use workflow skills when the task calls for them; changing code does not by
itself require running the SDD pipeline. `.agents/jig` contains executable
workflows; `.agents/skills` and `.claude/skills` contain agent instructions.
Keep skill input/output contracts aligned with their invoking TOML when edited.

## Non-negotiable design constraints

- **Pre-v1:** prefer the correct design. When replacing a mechanism, remove the
  obsolete path and update callers, tests, examples, and docs together. Do not
  add compatibility wrappers or environment aliases speculatively.
- **Explicit orchestration:** dependencies, typed outputs, conditions, bounded
  routes, and retry policies belong in the schema and engine. Prompts and TUI
  code must not become a second scheduler.
- **Single owner of run state:** workers report through engine interfaces;
  callers use `Run` commands and snapshots. Preserve journal-before-publication
  and the distinction between reliable control and lossy live signals.
- **File is truth, bus is liveness:** finalized step content is read from
  `transcript.jsonl`. Live deltas are previews, never authoritative artifacts.
- **Persistence-off is supported:** empty run/artifact/transcript paths disable
  optional persistence. Do not join an empty root into an unintended relative
  write. Required durable operations must fail explicitly when unavailable.
- **Backend-agnostic Monitor:** normalize vendor events in harness/runner code.
  Use `internal/tui/shared` for theme, panels, input, and help primitives.
- **Bounded work:** preserve concurrency, retry, route, fan-out, and content
  limits. Bounded graph loops do not guarantee wall-clock completion: human
  gates and steps without deadlines can wait indefinitely.
- **Sensitive local state:** `.jig/` can contain raw prompts, tool output,
  reasoning, and credentials. Use synthetic fixtures; never copy real run data
  or `.env` contents into tests, docs, logs, or proof artifacts.

## Backend selection

Selection is TOML-only: resolved step fields go to
`harness.For(backend, transport)`. Do not use or reintroduce `JIG_HARNESS` or
`harness.FromEnv`.

| Backend | Supported transport | Harness |
|---|---|---|
| `claude` | `sdk` | `ClaudeHarness` |
| `claude` | `acp` | `AcpHarness`, using Zed's Claude ACP adapter |
| `cursor` | `acp` | `CursorHarness`, native `cursor-agent acp` |
| `codex` | `acp` | `CodexHarness`, using `@agentclientprotocol/codex-acp` |

`backend` resolves step → `[defaults]` → `claude`. `transport` resolves
step → `[defaults]` → the backend default (`sdk` for Claude, `acp` for
Cursor/Codex). An explicitly inherited incompatible pair is rejected at load
time. Verify resolution in `internal/workflow/load.go` and supported pairs in
`internal/harness/select.go` when changing this area.

ACP is a transport, not a backend. Gemini is not implemented. The Codex ACP
adapter drives the App Server using the operator's existing login; do not
replace it with `codex exec`, an MCP server, or workflow API-key fields.
Adapter pins belong in implementation/dependency sources, not copied into
agent instructions. Required harness capabilities must fail closed.

## Commands and completion

Use the toolchain required by [go.mod](go.mod) and
[harness/acp/go.mod](harness/acp/go.mod); [mise.toml](mise.toml) selects the
Go 1.25 series. Charm imports use `charm.land/*/v2`.

```bash
go build ./cmd/jig
go run ./cmd/jig                             # Home → Monitor TUI
go run ./cmd/jig validate .agents/jig/sdd.toml
go test ./...
go vet ./...
(cd harness/acp && go test ./... && go vet ./...)
```

The nested ACP module is not covered by root `go test ./...`. Format changed
Go files with `gofmt -w <files>`, then use the change-specific checks in
[Testing](docs/TESTING.md). Avoid formatting unrelated files.

A change is complete when its intended behavior and failure paths are covered,
applicable checks pass (or blockers are reported precisely), examples and
guidance agree with the implementation, and the final diff contains only
intended changes. Report what changed, verification actually run, and remaining
limitations. Do not describe a skipped or blocked check as passing.
