# 03-validation-step-context-assembly.md

Validation report for the **engine-assembled deterministic step-context
preamble** (spec 03). Evaluates the implementation on the current `main` branch
against [`03-spec-step-context-assembly.md`](03-spec-step-context-assembly.md)
and [`03-tasks-step-context-assembly.md`](03-tasks-step-context-assembly.md),
using the committed [`03-proofs/`](03-proofs/) artifacts as the evidence source.

## 1) Executive Summary

- **Overall:** **PASS** — no gates tripped (GATE A–F all pass).
- **Implementation Ready:** **Yes** — all six Demoable Units are implemented,
  every functional requirement is backed by a passing named test and/or a
  test-locked proof artifact, and the change is confined to the deterministic
  orchestration layer with no scope creep.
- **Key metrics:**
  - **Functional requirements verified:** 6/6 units (100%); every unit's proof
    tests pass.
  - **Proof artifacts working:** 15/15 (100%) — 5 rendered `.txt` goldens (3
    test-locked), 1 CLI-error capture (reproduced live), 6 per-task proof docs,
    the skill-narration diff, and the behavioral spot-check.
  - **Files changed vs. expected:** 18 core+doc files, **all** on the tasks'
    "Relevant Files" list; **0** out-of-scope core changes.
  - **Regression suite:** `go build ./...`, `go vet ./...`, and
    `go test ./... -race` all pass; `go run ./cmd/jig validate
    examples/feature.toml` exits 0.

One non-blocking follow-up (LOW): the Unit 6 behavioral spot-check leaves the
human live-run sign-off checkbox unchecked — intentional per spec (it is a
manual acceptance step, explicitly *not* an automated gate), noted below as a
pre-merge reviewer action.

## 2) Coverage Matrix

### Functional Requirements (by Demoable Unit)

