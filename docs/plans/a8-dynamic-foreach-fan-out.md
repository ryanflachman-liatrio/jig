# Implementation Plan: Dynamic foreach fan-out (A8)

**Status:** Planned — goal A8 (P0)
**Risk:** **high** — this adds a runtime-sized execution family across workflow
schema/type checking, scheduler ownership, result aggregation, journal replay,
crash reopen, reset/worktree integration, the Monitor, ops inspection, and
headless JSONL.
**Depends on:** Structured producer outputs and list fields
(`internal/workflow.Schema`); journal-before-fan-out; immutable
`workflow.json`; Specs 20/21 unfinished-run reopen; Spec 08 reset; existing
`max_parallel` / resource-class limits.
**Complements:** A7 Codex parallel reliability; A11 richer conditions; A18
telemetry; T18 parallel-tool visualization.
**Breaks:** The deferred-schema statement becomes obsolete. No compatibility
shim is needed because no released `foreach` syntax exists.

### Summary

Add a bounded `[step.foreach]` block to agent and command steps, expand its
typed list into deterministic runtime instances, and expose the family as one
static DAG barrier with ordered aggregate output. The top risk is making the
dynamic children as durable and controllable as ordinary steps without
weakening static validation or double-counting lifecycle state.

### Approach

Add `workflow.ForEach` to the existing `workflow.Step` instead of introducing a
sixth step type: the declared step remains the author-facing graph node and its
agent/command fields remain the executable template. Validate the list source,
item binding, and hard bounds in `internal/workflow` first; then add a durable
`FanOutExpanded` event plus a per-generation expansion manifest before teaching
`engine.scheduler` to create child `step.State` values and dispatch cloned
templates through the existing worker, retry, validation, security, worktree,
and recovery paths. Complete the barrier by writing an ordered aggregate
`output.json`, then update resume/reset, Monitor/Runs, ops, headless output,
chart annotations, examples, and schema documentation in that order. The key
design decision is that runtime children never rewrite `wf.Steps`: the static
DAG remains valid and visualizable, while a scheduler-owned runtime registry
holds the exact instances produced for one run generation.

### Problem and goals

Today jig can run a statically declared fan-out, but the number of steps must be
known while writing TOML. A discovery agent can return a typed list such as
packages, files, services, or tickets, but an author cannot run the same bounded
agent/command once per element and then join those results.

The implementation must:

1. Accept a typed list field from a direct dependency and bind one element to
   each runtime child.
2. Bound both child count and concurrency before dispatching any child.
3. Preserve input order in instance identity, scheduling order, UI order, and
   aggregate output regardless of completion order.
4. Reuse ordinary execution semantics per child: timeouts, automatic retry,
   resource limits, security, transcripts, recovery gates, validation, and
   worktree integration.
5. Make the declared family a fan-in barrier: dependents wait for all children
   to settle under their normal failure policy.
6. Persist enough expansion data to resume exactly the same children after a
   process death without rereading or re-running the producer.
7. Make reset and bounded routes that target the family invalidate the current
   expansion and produce a fresh generation.
8. Keep persistence-off execution working with the same semantics in memory and
   no attempted file writes.

### Non-goals for the first delivery

- A general expression language over collections (`filter`, `flatMap`, joins,
  Cartesian products, or per-item `when`). A step-level `when` is evaluated once
  before expansion.
- Dynamic review/check/subworkflow templates. `foreach` is initially valid only
  on `agent` and `command`; static review steps can inspect the aggregate.
- A route declared by a foreach template. A static route may target the family
  as a whole, but an individual child cannot rewind the graph.
- Addressing a child from workflow TOML. Author references resolve to the family
  aggregate; runtime instance IDs are operator/provenance identities only.
- Projecting through a list in a dotted reference such as
  `@analyze.results.output.summary`. Consumers receive `@analyze.results` as
  ordered JSON and may feed that list into another foreach family.
- Distributed execution, unbounded queues, or bypassing existing
  `max_parallel`, read/mutate, resource-class, cost, network, and security caps.

