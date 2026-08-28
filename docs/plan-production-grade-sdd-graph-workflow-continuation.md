# Continuation Plan: production-grade SDD graph workflow

## Starting point

The first implementation slice landed in commit `c27d8b9` (`feat: add typed
quality checks and routes`). It added:

- `check` steps with typed `pass|fail|skip|error` verdicts and per-attempt log
  plus JSON evidence under the run directory;
- `[[step.route]]`, bounded route rewinds, and conservative route-guard
  exclusivity validation;
- `[defaults].resource_limits`, per-step `resource_class`, and scheduler
  enforcement;
- `from = "user", once = true` inputs retained across loop rewinds;
- migration of `.agents/jig/sdd.toml` from legacy loops to routes, removal of
  workflow-owned commit steps, isolated mutation steps, and typed Go
  test/lint/coverage checks.

The baseline passed `go test ./...`, `go vet ./...`, `go build ./cmd/jig`, and
`go run ./cmd/jig validate .agents/jig/sdd.toml` before the commit.

Do not include or discard the unrelated existing worktree changes (notably the
TUI/spec work and `workflow-rs/`). Inspect `git status --short` before editing.

## Remaining outcome

Finish the original production-grade redesign: native, typed SDD modules;
strict check and route contracts; production execution controls; and an SDD
workflow whose phase transitions, remediation, audit evidence, and worktree
integration are all engine-enforced.

The source workflow contract remains [workflow-schema.md](workflow-schema.md).
The existing SDD workflow is [.agents/jig/sdd.toml](../.agents/jig/sdd.toml).

## Phase 1 — tighten the already-added primitives

1. Finish the `check` contract.
   - Require `applies_when` and a declared structured findings interface for
     every check; an inapplicable check must be the only path to `skip`.
   - Replace the current exit-status heuristic with explicit outcome protocol
     handling. A tool startup error, timeout, unreadable evidence, malformed
     findings, or unavailable required tooling must become `error`, never
     `fail` or `skip`.
   - Define and validate a versioned findings JSON schema. Preserve command log
     and findings JSON as separate immutable artifacts.
   - Route a `fail` or `error` into remediation/review automatically. A failed
     quality gate must not reach a `continue` approval merely because the
     reviewer overlooked the evidence.
   - Add runner and engine tests for pass/fail/skip/error, malformed findings,
     required-but-missing tools, iteration-local evidence, and failure routing.

2. Complete route semantics.
   - Extend mutual-exclusivity checking beyond the current conservative
     same-enum equality rule where possible; reject any condition form that
     cannot be proven disjoint when it appears with other routes.
   - Require a terminal/exhaustive route policy. A single guarded route is only
     valid if non-match naturally terminates; multi-route decisions must either
     cover their complete enum/bool domain or end in a fallback.
   - Replace the legacy `[step.loop]` parser/runtime path after all bundled
     workflows have migrated. Do not leave a compatibility shim past migration.
   - Make route selection and cap metadata first-class journal events rather
     than reusing the legacy `LoopFired` name.
   - Add validator tests for overlapping guards, missing fallback, unreachable
     fallback, unknown feedback references, cap exhaustion, and route resume.

3. Fix worktree data sharing by contract, not by rerunning commands.
   - Checks that consume a prior check's artifact must receive an immutable
     engine-owned input artifact, not a path in another step worktree.
   - Revisit the current coverage check in `sdd.toml`, which reruns tests in its
     own worktree as a temporary correctness workaround. Replace it with an
     explicit coverage artifact exported by the test check.
   - Add an integration test proving a producer check can export evidence to a
     consumer check across isolated worktrees.

## Phase 2 — native typed subworkflows

1. Design the module TOML contract before implementation.
   - Module workflows declare a schema-versioned `[module.inputs]` and
     `[module.exports]` interface. Exports may reference only module-internal,
     typed fields/artifacts.
   - Parent `type = "subworkflow"` steps set `module = "path/to/module.toml"`
     and an explicit `with` binding map. Parent consumers use named exports, not
     internal module step ids.
   - Specify namespace rules for expanded ids, route targets, reviews,
     worktrees, transcripts, artifacts, persistence-off mode, and resume.
   - Record the design in `docs/workflow-schema.md` before writing the parser.

2. Implement loader and validator support.
   - Resolve module paths relative to the referring workflow and reject module
     cycles, duplicate expanded ids, unknown bindings, missing required inputs,
     unsupported module versions, illegal export references, and schema-type
     mismatches.
   - Validate the fully expanded graph before any step starts, including all
     dependency edges, conditions, routes, resource classes, isolation, and
     secret declarations across module boundaries.
   - Keep the public parent graph available for the TUI while maintaining an
     immutable expanded execution graph internally.

