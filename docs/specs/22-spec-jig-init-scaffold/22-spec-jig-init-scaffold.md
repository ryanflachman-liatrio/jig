# 22-spec-jig-init-scaffold.md

Backlog source: [`docs/plans/open-goals.md`](../../plans/open-goals.md) — **A20**
(P2, Kind **C**): “`jig init` / workflow scaffold — Hand-authored TOML.”
Clarification round: [`22-questions-1-jig-init-scaffold.md`](./22-questions-1-jig-init-scaffold.md)
(all recommendations accepted).

## Introduction/Overview

Today the only way to start using jig in a repository is to hand-author a
`.toml` workflow, place it in exactly the right directory, and — if it contains
an agent step — hand-author the `SKILL.md` files it references, because a
missing skill directory is a hard load error (`internal/workflow/skill.go:23`).
A newcomer has no path from “empty repo” to “a workflow that runs” that does not
involve reading a 1,300-line schema document first.

`jig init` closes that gap: a single non-interactive command that writes a
complete, immediately valid jig layout into the current repository, verifies its
own output through the real workflow loader, and tells the operator exactly what
to run next. The default scaffold runs end to end with **no agent credential and
no token spend**, so a first-time user’s first `jig run` always succeeds.

## Goals

1. Take a repository with no jig layout to a passing `jig validate` in **one
   command and zero manual edits**.
2. Make the default scaffold **runnable offline** — the default template
   contains no agent step, so `jig run` after `jig init` needs no backend
   credential.
3. Guarantee the scaffold is **never invalid**: `init` validates what it wrote
   using the same `workflow.Load` path as `jig validate`, and fails loudly if
   the result does not load.
4. Be **safe by default on a non-empty repo**: refuse to overwrite, preview on
   request, and overwrite only under an explicit flag.
5. Be **usable unattended** — no prompts, no TTY assumptions, stdout/stderr
   split and exit codes matching the existing operations CLI contract.

## User Stories

- **As a developer evaluating jig**, I want one command that produces a working
  workflow so that I can see the engine run before I invest time in learning the
  schema.
- **As a developer starting a new jig workflow in an existing project**, I want
  a correct skeleton with the right directory layout and a valid skill stub so
  that I spend my time on the prompt content rather than on path resolution
  rules.
- **As an operator or agent scripting a repo bootstrap**, I want `jig init` to
  run non-interactively and fail with a clear exit code so that I can call it
  from a setup script without it hanging on a prompt or silently clobbering
  files.
- **As a maintainer of jig**, I want every shipped template to be covered by a
  test that loads it through the real validator so that a schema change can
  never silently break the onboarding path.

## Demoable Units of Work

### Unit 1: Cold-start bootstrap (`jig init` with defaults)

**Purpose:** The headline slice — an empty repository becomes a working jig
project. Serves the evaluating developer.

**Functional Requirements:**

- The system shall add an `init` subcommand to the `cmd/jig` dispatch in
  `main.go`, running and exiting before the TUI initializes, matching the
  existing `runValidate`/`runPrune` shape (a function returning a process exit
  code).
- The system shall, with no flags, write the `minimal` template into
  `.agents/jig/<name>.toml`, where `<name>` defaults to the sanitized base name
  of the target directory and is overridable with `--name NAME`.
- The system shall reject a `--name` that is not a safe single path segment
  (empty, containing a path separator, a dot segment, or characters outside
  `[a-z0-9._-]` after lowercasing) with exit code 2 and without creating
  anything — the same identifier-not-a-path discipline `jig status` applies to
  run IDs.
- The system shall set `[workflow] name` in the emitted TOML to the same
  `<name>` used for the filename.
- The system shall ensure `.jig/` is git-ignored by appending a single `.jig/`
  line to `.gitignore` (creating the file if absent), and shall not append a
  duplicate line if `.jig/` is already ignored.
- The system shall accept `--dir PATH` to target a directory other than the
  process working directory, defaulting to `.`.
- The system shall, after writing, load the emitted workflow through
  `workflow.Load` and exit non-zero with the loader’s error if it does not
  validate.
- The system shall print, to stdout, each path it created and a next-step hint
  naming `jig validate <path>`, `jig run <path>`, and `jig doctor`.
- The system shall list `init` in `printHelp` (`cmd/jig/ops.go:272`) and in the
  unknown-command usage line in `main.go`.
- The `minimal` template shall contain no agent step, no `skill` reference, and
  no `backend`/`transport` requirement, so that it runs with no credential.

**Proof Artifacts:**

