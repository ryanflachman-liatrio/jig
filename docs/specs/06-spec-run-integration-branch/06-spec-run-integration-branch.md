# 06-spec-run-integration-branch.md

> **Foundation A of the [`05-spec-run-integration-reset`](../05-spec-run-integration-reset/05-spec-run-integration-reset.md) mega-spec**, split into its own SDD workflow.
> This foundation builds the **run-integration-branch** model (ADR
> [0007](../../adr/0007-run-integration-branch-model.md)) so steps compose on each other's
> code and a run lands once under human control. It is the dependency root: Foundation B
> ([`07-spec-stop-resume-step`](../07-spec-stop-resume-step/07-spec-stop-resume-step.md)) and
> Feature C ([`08-spec-reset-to-step`](../08-spec-reset-to-step/08-spec-reset-to-step.md))
> both build on the addressable per-step commit history this foundation creates. Read the
> parent mega-spec for the full cross-foundation rationale; this document is self-contained
> for implementation.

## Introduction/Overview

jig puts a deterministic orchestration layer around non-deterministic agents. Today the part
of that layer that handles **code changes between steps** is too thin for real multi-step
engineering workflows.

One fact about the current engine, verified against the code, frames this foundation:

- **Steps cannot build on each other's code.** Each mutating step runs in its own git
  worktree branched off the **repo-root HEAD** (`internal/engine/engine.go:720-742`,
  `worktree.go:23-37`), so `implement` never sees the code `plan`'s predecessor wrote. Steps
  exchange only structured `@ref` output; code lands on the user's branch only via an explicit
  `merge` **command** step an author wires by hand (`examples/bugfix.toml`).

This foundation replaces that model with a **per-run integration branch**: a branch created at
run start into which each step's changes are squash-merged (one commit per step, tagged with
the step id). Each step's worktree branches off the run branch's *current* HEAD at dispatch
time, so steps compose on code. Integration conflicts surface through a human gate rather than
being auto-resolved or failing silently, and one final human-gated merge lands the run branch
on the user's working branch — retiring the hand-wired `merge` command steps.

This addressable, linear per-step history is also the substrate the later foundations need: a
per-step commit map is what makes a run *rewindable* (Feature C's reset). This foundation adds
that history but no rewind; it is complete and demoable on its own — steps compose, conflicts
are human-owned, and the run lands once.

**Invariants preserved.** `state = fold(journal.jsonl)` with an append-only journal
(`internal/engine/replay.go`) is unchanged. `StepStatus` transitions remain journaled.
Transcripts are logs and are never truncated. Persistence-off / non-git (`runDir == ""` /
`repoRoot == ""`) stays a first-class no-op path: no run branch, no worktrees, steps run in
place, integration is skipped.

## Goals

- **Let steps build on each other's code** via a per-run integration branch, with each step
  contributing exactly one squash commit, keeping each step's turn isolated in its own worktree.
- **Land the run once, under human control** — a single gated merge of the run branch onto the
  user's working branch, replacing hand-wired `merge` command steps.
- **Surface integration conflicts to a human**, never auto-resolving overlapping changes.
- **Keep determinism and the durable record.** No new graph edges, no new termination risk;
  integration is serialized in a deterministic order so the run branch has a single linear
  history — the property that makes the per-step commits addressable for the later foundations.

## User Stories

**As an author of a code-writing workflow**, I want downstream steps to see the code upstream
steps wrote, so I can chain `plan → implement → test` without hand-wiring merges.

**As an operator at the end of a run**, I want to approve one final merge of the run's work
onto my branch, so integration is explicit and under my control.

**As an operator whose two parallel steps touched the same file**, I want the conflict surfaced
to me to resolve, so jig never silently guesses whose version wins.

## Demoable Units of Work

Dependency-ordered. Foundation A (Units A1–A3) builds the integration model. Each unit is a
candidate implementation increment.

### Unit A1: Run branch + step worktrees off run HEAD + squash-per-step integration

**Purpose:** Replace "each step off repo-root HEAD" with "each step off the run branch's
current HEAD, squash-merged back as one commit." The core of code composition.

**Functional Requirements:**
- On run start (when `repoRoot != ""`), the system shall create a **run branch** at the
  user's working-branch HEAD and a run worktree, recorded on the scheduler.
- A mutating step's worktree (`isolation = "worktree"`; auto-enabled per
  `internal/workflow/load.go:134` + `mutatingTools`) shall branch off the **run branch's
  current HEAD at dispatch time**, not repo-root HEAD. Read-only steps get no worktree.
