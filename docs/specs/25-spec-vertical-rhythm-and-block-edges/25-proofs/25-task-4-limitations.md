# Task 04 recorded limitations

## Applicable acceptance checks

Every check runs against `HEAD` = `b4f587e` on branch
`cursor/plan-slice-04-vertical-rhythm-9714` (four commits ahead of
`origin/main` = `6b583fa`, the same base slice 03 landed on).
Captured under `25-task-4-acceptance/`:

| Check | Result | Artifact |
|---|---|---|
| `go build ./cmd/jig` | pass | `build.txt` (EXIT=0) |
| `go vet ./...` | pass | `vet.txt` (EXIT=0) |
| `go test -race ./internal/tui/...` | pass | `test-race-tui.txt` (EXIT=0) |
| `gofmt -l <changed-go-files>` | pass (empty output) | `gofmt.txt` (EXIT=0) |
| `git diff --check main HEAD -- internal/tui/monitor/` | pass | `git-diff-check.txt` (EXIT=0) |
| `go test ./...` | **fail with pre-existing** (see below) | `test.txt` (EXIT=1) |

Slice 04 changes three production files
(`internal/tui/monitor/monitor_transcript.go` — adds
`isStructuralBlank` and `trimStructuralBlankEdges` and refreshes
`stripBlankEdges`'s doc comment; `internal/tui/monitor/monitor_transcript_items_view.go`
— refactors `itemTranscriptBody` and factors out `writeTranscriptItem`;
`internal/tui/monitor/monitor_transcript_items.go` — doc-comment update
on `itemSpacingBefore` only) plus two Monitor test files
(`monitor_transcript_test.go` and the new `monitor_vertical_rhythm_test.go`).
Slice 04 changes no harness, engine, scheduler, backend, wire-format,
workflow-schema, or security-monitor code path, so the failures below
cannot plausibly be introduced by this slice.

## Pre-existing failures in `go test ./...`

### `internal/engine` scheduler / reset family (all pre-existing)

- `TestForEachReset_UpstreamProducerResetChangesCardinality` — `timeout
  waiting for ReviewRequest for "gate"` at `fanout_reset_test.go:314`.
- `TestForEachReset_FamilyRemovesChildCommitsAndReExpands` — `timeout
  waiting for ReviewRequest for "gate"` at `fanout_reset_test.go:131`.
- `TestForEachReset_RejectsDirectChildReset` — `timeout waiting for
  ReviewRequest` at `fanout_reset_test.go`.
- `TestResetFanOut` — `timeout waiting for ReviewRequest for "gate"` at
  `integration_test.go:1072`.
- `TestResetLinearTip` — `timeout waiting for ReviewRequest for "gate"`
  at `integration_test.go:1204`.

**Slice-04 scope check:** All five failures reproduced on `main`
(`origin/main` = `6b583fa`, checked out into a temporary `git worktree`)
by running the same test subset in a fresh process. Recorded here as
pre-existing engine-scheduler timeouts unrelated to Monitor rendering.
This is the same family the slice-03 limitations note recorded on the
same base commit (see
[`docs/specs/25-spec-selection-affordance/25-proofs/25-task-3-limitations.md`](../../25-spec-selection-affordance/25-proofs/25-task-3-limitations.md)).

### `internal/harness` — `TestTier2MixedACPHarnessRunKeepsStepWindowsIsolated`

- Failure mode: `security_integration_test.go:699` reports
  `exfil-pattern missed enabled backend marker` against a trusted
  monitor-context payload.

**Slice-04 scope check:** Reproduced on `main` with the same in-tree
isolation described above. Ownership belongs to the harness
security-monitor scenario. Also the same failure slices 02 and 03
already recorded (see
[`docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-3-limitations.md`](../../25-spec-status-line-header-grammar/25-proofs/25-task-3-limitations.md)
and
[`docs/specs/25-spec-selection-affordance/25-proofs/25-task-3-limitations.md`](../../25-spec-selection-affordance/25-proofs/25-task-3-limitations.md)).

