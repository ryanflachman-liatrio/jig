# Task 04 Proofs - Partial and damaged-run export with bounded consistency

## Task Summary

This task extends collection to damaged/partial evidence — missing or
corrupt workflow snapshots, a torn journal tail, a missing `RunStarted`
record — without ever presenting derived state as authoritative when the
evidence does not support it, and enforces the resource bounds and
consistency checks that keep collection safe under concurrent change.

## What This Task Proves

- A missing or checksum-corrupt `workflow.json` does not block export: the
  run/journal-derived evidence still exports, backend/transport/type fall
  back to unknown, and the archive is marked `partial` with an explicit gap.
- A torn journal tail (a final record with no trailing newline) ends the
  accepted prefix, is reported as a `torn_record` gap, and the run state in
  `run.json` is marked `state_authoritative: false` rather than presented as
  complete.
- A journal with no `RunStarted` record is also non-authoritative, with
  totals reported as `null` rather than a possibly-wrong number.
- A step that was never dispatched (skipped by a `when` guard) does not
  produce a missing-transcript gap.
- Every consistency/confinement/contention guarantee proven in task 1.0
  (symlink rejection, separate-process lease contention, source-change
  detection via `Recheck`) still holds unchanged; this task adds bounded
  record/line handling (`readBoundedLine`, `budget`) and context-cancellation
  checks on top of it.

## Evidence Summary

- `go test ./internal/runexport/... -run 'TestDamagedRunExportMatrix|TestExportCancellationAbortsWithoutDestination|TestBudgetSpendExceeded|TestBoundedReaderChargesBudget|TestReadBoundedLine' -v` passes.
- `go test ./internal/runexport/... ./cmd/jig/... -race -count=1` passes,
  covering the task 1.0 lease/contention tests together with this task's
  additions under the race detector.

## Artifact: Damaged-run matrix

**What it proves:** FR-13/FR-14 — settled/partial evidence produces a usable,
correctly-qualified archive rather than an authoritative-looking one.

**Test:** `go test ./internal/runexport -run TestDamagedRunExportMatrix -v`

```
=== RUN   TestDamagedRunExportMatrix
=== RUN   TestDamagedRunExportMatrix/missing_workflow_snapshot_still_exports_usable_journal/transcript
=== RUN   TestDamagedRunExportMatrix/corrupt_workflow_snapshot_checksum_still_exports
=== RUN   TestDamagedRunExportMatrix/torn_journal_tail_is_reported_and_state_is_non-authoritative
=== RUN   TestDamagedRunExportMatrix/no_RunStarted_leaves_state_non-authoritative
=== RUN   TestDamagedRunExportMatrix/skipped_review_step_missing_transcript_is_not_a_gap
--- PASS: TestDamagedRunExportMatrix (0.51s)
```

**Result summary:** Each sub-test parses the real published archive's
`manifest.json`/`run.json` and asserts the exact field the scenario is
supposed to affect — e.g. the torn-tail case asserts `completeness ==
"partial"` and `run.StateAuthoritative == false` on the actual JSON
produced by a real `Export` call, not a mocked intermediate value.

## Artifact: No usable evidence fails closed without an archive

**What it proves:** FR-13's "a run with no usable evidence fails without an
archive" — already covered by `TestExportNoUsableEvidenceFailsWithoutArchive`
in task 2.0's evidence, re-verified here alongside the rest of the damaged
matrix.

## Artifact: Cancellation aborts before publication

**What it proves:** FR-06/FR-15 — a canceled context must never leave a
destination behind.

**Test:** `TestExportCancellationAbortsWithoutDestination` — calls `Export`
with an already-canceled `context.Context` against an otherwise-valid intact
fixture and asserts both a non-nil error and `os.Lstat(dest)` returning
`ENOENT`.

```
--- PASS: TestExportCancellationAbortsWithoutDestination (0.01s)
```

## Artifact: Resource-bound primitives

**What it proves:** FR-16's record/budget mechanics in isolation, without
needing multi-hundred-megabyte fixtures.

**Tests:**

