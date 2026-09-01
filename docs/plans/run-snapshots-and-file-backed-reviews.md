# Plan: Run snapshots and file-backed human reviews

**Status:** Proposed  
**Risk:** High  
**Primary packages:** `internal/workflow`, `internal/engine`, `internal/runner`, `.agents/jig`  
**Motivation:** A persisted jig run must give every consumer a coherent view of
its completed dependencies, and a review gate must be able to snapshot an
upstream-produced file rather than only a summary or a path string.

## Executive summary

The current run model has two distinct working-directory concepts but exposes
only one of them to executors:

- a mutating step has a private worktree, later squash-merged into the run
  branch;
- the run branch itself has a `_run` worktree containing the integrated result.

Only the first is passed to agent, command, and check executors. Consequently,
a read-only downstream step (`isolation = "none"`) runs in the process's
original checkout instead of the run state. The latest SDD run therefore
generated its specification and task plan in the run branch, while its audit
agents looked for those files in the original checkout and reported false
missing-input failures.

Reviews have a related but separate type error. `source =
"@step.path_field"` means “review the *string* held by that field.” It does not
mean “open the file named by that string.” Thus architecture and proof gates
can show a one-sentence summary or a pathname instead of the document the human
needs to inspect.

This change introduces a run-owned **execution snapshot** for all persisted
workers and a distinct `file` review target for dynamic, upstream-produced
files. It deliberately does **not** give a downstream consumer direct access
to another step's private worktree. A private worktree is an implementation
detail of one producer; the run branch is the durable, integrated contract
between graph nodes.

## Evidence from the latest run

Run `20260831-201102-2k35pden` produced these files in its `_run` worktree:

- `docs/specs/sub-workflows/sub-workflows.md`
- `docs/specs/sub-workflows/sub-workflows-tasks.md`
- `docs/specs/sub-workflows/sub-workflows-audit.md`

The audit agents had `isolation = "none"`, so their ACP sessions started from
the original checkout. They received path strings such as
`docs/specs/sub-workflows/sub-workflows.md`, but that path did not exist in
their CWD. Gate 1 therefore falsely reported missing specification and task
files. Gate 3 independently lacked the `standards_evidence` input entirely.

The review configuration also explains the human experience:

- `spec__architecture_gate` reviews `@write_spec.summary`, a deliberate short
  structured field, not `@write_spec.spec_path` as a file.
- `plan__audit_review` reviews only `@synthesize_audit.audit_report`; it does
  not include the specification or task file.
- `implement_task__task_checkpoint` reviews `@proof.proof_path`, which renders
  the path text rather than the proof document.

The review workspace itself correctly snapshots the document it is given. The
defect is target selection and execution-root resolution, not TUI truncation or
review persistence.

## Goals

1. Every persisted agent, command, and check step runs against a stable
   run-owned snapshot that contains all successfully integrated dependencies.
2. A human-review target can explicitly dereference an upstream text field as a
   repository-relative file path and snapshot that file's exact bytes.
3. Path-bearing runtime operations—literal file inputs, explicit agent output,
   validate-gate output checks, dynamic review files—resolve against the same
   execution snapshot.
4. The SDD workflow lets a reviewer inspect the actual specification, task
   plan, proofs, and code diff appropriate to each gate.
5. Persistence-off remains first-class: no run worktree or filesystem snapshot
   is required when jig has no persisted run directory/repository context.

## Non-goals

- Allowing review documents to be edited in the review workspace.
- Allowing an agent-produced path to escape the repository/run snapshot.
- Making producer-private worktrees a public workflow concept.
- Changing the human review submission, comment, digest, replay, or feedback
  model introduced by the document-review workspace.
- Retaining compatibility shims for ambiguous legacy semantics. jig is pre-v1;
  the documented execution-snapshot contract replaces the old implicit CWD
  behavior.

## Design decisions

### 1. Use an execution snapshot, not a shared producer worktree

At worker dispatch, the scheduler owns the only decision about which repository
state is visible. A mutating step continues to receive a private worktree
created from the current run-branch HEAD. A read-only step receives an
ephemeral **execution-view worktree** created from that same run-branch HEAD.

This is stronger than pointing all read-only steps at `_run` directly:

- a snapshot cannot change beneath a long-running audit while an unrelated
  step integrates;
- independent readers remain safe to run in parallel;
- a misbehaving read-only command cannot modify the central `_run` worktree;
- every worker has a precise, inspectable dispatch base SHA.

