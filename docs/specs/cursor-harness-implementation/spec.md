# Spec: Cursor Harness — Foundational Seam & Capability Model

**Status:** Draft for review
**Slice:** `cursor-harness-implementation`
**Depends on / supersedes:** builds the buildable first slice of `docs/specs/harness-abstraction/harness-abstraction.md`

---

## 1. Problem

Jig is bound to the pre-1.0 Claude Agent SDK (`github.com/severity1/claude-agent-sdk-go v0.6.22`, alias `claudecode`) through direct imports in `internal/runner/agent.go` and `internal/runner/monitor.go`. Adding a Cursor-based agent backend is impossible without first extracting a jig-owned seam: today `AgentExecutor.Execute` speaks SDK functional-options, an SDK client lifecycle, an SDK message stream, and SDK-shaped result fields directly.

Two hazards make this more than a mechanical extraction:

1. **Security perimeter divergence.** The Tier-1 firewall is enforced by the SDK's `WithCanUseTool` per-call permission callback, forced into effect by `WithPermissionMode(PermissionModeDefault)` (`agent.go:63-81`). A Cursor/CLI backend may have **no equivalent per-call callback**. A naive abstraction that "degrades gracefully" would silently run guarded steps with no firewall.
2. **False-capability escalation.** If backends advertise capabilities via runtime type assertion, a backend that fails the assertion is indistinguishable from one that legitimately lacks the capability — and a backend could claim a capability it does not actually honor, bypassing security controls.

## 2. Goals

- Define a jig-owned `Harness` interface covering the agent client lifecycle, in a new `internal/harness` package that is the **only** package (besides the deferred `monitor.go` / dead `tui` chat) permitted to import `claudecode`.
- Model backend variance with an **explicit capability set that is queryable before a session opens** — never via runtime type assertion after the fact.
- Extract the existing Claude SDK usage into a single concrete `ClaudeHarness` that advertises the full capability set (reference implementation).
- Enforce **fail-closed Guard**: a step carrying a configured `Guard` is rejected with a clear error when the active harness does not advertise the permission-callback capability. The per-call permission callback is a non-degradable security invariant.
- Make the harness **selectable via environment variable** (`JIG_HARNESS`), so a Cursor harness can be slotted in without a TOML schema change.
- Refactor `AgentExecutor` so `agent.go` contains **no direct `claudecode` calls** — it drives the `Harness` interface only.

## 3. Non-Goals (explicitly deferred)

These were rated out of scope for this slice during clarification and MUST NOT be built here:

- **TOML-level harness selection** (per-step or per-workflow `harness = "..."` field). Selection is env-var only this slice.
- **Transport / data-contract normalization.** SDK-shaped fields on `step.Result` (`SessionID`, `Subtype`, `TotalCostUSD`, `Usage`, `Structured`) stay as-is. The `captureStream` message-decoding logic stays SDK-typed, living **inside** the Claude harness.
- **Transcript schema versioning.**
- **Non-security composition-fragility hardening** (wrapper backends losing inner optionals, combinatorial capability test matrix beyond the conformance test below).
- Migrating `internal/runner/monitor.go` and the dead `tui` chat off the SDK — they remain on the direct SDK path.
- A production-grade Cursor transport. This slice defines the Cursor harness **contract and capability profile** and wires selection; whether a working Cursor transport lands here is gated on Cursor CLI capabilities (see §9, Assumptions).

## 4. Load-bearing decisions (resolved during clarify — each overridable)

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Capability model = explicit set queryable *before* step execution**, not runtime type assertion. `Harness.Capabilities()` returns a stable `CapabilitySet` the executor reads before dispatch. | Prevents a backend from claiming a false capability and bypassing the Tier-1 firewall; resolves the capability-model-blocks-permission-architecture circular dependency. |
| D2 | **Guard fallback = fail-closed.** A step with `Guard != nil` is rejected with a clear error when the active harness lacks `CapPermissionCallback`. Warn-and-degrade is rejected; external tool-filter proxy is deferred. | The per-call permission callback is a non-degradable security invariant. |
| D3 | **Slice scope** = core seam + capability model + fail-closed Guard + Cursor selectable via env var. Everything in §3 deferred. | Only framing scope-assessment rated "just right." |

