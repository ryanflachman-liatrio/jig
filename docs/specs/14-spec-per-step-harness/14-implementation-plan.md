# Implementation Plan: Per-Step Harness Selection

**Status:** Plan for review (no code yet)
**Depends on:** Spec 12 (`12-spec-acp-claude-harness`) — which deliberately deferred
TOML-level harness selection (non-goal §3)
**Supersedes the env-only selection path** introduced by Spec 12's `JIG_HARNESS`

---

## TUI review (current surface)

The Bubble Tea TUI never imports `internal/harness` and never reads `JIG_HARNESS`.
That is intentional from Spec 12: the run monitor is transcript-only and
backend-agnostic.

| Screen / package | Role | Harness relevance |
|---|---|---|
| `internal/tui/root.go` | Routes Selector → Detail → Runs → Monitor; owns `tea.View` | None. Constructs monitor with `engine.Manager` only. |
| `internal/tui/selector` | Workflow file picker | None. |
| `internal/tui/detail` | Workflow preview: step list + DAG chart | Shows type badge (`agent`/`command`/`review`) via `theme.Step.Types`. **Does not show which backend will run an agent step.** |
| `internal/tui/chart` | DAG layout from `Step.DependsOn` | Type-colored nodes only; no harness annotation. |
| `internal/tui/monitor` | Live run: step list + transcript + gates | Reads `transcript.jsonl` from disk. Already correct for mixed-harness runs — no change required for correctness. |
| `internal/tui/chat` | Standalone streaming chat | Still on its own path; out of scope (same as Spec 12). |
| `internal/tui/shared/styles.go` | Theme tokens | No harness style today. |

**Verdict:** Correctness of multi-harness runs does **not** require TUI changes.
The only useful UI addition is optional operator clarity on the detail step list
(and optionally the chart): show the resolved harness name on agent steps so a
workflow that mixes `claude` and `acp` is inspectable before Run.

---

## Problem

Today harness selection is process-wide:

```go
// cmd/jig/main.go
h, err := harness.FromEnv()          // JIG_HARNESS = ""|"claude"|"acp"
mux.Register(workflow.StepAgent, runner.NewAgentExecutor(h))
```

`AgentExecutor` holds a single `harness.Harness` for the whole process. Every
agent step in every workflow shares that backend. Spec 12 explicitly deferred
`harness` on `workflow.Step` / `Defaults`.

**Desired behavior:** each agent step declares its own harness in the `.toml`,
so one workflow can mix backends (e.g. `claude` for structured-output / resume
steps, `acp` for a permission-sensitive spike step).

---

## Goal (one sentence)

Add an optional `harness` field to `[defaults]` and `[[step]]` so each agent
step resolves to a named harness at load time, and `AgentExecutor` selects that
harness per `Execute` — without requiring TUI changes for correctness, and
without silently degrading when a step needs a capability the chosen harness
lacks.

---

## Non-goals

- New harness backends (Cursor, Gemini, etc.)
- Migrating Tier-2 `MonitorAdapter` or standalone `tui/chat` onto the harness seam
- Load-time capability cross-checking (schema vs harness caps) — keep fail-closed
  at `AgentExecutor.Execute` (already implemented)
- Changing transcript format or monitor rendering of tool/text blocks
- Making `harness` required on every agent step

---

## Decision map

