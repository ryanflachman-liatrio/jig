# Architecture

How jig is put together, why it is shaped this way, and where the seams are.
For the *user-facing* workflow spec, see [`workflow-schema.md`](workflow-schema.md).

## The big picture

jig has two halves that meet at the `.toml` workflow file:

```
                        ┌──────────────────────────────┐
   author a workflow →  │  internal/workflow            │
   .toml file           │  parse · default · validate   │  ← statically checked
                        └───────────────┬──────────────┘
                                        │ *Workflow (a validated DAG)
                                        ▼
                        ┌──────────────────────────────┐
                        │  internal/engine  (planned)   │
   run the workflow  →  │  traverse DAG · gates · loops │
                        │  ┌─ runner · step · manifest ─┐│
                        │  │ worktrees · agents · shell ││
                        └──┴────────────┬──────────────┴┘
                                        │ SDK stream / results
                                        ▼
                        ┌──────────────────────────────┐
   watch / review    →  │  internal/tui  (Bubble Tea)   │  ← human-in-the-loop
                        │  chat today → run monitor next │
                        └──────────────────────────────┘
```

- The **workflow package** turns text into a validated in-memory graph. It is
  pure and deterministic — no I/O beyond reading referenced files during
  validation, no agent calls. This is the mature, tested core.
- The **engine** (and its helper packages) will *execute* that graph. Not yet
  implemented; the schema already describes its intended runtime behavior.
- The **TUI** is the human surface — today a streaming Claude chat client, on
  the path to becoming the run monitor and the host for `review` steps.

## Package layout

```
cmd/jig/            entry point: `jig validate <file>` subcommand + the TUI
internal/
  workflow/         schema, loader, validator, guard/condition parser   [DONE]
  tui/              Bubble Tea app: streaming chat client                [DONE]
  engine/           DAG executor / orchestrator                         [planned]
  runner/           runs an individual step to completion               [planned]
  step/             per-step execution state & result model             [planned]
  manifest/         run manifest / persisted run records                [planned]
  datastore/        artifact & metadata persistence under .jig/         [planned]
examples/           worked workflows, skills, agent files, JSON schemas
docs/               this file, TESTING.md, workflow-schema.md
```

Everything lives under `internal/` so nothing is part of a public API surface —
jig is an application, not a library.

## `internal/workflow` — the deterministic core

Responsibility: read a `.toml` file and either return a fully validated
`*Workflow` or a precise error. If this package returns no error, the graph is
guaranteed well-formed: unique ids, a real DAG (back-edges only via
`[step.loop]`), every `@ref` and `.field` path resolves, every guard comparison
is legal for the type it tests, and every loop is bounded.

File-by-file:

| File | Responsibility |
|------|----------------|
| `schema.go` | The core types: `Workflow`, `Meta`, `Defaults`, `Step`, `Input`, `OutputType`, `Field`, `Schema`, and the step/field/failure enums. This is the data model everything else operates on. |
| `load.go` | `Load(path)` / `Decode(data, baseDir)`: TOML decode, `applyDefaults()` (fold `[defaults]` into steps, build the id→index map), then validate. `baseDir` roots file-existence checks; `""` skips them for tests. |
| `validate.go` | The static validator — structural, referential, and type checks. The bulk of the package's rules live here. |
| `condition.go` | `ParseCondition` for `when` / `loop.when` guards: parses `stepid`, `stepid.field.path`, and the `truthy | == | !=` operators. |
| `agent_file.go` | Parses a Claude agent `.md` file (frontmatter + body) and folds its `tools`/`model` into a step when the step leaves them unset. |
| `schema_json.go` | `ParseJSONSchema` (raw JSON Schema → the internal `Field` model) and `Schema.JSONSchema()` (compile back out to JSON Schema for constrained decoding). |

### Key design decisions

- **DAG + bounded back-edges, not a state machine.** Forward edges (`depends_on`)
  form a DAG; the *only* backward edges are `[step.loop]` with a mandatory
  `max_iterations`. This is what buys static validation, visualization, and a
  termination guarantee. Preserve this invariant — do not add a construct that
  lets control flow jump arbitrarily.
- **Data edges are ordering edges.** Every `@stepid` input reference must also
  appear in `depends_on`. The validator enforces it, so you can never read from
  a step that isn't guaranteed to have run.
