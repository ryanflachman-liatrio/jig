# jig vocabulary

Use these terms consistently in code, workflow skills, and user-facing text.
This is a glossary; [Architecture](docs/ARCHITECTURE.md) describes ownership,
and [Workflow schema](docs/workflow-schema.md) defines the authoring contract.

## Workflow and graph

**Workflow:** a validated TOML definition of explicit dependencies, inputs,
outputs, conditions, checks, human reviews, and bounded routes. A workflow is a
definition; a **run** is one execution with its own identity and state.

**Step:** a declared unit of workflow behavior. Author-facing kinds are
`agent`, `command`, `check`, `review`, and `subworkflow`. A step definition is
not the same as its mutable runtime `step.State` or its execution result.

**Dependency:** a forward `depends_on` relationship. “Upstream” means producers
and prerequisites; “downstream” means consumers and dependents. Specify which
direction is meant by a “closure”; reset uses the target plus downstream
transitive dependents, not the target's prerequisites.

**Condition:** a parsed, typed expression used by a guard such as `when`.
Conditions inspect declared facts; they do not execute code or query an agent.

**Route:** an ordered, bounded back-edge that selects another graph iteration
and carries feedback. Distinct from a dependency edge or automatic retry.

**Module / subworkflow:** a reusable TOML interface with inputs and exports,
invoked by a subworkflow step and expanded into namespaced steps at load time.
The engine runs one expanded graph. A module is not an independent child run.

**Producer / artifact:** a producer declares output that consumers reference.
An artifact is captured output/evidence; a path in a mutable worktree is not
necessarily an immutable artifact. Structured conclusions are schema-checked
values, not decisions inferred from prose.

**Check:** deterministic execution with applicability and a typed findings
protocol. Check outcomes distinguish pass, fail, error, and engine-determined
non-applicability; these are not interchangeable with ordinary step status.

**Agent profile:** reusable agent settings folded into unresolved step fields.
**Quality profile:** a repository-specific deterministic check contract used by
SDD workflows. **Notification profile:** reusable run notification policy.
Qualify “profile” when the meaning is ambiguous.

## Runtime and recovery

**Scheduler:** the single owner of a run's mutable execution state. Workers
perform execution and report messages; callers use the `Run` command/snapshot
seam rather than changing state directly.

**Park:** unfinished state waiting for input, review, recovery, integration, or
resume. A parked step is not automatically failed or terminal.

**Quiescent:** no worker is in flight while the run remains unfinished.
Quiescence is a reset prerequisite; it does not mean the run succeeded.

**Attempt:** execution count associated with automatic failure retry.
**Iteration:** execution pass associated with a route loop.
**Generation:** provenance counter advanced by manual reset. Keep all three
axes distinct in transcripts, snapshots, and tool matching.

**Retry:** another attempt under a declared failure policy. New retry contracts
use `[step.retry]`; `max_attempts` includes the initial attempt, whereas legacy
`max_retries` counts additional retries. Idempotency is a promise about repeated
external effects, not a property conferred by the retry mechanism.

**Stop / resume a step:** interrupt one worker while preserving the run; resume
opens a session using its durable backend conversation ID and new input.
Resume continues a conversation, not an exact interrupted machine instruction.
**Cancel a run:** end the whole run through its cancellation lifecycle.

**Crash reopen:** acquire ownership and rebuild a live scheduler from the
locked workflow, journal, and required artifacts. Interrupted running work
moves into recovery; existing parks restore in place. Display replay may show
an orphaned history that is not safe to reopen.

**Journal:** ordered orchestration history in `journal.jsonl`, persisted before
event publication. **Live signal:** lightweight, potentially lossy observation
such as transcript progress or a text delta. Live signals are not durable truth.

**Snapshot:** a captured representation for a particular purpose. Qualify as
workflow snapshot, execution view, input snapshot, or `RunSnapshot`; an
in-memory status snapshot is not itself a durable execution checkpoint.

**Lease:** exclusive process ownership of a persisted run's scheduler. A UI
opening the run for inspection does not acquire authority to mutate it.

## Repository integration

**Run branch:** the per-run Git branch accumulating integrated step changes,
starting from the user's working-branch HEAD. Final landing back onto the
user's branch is human-gated. “Integration branch” is an acceptable synonym.

**Step worktree:** the private mutation workspace based on integrated run
state. **Execution view:** a separate repository snapshot selected for a
read-only dispatch. Reader views do not participate in mutation integration.
Neither implies an OS sandbox or a security boundary.

