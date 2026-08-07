# jig integrates step changes on a per-run branch, one squash commit per step

Status: proposed.

Historically each mutating step ran in its own worktree branched off the **repo-root
HEAD**, and a step's changes reached the rest of the workflow only two ways: as
structured `@ref` outputs, or via an explicit `merge` command step an author wired by
hand (e.g. `examples/bugfix.toml`). Steps therefore could not build on each other's
*code* — only on each other's declared output — and the integrated result was assembled
by hand.

We decided jig owns integration on a **per-run branch** (the "run branch"). Each step's
worktree branches off the **run branch's current HEAD**, so a step sees the code its
upstream steps produced; when a step completes, jig **squash-merges its worktree branch
back into the run branch as exactly one commit, tagged with the step id**. At run end a
single **human-gated merge** lands the run branch onto the user's working branch.
Because squash-per-step gives the run branch a commit history keyed by step, it is the
foundation that makes commit-addressable **reset** possible ([ADR 0008](0008-manual-reset-rewind-and-replay.md)).

## Why

The driving use case — a `plan → implement → …` workflow where `implement` extends code
an earlier step wrote — is impossible under per-step isolation, where every step starts
from the same untouched repo HEAD and exchanges only `@ref` output. Threading code
through a run branch is the smallest model that lets steps compose on code while keeping
each step's turn isolated in its own worktree.

## Consequences

- **Integration conflicts become possible** between parallel steps that touch the same
  files (previously impossible because nothing auto-merged). They surface through a new
  **integration-conflict gate** — a human-resolved Gate entry, honoring the
  non-blocking-gate model ([ADR 0002](0002-gates-are-nonblocking-focus-regions.md)) — never an
  auto-resolver. Well-authored parallel steps are file-disjoint and never trigger it.
- **Explicit `merge` command steps are retired** in favor of the one final gated merge;
  examples that hand-merge step branches are simplified.
- The run branch and worktrees persist for the run's life; teardown happens at the final
  merge / run cancel, not at first settle.

## Rejected alternatives

- **Keep per-step isolation + explicit merge steps.** Cannot deliver "downstream builds
  on upstream code," which is the actual requirement.
- **Each step branches off repo-root HEAD, merged pairwise along `depends_on` edges.** A
  step with two upstreams needs an ad-hoc octopus merge, and there is no linear run
  history to reset against.