**Timing constraint:** land before the next Claude Agent SDK minor bump (currently `v0.6.22`, pre-1.0) to limit cascade failures.

## 5. Design

### 5.1 Package layout

New package `internal/harness`:

```
internal/harness/
  harness.go     // Harness + Session interfaces, SessionSpec, Event, capability types
  capability.go  // Capability constants, CapabilitySet
  claude.go      // ClaudeHarness — sole full implementation; only file importing claudecode (this slice)
  cursor.go      // CursorHarness — capability profile + fail-closed contract (transport gated, see §9)
  select.go      // FromEnv() factory reading JIG_HARNESS
```

The `Executor` interface stays in `engine` (consumer-defined; `executor.go:13`). `engine` continues to import neither the SDK nor `harness`'s concrete backends — `AgentExecutor` in `runner` holds the `Harness`.

### 5.2 Core interface

```go
package harness

// Harness is a jig-owned agent backend. Capabilities() is queryable before any
// session is opened; the executor gates security-sensitive behaviour on it.
type Harness interface {
    Name() string                 // "claude" | "cursor"; for logging and selection
    Capabilities() CapabilitySet  // stable for the harness lifetime; queryable pre-Open
    Open(ctx context.Context, spec SessionSpec) (Session, error)
}

// Session is one agent turn/stream. Messages() yields normalized-enough events
// until the stream ends; Send injects a mid-session tool result (AskUserQuestion
// answer); Close tears down the underlying client.
type Session interface {
    Messages() <-chan Event
    Send(ctx context.Context, tr ToolResult) error
    Close() error
}
```

`Open` maps to today's `NewClient → Connect → QueryStream(sendCh) → Query` sequence (`agent.go:83-107`); `Session.Messages()` wraps `ReceiveMessages`; `Session.Send` wraps writes to `sendCh`; `Session.Close` wraps `Disconnect` + `close(sendCh)`.

> **Scope note (D3):** Because transport normalization is deferred, `Event` this slice is a thin envelope that carries the SDK message through for the Claude harness's own `captureStream` logic — the decoding switch stays inside `claude.go`. The interface shape above is chosen so a later slice can normalize `Event` without changing the `Harness`/`Session` signatures.

### 5.3 Capability model (D1)

```go
type Capability string

const (
    CapPermissionCallback Capability = "permission_callback" // per-call Tier-1 firewall
    CapInProcessMCP       Capability = "in_process_mcp"      // AskUserQuestion tool
    CapSessionResume      Capability = "session_resume"      // WithResume + continue
    CapStructuredOutput   Capability = "structured_output"   // JSON-schema output
    CapPartialStreaming   Capability = "partial_streaming"   // include_partial_messages
)

type CapabilitySet map[Capability]bool

func (c CapabilitySet) Has(cap Capability) bool { return c[cap] }
```

`Capabilities()` returns a value computed at construction and never mutated. The executor reads it **before** calling `Open`. When the executor passes a capability-gated field in `SessionSpec` (e.g. a permission function), it MUST have first confirmed the capability via `Capabilities().Has(...)`; a harness that receives a gated field it did not advertise MUST return an error from `Open` rather than silently ignore it (defensive symmetry against the escalation hazard).

### 5.4 SessionSpec — jig-owned options

Replaces the `[]claudecode.Option` slice built in `buildOptions` (`agent.go:120-168`). Fields are populated from the already-defaulted `workflow.Step`; zero values are omitted so backend defaults apply, matching current behavior.

```go
type SessionSpec struct {
    // Core (all backends)
    Prompt            string
    Model             string
    FallbackModel     string
    Effort            string
    MaxTurns          int
    MaxThinkingTokens int
    MaxBudgetUSD      float64
    PermissionMode    string
    AllowedTools      []string
    DisallowedTools   []string
    Cwd               string

    // Capability-gated (nil/zero unless the corresponding capability is advertised)
    Permission PermissionFn     // requires CapPermissionCallback
    MCPServers []MCPServer      // requires CapInProcessMCP  (AskUserQuestion)
    Resume     *ResumeSpec      // requires CapSessionResume
    Schema     map[string]any   // requires CapStructuredOutput
    Partial    bool             // requires CapPartialStreaming
}

type PermissionFn func(ctx context.Context, tool string, input map[string]any) PermissionDecision
type PermissionDecision struct { Allow bool; Reason string }

type ResumeSpec struct { SessionID, Message string }
```

