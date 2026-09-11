# Task 01 Proofs - Embedded scaffold planning foundation

## Task Summary

This task adds the pure `internal/scaffold` foundation for `jig init`: embedded
assets, an ordered template registry, safe workflow-name handling, line-aware
`.gitignore` planning, and a complete in-memory write plan. No CLI wiring or
filesystem writes are part of this task.

The exported `Plan(Options)` function returns `*WritePlan`; Go does not permit a
type and function to share the package-level identifier `Plan`. The distinct
type name preserves the specified call site while providing the planned-file
methods needed by later tasks.

## What This Task Proves

- The `minimal` workflow template is embedded in the binary and rendered with
  the normalized workflow name.
- Registry lookup is ordered, returns isolated values, and reports valid names
  when lookup fails.
- Workflow names remain identifiers rather than caller-controlled paths.
- Planning computes workflow and `.gitignore` mutations before any write and
  records whether destinations already exist.
- `.gitignore` changes are modeled as append-only and skipped when `.jig` is
  already ignored.
- Every planned path is checked against the absolute target directory.

## Evidence Summary

All task-specific tests pass. The rendered minimal workflow also passes
`workflow.Decode` inside `TestPlan`, and the no-write case confirms the target
directory does not exist after planning. Package vet and formatting gates are
clean. The complete repository test suite and vet also pass.

## Artifact: Embedded registry coverage

**What it proves:** Every currently registered template resolves to embedded
assets, and an unknown template error names the valid entry.

**Why it matters:** The registry is the data-only extension seam used by later
templates and `--list-templates`.

**Command:**

```bash
go test ./internal/scaffold -run TestRegistry -v
```

**Result summary:** All registry lookup, error-message, ordering, and defensive
copy assertions pass.

```text
=== RUN   TestRegistry
=== RUN   TestRegistry/lookup_hit
=== RUN   TestRegistry/unknown_lists_valid_names
=== RUN   TestRegistry/all_is_ordered_and_isolated
--- PASS: TestRegistry (0.00s)
    --- PASS: TestRegistry/lookup_hit (0.00s)
    --- PASS: TestRegistry/unknown_lists_valid_names (0.00s)
    --- PASS: TestRegistry/all_is_ordered_and_isolated (0.00s)
PASS
ok  jig/internal/scaffold
```

## Artifact: Safe workflow-name validation

**What it proves:** Empty names, traversal, both separator forms, dot segments,
and unsupported characters are rejected; uppercase input is normalized and a
valid identifier is retained.

**Why it matters:** The workflow name becomes a filename, so it must never act
as a path supplied by the caller.

**Command:**

```bash
go test ./internal/scaffold -run TestValidateName -v
```

**Result summary:** All required invalid and valid table rows pass, including
`../escape`, `a/b`, `.`, uppercase normalization, and a valid dotted name.

```text
=== RUN   TestValidateName
=== RUN   TestValidateName/empty
=== RUN   TestValidateName/parent_traversal
=== RUN   TestValidateName/slash
=== RUN   TestValidateName/backslash
=== RUN   TestValidateName/dot
=== RUN   TestValidateName/double_dot
=== RUN   TestValidateName/uppercase_is_normalized
=== RUN   TestValidateName/valid
=== RUN   TestValidateName/space
--- PASS: TestValidateName (0.00s)
PASS
ok  jig/internal/scaffold
```

## Artifact: Complete write planning without mutation

**What it proves:** `Plan` returns the ordered workflow and `.gitignore` path
set, correct `Exists`/`Append` flags, the rendered workflow name, both
`.gitignore` decisions, and a traversal rejection before any path is emitted.

**Why it matters:** Collision refusal and dry-run safety in later tasks depend
on the entire write set being known before the first mutation.

**Command:**

```bash
go test ./internal/scaffold -run TestPlan -v
```

**Result summary:** All planning cases pass. The first case validates the
rendered TOML through `workflow.Decode` and confirms the target remains absent.

```text
=== RUN   TestPlan
=== RUN   TestPlan/computes_complete_ordered_plan_without_writing
=== RUN   TestPlan/records_collisions_and_appends_to_unrelated_gitignore
=== RUN   TestPlan/skips_gitignore_already_covering_jig
=== RUN   TestPlan/rejects_malicious_name_before_planning_paths
--- PASS: TestPlan (0.00s)
PASS
ok  jig/internal/scaffold
```

## Artifact: Task-specific formatting, vet, and test gate

**What it proves:** The new package is formatted, passes static analysis, and
passes its complete test suite.

**Why it matters:** This is the repository-equivalent quality gate required
before the parent task can be committed.

**Command:**

```bash
gofmt -l -w internal/scaffold && go vet ./internal/scaffold && go test ./internal/scaffold -count=1
```

**Result summary:** `gofmt` and `go vet` emitted no findings; the package test
suite passed.

```text
ok  jig/internal/scaffold  0.204s
```

## Artifact: Repository-wide regression gates

**What it proves:** The new package integrates without breaking any existing Go
package.

**Why it matters:** A focused package pass is insufficient if the new internal
dependency or embedded assets disturb the rest of the module.

**Commands:**

```bash
gofmt -l cmd internal
go vet ./...
go test ./... -count=1
```

**Result summary:** Formatting output was empty, vet exited 0 with no findings,
and every package passed. The full suite included `jig/internal/scaffold` and
all existing engine, harness, runner, workflow, CLI, and TUI packages.

```text
ok  jig/cmd/jig
ok  jig/internal/engine
ok  jig/internal/harness
ok  jig/internal/runner
ok  jig/internal/scaffold
ok  jig/internal/tui
ok  jig/internal/workflow
```

The literal `gofmt -l -w .` command was also attempted. It encountered existing
merge-conflict markers under the ignored runtime directory
`.jig/worktrees/20260901-195720-jq6cs1ap/_run/`; the source-scoped gate above
excludes generated `.jig` state and completed cleanly.

## Reviewer Conclusion

The evidence shows that Task 1.0 delivers a deterministic, read-only scaffold
planning seam with embedded templates, safe names and paths, correct
`.gitignore` decisions, and clean package and repository quality gates. It is
ready for the CLI/application layer in Task 2.0.