- CLI: terminal capture of `jig init` inside a fresh `git init` temp directory,
  followed by `jig validate .agents/jig/<name>.toml` printing `ok:` —
  demonstrates cold start to valid in one command.
- CLI: terminal capture of `jig run .agents/jig/<name>.toml --ci` exiting 0 with
  no `ANTHROPIC_API_KEY` set — demonstrates the default scaffold is offline-runnable.
- CLI: `jig --help` output showing the `init` line — demonstrates discoverability.
- Test: `go test ./cmd/jig -run TestInit` passing — demonstrates the unit is
  covered by the repo’s table-driven convention.

### Unit 2: Collision safety (`--dry-run`, refusal, `--force`)

**Purpose:** Make `init` safe to run in a repository that already has content.
Serves the developer adding jig to an existing project, and any script calling it.

**Functional Requirements:**

- The system shall compute the complete set of paths it would write **before**
  writing any of them.
- The system shall, when any target path already exists and `--force` is not
  set, write nothing, list every colliding path on stderr, and exit with code 1.
- The system shall, with `--dry-run`, print the full list of paths it would
  create (and any it would overwrite) and exit 0 **without creating, modifying,
  or deleting anything**, including `.gitignore`.
- The system shall, with `--force`, overwrite colliding paths and report each
  overwritten path distinctly from each created path.
- The system shall treat `--dry-run` as dominant: `--dry-run --force` previews
  and still writes nothing.
- The system shall never remove a file, and shall never write outside the
  target directory’s `.agents/` and `.jig/`-related paths plus `.gitignore`.
- The system shall, if a write fails partway through, report which paths were
  already created so the operator can clean up, and exit 1.

**Proof Artifacts:**

- CLI: capture of `jig init --dry-run` in a scaffolded directory listing planned
  paths, followed by `git status --porcelain` showing no changes — demonstrates
  preview is genuinely read-only.
- CLI: capture of a second `jig init` in a scaffolded directory printing the
  collision list and `echo $?` showing `1` — demonstrates fail-closed default.
- CLI: capture of `jig init --force` reporting overwritten paths and exiting 0 —
  demonstrates the explicit escape hatch.
- Test: `go test ./cmd/jig -run TestInitCollision` passing — demonstrates the
  refusal, force, and dry-run branches are all covered.

### Unit 3: Template registry and the `starter` template

**Purpose:** Give the scaffold more than one shape, and prove that an
agent-bearing scaffold is emitted complete (workflow **plus** its skill stubs).
Serves the developer who wants a real agent pipeline skeleton.

**Functional Requirements:**

- The system shall embed all template assets in the binary using `go:embed`, so
  `init` works from a binary installed outside this repository. (This introduces
  the first `go:embed` use in the codebase.)
- The system shall expose a named template registry with exactly two entries in
  this spec — `minimal` (default) and `starter` — selected with
  `--template NAME`, and shall reject an unknown name with exit code 2 and a
  message listing the valid names.
- The system shall support `--list-templates`, printing each template’s name and
  a one-line description to stdout and exiting 0 without writing anything.
- The registry shall be structured so that adding a template is a data-only
  change (add assets + one registry entry), with no change to the write,
  collision, or validation logic.
- The `starter` template shall contain at least one `agent` step, one `review`
  step with a bounded `[[step.route]]` back-edge, and one deterministic gate, so
  it demonstrates the constructs the schema doc leads with.
- The system shall, for every `skill = "…"` reference in the chosen template,
  write a corresponding `SKILL.md` stub at the path the loader resolves —
  `filepath.Join(<workflow dir>, skill)` — so that `../skills/<name>` from
  `.agents/jig/` lands in `.agents/skills/<name>/SKILL.md`, matching
  `.agents/jig/feature.toml`.
- Each emitted `SKILL.md` shall carry front matter that satisfies
  `parseSkillFile` (a non-empty `name`, plus `description` and
  `disable-model-invocation: true` per repo convention) and a clearly marked
  TODO body.

**Proof Artifacts:**

- CLI: `jig init --list-templates` output — demonstrates the registry is
  discoverable.
- CLI: `jig init --template starter` in a fresh directory, listing the workflow
  **and** the `.agents/skills/*/SKILL.md` paths, followed by `jig validate`
  printing `ok:` — demonstrates an agent scaffold is emitted complete and valid.
- CLI: `jig init --template nope` printing the valid names with `echo $?`
  showing `2` — demonstrates usage errors are distinguished from operational ones.