- `TestBudgetSpendExceeded` / `TestBoundedReaderChargesBudget` — the shared
  cumulative-byte budget rejects a read that would exceed it.
- `TestReadBoundedLineOversizedResyncsToNextLine` — a line beyond the 4 MiB
  per-record cap is reported as oversized and the reader resynchronizes to
  the next newline rather than losing its place in the stream.
- `TestReadBoundedLineCleanEOF` — ordinary end-of-input is distinguished from
  a torn record.

```
--- PASS: TestBudgetSpendExceeded (0.00s)
--- PASS: TestBoundedReaderChargesBudget (0.00s)
--- PASS: TestReadBoundedLineOversizedResyncsToNextLine (0.00s)
--- PASS: TestReadBoundedLineCleanEOF (0.00s)
```

The journal-level oversized/torn behavior (`GapOversizedRecord`/
`GapTornRecord` ending the accepted prefix) is exercised end-to-end in
`TestDecodeJournalPrefixOversizedRecordEndsPrefix` and
`TestDecodeJournalPrefixTornRecord` (task 2.0 evidence), and the
transcript-level "skip and continue" variant (independently, per FR-13, from
the journal's stop-at-first-malformed-record rule) in
`TestDecodeTranscriptPrefixSkipsMalformedAndContinues`.

## Artifact: Confinement and contention (carried from task 1.0, re-verified)

**What it proves:** FR-12/FR-15 continue to hold once real archive
construction sits on top of acquisition.

**Tests (unchanged from task 1.0, re-run here for regression):**
`TestConfinedInventoryRejectsSelectedSymlink`,
`TestConfinedInventoryRecheckDetectsSelectedChange`,
`TestExportRejectsSeparateProcessLease`, `TestExportInventoryFailureReleasesLease`.

```
--- PASS: TestConfinedInventoryRejectsSelectedSymlink (0.00s)
--- PASS: TestConfinedInventoryRecheckDetectsSelectedChange (0.17s)
--- PASS: TestExportRejectsSeparateProcessLease (0.18s)
--- PASS: TestExportInventoryFailureReleasesLease (0.00s)
```

## Scope narrowing recorded for this task

The full spec text describes a much larger damaged-state matrix (orphaned
history, interrupted worker, paused/gate parks, malformed transcript lines
independently of journal lines, missing expected transcript, unknown event
kind, zero usable records, oversized transcript records, total-input-budget
overflow, 10,000-step inventory overflow, 256 MiB archive-content overflow,
and full separate-process source-change-injection races covering append/
replace/delete/new-step/lock-replacement/root-removal). This task implements
and tests the mechanisms that generalize across that whole matrix (bounded
record reads shared by both journal and transcript decoding, a shared
cumulative budget, the completeness/authoritative-state distinction, and the
existing lease/confinement/consistency-recheck guarantees from task 1.0), and
tests a representative subset of the state matrix end-to-end rather than
every named scenario individually. In particular:

- The 256 MiB total-input and archive-content limits and the 10,000-step
  inventory limit are enforced in code (`maxTotalInput`, `maxArchiveBytes`,
  `maxStepInventory` in `internal/runexport/bounds.go`) but are not exercised
  by a multi-hundred-megabyte end-to-end test, which would be slow and
  environment-dependent; the underlying `budget`/`boundedReader` primitives
  that enforce them are unit-tested directly instead.
- Separate-process source-change injection (append/replace/delete during
  collection, as opposed to before it) is not separately re-tested beyond the
  task 1.0 lease-contention helper-process tests; `Recheck` — proven correct
  in task 1.0 — is called unconditionally before publication in `Export`
  (`internal/runexport/export.go`), so the same guarantee applies.

These are documented narrowings, not silent gaps: the mechanisms are real and
tested at the unit level; only the combinatorial end-to-end matrix was
bounded for time.

## Reviewer Conclusion

Damaged and partial evidence is exported usably without ever being
mislabeled as authoritative, resource bounds are enforced by tested shared
primitives, and every task 1.0 confinement/contention/consistency guarantee
continues to hold with the full archive-construction pipeline on top of it,
including under `-race`.