- **Two agent archetypes.** *Mutators* change the repo; their result is the diff
  plus engine-observed metadata, and they declare no output shape. *Producers*
  reach a conclusion the workflow consumes, captured as **schema-enforced JSON**
  (via `[step.schema]` or `schema_file`) — never scraped from prose. `stepid.field`
  references are type-checked against that schema at load time.
- **Worktree isolation is inferred, then overridable.** An agent step whose
  `allowed_tools` include mutating tools (`Edit`/`Write`/`Bash`/`NotebookEdit`)
  defaults to `isolation = "worktree"`; `isolation = "none"` opts out.
- **Defaults inherit, step fields win.** `[defaults]` seeds model/effort/limits;
  an explicit field on a step overrides. Same precedence rule when folding an
  `agent_file`'s `tools`/`model` in.

## `internal/tui` — the human surface

A Bubble Tea (Elm-architecture) app: one `model`, `Update`, `View`.

- `app.go` — the model and the `Update`/`View` loop; keyboard handling, focus
  toggling between input and output panes, turn navigation, resize reflow.
- `client.go` — Claude Agent SDK integration: connects a **persistent** client
  with partial-message streaming, drains the message channel in a background
  command, and extracts text deltas from `content_block_delta` StreamEvents.
- `turn.go` — a `turn` is one Q&A pair (question, accumulating answer, cached
  rendered markdown, scroll offset).
- `viewer.go` — viewport rendering: stick-to-bottom while streaming, lazy
  glamour markdown rendering cached per completed turn, cache invalidation on
  resize.
- `styles.go` — Lipgloss styles, adaptive to light/dark terminal background.

Message flow for one exchange: user submits (`ctrl+s`) → append a `turn`, set
`streaming` → `submitPromptCmd` sends on the persistent client →
`waitForClaudeMessageCmd` drains the channel → each `claudeDeltaMsg` appends to
the streaming turn and re-renders → `claudeTurnCompleteMsg` ends streaming and
the final answer is glamour-rendered once and cached.

**Why the background is detected in `main`, not here:** querying terminal
background after Bubble Tea owns stdin races the input reader and injects the
terminal's OSC reply as garbled keystrokes. So `main` reads it once up front and
passes it into `tui.New`.

## Where the engine will plug in (planned)

The schema already specifies the engine's contract; these are the seams to fill:

1. **Run directory.** `.jig/runs/<run-id>/` holds `artifacts/` (where `@stepid`
   resolves) and `steps/<step-id>/result.json` (engine-owned metadata). Kept
   outside the working tree so it survives worktree switches.
2. **Traversal.** Topologically order the validated DAG; run ready steps
   concurrently up to `max_parallel`; skip a step whose `when` guard is false.
3. **Step execution** (`runner`/`step`):
   - *agent* → fresh context from `SKILL.md`/agent file + resolved input paths +
     allowed tools + optional worktree. Producers additionally run headless
     under `--json-schema` so the final answer is constrained-decoded to the
     step's schema and saved as its JSON artifact.
   - *command* → run `run`/`script` in `cwd` (inside the worktree for mutating
     lineages).
   - *review* → pause, render the artifact/diff in the TUI, capture the human
     verdict.
4. **Gates & loops.** After a step, run `[step.validate]`; on failure apply
   `on_failure` (`abort`/`retry`/`continue`). Evaluate `[step.loop]`; if it fires
   and the cap isn't hit, re-run the target with `feedback` fed back in.
5. **Engine-observed metadata.** Status, changed files, tool-call log, and
   duration are derived from the SDK message stream — the engine sees every
   `Write`/`Edit`/`Bash` — not asked of the agent. This is the record gates trust.

## Dependencies

- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) — TUI framework (Elm architecture).
- `bubbles` (spinner, textarea, viewport), `lipgloss` (styling), `glamour` (markdown rendering) — the Charm stack.
- [`github.com/severity1/claude-agent-sdk-go`](https://github.com/severity1/claude-agent-sdk-go) — Claude Agent SDK client used by the TUI.
- [`github.com/BurntSushi/toml`](https://github.com/BurntSushi/toml) — workflow file parsing.

## Invariants worth protecting

- A `*Workflow` returned without error is fully valid — downstream code never
  re-checks structure.
- The workflow package stays pure/deterministic; agent calls and shell execution
  belong in the engine, never in parse/validate.
- Control flow is a DAG plus bounded back-edges. Termination is guaranteed and
  must stay that way.
- Producer conclusions are schema-enforced JSON; human-facing output is markdown
  (a `summary` field convention carries the prose inside the JSON). Nothing is
  scraped from model prose.
