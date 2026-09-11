# Spec 22 Validation - `jig init` scaffold

**Validation date:** 2026-09-10

## Executive Summary

**Overall: PASS.** Gates A–F pass: no critical or high findings, no unknown requirement coverage, all proof artifacts exist, all core changes map to tasks, repository standards are met, and no credentials were found in the proof set.

**Implementation Ready: Yes.** `jig init` creates, validates, previews, protects, and documents both shipped scaffold shapes with test and CLI evidence.

| Metric | Result |
| --- | --- |
| Functional requirements verified | 28/28 (100%) |
| Proof artifacts accessible | 5/5 (100%) |
| Core files changed vs. planned | 11/11 mapped |
| Supporting files changed vs. planned | 14/14 mapped |

## Coverage Matrix

### Functional Requirements

| Requirement ID/Name | Status | Evidence |
| --- | --- | --- |
| FR-1 Cold-start command, safe name, target, emitted name, gitignore, loader verification, output, and help | Verified | `6b89ad3`; [Task 2 proofs](./22-proofs/22-task-02-proofs.md); `TestInit`, `TestInitRejectsUnsafeName`, and `TestInitSuccessOutput` pass. |
| FR-2 Minimal is credential-free and offline-runnable | Verified | `TestMinimalTemplateIsOffline`; isolated `env -u ANTHROPIC_API_KEY jig run ... --ci` exited 0 with zero tokens and zero cost. |
| FR-3 Plan before write, collision refusal, dry-run purity, force, and no deletion | Verified | `3040687`; [Task 3 proofs](./22-proofs/22-task-03-proofs.md); `TestPlanNoWriteOnDryRun` and `TestInitCollision` pass. |
| FR-4 `--dry-run --force` previews without writing and `--list-templates` is read-only | Verified | `cmd/jig/init.go`; `TestInitCollision` and `TestInitListTemplates` pass. |
| FR-5 Two embedded templates, unknown-template usage error, data-only registry | Verified | `fdd72c2`; [Task 4 proofs](./22-proofs/22-task-04-proofs.md); `TestTemplatesValidate` passes for minimal and starter. |
| FR-6 Starter agent, bounded review route, deterministic gate, and resolved skill stub | Verified | `internal/scaffold/templates/starter/workflow.toml.tmpl`; `TestStarterSkillPathAndPrompt`; generated starter validates through `jig validate`. |
| FR-7 Operations documentation, README onboarding, tested exit table, A20 closure | Verified | `22542c6`; [Task 5 proofs](./22-proofs/22-task-05-proofs.md); `TestInitExitCodes` passes. |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Package boundaries | Verified | New planning, rendering, naming, gitignore, and registry concerns remain in focused `internal/scaffold` files; `cmd/jig/init.go` is a flag/output shell. |
| Backend selection | Verified | Neither template nor CLI introduces environment-based harness selection; agent starter relies on normal workflow defaults. |
| Testing pattern | Verified | Table-driven tests use `t.TempDir()` and injected writers; loader validation uses `workflow.Load`. |
| Quality gates | Verified | `go test ./... -count=1` and `go vet ./...` pass; all shipped workflows validate. Changed Go files have no `gofmt -l` output. |
| Documentation | Verified | `docs/operations.md`, `README.md`, `docs/TESTING.md`, and `docs/plans/open-goals.md` reflect the shipped command. |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| Task 1 | `22-task-01-proofs.md` | Verified | Exists and documents embedded-template, name, and plan tests. |
| Task 2 | `22-task-02-proofs.md` | Verified | Exists; current isolated minimal scaffold validates and runs under CI without `ANTHROPIC_API_KEY`. |
| Task 3 | `22-task-03-proofs.md` | Verified | Exists; current `TestPlanNoWriteOnDryRun` and `TestInitCollision` pass. |
| Task 4 | `22-task-04-proofs.md` | Verified | Exists; current starter scaffold emits its skill stub and validates. |
| Task 5 | `22-task-05-proofs.md` | Verified | Exists; current `TestInitExitCodes` and final repository checks pass. |

## Validation Issues

| Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- |
| LOW | Repository-wide `gofmt -l .` cannot scan an existing ignored `.jig/worktrees/.../_run` artifact because it contains merge markers. | Environmental quality-check limitation; no changed source file is affected. | Remove or repair that abandoned run artifact separately, then rerun the global formatter scan. |

No CRITICAL, HIGH, or MEDIUM issues were found.

## Evidence Appendix

### Commits analyzed

| Commit | Mapping |
| --- | --- |
| `8ea2bd7`, `c8577ac` | Task 1 planning foundation and proof status |
| `6b89ad3`, `988d8f8` | Task 2 cold-start CLI and proof status |
| `3040687` | Task 3 collision safety |
| `fdd72c2` | Task 4 starter template and skill stub |
| `22542c6` | Task 5 operator documentation and A20 closure |

### Commands executed

~~~bash
go test ./... -count=1
go vet ./...
for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow"; done
go build -o /private/tmp/jig-spec22-validation ./cmd/jig
env -u ANTHROPIC_API_KEY /private/tmp/jig-spec22-validation run <minimal-workflow> --ci
~~~

All commands completed successfully. The isolated minimal run exited 0 and emitted an envelope with `total_cost_usd: 0` and `total_tokens: 0`; the generated starter workflow validated successfully; unknown-template selection exited 2.

### File-integrity and security review

All runtime-impacting files changed since the spec was introduced are listed in the task file's Relevant Files table and map to Tasks 1–4. Tests, proof documents, and operator documentation are supporting files linked to those tasks. A credential-pattern scan of `22-proofs/` found only sanitized mentions of environment-variable names and token concepts, with no API keys, passwords, bearer strings, or secrets.