- On a step's successful completion, the system shall **squash-merge that step's worktree
  branch into the run branch as exactly one commit**, whose message is tagged with the step
  id (proposed trailer `jig-step: <stepID>`), advancing the run branch HEAD by one commit.
- The system shall maintain a **step → commit map** (`stepCommits[stepID] = sha`) on the
  scheduler, so a step's contribution is addressable later (used by Feature C). The map shall
  be reconstructable from the run branch history via the `jig-step` trailer.
- Parallel dispatch shall still be honored, but **integration is serialized** in a
  deterministic order (declaration order) so the run branch has a single linear history.
- Persistence-off / non-git (`repoRoot == ""`) shall remain a no-op path: no run branch, no
  worktrees, steps run in place, integration is skipped.

**Proof Artifacts:**
- Unit test: `A → B` where `A` writes `file_a`, `B` reads it and writes `file_b`; assert
  `B`'s worktree contains `file_a` (it branched off the run branch after `A` integrated),
  and the run branch ends with two commits tagged `jig-step: A`, `jig-step: B`.
- Unit test: read-only step produces no commit; the run branch HEAD is unchanged across it.
- Proof artifact: `06-proofs/A1.0-run-branch-log.txt` — `git log --format=%s` of the run
  branch showing one tagged commit per mutating step.

### Unit A2: Integration-conflict gate

**Purpose:** When a squash-merge (A1) hits a conflict — two steps touched the same lines —
surface it to a human instead of failing silently or auto-resolving.

**Functional Requirements:**
- On an integration conflict, the system shall create a new Gate **input entry** kind
  (proposed `inputKindIntegrationConflict`) naming the step and the conflicted paths, and
  transition the step to a parked state; the run stays alive (non-blocking gate, ADR 0002).
- The operator resolves the conflict in the run worktree, then signals completion through
  the Gate; the engine finishes the integration and continues. An abort path shall fail the
  step (routing to the existing recovery gate).
- The conflict resolution shall be a single-writer scheduler message + handler, mirroring
  `Run.Resolve`/`handleRecover` (`engine.go` inbox pattern).

**Proof Artifacts:**
- Unit test: two parallel steps writing the same file line; the second integration raises
  the gate; resolving it lands a merged commit; the run completes.
- TUI test: the integration-conflict entry renders in the Gate with the conflicted paths and
  takes focus like other entries.
- Proof artifact: `06-proofs/A2.0-integration-conflict-gate.txt`.

### Unit A3: Final gated merge (run branch → user branch)

**Purpose:** Land the run's work once, under human control; retire hand-wired `merge`
command steps.

**Functional Requirements:**
- At run end (all steps terminal), the system shall present a **final merge gate**: merge the
  run branch onto the user's working branch, or discard. On approve, jig performs the merge;
  on discard, the run branch is left for inspection and nothing lands.
- The `examples/*.toml` workflows that hand-merge step branches (e.g. `bugfix.toml`) shall be
  updated to drop the explicit `merge` command step; `jig validate` shall still exit 0.

**Proof Artifacts:**
- Unit test: after a run, approving the final merge fast-forwards/merges the run branch onto
  the base and the base HEAD contains the run's commits; discarding leaves the base untouched.
- Proof artifact: `06-proofs/A3.0-final-merge.txt` — base branch log before/after approve.

## Non-Goals (Out of Scope)

1. **Stop/resume and reset.** This foundation adds the integration history only; per-step
   cancellation (Foundation B) and rewind/replay reset (Feature C) are separate SDD workflows
   that build on the per-step commit map this foundation creates.
2. **Auto-resolving integration conflicts.** Overlapping changes are human-resolved through
   the Gate; jig never guesses whose version wins.
3. **Undoing an already-final-merged run from the user's branch.** Once the final gated merge
   lands the run branch on the user's branch, that is history the user owns.
4. **A workflow schema surface for integration.** Integration is a runtime/engine concern; no
   per-step flag and no new load-time validation are added.
