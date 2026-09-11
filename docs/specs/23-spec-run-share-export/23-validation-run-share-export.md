# 23-validation-run-share-export.md

Spec: [`23-spec-run-share-export.md`](./23-spec-run-share-export.md)
Tasks: [`23-tasks-run-share-export.md`](./23-tasks-run-share-export.md)
Proofs: [`23-proofs/`](./23-proofs/)

## 1) Executive Summary

- **Overall: PASS** (updated 2026-09-11 after remediation — see "Remediation" note below). Gate A originally tripped on one HIGH issue (FR-16's `maxStepInventory` was declared but unenforced); that issue is now fixed, tested, and re-verified. No other Gate A blockers were found.
- **Implementation Ready: Yes.** The feature is functionally complete and well-tested, its disclosure/sanitization guarantees hold up under independent adversarial spot-checks, and the previously-unenforced 10,000-step inventory limit is now enforced with a passing boundary test. Two MEDIUM/LOW-severity findings remain open (see Validation Issues) but are non-blocking.
- **Key metrics:**
  - Functional Requirements verified: 17/17 (100%) — FR-16 now fully verified after remediation.
  - Proof Artifacts working: independently re-run and confirmed for all five parent tasks (100%).
  - Files changed vs. expected: all changed files map cleanly to the task list's "Relevant Files" table; no unmapped core files.

All findings below were produced by independently re-running commands/tests and reading the implementation directly — not by re-reading the implementing agent's self-report.

### Remediation (2026-09-11, same validation cycle)

The original HIGH finding — `maxStepInventory` declared in `internal/runexport/bounds.go` but never checked — was fixed in commit `6072222` ("fix: enforce 10,000-step inventory limit (FR-16)"):

- `internal/runexport/source.go`'s `collectInventory` now aborts with `ErrOperational` when the discovered step-directory count exceeds `maxStepInventory`, checked immediately after `ReadDir(-1)` and before iterating entries.
- A new test, `TestConfinedInventoryEnforcesStepInventoryLimit` (`internal/runexport/export_test.go`), builds synthetic fixtures of exactly 10,000 and 10,001 step directories and asserts success at the boundary and an `ErrOperational` refusal one over it. Re-run independently in this validation pass:

  ```
  === RUN   TestConfinedInventoryEnforcesStepInventoryLimit
  === RUN   TestConfinedInventoryEnforcesStepInventoryLimit/at_limit
  === RUN   TestConfinedInventoryEnforcesStepInventoryLimit/above_limit
  --- PASS: TestConfinedInventoryEnforcesStepInventoryLimit (7.52s)
      --- PASS: TestConfinedInventoryEnforcesStepInventoryLimit/at_limit (4.59s)
      --- PASS: TestConfinedInventoryEnforcesStepInventoryLimit/above_limit (2.93s)
  PASS
  ok  	jig/internal/runexport	7.922s
  ```

- The task 4.0 proof artifact's inaccurate "enforced in code" claim for `maxStepInventory` was corrected in the same commit to describe the actual (previously missing, now added) enforcement and cite the new test.
- Full regression re-run after the fix: `go test ./... -count=1` (all packages pass), `go test ./internal/runexport ./cmd/jig -race -count=1` (race-clean), `go vet ./...` (clean), `gofmt -l .` (clean).

The MEDIUM and LOW findings below (`TestRequirementCoverageIsComplete` weak enforcement, 64 KiB truncation-marker overshoot, tool-call IDs excluded from free-text replacement) were **not** re-verified as fixed — they remain open exactly as originally found, since remediation this cycle was scoped to the Gate A blocker only.

## 2) Coverage Matrix

### Functional Requirements

| Requirement | Status | Evidence |
| --- | --- | --- |
| FR-01 — CLI contract | Verified | `jig export --help` and `jig export` (no args) reproduced live; matches spec text exactly. `go test ./cmd/jig -run TestExport -v` passes. |
| FR-02 — Target resolution | Verified | Live run: destination collision correctly refused (exit 2, `"destination already exists"`); source bytes unchanged (sha256 identical before/after). `TestResolveRequestRefusesUnsafeTargetsWithoutWrites` passes. |
| FR-03 — Structural archive | Verified | Live export of a synthetic fixture produced `run.json`/`events.jsonl` with aliased steps, attempt numbers, and dependency edges. `TestStructuralArchiveMemberContract` passes. |
| FR-04 — Closed disclosure policy | Verified | Live disclosure scan (own fixture, independent of the implementer's) found no seeded run ID, workflow name, or secret in any member or in raw ZIP bytes. `TestClosedProjectionExcludesPrivateData` passes. |
| FR-05 — Identity and time | Verified | Live archive used `run-1`/`step-0001` aliases; `TestExportSanitizerIdentifierTokenBoundary` and `TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps` pass. |
| FR-06 — Publication and exits | Verified | Live run: stdout contained only the destination path; collision case exited 2 without touching the existing file; temp/publish behavior confirmed by code read of `archive.go`. `TestArchivePublicationNoOverwrite`, `TestExportDestinationCollisionRefusal` pass. |
| FR-07 — Explicit content mode | Verified | Live paired export: `transcript.jsonl` absent by default, present with `--include-text`; stderr carried the mandatory notice verbatim. `TestTextModeArchiveIncludesSanitizedTranscript` passes. |
| FR-08 — Included/excluded text | Verified | `internal/runexport/transcript.go` read directly: thinking blocks become omission markers; attachments/binary/session/`input.md` are never opened. `TestProjectTranscriptEntryOmitsThinkingAndUnknownBlocks` passes. |
| FR-09 — Sanitization | Verified | Live transcript member showed `AKIAFAKEVALIDATE1234` → `[REDACTED:aws-key]` and the run ID replaced by `run-1` in free prose, from a fixture the implementer never saw. `sentinel.DetectSecrets` is a genuine extraction (same regex/entropy rules as the pre-existing live guard, confirmed by reading `internal/sentinel/rules.go`), not a stub. |
| FR-10 — Omission/safe representation | Verified | `sanitize.go`'s `Sanitize` runs control-stripping → prior-marker removal → secret redaction → identifier replacement → UTF-8 normalization → truncation, in that literal order (read directly), satisfying "sanitize before truncate." `TestExportSanitizerTruncatesAfterSanitizing` passes. |
| FR-11 — Privacy accounting | Verified | Live `manifest.json` showed fixed-category counters (`replacements: {aws-key:1, token:1}`) with no matched text or mapping present. |
| FR-12 — Ownership lease | Verified | `scheduler.lock` was created as the only coordination-side-effect during a live export run; task 1.0's separate-process lease tests re-pass under `-race`. |
| FR-13 — Inactive/damaged states | Verified | Live export against a fixture with a deliberately invalid `workflow.json` produced `completeness: "partial"` with an explicit `corrupt_source` gap rather than silently treating it as absent. `TestDamagedRunExportMatrix` (5 sub-cases) passes. |
| FR-14 — Completeness | Verified | Same live run's manifest distinguished `partial` with a fixed reason code (`corrupt_source`) and source category (`workflow`), matching the spec's gap-code contract. |
| FR-15 — Confinement/concurrent changes | Verified | `source.go` read directly: `os.Root`-scoped opens, explicit symlink/non-regular rejection at every selected path, `Recheck` comparing identity/size/mtime before publication. `TestConfinedInventoryRejectsSelectedSymlink`, `TestConfinedInventoryRecheckDetectsSelectedChange` pass. |
| FR-16 — Resource bounds | Verified (after remediation) | `maxInputRecord`, `maxTotalInput`, `maxArchiveBytes` are genuinely wired and enforced (confirmed by reading `export.go:79`, `export.go:258`, `archive.go:85-90`, and by their passing unit tests). `maxStepInventory` was originally declared but unchecked; commit `6072222` added an `ErrOperational` refusal in `collectInventory` (`source.go`) when the discovered step count exceeds the limit, plus `TestConfinedInventoryEnforcesStepInventoryLimit` covering the exact 10,000/10,001 boundary — re-run independently and passing. |
| FR-17 — Documentation/verification | Verified | `docs/operations.md` "## Export a run" section read directly; covers CLI grammar, both modes, archive contract, limits, states, streams/exits. `README.md` links it. `go test ./... -count=1`, `-race`, `go vet ./...`, `gofmt -l .`, and `jig validate` on all 9 `.agents/jig/*.toml` all independently re-run and pass. |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Coding Standards | Verified | `gofmt -l .` produced no output; `go vet ./...` clean. Package boundaries match `docs/CONVENTIONS.md` (one concern per file: `aliases.go`, `archive.go`, `sanitize.go`, `journal.go`, `transcript.go`, `bounds.go` are each single-purpose). |
| Testing Patterns | Verified | Table-driven tests, `t.TempDir()`, synthetic-only fixtures with obvious `FAKE`/placeholder secret shapes confirmed by grep; separate-process helper tests for lock contention; `-race` run clean. |
| Quality Gates | Verified | `go test ./... -count=1` (all 28 packages pass), `go test ./internal/runexport ./cmd/jig -race -count=1` (race-clean), `go vet ./...`, `gofmt -l .` all independently re-run with matching results to the proof artifacts. |
| Documentation | Verified | `docs/operations.md`, `README.md`, `docs/TESTING.md`, and `docs/plans/open-goals.md` (A22 row) all updated and cross-linked; confirmed by direct read. |
| Engine/CLI boundary | Verified | `cmd/jig/export.go` is thin (flag parsing, exit mapping); all archive/sanitization/lock logic lives in `internal/runexport` and the pre-existing `internal/engine`/`internal/sentinel` seams it reuses, per `CLAUDE.md`'s "keep `cmd/jig` thin" rule. |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| Task 1.0 | Lease/confinement/acquisition tests | Verified | Already validated in a prior session; re-confirmed passing in this run's full `go test ./...`. |
| Task 2.0 | Structural archive + CLI proof (`23-proofs/23-task-02-proofs.md`) | Verified | Independently re-ran `jig export` against a fresh synthetic fixture; got the same four-member structural archive shape, alias-only content, and disclosure-scan result. |
| Task 3.0 | Sanitized text-mode proof (`23-task-03-proofs.md`) | Verified | Independently re-ran with `--include-text`; confirmed `AKIAFAKEVALIDATE1234` → `[REDACTED:aws-key]` and run-ID aliasing in a fixture the implementer never saw. Sanitizer code read directly and matches the claimed order of operations. |
| Task 4.0 | Damaged-run/bounds proof (`23-task-04-proofs.md`) | Verified (after remediation) | `TestDamagedRunExportMatrix` and cancellation/budget tests re-ran and pass. The proof's original claim that `maxStepInventory` was "enforced in code" was false at validation time; commit `6072222` fixed the enforcement, corrected the proof's claim, and added a passing boundary test. The 256 MiB `maxTotalInput`/`maxArchiveBytes` claims were always true (verified by code read). |
| Task 5.0 | Docs + e2e acceptance proof (`23-task-05-proofs.md`) | Verified | `TestExportEndToEnd`, `TestRequirementCoverageIsComplete` re-ran and pass; documentation diffs read directly and match the claims; A22 marked Done in `docs/plans/open-goals.md`. |

## 3) Validation Issues

| Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- |
| ~~HIGH~~ (RESOLVED) | FR-16's 10,000-step inventory limit was unenforced dead code at original validation time. Fixed in commit `6072222`: `collectInventory` (`source.go`) now aborts with `ErrOperational` once the discovered step count exceeds `maxStepInventory`, verified by the new `TestConfinedInventoryEnforcesStepInventoryLimit` boundary test (10,000 passes, 10,001 refuses) re-run independently in this validation pass. The task 4.0 proof artifact's claim was also corrected. | Originally: unbounded step-directory reads during export collection. Now resolved. | No further action required. |
| MEDIUM | `TestRequirementCoverageIsComplete` (`internal/runexport/requirements_test.go`) only asserts the `requirementCoverage` map has a non-empty test-name list per FR; it does not verify the named tests actually exist (its own comment admits this: "It does not (and cannot, from this package) verify the named tests still exist in `cmd/jig`"). This validation independently confirmed via `grep -rl "func <name>("` that all 29 referenced test names do exist and pass, so there is no live gap today — but the enforcement mechanism itself cannot catch a future rename/deletion of a `cmd/jig` test, only an `internal/runexport` one (and not even that, since it doesn't grep at all). | If a referenced test is renamed or deleted later, the traceability table can silently rot back into a documentation-only claim, which is the exact failure mode task 5.4 was meant to prevent. | Strengthen the test to actually verify each referenced name resolves to a real `func TestX(t *testing.T)` — e.g., shell out to `go test -list` for both packages, or maintain the map only for same-package tests and drop the `cmd/jig`-scoped entries (`TestExportUsageAndHelp`, `TestExportHelpDocumentsContract`, `TestExportFlagsAfterRunID`, `TestExportDestinationCollisionRefusal`, `TestExportEndToEnd`) from this package's enforcement, documenting that half as reviewed by hand. |
| LOW | The 64 KiB truncation marker (`…[truncated]`, `sanitize.go:185`) is appended *after* cutting to `maxRetainedText` bytes, so the final retained-text length can exceed 64 KiB by the marker's byte length. The spec says "capped at 64 KiB UTF-8 after sanitization" without explicitly stating whether the marker counts toward the cap. | Cosmetic/spec-ambiguity only — the marker itself carries no source content and cannot leak anything; retained payloads are at most a few bytes over the stated cap. | Either subtract the marker's length from `max` before cutting, or add a one-line comment on `truncateUTF8` noting the cap is on retained *content* bytes and the marker is additive, so a future reader doesn't "fix" this as a bug. |
| LOW | FR-09's literal text lists "known tool identifiers" among values the sanitizer must replace; the implementation (documented candidly in the task 3.0 proof's "Scope narrowing" section) aliases tool IDs only at the structured `tool_alias` field level and does not add raw tool-call IDs to the free-text identifier-replacement list. | If a tool-call ID literally appears inside retained free-text prose (unusual, since these are opaque provider-issued strings, but possible via prompt injection or an agent echoing an ID in a text block), it would not be replaced by the identifier pass — though it also isn't a "secret" by the sentinel detectors' definition, so this is a narrow residual-disclosure edge case already covered by the mode's documented "not a guarantee of anonymity" caveat. | Optional: add tool-call IDs observed during transcript projection to the same `idReplacement` list used for run/workflow/step IDs, scoped per-transcript the way tool aliases already are. Not required to un-block this validation given the existing residual-risk framing in FR-07/README, but worth tracking if the product's disclosure bar tightens. |

No CRITICAL issues were found. No GATE F (credential) issues were found — all proof-artifact secrets use conspicuous placeholder shapes (`AKIAFAKE...`, `prod-incident-482`, etc.).

## 4) Evidence Appendix

### Git commits analyzed

```
6072222 fix: enforce 10,000-step inventory limit (FR-16)          (task 4.0, remediation)
7eb7465 docs: publish jig export contract and close A22          (task 5.0)
5a2ade5 feat: jig export CLI with structural and sanitized-text run archives  (tasks 2.0-4.0)
7240d4d feat: add safe run export acquisition                     (task 1.0, prior session)
```

All files touched in `5a2ade5`/`7eb7465` map to entries in `23-tasks-run-share-export.md`'s "Relevant Files" table (`internal/runexport/*`, `internal/engine/resume.go`, `internal/sentinel/rules.go`, `cmd/jig/export*.go`, `cmd/jig/main.go`, `cmd/jig/ops.go`, `docs/operations.md`, `README.md`, `docs/TESTING.md`, `docs/plans/open-goals.md`). No unmapped core/source file changes were found — Gate D passes.

### Quality gates (re-run independently in this validation session)

```
go build ./...                                          -> exit 0, no output
go vet ./...                                             -> exit 0, no output
gofmt -l .                                               -> no output
go test ./... -count=1                                   -> all 28 packages pass (2 have no test files, expected)
go test ./internal/runexport ./cmd/jig -race -count=1    -> ok (both), race-clean
go run ./cmd/jig validate <each of 9 .agents/jig/*.toml> -> all "ok"
```

### Independent live CLI verification (fixture never seen by the implementing agent)

Fixture: run ID `validation-run-007`, workflow name `validation-fixture`, seeded secret `AKIAFAKEVALIDATE1234`, transcript text referencing both.

```
$ jig export validation-run-007 --root .jig --destination out.zip --include-text
notice: text mode includes best-effort sanitized conversation text; review before sharing further
notice: archive is partial: 1 evidence gap(s) recorded in manifest.json
/tmp/exportval/out.zip
$ echo $?
0

$ unzip -p out.zip transcript.jsonl
{"step_alias":"step-0001","seq":1,...,"role":"user","blocks":[{"type":"text",
 "text":"my secret key is [REDACTED:aws-key] do not leak run-1"}]}

$ unzip -p out.zip manifest.json
{
  "completeness": "partial",
  "gaps": [{"reason": "corrupt_source", "source": "workflow"}],
  "counters": {"replacements": {"aws-key": 1, "token": 1}}
}

# Disclosure scan across every member + raw ZIP bytes:
absent: AKIAFAKEVALIDATE1234
absent: validation-run-007
absent: validation-fixture

# Destination collision (second export attempt, same destination):
$ jig export validation-run-007 --root .jig --destination out.zip
error: run export: invalid request: destination already exists
$ echo $?
2

# Source preservation:
$ sha256sum .jig/runs/validation-run-007/{journal.jsonl,steps/step-alpha/transcript.jsonl,workflow.json}
# — identical before and after all export attempts.
```

### FR-16 code inspection (the original HIGH finding, now resolved)

Before remediation:

```
$ grep -n "maxStepInventory" internal/runexport/*.go
internal/runexport/bounds.go:16:	maxStepInventory = 10000     // step directories inspected
internal/runexport/transcript.go:28:// which is itself bounded by the file's line count under maxStepInventory

$ grep -n "maxStepInventory" internal/runexport/source.go internal/runexport/export.go
(no output — never referenced where step directories are actually inventoried)

$ grep -rn "maxStepInventory" internal/runexport/*_test.go
(no output — never tested)
```

After commit `6072222`:

```
$ grep -n "maxStepInventory" internal/runexport/source.go internal/runexport/export_test.go
internal/runexport/source.go:120:	if len(entries) > maxStepInventory {
internal/runexport/export_test.go: (TestConfinedInventoryEnforcesStepInventoryLimit, at/above-boundary sub-tests)

$ go test ./internal/runexport -run 'TestConfinedInventoryEnforcesStepInventoryLimit' -v
--- PASS: TestConfinedInventoryEnforcesStepInventoryLimit (7.52s)
    --- PASS: TestConfinedInventoryEnforcesStepInventoryLimit/at_limit (4.59s)
    --- PASS: TestConfinedInventoryEnforcesStepInventoryLimit/above_limit (2.93s)
PASS
```

### CLI discoverability

```
$ jig help | grep -A1 export
  export RUN_ID --destination PATH [--include-text]
                            export a local, sanitized diagnostic ZIP

$ jig badcommand
usage: jig <init|validate|run|status|logs|doctor|resume|reset|prune|export|notifications>
```

## Validation Completed: 2026-09-11 (session time)
## Validation Performed By: Claude Sonnet 5 (SDD Phase 4 validation fork)
