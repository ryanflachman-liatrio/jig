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
                        │  internal/engine               │
   run the workflow  →  │  traverse DAG · gates · loops │
                        │  ┌─ runner · step · manifest ─┐│
                        │  │ worktrees · agents · shell ││
                        └──┴────────────┬──────────────┴┘
                                        │ SDK stream / results
                                        ▼
                        ┌──────────────────────────────┐
   operate / review  →  │  internal/tui + internal/ops   │
                        │  interactive UI + scriptable CLI│
                        └──────────────────────────────┘
```

- The **workflow package** turns text into a validated in-memory graph. It is
  pure and deterministic — no I/O beyond reading referenced files during
  validation, no agent calls. This is the mature, tested core.
- The **engine** (and its helper packages) executes that graph — DAG traversal,
  gates, loops, run-branch worktree management, and the event journal.
- The **TUI** is the human surface — a streaming Claude chat client and a
  navigable run monitor for driving and reviewing in-flight workflows.
- **ops + headless** are the non-TUI surface — immutable status/log/doctor
  reports and guarded historical control, supervised by the same event-policy
  loop as a newly started run.

## Package layout

```
cmd/jig/            thin command parsing/rendering + bare-command TUI entry
internal/
  workflow/         schema, loader, validator, guard/condition parser   [DONE]
  tui/              Bubble Tea app: chat client + navigable run monitor  [DONE]
  engine/           DAG executor / orchestrator + event bus + run-branch lifecycle [DONE]
  runner/           concrete executors (agent SDK, shell command)       [DONE]
  step/             per-step execution state & result model             [DONE]
  transcript/       per-step transcript.jsonl store (writer + reader)   [DONE]
  manifest/         journal.jsonl writer + per-step result.json         [DONE]
  datastore/        run-dir layout, path helpers, retention under .jig/ [DONE]
  ops/              status/log/doctor reports + resume/reset preflight  [DONE]
  headless/         shared start/resume supervision and gate policy      [DONE]
  telemetry/        optional OTel/Prometheus/OTLP exporter (subscriber)  [DONE]
