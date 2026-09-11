# 22-audit-jig-init-scaffold.md

Audit run: 2 (2026-09-10) — after user-approved remediation
Previous run: 1 (failing, 3 REQUIRED / 2 FLAG)
Artifacts audited: [`22-spec-jig-init-scaffold.md`](./22-spec-jig-init-scaffold.md),
[`22-tasks-jig-init-scaffold.md`](./22-tasks-jig-init-scaffold.md)

## Executive Summary

- Overall Status: **PASS**
- Required Gate Failures: **0**
- Flagged Risks: **2** (both accepted, neither blocking)

Run 1 failed three REQUIRED gates, all the same shape: a functional requirement
whose only planned evidence was a hand-captured terminal transcript. The
user-approved remediation added 8 sub-tasks (48 → 56) and one small testability
change — `runInit` now delegates to an `initMain(args, stdout, stderr)` inner
function so output is assertable. No spec change was needed.

## Gateboard

| Gate | Status | Note | Evidence |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | Every FR maps to a named test | tasks 2.12, 2.13, 2.14, 3.9, 3.10, 4.6, 5.4 |
| Proof artifact verifiability | PASS | Output contract now asserted, not captured | tasks 2.5, 2.13, 3.9, 3.10 |
| Repository standards consistency | PASS | 5 sources read; one stale doc remediated | task 5.6 |
| Open question resolution | PASS | 3 open questions, all non-blocking | spec `## Open Questions` |
| Regression-risk blind spots | FLAG | Docs drift unguarded (accepted) | mitigated by task 5.4 |
| Non-goal leakage | FLAG | Task 5.6 exceeds spec FRs (accepted) | recorded below |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pre-v1: no compat shims or env overrides; CLI lives in `cmd/jig`; `gofmt`/`go vet`/`go test ./...` before change | none |
| `CLAUDE.md` | yes | `internal/` package is the unit of design; validation is load-time; table-driven tests with inline TOML; comments explain *why* | none |
| `README.md` | yes | Quickstart command ordering; Go 1.25; workflows are documentation | Contained an incorrect `skill = "skills/fix"` path; corrected before task generation |
| `docs/TESTING.md` | yes | Table-driven + `t.Run`; `t.TempDir()` for filesystem cases; assert errors by substring; validate-every-example loop | **Stale**: `cmd/jig` row says "No tests" although `ops_test.go`/`run_test.go` exist. Precedence: code is truth (CLAUDE.md). Remediated by task 5.6 |
| `docs/CONVENTIONS.md` | yes | One concern per file; prefer same-package splitting; subpackage only when API is stable and multi-consumer | Mild tension with new `internal/scaffold` package; resolved — two consumers (CLI + tests), no unexported-access need |
| `CONTRIBUTING.md` | not found | — | — |
| `.github/pull_request_template.md`, `.github/workflows/` | not found | — | No CI exists; consistent with A14 still open |
| `.pre-commit-config.yaml`, `Makefile` | not found | — | Fallback evidence: `mise.toml` (Go 1.25), `go.mod` |

Standards confidence: **high** — 5 guideline sources read, both required ones
(`AGENTS.md`, root `README.md`) present and reviewed.

## Findings

### REQUIRED Failures

None. All three run-1 failures are resolved; see the Re-Audit Delta.

<details>
<summary>Run 1 failures (resolved — kept for traceability)</summary>

1. **Invalid `--name` has no CLI-level exit-code assertion**
   - Missing item: `ValidateName` is unit-tested (1.9), and `--template nope`
     has a CLI exit-2 test (4.6), but no test drives `runInit` with a bad
     `--name` to prove the usage-code mapping in 2.5 actually fires. Spec FR
     "reject a `--name` that is not a safe single path segment ... with exit
     code 2 ... without creating anything" is therefore untested end to end,
     and the "without creating anything" half is unasserted anywhere.
   - File section to edit: `## Tasks > ### [ ] 2.0 ... > #### 2.0 Tasks`
   - Acceptance condition: a `TestInit` row passes `--name ../escape`, asserts
     exit code 2, and asserts the target directory is still empty.

2. **Output-contract requirements are evidenced only by transcripts**
   - Missing item: four FRs describe observable output — the `created <path>`
     stdout lines and next-step hint (2.6), the `would create`/`would overwrite`
     preview lines (3.3), `--list-templates` name+description output (3.5), and
     the `printHelp` registration (2.8). Each currently maps to a captured
     terminal artifact only. A transcript proves the behavior once; it cannot
     fail when someone later reorders the output or drops the hint.
   - File section to edit: `## Tasks > #### 2.0 Tasks` and `#### 3.0 Tasks`
   - Acceptance condition: `runInit` writes to injectable `io.Writer`s (or the
     printing is factored into a testable function), and tests assert the
     stdout/stderr split plus the presence of each documented line for the
     success, dry-run, collision, and `--list-templates` paths.

