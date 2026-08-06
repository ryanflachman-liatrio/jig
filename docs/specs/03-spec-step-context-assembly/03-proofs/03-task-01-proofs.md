# Task 01 Proofs — `StepContext` data model & pure renderer

## Task Summary

This task proves jig has a typed, engine-agnostic model of a step's workflow
position (`StepContext` / `ContextNeighbor` in `internal/step`) and a pure
`Render() string` that turns it into the deterministic "Workflow context"
preamble. No engine wiring yet — the format is locked in isolation by golden
tests, so later units can assemble against a fixed contract. This matters because
the rendered byte layout is the one user-visible artifact of the whole spec, and
its determinism (fixed neighbor ordering, no map iteration into output) is what
makes the preamble safe to prepend to every agent turn.

## What This Task Proves

- A fully-populated `StepContext` renders **byte-for-byte** to the committed
  golden, exercising every render branch (own purpose/notes, an upstream with and
  without a purpose, a `succeeded` and a `failed` status, a propagated-purpose
  downstream and a guarded graph-derived downstream, the iteration clause, and the
  `State` line).
- Rendering is **deterministic**: 100 renders of the same input are identical.
- Absent parts are **omitted gracefully**: the zero value renders `""`, a
  topology-only struct drops the Purpose/Notes/State lines and the iteration
  clause, and a pipeline-entry step renders only the position line + `---`.
- `internal/step` still **imports nothing** beyond `fmt`/`strings` — the renderer
  is independently testable and carries no engine/workflow types.

## Evidence Summary

- `go test ./internal/step -run TestStepContextRender -v` passes all three tests
  (golden, stable, omits).
- The golden `1.0-render-golden.txt` (pre-committed with the spec) is the
  assertion target; the renderer reproduces it exactly, so the golden and the
  spec's "Rendered format" block cannot drift.
- `gofmt`, `go vet`, and the full `go test ./... -race` suite are green — the new
  package compiles cleanly and breaks nothing.

## Artifact: Golden, determinism, and omission tests

**What it proves:** `Render()` produces the exact locked byte layout, does so
deterministically, and omits absent parts.

**Why it matters:** The golden byte-equality is the acceptance gate for the whole
format; if this passes, every downstream unit assembles against a fixed,
proven contract.

**Command:**

```bash
go test ./internal/step -run TestStepContextRender -v
```

**Result summary:** All three tests pass. `TestStepContextRenderGolden` compares
`Render()` against the committed `03-proofs/1.0-render-golden.txt` byte-for-byte;
`TestStepContextRenderStable` renders 100× with identical output;
`TestStepContextRenderOmits` covers the zero-value, topology-only, and
pipeline-entry cases.

```
=== RUN   TestStepContextRenderGolden
--- PASS: TestStepContextRenderGolden (0.00s)
=== RUN   TestStepContextRenderStable
--- PASS: TestStepContextRenderStable (0.00s)
=== RUN   TestStepContextRenderOmits
=== RUN   TestStepContextRenderOmits/zero_value_renders_empty
=== RUN   TestStepContextRenderOmits/topology-only_omits_optional_lines
=== RUN   TestStepContextRenderOmits/pipeline-entry_renders_position_line_and_delimiter_only
--- PASS: TestStepContextRenderOmits (0.00s)
    --- PASS: TestStepContextRenderOmits/zero_value_renders_empty (0.00s)
    --- PASS: TestStepContextRenderOmits/topology-only_omits_optional_lines (0.00s)
    --- PASS: TestStepContextRenderOmits/pipeline-entry_renders_position_line_and_delimiter_only (0.00s)
PASS
ok  	jig/internal/step	0.454s
```

## Artifact: The committed render golden

**What it proves:** The exact byte layout the renderer must produce — header,
position line with iteration clause, Purpose/Notes, Upstream/Downstream blocks,
`State` line, and the trailing `---` with no trailing newline.

**Why it matters:** This is the assertion target the golden test reads, and it is
identical to the spec's "Rendered format" block — a single source of truth.

**Artifact path:** `03-proofs/1.0-render-golden.txt` (799 bytes, no trailing
newline)

**Result summary:** `Render()` reproduces this file exactly; the test reads it
directly rather than embedding a copy, so the proof and the golden cannot drift.

```
## Workflow context

You are step `plan` in workflow `feature` (iteration 2 of 3).
Purpose: turn research findings into an ordered implementation plan.
Notes: prefer the smallest change that satisfies the acceptance criteria.

Upstream (already complete):
- `intake` (succeeded) — classify the request and extract research areas
- `research_backend` (failed)
These reach you as the inputs listed below; this section is orientation only.

Downstream (what your output feeds):
- `plan_review` (human review) — a person reviews your summary and approves or requests revisions
- `implement` (agent) — consumes your `tasks` (conditional on `approved == true`)

State: re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.

---
```

## Artifact: Full regression under the race detector

**What it proves:** The new package builds and the entire existing suite still
passes, including the persistence-off engine/runner paths.

**Why it matters:** Unit 1 adds a new file to `internal/step` (imported widely);
this confirms it introduces no build break or data race anywhere.

**Command:**

```bash
go build ./... && go test ./... -race
```

**Result summary:** Build succeeds; every package passes under `-race`.

```
ok  	jig/internal/datastore	1.462s
ok  	jig/internal/engine	2.730s
ok  	jig/internal/runner	1.780s
ok  	jig/internal/step	3.016s
ok  	jig/internal/transcript	2.588s
ok  	jig/internal/tui	3.601s
ok  	jig/internal/workflow	3.863s
```

## Reviewer Conclusion

The renderer produces the spec's exact byte layout deterministically and omits
absent parts gracefully, proven by a golden byte-equality test against the
committed `1.0-render-golden.txt`, a 100× stability test, and omission cases.
`internal/step` stays import-free and the full `-race` suite is green. The format
contract is locked; Unit 2 can now assemble a `StepContext` from the engine
against it.