- Test: `go test ./cmd/jig -run TestInitTemplatesValidate` passing — a
  table-driven test that scaffolds **every** registry entry into `t.TempDir()`
  and loads it through `workflow.Load`; demonstrates no shipped template can
  drift out of schema.

### Unit 4: Documented contract

**Purpose:** Make the command part of jig’s stated operator contract rather than
an undocumented convenience. Serves every future reader, human or agent.

**Functional Requirements:**

- The system shall document `init` in [`docs/operations.md`](../../operations.md)
  with its flags, its created-path list, its collision semantics, and its place
  in the exit-code table (`0` success, `1` operational failure including
  collision and post-write validation failure, `2` usage).
- The documentation shall state that the default scaffold requires no agent
  credential.
- `README.md` shall show `jig init` as the first step of the install-and-run
  section, ahead of `jig validate`.
- The A20 row in [`docs/plans/open-goals.md`](../../plans/open-goals.md) shall be
  marked **Done** with a link to this spec, following the format used by the A1,
  A2, A3, and A11 rows.

**Proof Artifacts:**

- Diff: the `docs/operations.md` section and the amended exit-code table —
  demonstrates the contract is written down where operators look for it.
- Diff: the `README.md` quickstart change — demonstrates the onboarding path
  now starts at `init`.
- Diff: the updated A20 row — demonstrates backlog traceability.

## Non-Goals (Out of Scope)

1. **Interactive prompting or a TUI entry point**: `init` never prompts and adds
   no Home-screen affordance. A “New workflow” TUI screen is a separate spec.
2. **Git initialization, staging, or committing**: `init` does not run `git`. Its
   only git-adjacent action is appending one `.jig/` line to `.gitignore`.
3. **Interactive template authoring**: no wizard that builds a custom DAG step by
   step.
4. **Remote or registry templates**: no `jig init --from <url>`. Fetching TOML
   with executable `run =` strings from the network is a supply-chain surface
   that needs its own security review.
5. **Schema migration or upgrade**: `init` does not read, rewrite, or version-bump
   an existing workflow.
6. **Environment setup**: no backend installation, credential configuration, or
   Go/`mise` toolchain provisioning. `init` points at `jig doctor` for readiness.
7. **A third, multi-phase (`sdd`-shaped) template**: the registry is built to
   accept one, but authoring and maintaining that asset is deferred.
8. **Machine-readable `--output json`**: text output only. Other ops commands
   have a JSON mode; `init`’s output is a created-path list an operator reads
   once, and adding a second output contract is not justified here.
9. **Writing `.jig/tui.json`**: `prefs.Load` already returns correct defaults
   when the file is absent (`internal/tui/prefs/prefs.go`), so emitting one
   would pin preferences the user never chose.

## Design Considerations

No TUI work is in scope, so there are no visual design requirements. The output
is terminal text and should follow the conventions already set by the operations
CLI:

- Created/overwritten path lists and the next-step hint go to **stdout**;
  collision lists, warnings, and errors go to **stderr**.
- One path per line, prefixed with a verb (`created`, `overwrote`, `would
  create`), so the output greps cleanly.
- No ANSI styling — `init` runs before and outside the Bubble Tea program, and
  the rest of `cmd/jig` prints plain text.

## Repository Standards

- **Module path is `jig`**; new code imports as `jig/internal/...`.
- **CLI shape**: subcommands are dispatched from the `switch` in
  `cmd/jig/main.go`, each implemented as `runX(args []string) int` returning a
  process exit code, parsing flags with a `flag.NewFlagSet(name,
  flag.ContinueOnError)`. `runPrune` is the closest existing model.
- **Package placement**: per CLAUDE.md, `internal/` packages are the unit of
  design. The template assets, registry, path planning, and collision detection
  belong in a focused `internal/scaffold` package; `cmd/jig/init.go` stays a thin
  flag-parsing and printing shell. This keeps the logic testable without
  spawning a process, matching how `internal/headless` backs `jig run`.
- **Testing**: table-driven tests with `t.TempDir()`; see
  `internal/workflow/workflow_test.go` for the house style. Per CLAUDE.md, a
  schema-touching addition needs both a valid and an invalid case.
- **Validation is load-time**: reuse `workflow.Load`; do not re-implement any
  validation.
- **Comments explain the non-obvious “why.”**
- `gofmt -l -w .` and `go vet ./...` before committing.

## Technical Considerations