| Requirement (Unit) | Status | Evidence |
| --- | --- | --- |
| **U1** `StepContext`/`ContextNeighbor` data model + pure `Render()`; deterministic ordering; graceful omission; `""` on zero value | **Verified** | `internal/step/context.go`; `TestStepContextRenderGolden`, `TestStepContextRenderStable` (100×), `TestStepContextRenderOmits` (3 sub-cases) all PASS; golden `1.0-render-golden.txt` is byte-locked by the test. Commit `5e331f3`. |
| **U2** engine assembles topology into `StepRequest.WorkflowContext` (agent steps only); runner prepends before body with `---`; framing-only; command/review empty | **Verified** | `internal/engine/context.go` (`buildStepContext`), `internal/engine/executor.go` (`WorkflowContext` field), `internal/runner/agent.go:439` (prepend). `TestBuildRequestWorkflowContext`, `TestBuildRequestNoSiblingLeak`, `TestBuildRequestNonAgentEmpty`, `TestBuildAgentPromptPrependsContext`, `TestBuildAgentPromptEmptyContext` all PASS. Proof `2.0-plan-preamble.txt` is test-locked to the real example. Commit `7c3f45e`. |
| **U3** run-state framing: 0-indexed `Iteration`, re-run-only clause; `RerunReason` via in-memory `rerunSource` map; review vs. gate phrasing; no new persisted state/events | **Verified** | `internal/engine/context.go:43-49,137-142`, `engine.go` (`rerunSource` field + `fireLoop` write). `TestWorkflowContextReviseIteration`, `TestWorkflowContextGateRerun`, `TestWorkflowContextFirstRun`, `TestWorkflowContextMultipleLoops` (FLAG-1) all PASS. Proof `3.0-revise-loop-preamble.txt` test-locked. Commit `d3fe4f0`. |
| **U4** `inject_context` `*bool` on `[defaults]` + per-step; effective toggle skips assembly → empty context; validator rejects on command/review and rejects `[step.context]` + explicit `inject_context = false` | **Verified** | `schema.go` (`InjectContext *bool`), `load.go` (`InjectContextEnabled`), `validate.go` (rejections). `TestDecodeInjectContext`, `TestBuildRequestInjectContextOff`, and `TestDecodeInvalid` rows PASS; CLI proof `4.0-validate-error.txt` **reproduced live** (`exit status 1`, exact message). Commit `59fdf80`. |
| **U5** optional `[step.context]` (`purpose`/`notes`); own injection + neighbor `purpose` propagation; supplements never replaces; valid + invalid tests | **Verified** | `schema.go` (`StepContextSpec`), `internal/engine/context.go:31-34,62-64,88-90`. `TestWorkflowContextPurposePropagation`, `TestDecodeStepContext`, `TestDecodeInvalid` rows PASS; proof `5.0-context-block-preamble.txt`. Commit `856fc6c`. |
| **U6** docs (`workflow-schema.md` section + field tables), ADR 0006, example exercises both new fields + still validates, skill-narration removed; README refresh (FLAG-2) | **Verified** | `docs/workflow-schema.md` (+86), `docs/adr/0006-*.md` (new), `examples/feature.toml` (+19, `inject_context=false` on `research_frontend`, `[step.context]` on `plan`/`research_backend`), `.agents/skills/plan|implement/SKILL.md` trimmed, `README.md` (Go 1.25, engine runs). `6.0-validate-ok.txt` (exit 0, reproduced). Commit `72bac67`. |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Schema additions exhaustive & load-time (CLAUDE.md / TESTING.md) | **Verified** | Both new fields parsed, defaulted (`applyDefaults`), validated, with valid-path decode **and** `TestDecodeInvalid` rows per rejection asserting the specific error substring. |
| `internal/` packages as unit of design; `internal/step` imports nothing | **Verified** | `internal/step/context.go` imports only `fmt`/`strings`. `internal/engine/context.go` projects `workflow`/`step` data inward (imports `jig/internal/step`, never the reverse). |
| Comments explain the non-obvious "why" | **Verified** | Determinism (fixed ordering, no map-iteration), status-token retention (`on_failure=continue`), guard-conditional honesty, and block_on scoping are each commented at the site. |
| Persistence-off is a first-class no-op path | **Verified** | Empty `WorkflowContext` yields a byte-identical prompt, regression-locked by `TestBuildAgentPromptEmptyContext`; assembly is pure and needs no run dir. |
| Examples are documentation | **Verified** | `go run ./cmd/jig validate examples/feature.toml` → `ok: "feature" v1 — 15 step(s)` (exit 0). |
| Testing patterns (table-driven, `-race`) | **Verified** | New tests follow the inline-TOML/table-driven house style; full suite passes under `-race`. |
| Mirror `base_schema.go` deterministic-contract precedent | **Verified** | `StepContext` is the input/position analogue of `BaseSchema`'s output contract; doc-comment style follows the precedent. |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| U1 | `1.0-render-golden.txt` | **Verified** | Byte-identical to spec Rendered format; locked by `TestStepContextRenderGolden` (`goldenPath` const). |
| U2 | `2.0-plan-preamble.txt` | **Verified** | Regenerated from the real `examples/feature.toml` `plan` step and locked by `TestBuildRequestPlanPreambleGolden` (PASS). First-run form (no iteration/State). |
| U3 | `3.0-revise-loop-preamble.txt` | **Verified** | Same topology + `iteration 2 of 3` + revise `State:` line; locked by `TestWorkflowContextReviseIteration` (PASS). |
| U4 | `4.0-validate-error.txt` | **Verified** | Reproduced live: `step "build": inject_context is only valid on agent steps`, `exit status 1`. |
| U5 | `5.0-context-block-preamble.txt` | **Verified** | Shows own `Purpose`/`Notes` injection and `plan`'s purpose propagated onto `implement`'s Upstream line. |
| U6 | `6.0-validate-ok.txt` | **Verified** | Example validates, exit 0 (reproduced). |
| U6 | `6.0-skill-narration-removed.txt` | **Verified** | Git diff of the removed position/loop narration from both skills; matches the committed `SKILL.md` state. |
| U6 | `6.0-preamble-spotcheck.md` | **Verified (manual review doc present)** | Structured before/after orientation mapping; reviewer live-run checkbox intentionally open (see Issue LOW-1). |
| U1–U6 | `03-task-01…06-proofs.md` | **Verified** | Six per-task proof docs present, each front-loading task context before evidence. |

## 3) Validation Issues

Only one non-blocking item; no CRITICAL/HIGH/MEDIUM issues found.

| Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- |
| LOW | Unit 6 behavioral spot-check sign-off is unchecked. `6.0-preamble-spotcheck.md#L88` leaves `- [ ] Confirmed on a real plan run…`. Evidence: proof-doc review. | None to spec compliance — the spec (Unit 6, Design Decision 14) makes this a **manual** acceptance step, explicitly not an automated gate, and the deterministic before/after mapping is complete. | Before merge, the reviewer performs one live `plan` run and checks the box (or records any orientation gap). This is a merge-readiness action, not a validation failure. |