### Authoring contract

```toml
[[step]]
id         = "discover"
type       = "agent"
skill      = "skills/discover"

  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id         = "analyze"
type       = "agent"
depends_on = ["discover"]
skill      = "skills/analyze"

  [step.foreach]
  items        = "@discover.targets"
  as           = "target"
  max_items    = 32
  max_parallel = 4

  [step.schema]
  finding = "text"
  severity = { enum = ["low", "medium", "high"] }

[[step]]
id         = "synthesize"
type       = "agent"
depends_on = ["analyze"]
skill      = "skills/synthesize"
inputs     = ["@analyze.results"]
when       = "analyze.all_succeeded"
```

`[step.foreach]` fields:

| Field | Type | Contract |
|---|---|---|
| `items` | string | Required exact `@step.field` reference. The producer must be a direct `depends_on` entry and the resolved field must be `FieldList`. Bare step refs, files, artifacts, and runtime child IDs are invalid. |
| `as` | string | Required identifier used in the agent prompt and command environment. It must not collide with another named input or reserved fan-out metadata. |
| `max_items` | integer | Required and `>= 1`. If the runtime list is longer, expansion fails closed before a child is created; jig never truncates silently. |
| `max_parallel` | integer | Optional and `>= 1` when set. It caps in-flight children in this family in addition to all existing global/partition/resource limits; `0` inherits only those existing limits. |

Additional validation rules:

- A foreach step may be `agent` or `command`, including a mutating worktree
  template. All other types fail `jig validate`.
- The template may declare normal `inputs`, output schema, retry, timeout,
  validation, security, `block_on`, and agent question behavior; those apply to
  each child independently. `from="user"` inputs are rejected initially because
  prompting N times before dispatch is easy to trigger accidentally; agents may
  still ask runtime questions through the existing input queue.
- A foreach template cannot declare the legacy fixed `output` path: parallel
  children would race on one repository path. Agent prose/structured output and
  command transcripts still use each child's canonical run-owned directory;
  per-item author-defined output path templating is follow-on scope.
- `[[step.route]]` on the template is rejected. Routes elsewhere may use the
  family as a `goto` target or dependency and operate on the whole family.
- Conditions may reference aggregate fields such as
  `analyze.all_succeeded`; comparing the bare family scalar verdict is rejected
  because N child verdicts have no single scalar meaning.
- The validator reserves the runtime ID marker `.__fanout__.` so an author step
  cannot collide with generated IDs.
- Modules copy and namespace `foreach.items` exactly as they already rewrite
  `inputs`, reviews, conditions, and route feedback. A module may source a list
  produced inside the module; adding collection-valued public module inputs is
  separate scope.

### Runtime identity and item delivery

The declared ID (`analyze`) is the **family ID** and stays in the static DAG.
Each expansion receives the family's current `Generation` and `Iteration`;
child IDs are derived only from those coordinates plus the family ID and
zero-based source index:

```text
analyze.__fanout__.g000.r000.i0000
analyze.__fanout__.g000.r000.i0001
analyze.__fanout__.g000.r001.i0000   # after a bounded route rewind
analyze.__fanout__.g001.r000.i0000   # after a manual reset
```

The reserved marker prevents collision with author IDs. UI labels may use the
shorter `analyze[1/2]`, but events, directories, branches, logs, and control APIs
use the full stable ID. Duplicate item values remain distinct because identity
is positional.

Decode the producer field as `[]json.RawMessage`, canonicalize each element,
and retain list order. The canonical bytes and SHA-256 digest are stored in a
runtime `FanOutItem` attached to `engine.StepRequest`; the cloned
`workflow.Step.ID` is the child ID while `FanOutItem.TemplateID` retains the
family identity. The runtime clone clears `ForEach` to prevent recursive
expansion and clears `When` because the family guard has already been evaluated
once; it preserves the template's dependencies, ordinary inputs, executor
configuration, schema, validation, retry, security, and isolation fields.

