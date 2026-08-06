# Task 06 Proofs — docs, example alignment, ADR 0006, skill-narration removal

## Task Summary

This task updates the spec-of-record and proves the payoff. It documents the
engine-assembled step-context preamble (`docs/workflow-schema.md`), exercises the
new `inject_context` and `[step.context]` fields in `examples/feature.toml`,
records the decision as ADR 0006, and removes the now-redundant
upstream/downstream/loop narration from the `plan` and `implement` skills — the
narration the engine now supplies deterministically. It also folds in the audit's
FLAG-2 `README.md` doc-hygiene fix.

## What This Task Proves

- The workflow schema doc describes the always-on preamble, its framing-only
  guarantee, the rendered format, and both new fields.
- The kitchen-sink example uses an explicit `inject_context = false` override and
  two `[step.context]` blocks, and still validates.
- ADR 0006 records the decision and rejected alternatives, honoring ADR 0003.
- The `plan`/`implement` skills shed their hand-authored position/loop narration.
- A human spot-check shows the `plan` agent's orientation is preserved (old prose
  → engine preamble).
- The whole module is green under `-race`; `README.md` staleness is fixed.

## Evidence Summary

- `docs/workflow-schema.md`: new "Step context (engine-assembled)" section +
  `inject_context`/`[step.context]` in the `[defaults]` and agent-step tables.
- `examples/feature.toml`: `inject_context = false` on `research_frontend`;
  `[step.context]` on `plan` (purpose+notes) and `research_backend` (purpose);
  refreshed header. Validates OK (`6.0-validate-ok.txt`).
- `docs/adr/0006-engine-assembles-step-context-preamble.md`.
- Skill diff (`6.0-skill-narration-removed.txt`); behavioral spot-check
  (`6.0-preamble-spotcheck.md`).
- Regenerated goldens `2.0-plan-preamble.txt` / `3.0-revise-loop-preamble.txt`
  now show the example's `[step.context]` purpose/notes and propagation.
- `README.md`: Go 1.24 → 1.25; "engine not built yet" → "runs today".

## Artifact: Example still validates

**What it proves:** The schema additions are exercised by the example and it
still passes `jig validate`.

**Why it matters:** Examples are executable documentation — a broken kitchen-sink
example is a broken doc.

**Artifact path:** `03-proofs/6.0-validate-ok.txt`

**Result summary:** `ok: "feature" v1 — 15 step(s)`, exit 0.

## Artifact: Skill-narration removal

**What it proves:** The `plan` and `implement` skills no longer hand-author the
"you receive research from two agents" / "reviewer feedback present only when…" /
"QA feedback present only when…" / "this is what X reads" narration.

**Why it matters:** This is the payoff — the engine now owns that framing
deterministically, so the skills stop drifting from the actual graph.

**Artifact path:** `03-proofs/6.0-skill-narration-removed.txt`

**Result summary:** Four passages removed from `plan/SKILL.md` and two from
`implement/SKILL.md`, keeping only job instructions.

## Artifact: Behavioral spot-check (manual)

**What it proves:** Every orientation fact the old prose gave the `plan` agent is
present (and more accurate) in the engine-assembled preamble.

**Why it matters:** This is the acceptance evidence for "behavior preserved" — a
human review, explicitly not an automated equivalence gate (spec Unit 6).

**Artifact path:** `03-proofs/6.0-preamble-spotcheck.md`

**Result summary:** Old→new orientation mapping shows full coverage plus two facts
the prose lacked (the `security_scan` upstream and the guarded `implement` edge).
Final human sign-off on a live run is left as a checkbox for the reviewer.

## Artifact: Regenerated preamble goldens

**What it proves:** With the example's new `[step.context]` blocks, the `plan`
preamble carries its own `Purpose:`/`Notes:` and `research_backend`'s propagated
purpose; the first-run and revise forms are locked byte-for-byte.

**Why it matters:** Confirms the Unit 2/3 goldens track the real example after the
Unit 5/6 authoring blocks were added.

**Command:**

~~~bash
go test ./internal/engine -run 'TestBuildRequest(Plan|ReviseLoop)PreambleGolden'
~~~

**Result summary:** PASS — goldens regenerated via `UPDATE_PREAMBLE_GOLDEN` and
committed (`2.0-plan-preamble.txt`, `3.0-revise-loop-preamble.txt`).

## Artifact: Full regression (spec Success Metric 6)

**What it proves:** The change is green across the module under the race detector,
including the persistence-off engine/runner paths.

**Command:**

~~~bash
gofmt -l -w . && go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml
~~~

**Result summary:** gofmt/vet clean, all packages PASS under `-race`,
`ok: "feature" v1 — 15 step(s)`.

## Scoping note

The example's "FEATURES NOT YET IN THE SCHEMA / UNDOCUMENTED" comment blocks were
left as-is: they concern `block_on`/`max_messages` and other unrelated features,
not step context. Only the "exercises every construct" header list was refreshed
(the in-scope change), plus the FLAG-2 `README.md` fix.

## Reviewer Conclusion

Step context is now documented as a first-class deterministic contract, exercised
by the example, recorded in ADR 0006, and proven to subsume the hand-authored
skill narration. The one open item is the human sign-off on the spot-check, which
is by design a manual review.
