# 09-tasks-docs-examples-adrs.md

> Implementation task list for [`09-spec-docs-examples-adrs.md`](./09-spec-docs-examples-adrs.md).
> This is a documentation-only spec; no runtime code changes.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `docs/ARCHITECTURE.md` | Primary doc to update: replace the outdated "planned" engine section with the run-integration-branch model and the reset seam; add ADR links. |
| `docs/workflow-schema.md` | Update Worktrees section (step worktrees branch off run HEAD); retire the hand-wired `merge` command step example; add no-schema-surface note for stop/reset. |
| `docs/engine-design.md` | Add documented stop (quiescence) and reset flow sections; update stale event/status lists to include `StepsReset`/`StatusStopped`/`Generation`. |
| `docs/adr/README.md` | New file: ADR index listing all eight decision records so 0007 and 0008 are one click away. |
| `docs/adr/0007-run-integration-branch-model.md` | Read only — verify the prose to link/reference accurately in ARCHITECTURE.md and engine-design.md. |
| `docs/adr/0008-manual-reset-rewind-and-replay.md` | Read only — verify the prose to link/reference accurately in engine-design.md. |
| `examples/feature.toml` | Must pass `jig validate` after any example-wiring changes; used as the proof validation command. |
| `examples/bugfix.toml` | Must pass `jig validate`; already has the hand-wired merge step retired in code but the comment explanation should remain. |
| `docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md` | New proof artifact recording each doc section added, validation results, and ADR linkage. |

### Notes

- This spec introduces **no runtime code changes**. All tasks edit `.md` files and create one new proof artifact.
- Run quality gates with: `go build ./...`, `go vet ./...`, `go test ./...`, `go run ./cmd/jig validate examples/feature.toml`.
- Follow the CLAUDE.md convention: comments/doc prose explains the non-obvious *why*, not just *what*.
- ADRs hold the rationale; narrative docs cross-link rather than restating the full rationale.

---

## Tasks

### [x] 1.0 Update `docs/ARCHITECTURE.md` — run-integration-branch model + reset seam

Replace the outdated "Where the engine will plug in (planned)" section with an accurate description of the implemented engine: the run-branch integration model, worktrees branching from run HEAD, squash-per-step commits, the step→commit map, the stop/quiescence seam, and the reset seam (rewind + survivor replay). Add cross-links to ADRs 0007 and 0008.

#### 1.0 Proof Artifact(s)

- Doc: `docs/ARCHITECTURE.md` contains sections describing the run branch, step worktrees off run HEAD, squash-per-step, and the step→commit map — demonstrates the run-integration-branch model is documented.
- Doc: `docs/ARCHITECTURE.md` contains a description of the reset seam (rewind + survivor replay over the dependency closure) — demonstrates the reset model is documented.
- Doc: `docs/ARCHITECTURE.md` contains links to `docs/adr/0007-run-integration-branch-model.md` and `docs/adr/0008-manual-reset-rewind-and-replay.md` — demonstrates ADRs are anchored from the architecture doc.
- CLI: `go build ./...` && `go vet ./...` && `go test ./...` all exit 0 — demonstrates no regressions from doc-only edits.

#### 1.0 Tasks

- [x] 1.1 In the "big picture" diagram, change `internal/engine  (planned)` to `internal/engine` (engine is implemented).
- [x] 1.2 In the `internal/workflow` key design decisions, update the "Worktree isolation is inferred, then overridable" bullet to note that mutating agent steps run in a worktree branched off the **run HEAD** (not repo-root HEAD), so downstream steps see the code upstream steps produced.
- [x] 1.3 Replace the "Where the engine will plug in (planned)" section entirely with a new "## Integration model — the run branch" section that explains: one run branch per run; each step worktree branches off the run-branch HEAD; on step completion jig squash-merges the step's worktree branch back into the run branch as one commit tagged with the step id; the step→commit map this creates; and the final human-gated merge that lands the run branch onto the user's working branch. Cross-link to [ADR 0007](adr/0007-run-integration-branch-model.md).
- [x] 1.4 Immediately after the integration model section, add a "### Stop and reset seam" subsection describing: `Run.Stop` cancels one step's worker without ending the run; a run is **quiescent** when no worker is in flight; reset is only available on a quiescent unfinished run; reset computes the dependency closure of the target, rewinds the run branch to just before the earliest reset-set commit, cherry-picks surviving commits, and returns the reset set to `pending`. Cross-link to [ADR 0008](adr/0008-manual-reset-rewind-and-replay.md).
- [x] 1.5 Update the package layout table: remove any `(planned)` annotation from `engine/`; add a short note that the engine package manages the run-branch worktree lifecycle.