- Agent runner: add a clearly labeled JSON block naming `as`, display position,
  total, and instance ID before ordinary inputs. The JSON form preserves scalar,
  object, and nested-list types.
- Command runner: expose compact JSON as `JIG_INPUT_<AS>`, zero-based index as
  `JIG_FANOUT_INDEX`, total as `JIG_FANOUT_TOTAL`, and the stable ID as
  `JIG_FANOUT_INSTANCE_ID`. Use the same environment-name normalization as
  declared artifacts/secrets.
- The item is also included in `inputs-generation-*.json` provenance with its
  digest. Do not duplicate raw item data into the journal or event bus.

### Scheduler and fan-in semantics

`scheduler` gains a runtime registry and a deterministic execution order:

```text
static producer succeeds
  -> family becomes dependency-ready
  -> resolve and bound list
  -> atomically write expansion manifest (when persistence is on)
  -> journal FanOutExpanded
  -> register pending children adjacent to the family
  -> family pending -> running
  -> dispatch children under all capacity checks
  -> each child follows the ordinary lifecycle
  -> all children terminal/accepted
  -> write ordered aggregate output
  -> family running -> succeeded
  -> static dependents become ready
```

The family itself consumes no `inFlight` slot. Every child consumes one global
slot and, when applicable, one family-local, read/mutate, and resource-class
slot. Ready selection uses source order, but completion and worktree integration
remain concurrent/serialized exactly as for ordinary parallel steps.

Child failures use the template's existing policy:

- retry/backoff applies to that child only;
- `on_failure = "continue"` or operator recovery-skip makes that child terminal
  and allows the family to continue waiting for siblings;
- an abort-policy failure parks that child on the ordinary recovery gate and
  keeps the family running;
- operator abort cancels the run as today;
- once every child is terminal or explicitly accepted, the barrier succeeds and
  exposes failure counts. The run still reports failed when any child is failed,
  matching ordinary `on_failure="continue"` semantics without failing the
  barrier a second time.

An empty list is valid: journal an expansion with zero instances, write the
empty aggregate, and succeed the family immediately. A missing/non-array value,
an item-count overflow, a corrupt persisted manifest, or aggregate-write failure
is a family-level failure routed through the existing recovery policy before any
dependent runs.

### Aggregate result contract

The family writes the latest aggregate to its normal
`steps/<family-id>/output.json`, sets `Result.OutputPath` to that file when
persistence is enabled, and keeps the same bytes in `Result.Structured` for the
persistence-off path. Results are ordered by source index, never completion
order:

```json
{
  "count": 2,
  "succeeded": 1,
  "failed": 1,
  "all_succeeded": false,
  "results": [
    {
      "index": 0,
      "instance_id": "analyze.__fanout__.g000.r000.i0000",
      "item": {"name": "api", "path": "services/api"},
      "status": "succeeded",
      "verdict": "",
      "output": {
        "summary": "No issues found",
        "confidence": "high",
        "status": "succeeded",
        "assumptions": [],
        "issues": [],
        "finding": "clean",
        "severity": "low"
      },
      "output_path": "/.../steps/analyze.__fanout__.g000.i0000/output.md",
      "error": ""
    }
  ]
}
```

`workflow.Step.ReferenceSchema()` (new) returns the normal effective schema for
an ordinary producer and a synthetic aggregate schema for a foreach family.
That schema exposes `count`, `succeeded`, `failed`, `all_succeeded`, and
`results`; both `results[].item` and `results[].output` are opaque for the first
delivery, while the result-record fields around them are typed. This permits
static checks for `@analyze.results` and `analyze.all_succeeded` without
pretending the existing dotted-reference resolver supports list projection or
caching a producer's element schema on an unrelated step. The template's
declared `[step.schema]` still configures each child harness and is not replaced
by the aggregate schema.

### Durability, replay, and crash reopen

Add a versioned, per-generation manifest at:

```text
steps/<family-id>/fanout/generation-000-iteration-000.json
```

It contains schema version, family/generation/iteration, source reference,
source-output digest, and each ordered
`{instance_id,index,item,item_sha256}`. Write to a temporary file and rename
before journaling `FanOutExpanded`; the datastore helper must no-op when
`runDir == ""`.