The existing scheduler already integrates a successful mutating step before it
transitions to `succeeded`. A dependent can therefore only be dispatched after
the run branch contains that producer's accepted changes. The execution view
captures this existing ordering guarantee rather than creating a new dataflow
rule.

Suggested internal value object:

```go
// internal/engine/execution.go
type executionWorkspace struct {
	Dir     string // immutable-at-dispatch view; empty in persistence-off mode
	BaseSHA string
	Kind    executionWorkspaceKind // mutation or readOnlyView
}

func (s *scheduler) acquireExecutionWorkspace(st *workflow.Step) (executionWorkspace, error)
func (s *scheduler) releaseExecutionWorkspaces()
```

Use a small value object and two scheduler helpers rather than a hierarchy of
executor interfaces. This is the Go “Strategy at the boundary” pattern: the
scheduler chooses a workspace policy once, while runners simply consume
`ExecutionDir`. Keeping policy selection in one place prevents agent, command,
check, validation, and review code from independently re-implementing CWD
rules.

Suggested layout and branch convention:

```text
.jig/worktrees/<run-id>/_run                     # integrated run branch
.jig/worktrees/<run-id>/<mutating-step-id>        # existing mutable checkout
.jig/worktrees/<run-id>/views/<read-only-step-id> # new immutable-at-dispatch view

jig/<workflow>/<run-id>/<step-id>                # existing mutation branch
jig/<workflow>/<run-id>/view/<step-id>           # ephemeral reader branch
```

The reader branch is not integrated and is deleted with its worktree. Its only
purpose is a stable CWD. The existing `max_read_only` and `max_mutating`
accounting remains based on declared isolation; a non-mutating view must not be
mistaken for a mutation worktree merely because it is implemented with Git.

### 2. Split private mutation workspace from execution directory

`StepRequest.Worktree` currently means both “the isolated step workspace” and
“the directory in which a runner should execute.” Those meanings diverge for
read-only views. Add this field instead:

```go
// internal/engine/executor.go
type StepRequest struct {
	// ...existing fields...
	Worktree     string // private mutable workspace; empty for isolation = none
	ExecutionDir string // run-owned dispatch snapshot; empty without persistence
	RepoRoot     string
}
```

Runner behavior becomes simple and uniform:

```go
// AgentExecutor and CommandExecutor use the same precedence.
cwd := req.ExecutionDir
if cwd == "" {
	cwd = executorConfiguredCWD // persistence-off fallback
}
```

`Worktree` remains available to engine-only lifecycle code for diff capture,
mutation allowlists, and squash merge. Do not set `Worktree` for readers merely
to influence their CWD; that would silently make read-only views participate in
diff capture and integration.

### 3. Treat runtime repository paths as capabilities rooted at ExecutionDir

Introduce one shared runtime-path helper, ideally in `internal/workflow` so
both engine and runners use precisely the same policy:

```go
func ExecutionPath(root, authored string) (string, error)
```

Rules:

1. With an empty `root` (persistence-off), preserve existing process-CWD
   behavior.
2. With a non-empty `root`, a relative path resolves under `root`.
3. New dynamic review-file paths must be relative, clean, and remain within
   `root` after symlink evaluation; absolute paths and `..` escapes fail closed.
4. Static authoring assets (`skill`, `agent_file`, `output_template`, literal
   `source`) remain resolved from their workflow file as they are today. They
   are configuration, not runtime products.

The helper is a narrow capability boundary. It avoids scattering
`filepath.Join`, `Clean`, `EvalSymlinks`, and containment checks across
`engine.go`, `review.go`, and runners—the usual source of path traversal and
“validated one path, opened another” bugs.

Explicit agent outputs and validate-gate checks must call this same helper.
Currently, an agent can execute in a worktree while `Step.Output` is written
and later validated relative to the process CWD. That is an adjacent
consistency bug this plan resolves.

### 4. Add `file` as a distinct review-target form

Retain `source` for content values and existing special cases. Add `file` only
for an upstream field whose *value is a repository-relative path*:

```toml
[[step.review]]
file  = "@write_spec.spec_path"
label = "Specification and architecture"
```

The target is a small discriminated union represented in TOML by mutually
exclusive fields:

| Form | Meaning |
|---|---|
| `source = "@step.field"` | Render the text stored in a structured field. |
| `source = "@step"` | Render the producer's primary output artifact. |
| `source = "diff"` | Render captured diffs from transitive dependencies. |
| `source = "file.md"` | Render a static workflow-relative text file. |
| `file = "@step.path_field"` | Resolve the referenced text as a path in the review dispatch snapshot, then render that file. |