---

### [x] 2.0 Update `docs/workflow-schema.md` — retire merge-step guidance; document no schema surface for stop/reset

Update the Worktrees section to reflect the run-branch model (step worktrees branch off run HEAD). Remove the `[[step]] id = "merge"` hand-wired command step from the end-of-document example and replace it with a note about the final gated merge. Add a brief note that stop and reset are operator/TUI actions with no schema surface.

#### 2.0 Proof Artifact(s)

- Doc: `docs/workflow-schema.md` no longer contains a `[[step]] id = "merge" … run = "git merge …"` code snippet — demonstrates hand-wired merge guidance is retired.
- Doc: `docs/workflow-schema.md` Worktrees section describes step worktrees branching off the run HEAD, not repo-root HEAD — demonstrates the integration model is correctly described.
- Doc: `docs/workflow-schema.md` contains a note that reset and stop add no workflow schema surface — demonstrates the invariant is documented.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0 — demonstrates example workflows remain valid.
- CLI: `go run ./cmd/jig validate examples/bugfix.toml` exits 0 — demonstrates bugfix example remains valid.

#### 2.0 Tasks

- [x] 2.1 In the `## Worktrees` section, update the explanation to reflect that step worktrees branch off the **run-branch HEAD** (not repo-root HEAD), so each step sees the accumulated code changes from upstream steps. Remove the sentence "A downstream join step merges them (and errors on conflict)" — integration is now the engine's responsibility (final gated merge at run end). Keep the `isolation = "worktree"` default rule and the `isolation = "none"` opt-out.
- [x] 2.2 Find the `[[step]]` code block at the end of the full-workflow example (the step with `id = "merge"` and `run = "git merge --no-ff jig/bugfix/fix"`). Remove that step block. In its place add a comment block (or a paragraph following the code block) explaining that explicit `merge` command steps are no longer the integration mechanism: jig runs each step on a per-run integration branch and presents a single final human-gated merge at run end.
- [x] 2.3 Add a new short section or note (suitable location: after `## Execution semantics` or as an addendum to the Worktrees section) stating that **stop and reset are runtime/TUI operator actions** that add no workflow schema surface — `jig validate` is unaffected, and no `.toml` fields are needed to enable them.

---

### [x] 3.0 Update `docs/engine-design.md` — per-step stop/quiescence and reset flow

Add a stop section and a reset section to the low-level design. Update the stale event and status enumerations to include the symbols added by specs 07 and 08 (`StatusStopped`, `StatusAwaitingIntegration`, `StepsReset`, `Generation`). Remove or update the now-stale "merge-join convention" and "Deferred" entries that predate the implementation.

#### 3.0 Proof Artifact(s)

- Doc: `docs/engine-design.md` `internal/step` section documents `StatusStopped`, `StatusAwaitingIntegration`, and `Generation` — demonstrates the status/state model matches the code.
- Doc: `docs/engine-design.md` events section documents `StepsReset` — demonstrates the event vocabulary matches the code.
- Doc: `docs/engine-design.md` contains a "Stop — per-step quiescence" subsection describing `Run.Stop`, `handleStop`, the `stopping` set, quiescence, and when reset becomes available — demonstrates stop semantics are documented.
- Doc: `docs/engine-design.md` contains a "Reset — dependency closure and rewind+replay" subsection describing the crash-consistent write order, `git reset --hard`, survivor cherry-pick, and `Generation` counter bump — demonstrates the reset algorithm is documented.
- Doc: `docs/engine-design.md` cross-links to `docs/adr/0008-manual-reset-rewind-and-replay.md` — demonstrates ADR is anchored.
- CLI: `go build ./...` && `go test ./...` exit 0 — demonstrates no regressions.