**Integration:** squash a step's changed contribution into the run branch,
recording a step-addressable commit. An unchanged step need not produce a
commit. Reserve “final merge” for run-branch landing onto the user's branch.

**Integration conflict:** a Git conflict during integration or survivor replay.
The run parks for operator action; an optional agent resolver is explicitly
operator-invoked, not an automatic conflict policy.

**Reset:** rewind an unfinished, quiescent run to invalidate a target and its
downstream dependents, replay independent survivor commits, and return the
invalidated work to pending in a new generation. Distinct from retry or resume.

## Dynamic fan-out

**Family:** the single static node with `[step.foreach]`. It supplies a template
and acts as a fan-in barrier; it does not itself dispatch a normal worker.
Workflow references address the family, never a runtime child.

**Child / runtime instance:** one scheduler-owned clone bound to one source
list item. Its deterministic identity includes the family and expansion
coordinates. It is absent from author-declared `wf.Steps`, but visible in
operator/provenance surfaces and subject to the ordinary execution lifecycle.

**Expansion:** resolving a bounded list into ordered children, canonicalizing
items, and recording durable identity for one generation/iteration. Reopen
restores this expansion; reset/route invalidation can require a new one.

**Barrier / fan-in:** the family's join point. Dependents wait for the current
children to settle according to engine policy. An empty expansion succeeds.

**Aggregate:** the family's author-addressable result, including counts and
`results[]` ordered by source index rather than completion time.

**Event fan-out:** publishing the same event to multiple subscribers. This is
separate from dynamic fan-out, which multiplies runtime step instances.

## Harness and backend

**Backend:** the vendor/CLI being driven: Claude, Cursor, or Codex today.
**Transport:** the protocol used to reach it: SDK or ACP as supported.
**Harness:** jig's Go implementation of the transport/lifecycle seam:
`ClaudeHarness`, `AcpHarness`, `CursorHarness`, or `CodexHarness`.
Supported pairs and defaulting are in [AGENTS.md](AGENTS.md).

**Session:** the live value returned by `Harness.Open` for one executor call.
Mid-turn answers use that same session. A later resume opens a new session
with the previous backend conversation ID; it does not reuse a closed object.

**Capability:** a feature advertised by the harness before opening a session.
Required support is checked explicitly and fails closed when absent. Do not
infer capabilities from a concrete type assertion or vendor name.

**PermissionFn:** jig's synchronous pre-tool callback at the harness boundary.
The runner binds it to the security guard; the harness does not own sentinel
policy. A worktree and an allowed-tools list are different controls.

## TUI presentation

**Screen:** one of the root surfaces, Home or Monitor. Home contains workflow
and run lists; Detail/chart is an overlay. Standalone chat is a separate model,
not the default CLI entry point. Avoid calling every child model a screen.

**Panel:** shared border/title presentation around a caller-sized body. It
owns neither viewport state nor content wrapping. **Footer:** the unboxed
contextual key-hint line below the main content. **Status line:** run state and
progress; distinct from key hints.

**Focus:** the region receiving keyboard input. **Selection:** the current
list row, transcript item, or document. Focus and selection can differ.

**Gate:** Monitor's human-input surface. Its bar reports pending entries;
focusing pending work opens the relevant controls or review workspace without
preventing navigation elsewhere. An empty bar is skipped by the focus cycle.

**Input queue / entry:** the ordered pending interactions and one interaction
within them. The queue preserves simultaneous arrivals. Entries include review,
user input, questions, recovery, stopped-step, and integration interactions.
A Gate is the surface, not the engine's deterministic validation check.

**Review round / document / anchor:** an immutable review set, one source
within it, and a location tied to document identity and logical source lines.
Preview rows, folded hunks, and old/new diff coordinates are presentation, not
anchor identity. A draft holds pending comments/verdicts; submission uses the
review contract atomically.

**Transcript item:** Monitor's normalized unit for rendering, navigation,
search, expansion, and restored position, derived from durable transcript
blocks. Do not confuse a displayed item with a raw JSONL entry or screen row.

**Tool exchange:** a use/result pair within the same generation, iteration,
and attempt. **Use-only** or **result-only tool item:** one counterpart is
absent from the loaded page or durable record; its outcome is not invented.
A use-only item is pending only while its step is running.

**User guidance:** human-authored text, including initial and resumed input.
A user-role tool result is not user guidance. **Unsupported transcript item:**
unknown content preserved in place for inspection rather than silently dropped.
