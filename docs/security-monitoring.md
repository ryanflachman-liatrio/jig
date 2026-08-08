# Agent Security Monitoring

jig wraps every agent step in a two-tier security layer that runs
**out-of-band** — it observes the agent's tool calls and transcript without
blocking the observed run, then raises findings through the normal event bus
and recovery gate.

---

## Two tiers

### Tier 1 — Deterministic guard (`sentinel.Guard`)

A synchronous, LLM-free gate that runs **before** each tool call executes.
Implemented as a `claudecode.WithCanUseTool` callback; the agent never sees
the raw result of a blocked call — it receives a structured deny reason
instead.

| Rule | Action |
|------|--------|
| Secret-pattern write (`AKIA…`, `BEGIN PRIVATE KEY`, high-entropy ≥ 4.5 bits/char over ≥ 16 chars) to `Write`/`Edit`/`Bash` | `deny` |
| Outbound `WebFetch` or curl/wget to a host not in `outbound_allowlist` | `deny` |
| Dangerous shell pattern (`rm -rf` outside worktree, `chmod 777`, raw curl/wget) | `escalate` |

`deny` blocks the call and returns a reason string to the agent.
`escalate` blocks the call and also routes a critical `SecurityFinding` to
the recovery gate so a human can decide.

**Transcript redaction.** When the guard is active, any secret-shaped string
in the transcript (including denied-call inputs) is replaced with a redacted
preview (`<pattern>:…last4`) before the entry is written to
`transcript.jsonl`. The raw value is never stored on disk.

### Tier 2 — LLM monitor fleet (`sentinel.Supervisor`)

An **out-of-band bus subscriber** that reads transcript windows as steps run
and dispatches tools-off Haiku classifier agents to detect patterns that
require semantic judgment.

The supervisor:

1. Subscribes to `StepMessage` liveness events from the engine bus.
2. On each signal, reads newly-appended transcript entries for that step via
   a per-step cursor (advancing, not re-scanning from the start).
3. Assembles a dual-bounded window: at most `batch_size` entries, estimated
   at most `token_ceiling` tokens.
4. Batches/debounces signals (default: 5 entries or 500 ms, whichever comes
   first), then deduplicates by `Fingerprint` before dispatching.
5. Calls a monitor agent with `persistence_off` (no transcript written for
   the monitor run itself) and structured output (`flagged bool`, `severity
   string`, `detail string`).
6. Accumulates each monitor call's `TotalCostUSD` against the
   `fleet_budget_usd` ceiling. When exhausted, Tier-2 dispatch stops and
   exactly one `degraded-to-tier1` finding is recorded.

The observed run is **never blocked or slowed**: monitor dispatch is
asynchronous and its budget exhaustion degrades gracefully.

---

## Monitor roster

Three tools-off, Haiku classifier agents live under
`examples/agents/monitors/`:

| Monitor | What it watches | Prefilter |
|---------|-----------------|-----------|
| `prompt-injection.md` | `tool_result` blocks for injected commands | none (always dispatched) |
| `stuck-loop.md` | Repeated identical tool calls or error streaks | `StuckLoopPrefilter`: ≥ 3 repeated calls or ≥ 3 consecutive errors |
| `exfil-pattern.md` | Secret-read entry followed by an outbound `WebFetch`/curl | `ExfilPrefilter`: reuses guard entropy/pattern detector |

Each monitor emits structured output (`flagged`, `severity`, `detail`). The
supervisor converts a `flagged = true` response into a `Finding` and appends
it to `findings.jsonl`.

---

## Findings format

All findings are written to `.jig/runs/<run-id>/findings.jsonl` as
newline-delimited JSON, one finding per line:

```json
{
  "ts":          "2026-08-07T12:34:56.789Z",
  "run_id":      "run-abc123",
  "step_id":     "implement",
  "iteration":   1,
  "tier":        "guard",
  "monitor":     "secret-in-write",
  "severity":    "high",
  "action":      "blocked",
  "detail":      "Edit contained a secret matching pattern aws-access-key",
  "evidence":    "aws-access-key:…XYZW",
  "fingerprint": "sha256:3d4f…"
}
```

**Fields:**

| Field | Values | Notes |
|-------|--------|-------|
| `tier` | `"guard"` / `"monitor"` | Which tier produced the finding |
| `severity` | `"low"` / `"medium"` / `"high"` / `"critical"` | Guards typically emit `"high"`; monitors vary |
| `action` | `"observed"` / `"blocked"` / `"escalated"` | What the tier did with the finding |
| `evidence` | redacted preview | Raw secrets are **never** stored; only `<pattern>:…last4` |
| `fingerprint` | `sha256:…` | SHA-256 of `(stepID, monitor, evidenceKey)`; used for deduplication |

---

## Redaction guarantee

jig guarantees that neither `findings.jsonl` nor `transcript.jsonl` ever
stores a raw secret:

1. **Guard-level redaction.** Before any transcript entry containing a
   tool-use input is written, secret-shaped strings are replaced with
   `<pattern>:…last4` by `sentinel.Redact`.
2. **Finding-level redaction.** `Finding.Evidence` is always the redacted
   preview, never the raw match. The `Redact` helper that produces it never
   accepts a raw string in a return position — only in a discard position.
3. **Monitor input.** Monitor agents receive the already-redacted transcript
   window. The Tier-2 fleet never sees raw secrets.

---

## Escalation policy — raise, don't kill

Critical findings route through the **existing recovery gate** rather than
aborting the run:

- A `SecurityFinding` with `severity = "critical"` calls `enterRecovery` on
  the step's current state if it is still running or blockable.
- If the step is already terminal, the finding is recorded only — a completed
  step cannot be parked retroactively.
- Duplicate fingerprints never trigger a second recovery for the same step
  (`seenEscalations` map keyed by fingerprint).
- The human sees the finding in the Security pane and gets the same recovery
  actions as any other failure: retry, retry with guidance, or abort.

**Why raise instead of kill?** A false positive from the monitor fleet must
not abort a correct run. The human is in the loop and can dismiss a spurious
finding via "retry". See ADR `0010-agent-security-monitoring.md` for the
rationale.

---

## Cost accounting

Security monitoring adds two cost tracks:

| Track | Where recorded |
|-------|---------------|
| Per-step agent cost (`TotalCostUSD`) | `step.Result.TotalCostUSD` → `result.json` → `RunSnapshot.TotalCostUSD` |
| Tier-2 fleet monitor cost | Accumulated inside `Supervisor`; charged against `fleet_budget_usd` |

The TUI shows per-step cost in the step-detail pane and per-run total cost in
the run header.

---

## Configuration reference

Security config lives in `[defaults.security]` (workflow-wide) and
`[step.security]` (per-step override). See
[`docs/workflow-schema.md`](workflow-schema.md) for the full field reference.

```toml
[defaults.security]
enabled            = true          # set false to opt out entirely
tier1_enabled      = true          # Tier-1 guard (default on)
tier2_enabled      = true          # Tier-2 monitor fleet (default on)
outbound_allowlist = ["api.github.com"]
fleet_budget_usd   = 0.10          # per-run Tier-2 cost ceiling (0 = no limit)
concurrency_cap    = 4             # max simultaneous monitor dispatches
batch_size         = 5             # entries before forcing a flush
debounce_ms        = 500           # debounce window in ms

# Per-step override (subset of the above):
[step.security]
tier2_enabled = false              # disable Tier-2 on this step only
```

Security is **on by default** — no `[defaults.security]` block is needed.