- **Skill path resolution is the sharp edge.** `resolveSkills` does
  `filepath.Join(baseDir, s.Skill)` where `baseDir` is the *workflow file’s*
  directory, and requires both the directory and a parseable `SKILL.md`
  (`internal/workflow/skill.go:12-38`). Since workflows live in `.agents/jig/`
  and skills live in `.agents/skills/`, real workflows use `../skills/<name>`
  (see `.agents/jig/feature.toml:41`). Note that the `skill = "skills/fix"` form
  in the current `README.md` snippet would **not** resolve; the scaffold must
  emit the `../skills/` form. The stub writer should derive its output path from
  the same join the loader uses rather than hardcoding a layout.
- **`go:embed` is new to this codebase** — there are no existing directives.
  Embedded template files must live inside the embedding package’s directory
  tree (`internal/scaffold/templates/`), and `.toml`/`.md` assets are picked up
  by an explicit `//go:embed templates` on an `embed.FS`.
- **Templates need parameter substitution** for at least the workflow name. Use
  `text/template` on the embedded asset rather than string replacement, so the
  substitution points are explicit and a missing key is an error.
- **Self-validation must use the real loader**, including module expansion and
  profile loading, i.e. `workflow.Load(path)` — not `workflow.Decode` with an
  empty `baseDir`, which skips the file-existence resolution
  (`internal/workflow/load.go:88-119`) that is exactly what a scaffold can get
  wrong.
- **Discovery alignment**: the TUI selector scans the hardcoded
  `.agents/jig` (`internal/tui/selector/model.go:15`), so the scaffold must
  write there for the new workflow to appear on the Home screen. `--dir` shifts
  the whole layout together, keeping the relative structure intact.
- **`.gitignore` handling** must be a line-aware append, not a blind
  concatenation: read existing lines, skip if `.jig/` (or an equivalent
  unanchored `.jig`) is present, and ensure a trailing newline before appending.
- **Latest-standards research: not applicable.** This feature is entirely
  repo-internal — Go 1.25’s standard `embed` and `text/template` packages, this
  repository’s own TOML schema, and its existing CLI exit-code table. No
  external vendor guidance or evolving standard materially affects the design,
  so none was consulted.

## Security Considerations

- **No credentials are read, written, or required.** `init` does not touch
  `JIG_SECRET_*`, backend tokens, or any auth material, and the default template
  requires none to run.
- **Templates must not carry secrets or personal paths.** Embedded assets are
  reviewed source in this repository; the test that validates every template
  also guards against a template drifting into referencing a local path.
- **Path traversal is the real risk surface.** `--name` and `--dir` are
  attacker-adjacent inputs when `init` is called from a script. `--name` is
  validated as a single safe path segment (rejecting separators and dot
  segments), and every write path is derived by joining under the resolved
  target directory — the same “identifiers are not paths” rule
  [`docs/operations.md`](../../operations.md) already applies to run IDs.
- **No destructive default.** `init` never deletes; overwriting requires
  `--force`. This matters most for `.gitignore`, which is appended to and never
  rewritten.
- **No network access.** Templates are embedded; nothing is fetched. This is why
  remote templates are an explicit non-goal.
- **Proof artifacts** are terminal captures from throwaway temp directories and
  contain no repository-specific or user-specific data.

## Success Metrics

1. **Cold start**: from an empty directory, `jig init && jig validate
   .agents/jig/<name>.toml` succeeds in **one command each, zero manual edits**,
   with no credential configured.
2. **Template integrity**: 100% of registry entries pass a test that scaffolds
   them and loads them through `workflow.Load`; the suite fails if a schema
   change breaks any template.
3. **Safety**: running `init` twice never modifies a file without `--force`, and
   `--dry-run` leaves `git status --porcelain` empty — both asserted by tests.
4. **Contract parity**: `init`’s exit codes and stream usage match the table in
   `docs/operations.md` (`0`/`1`/`2`), verified by tests asserting the code for
   success, collision, and unknown-template.
5. **Backlog closure**: A20 is markable Done with the same evidence standard as
   A1/A2/A3 — a linked spec plus a documented contract.

## Open Questions

1. **Exact content of the `starter` template** — which specific agent/review/gate
   steps it demonstrates is a task-planning detail, bounded by the stated
   requirement that it include an agent step, a review step with a bounded
   route, and a deterministic gate. Non-blocking: any composition meeting those
   constraints satisfies the spec.
2. **Wording of the emitted `SKILL.md` TODO body** — a copy decision that does
   not affect validation or structure.
3. **Assumption**: `.gitignore` at the target-directory root is the right place
   for the `.jig/` line. If a consumer keeps ignores in `.git/info/exclude` or a
   nested `.gitignore`, they can delete the added line; `init` does not attempt
   to detect alternative ignore locations.
