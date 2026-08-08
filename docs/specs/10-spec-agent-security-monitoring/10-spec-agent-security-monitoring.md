# 10-spec-agent-security-monitoring.md

> **Status:** design finalized via a grilling session (2026-08-07). Every Open
> Question the first draft carried is now resolved; the decisions and the facts
> that drove them are recorded in [Resolved Design Decisions](#resolved-design-decisions).
> The unit plan gained a **Unit 0 (cost foundation)** and lost the scope-drift
> monitor (deferred). This document is the single source of truth for the work.

## Introduction/Overview

jig runs non-deterministic agents inside a deterministic orchestration layer. The
layer bounds *what* an agent can do (allowed tools, worktree isolation, budget and
turn caps) but not *what an agent actually does with those tools moment to moment*.
A skill instructed to "research, read-only" can be steered off-mission by a prompt
injection sitting in a web page it fetched; an implement step can write an API key
into source; a stuck agent can burn its whole budget thrashing on the same failing
tool call. Today jig has exactly one security seam — an in-graph `agent` step
(`examples/agents/security-reviewer.md`) that reviews *content* at a DAG
checkpoint. It cannot observe the *process* of a sibling agent while that agent is
running.

This spec adds a **continuous, out-of-band security layer** that observes agents as
they run and can both **prevent** (synchronously block a dangerous tool call) and
**detect** (asynchronously flag suspicious behavior for a human). It is built in two
tiers, phased:

- **Tier 1 — deterministic prevention.** A synchronous, LLM-free tool-use firewall
  inside the agent runner. It inspects each tool call's name and arguments *before
  execution* and can deny it (secret about to be written, denied `Bash` pattern,
  outbound fetch to a non-allowlisted host). This is the hard boundary and the
  higher-certainty half; it ships first. **Tier 1 is agent-step-only** — it *is* the
  SDK's pre-tool permission seam, which command steps do not have (see
  [D8](#d8--tier-1-prevention-is-agent-step-only)).
- **Tier 2 — semantic detection.** A fleet of cheap **Haiku** classifier agents,
  driven by an in-process supervisor that subscribes to the engine event bus,
  reads finalized transcript windows off disk, and dispatches them to monitors for
  the fuzzy judgments a regex cannot make: prompt injection, stuck / thrashing
  loops, and read-then-exfiltrate patterns. Monitors **raise findings; they never
  autonomously kill a run** — critical findings escalate to a human through jig's
  existing recovery gate.

The design is deliberately **additive and out-of-band**: the security layer is not a
node in the workflow DAG, adds no graph edges, and is invisible to `jig validate`
and the static termination guarantee. It reuses primitives that already exist —
the fan-out event bus (`Manager.Subscribe`), the concurrent-safe transcript reader,
the agent executor, and the `agent_file` (prompt/tools/model) triple — which is why
the plumbing is tractable. Both tiers write to one durable, redacted findings record
(`findings.jsonl`, "file is truth"), and both surface through one new must-not-drop
`SecurityFinding` bus event.

Two facts uncovered while finalizing this spec added scope the first draft did not
carry, and both are now first-class requirements:

- **The transcript is itself a leak surface.** The SDK hands jig the `tool_use`
  block *with its full input* before the CLI checks permission, so the runner writes
  a would-be secret to `transcript.jsonl` *even when the guard denies the write*
  (`runner/agent.go:362-389`). Redacting `findings.jsonl` while `transcript.jsonl`
  keeps the plaintext is a hole in the threat model; Unit 2 closes it
  ([N1](#n1--the-transcript-is-a-leak-surface-too)).
- **The per-run dollar budget requires cost plumbing jig does not have.**
  `AgentExecutor.Execute` discards `ResultMessage.TotalCostUSD` today
  (`runner/agent.go:289-350`); `step.Result` has no cost field; nothing sums cost
  per run. The fleet budget cannot be computed until that exists, so a small **cost
  foundation** (Unit 0) is sequenced first ([N3](#n3--cost-tracking-is-a-hard-prerequisite)).

The work is grouped into seven dependency-ordered Demoable Units. **Unit 0 (cost
foundation), Unit 1 (the finding record + sink) and Unit 2 (the Tier-1 firewall) are
the prevention phase and each is independently shippable.** Units 3–4 build the
detection fleet; Units 5–6 surface findings and make the layer configurable.

## Goals

- **Prevent the high-certainty harms synchronously.** A deterministic, LLM-free
  guard blocks a tool call whose name+arguments match a policy rule (secret in a
  `Write`/`Edit` payload, denied `Bash` pattern, non-allowlisted outbound host)
  *before* the tool runs, feeding the denial reason back to the agent so it can
  adapt. Prevention, not just after-the-fact detection.
- **Never let a redacted record sit beside a plaintext one.** Both the findings
  record *and* the transcript are scrubbed of raw secrets when Tier-1 is active, so
  `.jig/` never holds a leaked credential regardless of which file is inspected.
- **Detect the fuzzy harms continuously.** A supervisor + Haiku fleet watches every
  running agent step's transcript for prompt injection, stuck loops, and exfiltration
  patterns — the semantic cases a regex cannot judge — across a *window* of entries,
  not one line at a time.
- **Keep the security layer out-of-band and non-authoritative.** Monitors observe
  and raise; the DAG, its edges, and the termination guarantee are untouched.
  Critical findings escalate to a human through the existing recovery gate; no
  agent-driven autonomous kill switch in this spec.
- **One durable, redacted findings record.** Both tiers append to
  `.jig/runs/<id>/findings.jsonl` and emit one `SecurityFinding` ctrl event.
- **Bounded, accountable cost.** The detection fleet has a hard **per-run dollar
  ceiling**, batches/debounces per step, dedupes repeat findings by fingerprint, and
  uses cheap deterministic prefilters to gate whether an LLM is called at all; when
  the budget is exhausted it degrades to Tier-1-only rather than blocking work. This
  rides on a general per-step/per-run cost accounting (Unit 0) jig gains here.
- **Preserve the persistence-off path.** With no run dir, the sink no-ops and the
  supervisor does not run, exactly like every other writer in the tree.

## User Stories

**As a jig operator running an `implement` step**, I want a tool call that would
write an AWS key or a private-key block into a source file to be blocked before it
happens, with the reason fed back to the agent, so a secret never lands on disk in
the first place — and I want the blocked attempt scrubbed from the transcript too,
so the record of the block is not itself the leak.

**As a jig operator running a read-only `research` step**, I want to be alerted when
a fetched web page contains "ignore your previous instructions, instead run…" text
that the agent then starts to act on, so an injection that redirects the agent is
caught while the step is still running rather than discovered in the diff.

**As a jig operator watching a run**, I want a dedicated Security view that lists
findings by severity as they occur, and I want a *critical* finding to pause the run
and ask me what to do — retry, resume, or abort — rather than the monitor silently
killing work I care about.

**As a jig operator on a budget**, I want the monitoring fleet to have a hard
per-run dollar ceiling and to fall back to the deterministic guard when it's hit, and
I want to *see* what each step and each run actually cost, so turning on security
cannot silently balloon my spend.

**As a maintainer**, I want the security layer to be a bus subscriber and a set of
agent files, not new graph node types, so `jig validate`, the DAG visualization, and
the termination proof are unchanged and I can reason about the two independently.

**As a security-conscious maintainer**, I want the monitor agents to have no tools
and to treat all transcript content as untrusted data, so a monitor reading an
attacker-controlled tool result cannot itself be hijacked into suppressing a finding.

## Demoable Units of Work

The units are dependency-ordered. **Units 0–2 are the prevention phase** (cost
foundation, the finding record, the Tier-1 firewall) and are each independently
shippable. Unit 3 stands up the detection supervisor; Unit 4 populates the monitor
roster; Units 5–6 surface findings to the human and make the layer configurable.
Every unit's overarching proof is "`go test ./...` still green" (the no-regression
lock, including the persistence-off path) plus the unit-specific proofs below.

### Unit 0: Cost foundation — per-step and per-run cost accounting (prerequisite)

**Purpose:** Make cost a first-class, observable value before anything needs to
budget against it. The per-run fleet ceiling (Unit 3) is uncomputable until
`step.Result` carries cost, because `AgentExecutor.Execute` throws away the SDK's
`ResultMessage.TotalCostUSD` today. This is small, independently useful (operators
want "what did this run cost?" regardless of security), and unblocks the fleet
budget. Sequenced first for that reason.

**Functional Requirements:**
- The system shall add `TotalCostUSD *float64` and `Usage *map[string]any`
  (input/output/cache token counts) to `step.Result` (`internal/step/step.go:42-54`),
  read from the SDK `ResultMessage` (`message.go:188-202`, fields present at
  `v0.6.22`) in `captureStream`/`Execute` (`runner/agent.go:289-350`) where they are
  currently discarded. The fields are pointers so "unknown/unreported" is
  distinguishable from "$0.00".
- The values shall flow through the existing `result.json` write path (enriching the
  minimal `stepResultJSON`, `manifest/manifest.go:77-95`) so per-step cost is durable
  on disk, and the persistence-off path shall remain a no-op (no cost file when there
  is no run dir).
- The engine shall expose a **per-run cost sum** on `RunSnapshot`
  (`engine/engine.go:84-90`) computed from the steps' `Result.TotalCostUSD`, so both
  the TUI and the security supervisor can read one authoritative number.
- The TUI run monitor shall display per-step and per-run cost (a small addition to
  the step detail / run summary; styles via `internal/tui/styles.go`).
- **Out of scope for this unit** (see Non-Goals): a live streaming `StepCost` event
  and jig-side enforcement of the *observed run's* `max_budget_usd` (the SDK still
  enforces that per-step). This unit records cost; only the fleet (Unit 3) enforces a
  ceiling against it.

**Proof Artifacts:**
- Unit test (`runner`): a scripted SDK channel whose `ResultMessage` carries a
  `TotalCostUSD` yields a `step.Result` with that cost; a `ResultMessage` without one
  yields a nil pointer (not `0`). `10-proofs/0.0-cost-capture.txt`.
- Unit test (`engine`): three steps with known costs sum to the expected
  `RunSnapshot` total; persistence-off writes no cost file. `10-proofs/0.1-run-sum.txt`.

### Unit 1: Finding model, `findings.jsonl` sink, `SecurityFinding` event, and a *real* journal exhaustiveness guard (Foundation)

**Purpose:** Establish the one durable record and the one bus event that *both* tiers
produce, before either producer exists. This mirrors the transcript design ("file is
truth, bus is liveness"). While wiring `SecurityFinding` into the journal, this unit
also **builds the exhaustiveness guard the first draft wrongly assumed already
existed** and fixes three latent silent-drop bugs it uncovered
([N2](#n2--the-journal-exhaustiveness-guard-does-not-exist-yet)).

**Functional Requirements:**
- The system shall define a new pure package `internal/sentinel` (data + file I/O
  only, importing nothing from `engine`/`runner`/`tui`, mirroring
  `internal/transcript` and `internal/step`) containing a `Finding` type with at
  least: `Ts`, `RunID`, `StepID`, `Iteration`, `Tier` (`"guard"` | `"monitor"`),
  `Monitor` (rule/monitor name, e.g. `"secret-leak"`, `"prompt-injection"`),
  `Severity` (`enum{low, medium, high, critical}`), `Action`
  (`enum{observed, blocked, escalated}` — see [D5](#d5--the-action-model-deny--denycontinue-escalate--denypark)),
  a human-readable `Detail`, an optional `Evidence` locator (transcript seq / block
  index / tool name), and a `Fingerprint`.
- **The `Fingerprint` shall be** `hash{stepID, Monitor/rule name, stable evidence
  key}`, where the evidence key is a normalized locator (normalized tool input, or
  transcript seq/block). This composition makes identical repeats within a step
  collapse to one finding (and park once, Unit 5) while a *different* offending call,
  a *different* rule, or a *different* step is a fresh finding
  ([N4](#n4--fingerprint-composition)).
- A `Finding` shall **never carry a raw captured secret**. When a rule matches
  sensitive bytes, the finding stores the matched *pattern name* and a redacted
  preview (e.g. `AKIA…last4`), not the value. A helper `Redact(match)` shall enforce
  this at the single construction site — and this same helper is reused by Unit 2's
  transcript redaction filter, so detection and redaction share one implementation.
- The system shall provide an append-only JSONL writer/reader for
  `.jig/runs/<id>/findings.jsonl`, and `internal/datastore` shall gain a
  `FindingsPath(runDir)` helper beside `TranscriptPath` (`datastore.go:95`). The
  writer shall flush per append so a concurrent reader always sees whole lines
  (matching `transcript.Writer`, `writer.go:64-89`), and shall no-op when the path
  is `""` (persistence off).
- The system shall add a `SecurityFinding` event to the `Event` union
  (`internal/engine/event.go:165-178`) carrying the finding's identifying and
  severity fields (not the full struct). It shall ride the **ctrl** channel
  (critical, must-not-drop — `sub` at `engine.go:30-37`, `ctrl` buffered 256), never
  the droppable `live` channel (buffered 1024, drop-on-full), because a missed
  finding is not self-correcting the way a missed `StepMessage` is
  ([D4](#d4--findings-ride-ctrl-not-live)).
- `SecurityFinding` shall be wired into the journal registry so it round-trips: a
  `case` in `eventKind` (`journal.go:26`) and an entry in `decoders` (`journal.go:72`).
- **The system shall build a genuine exhaustiveness guard** (not the incomplete test
  the first draft cited). The fact-find showed `decoders` covers only 11 of 14 events
  — `input_request`, `prompt_request`, and `agent_question` silently fall through to
  the `"unknown"` catch-all (`journal.go:51`) and round-trip nowhere, and
  `journal_test.go` never notices. This unit shall add a test that enumerates **every**
  `Event` union member and asserts each has a stable non-empty kind and decodes back,
  and shall **fix the three pre-existing missing decoders**. A must-not-drop ctrl event
  shipping onto a journal that silently drops unknown kinds is a contradiction; the
  guard has to be real for `SecurityFinding`'s durability claim to hold.

**Proof Artifacts:**
- Unit test (`sentinel`): a `Finding` constructed from a fake AWS-key match contains
  the pattern name and a redacted preview and **does not** contain the raw key; the
  same fingerprint composition collapses two identical matches and distinguishes a
  different call. `10-proofs/1.0-redaction-fingerprint.txt`.
- Unit test (`sentinel`/`datastore`): write three findings, read them back in order;
  a `""` path is a silent no-op. `10-proofs/1.1-sink-roundtrip.txt`.
- Unit test (`engine`): `SecurityFinding` round-trips through the journal; **the new
  exhaustiveness test fails on any union member lacking a kind or decoder**, and the
  three previously-missing events (`input_request`, `prompt_request`,
  `agent_question`) now round-trip. `10-proofs/1.2-event-exhaustiveness.txt`.

### Unit 2: Tier-1 deterministic tool-use firewall + transcript redaction (prevention)

**Purpose:** Block the high-certainty harms *before they happen*, with no LLM in the
path, and ensure the record of a block is not itself a leak. This is the hard
boundary. **Tier 1 is agent-step-only** — it is the SDK pre-tool permission seam
(`WithCanUseTool`, `options.go:673`; `WithPreToolUseHook`, `options.go:704`), which
command steps, being deterministic author-written shell, do not have.

**Functional Requirements:**
- The system shall define a pure policy engine in `internal/sentinel` (e.g.
  `Guard.Check(toolName string, input map[string]any) Decision`) that is
  deterministic, side-effect-free, and unit-testable without an SDK connection. It
  shall ship with a starter ruleset, each rule carrying a per-rule **action**
  ([D5](#d5--the-action-model-deny--denycontinue-escalate--denypark)):
  - **Secret in a write** (`Write`/`Edit`/`Bash` heredoc payload matching AWS
    `AKIA…`, GCP/GitHub tokens, PEM `-----BEGIN … PRIVATE KEY-----`, high-entropy
    strings above a threshold) → **deny** (deny + continue; the agent adapts).
  - **Non-allowlisted outbound host** (`WebFetch`/`Bash` network call to a host not on
    an allowlist) → **deny** (the exfiltration hard-stop).
  - **Denied shell pattern** (`rm -rf` outside the worktree, `chmod 777`, raw
    `curl`/`wget`) → **escalate** (deny + park a human via the recovery gate; a false
    deny here could wedge legitimate work and warrants a look).
- `AgentExecutor.Execute` (`runner/agent.go:36`) shall register the guard through the
  SDK interception seam so a **deny** result prevents the tool from running and
  returns the guard's reason to the agent as the tool result
  (`NewPermissionResultDeny(message)`, `internal/control/types.go:220`). Every deny
  shall also produce a Unit-1 `Finding` (`Tier: "guard"`, `Action: "blocked"`); every
  escalate produces a `Finding` (`Action: "escalated"`) that Unit 5 routes to the
  recovery gate.
- **Seam decision (resolves the first draft's blocking Open Question,
  [D7](#d7--interception-seam-canusetool-primary-force-default-mode-fallback)).** The
  primary seam is **`WithCanUseTool`** — it returns a deny *with a reason string* fed
  back to the agent, exactly what the requirement needs. Whether it fires for an
  `Edit` auto-accepted under `permission_mode = "acceptEdits"` is **unverifiable from
  SDK source** (confirmed by fact-find); the implementer shall run a **runtime probe
  against the live CLI first thing in this unit.** If the callback does not fire under
  `acceptEdits`, the guard shall **force `default` permission mode for guarded steps**
  so every tool passes through the callback — accepting the loss of auto-accept
  because correctness of the hard boundary outranks that convenience. (The `PreToolUse`
  hook is a fallback *only* if the probe proves it can actually deny, not merely
  observe.) A guard that silently no-ops under the default permission mode is a
  correctness bug, not a tuning choice.
- **Transcript redaction ([N1](#n1--the-transcript-is-a-leak-surface-too)).** Because
  the SDK emits the `tool_use` block with its full input *before* the permission
  check, the runner writes would-be secrets to `transcript.jsonl` even for denied
  calls (`agent.go:362-389`). When Tier-1 is enabled, the runner shall pass tool
  inputs (and command-step output) through the guard's secret-detector as a
  **redaction filter before appending to the transcript**, substituting the same
  redacted preview `Redact` produces for findings. `internal/transcript` stays pure —
  the runner (which already imports `sentinel` for the guard) does the redaction. This
  covers *all* tool inputs and command output, not only denied Writes (a secret can
  ride an allowed `Bash echo` too). With security off / persistence off, the transcript
  writer behaves byte-for-byte as today.
- The guard callback shall be **thread-safe** (the SDK documents it "may be invoked
  concurrently … If no callback is set, all tool requests are denied" —
  `options.go:671-672`) and shall hold no per-step mutable state; it receives the tool
  name + input and a run/step identity captured at registration.
- The guard shall be a **no-op-safe addition**: when disabled or its ruleset is empty,
  `AgentExecutor` behaves byte-for-byte as today, and the persistence-off path works.

**Proof Artifacts:**
- Unit test (`sentinel`): table-driven `Guard.Check` — an `Edit` writing a PEM private
  key denies; an `Edit` writing ordinary Go code allows; a `WebFetch` to a
  non-allowlisted host denies; a benign `Bash go test` allows; an `rm -rf /` escalates.
  `10-proofs/2.0-guard-rules.txt`.
- Integration test (`runner`): drive `captureStream`/`Execute` with a scripted SDK
  channel where the agent attempts a secret-write; assert the call is denied, the agent
  receives the reason, a `blocked` finding is produced, **and the transcript entry for
  that call is redacted** (no raw key on disk). `10-proofs/2.1-blocked-and-redacted.txt`.
- Probe artifact: a short recorded result of the `acceptEdits` seam probe and which
  seam was chosen. `10-proofs/2.2-seam-probe.md`.
- Manual/demo: a throwaway workflow whose implement step is prompted to write a fake
  key; run it; show the block in the findings record and the redacted transcript.
  `10-proofs/2.3-demo.md`.

### Unit 3: Detection supervisor — the fleet harness (Tier 2 infrastructure)

**Purpose:** Stand up the out-of-band supervisor that drives the Haiku fleet, before
defining the individual monitors. It is a bus subscriber, exactly like the TUI.

**Functional Requirements:**
- The system shall add an in-process supervisor (proposed `internal/sentinel/supervisor`
  or a `runner`-side component) that calls `Manager.Subscribe()` (`engine.go:157`) and
  consumes `StepMessage{RunID, StepID, Seq, Iteration}` liveness signals
  (`event.go:68`). On each signal it shall read the *newly-appended* transcript entries
  for that step via `transcript.Reader.Window/Tail` (`reader.go:45,64`, safe during
  concurrent appends) — never from the bus, which carries no content.
- The supervisor shall dispatch a **window** of recent entries (not a single entry) to
  each monitor, so cross-entry/temporal patterns (read-secret-then-exfiltrate) are
  observable. The window shall be bounded by **both an entry-count cap and a token
  ceiling**, truncating oldest-first, so a single giant `tool_result` (a fetched web
  page) can blow neither the context nor the budget ([D9](#d9--monitor-window-is-dual-bounded)).
  It shall maintain minimal per-step cursor state (last seq classified) so it advances
  rather than re-scanning.
- The supervisor shall **batch and debounce** per step (flush every N new entries or T
  milliseconds, whichever first; defaults tunable via Unit 6 config) and
  **deduplicate** findings by `Fingerprint`, so a busy step does not produce one LLM
  call per transcript line.
- The supervisor shall run monitors as **tools-off classifier agents** via
  `runner.AgentExecutor` with **no run dir** (persistence off), so monitor
  conversations never pollute `.jig/runs/…`; only their `Finding` outputs persist
  (Unit 1). Each monitor is an `agent_file`-style (prompt/model/schema) triple with
  `model` defaulting to Haiku and a structured-output schema (`severity`, `flagged`,
  `detail`). The supervisor shall hold a **persistent Haiku client** rather than
  connect/disconnect per window (as the TUI holds a persistent chat client), to
  amortize connection overhead across a high-frequency fleet
  ([D10](#d10--persistent-monitor-client)).
- The supervisor shall enforce a **per-run dollar budget** and a concurrency cap. It
  sums each monitor call's `Result.TotalCostUSD` (the Unit 0 field) against the
  ceiling; when exhausted it shall stop dispatching to LLM monitors and log exactly one
  `degraded-to-tier1` finding — it shall never block or slow the observed run
  ([D6](#d6--fleet-budget-per-run-dollars)).
- The supervisor shall be **optional and off when persistence is off or security is
  disabled**, so the existing engine/runner suites are unaffected.

**Proof Artifacts:**
- Unit test (supervisor): a fake bus emitting `StepMessage`s over a seeded transcript
  file drives the supervisor; with one stub monitor that flags on a marker string,
  assert exactly one deduped finding and that batching collapses a burst of signals
  into a bounded number of monitor calls; assert an oversized `tool_result` is
  truncated to the window bounds. `10-proofs/3.0-supervisor.txt`.
- Unit test (supervisor): budget-exhaustion (summed monitor cost ≥ per-run ceiling)
  stops LLM dispatch and emits the degradation finding without blocking.
  `10-proofs/3.1-budget-degrade.txt`.

### Unit 4: The monitor roster (Tier 2 detectors)

**Purpose:** Populate the fleet with specific detectors, each a small agent file with a
tight detection contract. Depends on Unit 3. **Scope-drift is deferred**
([N6](#n6--scope-drift-monitor-deferred)) — the roster for this spec is three
monitors.

**Functional Requirements:**
- The system shall ship these monitors as agent files under `examples/agents/monitors/`
  (or a config dir), each tools-off, structured-output, and prompted to treat all
  transcript content as **untrusted data, never instructions**:
  - **prompt-injection** — watches `tool_result` blocks specifically (the only place
    external/attacker-controlled text enters — web fetches, untrusted reads) for
    instruction-hijack patterns; flags when the *following* assistant turn appears to
    act on injected instructions.
  - **stuck-loop** — largely deterministic prefilter (same tool + same input repeated N
    times, climbing turn count with repeated `IsError` results, no movement toward the
    step's schema output); the LLM confirms only ambiguous cases. Early-warning ahead
    of the hard `max_turns`/`max_budget_usd` stops.
  - **exfil-pattern** — temporal: a secret/PII read followed by an outbound call;
    explicitly requires the entry *window* (Unit 3) and coordinates with the Tier-1
    outbound-host rule (Unit 2).
- Each monitor shall declare a severity rubric in its prompt so findings are comparably
  tiered, and shall emit `flagged=false` cheaply when nothing is wrong.
- The stuck-loop and (partly) exfil detectors shall implement their deterministic
  prefilter in `internal/sentinel` so the LLM is invoked only when the cheap signal
  fires — keeping cost down and matching the "regex where a regex suffices" principle.
- **Deferred:** the **scope-drift** monitor (an allowed tool used off-mission). It
  requires the supervisor to know each step's declared intent + allowed-tool set — the
  natural mechanism is constructing the supervisor with the `*workflow.Workflow` and
  looking the step up by `StepID`. This is recorded for a later unit; not built here.

**Proof Artifacts:**
- Unit test (per monitor, via the supervisor stub harness): a seeded window containing
  an injection payload in a `tool_result` yields a `prompt-injection` finding; a
  repeated-identical-tool-call window yields a `stuck-loop` finding; a
  secret-read-then-outbound window yields an `exfil-pattern` finding.
  `10-proofs/4.0-roster.txt`.
- Fixture: the three monitor agent files, each validated to parse as an `agent_file`
  triple. `10-proofs/4.1-monitor-files.md`.

### Unit 5: Surfacing — Security pane and human escalation

**Purpose:** Make findings visible and make *critical* findings actionable through the
machinery jig already has, without inventing an autonomous kill path.

**Functional Requirements:**
- The TUI run monitor shall render a **Security region/pane** that lists findings for
  the current run by severity as `SecurityFinding` ctrl events arrive, reading detail
  from `findings.jsonl` (file is truth) rather than from the event. All new styles
  shall live in `internal/tui/styles.go` (a `theme.Security.*` sub-struct built from
  existing `danger`/`warning`/`success` tokens; no bare package styles, no hardcoded
  hex). Redacted-secret previews must **not** be routed through glamour (verbatim path).
- On a **critical** finding, the run shall **escalate to the human** through the
  existing recovery machinery (`RecoveryRequest` — `event.go:125` — resolved via
  `Run.Recover`, options retry/resume/abort at `engine.go:320-323`), offering the
  standard choices. Escalation is **best-effort to the nearest still-live decision
  point** ([N7](#n7--escalation-is-best-effort)): if the affected step is still
  running/blocked, park *it*; if it already completed but the run is still active, park
  the run at its current frontier (a completed step **cannot** be parked —
  `engine.go:1119` ignores non-`AwaitingRecovery` states); if the whole run has
  finished, record the finding and surface it in the pane with nothing to park.
  Monitors shall **not** cancel or abort a run on their own. (Tier-1 *blocks* an
  individual tool call inline; an `escalate`-marked Tier-1 rule additionally parks the
  step via this same path.)
- Escalation shall be **rate-limited and deduplicated by `Fingerprint`** so a burst of
  related critical findings parks the step **once**, not repeatedly.

**Proof Artifacts:**
- Unit test (`tui`): feeding `SecurityFinding` events populates the Security pane;
  render golden for low/high/critical rows. `10-proofs/5.0-security-pane.txt`.
- Unit test (`engine`): a critical finding on a running step routes it to
  `StatusAwaitingRecovery` and a subsequent `Run.Recover(abort)` tears down cleanly; a
  critical finding whose step already completed parks the run frontier (or records only
  if the run is done) and does **not** attempt to resurrect the finished step; a
  non-critical finding does not park anything; duplicate fingerprints park once.
  `10-proofs/5.1-escalation.txt`.

### Unit 6: Configuration, opt-out, and documentation

**Purpose:** Make the layer configurable and documented.

**Functional Requirements:**
- **Config surface ([D3](#d3--config-placement-baked-in-default-on--defaultssecurity-cascade)).**
  Because jig has **no global-config precedent** (all config is per-workflow; `cmd/jig`
  reads no startup config file) and security is **default-on**, the effective config
  must exist even when a workflow declares nothing. Therefore: the engine ships a
  **hardcoded default-on config** (starter ruleset, outbound allowlist, fleet budget,
  batch/debounce windows) in code — that is what makes default-on true with zero
  workflow config — and workflows *tune or opt out* via a `[defaults.security]` block
  cascading to `[step.security]`, mirroring the existing `model`/`effort` inheritance
  (`load.go` `applyDefaults`). Per-step override matters: a network-heavy `research`
  step widens its own allowlist without loosening the whole workflow. No new file or
  load path is introduced.
- Configurable knobs shall include: enabling/disabling each tier, the Tier-1 ruleset
  (or a path to it), the outbound-host allowlist, the monitor roster and their models,
  the per-run fleet dollar budget and concurrency cap, and the batching/debounce
  windows.
- The config shall be **statically validated at load time** with a valid and an
  invalid test case (the `internal/workflow` house rule), and the kitchen-sink
  `examples/feature.toml` shall continue to `validate` cleanly.
- `docs/workflow-schema.md` (and a new `docs/security-monitoring.md`) shall document
  the two tiers, the finding record format, the redaction guarantee (findings *and*
  transcript), the escalation policy, the cost accounting, and the monitor roster; an
  ADR shall record the out-of-band + raise-don't-kill decisions
  (`docs/adr/NNNN-agent-security-monitoring.md`), consistent with ADR 0003.

**Proof Artifacts:**
- Unit test (config/workflow): valid config loads and a zero-config workflow still has
  security on (baked default); an invalid ruleset/allowlist entry is rejected at load
  time. `10-proofs/6.0-config-validate.txt`.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0.
  `10-proofs/6.1-validate-ok.txt`.
- Docs: schema section + `security-monitoring.md` + the ADR, reviewed.

## Non-Goals (Out of Scope)

1. **Autonomous, agent-driven run termination.** Monitors *raise*; humans *decide*
   (Tier-1 blocks individual tool calls, which is not a run kill).
2. **A replacement for real DLP / SAST / secret-scanning infrastructure.** The Tier-1
   ruleset is a starter set, not an exhaustive detector.
3. **Sandboxing or network isolation of agents.** Containment remains worktree
   isolation + tool allowlists; this spec adds argument-level policy and observation.
4. **Any change to the DAG model, `jig validate`, or the termination guarantee.**
5. **Detection-evasion-proofing the LLM monitors.** The deterministic Tier-1 guard is
   the hard boundary; the monitors are defense-in-depth.
6. **Tier-1 *prevention* on command steps.** Command steps are deterministic
   author-written shell with no pre-tool SDK seam; their captured output may be
   *scanned* into an `observed` finding, but nothing is blocked
   ([D8](#d8--tier-1-prevention-is-agent-step-only)).
7. **The scope-drift monitor** ([N6](#n6--scope-drift-monitor-deferred)) — deferred to
   a later unit; it needs the supervisor-holds-workflow capability.
8. **A live streaming `StepCost` event and jig-side enforcement of the observed run's
   `max_budget_usd`.** Unit 0 *records* cost and the fleet enforces its own ceiling;
   the SDK still enforces the observed run's per-step budget. Live cost streaming and
   jig taking over run-budget enforcement are a possible later unit.
9. **Retroactive scanning of historical runs.** The layer observes live runs.

## Design Considerations

The only new user-facing surface is the TUI Security pane (Unit 5), the finding list,
and the per-step/per-run cost display (Unit 0). Everything else is internal (a pure
package, a runner seam, a bus subscriber, agent files). Styles follow the
singleton-theme rules in `internal/tui/styles.go`.

## Repository Standards

- **Deterministic orchestration is preserved.** The security layer is out-of-band; it
  is not expressible in the DAG and does not affect static validation or termination.
- **`internal/` packages are the unit of design.** `internal/sentinel` is pure data +
  file I/O + a deterministic policy engine, importing nothing from
  `engine`/`runner`/`tui`.
- **File is truth, bus is liveness.** Findings are authoritative on disk; the one
  deviation — `SecurityFinding` rides **ctrl**, not `live` — is intentional and
  documented (a missed finding is not self-correcting).
- **Fail at load time.** New config (Unit 6) is parsed, defaulted, and validated with a
  valid and an invalid test case.
- **Comments explain the non-obvious "why."** The permission-mode/hook interaction and
  its probe (Unit 2), the transcript-redaction rationale (Unit 2), the ctrl-not-live
  choice (Unit 1), the deny-vs-escalate distinction (Unit 2), and the
  monitors-are-untrusted-input stance (Unit 4) earn comments.
- **Persistence-off is a first-class path.** The sink and cost file no-op and the
  supervisor does not attach when there is no run dir.
- **Examples are documentation.** `examples/feature.toml` still validates; the monitor
  agent files live beside `examples/agents/security-reviewer.md`.
- **Format & vet before committing.** `gofmt -l -w .` and `go vet ./...` per unit.

## Technical Considerations

**Interception seam (Unit 2) — verified in the SDK, with one gap that needs a runtime
probe.** `github.com/severity1/claude-agent-sdk-go@v0.6.22` exposes
`WithCanUseTool(callback)` whose callback is
`func(ctx, toolName string, input map[string]any, ToolPermissionContext)
(PermissionResult, error)` returning `PermissionResultAllow` /
`NewPermissionResultDeny(message)` (`internal/control/types.go:220,237-242`), and
`WithPreToolUseHook` / `HookEventPreToolUse`. The SDK notes the callback "must be
thread-safe … may be invoked concurrently. If no callback is set, all tool requests
are denied (secure default)" (`options.go:671-672`). **Whether the callback fires for
an `Edit` auto-accepted under `acceptEdits` is not documented and is `UNVERIFIABLE`
from source** — hence the mandatory probe and the force-default-mode fallback in Unit 2.

**Transcript captures denied tool inputs (Unit 2 / N1).** Confirmed: the SDK emits the
`AssistantMessage` with the `tool_use` block *and its input* before the CLI checks
permission, and the runner records it (`agent.go:362-389`). So a would-be secret lands
in `transcript.jsonl` even when denied — which is why redaction must live at the
transcript-write path, not only at the finding.

**Cost is surfaced by the SDK but discarded by jig (Unit 0 / N3).**
`ResultMessage.TotalCostUSD *float64` and `Usage` exist (`message.go:188-202`) but
`captureStream` reads only `SessionID`/`Subtype` (`agent.go:289-350`); `step.Result`
has no cost field; `result.json` writes a minimal stub (`manifest.go:77-95`);
`RunSnapshot` has no cost. `max_budget_usd` is parsed and forwarded to the SDK
(`agent.go:113-114`) but enforced by the SDK, not jig. Unit 0 adds the recording path.

**Journal exhaustiveness is not actually guarded (Unit 1 / N2).** `eventKind`
(`journal.go:26-53`) is exhaustive but `decoders` (`journal.go:72-117`) covers only 11
of 14 events; `input_request`, `prompt_request`, `agent_question` have no decoder and
`journal_test.go` tests only 11. Unit 1 builds the real guard and fixes the three gaps.

**Bus integration (Unit 3).** `Manager.Subscribe()` (`engine.go:157`) is an explicit
fan-out; the supervisor subscribes identically, consumes `StepMessage` (`event.go:68`),
and reads content from disk via `transcript.Reader` (open→read-to-EOF→close, safe
during concurrent appends — `reader.go:17-29`). The transcript entry model exposes tool
`Name` + raw `Input` and `tool_result` `Content` + `IsError` — the substrate the
monitors need.

**Recovery (Unit 5).** `enterRecovery` (`engine.go:1101-1113`) transitions a step to
`StatusAwaitingRecovery` and emits `RecoveryRequest`; `handleRecover` (`engine.go:1119`)
ignores any step not in that state — so a **completed step cannot be parked**, which is
exactly why escalation is best-effort to the nearest live decision point (N7).

**No new third-party technology.** Tier 1 is stdlib regex/entropy; Tier 2 reuses the
existing Claude Agent SDK path with Haiku.

## Security Considerations

This spec *is* a security feature, so the threat model is explicit.

**What it defends against:** (1) secrets written to disk or shell by an agent (Tier-1
write/heredoc rules) **and secrets leaking into the transcript record** (N1 redaction);
(2) data exfiltration via outbound calls (Tier-1 host allowlist + Tier-2 exfil-pattern);
(3) prompt injection arriving in tool results (Tier-2 prompt-injection monitor); (4)
runaway/stuck agents burning budget (Tier-2 stuck-loop, ahead of the hard caps).

**The monitor's own attack surface.** A monitor reads attacker-controlled text and is
itself a prompt-injection target. Mitigations, all mandatory in Unit 4: monitors have
**no tools** (a hijack cannot *act*); **structured output only** (worst case is a
suppressed finding, not arbitrary behavior); and prompts that **spotlight/delimit all
transcript content as untrusted data**. The deterministic Tier-1 guard reads no free
text as instructions and is therefore not injectable — which is why prevention, not
detection, is the hard boundary.

**The record must not become a leak.** Both `findings.jsonl` *and* `transcript.jsonl`
are scrubbed of raw secrets when Tier-1 is active (Units 1 and 2), sharing the one
`Redact` helper, and both live under `.jig/` outside the repo. Proof artifacts under
`10-proofs/` are synthetic (fake keys, marker strings).

**Failure modes to accept, not hide.** The LLM monitors have false positives and
negatives; findings are advisory (except Tier-1 blocks and critical escalations) and
the human is the final arbiter. The layer degrades to Tier-1-only under budget pressure
rather than failing open silently.

## Success Metrics

1. **Prevention works synchronously:** the Unit 2 test proves a secret-writing tool
   call is denied before execution, the reason reaches the agent, **and the transcript
   entry is redacted** — impossible today.
2. **Detection works out-of-band:** the Unit 3/4 tests prove an injection payload in a
   `tool_result` produces a `prompt-injection` finding driven off a `StepMessage`, with
   no change to the observed run's behavior or timing.
3. **One redacted record surface:** no finding *and no transcript entry* contains a raw
   secret when Tier-1 is on (Units 1–2 tests).
4. **Loud serialization:** `SecurityFinding` round-trips through the journal and the
   *real* exhaustiveness test fails if any event skips its kind/decoder — including the
   three pre-existing gaps this spec fixes (Unit 1).
5. **Bounded, accountable cost:** per-step/per-run cost is recorded (Unit 0); batching
   collapses a burst of signals into a bounded number of monitor calls and per-run
   budget exhaustion degrades to Tier-1-only without blocking (Unit 3).
6. **Raise, don't kill:** a critical finding parks the nearest live decision point via
   the existing recovery gate (Unit 5); no monitor path cancels a run.
7. **No regressions (overarching gate):** `go build ./...`, `go vet ./...`,
   `gofmt -l .` (empty), and `go test ./...` pass at every unit boundary, the
   persistence-off path stays a no-op, and `go run ./cmd/jig validate examples/feature.toml`
   exits 0.

## Resolved Design Decisions

The first draft's Design Decisions D1–D5 stand; D6 and all five Open Questions are now
resolved, and seven new decisions (N1–N7) were forced by the grilling session and its
fact-finds. The rationale and rejected alternatives:

**D1 — Out-of-band observer, not graph nodes.** *Rejected:* modeling monitors as
`agent`/`review` steps in the DAG — a *checkpoint* that reviews content at a fixed
point and cannot observe a sibling's *process*, and that adds edges the termination
proof must account for. *Chosen:* an out-of-band bus subscriber, invisible to
`jig validate`. The in-graph `security-reviewer.md` remains valid and complementary.

**D2 — Two tiers; prevention is deterministic, detection is the LLM.** *Rejected:* a
Haiku fleet as the *whole* design — it is retrospective (the transcript is written after
a turn finalizes, so by the time it classifies "leaked secret" the secret is on disk)
and a regex detects a known secret shape faster and more reliably. *Chosen:*
deterministic Tier-1 for blockable harms, the Haiku fleet for fuzzy/temporal judgments.
Ship Tier-1 first.

**D3 — Config placement: baked-in default-on + `[defaults.security]` cascade.**
*Rejected:* a per-workflow opt-in `[security]` block (security authors must remember to
enable is off when it matters) and a brand-new global config file (no precedent, and
default-on would still need a code default when the file is absent). *Chosen:* the
engine ships hardcoded default-on config; workflows tune/opt-out via
`[defaults.security]` → `[step.security]`, mirroring existing inheritance. Resolves the
first draft's D6/Open Question 3.

**D4 — Findings ride ctrl, not live.** *Rejected:* the droppable `live` channel — a
dropped `StepMessage` self-corrects on the next disk read; a dropped finding does not.
*Chosen:* the must-not-drop `ctrl` channel, matching `ReviewRequest`/`RunFinished`.

**D5 — The action model: `deny` = deny+continue, `escalate` = deny+park.** The guard
callback is synchronous and must return allow/deny to the SDK; it cannot itself park a
step. *Rejected:* `escalate` = allow-the-tool-but-park (risks the `rm -rf` running).
*Chosen:* the dangerous op is blocked either way; the only difference is whether a human
is pulled in. `deny` denies inline and lets the agent adapt; `escalate` denies inline
*and* raises a critical finding that Unit 5 routes to the recovery gate. Resolves the
first draft's Open Question 5.

**D6 — Fleet budget: per-run dollars.** *Rejected:* token or call-count ceilings
(operators reason in dollars) and a per-process ceiling (one noisy run starves another).
*Chosen:* a per-run dollar ceiling summed from each monitor call's `TotalCostUSD`; on
exhaustion, degrade to Tier-1-only with one degradation finding. This is what forced
Unit 0.

**D7 — Interception seam: `CanUseTool` primary, force-default-mode fallback.** The
`acceptEdits` behavior is unverifiable from SDK source, so a runtime probe runs first.
*Rejected:* guarding only non-Edit tools under `acceptEdits` (a known Edit-shaped blind
spot is worse than no guard). *Chosen:* `WithCanUseTool` (returns a deny reason to the
agent); if it doesn't fire under `acceptEdits`, force `default` permission mode for
guarded steps — correctness of the boundary outranks auto-accept. Resolves the first
draft's Open Question 1.

**D8 — Tier-1 prevention is agent-step-only.** Tier-1 *is* the SDK pre-tool seam;
command steps have no such seam and are author-written deterministic shell (the threat
model is agent misbehavior, not author mistakes). *Chosen:* prevention applies to agent
steps; command-step output may be scanned into an `observed` finding but nothing is
blocked.

**D9 — Monitor window is dual-bounded.** A "window of recent entries" can be enormous
(a fetched web page in one `tool_result`). *Chosen:* bound by *both* an entry-count cap
and a token ceiling, truncating oldest-first, so neither context nor budget can be blown
by one giant entry. Resolves the window half of the first draft's Open Question 2.

**D10 — Persistent monitor client.** *Rejected:* per-window `Execute`
(connect/disconnect each time) for a high-frequency fleet. *Chosen:* a persistent
per-supervisor Haiku client, as the TUI holds a persistent chat client. Resolves the
client half of the first draft's Open Question 2.

**N1 — The transcript is a leak surface too.** The SDK hands jig the `tool_use` input
before the permission check, so denied secrets land in `transcript.jsonl`. *Chosen:*
the runner runs tool inputs (and command output) through the guard's secret-detector as
a redaction filter before appending to the transcript, reusing `Redact`. Closes a hole
the first draft's findings-only redaction left open.

**N2 — The journal exhaustiveness guard did not exist yet.** The first draft assumed a
guard from "spec 04 Unit 4"; in fact three events silently fail to round-trip. *Chosen:*
Unit 1 builds the real enumerate-every-member test and fixes the three gaps, because a
must-not-drop event on a lossy journal is a contradiction.

**N3 — Cost tracking is a hard prerequisite.** The per-run dollar budget cannot be
computed while `Execute` discards `TotalCostUSD`. *Chosen:* a small standalone Unit 0
adds per-step/per-run cost accounting (independently useful) that the fleet budget
reads.

**N4 — Fingerprint composition.** *Chosen:* `{stepID, rule/monitor, stable evidence
key}` — identical repeats within a step collapse and park once; distinct offenses,
rules, or steps are fresh findings. *Rejected:* `{stepID, rule}` (collapses distinct
offenses) and content-hash of the redacted preview (previews can collide).

**N5 — Command-step boundary** — see D8.

**N6 — Scope-drift monitor deferred.** The off-mission judge needs the
supervisor-holds-workflow capability; the user is not ready to build that tier yet. The
roster for this spec is prompt-injection, stuck-loop, exfil-pattern.

**N7 — Escalation is best-effort.** Tier-2 is retrospective, so a critical finding can
land after its step finished, and a completed step cannot be parked (`engine.go:1119`).
*Chosen:* escalate to the nearest still-live decision point — running step → run
frontier → record-only.

## Remaining Clerical Notes

- **Numbering.** This spec is filed as **10** to avoid colliding with the
  run-integration/reset epic that holds **05–09**. Purely clerical; renumber if that
  epic's slots free up.
- **Glossary / ADR.** A security-domain glossary (`CONTEXT.md` + a `CONTEXT-MAP.md`) and
  an ADR for the out-of-band + raise-don't-kill decisions were discussed and
  **deferred** — to be produced when the security package takes shape (its context root
  is `internal/sentinel/`, which does not exist yet).