Use `ReviewTarget.Kind()` and `ReviewTarget.Reference()` helper methods to keep
the validator, module rewriter, context builder, and review engine from
switching on raw strings independently. A full interface hierarchy would add no
value to this two-field data model.

Validation requirements for `file`:

- exactly one of `source` or `file` is set;
- `file` must be an `@step.field` reference, never a bare step or literal path;
- the referenced field exists, has `FieldText`, and its step is a direct
  `depends_on` dependency;
- labels remain non-blank and unique across every target form;
- module input references may use `@module.<input>` and must be rebound during
  subworkflow instantiation.

At runtime, `prepareReview` resolves the structured field, applies
`ExecutionPath(s.runWorktree, value)`, verifies a regular non-binary file, and
copies the bytes into the already-existing round snapshot store. The persisted
`review.Document.Source` should record the logical field reference plus the
resolved repository-relative path for reviewer traceability; presentation
format should derive from the resolved filename extension.

### 5. Keep review snapshots immutable and replay-compatible

No review event should contain bulk file content. `prepareReview` continues to
write per-round documents under:

```text
.jig/runs/<run-id>/steps/<review-step>/review/<round>/documents/
```

The event carries only descriptors and snapshot paths; replay loads the
snapshots and checks their digest exactly as it does now. Dynamic file targets
therefore gain the same stable-line-anchor, immutable-round, and resume
guarantees as static targets.

## Detailed implementation plan

### Phase 1 — Runtime execution-root foundation

1. **Add execution workspace lifecycle** — `internal/engine/execution.go`
   (new), `internal/engine/engine.go` — 60 min.

   Add scheduler-owned acquisition and cleanup for reader views. Reuse
   `createWorktreeAt` with a distinct branch/path convention. Record each view
   independently from `s.worktrees`; mutation worktrees must retain their
   current retry and loop reuse behavior, while reader views are recreated for
   each dispatch so a retry/re-run receives the correct new run state.

2. **Pass `ExecutionDir` through dispatch** — `internal/engine/executor.go`,
   `internal/engine/engine.go` — 35 min.

   Extend `StepRequest`, acquire the workspace before `resolveAllInputs`, and
   make `buildRequest` accept the execution directory. Preserve `Worktree` for
   mutation lifecycle only. Update error/recovery paths so an acquisition
   failure parks through the existing recovery gate and leaked reader views are
   removed.

3. **Use execution directory for validate gates** — `internal/engine/handlers.go`,
   `internal/engine/engine.go` — 25 min.

   Pass the completed step's execution directory to `runGate`; run gate
   commands and output checks against that root. Avoid consulting the mutable
   worktree map for `isolation = "none"` steps.

4. **Add workspace lifecycle tests** — `internal/engine/worktree_test.go`,
   `internal/engine/integration_test.go` — 60 min.

   Prove that a downstream reader sees a predecessor's integrated file; a
   reader does not see an uncommitted user-checkout edit; two concurrent readers
   receive distinct stable views; views clean up at run completion; and
   persistence-off carries no execution directory or filesystem requirement.

### Phase 2 — Unified runtime path use

5. **Introduce the runtime path resolver** — `internal/workflow/scriptpath.go`
   (rename/generalize only if it stays cohesive) or new
   `internal/workflow/runtimepath.go` — 35 min.

   Keep `ScriptPath`'s project-root contract intact. Add a separate helper for
   runtime repository paths rather than overloading script resolution with
   different security rules. Include containment helpers and symlink handling
   required by dynamic file review.

6. **Resolve literal inputs before snapshotting** — `internal/engine/engine.go`
   — 30 min.

   Change `resolveAllInputs` to take the execution directory. Relative
   `Input.Path` entries are resolved before `snapshotInputs` so the immutable
   input copy and the prompt both name the same file. Structured fields remain
   text; only authored `Input.Path` uses runtime path resolution.

7. **Use the same path resolver in runners** — `internal/runner/agent.go`,
   `internal/runner/command.go`, `internal/runner/check.go` — 45 min.

   Set ACP/SDK session CWD and shell `cmd.Dir` from `ExecutionDir`. Resolve an
   explicit agent `output` under that directory before writing it. Preserve
   absolute run-owned artifact paths used for bare upstream outputs.