### Timing-sensitive flakes under full-suite load

- `TestReadOnlyStepReceivesRunExecutionView` — `timeout waiting for
  RunFinished` at `integration_test.go:125` (fails under full suite,
  passes in isolation and on `main`).
- `TestRunSnapshotFileReviewAcceptance` — timeout under full-suite
  load (passes in isolation and on `main`).
- `TestScheduler_WorktreeBranchReuseAcrossRuns` — timeout under
  full-suite load (passes in isolation and on `main`).

**Slice-04 scope check:** These three failures appear only in the
`go test ./...` capture (concurrent load across the whole tree) and
pass when run as a targeted subset both on this branch and on `main`.
The slice-03 limitations note recorded the same pattern
(`TestReadOnlyStepReceivesRunExecutionView` as one of "timing-sensitive
flakes that appeared under load in the full suite"). Slice 04 does not
touch the engine or scheduler code these tests exercise.

**Ownership:** These failures belong to the engine scheduler and
harness security-monitor scenarios respectively. Recorded here so a
reviewer running `go test ./...` on slice-04 changes can distinguish
them from a slice-04 regression.

## PNG rendering pipeline

**Environment note:** the slice-04 visual-proof capture
(`25-task-4-monitor-rhythm.ansi` / `.html`) is the deterministic
ground truth. Headless-Chrome PNG conversion runs outside the test
process; the same command block documented by slice 03 produces a PNG
from the `.html` capture in the cloud-agent environment:

```
google-chrome --headless=new --disable-gpu \
  --disable-features=BackForwardCache --virtual-time-budget=2000 \
  --hide-scrollbars --window-size=1200,900 --screenshot \
  25-task-4-monitor-rhythm.html
```

No PNG is committed to the slice-04 proof directory because it adds
no reviewer value the HTML capture does not already convey — the HTML
is deterministic and re-renders identically in any modern browser,
whereas the PNG is a snapshot of one browser's render of that HTML.
The baseline capture (`25-task-4-monitor-rhythm-baseline.ansi`) is
recorded ANSI-only for the same reason: reviewers who want to compare
row-by-row can `diff` the two ANSI files or the two notes files.

## Baseline capture provenance

`25-task-4-monitor-rhythm-baseline.ansi` and its companion
`-baseline-notes.txt` were produced by rendering the slice-04
`verticalRhythmVisualPage` fixture inside a `git worktree` pointed at
`origin/main` (= `6b583fa`), with only the new
`monitor_vertical_rhythm_test.go` file copied in so the visual proof
test could execute. The worktree was removed after capture; no
implementation code from `main` was modified and no baseline harness
was committed to this branch.

## Deferred slice-04 scope

Every non-goal listed in the slice-04 spec remains untouched by this
change:

- Slice 05 (tool detail sections) → not started; detail writers still
  render below the header card at their existing six- and four-space
  indents.
- Slice 09 (user-message bubble and lazy build) → FR-04.3 is proved
  here at the loop level with a synthetic tinted-padding fixture; the
  bubble itself is not introduced.
- Slice 12 (boundary banners) → the two-line coord gap is preserved
  verbatim so slice 12 has somewhere to render.
- Fully re-deriving `itemSpacingBefore` from rendered output (Q-04.1)
  → deferred; the kind-based table is still correct for every current
  item kind and the zero-height guard fixes the only mismatch it
  produced.
- Card-side "structural padding" marker (Q-04.3) → deferred; the
  raw-bytes trim is sufficient for slice 09's tinted bubble.
- Unifying `stripBlankEdges` and `trimStructuralBlankEdges` (Q-04.4)
  → deferred; the two semantics differ as long as any code path
  routes Glamour output outside a card body.

No palette hex addition, no new `lipgloss.NewStyle`, no wire/format/
harness change, no engine or scheduler change, no card-primitive
change, no header-grammar change, no selection-affordance change.
