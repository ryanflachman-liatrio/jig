# The engine assembles a deterministic step-context preamble

Status: accepted.

Every agent step now runs with a short **"Workflow context" preamble** that jig
assembles from the static DAG and the scheduler's dispatch-time state, prepended
to the agent's single user turn ahead of the skill/agent-file body. It tells the
agent where it sits: its upstream dependencies (with dispatch-time statuses), the
downstream steps that consume its output (with the consumed field names and any
`when` guard), and — on a loop re-run — that it is iterating and why.

This gives jig a deterministic **input/position** contract that mirrors the
existing deterministic **output** contract (`internal/workflow/base_schema.go`).
The author writes the skill body about *how to do the job*; jig owns *where the
job sits in the graph*. Previously that orientation was hand-narrated inside each
skill ("you receive research from two agents", "reviewer feedback present only
when…"), which drifts from the actual graph and duplicates what the workflow file
already declares. The engine knows the graph precisely, so it is the honest
author of that framing.

The preamble is **framing only.** It carries ids, statuses, declared purposes,
and run state — never an upstream artifact body or a live sibling status. Content
still reaches a step through its declared `@ref` inputs. Ordering is fixed
(upstream in `depends_on` order, downstream in declaration order) and nothing
iterates a map into the output, so the same graph position always renders the
same bytes — the property the golden tests lock.

## Where it lives (honoring ADR 0003)

Assembly is an **engine** concern and the toggles are a **schema** concern, per
[ADR-0003](0003-extensibility-lives-in-engine-and-schema.md): the scheduler
projects `workflow` + `step` data into a pure, dependency-free `step.StepContext`
whose `Render()` produces the exact bytes; the runner stays thin, only *prepending*
a non-empty pre-rendered string. `internal/step` keeps importing nothing, so the
format is testable in isolation. Two schema fields expose the seam:
`inject_context` (a `bool` on `[defaults]` and a per-step `*bool` override, default
on) and an optional `[step.context]` table (`purpose` / `notes`) that *supplements,
never replaces* the graph-derived framing.

## Rejected alternatives

- **Author-written template.** Let each skill embed a template that jig fills in.
  Rejected: it puts the deterministic part back in non-deterministic, drift-prone
  prose and defeats the point — the engine already knows the graph exactly.
- **Content injection.** Inline upstream artifact bodies (or a live sibling
  status board) into the preamble. Rejected: it duplicates what `@ref` inputs
  already deliver, bloats the turn, and breaks determinism/caching; the preamble
  is orientation, not data.
- **Live-sibling status.** Report the runtime status of *non-dependency* siblings.
  Rejected: it is non-deterministic at assembly time and leaks unrelated steps
  into a step's framing; only declared neighbors (with their dispatch-time status,
  which is stable for a dependency) belong.

## Consequence

Skill files shed their hand-authored position/loop narration and keep only job
instructions. New graph-derived framing is added by extending the pure
`StepContext` renderer and the engine's assembly, never by re-narrating it in each
skill. The opt-out (`inject_context = false`) yields a byte-identical no-context
prompt, so the feature is a strict superset of the prior behavior.