8. **Add runner/path tests** — `internal/runner/agent_test.go`,
   `internal/runner/command_test.go`, `internal/engine/engine_test.go` —
   60 min.

   Cover relative literal inputs, output creation, `output_exists`,
   `output_contains`, command CWD, harness `SessionSpec.Cwd`, persistence-off,
   and rejection of an execution-root escape.

### Phase 3 — File-backed review target schema and engine behavior

9. **Extend the workflow schema** — `internal/workflow/schema.go`,
   `internal/workflow/load.go` — 35 min.

   Add `ReviewTarget.File`, target-kind helpers, and only the static-path
   resolution that remains relevant to `source`. Do not resolve a dynamic file
   value while loading: it does not exist until a producer runs.

10. **Validate dynamic file targets** — `internal/workflow/validate.go`,
    `internal/workflow/workflow_test.go` — 60 min.

    Enforce mutually exclusive target forms, reference syntax, direct data and
    ordering edges, text type, duplicate source detection, and meaningful
    errors. Add valid, unknown-step, missing-dependency, non-text, bare-ref,
    duplicate-label, and mixed-source/file cases.

11. **Propagate file references through module expansion** —
    `internal/workflow/module.go`, `internal/workflow/workflow_test.go` —
    60 min.

    Generalize the existing `@module` input marking/rebinding and review source
    prefixing to work for `ReviewTarget.File`. Module expansion must append the
    ultimate producer to `depends_on`, exactly as it does for ordinary inputs.
    Test nested-module and external-binding cases.

12. **Expose file target consumption in graph context** —
    `internal/engine/context.go`, `internal/engine/context_test.go` — 20 min.

    Include the file-reference field in `consumedFields`, so agent context
    accurately says a downstream human review consumes `spec_path` rather than
    a generic output.

13. **Resolve and snapshot dynamic review files** — `internal/engine/review.go`
    — 60 min.

    Refactor field extraction into one engine helper shared by normal inputs and
    review targets. Implement `file` resolution through the run worktree,
    enforce regular-file/UTF-8/byte-budget/path-containment rules, derive the
    presentation format from the resolved path, and persist the existing
    descriptor/snapshot shape without new journal event kinds.

14. **Add review-engine and replay tests** — `internal/engine/review_test.go`
    (new or expanded), `internal/engine/replay_test.go`,
    `internal/engine/resume_test.go` — 75 min.

    Prove the workspace receives document bytes rather than a path string;
    review snapshots survive source mutation and resume; invalid/out-of-root/
    symlink/binary/oversize targets fail closed; and a review containing source,
    file, and diff targets preserves author order.

### Phase 4 — SDD workflow repair

15. **Repair the specification gate** — `.agents/jig/modules/spec.toml` —
    15 min.

    Replace `source = "@write_spec.summary"` with
    `file = "@write_spec.spec_path"`. Retain scope-gate rationale review as a
    text target because it is intentionally a short decision, not a document.

16. **Repair planning audit inputs and review surface** —
    `.agents/jig/modules/plan.toml` — 35 min.

    Rename the `analyze_standards` schema output to `standards_evidence` (to
    match its consuming audit skill), pass it into `audit_standards`, and add
    the direct dependency. At `audit_review`, retain the synthesized audit text
    and add file targets for `@module.spec_path` and
    `@expand_tasks.task_path`.

17. **Repair implementation and merge reviews** —
    `.agents/jig/modules/implement_task.toml`, `.agents/jig/sdd.toml` —
    30 min.

    Change `task_checkpoint` to a file target for `@proof.proof_path`. Add a
    `diff` target to `implementation_review` and `final_merge_approval`, while
    retaining their completion/release summaries as orientation documents.

18. **Update workflow examples and schema documentation** —
    `docs/workflow-schema.md`, `.agents/jig/review-ui-demo.toml`,
    `.agents/jig/feature.toml` if needed — 40 min.

    Define `ExecutionDir`, the snapshot lifecycle, and every review target
    form. Add a small file-backed example. Re-validate every bundled workflow;
    do not alter unrelated examples merely to preserve obsolete semantics.

### Phase 5 — End-to-end proof and quality gates

19. **Create a production-shaped acceptance fixture** —
    `internal/engine/testdata/sdd-acceptance/`,
    `internal/engine/sdd_acceptance_test.go` — 75 min.

    The fixture should write a spec and task file from mutation steps, run a
    read-only audit that reads both, and open a review that snapshots both files
    plus an audit report. Assert exact document content and path provenance,
    then submit a verdict and verify normal routing. This test must use a real
    temporary Git repository and a deliberately different user-checkout file to
    prevent a false-positive CWD test.

