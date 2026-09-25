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

### Permission decisions

The guard runs inside jig's permission callback, which every agent step
installs; a harness without permission callbacks fails the step closed. Each
adapter permission request is resolved to a **canonical tool name** and the
input the rules read (`command` for `Bash`, `url` for `WebFetch`, `file_path`
and `content` for edits), in one place in `internal/harness`:

- A per-session cache, filled from `tool_call` notifications and keyed by
  `toolCallId`, supplies what the request itself lacks. The ACP SDK can deliver
  a request before the notification the adapter sent first, so a request that
  cannot be resolved yet waits up to about 2 s for it; the wait ends if the
  request is cancelled.
- Claude names come from `_meta.claudeCode.toolName` (request, then cache),
  then the ACP `kind`. Codex and Cursor names come from `kind`
  (`execute`→`Bash`, `edit`/`delete`→`Edit`, `fetch`→`WebFetch`,
  `search`→`WebSearch`, `read`→`Read`), else the title (for example an MCP
  tool).
- Inputs come from each backend's wire shape: Claude `rawInput`; Codex
  `rawInput.command` and, for edits, the cached `tool_call` diff; Cursor the
  backticked request title and the request's `content` diff.

The decision order is:

1. **Unresolved identity** — a call whose tool name cannot be resolved is
   denied by the harness, on every step.
2. **Unresolved input** — on a guarded step, a `Bash` or edit call whose
   command, path or content cannot be assembled is denied.
3. **`ExitPlanMode`** is denied, guarded or not, so an agent never changes its
   own permission mode.
4. **Claude second layer** — a call outside a present `tools` list, inside
   `disallowed_tools`, or outside the read-only set under
   `permission_mode = "plan"` is denied. `AskUserQuestion` is exempt; only
   `ask_user` governs it.
5. **Tier-1 guard**, when enabled; a denial records a finding.
6. Otherwise **allow**.

An allow selects only the request's `allow_once` option. `allow_always` answers
persist rules — adapter session rules, or the operator's Codex exec-policy and
Cursor allowlist — that later calls would skip jig for, so a request offering no
`allow_once` option is rejected. The Tier-2 classifier session keeps a deny-all
callback.

### Tier-1 coverage per backend and mode

The guard sees only calls the adapter asks about. What reaches it depends on
the backend and mode:

| Backend | Mode | What prompts, and so reaches the guard | Gaps |
|---|---|---|---|
| `claude` | `default` | Calls the repository's `.claude/settings.json` allow rules do not pre-approve. | Project allow rules auto-approve without a prompt. |
| `claude` | `acceptEdits` | Shell, fetch and other non-edit calls. | Auto-approved edits bypass the secret-in-write rule. Project allow rules. |
| `claude` | `plan` | Read-only calls; jig's second layer denies everything else and `ExitPlanMode`. | Project allow rules. |
| `claude` | `dontAsk` | Nothing: calls not pre-approved are denied, not prompted. | Project allow rules are the only way a call runs. |
| `claude` | `bypassPermissions` | Nothing. | Rejected at load under Tier-1. |
| `codex` | `read-only` | Edits and commands that leave the read-only sandbox. | Reads never prompt. Exec-policy prefix rules auto-approve. |
| `codex` | `agent` | Commands that leave the sandbox (for example network access). | Edits and in-sandbox commands run without prompting. Reads never prompt. Exec-policy prefix rules. |
| `codex` | `agent-full-access` | Nothing. | Rejected at load under Tier-1. |
| `cursor` | `agent` | Shell commands and edits not on the operator's allowlist. | Allowlist entries auto-approve. Reads never prompt. Cursor approves any operation without a `toolCallId` without asking — an adapter gap jig cannot close. |
| `cursor` | `plan`, `ask` | The adapter's read-only modes; anything it asks about. | Reads never prompt. Allowlist entries. Operations without a `toolCallId`. |

Tier 2 still reviews the transcript in every case.

Three rules keep the gaps from being silent:

- **Never-prompt modes fail at load.** A step with Tier-1 enabled whose agent
  uses Claude `bypassPermissions` or Codex `agent-full-access` is rejected, with
  a message suggesting `[step.security] tier1_enabled = false` or another mode.
- **Operator allowlists are checked before `Open`.** For a guarded Codex or
  Cursor step, the runner inspects operator-local permission config just before
  opening the session (never in `jig validate`, which stays machine-independent).
  **Blanket** auto-approval fails the step: Cursor `approvalMode =
  "unrestricted"` or a `permissions.json`, or a Codex `approvals_reviewer` other
  than the user. **Narrow** entries — Cursor `permissions.allow` entries,
  including every `.cursor/cli.json` from the project root to the cwd, and
  Codex exec-policy `prefix_rule`s that allow, including the repository's
  `.codex/rules` — are recorded as `guard` findings (monitor
  `operator-allowlist`, severity `low`, action `observed`) and the step runs.
  Messages name the file and entry index only, never the entry text. `jig
  doctor` lists the same findings: blanket as a failure, narrow as a warning.
- **Claude user settings are dropped.** jig sends
  `settingSources: ["project"]`, so `~/.claude/settings.json` and local
  settings — and their allow rules — never apply. `jig doctor` warns when user
  settings set `env` or `apiKeyHelper`.

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

Tier-2 classifiers run on the Claude ACP adapter even when the observed worker
uses Cursor or Codex. The operator therefore needs a working Claude login in
addition to any worker-backend login, and classifier calls incur separate
Claude billing.

Each invocation is a Claude agent with `tools = []` (no built-in tools),
`max_turns = 1`, no question handler, and a deny-all permission callback. ACP
has no separate system-prompt channel, so the embedded prompt and the bounded
transcript, marked as untrusted, are sent as one delimited prompt. The result
schema permits exactly the required `flagged`, `severity`, and `detail` fields,
and dispatch has a 30-second upper bound. jig creates no transcript or artifact files for the
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
