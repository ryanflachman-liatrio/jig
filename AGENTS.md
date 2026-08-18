# AGENTS.md

Cross-tool instructions for coding agents working in this repository
(Cursor, Codex, Gemini CLI, Claude Code via `@AGENTS.md`, etc.).

Deeper Claude-oriented notes (TUI styling, Charm v2 gotchas) live in
[`CLAUDE.md`](CLAUDE.md). Domain vocabulary is in [`CONTEXT.md`](CONTEXT.md).
Workflow schema source of truth: [`docs/workflow-schema.md`](docs/workflow-schema.md).

## What jig is

**jig** is a Go CLI/TUI that puts a **deterministic orchestration layer around
non-deterministic agents.** Workflows are `.toml` graphs of steps; jig routes
between agents, shell commands, and human review gates so local agent chains
are repeatable and inspectable.

## Pre-v1: breaking changes are fine

jig is **not at v1**. Prefer the correct long-term design over compatibility
shims. Do not preserve deprecated env vars, dual code paths, or migration
wrappers “just in case.” When replacing a mechanism, delete the old one in
the same change and update docs/examples/tests to match.

## Backend selection (TOML only — no env)

Agent backends are selected **in the workflow `.toml`**, never via
environment variables.

- **Do not use or reintroduce `JIG_HARNESS` / `harness.FromEnv`.** Selection is
  `harness.For(backend, transport)` from each step's resolved fields.
- Operators set `backend` / `transport` on `[defaults]` and/or each `[[step]]`.
  Inheritance: step → `[defaults]` → `"claude"` / `"sdk"`.
- `jig validate` rejects unknown backend/transport names at load time.

### Harness vs backend (do not conflate)

| Term | Meaning | Examples |
|---|---|---|
| **Backend** | Vendor / CLI the step talks to | `claude`, `cursor`, `codex`, `gemini` |
| **Harness** | jig’s Go adapter that speaks a transport | `ClaudeHarness` (SDK), `AcpHarness` (ACP) |
| **Transport** | Wire protocol used to reach a backend | Claude Agent SDK, ACP |

Today’s code only implements **Claude**, two ways:

- `ClaudeHarness` — direct Claude Agent SDK
- `AcpHarness` — ACP → Claude via Zed’s `npx @zed-industries/claude-code-acp`

`acp` is a **transport**, not Cursor. Cursor / Codex / Gemini are **not
implemented yet**; Spec 12 deferred them. Do not invent TOML values for them
until a real harness exists.

Planned author-facing fields (Spec 14):

```toml
[defaults]
backend   = "claude"   # vendor; default "claude"
transport = "sdk"      # "sdk" | "acp" for Claude; default "sdk"

[[step]]
id        = "spike"
type      = "agent"
backend   = "claude"
transport = "acp"      # ACP→Claude for this step only
```

When Cursor/Codex/Gemini land, they become new `backend` values (likely with
their own default transport). Do **not** add a process-wide env override for
any of them.

Plan: [`docs/specs/14-spec-per-step-harness/14-implementation-plan.md`](docs/specs/14-spec-per-step-harness/14-implementation-plan.md).

## Package map (short)

- `internal/workflow` — schema, TOML load, validate
- `internal/engine` — DAG scheduler (no SDK / harness imports)
- `internal/runner` — `AgentExecutor` / `CommandExecutor`
- `internal/harness` — `Harness` seam (`ClaudeHarness`, `AcpHarness`)
- `internal/transcript` — per-step `transcript.jsonl` (file is truth)
- `internal/tui` — Bubble Tea UI (transcript-only; backend-agnostic)
- `cmd/jig` — `validate` + TUI entry

## Commands

```bash
go build ./cmd/jig
go run ./cmd/jig
go run ./cmd/jig validate <workflow.toml>
go test ./...
gofmt -l -w .
go vet ./...
```

Go 1.25 (see `mise.toml`).

## Conventions that matter for every change

- New schema fields: parse, default from `[defaults]`, validate, test valid + invalid.
- Persistence-off is first-class: empty run dir → writers no-op.
- Comments explain non-obvious **why**, not what.
- TUI styles only via `internal/tui` theme singleton — no ad-hoc lipgloss colors.
- Keep examples valid after schema changes.