.agents/jig/        executable workflows, scripts, and workflow templates
.agents/skills/     reusable agent skills used by those workflows
docs/               this file, TESTING.md, workflow-schema.md
```

Everything lives under `internal/` so nothing is part of a public API surface —
jig is an application, not a library.

## `internal/workflow` — the deterministic core

Responsibility: read a `.toml` file and either return a fully validated
`*Workflow` or a precise error. If this package returns no error, the graph is
guaranteed well-formed: unique ids, a real DAG (back-edges only via
`[[step.route]]`), every `@ref` and `.field` path resolves, every guard comparison
is legal for the type it tests, and every loop is bounded.

File-by-file:

| File | Responsibility |
|------|----------------|
| `schema.go` | The core types: `Workflow`, `Meta`, `Defaults`, `Step`, `Input`, `OutputType`, `Field`, `Schema`, and the step/field/failure enums. This is the data model everything else operates on. |
| `load.go` | `Load(path)` / `Decode(data, baseDir)`: TOML decode, `applyDefaults()` (fold `[defaults]` into steps, build the id→index map), then validate. `baseDir` roots file-existence checks; `""` skips them for tests. |
| `validate.go` | The static validator — structural, referential, and type checks. The bulk of the package's rules live here. |
| `condition.go` | Bounded lexer/parser and expression AST for every guard surface: dotted references, truthiness, typed equality/ordering, `&&`, `||`, grouping, canonical formatting, and reference visitors/rewriting. |
| `agent_file.go` | Parses a Claude agent `.md` file (frontmatter + body) and folds its `tools`/`model` into a step when the step leaves them unset. |
| `schema_json.go` | `ParseJSONSchema` (raw JSON Schema → the internal `Field` model) and `Schema.JSONSchema()` (compile back out to JSON Schema for constrained decoding). |

### Key design decisions

- **DAG + bounded back-edges, not a state machine.** Forward edges (`depends_on`)
form a DAG; the *only* backward edges are `[[step.route]]` with a mandatory
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
  defaults to `isolation = "worktree"`; `isolation = "none"` opts out. When
  running in a worktree, the step's branch is created off the current tip of the
  **run branch** (not repo-root HEAD), so each step sees the accumulated code
  changes produced by its upstream steps — see the integration model below.
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

### Document review workspace

The Monitor opens `internal/tui/review.Model` as a child workspace for queued
review gates. Each immutable document keeps its own transient presentation
mode: source-addressable Markdown opens in rendered preview, while diffs and
plain text always open as verbatim source. Switching documents restores that
document's presentation choice for the lifetime of the workspace; presentation
state is not persisted in the review draft.

Markdown preview is presentation only. Goldmark maps each top-level block to an
inclusive range of logical source lines, Glamour renders the block, and the TUI
decorates it with its source range and overlapping comment state. Cursor
movement, comments, anchors, draft projection, and submission continue to use
the immutable source document and one-based logical lines—rendered terminal
rows never become review identity. Non-Markdown content is never passed through
Glamour.

Comment bodies are transient disclosure rather than part of the document
layout. The review surface keeps only line/block markers visible; `c` opens a
centered modal for a new comment, while `enter` on a marked line, block, or
selected range opens the intersecting comment in the same modal for editing.
Closing the modal restores the unchanged document viewport, so accumulated
comments cannot push the editor or active source content outside the workspace.

When a review step is merely selected, the Monitor's content panel shows the
complete review-document inventory, progress, and—after submission—the verdict
summary. It does not expand one privileged diff into a synthetic transcript.
Opening the review gate switches the Monitor body into a focused, full-width
workspace while retaining the gate bar, run status, and global footer. The
workspace uses the Monitor's outer panel chrome and a flat document rail/content
split, so document browsing does not nest another pair of bordered panels or
compete with an otherwise idle Steps panel.

Source presentation follows a separate, width-independent path. Literal
`.diff` and `.patch` documents are parsed into an ephemeral projection that
keeps every immutable patch row while adding old/new source-file coordinates
for hunk body rows. Those source gutters are only comprehension and navigation
aids: cursors, comments, drafts, and submissions remain anchored to one-based
raw patch lines. `[` and `]` move between parsed hunks; `z` transiently folds
the active hunk body while retaining its header. Parse failures retain the
styled raw patch and show that hunk navigation is unavailable. Markdown source
uses the Markdown lexer; other literal files select Chroma only from the source
filename; unmatched or known-ambiguous matches such as `go.mod` remain plain
text. Chroma tokenizes the complete immutable document so multiline lexer state
survives, then reconstructs exactly one styled item for every logical source
line. Any tokenizer failure or line-count mismatch falls back to the original
verbatim lines.

The review view adds the stable line-number gutter, selection rails, and
comment markers after source highlighting. Parsed diffs replace the single
logical-line display with fixed old/new source gutters; metadata and hunk
headers intentionally leave both blank. Only the styled content segment is
horizontally clipped: `h`/left and `l`/right pan by four terminal cells, while
`0` returns to the first column. Offsets and folds are transient and per
document. Gutters never pan, code never soft-wraps, and anchors continue to
quote bytes from the immutable document rather than ANSI-rendered output.

Message flow for one exchange: user submits (`ctrl+s`) → append a `turn`, set
`streaming` → `submitPromptCmd` sends on the persistent client →
`waitForClaudeMessageCmd` drains the channel → each `claudeDeltaMsg` appends to
the streaming turn and re-renders → `claudeTurnCompleteMsg` ends streaming and
the final answer is glamour-rendered once and cached.

**Why the background is detected in `main`, not here:** querying terminal
background after Bubble Tea owns stdin races the input reader and injects the
terminal's OSC reply as garbled keystrokes. So `main` reads it once up front and
passes it into `tui.New`.

## `internal/transcript` — the per-step conversation store

Each step's full conversation — assistant text, reasoning, tool calls with
their inputs, and tool results — is captured to an append-only JSONL file,
`transcript.jsonl`, beside that step's `result.json`. It is the durable record
the run monitor renders from. The package is pure data + file I/O (`Entry`,
`Block`, an append `Writer`, and a windowed `Reader`); it imports nothing from
engine/runner/tui, mirroring the `internal/step` style.

**File is truth, bus is liveness.** The transcript file — written *directly by
the runner* — is the source of truth for step output. The engine event bus
carries only lightweight liveness signals (`StepMessage{Seq}`), never bulk
content; a dropped signal just means "one seq stale," corrected on the next
read. The TUI renders from disk, never from the lossy bus. Nothing is lost to a
dropped channel send, a monitor re-entry, or a run switch. Bulk content never
rides the bus or `journal.jsonl`.

**Bounded on disk, bounded on screen.** A write-time cap (`MaxBlockBytes`,
default 256 KiB) truncates a pathological block and flags it `truncated`,
protecting the write loop and disk. Separately, the monitor collapses large
blocks to 80 chars with an expand affordance backed by a bounded windowed read —
the whole file is never slurped into memory.

**Unversioned, best-effort read.** The format carries no schema version. Runs
are ephemeral and a `.jig/` dir is not expected to outlive a jig upgrade, so
there is no cross-version compatibility contract to keep (or test, or grow the
line format for). The reader is simply defensive: it skips lines it cannot parse
(a partial trailing line from a crash or a concurrent write), `encoding/json`
drops unknown fields, and unknown block types render as an "unsupported"
placeholder. A stale transcript from a different jig build therefore degrades
gracefully rather than crashing — with no promise it renders correctly. Prune
old runs after upgrading.

**Retention & housekeeping.** Transcripts (and everything else under a run dir)
persist until pruned. `datastore.Prune` / `jig prune` removes finished run
directories by age (`--max-age`) and/or count (`--keep-last`); `--dry-run`
previews via `datastore.Prunable`. Retention is conservative by design: a run is
a deletion candidate **only** once its journal shows a terminal `run_finished`
event, so an in-progress or crashed run is never removed. With no flags, prune
is a no-op.

**Security / PII posture.** Transcripts persist **raw tool output and model
reasoning** to disk under `.jig/` — command stdout/stderr, file contents read by
the agent, and thinking blocks can all contain secrets. `.jig/` is git-ignored
(see `.gitignore`) so it never reaches version control, but it is plaintext on
the local disk. Treat a run directory as sensitive; `jig prune` is the supported
way to clear it.

## Integration model — the run branch

jig runs each step on a per-run **integration branch** (the "run branch"). This
is the key design decision that lets downstream steps build on upstream code
while keeping each step's turn isolated in its own worktree. See
[ADR 0007](adr/0007-run-integration-branch-model.md) for the full rationale.

- **One run branch per run.** When a run starts, jig creates a branch named
  `jig/<workflow>/<run-id>` off the user's working HEAD. This is the mutable
  spine of the run; all step contributions land here.
- **Step worktrees branch off run HEAD.** Each mutating step gets its own git
  worktree branched from the current tip of the run branch. The step therefore
  sees all code changes produced by its upstream steps — not just the baseline
  from which the run started.
- **Squash-per-step.** When a step completes, jig squash-merges the step's
  worktree branch back into the run branch as exactly one commit, tagged with
  the step id. This keeps the run-branch history linear and commit-addressable.
- **Step→commit map.** Because each step's contribution lands as a single
  labeled commit, every step's code changes are retrievable by commit hash. This
  is the foundation that makes commit-addressable reset possible.
- **Final human-gated merge.** At run end, jig presents the run branch for a
  single human-gated merge onto the user's working branch. Explicit `merge`
  command steps are not needed — integration is the engine's responsibility.

**Run directory layout.** `.jig/runs/<run-id>/` lives outside the working tree
so it survives worktree switches:

```
.jig/runs/<run-id>/
  journal.jsonl                – orchestration events (one Envelope/line)
  steps/<step-id>/
    result.json                – terminal step summary
    input.md                   – effective agent prompt for the latest dispatch
    transcript.jsonl           – append-only per-step conversation
  artifacts/                   – producer output (@stepid resolves here)
