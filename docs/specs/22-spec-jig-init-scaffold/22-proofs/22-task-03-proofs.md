# Task 03 Proofs - collision-safe scaffold application

## Task Summary

This task makes `jig init` safe to repeat in an existing repository. It plans before writing, refuses unintended replacements, previews without mutation, and requires `--force` for replacement.

## What This Task Proves

- Existing scaffold files cause a fail-closed exit code 1 and a stderr collision list.
- `--dry-run` returns before writes, including when combined with `--force`.
- `--force` reports replaced files as `overwrote`, and it does not remove unrelated files.
- Template discovery is read-only and available before path planning.

## Evidence Summary

- Focused package and CLI tests cover collision refusal, force, dry-run, dry-run precedence, non-deletion, list-templates, and a filesystem snapshot around planning.
- A clean temporary Git repository remained clean after its dry-run preview.
- Repository tests, vet, and shipped-workflow validation passed. The global formatter scan could not complete because an existing ignored `.jig/worktrees/.../_run` artifact contains merge markers; all files changed by this task are formatted.

## Artifact: collision-safety automated tests

**What it proves:** The CLI and planning seams assert every branch required by the task.

**Why it matters:** The refusal and read-only guarantees are enforced by tests, not only by a manual command transcript.

**Command:**

~~~bash
go test ./internal/scaffold ./cmd/jig
~~~

**Result summary:** Both packages passed, including `TestPlanNoWriteOnDryRun`, `TestInitCollision`, and `TestInitListTemplates`.

~~~text
ok   jig/internal/scaffold
ok   jig/cmd/jig
~~~

## Artifact: isolated CLI collision flow

**What it proves:** A dry-run previews replacement without changing a committed temporary repository; a plain repeat fails, while `--force` replaces the workflow.

**Why it matters:** This shows the actual installed-command behavior and its stdout/stderr contract.

**Command:** An isolated `git init` repository was scaffolded, committed, then exercised with `--dry-run`, a bare repeat, and `--force`.

**Result summary:** `git status --porcelain` was empty after the dry-run. The bare repeat exited 1 and named the collision; force exited 0 and reported `overwrote`.

~~~text
would overwrite /private/tmp/jig-init-proof.<temp>/.agents/jig/demo.toml

error: scaffold files already exist; rerun with --force to overwrite:
/private/tmp/jig-init-proof.<temp>/.agents/jig/demo.toml
collision exit: 1

overwrote /private/tmp/jig-init-proof.<temp>/.agents/jig/demo.toml
force exit: 0
~~~

## Artifact: repository quality gates

**What it proves:** The change integrates with the repository's test, static-analysis, and workflow-validation gates.

**Why it matters:** Collision handling is CLI-facing code that must not regress the rest of jig.

**Command:**

~~~bash
go test ./...
go vet ./...
for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow"; done
~~~

**Result summary:** `go test ./...` and `go vet ./...` exited 0. All eight shipped workflows validated successfully. `gofmt -l` produced no output for the task's changed Go files.

## Reviewer Conclusion

`jig init` now computes collisions before mutation, previews structurally without applying a plan, fails closed by default, and makes overwrites explicit and observable.