`FanOutExpanded` is a control event containing family ID, generation, iteration,
manifest digest, and lightweight instance descriptors (ID/index/item digest),
but not raw items. Its journal decoder is the authoritative creation record for
runtime states. On resume, `restoreUnfinishedCheckpoint` loads and verifies the
matching manifest, recreates child templates/states in event order, then folds
their ordinary `StepStatus` records. A child left `running`/`validating`,
stopped, question-blocked, in recovery, or in integration conflict reopens
through the same Specs 20/21 path as a static step. A logical family left
`running` is never misclassified as an interrupted worker; after restore it
waits for/re-aggregates its children.

Keep `RunStarted.Steps` as the static author graph so old readers still get a
valid baseline. New readers fold `FanOutExpanded` to discover children. If a
process dies after the manifest rename but before the event, the unreferenced
file is ignored and deterministic expansion may overwrite it. If the event is
durable, the manifest must already exist and match or Resume fails closed.

### Reset, routes, worktrees, and budgets

- `ResetClosure` remains a pure static-DAG operation for previews. Scheduler
  reset expands every family in that closure to include its current child IDs
  for journal transitions, artifact cleanup, worktree removal, and commit rewind.
- Resetting an individual runtime child is rejected with a typed error directing
  the operator to reset the family; recovery retry/resume remains the way to
  rerun one failed child before fan-in completes.
- Reset or a route rewind that includes the family invalidates the active
  expansion, removes current children from runtime scheduling, and leaves older
  coordinate directories/transcripts as immutable history. Manual reset bumps
  `Generation`; a bounded route keeps `Generation` and bumps `Iteration`,
  preserving jig's existing provenance vocabulary. The next readiness pass
  resolves the producer's current list and journals a new `FanOutExpanded`
  event.
- `rewindPlan` treats every child integration commit as belonging to its family,
  so reset removes all affected child commits and replays independent survivors
  in original run-branch order.
- Child worktree branches use the full generated ID after the existing branch
  sanitizer; integrations remain one `jig-step:` commit per child. Conflicts are
  child-specific gate entries.
- Cost, tokens, network calls, and security findings accrue on children only.
  The logical family reports a roll-up for display but contributes zero extra
  spend, preventing `RunSnapshot` and ops from double counting.

### Operator surfaces

**Monitor:** render the static family as one expandable Steps row with
`completed/total` and aggregate status. Expanding it reveals child rows in source
order; expanding a child reveals its ordinary files and selecting it loads its
transcript. Gate entries focus the exact child. Stop/resume/recovery work on the
child; reset is offered only on the family. Use existing shared tree/status
styles, adding any genuinely new visual token only in
`internal/tui/shared/styles.go`.

**Runs:** fold `FanOutExpanded` so completed/total includes discovered children
and never treats an unknown child `StepStatus` as a corrupt run. Preserve the
static count before expansion and increase it deterministically once the event
arrives.

**Ops:** `status` reports `parent_id`, index, and total on child records and
orders them below the family; `logs --all` discovers instance transcript paths
from expansion events. `logs --step` accepts full runtime IDs. Reset preview
stays family-level and applied reset uses the scheduler-expanded closure.

**Headless:** JSONL emits `fanout_expanded` plus ordinary child status/gate
events. Text progress prints one concise expansion line and normal child IDs;
the final JSON envelope remains unchanged because `run_dir` is the artifact
discovery root.

**Chart/detail:** the author graph stays one node per declared family. Mark a
foreach node with a compact `×*`/`foreach` annotation and show its configured
maximum; do not attempt a runtime-sized chart in the workflow detail screen.

### Ordered implementation tasks

