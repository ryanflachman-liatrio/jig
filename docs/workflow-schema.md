# Jig Workflow Schema — MVP 1

## Purpose

Jig adds a **deterministic orchestration layer around non-deterministic agents.**
A workflow is a `.toml` file describing a graph of steps. Everything *around* the
agent is deterministic and inspectable:

- **The graph** — which steps run, their order, what is parallel vs. serialized,
  and which branches fire based on typed decisions.
- **The I/O contract** — every step declares its input files; content steps
  declare a single output file. Data flow is explicit and replayable.
- **The gates** — deterministic checks between steps (command exit code, JSON
  schema, file existence) that decide whether dependents run.
- **Termination** — loops are bounded, so a workflow is guaranteed to finish.

The only non-deterministic part is what happens *inside* one agent context, and
that is bounded by the skill instructions, the input files, and the allowed
tools handed to it.

The engine is a **DAG plus a small set of labeled, bounded back-edges** — not a
free-form state machine. That preserves static validation, visualization, and a
termination guarantee.

---

## Top-level structure

```toml
[workflow]
name    = "feature-implementation"
version = "1"
description = "…"                    # optional

[defaults]                           # optional; per-step fields override these
model               = "claude-opus-4-8"
fallback_model      = "claude-sonnet-4-6"  # used if the primary is overloaded
effort              = "high"         # low | medium | high | xhigh | max
max_turns           = 20
max_thinking_tokens = 8000
max_budget_usd      = 5.0            # per-step cost ceiling
cwd                 = "."
permission_mode     = "acceptEdits"
backend             = "claude"       # agent vendor (claude today)
transport           = "sdk"          # sdk | acp (how jig reaches the backend)
max_parallel        = 4
artifacts_dir       = ".jig/artifacts"   # run artifacts live outside the working tree
inject_context      = true               # engine-assembled step-context preamble on agent steps (default true)
```

