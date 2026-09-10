# Task 05 Proofs - documented `jig init` contract and backlog closure

## Task Summary

This task makes `jig init` part of jig's documented operator contract and closes the source backlog goal. It also corrects the stale test-coverage statement for `cmd/jig`.

## What This Task Proves

- Operators can find every `init` flag, output behavior, collision rule, and exit code in the operations guide.
- The quickstart begins with creating a runnable workflow.
- The A20 backlog item records completion and links to this specification.
- The documented success, operational-failure, and usage codes are exercised by a focused test.

## Evidence Summary

- `docs/operations.md` now describes minimal and starter output paths, `--dry-run` precedence, and the `init` exit-code row.
- The README and test-coverage table now reflect the actual onboarding command and `cmd/jig` test suite.
- The final uncached test suite, vet, and all shipped-workflow validation passed.

## Artifact: exit-code contract test

**What it proves:** Success, usage, and collision paths return the codes documented for `init`.

**Why it matters:** The operations table remains connected to executable behavior.

**Command:**

~~~bash
go test ./cmd/jig -run TestInitExitCodes -v
~~~

**Result summary:** `TestInitExitCodes` passed, pinning success to 0, usage to 2, and collision failure to 1.

## Artifact: repository-wide final checks

**What it proves:** The completed scaffold integrates with the full repository and all shipped workflow examples.

**Why it matters:** The onboarding command changes both CLI behavior and documentation, so it must coexist with existing workflows.

**Command:**

~~~bash
go test ./... -count=1
go vet ./...
for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow"; done
~~~

**Result summary:** All packages passed uncached tests and vet. All nine shipped workflows validated. The global `gofmt -l .` scan remains blocked by merge markers in a pre-existing ignored `.jig/worktrees/.../_run` artifact; the changed Go file is formatted.

## Artifact: requirement review

**What it proves:** Every specification unit has a corresponding implementation task and proof artifact.

**Why it matters:** Validation can trace behavior from the spec through implementation evidence.

**Result summary:** Units 1–3 map to Tasks 1–4 and their proofs; the documented-contract requirements map to this task. No uncovered specification requirement was found. The ranked "If only twenty goals" list contains no A20 entry, so only the authoritative A20 table row was updated.

## Reviewer Conclusion

`jig init` is now discoverable, documented, tested against its exit contract, and marked complete in the backlog. All implementation parent tasks for Spec 22 are complete.
