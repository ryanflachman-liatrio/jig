# 09-validation-docs-examples-adrs.md

**Validation Completed:** 2026-08-07  
**Validation Performed By:** Claude Sonnet 4.6 (1M context)

---

## 1. Executive Summary

- **Overall:** PASS — all mandatory gates clear
- **Implementation Ready:** Yes — all four functional requirements verified with direct evidence; no blockers
- **Key metrics:** 4/4 Requirements Verified (100%), 4/4 Proof Artifacts Working (100%), 7 files changed (all within Relevant Files list or spec artifacts)

---

## 2. Coverage Matrix

### Functional Requirements

| Requirement | Status | Evidence |
| --- | --- | --- |
| FR-1: `ARCHITECTURE.md` documents the run-integration-branch model (run branch, step worktrees off run HEAD, squash-per-step, step→commit map) and the reset seam | Verified | `docs/ARCHITECTURE.md`: new `## Integration model — the run branch` section present; 9 matches for `run branch\|squash-per-step\|step→commit\|final human-gated`; `### Stop and reset seam` subsection present with ADR 0008 link |
| FR-2: `workflow-schema.md` notes explicit `merge` steps are no longer the integration mechanism; states reset/stop add no schema surface | Verified | `git merge --no-ff` string: 0 matches in workflow-schema.md (removed); `"Stop and reset add no schema surface"` paragraph: 1 match confirmed; Worktrees section updated to describe run-branch model |
| FR-3: `engine-design.md` describes stop (per-step quiescence) and the reset flow | Verified | `### Stop — per-step quiescence` section present; `### Reset — dependency closure and rewind+replay` section present; `StatusStopped`, `StatusAwaitingIntegration`, `Generation int`, `StepsReset` all documented and matching `internal/step/step.go` and `internal/engine/event.go` |
| FR-4: ADRs 0007 and 0008 are linked from the ADR index | Verified | `docs/adr/README.md` created with table covering ADRs 0001–0004, 0006–0008 (ADR 0005 absence noted explicitly); 3 link references to 0007/0008 confirmed across `ARCHITECTURE.md` and `engine-design.md` |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Comments explain non-obvious *why* | Verified | New sections in all three docs explain *why* integration moved into the engine, *why* reset is rewind+replay (not bare `git reset`), *why* quiescence is required — cross-referencing ADRs for full rationale rather than duplicating it |
| ADRs are the durable decision record | Verified | Docs cross-link ADR 0007 and 0008 rather than restating their full rationale; `docs/adr/README.md` created as the authoritative index |
| Examples must pass `jig validate` | Verified | `go run ./cmd/jig validate examples/feature.toml` → `ok: "feature" v1 — 15 step(s)` exit 0; `go run ./cmd/jig validate examples/bugfix.toml` → `ok: "bugfix" v1 — 3 step(s)` exit 0 |
| No new runtime behavior | Verified | All 7 changed files are documentation or spec artifacts; `go test ./...` → 280 tests pass (same count as before spec 09); no Go source files modified |

### Proof Artifacts

| Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| 1.0 ARCHITECTURE.md | Doc: integration model + reset seam sections with ADR links | Verified | `grep` confirms 9 occurrences of key phrases; ADR 0007 and 0008 links present at lines 183 and 217 |
| 2.0 workflow-schema.md | Doc: no merge-step snippet; no-schema-surface note | Verified | 0 matches for `git merge --no-ff`; 1 match for `Stop and reset add no schema surface` |
| 2.0 workflow-schema.md | CLI: `jig validate examples/feature.toml` exits 0 | Verified | `ok: "feature" v1 — 15 step(s)` exit 0 |
| 2.0 workflow-schema.md | CLI: `jig validate examples/bugfix.toml` exits 0 | Verified | `ok: "bugfix" v1 — 3 step(s)` exit 0 |
| 3.0 engine-design.md | Doc: stop/quiescence + reset sections with ADR link | Verified | Both `### Stop —` and `### Reset —` sections present; ADR 0008 link at line 331 |
| 3.0 engine-design.md | Doc: `StatusStopped`, `StatusAwaitingIntegration`, `Generation`, `StepsReset` | Verified | All four symbols confirmed present in engine-design.md; match the live code in `internal/step/step.go` and `internal/engine/event.go` |
| 4.0 ADR index | Doc: `docs/adr/README.md` lists ADRs 0001–0004, 0006–0008 | Verified | File exists (2566 bytes); 3 occurrences of 0007/0008 link references confirmed |
| 4.0 Proof artifact | File: `09-proofs/D.0-docs-and-adr.md` exists | Verified | File present at `docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md` (5834 bytes) |
| All tasks | CLI: `go build/vet/test ./...` all exit 0 | Verified | `go build ./...` exit 0; `go vet ./...` exit 0; `go test ./...` → 280 passed in 9 packages |

---

## 3. Validation Issues

No issues found. All functional requirements verified, all proof artifacts accessible and functional, no out-of-scope changes, no credentials in artifacts, repository standards followed throughout.

**Pre-existing note (not a spec 09 issue):** `examples/research.toml` and `examples/review.toml` fail `jig validate` with schema errors that predate this spec (confirmed via `git stash` verification before any spec 09 changes were applied). These failures are unrelated to this spec's scope and are not regressions.

---

## 4. Evidence Appendix

### Git commit

```
commit 1242437
docs: spec 09 — align docs, examples, and ADRs with implemented engine

 docs/ARCHITECTURE.md                               | 109 ++++++++++++--------
 docs/adr/README.md                                 |  21 ++++
 docs/engine-design.md                              |  93 +++++++++++++++--
 docs/specs/09-spec-docs-examples-adrs/09-audit-docs-examples-adrs.md  |  44 ++++++++
 docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md   | 113 +++++++++++++++++++++
 docs/specs/09-spec-docs-examples-adrs/09-tasks-docs-examples-adrs.md  | 113 +++++++++++++++++++++
 docs/workflow-schema.md                            |  29 ++++--
 7 files changed, 459 insertions(+), 63 deletions(-)
```

All 7 changed files are within the Relevant Files list or are spec artifacts (task list, audit, proof). No files outside the planned scope.

### FR verification commands

```
# FR-1: run-integration-branch model in ARCHITECTURE.md
grep -c "run branch\|squash-per-step\|step→commit\|final human-gated" docs/ARCHITECTURE.md
→ 9

# FR-2: merge step removed
grep -c "git merge --no-ff" docs/workflow-schema.md
→ 0

grep -c "Stop and reset add no schema surface" docs/workflow-schema.md
→ 1

# FR-3: stop/reset sections in engine-design.md
grep -c "Stop — per-step quiescence\|Reset — dependency closure" docs/engine-design.md
→ 2

# FR-4: ADR index created and links present
grep -c "0007\|0008" docs/adr/README.md
→ 3
```

### Example validation results

```
go run ./cmd/jig validate examples/feature.toml
→ ok: "feature" v1 — 15 step(s)   (exit 0)

go run ./cmd/jig validate examples/bugfix.toml
→ ok: "bugfix" v1 — 3 step(s)     (exit 0)
```

### Build and test results

```
go build ./...     → exit 0
go vet ./...       → exit 0
go test ./...      → 280 tests passed in 9 packages (exit 0)
```

### Security check

```
grep -iE "api[_-]?key|token|password|secret|credential|bearer" \
  docs/specs/09-spec-docs-examples-adrs/09-proofs/D.0-docs-and-adr.md
→ (no output — clean)
```