5. **Preserving an agent's intermediate commits.** Squash-per-step is settled — one addressable
   commit per step. Inspecting intermediate commits is a separate future change to the
   integration step, not part of this foundation.

## Design Considerations

The new visual surfaces are the **integration-conflict gate** and the **final merge gate** —
both reuse the existing Gate/entry pattern and precedence (ADR 0002), so they inherit focus
handling and styling. Footer hints for the final-merge approve/discard follow the mode-specific
`footerView` convention.

## Repository Standards

- **Engine extensibility, thin consumers (ADR 0003).** Integration and the final merge live in
  the scheduler — the single owner of state, the DAG, the run branch, and the journal writer;
  the TUI only sends messages and renders.
- **Single-writer scheduler.** Every new action (conflict resolve, final-merge approve/discard)
  is one `inbox` message + one `handle` case — no locks.
- **File is truth, bus is liveness.** Integration mutates the run branch; the bus carries only
  `StepStatus` liveness.
- **Persistence-off / non-git is first-class.** Every git and disk operation no-ops when there
  is no run dir / no repo root; the engine tests that run without them must keep passing.
- **Tests are table-driven with inline fixtures**, and new engine behavior gets both the happy
  path and the guard/conflict path.
- **Comments explain the non-obvious "why"** — especially serialized integration over a parallel
  DAG and the squash-per-step tagging.

## Technical Considerations

- **Serialized integration over a parallel DAG.** Steps still run in parallel, but their
  squash-merges into the run branch are serialized in declaration order to keep a single linear
  history — the property that makes each step's contribution addressable by commit (relied on by
  Feature C's reset).
- **The step → commit map is reconstructable** from the run branch history via the `jig-step`
  trailer, so it survives a process that reads an existing run branch, not only the in-memory map.
- **No schema, no load-time validation** is added; the "schema additions are exhaustive/load-time"
  rule does not apply here.

## Security Considerations

- **Bounded git and file operations.** Integration operates only on the run branch and jig's own
  worktrees, using worktree helpers exclusively — never a path from user input.
- **The final merge is the only write to the user's branch, and it is human-gated.** Nothing
  lands on the user's working history without an explicit approval.
- **Conflicts pause for a human** rather than being auto-resolved.

## Success Metrics

1. **Code composes:** in `A → B`, `B`'s worktree contains `A`'s changes, and the run branch has
   one tagged commit per mutating step (A1).
2. **Conflicts are human-owned:** an overlapping integration raises the gate and resolves
   cleanly; it is never auto-resolved (A2).
3. **One gated landing:** the run's work reaches the user's branch only via the approved final
   merge (A3).
4. **No regressions:** `go build/vet/test ./...` pass including persistence-off paths, and
   `jig validate examples/feature.toml` exits 0 (no schema change).

## Design Decisions & Rationale

Resolved with the operator across the 2026-08-07 grilling session, grounded in read-only
exploration of `internal/engine`, `internal/datastore`, `internal/transcript`,
`internal/runner`, `internal/workflow`, and `internal/tui`. Recorded in ADR
[0007](../../adr/0007-run-integration-branch-model.md).

1. **Steps compose on code via a per-run integration branch, squash-per-step.** The
   per-step-isolation-off-repo-HEAD model cannot deliver "downstream builds on upstream code,"
   which is the actual requirement. Squash-per-step gives the addressable, linear history the
   later reset foundation needs.
2. **Integration conflicts are human-resolved via the Gate.** jig cannot statically prevent two
   agents from writing the same file, so a runtime resolution path is the honest answer; a
   dedicated gate keeps it deterministic and inspectable.
3. **One final human-gated merge lands the run branch**, replacing hand-wired `merge` command
   steps — integration becomes an engine concern with a single explicit approval.
4. **Integration is serialized in declaration order** even though steps run in parallel, so the
   run branch is a single linear history addressable by commit.

## Open Questions

1. **Run-branch naming and lifetime.** Proposed `jig/<workflow>/run-<runID>`; confirm the naming
   and whether the run branch is deleted after the final merge or kept for inspection. Non-blocking:
   a sensible default (keep for inspection) can ship and be refined.
2. **Squash vs. preserve step commits.** Squash-per-step is settled (one addressable commit per
   step); if a future need arises to inspect an agent's intermediate commits, that is a separate
   change to the integration step, not to this foundation.
