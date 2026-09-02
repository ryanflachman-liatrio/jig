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
backend             = "claude"       # agent vendor (claude or cursor today)
transport           = "sdk"          # sdk | acp (how jig reaches the backend)
max_parallel        = 4
resource_limits     = { research = 4, mutation = 1, checks = 2 } # optional per-class caps
max_read_only       = 6                # optional capacity for isolation = "none"
max_mutating        = 1                # optional capacity for worktree steps
max_cost_usd        = 20.0             # run-wide observed-cost ceiling; 0 is unlimited
max_security_findings = 3              # unique findings allowed before dispatch stops; 0 is unlimited
max_network_requests = 50              # observed outbound agent-tool calls; 0 is unlimited
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
    workflow.json       # locked root workflow, expanded module sources, and digests
    steps/<step-id>/
      result.json       # engine-owned metadata (see "Engine-observed metadata")
```

---

## The step model

Steps are declared with `[[step]]`. Five author-facing types: `agent`,
`command`, `check`, `review`, and `subworkflow`.

### Common fields

| Field         | Type     | Notes                                                            |
|---------------|----------|------------------------------------------------------------------|
| `id`          | string   | Required. Unique. Used by `depends_on`, `@id` refs, and `when`.  |
| `type`        | string   | `"agent"`, `"command"`, `"check"`, `"review"`, or `"subworkflow"`. |
| `depends_on`  | [string] | **Always explicit.** Step ids that must finish first.            |
| `when`        | string   | Guard expression; step runs only if true. See "Conditionals".    |
| `output`      | path     | Single output file (content). **Optional.**                     |
| `output_type` | see below| `"text"` (default) or a scalar verdict. See "Structured outputs". |
| `on_failure`  | string   | `"abort"` (default), `"continue"`, or legacy `"retry"`; new retry contracts use `[step.retry]`. |
| `timeout` | duration | Per-attempt deadline such as `"30s"`; zero is unbounded. |
| `[step.retry]` | table | Automatic retry contract; requires `idempotent = true`. |
| `resource_class` | string | Optional named concurrency class declared in `resource_limits`.       |
| `mutation_paths` | [path/glob] | Allowed repository paths for a worktree step; unexpected diffs fail before integration. |
| `secrets` | [string] | Named externally resolved references; values are never stored in TOML. |
| `[step.validate]` | table| Deterministic gate. See "Validation".                            |
| `[[step.route]]` | table | Ordered bounded back-edge. See "Routes".                           |

### Subworkflow modules

A reusable workflow module is a TOML file with a schema-versioned interface.
It is not independently runnable: a parent invokes it through a
`type = "subworkflow"` step and provides every required input explicitly.

```toml
# modules/spec.toml
[module]
schema_version = 1

[module.inputs.request]
type = "text"

[module.inputs.include_api]
type = "bool"
required = false

[module.exports.document]
ref = "@write_spec.document"

[module.exports.coverage_profile]
artifact = "@run_tests.coverage_profile"

[[step]]
id = "write_spec"
type = "agent"
skill = "skills/write-spec"
inputs = ["@module.request"]
  [step.schema]
  document = "text"
```

Module input types are `text`, `number`, `bool`, `enum`, and `artifact`; enum
inputs declare their complete `enum` set. `@module.name` is only legal inside a
module. Exports may expose a module-internal scalar or typed field with `ref`, or a
declared check artifact with `artifact`, but never an internal worktree path.

The parent gives the module its public name, source path, and bindings. Parent
steps can depend on the invocation and consume a named export; they cannot
reference internal step IDs.

```toml
[[step]]
id = "spec"
type = "subworkflow"
depends_on = ["collect_request"]
module = "modules/spec.toml"          # relative to this workflow file
with = { request = "@collect_request.request" }

[[step]]
id = "review_spec"
type = "review"
depends_on = ["spec"]
inputs = ["@spec.document"]
output_type = { enum = ["approve", "revise"] }
  [[step.review]]
  source = "@spec.document"
  label = "Specification"
