# Implementation Plan: Per-Step Backend Selection

**Status:** Plan for review (no code yet)
**Depends on:** Spec 12 (`12-spec-acp-claude-harness`) — which deferred TOML-level
selection (non-goal §3)
**Breaks:** Spec 12’s `JIG_HARNESS` / `harness.FromEnv` process-wide selection
(pre-v1; no compatibility shim — see [`AGENTS.md`](../../../AGENTS.md))

---

## TUI review (current surface)

The Bubble Tea TUI never imports `internal/harness` and never reads `JIG_HARNESS`.
That is intentional from Spec 12: the run monitor is transcript-only and
backend-agnostic.

| Screen / package | Role | Backend relevance |
|---|---|---|
| `internal/tui/root.go` | Routes Selector → Detail → Runs → Monitor | None |
| `internal/tui/selector` | Workflow file picker | None |
| `internal/tui/detail` | Workflow preview: step list + DAG chart | Type badge only; does not show backend/transport |
| `internal/tui/chart` | DAG from `Step.DependsOn` | Type-colored nodes only |
| `internal/tui/monitor` | Live run: transcript + gates | Reads `transcript.jsonl` — correct for mixed backends with no changes |
| `internal/tui/chat` | Standalone chat | Out of scope |
| `internal/tui/shared/styles.go` | Theme tokens | No backend style today |

**Verdict:** No TUI changes required for correctness. Optional: detail step markers
showing resolved `backend`/`transport` for operator clarity before Run.

---

## Problem

Today selection is process-wide via env:

```go
// cmd/jig/main.go — DELETE this pattern
h, err := harness.FromEnv()          // JIG_HARNESS = ""|"claude"|"acp"
mux.Register(workflow.StepAgent, runner.NewAgentExecutor(h))
```

`AgentExecutor` holds one `harness.Harness` for the whole process. Spec 12
explicitly deferred TOML selection. Operators cannot mix backends (or Claude SDK
vs ACP→Claude) within one workflow.

Also: `JIG_HARNESS=acp` names a **transport**, which confuses authors who expect
Cursor/Codex/Gemini. Vocabulary (from `CONTEXT.md` / `AGENTS.md`):

- **Backend** = vendor (`claude`, later `cursor` / `codex` / `gemini`)
- **Harness** = jig Go type (`ClaudeHarness`, `AcpHarness`)
- **Transport** = how we reach the backend (`sdk` | `acp`)

Today only **Claude** is implemented; ACP reaches Claude via Zed’s adapter, not
Cursor.

---

## Goal

Add optional `backend` and `transport` fields on `[defaults]` and `[[step]]` so
each agent step resolves its harness at load time; `AgentExecutor` selects per
`Execute`. **Remove** `JIG_HARNESS` and `FromEnv` entirely — no env fallback.

---

## Non-goals

- Implementing Cursor / Codex / Gemini backends (schema may reserve names later;
  do not accept them in validate until a harness exists)
- Migrating Tier-2 `MonitorAdapter` or standalone `tui/chat` onto the harness seam
- Load-time capability cross-checks (keep execute-time fail-closed)
- Env-based overrides or migration shims of any kind

---

## Decision map

| # | Topic | Decision |
|---|---|---|
| D1 | Author-facing fields | `backend` + `transport` on `[defaults]` and `[[step]]`, same inheritance as `model` |
| D2 | Allowed values (this slice) | `backend`: `"claude"` only. `transport`: `"sdk"` \| `"acp"`. Unknown → validate error |
| D3 | Defaults | step → `[defaults]` → `backend="claude"`, `transport="sdk"` |
| D4 | Mapping to harness | `(claude, sdk)` → `ClaudeHarness`; `(claude, acp)` → `AcpHarness` |
| D5 | `JIG_HARNESS` / `FromEnv` | **Delete.** Remove `FromEnv`, its tests, and all `cmd/jig` callers. No shim, no warning, no “if env set” branch |
| D6 | `AgentExecutor` | Lookup by resolved `(backend, transport)` (or a single factory key). Inject lookup for tests |
| D7 | Factory API | `harness.For(backend, transport string) (Harness, error)` (or equivalent). Replace `FromEnv` |
| D8 | Agent-only? | Like `model`: inherited on all step types, ignored by command/review; enum-checked in tuning-style validation |
| D9 | Validate vs Execute | Unknown backend/transport → validate-time. Capability mismatch → execute-time fail-closed |
| D10 | TUI | Optional detail markers; monitor unchanged |
| D11 | Package deps | `workflow` must not import `internal/harness`. String allowlists in `workflow` |
| D12 | Pre-v1 | Breaking change is intentional; update `AGENTS.md`, schema docs, CONTEXT, Spec 12 notes |

