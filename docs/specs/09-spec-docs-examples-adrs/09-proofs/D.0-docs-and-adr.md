# D.0 — Docs, Examples, and ADRs

This file records the evidence for spec 09 (docs-examples-adrs). All changes are documentation-only; no runtime code was modified.

## What This Proves

- `docs/ARCHITECTURE.md` describes the run-integration-branch model and the reset seam, replacing the stale "planned" section.
- `docs/workflow-schema.md` retires the hand-wired `merge` command step example and states that stop/reset add no schema surface.
- `docs/engine-design.md` documents per-step stop/quiescence and the reset algorithm, with updated status/event/state enumerations.
- `docs/adr/README.md` is created as an ADR index with ADRs 0001–0004, 0006–0008 linked.
- ADRs 0007 and 0008 are linked from `ARCHITECTURE.md` and `engine-design.md`.
- All example workflows that were valid before remain valid. `research.toml` and `review.toml` have pre-existing validation failures unrelated to this spec (confirmed by `git stash` verification).
- `go build/vet/test ./...` all pass with no regressions.

---

## Artifact: ARCHITECTURE.md — integration model section

**What it proves:** The run-integration-branch model is documented in `docs/ARCHITECTURE.md`, replacing the outdated "planned" section.

**Sections added/changed:**

- Diagram: `internal/engine  (planned)` → `internal/engine` (no more "planned" marker)
- Big-picture bullets: "will execute" → "executes"; TUI description updated to mention run monitor
- `internal/workflow` key decisions: "Worktree isolation" bullet updated to note step worktrees branch off run HEAD
- Package layout: `engine/` entry updated to mention run-branch lifecycle
- Section `## Integration model — the run branch` (new): describes run branch, step worktrees off run HEAD, squash-per-step, step→commit map, final human-gated merge; links to ADR 0007
- Section `### Stop and reset seam` (new): describes `Run.Stop`, quiescence, and the reset rewind+replay algorithm; links to ADR 0008

**Command:**

```bash
grep -c "run branch\|quiescent\|squash\|ADR 0007\|ADR 0008" docs/ARCHITECTURE.md
```

**Result summary:** 17 matches confirm the new content is present.

---

## Artifact: workflow-schema.md — merge step retired, no-schema-surface note

**What it proves:** The hand-wired `merge` command step is removed from the worked example; the Worktrees section reflects the run-branch model; stop/reset have no schema surface.

**Sections changed:**

- `## Worktrees`: updated to say step worktrees branch off run HEAD, not repo-root HEAD; removed "downstream join step merges them" (old model); added note that integration is the engine's job
- Worked example `[[step]] id = "merge" run = "git merge …"` block: **removed** and replaced with a `> Note:` block explaining the final gated merge
- `## Execution semantics`: added a "Stop and reset add no schema surface" paragraph

**Validation commands and exit codes:**

```
go run ./cmd/jig validate examples/feature.toml
→ ok: "feature" v1 — 15 step(s)   exit 0

go run ./cmd/jig validate examples/bugfix.toml
→ ok: "bugfix" v1 — 3 step(s)     exit 0
```

`research.toml` and `review.toml` fail with pre-existing schema errors confirmed via `git stash` before this spec was applied — these failures are unrelated to this spec's changes.

---

## Artifact: engine-design.md — stop/quiescence and reset sections

**What it proves:** `docs/engine-design.md` now documents `StatusStopped`, `StatusAwaitingIntegration`, `Generation`, `StepsReset`, per-step stop/quiescence, and the reset algorithm.

**Sections added/changed:**

- `### internal/step` Status constants: added `StatusAwaitingIntegration` and `StatusStopped` with explanations
- `### internal/step` State struct: added `Generation int` field with provenance-axis note
- `### internal/engine — events`: added `StepsReset` event struct with its crash-consistent write-order note; updated `StepStatus` to include `Generation` and `Err`
- Section `### Stop — per-step quiescence` (new): describes `Run.Stop`, `handleStop`, the `stopping` set, quiescence definition
- Section `### Reset — dependency closure and rewind+replay` (new): 5-step algorithm, `Generation` bump, cherry-pick conflicts, deferred cases; links to ADR 0008
- `### Phase 5 — mutation, safely`: updated to reflect run-branch model (removed old "merge-join convention" reference)
- `### Deferred`: removed stop/reset (implemented); clarified `StepOutput` journaling decision; added settled-run reset as genuinely deferred

**Cross-link verification:**

```
grep "adr/0008" docs/engine-design.md
→ [ADR 0008](../adr/0008-manual-reset-rewind-and-replay.md) for the full algorithm
```

---

## Artifact: docs/adr/README.md — ADR index created

**What it proves:** ADRs 0001–0004, 0006–0008 are indexed and ADRs 0007/0008 are one click away from the index.

**File created:** `docs/adr/README.md`

Contains a markdown table with ADR number, title, status, and one-line decision summary for each of the seven ADRs present (ADR 0005 is absent from the directory; this is noted explicitly in the index).

---

## Artifact: Full build and test suite

**What it proves:** No regressions were introduced by any documentation edit.

**Commands and results:**

```
go build ./...     → exit 0 (no output)
go vet ./...       → exit 0 (no output)
go test ./...      → 280 tests passed in 9 packages
```

---

## Reviewer Conclusion

All four functional requirements from the spec are met: ARCHITECTURE.md describes the integration model and reset seam (with ADR links), workflow-schema.md retires the merge-step guidance and documents no schema surface for stop/reset, engine-design.md documents stop and reset with the full algorithm and ADR link, and the ADR index is created. The two spec-mandated example validations pass. The full test suite passes with 280 tests in 9 packages.