Backend selection is **TOML-only** (never an environment variable). See
[Agent backend](#agent-backend-backend--transport).

---

## Run directory convention

Artifacts and engine records live under `.jig/` (outside the working tree) so
they stay reachable regardless of which git worktree a step runs in:

```
.jig/
  runs/<run-id>/
    artifacts/          # step outputs (@stepid resolves to files here)
    steps/<step-id>/
      result.json       # engine-owned metadata (see "Engine-observed metadata")
```

---

## The step model

Steps are declared with `[[step]]`. Three types: `agent`, `command`, `review`.

### Common fields

| Field         | Type     | Notes                                                            |
|---------------|----------|------------------------------------------------------------------|
| `id`          | string   | Required. Unique. Used by `depends_on`, `@id` refs, and `when`.  |
| `type`        | string   | `"agent"`, `"command"`, or `"review"`.                           |
| `depends_on`  | [string] | **Always explicit.** Step ids that must finish first.            |
| `when`        | string   | Guard expression; step runs only if true. See "Conditionals".    |
| `output`      | path     | Single output file (content). **Optional.**                     |
| `output_type` | see below| `"text"` (default) or a scalar verdict. See "Structured outputs". |
| `on_failure`  | string   | `"abort"` (default), `"retry"`, `"continue"`. See "Failure recovery". |
| `max_retries` | int      | With `on_failure = "retry"`. Default 1.                          |
| `[step.validate]` | table| Deterministic gate. See "Validation".                            |
| `[step.loop]` | table    | Bounded loop-back. See "Loops".                                  |

### Agent step

Spins up a **fresh agent context**, driven by **either a skill directory or a
Claude agent file** (exactly one of `skill` / `agent_file`).

| Field                  | Type     | Notes                                                       |
|------------------------|----------|-------------------------------------------------------------|
| `skill`                | dir path | Directory with `SKILL.md` (+ optional helpers). Xor `agent_file`. |
| `agent_file`           | path     | A Claude agent `.md` file (frontmatter + prompt). Xor `skill`. |
| `inputs`               | [string] | `@stepid` / `@stepid.field` refs and/or file paths. See "Data flow". |
| `allowed_tools`        | [string] | Tool allowlist for this context.                            |
| `disallowed_tools`     | [string] | Tool denylist (complement of the allowlist).                |
| `append_system_prompt` | string   | Extra per-step instructions appended after the skill/agent prompt. |
| `isolation`            | string   | `"worktree"` or `"none"`. See "Worktrees".                  |
| `[step.schema]`        | table    | Structured output contract (TOML-native). See "Structured outputs". |
| `schema_file`          | path     | Alternative to `[step.schema]`: a raw JSON Schema file.     |
| `block_on`             | string   | Condition referencing the step's **own** schema output. See "Interactive input". |
| `inject_context`       | bool     | Opt out of the engine-assembled step-context preamble (default `true`; overrides `[defaults]`). Agent-only. See "Step context". |
| `[step.context]`       | table    | Author-supplied `purpose` / `notes` that *supplement* the preamble. See "Step context". |
| `model` / `fallback_model` / `effort` / `max_turns` / `max_thinking_tokens` / `max_budget_usd` / `permission_mode` | | Override `[defaults]`. |
| `backend` / `transport` | string   | Override `[defaults]`. See [Agent backend](#agent-backend-backend--transport). |

**`SKILL.md` contract.** Agent Skills convention: YAML frontmatter (`name`,
`description`) + instruction body. At runtime the engine builds the prompt from
`SKILL.md` and injects the resolved input paths and the required `output` path
(when one is declared). Keeps prose out of the TOML and makes skills reusable.

**`agent_file` contract.** A Claude Code agent file: YAML frontmatter (`name`,
`description`, optional `tools`, optional `model`) + a system-prompt body. It is
just a bundled `(prompt, tools, model)` triple, so at load time jig folds its
`tools` into `allowed_tools` and its `model` into `model` **when the step leaves
them unset** (explicit step fields win), and uses the body as the agent's system
prompt. Everything else — `inputs`, schema, `validate`, `loop`, worktree
isolation — behaves exactly as for a skill-driven step.

### Agent backend (`backend` / `transport`)

Each agent step names which **backend** (vendor) and **transport** (wire
protocol) run it. Selection is TOML-only — there is no process-wide env var.

| Field | Default | Values today | Notes |
|---|---|---|---|
| `backend` | `claude` | `claude` | Vendor. Cursor / Codex / Gemini are not implemented yet. |
| `transport` | `sdk` | `sdk` \| `acp` | `sdk` = Claude Agent SDK; `acp` = ACP→Claude via `@agentclientprotocol/claude-agent-acp@0.70.0` |

Inheritance matches `model` / `effort`: step → `[defaults]` → engine default.
Unknown values fail at `jig validate`. Capability mismatches (e.g. `transport =
"acp"` with `[step.schema]`) fail closed at execute time.

Interactive steps may enable `AskUserQuestion` with either transport. The ACP
path advertises form elicitation only and supports text, single-select, and
multi-select questions, including the Claude adapter's “Other” answer fields.
ACP URL elicitation and other primitive form field types are intentionally not
advertised; an agent that sends one receives a protocol error rather than a
partially interpreted prompt. Permission requests remain a separate security
decision and are never rendered as user questions.

```toml
[defaults]
backend   = "claude"
transport = "sdk"

[[step]]
id        = "acp-spike"
type      = "agent"
transport = "acp"      # ACP→Claude for this step only
skill     = "skills/…"
```

### Command step

Deterministic script/command; no agent context.

| Field    | Type    | Notes                                              |
|----------|---------|----------------------------------------------------|
| `run`    | string  | Shell command (runs in `cwd`). Exactly one of run/script. |
| `script` | path    | Script file to execute, resolved from the **project (git repo) root** (e.g. `examples/scripts/test.sh`). A multi-line value is an inline script body. |
| `inputs` | [string]| `@stepid` refs / paths made available.             |
| `output` | path    | Optional file the command writes.                  |

### Review step (human-in-the-loop)

Pauses the run, renders an upstream artifact for a human, and captures a verdict.

| Field          | Type     | Notes                                                     |
|----------------|----------|-----------------------------------------------------------|
| `review`       | string   | `@stepid` (glamour-render that markdown) or `"diff"` (render the upstream worktree diff). |
| `output_type`  | table    | The decision, e.g. `{ enum = ["approve", "revise"] }`. Captured from the TUI. |
| `max_messages` | int      | How many free-text messages the human may send to the reviewed agent before giving a verdict. Default 10. See "Interactive input". |

---

## Data flow

- **Inputs are an array.** Each entry is either an `@stepid` reference (resolves
  to that step's `output` file, or its structured JSON artifact for producers),
  an `@stepid.field` reference (a single field of a producer's JSON output), or
  a literal path for static inputs. `@` refs — including the field path against
  the producer's schema — are validated at load time; a dangling wire or a typo'd
  field is a parse error.
- **Input delivery is by path** (agent reads with its tools). Opt into inlining
  a specific small file's contents: `{ path = "conventions.md", inline = true }`.
  A field ref is naturally small and is typically inlined.
- **`output` is a single file and is optional.** Content steps (plan, review,
  synthesis) emit **markdown** for humans (glamour-rendered in the TUI).
  Mutating steps (e.g. `fix`) can omit `output` entirely — their diff and the
  engine-observed metadata are the result.

### Engine-observed metadata (not agent-authored)

Status, **which files changed**, the tool-call log, and duration are derived by
the engine from the SDK message stream — it sees every `Write`/`Edit`/`Bash`
call. For *mutation* facts there is nothing to ask the agent for: the engine
observes them directly, and this is the record a downstream gate trusts. Written
to `.jig/runs/<run-id>/steps/<step-id>/result.json`. (For an *analysis* agent's
conclusions — a verdict, findings, a status — see "Structured outputs", which
are schema-enforced rather than observed.) Humans review markdown/diffs, never
raw JSON.

---

## Worktrees

`isolation = "worktree"` runs a step in its own git worktree so mutating agents
don't clobber each other and every change is reviewable/revertible.

- **Default on** for agent steps whose `allowed_tools` include mutating tools
  (`Edit`/`Write`/`Bash`); override with `isolation = "none"`.
- **Branch name is derived by convention:** `jig/<workflow>/<step-id>`. No
  injected variable needed — a later `command` step can reference it directly.
- **Each step worktree branches off the run-branch HEAD**, not repo-root HEAD.
  This means each step sees the accumulated code changes produced by its upstream
  steps. When the step completes, jig squash-merges its worktree branch back into
  the run branch as one commit. Integration is the engine's responsibility — no
  explicit `merge` command step is needed.
- `validate` commands run *inside* the step's worktree, so they see the step's
  changes before integration.

---

## Conditionals (forward branching)

A step runs only if its `when` guard is true. Branches live on the **consumer**
side, so the graph stays readable. Expression grammar (minimal):

```
when = "validate == 'valid'"          # scalar output_type verdict
when = "review != 'approve'"
when = "is_valid"                      # bare bool for output_type = "bool"
when = "research.status == 'complete'" # a field of a producer's schema
when = "research.blocked"              # bare bool field
```

The left-hand side is a `<stepid>`, optionally followed by a dotted
`.field.path`. A bare step id tests that step's scalar `output_type` verdict; a
field path tests a named field of the step's structured (`[step.schema]` /
`schema_file`) output. Either way, the compared value is checked at load time
against the referenced type — an enum comparison to a non-member, or a field
that doesn't exist, is a parse error. The referenced step must be in
`depends_on`.

---

## Structured outputs

Agents split into two archetypes. **Mutators** (implementation steps) change the
repo; their result is the diff plus engine-observed metadata (above), and they
declare no output shape. **Producers** (research, validation, triage, routing,
review) reach a conclusion the *workflow* consumes — and that conclusion is
captured as **schema-enforced JSON**, not scraped from prose.

The engine runs a producer step in headless mode (`claude -p --output-format
json --json-schema '<schema>'`). The model's final answer is
**constrained-decoded** to the schema — it is guaranteed well-formed — and lands
in the CLI's `structured_output` field, which the engine writes to
`.jig/artifacts/<step-id>.json`. Tool use composes with this: a producer may
Read/Grep/Bash through its loop and *then* emit the schema-valid result.

A producer declares its output shape one of three ways (mutually exclusive):

**1. Scalar `output_type`** — a single verdict, for simple gates and review
steps. Referenced bare (`stepid` / `stepid == 'x'`).

```toml
output_type = "text"                              # default; not a producer
output_type = "bool"
output_type = { enum = ["valid", "invalid", "needs_human"] }
```

**2. `[step.schema]`** — TOML-native multi-field schema, compiled to JSON Schema
by the engine. Field specs: `"text"` / `"number"` / `"bool"`, `{ enum = [...] }`,
`{ list = <spec> }`, or a nested table of more fields. Fields are referenced as
`stepid.field`.

```toml
[[step]]
id            = "research"
type          = "agent"
skill         = "skills/research"
allowed_tools = ["Read", "Grep", "Glob"]

  [step.schema]
  summary    = "text"                                   # convention: always include a markdown summary
  status     = { enum = ["complete", "partial", "blocked"] }
  confidence = "number"
  findings   = { list = "text" }
  sources    = { list = { url = "text", relevance = "number" } }
```

**3. `schema_file`** — point at a hand-written JSON Schema when you already have
one. It is parsed at load time into the same field model, so `stepid.field`
refs are type-checked identically.

```toml
schema_file = "schemas/research.json"
```

**Convention: every producer schema carries a `summary` markdown field.** That
dissolves the markdown-vs-JSON tension — the JSON *carries* the human-facing
prose. A review step renders `@research.summary` (glamour), while the engine
branches on `@research.status`. Both come from one artifact; nothing is scraped.

For **review** steps, the verdict is still captured from the human's choice in
the TUI (a scalar `output_type`), not from a model.

---

## Step context (engine-assembled)

Every agent step's single user turn is prefixed with a short, **engine-assembled
"Workflow context" preamble** — a deterministic block telling the agent where it
sits in the graph. It is the input/position counterpart to the deterministic
*output* contract (structured outputs, above): the author writes the skill body
about *how to do the job*, and jig supplies *where the job sits* — which steps ran
before it, which steps consume its output, and whether it is a loop re-run.

The preamble is **framing only.** It never inlines an upstream artifact body or a
live sibling status — content still reaches a step through its declared `@ref`
inputs. It carries only ids, statuses, declared purposes, and run state, so it is
deterministic: the same graph position always renders the same bytes (fixed
neighbor ordering — upstream in `depends_on` order, downstream in declaration
order — and no map iteration into the output). It is assembled for **agent steps
only** (command and review steps get none) and prepended ahead of the
skill/agent-file body, separated by a `---` delimiter.

### Rendered format

```
## Workflow context

You are step `plan` in workflow `feature` (iteration 2 of 3).
Purpose: produce the ordered implementation plan
Notes: prefer the smallest change that satisfies the spec

Upstream (already complete):
- `research_backend` (succeeded) — backend findings
- `research_frontend` (succeeded)
These reach you as the inputs listed below; this section is orientation only.

Downstream (what your output feeds):
- `plan_review` (human review) — a person reviews your `summary`
- `implement` (agent) — consumes your `tasks`, `approach` (conditional on `plan_review == 'approve'`)

State: re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.

---
```

Each part is emitted only when it applies: the `(iteration N of M)` clause only on
a genuine re-run, the `Purpose`/`Notes` lines only when a `[step.context]` block
supplies them, the Upstream/Downstream blocks only when the step has neighbors,
and the `State:` line only on a loop re-run.

### `inject_context` — the opt-out

The preamble is **on by default.** Set `inject_context = false` on a step to
suppress it (or in `[defaults]` to flip the default for the whole workflow); a
per-step value overrides `[defaults]`. With it off, the step dispatches with a
byte-identical no-context prompt. `inject_context` is agent-only — it is a
load-time error on a command or review step.

### `[step.context]` — author-supplied context (optional)

An agent step may add a `[step.context]` table to *supplement* (never replace) the
graph-derived framing:

```toml
[[step]]
id    = "plan"
type  = "agent"
skill = "skills/plan"

  [step.context]
  purpose = "produce the ordered implementation plan"           # why this step exists
  notes   = "prefer the smallest change that satisfies the spec" # local guidance
```

- `purpose` renders as a `Purpose:` line on the step's own preamble **and**
  propagates onto a consumer's neighbor line: an upstream bullet gains
  `— <purpose>`, and a downstream bullet's derived clause is *replaced* by it. A
  neighbor that declares no purpose stays graph-derived — jig never guesses a
  description.
- `notes` renders as a `Notes:` line on the step's own preamble only.

Both fields are optional; an absent or empty block changes nothing. A
`[step.context]` block together with `inject_context = false` on the same step is
a load-time error (the block would be inert).

---

## Loops (bounded back-edges)

A `[step.loop]` re-runs a prior step (and everything between) with a hard cap,
guaranteeing termination.

```toml
[[step]]
id         = "review"
type       = "review"
depends_on = ["draft"]
review     = "@draft"
output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when           = "review == 'revise'"
  goto           = "draft"          # target step to re-run
  max_iterations = 3                # engine aborts the run past this
  feedback       = "@review"        # becomes an input to the target's next run
```

---

## Interactive input

Two mechanisms let an agent and a human exchange information beyond a review gate.
They are complementary: `block_on` is driven by the **agent** (it knows it needs
clarification), while `max_messages` is driven by the **human** (they have a
follow-up question after seeing the agent's output).

### `block_on` — agent-initiated pause

An agent step may declare `block_on` as a condition expression that references the
step's **own** schema output field (the left-hand side must be the step's own id).

```toml
[[step]]
id         = "security_scan"
type       = "agent"
agent_file = "agents/security-reviewer.md"
block_on   = "security_scan.needs_input"   # must reference this step's own output

  [step.schema]
  needs_input = "bool"    # agent sets this true when it has a question
  question    = "text"    # the agent's question, surfaced in the TUI
  # …other fields…
```

**Execution flow:**

1. The step runs and emits its structured output.
2. The engine evaluates `block_on` against that output.
3. If true, the step transitions to `StatusNeedsInput` and the TUI opens a compose
   box. The human types their answer and submits.
4. The agent **resumes the same session** with the human's response as the next
   query, re-runs, and emits a new structured output.
5. The engine re-evaluates `block_on`. If false, the step succeeds and downstream
   steps proceed. If still true, step 3 repeats.
6. A hard cap of **20 input rounds** applies. Exceeding it is a run error.

`block_on` requires `[step.schema]` — the referenced field must be declared there
and is type-checked by the validator.

### `max_messages` — human-initiated follow-up at a review gate

A review step may declare `max_messages` (default **10**) to allow the human to
send free-text messages to the reviewed agent before giving the final verdict.

```toml
[[step]]
id           = "plan_review"
type         = "review"
depends_on   = ["plan"]
review       = "@plan.summary"             # must target an agent step
output_type  = { enum = ["approve", "revise"] }
max_messages = 5                           # human may send up to 5 messages
```

**Execution flow:**

1. The review step fires; the TUI renders the reviewed content and the verdict
   choices. If the message cap has not been reached, the TUI also offers a compose
   box labelled **[m] message**.
2. The human types a message. The engine resets both the reviewed agent step and
   the review step to pending.
3. The agent re-runs with the message fed in as additional context; the review
   gate re-fires with the updated output.
4. Steps 1–3 repeat until the human submits a verdict or the cap is hit.

`max_messages` only has effect when `review = "@stepid"` (or `@stepid.field`)
targeting an **agent** step. A `review = "diff"` step never enables messaging
because diffs are not re-generated by re-running an agent.

Setting `max_messages = 0` is treated the same as omitting it (the default 10
applies). To disable messaging entirely, set a value that is effectively never
reached, or rely on the fact that `review = "diff"` never enables it.

---

## Validation (the deterministic gate)

Any step may declare `[step.validate]`. Dependents wait until it passes.

```toml
[step.validate]
command         = "go build ./..."             # must exit 0 (runs in the step's worktree)
output_schema   = "schemas/thing.json"         # `output` must match this JSON Schema
output_exists   = true
output_contains = "APPROVED"
```

`output_schema` validates that a step's `output` *file* conforms to a JSON
Schema — useful for a `command` step that writes JSON. Producer **agent** steps
don't need it: their structured output is already schema-enforced by construction
(see "Structured outputs"), so declare `[step.schema]` / `schema_file` instead.

On failure the step's `on_failure` policy applies.

---

## Failure recovery

`on_failure` governs the *automatic* response to a step failure (a non-zero
command, a failed `[step.validate]` gate, or an agent that errors out):

- **`retry`** re-runs the step up to `max_retries` times.
- **`continue`** marks the step failed but lets its dependents run anyway (they
  treat it as a satisfied node).
- **`abort`** (the default) stops scheduling new work for the run.

When the automatic policy is exhausted — `abort`, or `retry` past `max_retries`
— the step does **not** silently tear the run down. It parks in
`awaiting_recovery` and the engine emits a `RecoveryRequest`, keeping the run and
any in-flight sibling steps alive while a human decides. The run monitor surfaces
a recovery gate with three actions:

- **retry** — re-run the step fresh (a new agent session / full prompt).
- **retry with guidance** — resume the *failed agent's* session, feeding the
  captured error plus optional operator guidance back in so it doesn't repeat the
  mistake. Offered only when the failed step has a resumable session (an agent
  step that ran; not a worktree/setup failure).
- **abort** — fail the step and tear the run down (the pre-recovery default).

The retry/resume round-trip is bounded (an internal cap) so the static
termination guarantee holds; the human can always abort instead. Worktree setup
failures (e.g. a git error creating the step branch) route through the same gate
rather than aborting — a retry re-attempts the setup.

---

## Worked example — bug fix with worktree, gate, diff review, revise loop

```toml
[workflow]
name = "bugfix"
version = "1"

[defaults]
permission_mode = "acceptEdits"

[[step]]
id            = "triage"
type          = "agent"
skill         = "skills/triage"
inputs        = ["reports/bug-1234.md"]
output        = ".jig/artifacts/triage.md"
allowed_tools = ["Read", "Grep", "Glob"]

# mutating: worktree defaulted on; no `output` — diff + observed metadata are the result
[[step]]
id            = "fix"
type          = "agent"
depends_on    = ["triage"]
skill         = "skills/fix"
inputs        = ["@triage"]
allowed_tools = ["Read", "Edit", "Write", "Bash"]

  [step.validate]
  command = "go test ./..."

# human reviews the diff and decides
[[step]]
id          = "approve"
type        = "review"
depends_on  = ["fix"]
review      = "diff"
output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when           = "approve == 'revise'"
  goto           = "fix"
  max_iterations = 3
  feedback       = "@approve"
```

> **Note:** A hand-wired `merge` command step is no longer needed. jig runs each
> step on a per-run integration branch and squash-merges step results into it
> automatically. At run end, the run monitor presents a single human-gated merge
> that lands the integration branch onto the user's working branch.

---

## Execution semantics

1. **Parse & validate:** unique ids; forward edges form a DAG (back-edges only
   via `[step.loop]`); every `@ref` (and `.field` path) resolves and its target
   is in `depends_on`; every `skill` dir / schema file exists; every comparison
   value is legal for the type it tests; every `goto` target exists.
2. **Topological execution.** Ready steps run concurrently up to `max_parallel`.
   A step is skipped if its `when` guard is false.
3. **Each agent step** = a fresh context with its `SKILL.md`, resolved input
   paths, allowed tools, and optional worktree. A **producer** additionally runs
   under `--json-schema` (from `[step.schema]` / `schema_file`), and its
   schema-valid `structured_output` is saved as the step's JSON artifact.
4. **After completion**, run `[step.validate]`; on failure apply `on_failure`.
   Evaluate `[step.loop]`; if it fires and the cap isn't hit, re-run the target.
5. **Workflow succeeds** when every reachable terminal step succeeds.

**Stop and reset add no schema surface.** Stopping a running step and resetting
a run to an earlier step are operator actions available in the run monitor — they
add no `.toml` fields and require no changes to a workflow file. `jig validate`
is unaffected. The run-branch integration model (worktrees + squash-per-step)
is what makes reset safe and coherent; the workflow schema simply needs to be
a well-formed DAG.

---

## Security monitoring (`[defaults.security]` / `[step.security]`)

jig wraps every agent step in a two-tier security layer. Security is **on by
default** — no configuration is needed to enable it. Opt out or tune it via
`[defaults.security]` (workflow-wide) and `[step.security]` (per-step
override).

### `[defaults.security]`

```toml
[defaults.security]
enabled            = true          # false disables both tiers for all steps
tier1_enabled      = true          # Tier-1: deterministic guard (LLM-free)
tier2_enabled      = true          # Tier-2: out-of-band LLM monitor fleet
outbound_allowlist = ["api.github.com", "storage.googleapis.com"]
fleet_budget_usd   = 0.10          # per-run Tier-2 cost ceiling; 0 = no limit
concurrency_cap    = 4             # max simultaneous Tier-2 dispatches; 0 = engine default
batch_size         = 5             # transcript entries before forcing a monitor flush
debounce_ms        = 500           # debounce window before flushing
```

| Field | Type | Notes |
|-------|------|-------|
| `enabled` | bool | Disables both tiers when `false`. Default: `true`. |
| `tier1_enabled` | bool | Toggle Tier-1 deterministic guard. Default: `true`. |
| `tier2_enabled` | bool | Toggle Tier-2 LLM monitor fleet. Default: `true`. |
| `outbound_allowlist` | [string] | Hosts permitted for `WebFetch` and curl/wget. Validated as hostnames at load time. |
| `fleet_budget_usd` | float | Per-run Tier-2 spend ceiling. When exceeded, Tier-2 degrades to Tier-1-only without blocking the run. `0` means no ceiling. Must be `>= 0`. |
| `concurrency_cap` | int | Max simultaneous Tier-2 monitor dispatches. Must be `>= 1` when set; `0` uses the engine default. |
| `batch_size` | int | Flush window size (entry count). `0` uses the engine default. |
| `debounce_ms` | int | Flush debounce in milliseconds. `0` uses the engine default. |

### `[step.security]`

A per-step subset of `SecurityConfig`. Fleet-wide fields (`fleet_budget_usd`,
`concurrency_cap`, `batch_size`, `debounce_ms`) are not overrideable per step.

```toml
[[step]]
id         = "security_scan"
type       = "agent"
agent_file = "agents/security-reviewer.md"

  [step.security]
  tier2_enabled = false   # disable Tier-2 fleet monitors on this step only
```

| Field | Type | Notes |
|-------|------|-------|
| `enabled` | bool | Opt this step out of both tiers. |
| `tier1_enabled` | bool | Toggle Tier-1 guard for this step. |
| `tier2_enabled` | bool | Toggle Tier-2 monitors for this step. |
| `outbound_allowlist` | [string] | Step-local host exceptions; inherits from `[defaults.security]` when empty. |

**Inheritance** follows the same zero-value precedence as `model`/`effort`: an
explicit per-step value wins, else `[defaults.security]`, else the engine's
built-in default (on).

For the full two-tier architecture, findings format, redaction guarantee, and
escalation policy, see [`docs/security-monitoring.md`](security-monitoring.md).

---

## Visualizing a workflow (TUI chart view)

The read-only workflow detail screen (open a workflow from the picker) shows the
flat step list by default and can toggle to a **chart view** — a mermaid-style
top-down flowchart of the graph — with **`v`**. Press `v` again to return to the
list. The toggle only appears once the workflow passes full validation (an
invalid file shows its errors instead).

The chart is a direct, deterministic drawing of the constructs above:

- **Nodes** are steps, boxed and colored by `type` (agent / command / review),
  the same palette the step list uses for its type badges. A `⇢` marks a step
  with a `[step.validate]` gate; a `↺` marks a step that carries a `[step.loop]`.
  The gate's check is spelled out next to the box (`⇢ go build`, `⇢ exists`, …).
- **Layers (top → bottom)** are longest-path ranks over `depends_on`: a step sits
  one row below its deepest dependency, so edges always flow downward. Steps in
  the same rank are ordered left-to-right by their position in the file.
- **Edges** are `depends_on` links, drawn with elbow connectors that fan out from
  a parent and fan in above a child (`▼`).
- **Conditional edges** — the one edge a step's `when` guard decorates — use a
  hollow arrowhead (`▽`) in the conditional color, since `when` gates an existing
  dependency rather than adding a new one. The guard is labeled beside the edge
  in compact form (e.g. `review == approve`).
- **Back-edges** — bounded `[step.loop]` goto targets — are a distinct class
  routed up a dedicated channel on the right (`↺`, `◄`), reflecting that they are
  the only cycles in the graph and are capped by `max_iterations`. The channel is
  captioned with the loop guard and its bound (e.g. `review == revise  ≤3`).

Long labels are truncated with an ellipsis; the guard-labeled edges reserve an
extra row between ranks so the text never overlaps a connector.

The chart is laid out to fit the panel width. A graph wider than the panel (a
rank with many parallel steps) renders at its natural width; in chart mode the
`←`/`→` arrows (and vim `h`/`l`) scroll horizontally as the escape hatch — in the
list view those keys keep their normal meaning.

**MVP exclusions.** The layout does not do crossing-minimization: within-rank
order is purely file order, so an edge that spans more than one rank passes
behind the intervening node boxes rather than routing around them. Column
alignment between ranks is centered, not optimized to reduce edge crossings.

---

## MVP 1 scope

**In:** DAG orchestration, parallel/sync, worktree isolation, deterministic
gates, engine-observed metadata, `review` (human-in-the-loop) steps, scalar
`output_type` verdicts, schema-enforced producer output (`[step.schema]` /
`schema_file`) with `stepid.field` refs, forward `when` conditionals, bounded
`[step.loop]`.

**Deferred:** map/fan-out over a dynamic list (N parallel steps from data),
checkpoint/resume of a partial run, secrets management, remote/distributed
execution.
```