3. Persist reproducibility data.
   - Persist the expanded graph and module source digests in the run directory;
     resume must load that locked expansion, never reread a changed module from
     the checkout.
   - Include module/version/digest provenance in manifests and relevant review
     evidence.
   - Add parser, validator, run snapshot, resume, and TUI presentation tests.

## Phase 3 — production execution controls

1. Extend the schema and scheduler with:
   - per-step timeout;
   - retry count, backoff policy, and retry classification;
   - explicit idempotency declaration required for automatic retries;
   - global plus class concurrency limits, with separate read-only research and
     mutating-worktree capacity;
   - declared mutation path allowlists and an integration-time unexpected-diff
     rejection gate;
   - named secret references only, with resolution outside TOML and redaction
     in prompts, transcripts, logs, snapshots, findings, and review artifacts;
   - outbound/network and cost/security budgets enforced by the engine.

2. Upgrade provenance.
   - Snapshot input artifacts by content and digest before dispatch.
   - Capture resolved workflow/module digests, prompt/skill versions, backend,
     model, transport, tool policy, output/finding digests, and integration
     commit/diff provenance.
   - Ensure every artifact filename includes generation, iteration, and attempt
     where an overwrite could hide evidence.

3. Add focused engine integration coverage for timeouts, retry/backoff,
retryable/non-retryable failures, class limits, mutation isolation, unexpected
diff handling, secret redaction, immutable input snapshots, and resume/reset
across module boundaries.

## Phase 4 — split and redesign SDD

1. Replace the monolithic SDD graph with these typed modules:
   - `spec`: context, concurrent research, scope/architecture gates, and spec
     export;
   - `plan`: analysis, task generation, audit fan-out/fan-in, and task export;
   - `implement_task`: find/implement/check/proof/task-review cycle for one
     selected parent task;
   - `validate`: artifact discovery, coverage/proof/repository checks, final
     validation report;
   - `compliance`: applicability, security/privacy/accessibility/dependency
     checks, exceptions, and compliance report;
   - `release_notes`: changelog draft/export only. Engine-owned final merge
     remains outside this module.

2. Keep orchestration in a short parent workflow.
   - The parent owns phase transitions, risk-based human gates, exception
     records, final merge approval, and targeted-remediation dispatch.
   - Auto-advance successful read-only research and machine-passing validation.
   - Require review for scope/architecture exceptions, unexpected diffs,
     blocking check failures, compliance/security exceptions, and final merge.
   - Override records must include reviewer identity, rationale, evidence
     digests, scope, expiry, and follow-up policy.

3. Redesign implementation iteration precisely.
   - Collect checkpoint policy once before entering the cycle.
   - `find_next_task` runs once per task selection.
   - `redo` returns to the same selected task's implementation module without
     integration; `continue` integrates then selects the next task; `done`
     starts final validation.
   - Final review findings must call a targeted remediation module with the
     finding ids and affected task/path, never rediscover the first unchecked
     task.

4. Replace generic shell auto-detection with declared quality profiles.
   - Define project/module profiles for build, test, lint, format, coverage,
     dependencies, and API contracts, including tool versions, thresholds,
     baselines, applicability, and not-applicable criteria.
   - Delete obsolete auto-detection/commit scripts in `.agents/jig/scripts/`
     once no bundled workflow references them.

## Phase 5 — acceptance suite and migration completion

1. Add a complete end-to-end SDD fixture that proves:
   - research fan-out is concurrent;
   - a `redo` re-runs the same task and does not integrate it;
   - failed/error checks cannot silently reach approval;
   - unrelated user worktree changes remain untouched;
   - each attempt's evidence is independently inspectable;
   - resume uses the persisted expanded graph even if the source module changes.

2. Validate every bundled workflow and migrate any remaining legacy loops.
   Remove `[step.loop]` support and update docs/examples/tests in the same
   change; this repository is pre-v1 and should not retain dual DSL paths.

3. Release gate:

```bash
go test ./...
go vet ./...
go build ./cmd/jig
for workflow in .agents/jig/*.toml; do go run ./cmd/jig validate "$workflow"; done
```

## Suggested skills

- `matt-skills-curated:to-spec` before Phase 2: resolve the module-interface
  details into a reviewable spec instead of inventing semantics mid-build.
- `matt-skills-curated:tdd` for every parser/validator/runtime increment.
- `matt-skills-curated:implement` for each approved phase.
- `matt-skills-curated:code-review` after each phase, using the originating
  specification and the schema documentation as review sources.
- `matt-skills-curated:handoff` if this work must move contexts again.