```

At load time jig resolves module paths relative to their referring workflow,
expands execution IDs as `parent__internal`, rewrites dependencies, inputs, and
route targets, then validates the complete expanded graph before dispatch. The
TUI retains the compact parent graph. A run snapshot includes the root source,
every module source, and their SHA-256 digests; resume rebuilds from that locked
data rather than reading modules from the checkout.

### Check step

A `check` is a deterministic command with a machine-readable outcome rather
than an engine failure. It must declare an enum containing `pass`, `fail`,
`skip`, and `error`, an `applies_when` guard, and a versioned
`[step.findings]` interface. A false `applies_when` is the only way a check can
produce `skip`; otherwise the tool must write its own findings JSON with a
`pass`, `fail`, or `error` outcome. Process exit status is not a verdict: a
startup failure, cancellation, unavailable declared tool, unreadable findings,
or malformed findings becomes `error`. Every attempt's command log and findings
JSON are separately copied under the step's run directory, so looping does not
overwrite prior evidence.

Every check must also route `fail` and `error` back to a remediation or review
step. A single `when = "check_id != 'pass'"` route is the usual form: `skip`
never reaches route selection because the engine produces it before dispatch.

Findings schema version 1 is an exact JSON object:

```json
{
  "schema_version": 1,
  "outcome": "pass",
  "findings": [
    {"id": "unique-check-id", "severity": "info", "message": "details"}
  ]
}
```

`outcome` is `pass`, `fail`, or `error`; `findings` is always an array and each
entry has a non-empty `id` and `message`, plus severity `info`, `warning`, or
`error`. The command must declare every external executable it relies on in
`required_tools`; jig verifies these before dispatch.

`findings.artifacts` optionally names other check outputs that downstream steps
need. Its values are paths relative to the producer's execution directory. Jig
copies each declared file into the per-attempt evidence directory before a
dependent dispatches, then consumers bind it explicitly with
`{ artifact = "@producer.export", as = "name" }`. Command consumers receive the
absolute snapshot path as `JIG_INPUT_NAME`; they never receive a producer
worktree path.

```toml
[[step]]
id             = "unit_tests"
type           = "check"
depends_on     = ["implement"]
applies_when   = "implement.ready"
resource_class = "checks"
isolation      = "worktree"
output_type    = { enum = ["pass", "fail", "skip", "error"] }
run            = """
if go test ./...; then outcome=pass; else outcome=fail; fi
printf '{\"schema_version\":1,\"outcome\":\"%s\",\"findings\":[]}' "$outcome" > .jig/unit-tests.findings.json
"""

  [step.findings]
  schema_version = 1
  file = ".jig/unit-tests.findings.json"
  required_tools = ["go"]
  artifacts = { coverage_profile = ".jig/coverage.out" }

[[step.route]]
when           = "unit_tests != 'pass'"
goto           = "implement"
max_iterations = 3
feedback       = "@unit_tests"
```

```toml
[[step]]
id         = "coverage"
type       = "check"
depends_on = ["unit_tests"]
inputs     = [{ artifact = "@unit_tests.coverage_profile", as = "coverage_profile" }]
# Its command reads "$JIG_INPUT_COVERAGE_PROFILE".
```

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
`description`, `disable-model-invocation: true`) + instruction body. The
invocation flag prevents backends with native skill discovery from separately
loading instructions that jig already supplies. At load time jig validates the
standard frontmatter and snapshots the instruction body into the step's
effective prompt; the agent does not need to locate or reread `SKILL.md`. The
body is therefore visible in the persisted `input.md` alongside the resolved
inputs and output instructions. This keeps prose out of the TOML and makes
skills reusable.

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
| `backend` | `claude` | `claude` \| `cursor` \| `codex` | Vendor. Gemini is not implemented yet. |
| `transport` | backend-aware | `sdk` \| `acp` | Claude defaults to `sdk` and supports `sdk` or `acp`; Cursor and Codex use `acp`. ACP reaches Claude through `@agentclientprotocol/claude-agent-acp@0.70.0`, Cursor through native `cursor-agent acp`, and Codex through `@agentclientprotocol/codex-acp@1.6.2`. |

Inheritance matches `model` / `effort`: step → `[defaults]` → a backend-aware
default (`claude` → `sdk`, `cursor` / `codex` → `acp`). Unknown values and invalid
backend/transport pairs fail at `jig validate`. Capability mismatches (e.g.
`transport = "acp"` with `[step.schema]`) fail closed at execute time.

Codex's CLI has no native ACP server. `backend = "codex"` starts the
`@agentclientprotocol/codex-acp` stdio adapter, which drives the Codex App
Server using the operator's existing Codex login. Do not use `codex exec` or
Codex's MCP server as a substitute; see [Codex ACP compatibility gate](research/codex-acp.md).

The run monitor shows file patches only when an ACP adapter emits standard
tool-call diff or location detail. A completed edit with no such detail shows
`Adapter did not provide edit details.`; jig does not infer patches from tool
titles or the workspace.

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

```toml
[defaults]
backend   = "codex"
transport = "acp"
```

### Command step

Deterministic script/command; no agent context.

| Field    | Type    | Notes                                              |
|----------|---------|----------------------------------------------------|
| `run`    | string  | Shell command (runs in `cwd`). Exactly one of run/script. |
| `script` | path    | Script file to execute, resolved from the **project (git repo) root** (e.g. `.agents/jig/scripts/verify-proof-files.sh`). A multi-line value is an inline script body. |
| `inputs` | [string]| `@stepid` refs / paths made available.             |
| `output` | path    | Optional file the command writes.                  |

### Review step (human-in-the-loop)

Pauses the run, renders an upstream artifact for a human, and captures a verdict.

| Field          | Type     | Notes                                                     |
|----------------|----------|-----------------------------------------------------------|
| `review`       | array    | Ordered `[[step.review]]` targets with `source` and `label`; sources may be refs, `diff`, or literal files. |
| `output_type`  | table    | The decision, e.g. `{ enum = ["approve", "revise"] }`. Captured from the TUI. |

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

## Production execution controls

Every automatic retry is an explicit safety contract. `max_attempts` includes
the initial dispatch; the terminal `on_failure` policy applies only after the
declared classes are exhausted. The engine never retries a step unless the
author has declared `idempotent = true`.

```toml
[[step]]
id = "fetch_metadata"
type = "command"
run = "./scripts/fetch-metadata.sh"
timeout = "45s"
idempotent = true

  [step.retry]
  max_attempts = 3
  backoff = "exponential"              # none | fixed | exponential
  initial_backoff = "1s"
  retry_on = ["timeout", "temporary"] # timeout | temporary | exit_failure | agent_error