Each task is intentionally confined to one package or file. Tests follow the
substantive change they prove.

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `ForEach`, child-vs-aggregate `ReferenceSchema` helpers, and JSON snapshot fields to `Step` | `internal/workflow/schema.go` | 30 min |
| 2 | Parse/default and validate `[step.foreach]`: exact list ref, direct dependency, bounds, supported step types, `as` collisions, route/from-user/fixed-output exclusions, bare-family condition rejection, reserved ID marker | `internal/workflow/validate.go` | 35 min |
| 3 | Add valid/invalid table tests for scalar/object/nested-list sources, `schema_file`, caps, unknown refs, non-list fields, invalid types, collisions, and aggregate field refs | `internal/workflow/workflow_test.go` | 35 min |
| 4 | Rewrite and clone foreach references during module expansion and use the correct reference schema for exports/consumers | `internal/workflow/module.go` | 25 min |
| 5 | Prove module namespacing, snapshot cloning, and rejection of unsupported module-input collection bindings | `internal/workflow/workflow_test.go` | 25 min |
| 6 | Add versioned fan-out manifest types plus atomic read/write/digest helpers; all helpers no-op cleanly for an empty run dir | `internal/datastore/fanout.go` | 30 min |
| 7 | Test manifest round-trip, order, digest mismatch, corrupt/version-mismatch rejection, atomic replacement, path layout, and persistence-off behavior | `internal/datastore/fanout_test.go` | 25 min |
| 8 | Add optional `ParentID`, `FanOutIndex`, and `FanOutTotal` provenance fields to runtime `State` without changing ordinary-state behavior | `internal/step/step.go` | 15 min |
| 9 | Define `FanOutExpanded`/instance descriptors and journal encode/decode support | `internal/engine` | 25 min |
| 10 | Add event round-trip, unknown-version, no-raw-item, and backward-compatible replay tests | `internal/engine/journal_test.go` | 20 min |
| 11 | Implement expansion, canonical item binding, family-local capacity, deterministic runtime order, aggregate schema/value construction, and barrier settlement behind a focused coordinator | `internal/engine/fanout.go` | 45 min |
| 12 | Prove empty/single/duplicate/max-sized lists, source-order identity, out-of-order completion, local/global/resource caps, overflow/type failure, chained fan-out, and persistence-off execution | `internal/engine/fanout_test.go` | 45 min |
| 13 | Integrate runtime children into `newScheduler`, `nextReady`, `stepByID`, dispatch requests, transitions, terminal detection, snapshots, failure policy, and spend accounting | `internal/engine/engine.go` | 40 min |
| 14 | Invoke family settlement after success, continue-failure, operator skip, stopped/recovered child, validation, and integration paths without double transitions | `internal/engine/commands.go` | 35 min |
| 15 | Add scheduler integration tests for retry/timeout/security/question/recovery behavior and ensure dependents wait for the complete family | `internal/engine/engine_test.go` | 40 min |
| 16 | Rebuild runtime families and children from expansion manifests before folding child statuses during replay/`Manager.Resume`, and distinguish logical running families from interrupted workers | `internal/engine` | 35 min |
| 17 | Test crash boundaries before/after expansion, mixed child park kinds, missing/corrupt manifests, completed-children aggregation, and exact item reuse on Resume | `internal/engine/resume_test.go` | 40 min |
| 18 | Expand reset/loop bodies to current child IDs, invalidate generations, include child commits in rewind planning, and reject child-only reset | `internal/engine/engine.go` | 35 min |
| 19 | Prove family and upstream reset, route-to-family re-expansion, changed list cardinality, child commit removal, survivor replay, and historical artifact retention | `internal/engine` | 40 min |
| 20 | Add immutable `FanOutItem` identity, position, canonical value, and digest metadata to `StepRequest` | `internal/engine/executor.go` | 20 min |
| 21 | Render the named item deterministically in agent prompts without disturbing ordinary prompt golden cases | `internal/runner/agent.go` | 20 min |
| 22 | Add fan-out prompt tests for strings, objects, JSON escaping, index/total, and secret redaction boundaries | `internal/runner/agent_test.go` | 20 min |
| 23 | Export normalized `JIG_INPUT_<AS>` plus fan-out metadata for command children | `internal/runner/command.go` | 20 min |
| 24 | Test command environment values, naming, JSON types, collision rejection assumptions, and ordinary-command compatibility | `internal/runner/command_test.go` | 20 min |
| 25 | Teach Monitor event/snapshot folding and model state about families, generation replacement, and child relationships | `internal/tui/monitor` | 35 min |
| 26 | Render collapsible family/child/file rows, aggregate progress, transcript selection, and child-vs-family lifecycle actions | `internal/tui/monitor` | 35 min |
| 27 | Add Monitor tests for live/replayed expansion, cursor stability, nested row expansion, gates on children, reset affordance, totals, narrow width, and no color-only state | `internal/tui/monitor/monitor_test.go` | 40 min |
| 28 | Fold expansion counts into live and historical run-list progress | `internal/tui/runs` | 20 min |
| 29 | Test run-list totals before/after expansion, replay, replacement generation, and completion | `internal/tui/runs/runs_test.go` | 20 min |
| 30 | Add parent/index metadata and expansion ordering to status; discover runtime transcripts for `logs --all` | `internal/ops` | 30 min |
| 31 | Test status, logs, follow, and reset-preview behavior with multiple generations and child gates | `internal/ops` | 30 min |
| 32 | Emit typed expansion progress/JSONL and route the event through headless run-ID filtering | `internal/headless` | 20 min |
| 33 | Test text, quiet, JSONL, timeout, and recovery-policy behavior for dynamic children | `internal/headless` | 25 min |
| 34 | Annotate foreach template nodes while retaining the static graph layout | `internal/tui/chart` | 20 min |
| 35 | Add layout/render tests for foreach nodes, including narrow and horizontally scrolling graphs | `internal/tui/chart` | 20 min |
| 36 | Document syntax, aggregate schema, lifecycle, limits, reset/resume, directories, and remove A8 from the deferred list | `docs/workflow-schema.md` | 30 min |
| 37 | Add a non-mutating, bounded discovery -> foreach -> synthesis example that runs under headless CI | `examples/dynamic-fanout.toml` | 20 min |
| 38 | Update the kitchen-sink schema commentary/coverage where appropriate and revalidate it without changing backend selection semantics | `.agents/jig/feature.toml` | 15 min |
| 39 | Update engine scheduling/replay/reset diagrams and persistence invariants | `docs/engine-design.md` | 25 min |
| 40 | Add family/instance/barrier vocabulary and avoid conflating runtime fan-out with the event subscriber fan-out | `CONTEXT.md` | 15 min |
| 41 | Run focused packages, full tests, race-sensitive engine cases, vet, build, both workflow validations, and the headless dynamic example | repository-wide verification | 30 min |