3. **The offline-runnable guarantee has no automated check**
   - Missing item: Goal 2 and Success Metric 1 rest on the `minimal` template
     containing no agent step. The only planned evidence is a manual
     `env -u ANTHROPIC_API_KEY jig run … --ci` capture (2.12). A future edit
     adding an agent step to `minimal` would pass `TestTemplatesValidate` (4.5)
     and break the headline promise silently.
   - File section to edit: `## Tasks > #### 4.0 Tasks` (beside the sibling
     template test)
   - Acceptance condition: a test loads the scaffolded `minimal` workflow and
     asserts no step has `type = "agent"` and no step declares a `skill`.

</details>

### FLAG Findings

1. **Docs contract can drift from code with nothing to catch it**
   - Risk: task 5.3 writes an exit-code table into `docs/operations.md`. If
     `runInit`'s codes later change, no test compares the two. This is the same
     class of drift that left the `docs/TESTING.md` coverage table stale.
   - Suggested remediation: accept the risk (documentation drift is repo-wide
     and out of scope for one spec), but assert the exit-code *constants* in
     tests so the documented values have a single tested source of truth.

2. **Task 5.6 exceeds the spec's stated requirements**
   - Risk: correcting the `docs/TESTING.md` coverage table is not in any spec
     functional requirement. It is a two-line fix to a document this
     implementation will directly contradict, and it was surfaced and accepted
     during parent-task review.
   - Suggested remediation: keep it, and record it here as a deliberate,
     approved scope addition rather than silent creep.

## User-Approved Remediation Plan

- **Completed** (approved by the user, applied in run-1 → run-2 delta).

| # | Remediation | Landed as |
| --- | --- | --- |
| 1 | Testability: split `runInit` into a writer-injecting `initMain` | task 2.5 |
| 2 | CLI test for unsafe `--name`: exit 2 **and** nothing created | task 2.12 |
| 3 | Assert success stdout: `created` lines + next-step hint | task 2.13 |
| 4 | Assert `init` appears in `printHelp` | task 2.14 |
| 5 | Assert collision/dry-run/force output streams and content | task 3.9 |
| 6 | Assert `--list-templates` output, streams, and no writes | task 3.10 |
| 7 | Assert `minimal` has no agent step and no skill (Goal 2 guard) | task 4.6 |
| 8 | Pin documented exit codes to the `headless` constants | task 5.4 |

Proof-artifact lists for parents 2.0, 3.0, 4.0, and 5.0 were updated to name
each new test.

## Re-Audit Delta (Run 2)

Changed gate statuses since run 1:

- Requirement-to-test traceability: **failing → PASS** — the `--name` refusal path
  now has an end-to-end test asserting both the exit code and the
  nothing-was-created invariant (task 2.12).
- Proof artifact verifiability: **failing → PASS** — the four output-contract FRs
  moved from transcript-only evidence to asserted stdout/stderr content, enabled
  by the writer injection in task 2.5 (tasks 2.13, 2.14, 3.9, 3.10).
- Offline-runnable guarantee (reported under run-1 Findings): **failing → PASS** —
  task 4.6 machine-enforces that `minimal` carries no agent step or skill.

Still-failing REQUIRED gates: none.

Newly introduced findings: none. The remediation added tests and one
function-boundary change; it introduced no new files beyond those already in the
Relevant Files table and no new scope.

## Chain-of-Verification (Run 2)

1. **Initial assessment:** all REQUIRED gates read PASS after remediation.
2. **Self-questioning:** does every REQUIRED gate pass with *explicit* evidence,
   or only plausible evidence?
3. **Fact-checking:** each run-1 finding was re-read against the current task
   file. The `--name` gap resolves to task 2.12; the output-contract gap resolves
   to tasks 2.5/2.13/2.14/3.9/3.10; the Goal 2 gap resolves to task 4.6. Each
   named test also appears in its parent task's Proof Artifact list, so the
   validation phase can locate the evidence without re-deriving it.
4. **Inconsistency resolution:** one correction — the run-1 report referred to
   the `docs/TESTING.md` fix as "task 5.5"; renumbering during remediation moved
   it to **5.6**. References updated.
5. **Final synthesis:** **PASS**. Planning is ready for the implementation phase.
   Both FLAG findings are accepted and non-blocking: docs drift is a repo-wide
   condition partially mitigated by task 5.4, and the `docs/TESTING.md`
   correction is a deliberate, user-approved scope addition.
