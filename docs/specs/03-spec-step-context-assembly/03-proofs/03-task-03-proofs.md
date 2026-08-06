# Task 03 Proofs — Run-state framing (loops & re-runs)

## Task Summary

This task populates the **state** part of the preamble so a re-run agent is told
it is iterating and why. At the existing `fireLoop` write site the scheduler now
records the firing loop's source step in a new in-memory `rerunSource` map
(`goto → sourceStepID`, last-fired wins). At dispatch, `buildStepContext` reads
that entry to set `Iteration`, the firing loop's `MaxIterations`, and a
`RerunReason` derived from the source step's type — a review verdict versus an
agent/command gate failure. No new persisted state and no new events, matching
the spec's scope: only loop re-runs re-run `buildAgentPrompt` (a block_on resume
continues the existing conversation).

## What This Task Proves

- A `plan_review == 'revise'` loop fire re-dispatches `plan` at `Iteration == 1`,
  rendering **`(iteration 2 of 3)`** and the revise **State** line.
- An agent/command **gate** loop renders the **gate-failure** phrasing, and the
  source is recorded **even when the loop wires no feedback ref** (the write lives
  outside the `if loop.Feedback != ""` block).
- A **first run** (no `rerunSource` entry) renders no iteration clause and no
  State line.
- **Multiple loops targeting one step** (audit FLAG-1): firing a review loop then
  a gate loop leaves the **last-fired** source driving the State line
  (last-write-wins), with the gate loop's own cap in the iteration clause.
- The reconciled `3.0-revise-loop-preamble.txt` is exactly what the engine renders
  for the real example on its revise iteration.

## Evidence Summary

- `go test ./internal/engine -run TestWorkflowContext` — revise, gate-rerun,
  first-run, and multiple-loops all pass.
- `go test ./internal/engine -run TestBuildRequestReviseLoopPreambleGolden` — the
  3.0 proof matches the engine's render.
- `go test ./... -race` green; `go vet`/`gofmt` clean; `jig validate
  examples/feature.toml` exits 0.

## Design decision recorded

**Iteration is populated alongside MaxIterations (not unconditionally).** The
iteration clause "iteration N of M" needs both numbers, and only the firing loop's
goto target has a recorded source (hence a known cap). Populating `Iteration` only
when a `rerunSource` entry resolves keeps the goto target's output identical to
the literal task for every in-scope case (first run and re-run), while avoiding a
malformed "iteration N of 0" for an intermediate loop-body step that has an
incremented iteration but no recorded source. This honors tasks 3.3/3.4's data
sources (`rerunSource` → source step → `Loop.MaxIterations` / type) and the
spec's determinism/honesty intent.

## Reconciliation note (3.0 golden corrected)

Like the 2.0 proof, the pre-authored `3.0-revise-loop-preamble.txt` rendered the
review consumer as "a person reviews your **output**". The engine now extracts the
review target field per spec Unit 2 + task 2.3, so the committed golden was
regenerated. The diff is a single line (`output` → `` `summary` ``); the iteration
clause and State line were already correct.

## Artifact: Run-state framing tests

**What it proves:** The revise/gate/first-run/multiple-loops cases all render the
correct state framing, and the source is recorded at `fireLoop` even without
feedback.

**Why it matters:** A wrong iteration number, a missing State line, or the wrong
source on a multi-loop target would mislead the re-run agent.

**Command:**

```bash
go test ./internal/engine -run 'TestWorkflowContext' -v
```

**Result summary:** All four pass. The revise test drives a real `fireLoop` and
asserts `(iteration 2 of 3)` + the revise State line; the gate test asserts the
gate phrasing and `rerunSource[build] == "qa"` with no feedback ref; the first-run
test asserts both optional parts are omitted; the multiple-loops test fires a
review loop then a gate loop and asserts the gate (last-fired) drives the State
line while the superseded revise phrasing is absent.

```
=== RUN   TestWorkflowContextReviseIteration
--- PASS: TestWorkflowContextReviseIteration (0.00s)
=== RUN   TestWorkflowContextGateRerun
--- PASS: TestWorkflowContextGateRerun (0.00s)
=== RUN   TestWorkflowContextFirstRun
--- PASS: TestWorkflowContextFirstRun (0.00s)
=== RUN   TestWorkflowContextMultipleLoops
--- PASS: TestWorkflowContextMultipleLoops (0.00s)
PASS
ok  	jig/internal/engine
```

## Artifact: Revise-iteration preamble for the real example

**What it proves:** End-to-end, driving a `plan_review`-triggered revise fire on
the real example produces the expected preamble — same topology as the first-run
2.0 proof plus the iteration clause and the State line.

**Why it matters:** This is the human-readable demo of the whole unit, asserted by
`TestBuildRequestReviseLoopPreambleGolden`.

**Artifact path:** `03-proofs/3.0-revise-loop-preamble.txt`

**Result summary:** The `plan` step on its revise iteration is told it is on
iteration 2 of 3 and why (`plan_review` requested revisions).

```
## Workflow context

You are step `plan` in workflow `feature` (iteration 2 of 3).

Upstream (already complete):
- `research_backend` (succeeded)
- `research_frontend` (succeeded)
- `security_scan` (succeeded)
These reach you as the inputs listed below; this section is orientation only.

Downstream (what your output feeds):
- `plan_review` (human review) — a person reviews your `summary`
- `implement` (agent) — consumes your `tasks`, `approach` (conditional on `plan_review == 'approve'`)

State: re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.

---
```

## Artifact: Full regression + example validation

**Command:**

```bash
go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml
```

**Result summary:** Vet clean; every package passes under `-race`; validate exits
0 with `ok: "feature" v1 — 15 step(s)`.

```
ok  	jig/internal/engine	1.982s
...
ok: "feature" v1 — 15 step(s)
```

## Reviewer Conclusion

The preamble now carries honest run-state framing for loop re-runs: the iteration
clause and a typed re-run reason, recorded deterministically at the single
`fireLoop` site with last-write-wins semantics proven for the multi-loop target.
First runs stay clean. The behavior is demonstrated against both synthetic
fixtures and the real example (revise golden).