Estimated focused implementation time: **17–20 hours**, best delivered as the
phases below rather than one review unit.

### Delivery phases and exit criteria

#### Phase 1 — schema and pure contracts

Tasks 1–10. Exit when valid TOML yields a fully checked `ForEach`, invalid
sources/caps fail at load time, module expansion preserves refs, and the manifest
plus event round-trip without engine execution.

#### Phase 2 — in-memory scheduler MVP

Tasks 11–15 and 20–24. Exit when persistence-off tests prove N child agent and
command executions, bounded concurrency, deterministic aggregation, empty-list
success, and complete failure/recovery behavior with no downstream early start.

#### Phase 3 — durability, reset, and worktrees

Tasks 16–19. Exit when a killed run reopens every child park using the original
manifest, mutating children integrate one commit each, and family/upstream reset
removes all affected child state and commits before creating a new generation.

#### Phase 4 — operator surfaces

Tasks 25–35. Exit when live and replayed Monitor/Runs, ops status/logs, headless
JSONL, and static chart presentation all agree on family membership, counts,
ordering, gates, and spend.

#### Phase 5 — docs, dogfood, and full verification

Tasks 36–41. Exit when the schema no longer calls dynamic fan-out deferred, both
reference workflows validate, the headless example completes with ordered
aggregate evidence, and the repository passes the verification matrix.

### Test and proof matrix

