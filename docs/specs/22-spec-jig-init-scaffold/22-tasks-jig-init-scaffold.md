# 22-tasks-jig-init-scaffold.md

Spec: [`22-spec-jig-init-scaffold.md`](./22-spec-jig-init-scaffold.md)
Clarifications: [`22-questions-1-jig-init-scaffold.md`](./22-questions-1-jig-init-scaffold.md)
Audit: `22-audit-jig-init-scaffold.md` (generated after sub-tasks)

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/scaffold/scaffold.go` | New. Public seam: `Plan(Options) (*Plan, error)` and `Plan.Apply(...)`. The deep module `cmd/jig/init.go` delegates to. |
| `internal/scaffold/registry.go` | New. Named template registry (`minimal`, `starter`) plus each entry's description and skill references. Data-only extension point. |
| `internal/scaffold/templates.go` | New. `//go:embed templates` `embed.FS` and `text/template` rendering. First `go:embed` use in the repo. |
| `internal/scaffold/templates/minimal/workflow.toml.tmpl` | New. Default template: command + review only, no agent step, no `skill`, runs with no credential. |
| `internal/scaffold/templates/starter/workflow.toml.tmpl` | New. Agent step + review step with a bounded `[[step.route]]` + deterministic gate. |
| `internal/scaffold/templates/starter/skills/<name>/SKILL.md.tmpl` | New. Skill stub asset(s) referenced by `starter`; front matter must satisfy `parseSkillFile`. |
| `internal/scaffold/name.go` | New. `--name` / `--dir` validation: single safe path segment, no separators or dot segments. |
| `internal/scaffold/gitignore.go` | New. Line-aware idempotent `.jig/` append (never rewrite, never delete). |
| `internal/scaffold/scaffold_test.go` | New. Table-driven tests for planning, dry-run purity, collision detection, gitignore idempotence. |
| `internal/scaffold/name_test.go` | New. Table-driven valid/invalid name and target-dir cases. |
| `internal/scaffold/registry_test.go` | New. `TestTemplatesValidate`: scaffold every entry into `t.TempDir()` and load via `workflow.Load`. |
| `cmd/jig/init.go` | New. Thin flag-parsing + printing shell returning a process exit code, matching `runPrune`/`runRun`. |
| `cmd/jig/init_test.go` | New. Exit-code and branch coverage for happy path, collision, force, dry-run, unknown template. |
| `cmd/jig/main.go` | Modify. Add `case "init"` to the pre-TUI dispatch switch and to the unknown-command usage line (`main.go:44`). |
| `cmd/jig/ops.go` | Modify. Add the `init` line to `printHelp` (`ops.go:272`). |
| `internal/workflow/load.go` | Read-only reference. `Load(path)` is the self-validation seam; do not re-implement validation. |
| `internal/workflow/skill.go` | Read-only reference. `resolveSkills` (`skill.go:12-38`) defines the skill-stub path the scaffold must emit to. |
| `internal/headless/types.go` | Read-only reference. Reuse `ExitOK`/`ExitUsage` (0/2) rather than defining new exit constants. |
| `internal/tui/selector/model.go` | Read-only reference. `workflowsDir = ".agents/jig"` (`model.go:15`) fixes where the scaffold must write to appear on Home. |
| `docs/operations.md` | Modify. New scaffold section + `init` row in the exit-code table. |
| `README.md` | Modify. Quickstart starts at `jig init`. |
| `docs/TESTING.md` | Modify. Correct the stale `cmd/jig` "No tests" coverage row. |
| `docs/plans/open-goals.md` | Modify. Mark A20 **Done** with a link to this spec. |

### Notes

- Go tests live beside the code as `*_test.go` in the same package; use
  `package scaffold` (not `_test`) only where unexported access is needed.
- Follow `docs/TESTING.md`: table-driven cases with `t.Run`, inline TOML string
  constants, assert errors by distinctive substring, `t.TempDir()` for anything
  touching the filesystem.
- Run `go test ./...`, `gofmt -l -w .`, and `go vet ./...` before each commit,
  plus the example-validation loop in `docs/TESTING.md`.
- Per `AGENTS.md` (pre-v1): no compatibility shims, no env-var overrides. `init`
  takes flags only.
- Per `docs/CONVENTIONS.md`: one concern per file — keep registry, embedding,
  naming, and gitignore handling in separate files rather than a `util.go`.

## Tasks

### [x] 1.0 `internal/scaffold` foundation — embedded templates, registry, and write planning