---

## Schema (author-facing)

```toml
[defaults]
backend   = "claude"   # optional; default "claude"
transport = "sdk"      # optional; default "sdk"

[[step]]
id        = "intake"
type      = "agent"
backend   = "claude"
transport = "sdk"
skill     = "intake"

[[step]]
id        = "acp-spike"
type      = "agent"
backend   = "claude"
transport = "acp"      # ACP→Claude; CapPermissionCallback only today
skill     = "…"
```

| Field | Where | Type | Notes |
|---|---|---|---|
| `backend` | `[defaults]`, `[[step]]` | string | `claude` (only implemented value). Future: `cursor`, `codex`, `gemini` |
| `transport` | `[defaults]`, `[[step]]` | string | `sdk` (default) or `acp`. Meaningful for Claude today |

---

## Code changes (ordered)

### 1. `internal/harness` — named factory; delete env

- Add `For(backend, transport string) (Harness, error)`.
- **Delete** `FromEnv` and `JIG_HARNESS` handling from `select.go` (rename file if useful).
- Rewrite `select_test.go` for `For` only — no env tests.

### 2. `internal/workflow` — schema + defaults + validate

- `Defaults.Backend`, `Defaults.Transport`, `Step.Backend`, `Step.Transport`.
- `applyDefaults`: inherit then fill `"claude"` / `"sdk"`.
- Validate enums; reject unknown pairs (e.g. `transport=acp` with a future
  backend that doesn’t support it once those exist).
- Table-driven tests: inheritance, per-step override, unknown values.

### 3. `internal/runner` — per-execute selection

```go
type AgentExecutor struct {
    forHarness func(backend, transport string) (harness.Harness, error)
}

func (e *AgentExecutor) Execute(...) {
    h, err := e.forHarness(req.Step.Backend, req.Step.Transport)
    // existing capability gates against h
}
```

Update all `agent_test.go` sites.

### 4. `cmd/jig/main.go`

- Remove **all** `harness.FromEnv()` usage (including the pre-subcommand fail-fast).
- `mux.Register(workflow.StepAgent, runner.NewAgentExecutor(harness.For))`
- `jig validate` must not touch harness selection at all.

### 5. Docs

- `AGENTS.md` — already states TOML-only / pre-v1 (keep in sync).
- `docs/workflow-schema.md`, `CONTEXT.md`, Spec 12 “selection via env” notes → TOML.
- Example TOML with mixed `transport` on Claude steps.

### 6. TUI (optional)

- Detail markers for `backend`/`transport` on agent steps.
- Monitor: no change.

---

## Test plan

| Layer | What |
|---|---|
| `internal/harness` | `For` matrix; **no** `FromEnv` / env tests remain |
| `internal/workflow` | decode, defaults, validate |
| `internal/runner` | two fakes: sdk step vs acp step open different harnesses |
| `cmd` | validate works with no harness env; unknown backend fails validate |
| Grep | `JIG_HARNESS` and `FromEnv` gone from tree (except historical specs/proofs) |

```bash
go test ./internal/harness ./internal/workflow ./internal/runner -count=1
go test ./...
gofmt -l -w .
go vet ./...
```

---

## Risk

**medium** — schema + runner + cmd; intentional break of `JIG_HARNESS`. Mitigate by
docs/`AGENTS.md` clarity, not by shims. Capability mismatches stay execute-time.

---

## Task list

| # | Title | Area | Est. (min) |
|---|---|---|---|
| 1 | Add `For`; delete `FromEnv` + env tests | `internal/harness` | 25 |
| 2 | Add `Backend`/`Transport` fields; defaults; validate; tests | `internal/workflow` | 45 |
| 3 | Per-step lookup in `AgentExecutor`; update tests | `internal/runner` | 45 |
| 4 | Remove env wiring from `main` | `cmd/jig` | 15 |
| 5 | Schema docs + CONTEXT; scrub live docs of `JIG_HARNESS` | `docs/`, `AGENTS.md` | 25 |
| 6 | (Optional) Detail markers | `internal/tui/detail` | 20 |
| 7 | Example TOML with mixed transport | `examples/` | 15 |

---

## Summary

Delete process-wide `JIG_HARNESS` / `FromEnv`. Select agent backends in TOML via
`backend` + `transport` (Claude + `sdk`/`acp` this slice). `AgentExecutor`
resolves per step. Pre-v1: no compatibility shim. TUI unchanged for correctness.
