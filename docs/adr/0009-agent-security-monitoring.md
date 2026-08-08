# Agent security monitoring runs out-of-band and raises findings rather than killing the run

Status: proposed.

jig wraps every agent step in a two-tier security layer: a synchronous
deterministic guard (Tier 1) and an asynchronous LLM monitor fleet (Tier 2).
Two decisions distinguish this design from simpler alternatives and are
recorded here.

## Decision 1 — Tier 2 runs out-of-band, never on the hot path

The LLM monitor fleet subscribes to the engine's event bus as a passive
observer. It reads the same transcript windows the TUI reads, dispatches
tools-off Haiku classifiers to flag patterns requiring semantic judgment, and
accumulates findings in `findings.jsonl`. The observed run does not wait for
monitor dispatch — a monitor call is asynchronous and its cost is charged
against a per-run `fleet_budget_usd` ceiling that, when exhausted, degrades
the fleet to Tier-1-only without interrupting any step.

### Why out-of-band

Requiring monitor verdicts before a step can proceed would add LLM latency
(and potential failures) to every step transition. The security value does not
warrant the tradeoff: most steps are benign, and a latency regression on every
step is a much larger cost than the occasional delay in surfacing a finding.

Out-of-band monitoring is the model the TUI already uses: a bus subscriber
reads transcript events and renders them without any coupling to the run's
control flow. Tier 2 follows the same pattern, so it inherits the same
resilience properties — a slow or failed monitor call drops a finding but
never stalls the run.

### Rejected alternative — synchronous Tier-2 gate

A synchronous monitor call before every tool execution would give the lowest
latency for blocking a bad action, but at the cost of injecting an LLM round
trip on the critical path of every tool call. That is a regression for all
runs, not just the ones that trigger a finding. Tier 1 (the deterministic
guard) covers the fast-path blocking case without LLM latency; Tier 2 is
reserved for patterns that require semantic reasoning and where a few seconds
of lag is acceptable.

---

## Decision 2 — Critical findings raise to the recovery gate; they do not kill the run

When a critical `SecurityFinding` arrives while a step is running, the engine
calls `enterRecovery` on that step rather than aborting the run. The step
parks in `StatusAwaitingRecovery` and the run monitor surfaces a recovery gate
with the same actions available for any other failure: retry, retry with
guidance, or abort.

Duplicate fingerprints are deduplicated: a finding that has already triggered
recovery for a step does not trigger it again.

If the step is already terminal when the finding arrives (a race the out-of-
band model makes possible), the finding is recorded in `findings.jsonl` and
shown in the Security pane, but no recovery is attempted — a completed step
cannot be parked retroactively.

### Why raise instead of kill

A finding from the LLM monitor fleet may be a false positive. Automatically
aborting the run on a false positive destroys work and teaches operators to
distrust the security layer, which leads to disabling it. The recovery gate
gives the human the evidence (the finding detail and redacted preview) and a
choice; a false positive is dismissed with "retry" in a few seconds.

The non-blocking-gate model ([ADR 0002](0002-gates-are-nonblocking-focus-regions.md))
established that jig's human-in-the-loop integration points should be
nonblocking focus regions that don't hold up the TUI. The recovery gate
honours that model: other steps keep running while the human decides.

### Rejected alternative — auto-abort on critical findings

Auto-abort eliminates the operator from the loop. For a Tier-1 deny (a
synchronous, deterministic block with no false-positive risk), the action is
warranted. For a Tier-2 LLM verdict, it is not — LLM classifiers have
non-zero false-positive rates, and the semantic patterns they watch for
(prompt injection, exfiltration sequences) are context-dependent enough that
an automated abort would cause too many unwarranted interruptions.

---

## Consequences

- **Transcript and findings are redacted at write time.** `sentinel.Redact`
  replaces secret-shaped strings with `<pattern>:…last4` before any entry
  reaches disk. Neither `transcript.jsonl` nor `findings.jsonl` ever stores a
  raw secret.
- **Tier-2 budget exhaustion is a first-class state.** The supervisor records
  a `degraded-to-tier1` finding (severity `"low"`, action `"observed"`) when
  the fleet budget is exhausted, so operators can tune `fleet_budget_usd`
  without losing visibility into degradation events.
- **Security is on by default.** No workflow change is needed to get Tier-1
  protection. Tier-2 requires a running Claude client (used by the supervisor)
  and is gated by `tier2_enabled` (default true) and `fleet_budget_usd`.
  A zero-budget workflow gets Tier-1 protection only.