**GATE D note (informational, not an issue):** a `git diff` from `1f8808c`
surfaces `internal/tui/monitor.go`; this belongs to the pre-spec commit `76f377b`
("chore: update the input ui…"), *outside* the spec-03 range
(`5e331f3^..HEAD`). Within the spec range every changed core file maps to a
Relevant Files entry and a functional requirement — no unmapped out-of-scope
core change (GATE D1 clean).

## 4) Evidence Appendix

### Git commits analyzed (spec-03 range `5e331f3^..HEAD`, all 2026-08-06)

| Commit | Unit | Summary |
| --- | --- | --- |
| `5e331f3` | 1 | StepContext data model and pure deterministic renderer (+ spec/tasks/audit) |
| `7c3f45e` | 2 | engine assembles step-context preamble, runner prepends it |
| `d3fe4f0` | 3 | run-state framing for loop re-runs in step-context preamble |
| `59fdf80` | 4 | inject_context opt-out for the step-context preamble |
| `856fc6c` | 5 | [step.context] authoring block — own + propagated purpose/notes |
| `72bac67` | 6 | docs: schema section, ADR 0006, example + skill-narration payoff |

Commits map 1:1 to the six Demoable Units; the implementation story is coherent
and each message references its unit's scope. No unrelated changes within range.

### Commands executed with results

```
$ go build ./...                              # exit 0 (no output)
$ go vet ./...                                # exit 0 (no output)
$ go test ./... -race                         # all packages ok (step, engine, runner,
                                              #   workflow, tui, datastore, transcript)
$ go run ./cmd/jig validate examples/feature.toml
  ok: "feature" v1 — 15 step(s)               # exit 0
$ go run ./cmd/jig validate /tmp/bad-inject-context.toml
  invalid workflow: step "build": inject_context is only valid on agent steps
  exit status 1                               # matches 4.0-validate-error.txt
```

### Named proof tests (all PASS)

- `internal/step`: `TestStepContextRenderGolden`, `TestStepContextRenderStable`,
  `TestStepContextRenderOmits`.
- `internal/engine`: `TestBuildRequestWorkflowContext`,
  `TestBuildRequestNoSiblingLeak`, `TestBuildRequestNonAgentEmpty`,
  `TestBuildRequestInjectContextOff`, `TestBuildRequestPlanPreambleGolden`,
  `TestWorkflowContextReviseIteration`, `TestWorkflowContextGateRerun`,
  `TestWorkflowContextFirstRun`, `TestWorkflowContextMultipleLoops`,
  `TestWorkflowContextPurposePropagation`.
- `internal/runner`: `TestBuildAgentPromptPrependsContext`,
  `TestBuildAgentPromptEmptyContext`.
- `internal/workflow`: `TestDecodeInjectContext`, `TestDecodeStepContext`,
  `TestDecodeInvalid` (non-agent + contradiction + non-string-field rows).

### Security check (GATE F)

Credential scan across `03-proofs/` (`sk-…`, `api_key=`, `password=`, `token=`,
`secret=`, `bearer`, `ghp_`, `AKIA`) returned **no matches**. Proof artifacts are
rendered preambles and CLI captures from the local example workflow — no secrets.

## Gate Results

| Gate | Result | Basis |
| --- | --- | --- |
| A — no CRITICAL/HIGH | **PASS** | No such issues found |
| B — no `Unknown` in matrix | **PASS** | All requirements Verified |
| C — proof artifacts accessible & functional | **PASS** | 15/15; 3 goldens test-locked, CLI captures reproduced |
| D — file integrity (tiered) | **PASS** | All in-range core changes mapped; no unmapped out-of-scope core change |
| E — repository standards | **PASS** | Load-time exhaustive validation, pure `internal/step`, why-comments, persistence-off no-op |
| F — security | **PASS** | No credentials in proofs |

---

## How to Continue the SDD Workflow

This feature's SDD workflow is **complete** — spec → tasks → audit → implementation
→ validation are all present and passing. The next SDD action is starting Phase 1
for a new feature.

To continue in this chat, reply with:

`Start SDD for a new feature.`

**Before merging:** perform a final code review of the completed implementation and
this validation report, and complete the one pre-merge action — the human live-run
sign-off in `03-proofs/6.0-preamble-spotcheck.md` (Issue LOW-1).

**Validation Completed:** 2026-08-06
**Validation Performed By:** Claude (Opus 4.8)
