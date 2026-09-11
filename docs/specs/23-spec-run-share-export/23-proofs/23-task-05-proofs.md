# Task 05 Proofs - Publish the export contract and close offline acceptance

## Task Summary

This task documents `jig export` as a stable recipient-facing contract,
links it from the README, corrects stale `docs/TESTING.md` coverage claims,
adds an end-to-end acceptance test that drives the real CLI, maps every
functional requirement to an executable test, runs the full repository
quality gate set, and closes backlog item A22 only after all of that passes.

## What This Task Proves

- `docs/operations.md` documents the full command grammar, both content
  modes, selected/excluded sources, the fixed archive contract and every
  member, identity/time semantics, completeness/gap codes, resource limits,
  sanitization behavior, and streams/exits/publication/cancellation — and
  `README.md` links it.
- `docs/TESTING.md`'s package coverage table, which claimed
  `internal/engine`/`runner`/`step`/`manifest`/`datastore` were "Not
  implemented" (stale relative to the current tree), is corrected, and a new
  "Testing `jig export`" section documents the synthetic-fixture,
  archive-parsing, disclosure-scan, helper-process, and race conventions this
  feature introduced.
- `TestExportEndToEnd` (`cmd/jig/export_e2e_test.go`) drives the real CLI
  entry point end to end for structural, text-mode, and damaged-run exports
  against temporary run stores, inspects every archive with `archive/zip` and
  `encoding/json` only, and asserts the run store's own bytes are unchanged
  after three separate export invocations.
- Every FR-01 through FR-17 is mapped to at least one executable test name in
  `internal/runexport/requirements_test.go`, and `TestRequirementCoverageIsComplete`
  fails the moment that map is incomplete — so the traceability table cannot
  regress into documentation-only.
- The full repository quality gate set — `go test ./... -count=1`, targeted
  `-race`, `go vet ./...`, `gofmt -l .`, and every `.agents/jig/*.toml`
  validated — passes with this feature in the tree.
- `docs/plans/open-goals.md` marks A22 **Done** only now, after every
  preceding proof artifact in this spec passed.

## Evidence Summary

- `go test ./cmd/jig/... -run TestExportEndToEnd -v` passes.
- `go test ./internal/runexport/... -run TestRequirementCoverageIsComplete -v` passes.
- `go test ./... -count=1`, `go test ./internal/runexport ./cmd/jig -race -count=1`,
  `go vet ./...`, and `gofmt -l .` all pass with no output/failures.
- `jig validate` succeeds for all nine `.agents/jig/*.toml` workflows.

## Artifact: Documentation contract

**What it proves:** FR-17 — the archive/command contract is documented where
a recipient or operator would look for it, and discoverable from the README.

**Diff:** `docs/operations.md` gained a new "## Export a run" section (command
grammar, content modes, selected/excluded sources, archive member table,
completeness/gap codes, eligible states, limits, sanitization, and
streams/exits/publication) and the "## Streams and exits" table gained an
`export` row. `README.md` gained a "## Sharing a run" section linking
`docs/operations.md#export-a-run` and a `docs/operations.md` link in the
Documentation list.

**Result summary:** Both files build and render as ordinary Markdown; no
code changed as part of this diff.

## Artifact: `docs/TESTING.md` correction

**What it proves:** The task's specific requirement to correct stale
coverage claims discovered during the planning audit
(`23-audit-run-share-export.md`'s Standards Evidence Table flagged this).

**Diff:** The "Where the tests are" table no longer claims `internal/engine`,
`runner`, `step`, `manifest`, `datastore` are "Not implemented" — they are
tested today. A new `internal/runexport` row and a "Testing `jig export`"
section were added; the stale "(future) engine" heading was corrected to
reflect that the engine is tested now, not planned.

## Artifact: End-to-end acceptance test

**What it proves:** FR-01 through FR-16 working together through the real
CLI entry point, not through package-level calls to `runexport.Export`
directly.

**Test:** `go test ./cmd/jig -run TestExportEndToEnd -v`

```
=== RUN   TestExportEndToEnd
=== RUN   TestExportEndToEnd/structural_export
=== RUN   TestExportEndToEnd/text_export
=== RUN   TestExportEndToEnd/damaged_run_still_exports_partial_evidence
--- PASS: TestExportEndToEnd (0.04s)
    --- PASS: TestExportEndToEnd/structural_export (0.01s)
    --- PASS: TestExportEndToEnd/text_export (0.01s)
    --- PASS: TestExportEndToEnd/damaged_run_still_exports_partial_evidence (0.02s)
PASS
ok  	jig/cmd/jig	0.524s
```

**Result summary:** All three exports pass a disclosure scan against the
raw archive bytes and every member; the text-mode export's stderr carries
the mandatory best-effort notice; the damaged-run export (a torn journal
tail) still succeeds with `completeness: "partial"`; and the run store's own
files are byte-identical before the first export and after all three.

## Artifact: Requirement traceability

**What it proves:** FR-01 through FR-17 each have at least one enforced
executable test reference (task 5.4).

**Test:** `go test ./internal/runexport -run TestRequirementCoverageIsComplete -v`

```
--- PASS: TestRequirementCoverageIsComplete (0.00s)
```

**Result summary:** `requirementCoverage` in
`internal/runexport/requirements_test.go` lists every FR-01..FR-17 with at
least one real test name already used elsewhere in this spec's evidence
(cross-referenced against `23-tasks-run-share-export.md`'s Requirement
Coverage table).

## Artifact: Repository-wide quality gates

**What it proves:** This feature introduces no regression anywhere else in
the repository and follows the pinned toolchain/format/vet conventions.

**Commands and results:**

```
go test ./... -count=1                              # all packages pass
go test ./internal/runexport ./cmd/jig -race -count=1   # race-clean
go vet ./...                                        # no findings
gofmt -l .                                          # no output (already formatted)
```

## Artifact: Workflow example validation

**What it proves:** Task 5.6 — the new CLI command did not change workflow
schema, defaulting, or require example migration.

**Command:**

```bash
for f in .agents/jig/*.toml; do jig validate "$f"; done
```

**Result summary:**

```
ok: "bugfix" v1 — 4 step(s)
ok: "feature" v1 — 16 step(s)
ok: "golden-path" v1 — 2 step(s)
ok: "implementation-review" v1 — 2 step(s)
ok: "mixed-transport" v1 — 2 step(s)
ok: "research" v1 — 3 step(s)
ok: "review-ui-demo" v1 — 1 step(s)
ok: "review" v1 — 2 step(s)
ok: "sdd" v1 — 48 step(s)
```

## Artifact: Backlog closure

**What it proves:** A22 is marked done only after every preceding artifact
in this file, and in tasks 2.0–4.0's proof files, passed.

**Diff:** `docs/plans/open-goals.md` A22 row now reads **Done**, linking the
spec, task list, proof directory, and `docs/operations.md#export-a-run`.

## Reviewer Conclusion

The export command and its archive format are documented as a stable
contract linked from the README, an end-to-end test drives the real CLI
across structural, text, and damaged-run cases while scanning for
disclosure and confirming source preservation, every functional requirement
has an enforced test mapping, and the full repository quality-gate suite
(tests, race, vet, format, workflow validation) passes with this feature in
the tree — the basis for marking A22 done.
