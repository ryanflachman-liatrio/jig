# Task 05 Proofs — optional `[step.context]` authoring block

## Task Summary

This task activates the optional `[step.context]` table on agent steps (the
schema field was defined in Task 4.0 because the contradiction check needed it).
The block has two optional string fields — `purpose` (why the step exists) and
`notes` (local free-form guidance). The engine injects the step's own
`purpose`/`notes` into its own preamble, and propagates each **neighbor's**
declared `purpose` onto this step's Upstream/Downstream lines. A neighbor without
a purpose stays graph-derived — never a guessed description. The validator
rejects a `[step.context]` block on non-agent steps and (via the TOML decoder) a
non-string `purpose`/`notes`.

## What This Task Proves

- A step's own `[step.context].purpose`/`notes` render as `Purpose:`/`Notes:`
  lines on its preamble.
- A downstream consumer's preamble shows the producer's declared `purpose` on its
  Upstream line (author-supplied propagation, not a guess).
- A valid `[step.context]` parses and its values land on the step.
- `[step.context]` on a non-agent step is a load-time error.
- A non-string `purpose`/`notes` is a load-time error (TOML type mismatch).

## Design Note

`Render()` already emitted the own `Purpose`/`Notes` lines and the neighbor
purpose clause (upstream `— <purpose>`; downstream purpose *replaces* the derived
clause) — that logic was built in Unit 1. So Task 5.0 added **no** render code
(5.3): only the engine-side population in `buildStepContext` and the validator's
non-agent rejection. `checkContext` was moved from `checkAgent` to the shared
per-step check list so it runs for every step type and can reject the block on
non-agent steps while still enforcing the Unit 4 contradiction on agent steps.

## Evidence Summary

- `TestWorkflowContextPurposePropagation` passes — own injection + neighbor
  propagation.
- `TestDecodeStepContext` passes — valid parse lands `purpose`/`notes`.
- Two `TestDecodeInvalid` rows pass — non-agent block; non-string field.
- `03-proofs/5.0-context-block-preamble.txt` — a real assembled preamble pair
  showing both own-injection and propagation.
- Full `go test ./... -race`, `go vet`, `gofmt` clean; example still validates.

## Artifact: Own-injection + neighbor-propagation (engine test)

**What it proves:** A step renders its own `Purpose`/`Notes`, and its declared
purpose propagates onto a downstream consumer's Upstream line.

**Why it matters:** These are the two directions of the authoring block; the
propagation direction is the subtle one (a neighbor's block affects *this* step's
framing).

**Command:**

~~~bash
go test ./internal/engine -run TestWorkflowContextPurposePropagation -v
~~~

**Result summary:** PASS — `plan` shows its own `Purpose:`/`Notes:`; `implement`
shows ``- `plan` (succeeded) — produce the implementation plan``.

~~~
=== RUN   TestWorkflowContextPurposePropagation
--- PASS: TestWorkflowContextPurposePropagation (0.00s)
PASS
ok  	jig/internal/engine
~~~

## Artifact: Valid parse + load-time rejections (validator)

**What it proves:** A valid `[step.context]` parses; a non-agent block and a
non-string field are rejected at load time.

**Why it matters:** Schema additions are exhaustive and load-time — both the
valid and every invalid path are covered.

**Command:**

~~~bash
go test ./internal/workflow -run 'TestDecodeStepContext|TestDecodeInvalid' -v
~~~

**Result summary:** PASS, including
`TestDecodeInvalid/context_block_on_command_step` and
`TestDecodeInvalid/context_purpose_non-string`.

~~~
--- PASS: TestDecodeStepContext (0.00s)
--- PASS: TestDecodeInvalid/context_block_on_command_step (0.00s)
--- PASS: TestDecodeInvalid/context_purpose_non-string (0.00s)
~~~

## Artifact: Rendered context-block preamble

**What it proves:** With a real `[step.context]` block, the assembled preamble
carries the author's purpose/notes and propagates the neighbor purpose.

**Why it matters:** This is the human-readable payoff — the framing an agent
actually receives.

**Artifact path:** `03-proofs/5.0-context-block-preamble.txt`

**Result summary:** `plan` shows `Purpose:`/`Notes:` lines; `implement` shows
plan's purpose on its Upstream bullet. Both end at the `---` delimiter.

~~~
=== `plan` preamble (own [step.context] purpose + notes) ===

## Workflow context

You are step `plan` in workflow `fixture`.
Purpose: produce the implementation plan
Notes: focus on the public API surface

Downstream (what your output feeds):
- `implement` (agent) — consumes your `tasks`

---

=== `implement` preamble (plan's purpose propagated onto the Upstream line) ===

## Workflow context

You are step `implement` in workflow `fixture`.

Upstream (already complete):
- `plan` (succeeded) — produce the implementation plan
These reach you as the inputs listed below; this section is orientation only.

---
~~~

## Artifact: Full regression

**What it proves:** The change is green across the module under `-race`, and the
kitchen-sink example still validates.

**Command:**

~~~bash
gofmt -l internal/ && go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml
~~~

**Result summary:** gofmt/vet clean, all packages PASS under `-race`,
`ok: "feature" v1 — 15 step(s)`.

## Reviewer Conclusion

`[step.context]` is a fully load-validated, optional authoring block that
supplements — never replaces — the graph-derived framing. Own `purpose`/`notes`
render on the step, and a neighbor's `purpose` propagates onto this step's
neighbor lines, with an undeclared purpose left graph-derived.