```

### Stop and reset seam

The run branch's commit-per-step history is what makes operator-driven stop and
reset coherent. See [ADR 0008](adr/0008-manual-reset-rewind-and-replay.md) for
the full algorithm rationale.

- **`Run.Stop`** cancels one running step's worker without ending the run. The
  step transitions to `stopped` — not a failure, not end-of-run. A run is
  **quiescent** when no worker is in flight; quiescence is the precondition for
  reset.
- **Reset** is a git rewind plus survivor replay scoped to the target step's
  dependency closure. Given a target step T, jig computes the reset set (T and
  its transitive `depends_on` closure); rewinds the run branch to the commit
  just before the earliest reset-set commit; cherry-picks any later commits that
  are *not* in the reset set (independent "survivors"); and returns the reset
  set to `pending`. In a linear workflow the reset set is a contiguous tail and
  the cherry-pick step is a no-op.
- Reset is available only on an unfinished, quiescent run. A fully-settled run
  is locked — reopening a finished run to reset it is a deferred follow-up.

## `internal/telemetry` — the observability seam

Optional OpenTelemetry exporter. This is the only package that imports
`go.opentelemetry.io/otel/...`; the engine, runner, and TUI stay OTel-free.

- `EventExporter` attaches to `Manager.Subscribe()` and translates the live
  event stream to OTel metrics (per-run/step counters and histograms, gate/
  review/security counters, tool activity, exporter self-observability). It
  never mutates engine state and never applies backpressure — a slow collector
  drops events and increments a self-observability counter.
- `MetricMux` wraps `runner.Mux` in `cmd/jig/wire.go` so per-step spans and
  duration histograms are recorded at executor boundaries. Zero-cost pass-
  through when telemetry is off, so callers wrap it unconditionally.
- `TelemetryReporter` wraps `engine.Reporter` so per-tool observations
  (`Reporter.ToolCall`, `NetworkRequest`) become counter increments and child
  spans without touching the scheduler.
- Configuration layers: OTel-standard `OTEL_*` env → `JIG_TELEMETRY_*` env →
  `.jig/telemetry.json` → workflow `[telemetry]` block. Defaults leave the
  exporter off; `OTEL_SDK_DISABLED=true` is the kill switch.

Full posture is fixed by [ADR 0012](adr/0012-observability-export.md); the
metric catalog, span catalog, and operator contract live in
[`observability.md`](observability.md).

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
