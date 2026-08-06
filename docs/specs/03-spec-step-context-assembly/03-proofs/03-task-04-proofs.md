# Task 04 Proofs — `inject_context` opt-out toggle

## Task Summary

This task adds `inject_context`, the opt-out for the engine-assembled "Workflow
context" preamble. It is a `bool` on `[defaults]` (default `true`) and a per-step
`*bool` override on agent steps (nil = inherit, distinct from explicit `false`).
When the effective value is `false` the engine skips `StepContext` assembly and
leaves `req.WorkflowContext == ""`, so the dispatched prompt is byte-identical to
the pre-feature prompt. The validator rejects, at load time, `inject_context` on
`command`/`review` steps and the contradiction of a `[step.context]` block with
an explicit `inject_context = false` on the same step.

## What This Task Proves

- A per-step `inject_context = true` overrides `[defaults].inject_context =
  false` (precedence direction), and an unset step inherits the default.
- `jig validate` fails fast (exit 1) on `inject_context` on a command step.
- A `[step.context]` block plus an explicit `inject_context = false` on the same
  step is a load-time error.
- With the effective toggle off, the engine dispatches an empty
  `WorkflowContext` (the no-context baseline), while a sibling with the toggle on
  still gets its assembled preamble.

## Design Note (audit Open Question)

The audit's Open Question resolved the contradiction check to the **explicit**
per-step `inject_context = false`, "evaluated before defaulting collapses it," so
an *inherited* false with a context block is not flagged. To honor that, the raw
`*bool` is **not** overwritten by `applyDefaults` (which would erase the
explicit/inherited distinction). Instead the effective value is resolved once at
load time into an unexported `Step.injectContext` field (mirroring the existing
`agentPrompt` load-time field), read by `InjectContextEnabled()`. This is the one
deliberate deviation from task 4.2's literal "set `s.InjectContext =
wf.Defaults.InjectContext`" wording; it is required for the validator to satisfy
the resolved Open Question and for `[defaults].inject_context` to still affect
inherited steps correctly.

## Evidence Summary

- `TestDecodeInjectContext` passes — precedence + inheritance both correct.
- Two `TestDecodeInvalid` rows pass — non-agent rejection and the contradiction.
- `TestBuildRequestInjectContextOff` passes — toggle-off yields an empty preamble.
- `go run ./cmd/jig validate` on the invalid fixture exits 1 with a clear message.
- Full `go test ./... -race`, `go vet ./...`, `gofmt` all clean; the example
  workflow still validates.

## Artifact: Defaulting / precedence unit test

**What it proves:** A per-step `inject_context = true` beats
`[defaults].inject_context = false`, and an unset step inherits the default.

**Why it matters:** This is the core resolution rule of the toggle; getting the
precedence direction right is the point of parsing it as `*bool`.

**Command:**

~~~bash
go test ./internal/workflow -run TestDecodeInjectContext -v
~~~

**Result summary:** PASS — `override_on` (explicit true) resolves enabled;
`inherit_off` (unset) resolves disabled under `[defaults] = false`.

~~~
=== RUN   TestDecodeInjectContext
--- PASS: TestDecodeInjectContext (0.00s)
PASS
ok  	jig/internal/workflow
~~~

## Artifact: Load-time rejections (validator)

**What it proves:** `inject_context` on a command step and a `[step.context]` +
explicit `inject_context = false` combination are both rejected at load time.

**Why it matters:** Schema rules are exhaustive and load-time — a bad workflow
must fail at `jig validate`, before any agent burns a token.

**Command:**

~~~bash
go test ./internal/workflow -run TestDecodeInvalid -v
~~~

**Result summary:** PASS, including
`TestDecodeInvalid/inject_context_on_command_step` and
`TestDecodeInvalid/context_block_with_explicit_inject_context_false`.

~~~
--- PASS: TestDecodeInvalid/inject_context_on_command_step (0.00s)
--- PASS: TestDecodeInvalid/context_block_with_explicit_inject_context_false (0.00s)
--- PASS: TestDecodeInvalid (0.00s)
~~~

## Artifact: Engine toggle-off is a no-context no-op

**What it proves:** With the effective toggle off, the engine leaves
`req.WorkflowContext == ""`; a sibling with the toggle on still assembles a
non-empty preamble.

**Why it matters:** The opt-out must produce a byte-identical no-context prompt
(persistence-off / opt-out stays a no-op), not a subtly different one.

**Command:**

~~~bash
go test ./internal/engine -run TestBuildRequestInjectContextOff -v
~~~

**Result summary:** PASS — `b` (inject_context = false) dispatches empty; `a`
(default on) dispatches non-empty.

~~~
=== RUN   TestBuildRequestInjectContextOff
--- PASS: TestBuildRequestInjectContextOff (0.00s)
PASS
ok  	jig/internal/engine
~~~

## Artifact: CLI validate error

**What it proves:** The user-facing `jig validate` rejects `inject_context` on a
command step with a clear message and a non-zero exit.

**Why it matters:** This is the acceptance evidence a human sees — fail at parse
time, not run time.

**Artifact path:** `03-proofs/4.0-validate-error.txt`

**Result summary:** Exit status 1 with
`invalid workflow: step "build": inject_context is only valid on agent steps`.

~~~
$ go run ./cmd/jig validate bad-inject-context.toml
invalid workflow: step "build": inject_context is only valid on agent steps
exit status 1
~~~

## Artifact: Full regression

**What it proves:** The change is green across the whole module under the race
detector, and the kitchen-sink example still validates.

**Why it matters:** Confirms the persistence-off engine/runner paths and every
other package are unaffected.

**Command:**

~~~bash
gofmt -l internal/ && go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml
~~~

**Result summary:** gofmt clean, vet clean, all packages PASS under `-race`, and
`ok: "feature" v1 — 15 step(s)`.

## Reviewer Conclusion

`inject_context` is a fully load-validated, `[defaults]`-inheritable opt-out that
turns the engine-assembled preamble into a byte-identical no-op when off. The
one design decision (explicit-only contradiction check via a load-time effective
field) is the audit-resolved behavior and is covered by tests.