```

`max_parallel` remains the global ceiling. `resource_limits` applies an
additional named-class ceiling; `max_read_only` and `max_mutating` independently
limit read-only execution views and mutating worktrees. A zero partition limit
is unbounded, subject to `max_parallel`.

Any worktree diff is checked before it is staged or merged. `mutation_paths`
uses repository-relative paths, normal globs, or a recursive `dir/**` prefix.
When declared, its allowlist is fail-closed; workflows that omit the field keep
the baseline worktree integration policy.

```toml
[[step]]
id = "implement"
type = "agent"
isolation = "worktree"
mutation_paths = ["internal/**", "cmd/**", "go.mod", "go.sum"]
```

Secrets are only names in a workflow and are resolved by the embedding
application immediately before dispatch. Command and check steps receive named
values as `JIG_SECRET_<NAME>` environment variables. Values are redacted from
prompts, transcripts, logs, evidence, findings, and generated output artifacts;
they are never placed in a workflow or run snapshot.

```toml
[[step]]
id = "publish"
type = "command"
secrets = ["release_token"]
run = "./scripts/publish.sh"
```

The scheduler enforces `max_cost_usd` before each new dispatch using all
observed attempt spend, `max_security_findings` using unique engine security
findings, and `max_network_requests` through guarded outbound agent tool calls.
When a budget is exhausted, pending work fails closed rather than being
dispatched. Input files are copied into an immutable per-attempt run
snapshot with SHA-256 digests before a consumer starts; snapshot and evidence
filenames include generation, iteration, and attempt.

Bundled SDD checks use explicit repository quality profiles rather than
auto-detecting a package manager or linter; see
[quality profiles](quality-profiles.md).

## Execution snapshots and worktrees

Every persisted worker receives an `ExecutionDir`: a stable, run-owned snapshot
of the integrated repository state captured when that worker dispatches. This
is an engine execution field rather than workflow TOML. Relative runtime paths
(literal inputs, declared outputs, check findings, and dynamic review files)
resolve beneath it, so a dependency's integrated files are visible without
exposing its private mutation worktree.

`isolation = "worktree"` gives a mutating step its own `ExecutionDir`; read-only
steps receive ephemeral execution-view worktrees at the same run-branch commit.
Views are distinct per dispatch and removed when their worker completes, so
concurrent readers neither observe later integration nor modify the central run
checkout. With persistence disabled, `ExecutionDir` is empty and the process
working directory remains the fallback.

- **Default on** for agent steps whose `allowed_tools` include mutating tools
  (`Edit`/`Write`/`Bash`); override with `isolation = "none"`.
- **Branch name is derived by convention:** `jig/<workflow>/<run-id>/<step-id>`.
  No injected variable is needed; the scheduler owns branch lifecycle.
- **Each step worktree branches off the run-branch HEAD**, not repo-root HEAD.
  This means each step sees the accumulated code changes produced by its upstream
  steps. When the step completes, jig squash-merges its worktree branch back into
  the run branch as one commit. Integration is the engine's responsibility — no
  explicit `merge` command step is needed.
- `validate` commands run inside the completed step's `ExecutionDir`, so they
  see the exact state the worker used.

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

## Routes (bounded back-edges)

A `[[step.route]]` re-runs an upstream remediation or review step (and
everything between) with a hard cap, guaranteeing termination.

```toml
[[step]]
id         = "review"
type       = "review"
depends_on = ["draft"]
[[step.review]]
source     = "@draft"
label      = "Draft"
output_type = { enum = ["approve", "revise"] }

[[step.route]]
when           = "review == 'revise'"
goto           = "draft"          # target step to re-run
max_iterations = 3                # engine aborts the run past this
feedback       = "@review"        # becomes an input to the target's next run
```

Routes are evaluated in declaration order; guards must be non-ambiguous and
either exhaust the source enum/bool domain or end in `fallback = true`. Every
selection and cap exhaustion is recorded as a distinct journal event.

```toml
[[step.route]]
when           = "checkpoint == 'redo'"
goto           = "implement"
max_iterations = 3
feedback       = "@checkpoint"

[[step.route]]
when           = "checkpoint == 'continue'"
goto           = "next_task"
max_iterations = 12
feedback       = "@checkpoint"
```

---

## Interactive input

The agent can pause for clarification with `block_on`; review feedback is
collected as a structured, batched submission in the review workspace.

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

### Review workspace feedback

A review step opens the workspace so the human can inspect every declared
document, annotate source ranges, and submit the verdict with a complete batch
of comments and an optional summary.

**Execution flow:**

1. The review step fires and creates an immutable round snapshot for each target.
2. The workspace renders source or Markdown preview, mapping preview blocks back
   to source lines for stable comments.
3. The human acknowledges every document and submits one atomic verdict batch.
4. The engine verifies snapshot digests and rejects incomplete or stale batches.

### Review targets

Every `[[step.review]]` target has a non-blank, unique `label` and exactly one
of `source` or `file`:

| Form | Meaning |
|------|---------|
| `source = "@step.field"` | Render the text stored in a structured output field. |
| `source = "@step"` | Render the producer's primary output artifact. |
| `source = "diff"` | Render captured diffs from transitive dependencies. |
| `source = "notes.md"` | Render a static file resolved relative to the workflow file. |
| `file = "@step.path_field"` | Dereference a text field as a repository-relative file in the review dispatch snapshot. |

`file` is for an upstream-produced pathname, not the pathname text itself. It
must name a text field from a direct dependency; absolute paths, parent escapes,
symlink escapes, directories, binary files, and oversized files fail closed.
The review snapshot records both the logical field reference and its resolved
repository-relative path, then replays that immutable content without rereading
the source file.

```toml
[[step]]
id = "architecture_gate"
type = "review"
depends_on = ["write_spec"]
output_type = { enum = ["approve", "revise"] }

  [[step.review]]
  file = "@write_spec.spec_path"
  label = "Specification"

  [[step.review]]
  source = "@write_spec.summary"
  label = "Author orientation"
```

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
a recovery gate with four actions:

- **retry** — re-run the step fresh (a new agent session / full prompt).
- **retry with guidance** — resume the *failed agent's* session, feeding the
  captured error plus optional operator guidance back in so it doesn't repeat the
  mistake. Offered only when the failed step has a resumable session (an agent
  step that ran; not a worktree/setup failure).
- **skip** — accept the failed step and continue scheduling its dependents as if
  it used `on_failure = "continue"`.
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
[[step.review]]
source      = "diff"
label       = "Code changes"
output_type = { enum = ["approve", "revise"] }

[[step.route]]
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
   via `[[step.route]]`); every `@ref` (and `.field` path) resolves and its target
   is in `depends_on`; every `skill` dir / schema file exists; every comparison
   value is legal for the type it tests; every `goto` target exists.
2. **Topological execution.** Ready steps run concurrently up to `max_parallel`.
   A step is skipped if its `when` guard is false.
3. **Each agent step** = a fresh context with its `SKILL.md`, resolved input
   paths, allowed tools, and optional worktree. A **producer** additionally runs
   under `--json-schema` (from `[step.schema]` / `schema_file`), and its
   schema-valid `structured_output` is saved as the step's JSON artifact.
4. **After completion**, run `[step.validate]`; on failure apply `on_failure`.
   Evaluate routes; if one fires and its cap isn't hit, re-run the target.
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
  with a `[step.validate]` gate; a `↺` marks a step that carries a route.
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
- **Back-edges** — bounded route targets — are a distinct class
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
`[[step.route]]`.

**Deferred:** map/fan-out over a dynamic list (N parallel steps from data),
arbitrary in-flight agent checkpoint/resume, secrets management,
remote/distributed execution. An unfinished historical run parked only on
document-review gates is shown as `paused` and can restore a live scheduler;
workers interrupted mid-execution remain recovery cases because their backend
process and session may no longer exist.
```
