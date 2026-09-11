# Agent security monitoring

jig applies two independent security layers to agent steps. Tier 1 is a
synchronous deterministic guard. Tier 2 is a retrospective classifier fleet:
it observes durable transcript activity without putting classifier latency on
the worker's execution path.

## Tier 1: deterministic guard

`sentinel.Guard` runs before supported tool calls. It detects secret-shaped
values in write-tool inputs, denies outbound calls to hosts outside an effective
allowlist, and escalates selected dangerous shell patterns. A denied call is
returned to the worker as a structured denial; a critical escalation also uses
the existing recovery gate.

The guard redacts the supported tool inputs it inspects. This is pattern-based
protection, not a claim that every secret, PII value, assistant block, or tool
result is recognized.

## Tier 2: embedded classifier fleet

The binary embeds a fixed roster from `internal/runner/monitors/`:

| Monitor | Contract | Dispatch policy |
|---|---|---|
| `prompt-injection` | Tool-result instruction redirection followed by a consistent assistant action | Every eligible nonempty window |
| `stuck-loop` | Repeated calls/errors without corrective progress | Only after `StuckLoopPrefilter` fires |
| `exfil-pattern` | Sensitive read followed by an outbound action that indicates unauthorized disclosure | Every eligible nonempty window |

The roster does not depend on the process working directory and cannot be
overridden by a same-named local file. Malformed built-ins fail manager
construction visibly.

Each run owns a private buffered signal channel. After a worker reports that its
transcript append completed, the scheduler offers the signal non-blockingly to
that run's supervisor. Signals carry generation, iteration, and attempt, so a
window never combines separate executions. The same setup is used for a new run
and `Manager.Resume`; manager-wide UI subscribers are not used for monitoring.

`batch_size` is the number of transcript-advance signals that forces a flush;
`debounce_ms` is the maximum wait for a smaller batch. They do not control the
classifier window size. Each dispatch reads a bounded recent tail and sends at
most 20 entries and 32,000 bytes of final rendered input, including evidence
labels and truncation markers. A later window retains bounded prior context, so
an event immediately before one flush can be paired with a later action. Evidence
outside the tail can be missed, and a run may finish before a retrospective
verdict arrives.

Before transmission, jig pattern-redacts text, thinking, tool names/status,
tool inputs, result content, and trusted allowlist context. Returned finding
detail is redacted again before persistence. Markers remain visible to the
exfiltration classifier. This reduces exposure but is not comprehensive secret
or PII detection.

## Classifier isolation and prerequisites

Tier-2 classifiers use the direct Claude Agent SDK even when the observed worker
uses Claude ACP, Cursor ACP, or Codex ACP. The operator therefore needs a working
Claude CLI/SDK login in addition to any worker-backend login, and classifier
calls incur separate Claude billing.

Every invocation uses the embedded prompt as the SDK system prompt and the
bounded transcript as untrusted user data. It has an explicitly empty tool list,
allowed-tool list, settings-source list, and skills list; a deny callback is
also installed. The result schema permits exactly the required `flagged`,
`severity`, and `detail` fields, the turn limit is one, and dispatch has a
30-second upper bound. jig creates no transcript or artifact files for the
classifier session. That is a jig persistence-off guarantee, not a promise about
vendor CLI session storage.

Classifier dispatch is serial today. `concurrency_cap` remains an upper bound;
valid values do not promise parallel classifier calls.

## Findings, degradation, and recovery

Findings are appended to `.jig/runs/<run-id>/findings.jsonl`. Tier-2 findings use
`tier = "monitor"`, `action = "observed"`, and evidence such as a transcript
sequence range. Fingerprints suppress duplicate findings, including after a
persisted run is reopened.

A classifier connection, timeout, or verdict failure produces one low-severity
`monitor-unavailable/<id>` health finding for that run and disables that
classifier for the remaining process lifetime. Other classifiers and Tier 1
continue. Transcript, accounting-state, and finding-persistence failures use the
same security event surface and name the degraded subsystem. Raw SDK errors are
not persisted.

A critical finding parks only the still-current, nonterminal execution at the
existing recovery gate. A duplicate verdict, a verdict for an older
generation/iteration/attempt, or a verdict arriving after the step became
terminal is recorded without parking replacement work.

## Budget and reopen behavior

Known classifier cost is accumulated per run. A versioned
`security-monitor-state.json` stores cumulative spend, degradation, and whether
a paid invocation may be in flight. The in-flight marker is atomically persisted
before dispatch and cleared with updated spend afterward. Known costs returned
with failed or invalid verdicts still count.

`fleet_budget_usd = 0` means unlimited Tier-2 spend; it does not disable Tier 2.
A positive ceiling is checked before each serial call and after reported cost,
so it may be exceeded by one completed invocation. If a finite-budget run is
reopened with missing, corrupt, or in-flight accounting—or a launched call
returns no cost—remaining budget is unknown and Tier 2 stays disabled for that
run with a health finding. Unlimited runs may continue with an explicit
unknown-cost note. Resetting a step does not reset fleet spend.

On ordinary reopen, prior transcript content alone triggers no classifier call.
The first new activity may use a bounded historical tail as context, while saved
spend, degradation, and finding fingerprints remain in effect.

## Configuration

```toml
[defaults.security]
enabled            = true
tier1_enabled      = true
tier2_enabled      = true
outbound_allowlist = ["api.github.com"]
fleet_budget_usd   = 0.10
concurrency_cap    = 4
batch_size         = 5
debounce_ms        = 500

[[step]]
id   = "untrusted-research"
type = "agent"

  [step.security]
  tier2_enabled = false
```

An explicit step value wins over `[defaults.security]`, including an explicit
`tier2_enabled = true` when the default is false. Security-off, Tier-2-off,
command/check/review-only, and persistence-off execution makes no classifier
calls.

## Optional live smoke procedure

Deterministic tests replace external ACP processes and classifier service calls;
they do not establish real-model judgment. Before a release, use synthetic local
fixtures and a small positive `fleet_budget_usd` to run each worker selection
(`claude/acp`, `cursor/acp`, and `codex/acp`) with a working worker login and
Claude classifier login. Record CLI/adapter versions, elapsed time, classifier
cost, captured tool evidence, resulting finding, cancellation behavior, and the
backend's supported reopen result. Also submit an adversarial transcript that
asks the classifier to invoke a tool and confirm no tool executes. A row not run
must be reported as not run rather than inferred from offline coverage.