20. **Run repository verification and record outcomes** — focused tests first,
    then repository commands — 30 min plus suite time.

    ```bash
    go test ./internal/workflow ./internal/engine ./internal/runner
    go test ./...
    go test ./... -race
    go vet ./...
    gofmt -l -w .
    go build ./cmd/jig
    for workflow in .agents/jig/*.toml; do
      go run ./cmd/jig validate "$workflow" || exit 1
    done
    ```

## SDD workflow target shape after implementation

```toml
# .agents/jig/modules/spec.toml
[[step]]
id = "architecture_gate"
type = "review"
depends_on = ["write_spec", "scope_gate"]
output_type = { enum = ["approve", "revise"] }

  [[step.review]]
  file = "@write_spec.spec_path"
  label = "Specification and architecture"

# .agents/jig/modules/plan.toml
[[step]]
id = "audit_review"
type = "review"
depends_on = ["synthesize_audit", "expand_tasks"]
output_type = { enum = ["approve", "remediate"] }

  [[step.review]]
  source = "@synthesize_audit.audit_report"
  label = "Planning audit"

  [[step.review]]
  file = "@module.spec_path"
  label = "Specification"

  [[step.review]]
  file = "@expand_tasks.task_path"
  label = "Expanded task plan"

# .agents/jig/modules/implement_task.toml
[[step.review]]
file = "@proof.proof_path"
label = "Task proof"

# .agents/jig/sdd.toml
[[step.review]]
source = "diff"
label = "Cumulative implementation diff"
```

## Failure and recovery behavior

| Condition | Required behavior |
|---|---|
| Cannot create reader view | Park the owning step in the existing recovery flow; do not run from the original checkout as a fallback. |
| Dynamic file field empty/missing | Fail review preparation with the target label and logical field reference. |
| Dynamic path absolute, traverses `..`, or escapes through symlink | Fail closed before reading any content. |
| Dynamic file is directory, binary, or exceeds document/round size limits | Fail review preparation using existing document-limit semantics. |
| Persistence-off run | No view worktree is created; runtime uses current process CWD and existing no-op persistence behavior. |
| Review resumes after process exit | Reload only the immutable review snapshot and verify its digest; never re-read the current file path. |

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Ephemeral reader worktrees increase Git operations | Create them only for persisted Git runs; remove them promptly; preserve current concurrency limits; benchmark after correctness is established. |
| A change accidentally integrates reader-view changes | Keep reader views in a separate scheduler map and branch namespace; integration continues to read only the existing mutating-worktree map. |
| Module rewriting drifts from normal inputs | Generalize existing reference-rewrite helpers and cover nested module tests rather than duplicating parsing logic. |
| Runtime path handling becomes inconsistent again | Establish one path-capability helper and make runner, engine validation, input snapshots, and review resolution use it. |
| Existing workflows rely on original-checkout CWD | This is a pre-v1 contract correction. Update bundled workflows and document the new invariant rather than retaining dual runtime modes. |
| Review documents change while a human reads them | Snapshot at review dispatch; use existing digest-backed immutable round storage. |

## Additional findings to resolve while implementing

1. `[defaults].cwd` is parsed and documented but is not currently propagated to
   the agent or command runner. Decide explicitly whether it becomes the
   persistence-off fallback beneath `ExecutionDir`, or remove it as unused
   pre-v1 surface area. Do not leave it documented but inert.
2. Relative literal `inputs` currently depend on process CWD before their input
   snapshot is made. Phase 2 makes that deterministic.
3. Explicit agent `output` and `[step.validate]` output checks currently may
   resolve relative to a different directory from the executing agent. Phase 2
   makes the producer, validator, and consumer agree.
4. The final merge gate should review a diff as well as release notes; release
   notes alone are not sufficient evidence for code approval.

## Completion criteria

- A read-only downstream audit can read files written and integrated by an
  upstream mutation step without seeing unrelated user-checkout modifications.
- The audit and human review operate on the exact same run-owned file bytes.
- A `file = "@producer.path"` target displays the file content, line count, and
  extension-appropriate presentation—not the path string.
- All target forms snapshot and replay with digest validation.
- The SDD architecture, planning, task-proof, implementation, and final-merge
  gates present the intended review evidence.
- Every workflow in `.agents/jig` validates, and the full Go test/vet/build/
  format suite passes.
