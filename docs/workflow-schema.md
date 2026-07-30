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
max_parallel        = 4
artifacts_dir       = ".jig/artifacts"   # run artifacts live outside the working tree
```

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
| `on_failure`  | string   | `"abort"` (default), `"retry"`, `"continue"`.                    |
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
| `model` / `fallback_model` / `effort` / `max_turns` / `max_thinking_tokens` / `max_budget_usd` / `permission_mode` | | Override `[defaults]`. |

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

### Command step

Deterministic script/command; no agent context.

| Field    | Type    | Notes                                              |
|----------|---------|----------------------------------------------------|
| `run`    | string  | Shell command (runs in `cwd`). Exactly one of run/script. |
| `script` | path    | Script file to execute.                            |
| `inputs` | [string]| `@stepid` refs / paths made available.             |
| `output` | path    | Optional file the command writes.                  |

### Review step (human-in-the-loop)

Pauses the run, renders an upstream artifact for a human, and captures a verdict.

| Field         | Type     | Notes                                                     |
|---------------|----------|-----------------------------------------------------------|
| `review`      | string   | `@stepid` (glamour-render that markdown) or `"diff"` (render the upstream worktree diff). |
| `output_type` | table    | The decision, e.g. `{ enum = ["approve", "revise"] }`. Captured from the TUI. |

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
- **Parallel mutating steps** must branch from the same base; a downstream join
  step merges them (and errors on conflict). `validate` commands run *inside*
  the step's worktree.

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

[[step]]
id         = "merge"
type       = "command"
depends_on = ["approve"]
when       = "approve == 'approve'"
run        = "git merge --no-ff jig/bugfix/fix"
```

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
