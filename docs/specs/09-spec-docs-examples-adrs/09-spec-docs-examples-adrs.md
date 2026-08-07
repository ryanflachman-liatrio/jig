# 09-spec-docs-examples-adrs.md

> **Unit D of the [`05-spec-run-integration-reset`](../05-spec-run-integration-reset/05-spec-run-integration-reset.md) mega-spec**, split into its own SDD workflow.
> This is the **documentation-alignment** unit: it updates the architecture, schema, and
> engine-design docs, retires the hand-wired `merge`-step guidance in the examples, and links the
> two ADRs, so the repo's prose matches the behavior built in Foundations A/B and Feature C. It
> depends on all three shipping first:
> A ([`06-spec-run-integration-branch`](../06-spec-run-integration-branch/06-spec-run-integration-branch.md)),
> B ([`07-spec-stop-resume-step`](../07-spec-stop-resume-step/07-spec-stop-resume-step.md)),
> C ([`08-spec-reset-to-step`](../08-spec-reset-to-step/08-spec-reset-to-step.md)).
> Read the parent mega-spec for the full cross-foundation rationale; this document is
> self-contained for implementation.

## Introduction/Overview

The run-integration-branch model (Foundation A), per-step stop/resume (Foundation B), and reset
(Feature C) change load-bearing behavior that the repo's documentation currently describes the *old*
way: `docs/ARCHITECTURE.md` describes per-step isolation off repo-root HEAD, `docs/workflow-schema.md`
presents the hand-wired `merge` command step as the integration mechanism, and `docs/engine-design.md`
has no account of stop or reset. This unit brings the prose in line with the code so a future reader
or workflow author is not misled.

It also confirms the load-time contract is unchanged: reset and stop add **no** workflow schema
surface, so `jig validate examples/feature.toml` must still exit 0 and the example workflows must
still validate after the `merge`-step guidance is retired.

This unit changes documentation and example wiring only — it introduces no new runtime behavior of
its own; the behaviors it documents are delivered by the three prior specs.

## Goals

- **Make the docs true.** `ARCHITECTURE.md`, `workflow-schema.md`, and `engine-design.md` describe the
  run-integration-branch model, stop, and reset as built, replacing the per-step-isolation and
  hand-wired-merge descriptions.
- **Retire hand-wired merge guidance in examples** now that the final gated merge is the integration
  mechanism, keeping every example valid.
- **Anchor the decisions.** ADRs 0007 (integration model) and 0008 (reset algorithm) are linked from
  wherever ADRs are indexed, so the rationale is discoverable.

## User Stories

**As a workflow author reading the docs**, I want the architecture and schema docs to describe how
code actually flows between steps (the run branch and the final gated merge), so I don't hand-wire a
`merge` step that is no longer the right pattern.

**As a contributor to the engine**, I want the engine-design doc to describe per-step stop and the
reset flow, so I understand the single-writer actions before I touch them.

**As someone auditing the design later**, I want ADRs 0007 and 0008 linked from the ADR index, so the
load-bearing decisions are one click away.

## Demoable Units of Work

### Unit D: Docs, examples, ADRs

**Functional Requirements:**
- `docs/ARCHITECTURE.md` shall document the run-integration-branch model (run branch, step worktrees
  off run HEAD, squash-per-step, the step→commit map) and the reset seam (rewind + survivor replay),
  replacing the per-step-isolation description.
- `docs/workflow-schema.md` shall note that explicit `merge` command steps are no longer the
  integration mechanism (the final gated merge is), and that reset/stop are runtime/TUI actions adding
  **no** workflow schema surface (`jig validate examples/feature.toml` exits 0).
- `docs/engine-design.md` shall describe stop (per-step context) and the reset flow.
- ADRs [0007](../../adr/0007-run-integration-branch-model.md) (integration model) and
  [0008](../../adr/0008-manual-reset-rewind-and-replay.md) (reset algorithm) are recorded; link them
  from any ADR index.

**Proof Artifacts:**
- `go build ./...`, `go vet ./...`, `go test ./...` pass, including persistence-off paths.
- `jig validate examples/feature.toml` (and any other example workflows) exits 0 after the
  `merge`-step guidance is retired.
- `09-proofs/D.0-docs-and-adr.md` — the doc sections added and examples updated.

## Non-Goals (Out of Scope)

1. **New runtime behavior.** This unit documents behavior delivered by Foundations A/B and Feature C;
   it adds no engine or TUI code beyond example-workflow wiring.
2. **A workflow schema surface for reset/stop.** The docs shall *state* that there is none; they do
   not add one.
3. **Rewriting example workflow logic** beyond dropping the now-redundant hand-wired `merge` command
   steps and keeping the examples valid.

## Design Considerations

Documentation only. The prose should mirror the structure the code now has — run branch, step
worktrees off run HEAD, squash-per-step, the step→commit map, stop/quiescence, and rewind + survivor
replay — and cross-link the two ADRs rather than restating their full rationale.

## Repository Standards

- **Examples are documentation.** `examples/feature.toml` is the kitchen-sink reference; it and every
  other example must still pass `jig validate` after edits.
- **Comments/docs explain the non-obvious "why."** The doc updates should explain *why* integration
  moved into the engine and *why* reset is rewind+replay, not just *what* changed.
- **ADRs are the durable decision record.** Link them from the index; do not duplicate their content
  into the narrative docs.

## Technical Considerations

- **No schema change.** The load-time validator is untouched; the docs must assert that reset/stop add
  no schema surface, and the example-validation proof backs that assertion.
- **Ordering.** This unit is meaningfully completable only after A, B, and C land, since it documents
  their behavior and retires wiring they replace.

## Security Considerations

- **No new attack surface.** Documentation and example-wiring changes only; the security posture is
  set by the prior specs (bounded git/file ops, the human-gated final merge, reset confirmation).

## Success Metrics

1. **Docs match code:** `ARCHITECTURE.md`, `workflow-schema.md`, and `engine-design.md` describe the
   integration branch, stop, and reset as implemented, with no lingering per-step-isolation or
   hand-wired-merge description.
2. **Examples valid:** `jig validate` exits 0 on every example after the `merge`-step guidance is
   retired.
3. **ADRs linked:** 0007 and 0008 are reachable from the ADR index.
4. **No regressions:** `go build/vet/test ./...` pass including persistence-off paths.

## Design Decisions & Rationale

1. **Docs alignment is a distinct, last unit.** It depends on the behavior of all three prior specs
   and is best done once they are stable, so the prose describes shipped behavior rather than a moving
   target.
2. **ADRs hold the rationale; narrative docs link to them.** Keeps the architecture/engine docs
   concise and the decisions authoritative in one place.

## Open Questions

No open questions at this time. This unit is a documentation pass gated on the completion of
Foundations A/B and Feature C; any behavioral ambiguity is resolved in those specs, not here.
