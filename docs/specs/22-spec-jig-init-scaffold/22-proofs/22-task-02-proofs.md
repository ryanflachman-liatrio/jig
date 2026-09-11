# Task 02 Proofs - `jig init` cold start

## Task Summary

This task wires `jig init` into the pre-TUI command dispatch, applies the
in-memory scaffold plan, verifies the emitted workflow through `workflow.Load`,
and reports every created path plus concrete next steps. The default `minimal`
template runs without an agent credential or token spend.

The required review step is a failure-only gate guarded by
`when = "hello == false"`. The successful boolean command keeps that gate
skipped during the default headless run, reconciling the command-plus-review
template requirement with the required unattended `--ci` success.

## What This Task Proves

- A fresh Git repository reaches a valid jig workflow with one `jig init`.
- The emitted workflow passes the same full loader used by `jig validate`.
- The default workflow completes under `--ci` with `ANTHROPIC_API_KEY` removed,
  zero cost, and zero tokens.
- Writes use `0644`, `.gitignore` receives exactly one `.jig/` entry, and a
  partial write error returns the successfully written paths.
- Unsafe names exit 2 and leave the target untouched.
- Success output uses stdout in plan order; errors use stderr; help advertises
  the new command.

## Evidence Summary

The executable cold-start scenario passed with exit code 0 for initialization,
validation, headless execution, and help. Focused CLI and scaffold-package tests
all pass, as do the full repository test suite, vet, formatting, and validation
of every checked-in workflow example.

## Artifact: Fresh-repository cold start and full loader validation

**What it proves:** The built binary creates the expected layout from an empty
Git repository and the resulting workflow is immediately valid.

**Why it matters:** This is the feature's headline one-command onboarding path,
exercised through the real process boundary rather than only package tests.

**Commands:**

```bash
proof_dir=$(mktemp -d /private/tmp/jig-cold-XXXXXX)
git -C "$proof_dir" init -q
cd "$proof_dir"
/private/tmp/jig-spec22-task02 init
/private/tmp/jig-spec22-task02 validate .agents/jig/*.toml
```

**Result summary:** Initialization and validation both exited 0. The default
directory name was normalized to lowercase and used for both the filename and
`[workflow] name`.

```text
/private/tmp/jig-cold-1LPOUS
created /private/tmp/jig-cold-1LPOUS/.agents/jig/jig-cold-1lpous.toml
created /private/tmp/jig-cold-1LPOUS/.gitignore
next: jig validate /private/tmp/jig-cold-1LPOUS/.agents/jig/jig-cold-1lpous.toml
next: jig run /private/tmp/jig-cold-1LPOUS/.agents/jig/jig-cold-1lpous.toml
next: jig doctor
init_exit=0
ok: "jig-cold-1lpous" v1 — 2 step(s)
validate_exit=0
```

## Artifact: Credential-free headless run

**What it proves:** The default template runs to completion without an Anthropic
credential and without invoking an agent.

**Why it matters:** A first-time user's first run must succeed without account
setup, token spend, or a human gate in unattended mode.

**Command:**

```bash
env -u ANTHROPIC_API_KEY /private/tmp/jig-spec22-task02 run .agents/jig/*.toml --ci
```

**Result summary:** The command step succeeded, the conditional review was
skipped, and the run returned a successful JSON envelope with zero cost and
zero tokens.

```text
ci: --ci --output json --discard-merge --on-recovery abort --on-conflict abort --timeout 45m0s
warning: step "review": type=review will fail-closed if reached
started jig-cold-1lpous (2 steps)
hello running
hello succeeded
review skipped
{"ok":true,"workflow":"jig-cold-1lpous","failed":false,"total_cost_usd":0,"total_tokens":0,"error":null}
run_exit=0
```

## Artifact: Command discoverability

**What it proves:** The top-level help output lists `init` first.

**Why it matters:** New users can discover the onboarding command without
already knowing the workflow schema or repository layout.

**Command:**

```bash
/private/tmp/jig-spec22-task02 --help
```

**Result summary:** Help exited 0 and showed the new command at the start of the
command list.

```text
Commands:
  init                      scaffold a valid workflow
  validate WORKFLOW.toml    validate a workflow
  run WORKFLOW.toml         run a workflow headlessly
help_exit=0
```

## Artifact: CLI branch and output-contract tests

**What it proves:** Success, loader failure, unsafe names, `.gitignore`
idempotence, and success-output ordering are asserted at the injected-writer
CLI seam.

**Why it matters:** The observable stream and exit-code contract can regress in
tests rather than relying on a one-time transcript.