Build the pure, testable core behind `jig init`: embedded template assets, the
named registry, name/target validation, template rendering, and the computed
plan of paths to write. No CLI wiring and no filesystem mutation outside a
caller-supplied root. Keeps `cmd/jig/init.go` a thin shell, mirroring how
`internal/headless` backs `jig run`.

Covers spec FRs: `--name` validation, `[workflow] name` substitution, `go:embed`
assets, registry with rejection of unknown names, data-only extension point,
plan-before-write.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/scaffold -run TestRegistry -v` passing — demonstrates
  every registry entry resolves to embedded assets and unknown names are rejected.
- Test: `go test ./internal/scaffold -run TestValidateName -v` passing with
  table rows for empty, `../escape`, `a/b`, `.`, uppercase, and a valid name —
  demonstrates the identifier-is-not-a-path rule from the Security Considerations.
- Test: `go test ./internal/scaffold -run TestPlan -v` passing — demonstrates the
  full path set is computed before any write, including the `.gitignore` decision.
- CLI: `gofmt -l internal/scaffold` printing nothing and `go vet ./internal/scaffold`
  exiting 0 — demonstrates repository format/vet gates pass.

#### 1.0 Tasks

- [x] 1.1 Create `internal/scaffold` with `templates.go` declaring `//go:embed templates` over an `embed.FS`; add a trivial test that the FS is non-empty so the directive cannot silently regress.
- [x] 1.2 Author `templates/minimal/workflow.toml.tmpl` as a `text/template` with a `{{ .Name }}` substitution point. Model it on `.agents/jig/golden-path.toml`: one `command` step and one `review` step, no `agent` step, no `skill`, no `backend`/`transport`.
- [x] 1.3 Add `registry.go`: a `Template` struct (name, description, asset dir, skill references) and an ordered registry containing `minimal` only for now. Expose `Lookup(name) (Template, error)` returning an error that lists valid names, and `All()` for `--list-templates`.
- [x] 1.4 Add `name.go` with `ValidateName(string) (string, error)`: lowercase, then reject empty, any `/` or `\`, `.`/`..` segments, and characters outside `[a-z0-9._-]`. Return the sanitized name. Add `DefaultName(dir string) string` deriving from the target directory's base name.
- [x] 1.5 Add `gitignore.go` with a pure `NeedsJigIgnore(existing []byte) bool` (line-aware: skip if a `.jig/` or bare `.jig` entry already exists) and `AppendJigIgnore(existing []byte) []byte` that ensures a trailing newline before appending.
- [x] 1.6 Add `scaffold.go` with `Options{Dir, Name, Template}` and `Plan(Options) (*Plan, error)`. `Plan` resolves the target dir to an absolute path, validates the name, looks up the template, renders every asset into memory, and produces an ordered `[]PlannedFile{Path, Contents, Exists}` covering the workflow file, any skill stubs, and the `.gitignore` append. `Plan` performs **no writes**.
- [x] 1.7 Assert in `Plan` that every planned path is inside the resolved target dir (join-then-verify-prefix), returning an error otherwise; add a test row driving a malicious `--name`.
- [x] 1.8 Derive skill-stub paths with the same join the loader uses — `filepath.Join(filepath.Dir(workflowPath), skillRef)` — rather than hardcoding `.agents/skills`, so `../skills/x` from `.agents/jig/` resolves correctly (see `internal/workflow/skill.go:22`).
- [x] 1.9 Write `name_test.go` and `scaffold_test.go` table-driven cases: `TestValidateName` (empty, `../escape`, `a/b`, `.`, `Mixed-Case`, valid), `TestRegistry` (lookup hit, unknown name lists valid names), `TestPlan` (planned path set, `Exists` flags, gitignore decision both ways).
- [x] 1.10 Run `gofmt -l -w internal/scaffold && go vet ./internal/scaffold && go test ./internal/scaffold` and capture the clean output as the task 1.0 proof artifact.

### [x] 2.0 `jig init` cold start — the default `minimal` scaffold, end to end

Wire the `init` subcommand into `cmd/jig`, write the plan to disk, append the
`.gitignore` line, self-validate the emitted workflow through `workflow.Load`,
and print created paths plus the next-step hint. Ship the `minimal` template:
command + review only, no agent step, no `skill` reference, no credential.

Covers spec FRs: subcommand dispatch before the TUI, default template, `--name`,
`--dir`, `.gitignore` append (idempotent), post-write validation, stdout path
list + next-step hint, `printHelp` and usage-line registration, offline-runnable
default.

#### 2.0 Proof Artifact(s)

- CLI: transcript of `mkdir /tmp/jig-cold && cd /tmp/jig-cold && git init && jig init`
  followed by `jig validate .agents/jig/jig-cold.toml` printing `ok:` —
  demonstrates cold start to valid in one command with zero manual edits (Goal 1).
- CLI: transcript of `env -u ANTHROPIC_API_KEY jig run .agents/jig/jig-cold.toml --ci`
  with `echo $?` showing `0` — demonstrates the default scaffold is offline-runnable
  (Goal 2).
- CLI: `jig --help` output containing the `init` line — demonstrates discoverability.
- CLI: `cat /tmp/jig-cold/.gitignore` after running `jig init` twice with `--force`,
  showing exactly one `.jig/` line — demonstrates the append is idempotent.
- Test: `go test ./cmd/jig -run TestInit -v` passing — demonstrates the happy path,
  `--name`, `--dir`, and the post-write validation failure branch are covered.
- Test: `go test ./cmd/jig -run TestInitRejectsUnsafeName -v` passing — demonstrates
  `--name ../escape` exits 2 **and** creates nothing, closing the audit gap where
  the refusal path had no end-to-end assertion.
- Test: `go test ./cmd/jig -run 'TestInitSuccessOutput|TestPrintHelp' -v` passing —
  demonstrates the created-path lines, the next-step hint, and the `init` help
  registration are asserted rather than merely captured once.

#### 2.0 Tasks

- [x] 2.1 Add `Plan.Apply(force bool) (*Result, error)` to `scaffold.go`: create parent directories, write each planned file with `0o644`, apply the gitignore append, and return a `Result` recording created vs. overwritten paths in plan order. Collision handling itself lands in 3.0 — for now `Apply` assumes a clean target.
- [x] 2.2 On a mid-`Apply` write error, return the error together with the `Result` accumulated so far so the caller can report which paths already exist.
- [x] 2.3 Add `Verify(result *Result) error` that calls `workflow.Load(<written workflow path>)` — the full loader, not `Decode` with an empty `baseDir`, so skill and review resolution actually run — and wraps any error with the workflow path.
- [x] 2.4 Create `cmd/jig/init.go` with `runInit(args []string) int` using `flag.NewFlagSet("init", flag.ContinueOnError)` and `fs.SetOutput(os.Stderr)`. Define `--template` (default `minimal`), `--name`, `--dir` (default `.`), `--force`, `--dry-run`, `--list-templates`. No positional arguments, so no `reorderArgs` is needed.
- [x] 2.5 **(audit remediation)** Split `runInit` into a thin `runInit(args []string) int` that supplies `os.Stdout`/`os.Stderr`, and an inner `initMain(args []string, stdout, stderr io.Writer) int` holding all logic. Every subsequent print goes through the injected writers so output is assertable in tests rather than only observable in a transcript.
- [x] 2.6 Map outcomes to exit codes using `headless.ExitOK` and `headless.ExitUsage`: `0` success, `1` operational failure (write error, post-write validation failure, and — from 3.0 — collision), `2` usage (bad flag, invalid `--name`, unknown template).
- [x] 2.7 Print `created <path>` lines to stdout in plan order, then a next-step hint naming `jig validate <path>`, `jig run <path>`, and `jig doctor`. Send warnings and errors to stderr, matching the stream split documented in `docs/operations.md`.
- [x] 2.8 Add `case "init": os.Exit(runInit(os.Args[2:]))` to the `main.go` dispatch switch, and add `init` to the unknown-command usage line at `main.go:44`.
- [x] 2.9 Add the `init` line to `printHelp` in `cmd/jig/ops.go:272`, placed first in the command list since it is the entry point for a new user.
- [x] 2.10 Write `cmd/jig/init_test.go` `TestInit`: scaffold into `t.TempDir()` via `--dir`, assert exit code 0, assert the expected file set exists, assert `[workflow] name` matches `--name`, and assert the emitted workflow loads through `workflow.Load`.
- [x] 2.11 Add a `TestInit` row covering the post-write validation failure branch (inject an intentionally broken template through the package seam, not the CLI) asserting exit code 1 and that the loader error text is surfaced.
- [x] 2.12 **(audit remediation)** Add `TestInitRejectsUnsafeName`: drive `initMain` with `--name ../escape`, `--name a/b`, `--name .`, and `--name ""`, asserting exit code `2` for each **and** that the target directory contains no new files afterwards — covering the "without creating anything" half of the requirement.
- [x] 2.13 **(audit remediation)** Add `TestInitSuccessOutput`: assert the captured stdout contains one `created ` line per planned path in plan order and a next-step hint naming `jig validate`, `jig run`, and `jig doctor`; assert stderr is empty on the success path.
- [x] 2.14 **(audit remediation)** Add a test asserting `printHelp` output contains an `init` line, so the registration in 2.9 cannot be silently dropped.
- [x] 2.15 Add gitignore coverage: a case with no `.gitignore`, one with an unrelated `.gitignore`, and one already containing `.jig/` — asserting exactly one `.jig/` line results in each.
- [x] 2.16 Capture the cold-start proof artifacts: `jig init` in a fresh `git init` temp dir, `jig validate` printing `ok:`, `env -u ANTHROPIC_API_KEY jig run … --ci` exiting 0, and `jig --help` showing the `init` line.

### [ ] 3.0 Collision safety — `--dry-run`, fail-closed refusal, `--force`

Make `init` safe in a non-empty repository. Detect every collision against the
plan from 1.0 before writing, refuse with exit 1 and a stderr collision list,
support a genuinely read-only `--dry-run` (dominant over `--force`), and report
overwritten paths distinctly under `--force`. Never delete; report partial
progress on a mid-write failure.

Covers spec FRs: plan-before-write enforcement, refusal semantics, dry-run
read-only guarantee, `--dry-run` dominance, force reporting, no-delete and
write-boundary invariants, partial-failure reporting.

#### 3.0 Proof Artifact(s)

- CLI: transcript of `jig init --dry-run` in a scaffolded directory listing
  `would create` paths, immediately followed by `git status --porcelain` printing
  nothing — demonstrates preview mutates nothing, including `.gitignore` (Metric 3).
- CLI: transcript of a second bare `jig init` in a scaffolded directory printing
  the collision list to stderr with `echo $?` showing `1` — demonstrates the
  fail-closed default.
- CLI: transcript of `jig init --force` reporting `overwrote` lines distinctly
  from `created` lines with `echo $?` showing `0` — demonstrates the explicit
  escape hatch.
- Test: `go test ./cmd/jig -run TestInitCollision -v` passing, with rows for
  refusal, `--force`, `--dry-run`, and `--dry-run --force` — demonstrates all
  four branches are covered.
- Test: `go test ./internal/scaffold -run TestPlanNoWriteOnDryRun -v` passing —
  demonstrates the read-only guarantee is asserted at the package seam, not only
  through the CLI.
- Test: `go test ./cmd/jig -run TestInitListTemplates -v` passing — demonstrates
  `--list-templates` writes every registry entry to stdout, touches stderr never,
  and creates no files.

#### 3.0 Tasks

- [ ] 3.1 Add `Plan.Collisions() []string` returning every planned path whose `Exists` flag is set, in plan order. The `.gitignore` append is not a collision — it is an idempotent modification, not an overwrite.
- [ ] 3.2 Change `initMain` to call `Collisions()` before `Apply`: when non-empty and `--force` is unset, print each colliding path to stderr with a one-line explanation naming `--force`, write nothing, and return exit code 1.
- [ ] 3.3 Implement `--dry-run` entirely in `initMain` by printing `would create <path>` / `would overwrite <path>` lines from the plan and returning before `Apply` is ever called — so the read-only guarantee is structural, not a flag checked deep inside the writer.
- [ ] 3.4 Make `--dry-run` dominant over `--force`: when both are set, preview (including `would overwrite` lines) and write nothing. Document the precedence in the flag help text.
- [ ] 3.5 Make `--list-templates` print name + description for every `All()` entry to stdout and return `ExitOK` before any planning, so it works in a directory where planning would fail.
- [ ] 3.6 Report overwritten paths distinctly from created paths under `--force` (`overwrote <path>` vs `created <path>`), driven by the `Result` from 2.1.
- [ ] 3.7 Add `TestPlanNoWriteOnDryRun` in `internal/scaffold`: snapshot the temp dir's full file listing and mtimes, run the plan path used by dry-run, and assert the listing is byte-identical afterwards.
- [ ] 3.8 Add `cmd/jig/init_test.go` `TestInitCollision` with rows for: second bare run (exit 1, nothing modified), `--force` (exit 0, contents replaced), `--dry-run` (exit 0, nothing modified), and `--dry-run --force` (exit 0, nothing modified).
- [ ] 3.9 **(audit remediation)** Extend `TestInitCollision` to assert output content, not just exit codes: the collision case writes each colliding path to **stderr** and nothing to stdout; the dry-run case writes `would create`/`would overwrite` lines to **stdout** and creates nothing; the force case emits `overwrote` lines distinct from `created` lines.
- [ ] 3.10 **(audit remediation)** Add `TestInitListTemplates`: assert `--list-templates` exits `0`, writes every registry name and its description to stdout, writes nothing to stderr, and creates no files — including when run in a directory where planning would fail.
- [ ] 3.11 Add a test asserting `init` never deletes: pre-create an unrelated file in `.agents/jig/` and confirm it survives a `--force` run.
- [ ] 3.12 Capture the collision proof artifacts, including `git status --porcelain` printing nothing after `--dry-run`, and `echo $?` for each of the three exit-code claims.

### [ ] 4.0 The `starter` template — agent step, review gate, and emitted skill stubs

Add the second registry entry and prove the registry is a data-only extension
point. `starter` carries an `agent` step, a `review` step with a bounded
`[[step.route]]` back-edge, and a deterministic gate. Emit a `SKILL.md` stub for
every `skill = "…"` the template references, at the path the loader itself
resolves (`filepath.Join(<workflow dir>, skill)`), with front matter that
satisfies `parseSkillFile`. Add `--list-templates`.

Covers spec FRs: two-entry registry, `--template NAME`, unknown name → exit 2
listing valid names, `--list-templates`, `starter` construct requirements,
skill-stub path derivation, `SKILL.md` front-matter validity.

#### 4.0 Proof Artifact(s)

- CLI: `jig init --list-templates` output showing `minimal` and `starter` with
  one-line descriptions and `echo $?` showing `0` — demonstrates registry
  discoverability without writing anything.
- CLI: transcript of `jig init --template starter` in a fresh directory listing
  the `.agents/jig/*.toml` **and** `.agents/skills/*/SKILL.md` paths, followed by
  `jig validate` printing `ok:` — demonstrates an agent scaffold is emitted
  complete and passes the loader's hard skill-resolution check
  (`internal/workflow/skill.go:23`).
- CLI: `jig init --template nope` printing the valid template names with
  `echo $?` showing `2` — demonstrates usage errors are distinguished from
  operational failures (Metric 4).
- Test: `go test ./internal/scaffold -run TestTemplatesValidate -v` passing — a
  table-driven test that scaffolds **every** registry entry into `t.TempDir()`
  and loads it through `workflow.Load`; demonstrates no shipped template can
  drift out of schema (Metric 2).
- Test: `go test ./internal/scaffold -run TestMinimalTemplateIsOffline -v` passing —
  asserts the `minimal` scaffold contains no `agent` step and no `skill`;
  demonstrates Goal 2 is machine-enforced, not just captured once by hand.

#### 4.0 Tasks

- [ ] 4.1 Author `templates/starter/workflow.toml.tmpl`: an `agent` step referencing `../skills/<name>`, a `review` step with `output_type = { enum = [...] }` and a bounded `[[step.route]]` back-edge with `max_iterations`, and a deterministic `[step.validate]` gate. Validate it by hand with `jig validate` before wiring it up.
- [ ] 4.2 Author the matching `templates/starter/skills/<name>/SKILL.md.tmpl` stub with front matter carrying a non-empty `name`, a `description`, and `disable-model-invocation: true` — matching `.agents/skills/plan/SKILL.md` — plus a clearly marked TODO body.
- [ ] 4.3 Add the `starter` registry entry with its description and its skill references, verifying no change is needed to `Plan`, `Apply`, or `Collisions` — if any is needed, fix the design so the registry stays a data-only extension point.
- [ ] 4.4 Confirm the skill-stub path derivation from 1.8 places `../skills/<name>` at `.agents/skills/<name>/SKILL.md`, and add an explicit assertion for that resolved path.
- [ ] 4.5 Add `TestTemplatesValidate` in `internal/scaffold`: iterate `All()`, scaffold each into its own `t.TempDir()`, and load the result with `workflow.Load`, failing with the template name in the message. This is the guard for Success Metric 2.
- [ ] 4.6 **(audit remediation)** Add `TestMinimalTemplateIsOffline`: scaffold `minimal`, load it with `workflow.Load`, and assert **no** step has `type = "agent"` and **no** step declares a `skill`. This is the automated guard for Goal 2 — without it, adding an agent step to `minimal` would still pass 4.5 while silently breaking the no-credential promise.
- [ ] 4.7 Add a `cmd/jig` test row for `--template nope` asserting exit code 2 and that the error text lists both valid names.
- [ ] 4.8 Add a test asserting each emitted `SKILL.md` parses: load the scaffolded `starter` workflow and confirm the agent step's resolved prompt is non-empty, proving `parseSkillFile` accepted the stub front matter.
- [ ] 4.9 Capture the task 4.0 proof artifacts: `--list-templates` output, `--template starter` path listing plus a passing `jig validate`, and the `--template nope` exit-2 capture.

### [ ] 5.0 Documented contract and backlog closure

Make `init` part of jig's stated operator contract. Document flags, created
paths, collision semantics, and exit codes in `docs/operations.md`; move the
README quickstart to start at `jig init`; refresh the stale `cmd/jig` row in
`docs/TESTING.md`; mark A20 done in the open-goals backlog.

Covers spec FRs: `docs/operations.md` section and exit-code table row,
no-credential statement, README ordering, A20 row marked Done.

#### 5.0 Proof Artifact(s)

- Diff: the new `## Scaffold a project` section in `docs/operations.md` plus the
  amended exit-code table row for `init` (`0`/`1`/`2`) — demonstrates the contract
  is recorded where operators already look for `status`/`logs`/`resume`.
