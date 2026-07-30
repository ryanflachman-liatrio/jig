# Testing

How jig is tested, the conventions to follow, and what to cover when you touch
the code.

## Running tests

```bash
go test ./...                                   # everything
go test ./internal/workflow                     # just the workflow package
go test ./internal/workflow -run TestDecodeInvalid -v   # one test, verbose
go test ./internal/workflow -run TestDecodeInvalid/cycle    # one sub-case
go test ./... -count=1                           # bypass the test cache
go test ./... -race                              # race detector (use for TUI/engine work)
go test ./internal/workflow -cover               # coverage
```

Before opening a change, also run the formatter, the vetter, and the example
validator — the examples are executable documentation:

```bash
gofmt -l -w . && go vet ./...
go run ./cmd/jig validate examples/feature.toml   # and the other examples/*.toml
```

## Where the tests are

| Package | State | Notes |
|---------|-------|-------|
| `internal/workflow` | **Tested** | `workflow_test.go` — the real coverage lives here: parsing, defaulting, and the full validation surface. |
| `internal/tui` | No tests | Bubble Tea UI; exercised manually via `go run ./cmd/jig`. |
| `cmd/jig` | No tests | Thin entry point; covered indirectly by `validate` against the examples. |
| `internal/engine`, `runner`, `step`, `manifest`, `datastore` | Not implemented | Empty placeholders — no code, no tests yet. |

## Conventions (follow the existing style)

The workflow tests are the template. Match them:

- **Table-driven.** `TestDecodeInvalid` is a slice of `{name, toml, want}` cases
  run under `t.Run(tc.name, ...)`. Add a row rather than a new function when the
  shape fits.
- **Workflows are inline TOML string constants.** Real, readable `.toml` (see
  `validBugfix`, `validProducer`) rather than hand-built structs — the test
  doubles as documentation of the syntax.
- **`Decode(data, baseDir)` is the seam.** Pass `""` as `baseDir` to **skip**
  file-existence checks (most invalid-case tests do this — they're testing the
  parser/validator, not the filesystem). Pass a `t.TempDir()` when the case must
  reference real skill dirs / schema files, and write them first with the
  `mustWrite` helper.
- **Assert on the aggregated error by substring.** Invalid cases check
  `strings.Contains(err.Error(), tc.want)` against a distinctive fragment of the
  validator's message (e.g. `"max_iterations must be >= 1"`, `"cycle"`,
  `` "both `run` and `script`" ``). Keep those messages stable, or update the
  matching case when you deliberately reword one.
- **Look steps up via `wf.index`.** `wf.Steps[wf.index["fix"]]` — never assume a
  positional order for lookups by id.
- **`mustWrite(t, path, content)`** creates parent dirs and writes a file inside
  the temp dir; use it for `SKILL.md` stubs, agent files, and JSON schemas.

## What a schema change must cover

The validator is the product here, so a change to the schema is not done until
the tests prove both directions:

1. **A valid case** — a workflow using the new field/construct decodes without
   error and the parsed value lands where expected (add assertions like the
   isolation-inference and input-parsing checks in `TestDecodeValid`).
2. **Every new failure mode** — one `TestDecodeInvalid` row per rejection, each
   asserting the specific error substring a user would see.
3. **Defaulting & precedence**, if the field is inheritable from `[defaults]` or
   foldable from an `agent_file` — prove `[defaults]` seeds it, the file folds in
   only when the step leaves it unset, and an explicit step field outranks both
   (see `TestDecodeAgentFile`).
4. **Reference/type checks**, if the field participates in `@ref`s or guards —
   prove a dangling ref, an unknown field, and an illegal enum value are all
   caught at load time (see the field-ref cases and `TestDecodeProducerSchema`).

## Current coverage map (what's already asserted)

- **Valid decode** — step count, defaults applied, worktree isolation inferred
  from mutating tools (on for `fix`, off for read-only `triage`), mixed inputs
  parsed into ref vs. inline-path.
- **Invalid decode** (20+ cases) — unknown keys, missing/dangling `depends_on`,
  `@ref` not in `depends_on`, dependency cycles, illegal `when` values and
  unknown/illegal field refs, unbounded loops, `run`+`script` conflicts, review
  steps missing a verdict type, conflicting output shapes
  (`output_type`+schema, schema+`schema_file`), schema on non-agent steps,
  invalid effort, negative budget, `skill`+`agent_file` conflicts, and
  agent-only fields on the wrong step type.
- **Producer schemas** — name-sorted fields, nested list-of-object resolution,
  downstream field-path inputs, and a compiled JSON Schema that is a *closed*
  object with all fields required.
- **`schema_file`** — a raw JSON Schema parses into the same `Field` model so
  field-ref checks work against it identically.
- **`agent_file`** — frontmatter `tools`/`model` fold in, the body becomes the
  system prompt, mutating tools flip worktree isolation on, and explicit step
  fields outrank both the file and `[defaults]`.

## Testing the TUI and (future) engine

- **TUI:** no automated tests today. Verify manually with `go run ./cmd/jig`;
  when adding regression tests, drive the model directly — feed `tea.Msg` values
  through `Update` and assert on the returned model/`View()` string rather than
  spinning up a real terminal. Run UI/concurrency work under `-race`.
- **Engine (planned):** when `internal/engine` lands, keep agent invocation and
  shell execution behind interfaces so the DAG traversal, gate evaluation, and
  loop-termination logic can be tested with fakes — no live model calls in unit
  tests. The determinism guarantees (topological order, `when` skipping, bounded
  loops, gate pass/fail) are exactly the properties worth asserting.