**Command:**

```bash
go test ./cmd/jig -run TestInit -v -count=1
```

**Result summary:** Every required CLI case passed, including all four unsafe
names and all three `.gitignore` starting states.

```text
--- PASS: TestInit (0.00s)
    --- PASS: TestInit/scaffolds_a_valid_workflow (0.00s)
    --- PASS: TestInit/surfaces_post-write_validation_failure (0.00s)
--- PASS: TestInitRejectsUnsafeName (0.00s)
--- PASS: TestInitSuccessOutput (0.00s)
--- PASS: TestInitGitignore (0.00s)
PASS
ok  jig/cmd/jig
```

## Artifact: Unsafe-name refusal

**What it proves:** `../escape`, `a/b`, `.`, and an explicitly empty `--name`
all exit with usage code 2 and create no files.

**Why it matters:** This closes the audit's identifier-not-a-path requirement at
the actual CLI seam.

**Command:**

```bash
go test ./cmd/jig -run TestInitRejectsUnsafeName -v -count=1
```

**Result summary:** All four refusal rows passed with an empty target directory.

```text
--- PASS: TestInitRejectsUnsafeName (0.00s)
    --- PASS: TestInitRejectsUnsafeName/../escape (0.00s)
    --- PASS: TestInitRejectsUnsafeName/a/b (0.00s)
    --- PASS: TestInitRejectsUnsafeName/. (0.00s)
    --- PASS: TestInitRejectsUnsafeName/#00 (0.00s)
PASS
```

## Artifact: Success output and help assertions

**What it proves:** Each planned path receives one ordered `created` line,
the next-step hint names validate/run/doctor, stderr stays empty on success,
and help contains `init`.

**Why it matters:** These requirements were planning-audit remediations and must
remain machine-verifiable.

**Command:**

```bash
go test ./cmd/jig -run 'TestInitSuccessOutput|TestPrintHelp' -v -count=1
```

**Result summary:** Both output-contract tests passed.

```text
--- PASS: TestInitSuccessOutput (0.00s)
--- PASS: TestPrintHelpIncludesInit (0.00s)
PASS
ok  jig/cmd/jig
```

## Artifact: Apply and verification package seams

**What it proves:** Applying a plan writes all files at `0644`, verifies the
workflow with the full loader, and preserves completed paths when a later write
fails.

**Why it matters:** The thin CLI depends on this package boundary for reliable
filesystem behavior and actionable partial-failure reporting.

**Command:**

```bash
go test ./internal/scaffold -run 'TestApply|TestVerify' -v -count=1
```

**Result summary:** Successful apply/verify, mid-apply failure, and invalid
workflow path-reporting cases all passed.

```text
--- PASS: TestApply (0.00s)
    --- PASS: TestApply/writes_the_complete_plan_and_verifies_it (0.00s)
    --- PASS: TestApply/returns_completed_writes_with_a_later_error (0.00s)
--- PASS: TestVerifyReportsWorkflowPath (0.00s)
PASS
ok  jig/internal/scaffold
```

## Artifact: Repository quality gates

**What it proves:** The new command and write layer integrate with the complete
module and preserve every existing workflow example.

**Why it matters:** The cold-start path shares workflow, runner, engine, and
headless code, so package-local success alone is insufficient.

**Commands:**

```bash
gofmt -l cmd internal
go vet ./...
go test ./... -count=1
for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow" || exit 1; done
```

**Result summary:** Formatting produced no output, vet exited 0, all packages
passed, and all nine checked-in top-level workflows validated successfully.

```text
ok  jig/cmd/jig
ok  jig/internal/engine
ok  jig/internal/harness
ok  jig/internal/headless
ok  jig/internal/runner
ok  jig/internal/scaffold
ok  jig/internal/tui
ok  jig/internal/workflow
ok: "bugfix" v1 — 4 step(s)
ok: "feature" v1 — 16 step(s)
ok: "golden-path" v1 — 2 step(s)
ok: "implementation-review" v1 — 2 step(s)
ok: "mixed-transport" v1 — 2 step(s)
ok: "research" v1 — 3 step(s)
ok: "review-ui-demo" v1 — 1 step(s)
ok: "review" v1 — 2 step(s)
ok: "sdd" v1 — 48 step(s)
```

## Reviewer Conclusion

Task 2.0 provides a complete cold-start path: a new repository receives a valid
workflow and ignore rule, receives actionable output, and can immediately run
the default template offline with no agent credential or token usage. Automated
tests cover the loader, error, path-safety, output, help, and `.gitignore`
contracts.