#### 3.0 Tasks

- [x] 3.1 In the `### internal/step` section, update the `Status` constants block and `State` struct to match the actual code: add `StatusStopped`, `StatusAwaitingIntegration` (with their one-line explanations), and add the `Generation int` field to `State` (with its provenance-axis note).
- [x] 3.2 In the `### internal/engine — events` section, add `StepsReset` to the event vocabulary table/list, with a note that it is the journaled audit record for an operator reset (target + closure provenance, written before any destructive git operation).
- [x] 3.3 After the `### internal/engine — the scheduler loop` section, add a new subsection `### Stop — per-step quiescence` describing: `Run.Stop` sends a `stopMsg` on the scheduler inbox; `handleStop` cancels the step's child context and adds it to the `stopping` set; the worker detects cancellation and transitions to `StatusStopped` instead of the failure path; the run is **quiescent** when `inFlight == 0` with no pending-runnable steps; a quiescent run is a precondition for `Run.Reset`.
- [x] 3.4 After the stop subsection, add `### Reset — dependency closure and rewind+replay` describing: `Run.Reset(target)` is only valid on an unfinished quiescent run; it computes the reset set (target + transitive `depends_on` closure); writes a `StepsReset` audit event before any git mutation (crash-consistent ordering); runs `git reset --hard` to the commit just before the earliest reset-set commit; cherry-picks surviving commits (those not in the reset set); returns the reset set to `pending` with `Generation++`; cross-link to [ADR 0008](../adr/0008-manual-reset-rewind-and-replay.md).
- [x] 3.5 In the `### Phase 5 — mutation, safely` section, remove the "merge-join convention" bullet (it describes the old per-step-isolation model). The phase may note that step worktrees branch off the run HEAD and squash-merge back on completion.
- [x] 3.6 In the `### Deferred` section, remove or cross-out stop/reset if listed there (they are implemented). Keep resume-from-journal and map/fan-out as genuinely deferred.

---

### [x] 4.0 Create ADR index and generate proof artifact

Create `docs/adr/README.md` as an ADR index listing all eight decision records. Verify that ADRs 0007 and 0008 are linked from the updated docs. Run `jig validate` on all examples and the full build/test suite. Create the proof artifact file.

#### 4.0 Proof Artifact(s)

- Doc: `docs/adr/README.md` exists and lists ADRs 0001–0008 with titles and one-line summaries and links — demonstrates the ADR index is created and ADRs 0007/0008 are one click away.
- File: `docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md` exists and records each doc section updated, the example validation commands and exit codes, and ADR linkage confirmation — demonstrates spec acceptance criteria are met.
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0 — final confirm all examples valid.
- CLI: `go build ./...` && `go vet ./...` && `go test ./...` all exit 0 — final confirm no regressions.

#### 4.0 Tasks

- [x] 4.1 Create `docs/adr/README.md` with a table (or list) of all ADRs in the directory in sequence order (0001–0004, 0006–0008; note ADR 0005 is absent — do not invent it): for each, link the file, show the title, status (proposed/accepted), and a one-line summary of the decision. Titles and summaries come from reading each ADR file's first heading and opening sentence.
- [x] 4.2 Verify that `docs/ARCHITECTURE.md` (written in task 1) and `docs/engine-design.md` (written in task 3) both contain working relative links to `docs/adr/0007-…` and `docs/adr/0008-…` respectively.
- [x] 4.3 Run `go run ./cmd/jig validate` on all four example workflows (`feature.toml`, `bugfix.toml`, `research.toml`, `review.toml`); confirm all exit 0.
- [x] 4.4 Run `go build ./...`, `go vet ./...`, `go test ./...`; confirm all exit 0.
- [x] 4.5 Create `docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md` documenting: (a) which sections were added or replaced in each doc file, (b) the example validation commands and their exit codes, (c) confirmation that ADR 0007 and 0008 are linked from ARCHITECTURE.md and engine-design.md, and (d) the full build/test/vet commands and exit codes.