`PermissionDecision` is the jig-owned mirror of `sentinel.Decision` at the harness boundary; `ClaudeHarness` translates it to `NewPermissionResultAllow()` / `NewPermissionResultDeny(reason)`.

### 5.5 Fail-closed Guard enforcement (D2)

The check lives at the `AgentExecutor.Execute` entry, before `Open`:

```go
if req.Guard != nil && !h.Capabilities().Has(harness.CapPermissionCallback) {
    return failResult(fmt.Sprintf(
        "harness %q cannot enforce the Tier-1 security guard: it lacks the "+
        "per-call permission capability; guarded steps are rejected (fail-closed)",
        h.Name()), start), nil
}
```

- `ClaudeHarness` advertises `CapPermissionCallback` → guarded steps run exactly as today, with `PermissionMode` forced to default inside the harness when a `Permission` fn is present (preserving the `agent.go:60-64` invariant that `acceptEdits` bypasses the callback).
- `CursorHarness` (if it lacks a per-call callback) omits `CapPermissionCallback` → any step with a configured Guard is rejected with the message above. Non-guarded steps run.

This makes the security boundary explicit and testable, never silently degraded.

### 5.6 Harness selection (env var)

```go
// internal/harness/select.go
func FromEnv() (Harness, error) {
    switch os.Getenv("JIG_HARNESS") {
    case "", "claude": return NewClaudeHarness(), nil
    case "cursor":     return NewCursorHarness(), nil
    default:           return nil, fmt.Errorf("unknown JIG_HARNESS %q", os.Getenv("JIG_HARNESS"))
    }
}
```

Wiring in `cmd/jig/main.go:39-42`: `runner.NewAgentExecutor()` gains a `Harness` parameter. `main` calls `harness.FromEnv()` once at startup and injects it:

```go
h, err := harness.FromEnv()      // fail fast on unknown backend
mux.Register(workflow.StepAgent, runner.NewAgentExecutor(h))
```

This is the **first** use of `os.Getenv` in production code; today all config is TOML-driven. Confined to `select.go`, keeping the rest of the codebase env-free.

### 5.7 AgentExecutor refactor

`AgentExecutor` holds a `harness.Harness`. `Execute` (`agent.go:38-114`) becomes:

1. Build `SessionSpec` from `req.Step` (moves `buildOptions` logic; produces a struct, not SDK options).
2. Gate capability fields: only set `spec.Permission` when `req.Guard != nil` (after the §5.5 fail-closed check), only set `spec.MCPServers` when `AllowedTools` contains `AskUserQuestion`, only set `spec.Resume` when `req.ResumeSessionID != ""`, etc. Each gate also confirms the harness advertises the matching capability.
3. `sess, err := h.Open(ctx, spec)`; `defer sess.Close()`.
4. Drive `sess.Messages()` through the existing capture/transcript logic. For this slice, the SDK-typed `captureStream` switch moves **into** `claude.go` behind the `Session`, returning `*step.Result` and writing the transcript via the existing `transcript.Writer` — because result/transcript normalization is deferred (§3). The executor receives the finished `*step.Result`.

The `AskUserQuestion` in-process MCP server (`buildAskUserQuestionServer`, `agent.go:194-243`) and the `mcp__jig__AskUserQuestion` name rewrite move into the Claude harness, exposed to the executor as the backend-neutral `CapInProcessMCP` + `MCPServer` slice.

### 5.8 Cursor harness

`CursorHarness.Capabilities()` returns the set the Cursor CLI can actually honor. Expected profile given research: **omits `CapPermissionCallback`** (no per-call firewall) and likely omits `CapInProcessMCP`; may support `CapSessionResume`, `CapStructuredOutput`, `CapPartialStreaming` depending on the CLI. Consequences that fall out of the design with no special-casing:

- Guarded steps → fail-closed rejection (§5.5).
- `AskUserQuestion` steps → the executor's MCP gate finds `CapInProcessMCP` absent; such steps are rejected with a clear error (same fail-closed pattern extended to the MCP gate).

## 6. Security invariants (must survive the wrap)

