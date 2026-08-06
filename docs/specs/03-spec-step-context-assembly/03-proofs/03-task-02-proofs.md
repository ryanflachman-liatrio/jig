# Task 02 Proofs — Engine assembly of topology + runner prepend

## Task Summary

This task wires Unit 1's pure renderer into the running engine. The scheduler now
assembles a `StepContext` from the static DAG at dispatch — upstream deps with
their real statuses, downstream consumers with their kind, consumed fields, and
guard clauses — renders it, and hands the string to the runner on
`StepRequest.WorkflowContext`. `buildAgentPrompt` prepends that preamble ahead of
the skill body with the `---` delimiter. This delivers the always-on "before +
after" framing end-to-end for agent steps, while command/review steps and the
empty-context path stay byte-identical to today's prompt.

## What This Task Proves

- Dispatching `plan` yields a preamble that names its **three upstream deps with
  dispatch-time statuses** (including a `failed` dep kept honest via
  `on_failure = "continue"`) and its **two downstream consumers** with kind labels
  (`plan_review` → human review, `implement` → agent), consumed fields, and the
  `(conditional on …)` clause on the guarded edge.
- Assembly is **framing-only**: no non-dependency sibling ids and no upstream
  artifact bodies leak into the preamble.
- **Command and review steps** dispatch with an empty `WorkflowContext`.
- The runner **prepends** a non-empty preamble at the front with the `---`
  delimiter, and an **empty context leaves the prompt byte-identical** to the
  pre-feature four-part form (the persistence-off / opt-out regression lock).
- The reconciled `2.0-plan-preamble.txt` proof is exactly what the engine renders
  for the real `examples/feature.toml`.

## Evidence Summary

- `go test ./internal/engine -run TestBuildRequest` — four tests pass: topology
  assembly, no-sibling-leak, non-agent-empty, and the example golden.
- `go test ./internal/runner -run TestBuildAgentPrompt` — prepend + empty-context
  regression lock pass.
- `go test ./... -race` green; `go vet` and `gofmt` clean; `jig validate
  examples/feature.toml` exits 0 (15 steps).

## Reconciliation note (pre-authored golden corrected)

The pre-authored `2.0-plan-preamble.txt` rendered the review consumer as
"a person reviews your **output**". The spec's Unit 2 Proof Artifacts
("`plan_review` → human review of **`summary`**") and task 2.3 ("from its
`review = "@st.field"` ref (so `plan_review` yields `summary`)") both require the
review field to be extracted. Task 2.3 is the authoritative, post-audit
instruction, and the `summary` form is also more correct — the reviewer literally
reviews `@plan.summary`. The engine now extracts it; the committed golden was
regenerated. The diff is a single line:

```diff
 Downstream (what your output feeds):
-- `plan_review` (human review) — a person reviews your output
+- `plan_review` (human review) — a person reviews your `summary`
 - `implement` (agent) — consumes your `tasks`, `approach` (conditional on `plan_review == 'approve'`)
```

## Artifact: Engine assembly tests

**What it proves:** The scheduler builds the correct upstream/downstream topology
with honest statuses, stays framing-only, and gates non-agent steps to empty.

**Why it matters:** This is the core dispatch-time behavior — a wrong neighbor
set, a leaked sibling, or a non-agent preamble would all be visible to the agent.

**Command:**

```bash
go test ./internal/engine -run TestBuildRequest -v
```

**Result summary:** All four pass. `TestBuildRequestWorkflowContext` asserts the
upstream lines (with a retained `failed` status), the two downstream lines, and
the guard clause; `TestBuildRequestNoSiblingLeak` asserts no `sibling`/`lint` id
and no injected artifact body appear; `TestBuildRequestNonAgentEmpty` asserts a
command and a review step get `WorkflowContext == ""`; `TestBuildRequestPlanPreambleGolden`
locks the real-example render to the committed proof.

```
=== RUN   TestBuildRequestWorkflowContext
--- PASS: TestBuildRequestWorkflowContext (0.00s)
=== RUN   TestBuildRequestNoSiblingLeak
--- PASS: TestBuildRequestNoSiblingLeak (0.00s)
=== RUN   TestBuildRequestNonAgentEmpty
--- PASS: TestBuildRequestNonAgentEmpty (0.00s)
=== RUN   TestBuildRequestPlanPreambleGolden
--- PASS: TestBuildRequestPlanPreambleGolden (0.00s)
PASS
ok  	jig/internal/engine
```

## Artifact: Runner prepend + regression lock

**What it proves:** A non-empty preamble is prepended at the front with the `---`
delimiter ahead of the body; an empty context yields a byte-identical prompt.

**Why it matters:** The prepend must not perturb the existing four-part prompt —
the empty path is what most engine/runner tests and the `inject_context = false`
opt-out depend on.

**Command:**

```bash
go test ./internal/runner -run TestBuildAgentPrompt -v
```

**Result summary:** Both pass. `TestBuildAgentPromptEmptyContext` asserts the
exact four-part string and the absence of any preamble marker;
`TestBuildAgentPromptPrependsContext` asserts the front placement and that
everything after the preamble equals the no-context prompt byte-for-byte.

```
=== RUN   TestBuildAgentPromptEmptyContext
--- PASS: TestBuildAgentPromptEmptyContext (0.00s)
=== RUN   TestBuildAgentPromptPrependsContext
--- PASS: TestBuildAgentPromptPrependsContext (0.00s)
PASS
ok  	jig/internal/runner
```

## Artifact: Assembled `plan` preamble for the real example

**What it proves:** The end-to-end assembly, applied to `examples/feature.toml`,
produces the expected first-run preamble (no iteration clause, no `State`,
graph-derived neighbor lines).

**Why it matters:** This is the human-readable demo of the whole unit against the
kitchen-sink example, and it is asserted by the golden test above.

**Artifact path:** `03-proofs/2.0-plan-preamble.txt`

**Result summary:** The `plan` step is oriented to its real upstream (three
research/scan agents) and its real downstream (the review gate and the guarded
implement step).

```
## Workflow context

You are step `plan` in workflow `feature`.

Upstream (already complete):
- `research_backend` (succeeded)
- `research_frontend` (succeeded)
- `security_scan` (succeeded)
These reach you as the inputs listed below; this section is orientation only.

Downstream (what your output feeds):
- `plan_review` (human review) — a person reviews your `summary`
- `implement` (agent) — consumes your `tasks`, `approach` (conditional on `plan_review == 'approve'`)

---
```

## Artifact: Full regression + example validation

**What it proves:** The engine/runner changes break nothing under the race
detector, and the schema-driven example still validates.

**Command:**

```bash
go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml
```

**Result summary:** Vet clean; every package passes under `-race`; validate exits
0 with `ok: "feature" v1 — 15 step(s)`.

```
ok  	jig/internal/engine	1.914s
ok  	jig/internal/runner	1.808s
ok  	jig/internal/step	(cached)
...
ok: "feature" v1 — 15 step(s)
```

## Reviewer Conclusion

The engine assembles honest graph-derived framing for agent steps and prepends it
without disturbing the existing prompt: proven by topology/leak/non-agent tests,
a runner prepend test, a byte-identical empty-context regression lock, and a
golden render of the real example. The one behavioral refinement over the
pre-authored proof — extracting the review target field (`summary`) — follows the
spec and task instructions and is captured in the reconciled golden.