- Diff: the `README.md` install-and-run change showing `jig init` ahead of
  `jig validate` — demonstrates the onboarding path now starts at `init`.
- Diff: the `docs/TESTING.md` coverage-table row for `cmd/jig` updated from
  "No tests" to name `init_test.go` alongside the existing `ops_test.go` /
  `run_test.go` — demonstrates the stale standards doc was corrected rather than
  left to contradict the code.
- Diff: the A20 row in `docs/plans/open-goals.md` marked **Done** with a link to
  this spec, matching the A1/A2/A3/A11 row format — demonstrates backlog
  traceability (Metric 5).
- Test: `go test ./cmd/jig -run TestInitExitCodes -v` passing — pins `0`/`1`/`2`
  against `headless.ExitOK`/`ExitUsage` and the operational-failure literal;
  demonstrates the documented exit table has a tested source of truth.
- CLI: `go test ./... && gofmt -l . && go vet ./...` clean, plus the
  `for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow"; done`
  loop from `docs/TESTING.md` exiting 0 — demonstrates the repo-wide gates pass.

#### 5.0 Tasks

- [ ] 5.1 Add a `## Scaffold a project` section to `docs/operations.md` placed before `## Inspect runs`, documenting every flag, the exact created-path list per template, collision semantics, and `--dry-run` dominance.
- [ ] 5.2 State explicitly in that section that the default `minimal` scaffold requires no agent credential and spends no tokens.
- [ ] 5.3 Add an `init` row to the exit-code table in `docs/operations.md` (`0` success, `1` operational failure including collision and post-write validation failure, `2` usage; no gate/timeout/signal columns apply).
- [ ] 5.4 **(audit remediation)** Assert the documented exit codes against the constants in a test — `headless.ExitOK == 0`, `headless.ExitUsage == 2`, and the operational-failure literal `1` used by `initMain` — so the table in 5.3 has a single tested source of truth rather than a hand-copied set of numbers.
- [ ] 5.5 Update the `README.md` install-and-run block so `jig init` is the first command shown, ahead of `jig validate`, with a one-line note that it works in an empty repository.
- [ ] 5.6 Correct the stale `cmd/jig` row in the `docs/TESTING.md` coverage table: it currently reads "No tests" although `ops_test.go` and `run_test.go` exist; name those plus the new `init_test.go`. (Recorded in the audit as a deliberate, approved scope addition beyond the spec's FRs.)
- [ ] 5.7 Mark the A20 row in `docs/plans/open-goals.md` **Done** with a link to this spec, matching the A1/A2/A3/A11 row format, and update the A20 entry in the section D ranked list the same way those rows were struck through.
- [ ] 5.8 Run the full gate set and capture it: `go test ./... -count=1`, `gofmt -l .` (empty), `go vet ./...`, and the `for workflow in .agents/jig/*.toml` validate loop from `docs/TESTING.md`.
- [ ] 5.9 Re-read the spec's Functional Requirements one unit at a time against the implementation and confirm each has a landed test or captured artifact; note any gap in the task file rather than silently closing it.