| # | Topic | Decision |
|---|---|---|
| D1 | Where is harness declared? | `harness` string on `[defaults]` and `[[step]]`, same inheritance pattern as `model` / `effort` |
| D2 | Allowed values | `"claude"` \| `"acp"` (match today's `FromEnv` names). Unknown → `jig validate` error |
| D3 | Inheritance | step > `[defaults]` > `"claude"`. Resolved in `applyDefaults` so the executor reads a non-empty string |
| D4 | Agent-only? | Like `model`: inherited onto every step type, ignored by command/review. Do **not** put it in `hasAgentOnlyFields` (those reject command/review). Tuning-style validation covers the enum |
| D5 | `JIG_HARNESS` fate | **Deprecate as process-wide override.** TOML is source of truth. Keep `ByName` / thin `FromEnv` wrapper for tests and one-release migration: if env is set **and** neither defaults nor step set `harness`, use env as the unresolved default name. If TOML sets harness, env is ignored. Document removal in a follow-up |
| D6 | `AgentExecutor` shape | Stop holding one `Harness`. Hold a name→Harness lookup (registry or `harness.ByName`). Each `Execute` resolves `req.Step.Harness` |
| D7 | Factory API | Add `harness.ByName(name string) (Harness, error)`. Refactor `FromEnv` to `ByName(os.Getenv("JIG_HARNESS"))` |
| D8 | `main` wiring | `NewAgentExecutor` no longer takes a single harness from `FromEnv` at startup. Either pass a registry built once, or call `ByName` inside Execute. Prefer registry injected at construction so tests can inject fakes by name |
| D9 | Validate vs Execute | Unknown name → validate-time. Capability mismatch → execute-time fail-closed (existing gates unchanged) |
| D10 | TUI | Optional: detail step list shows harness marker for agent steps when resolved harness ≠ default `"claude"`, or always show it. Monitor unchanged |
| D11 | Package deps | `workflow` must **not** import `internal/harness` (would pull SDK into the schema package). Keep a string allowlist in `workflow` mirroring `ByName` |

---

## Schema (author-facing)

```toml
[defaults]
harness = "claude"          # optional; default when omitted is "claude"

[[step]]
id = "intake"
type = "agent"
harness = "claude"          # optional override
skill = "intake"
# …

[[step]]
id = "acp-spike"
type = "agent"
harness = "acp"             # different backend for this step only
skill = "…"
# note: block_on / schema / AskUserQuestion will fail closed on acp
# (AcpHarness advertises CapPermissionCallback only)
```

### Field reference (for `docs/workflow-schema.md`)

| Field | Where | Type | Notes |
|---|---|---|---|
| `harness` | `[defaults]`, `[[step]]` | string | `claude` (default) or `acp`. Per-step overrides defaults. Ignored on command/review. |

---

## Code changes (ordered)

### 1. `internal/harness` — named factory

- Add `ByName(name string) (Harness, error)` in `select.go` (or rename file).
- Keep `FromEnv()` as a one-liner wrapper for backward-compatible tests / migration.
- Tests in `select_test.go`: known names, empty → claude, unknown errors.

### 2. `internal/workflow` — schema + defaults + validate

- `Defaults.Harness string \`toml:"harness"\``
- `Step.Harness string \`toml:"harness"\``
- `applyDefaults`: if `s.Harness == ""` { use defaults; if still `""` { `"claude"` } }
  - Migration hook (optional, D5): if still empty after defaults, read nothing from env
    inside workflow — keep env handling in `cmd`/`runner` only so `workflow` stays pure.
  - Cleaner: resolve only TOML; default `"claude"`. Env migration in `main` by
    pre-filling `wf.Defaults.Harness` when empty before run (not in Load). Prefer
    **pure TOML default** — document that `JIG_HARNESS` no longer selects the
    process harness; operators set `[defaults] harness` instead.
- `checkTuning` (or sibling): invalid harness enum → validation error listing allowed values.
- Table-driven tests: valid inheritance, per-step override, unknown name, defaults-only.

**Committed design for D5 (refine):** drop process-wide injection entirely.
Unset field → `"claude"`. `JIG_HARNESS` remains parseable via `FromEnv` for
ad-hoc tooling but `cmd/jig` stops calling it for executor construction.
Update Spec 12 docs / CONTEXT.md accordingly. Fail-fast on unknown env can move
to a warning or be removed from the validate path (today `main` calls `FromEnv`
even for `jig validate` — that coupling goes away).

### 3. `internal/runner` — per-execute selection

```go
type AgentExecutor struct {
    // byName returns the harness for a resolved step.Harness name.
    // Production: harness.ByName. Tests: map lookup wrapping FakeHarness.
    byName func(name string) (harness.Harness, error)
}

func NewAgentExecutor(byName func(string) (harness.Harness, error)) *AgentExecutor

func (e *AgentExecutor) Execute(...) {
    h, err := e.byName(req.Step.Harness)
    if err != nil { return failResult(...), nil }
    caps := h.Capabilities()
    // existing fail-closed gates, using h instead of e.harness
    sess, err := h.Open(ctx, spec)
    ...
}
```

- Compatibility helper for tests: `NewAgentExecutor(func(string) (harness.Harness, error) { return fixed, nil })` or `NewAgentExecutorFixed(h)`.
- Update all `agent_test.go` construction sites.

### 4. `cmd/jig/main.go`

- Remove startup `harness.FromEnv()` before subcommand dispatch (or keep only if we
  still want env fail-fast — prefer remove so `validate` stays schema-only).
- `mux.Register(workflow.StepAgent, runner.NewAgentExecutor(harness.ByName))`

### 5. Docs + examples

- `docs/workflow-schema.md` — document `harness` under defaults and agent step fields.
- `CONTEXT.md` — note TOML selection replaces env as the operator-facing control.
- Kitchen-sink example (when present) or a small `examples/` snippet showing mixed harnesses.
- Short ADR or Spec 14 overview noting Spec 12 non-goal §3 is now in scope.

### 6. TUI (optional, small)

- `internal/tui/detail/view.go` `stepMarkers` or a new badge: for `type=agent`, append
  `harness <name>` when useful.
- Style via existing `theme.Marker` — no new color tokens required.
- One table-driven test on marker rendering if markers become harness-aware.
- **Monitor: no change.**

---

## Test plan

| Layer | What |
|---|---|
| `internal/harness` | `ByName` / `FromEnv` matrix |
| `internal/workflow` | decode + applyDefaults + validate valid/invalid; inheritance |
| `internal/runner` | `Execute` with registry of two fakes: step A opens fake-claude, step B opens fake-acp; capability fail-closed still names the **selected** harness |
| `cmd` / integration | `jig validate` accepts mixed-harness TOML; unknown harness fails validate; no longer requires valid `JIG_HARNESS` to validate |
| TUI (if D10 done) | detail markers include harness |

Commands (from CLAUDE.md):

```bash
go test ./internal/harness ./internal/workflow ./internal/runner -count=1
go test ./...
gofmt -l -w .
go vet ./...
# when an example exists:
go run ./cmd/jig validate <workflow-with-mixed-harness.toml>
```

---

## Risk

**medium–high** — schema + runner + cmd wiring; TUI optional and small. Main risk is
breaking existing operators who rely on `JIG_HARNESS=acp` without TOML changes:
mitigate with clear docs and, if needed, a one-release shim that maps env →
`Defaults.Harness` when the field is unset (call out in PR; default plan is
TOML-only with `"claude"` default).

Capability mismatch remains execute-time: an author can `validate` an `acp`
step that declares `schema` / `block_on` / `AskUserQuestion` and only fail when
the step runs. That matches today's env-selected behavior. A follow-up could
add static capability tables keyed by harness name for validate-time checks.

---

## Task list (implementation order)

| # | Title | Area | Est. (min) |
|---|---|---|---|
| 1 | Add `ByName`; keep `FromEnv` as wrapper; tests | `internal/harness` | 20 |
| 2 | Add `Harness` to `Defaults`/`Step`; `applyDefaults`; validate enum; tests | `internal/workflow` | 45 |
| 3 | Refactor `AgentExecutor` to per-step lookup; update tests | `internal/runner` | 45 |
| 4 | Wire `main` to `ByName`; stop process-wide `FromEnv` for executors | `cmd/jig` | 15 |
| 5 | Document field in `workflow-schema.md` + CONTEXT / Spec 14 note | `docs/` | 25 |
| 6 | (Optional) Detail step markers for harness | `internal/tui/detail` | 20 |
| 7 | Example TOML exercising mixed harness + `jig validate` | `examples/` | 15 |

---

## Summary

Replace process-wide `JIG_HARNESS` selection with a TOML `harness` field on
`[defaults]` and `[[step]]` (default `claude`), and make `AgentExecutor` resolve
the harness per step via `ByName`. The TUI is already correct for mixed backends;
only an optional detail-list marker improves operator visibility.