| Contract | Required proof |
|---|---|
| Load-time safety | Valid scalar/object lists; every invalid ref/type/bound/type combination; module rewrite; reserved ID and `as` collision errors |
| Determinism | Identical input produces identical generation/index IDs, manifest digest, child order, aggregate order, and replayed snapshot despite reversed completion timing |
| Bounds | `max_items` rejects before child creation; family/global/resource/read-mutate caps are all simultaneously respected |
| Data delivery | Agent and command receive exact canonical JSON; item digest appears in input provenance; ordinary inputs remain unchanged |
| Fan-in | No dependent dispatch before every child is terminal/accepted; zero items completes immediately; failed count and `all_succeeded` are honest |
| Failure | Per-child retry/backoff, timeout, validation failure, recovery retry/resume/skip/abort, security escalation, and question gate |
| Persistence | No writes at empty run dir; manifest is atomic/versioned/digested; raw items absent from journal; aggregate and child `result.json` files exist |
| Crash reopen | Crash at expansion boundaries and with child running/validating/stopped/question/recovery/integration parks; exact manifest values reused |
| Git/reset | Parallel mutating children integrate separately; conflict names child; family/upstream reset removes child commits and preserves unrelated survivors; next generation may change count |
| Presentation | Monitor collapse/expand/cursor/transcript/gate behavior; Runs totals; ops ordering/log discovery; headless event typing; static chart marker |
| Regression | `go test ./...`, selected `go test -race ./internal/engine`, `go vet ./...`, `go build ./cmd/jig`, both TOML validations, headless example |

### Risks and mitigations

| Risk | Mitigation |
|---|---|
| Runtime graph mutation leaks into static DAG algorithms | Never append children to `wf.Steps`; keep a scheduler-owned registry and explicitly expand closures only at runtime boundaries. |
| Crash between output production and child creation changes the list | Persist exact canonical items before the expansion event; Resume trusts the digested manifest, not a fresh producer read. |
| One large family starves unrelated work | Apply family-local `max_parallel` in addition to the existing global and resource caps; keep ready order deterministic. |
| Parent and children double-count progress or cost | Treat the parent as a zero-cost barrier; carry parent metadata explicitly and test every consumer's totals. |
| Child failures complete the barrier incorrectly | Centralize terminal/accepted classification in the coordinator and invoke it from every transition path, including operator skip and integration resolution. |
| Dynamic mutating children make reset history ambiguous | Give every child a stable per-generation ID and commit trailer; expand family membership before rewind planning. |
| Raw list data leaks into high-volume journal events | Journal IDs, indices, and digests only; keep values in the run-owned manifest/output files. |
| TUI becomes unreadable for large N | Family row is collapsed by default, shows progress, and reveals ordered children on demand; `max_items` keeps the upper bound explicit. |
| Schema appears to support list projection that runtime cannot perform | Expose only the whole `results` list plus scalar aggregate fields; reject deeper dotted traversal through a list. |

### Acceptance checklist

- [ ] `jig validate` accepts the documented example and rejects all invalid
      foreach contracts before an executor runs.
- [ ] A producer returning N items creates exactly N child IDs in source order,
      never exceeds every applicable concurrency cap, and runs siblings in
      parallel when capacity permits.
- [ ] Each child gets exact typed item JSON, independent transcript/result
      artifacts, ordinary retries/gates, and (when mutating) its own worktree and
      integration commit.
- [ ] The aggregate is stable by source order, exposes honest counts/statuses,
      and is the only author-addressable family output.
- [ ] Dependents never start early; zero items is a successful empty fan-in.
- [ ] Killing and resuming at each lifecycle boundary restores the exact
      expansion and existing child gate semantics.
- [ ] Resetting the family or its producer creates a new generation without
      losing prior transcripts or unrelated branch work.
- [ ] Monitor, Runs, ops, headless JSONL, and chart agree on family/child
      identity and do not double-count spend.
- [ ] Persistence-off tests pass with no filesystem writes.
- [ ] Full build/test/vet and reference-workflow validation pass.