1. When a Guard is configured and the harness supports it, the per-call permission callback fires for every tool call under default permission mode; `acceptEdits`/`bypassPermissions` still bypass it exactly as today.
2. A harness cannot run a guarded step without advertising `CapPermissionCallback` (fail-closed).
3. A harness cannot silently ignore a capability-gated `SessionSpec` field it did not advertise — `Open` errors instead.
4. Findings/`SecurityFinding` emission on deny/escalate is unchanged (still produced during message capture).

## 7. Units of work

| Unit | Description | Primary files |
|------|-------------|---------------|
| U1 | Define `Harness`/`Session`/`SessionSpec`/capabilities (types only, no SDK) | `internal/harness/harness.go`, `capability.go` |
| U2 | `ClaudeHarness` — wrap NewClient/Connect/Query/Receive/Disconnect; move `buildOptions`, `buildAskUserQuestionServer`, name rewrite, `captureStream` behind it; advertise full set | `internal/harness/claude.go` |
| U3 | `FromEnv()` selection + `NewAgentExecutor(h)` injection + `main` wiring | `internal/harness/select.go`, `internal/runner/agent.go`, `cmd/jig/main.go` |
| U4 | Fail-closed Guard + MCP capability gates in `Execute` | `internal/runner/agent.go` |
| U5 | `CursorHarness` capability profile + fail-closed contract (transport gated — see §9) | `internal/harness/cursor.go` |
| U6 | Conformance test + guard-semantics tests + fake harness | `internal/harness/*_test.go`, `internal/runner/*_test.go` |

## 8. Testing

- **Fake harness** (mirrors the existing `runner.FakeExecutor` pattern, `fake.go:14-45`): a `harness.Fake` with a configurable `CapabilitySet` and scripted `Messages()` events, so engine/runner tests run without SDK calls.
- **Conformance test** (closes the false-capability gap): for each real harness, assert that every advertised capability is actually honored — e.g. if `CapPermissionCallback` is set, opening a session with a `Permission` fn results in the fn being invoked; if a capability is *not* set, passing the corresponding gated field makes `Open` error. This is the anti-escalation guarantee turned into a test.
- **Guard-semantics tests:** (a) guarded step + Claude harness → permission fn fires under default mode; (b) guarded step + harness without `CapPermissionCallback` → fail-closed rejection with the expected error; (c) `acceptEdits` step → callback bypassed (unchanged).
- **Behavior-parity check:** a representative agent step produces byte-identical transcript entries and identical token/cost figures before and after the refactor (the SDK-shaped `step.Result` fields are untouched by design).
- `go build ./... && go vet ./... && go test ./...` pass.

## 9. Risks, assumptions, open questions

- **Assumption (Cursor transport):** the Cursor CLI's actual capability surface is unverified here. If it lacks a per-call permission callback, guarded steps are fail-closed by design — acceptable for this slice, but it means Cursor cannot run security-enabled workflows until an external tool-filter proxy is built (deferred). **A working Cursor transport may not fully land in this slice**; U5 delivers the contract and selection even if the transport is stubbed to a clear "not yet implemented" error on `Open`.
- **Assumption:** `include_partial_messages` streaming, structured output, and resume behave identically once routed through the harness — validated by the behavior-parity check.
- **Open question:** should `FromEnv` unknown-value handling fail fast at startup (recommended, shown above) or fall back to Claude with a warning? Recommend fail-fast.
- **Composition fragility** (wrapper harnesses losing inner capabilities) and the combinatorial capability test matrix are acknowledged but out of scope (§3); the conformance test covers only concrete harnesses this slice.

## 10. Success metrics

1. `claudecode` is imported only by `internal/harness/claude.go` plus the explicitly-deferred `internal/runner/monitor.go` and dead `tui` chat. `agent.go` has zero SDK imports.
2. Behavior identical: same transcripts, same token/cost figures for a reference workflow.
3. Guard preserved and fail-closed: permission callback fires under default mode on Claude; a harness lacking the capability rejects guarded steps with a clear error.
4. Backend-neutral: `JIG_HARNESS=cursor` selects the Cursor harness through the capability model with **no change** to the core `Harness` interface.
5. No regressions: `go build/vet/test ./...` pass.
