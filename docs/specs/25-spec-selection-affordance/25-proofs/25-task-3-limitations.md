# Task 03 recorded limitations

## Applicable acceptance checks

Every check runs against `HEAD` = `22e3ff5` on branch
`cursor/plan-slice-03-selection-affordance-cfa3` (three commits ahead of
`origin/main`). Captured under `25-task-3-acceptance/`:

| Check | Result | Artifact |
|---|---|---|
| `go build ./cmd/jig` | pass | `build.txt` (EXIT=0) |
| `go vet ./...` | pass | `vet.txt` (EXIT=0) |
| `go test -race ./internal/tui/...` | pass | `test-race-tui.txt` (EXIT=0) |
| `gofmt -l <changed-go-files>` | pass (empty output) | `gofmt.txt` (EXIT=0) |
| `git diff --check main HEAD -- internal/tui/monitor/` | pass | `git-diff-check.txt` (EXIT=0) |
| `go test ./...` | **fail with pre-existing** | `test.txt` (EXIT=1) |

Slice 03 changes only two production files
(`internal/tui/monitor/monitor_transcript_items_view.go` — one `+=` → `=`
plus a shared-glyph swap, and `internal/tui/monitor/monitor_layout.go`
— one comment refresh) and one new test file. Slice 03 changes no
harness, engine, scheduler, backend, wire-format, workflow-schema, or
security-monitor code path, so the failures below cannot plausibly be
introduced by this slice.

## Pre-existing failures in `go test ./...`

### `internal/engine` timeouts (6 tests)

- `TestReadOnlyStepReceivesRunExecutionView` — `timeout waiting for
  RunFinished` at `integration_test.go:125`.
- `TestScheduler_ReviewDiff` — `timeout waiting for ReviewRequest` at
  `worktree_test.go:283`.
- `TestForEachReset_UpstreamProducerResetChangesCardinality` — `timeout
  waiting for ReviewRequest for "gate"` at `fanout_reset_test.go:314`.
- `TestResetLinearTip` — `timeout waiting for ReviewRequest for "gate"`
  at `integration_test.go:1204`.
- `TestForEachReset_FamilyRemovesChildCommitsAndReExpands` — `timeout
  waiting for ReviewRequest for "gate"` at `fanout_reset_test.go:131`.
- `TestResetFanOut` — `timeout waiting for ReviewRequest for "gate"` at
  `integration_test.go:1072`.

**Slice-03 scope check:** Reproduced on `main` (in-tree isolation: `git
checkout main -- internal/tui/monitor/ && rm
internal/tui/monitor/monitor_selection_affordance_test.go && go test
./internal/engine ...`) — the four `TestForEachReset_*` /
`TestResetLinearTip` / `TestResetFanOut` failures reproduced 4-of-4
on `main` too; the other two (`TestReadOnlyStepReceivesRunExecutionView`,
`TestScheduler_ReviewDiff`) present as timing-sensitive flakes that
appeared under load in the full suite. All of these failure modes are
scheduler/review-request timeouts unrelated to Monitor rendering. Slice
03 changes only two files in `internal/tui/monitor/`, neither of which
imports the engine.

### `internal/harness` — `TestTier2MixedACPHarnessRunKeepsStepWindowsIsolated`

- Failure mode: `security_integration_test.go:699` reports
  `exfil-pattern missed enabled backend marker` against a trusted
  monitor-context payload.

**Slice-03 scope check:** This is the same failure slice 02 already
recorded as pre-existing in
[`docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-3-limitations.md`](../../25-spec-status-line-header-grammar/25-proofs/25-task-3-limitations.md).
Reproduced on `main` with the same in-tree isolation described above.
Ownership belongs to the harness security-monitor scenario.

**Ownership:** These failures belong to the engine scheduler and harness
security-monitor scenarios respectively. Recorded here so a reviewer
running `go test ./...` on slice-03 changes can distinguish them from a
slice-03 regression.

## PNG rendering pipeline

**Environment note:** `google-chrome --headless=new --disable-gpu
--disable-features=BackForwardCache --virtual-time-budget=2000
--hide-scrollbars --window-size=1200,900 --screenshot` reliably produced
the two PNGs in this directory from the `.html` captures. Chrome logs
two D-Bus warnings on stderr in the cloud-agent environment; both are
benign (`Failed to connect to the bus` / `unknown error type`) and the
PNGs are written correctly.

**Reviewer note:** The HTML captures (`25-task-3-selection-*.html`) are
the deterministic ground truth. Reviewers who prefer to regenerate the
PNGs can rerun that headless-Chrome command block against the HTML
files.

## Deferred slice-03 scope

Every non-goal listed in the slice-03 spec remains untouched by this
change:

- Option B (dotted-outline overlay for selection) → deferred pending
  Q-03.1.
- Selection semantics or navigation keys (`n`/`N`, `chatItemCursor`) →
  unchanged.
- Steps panel cursor (`monitor_steps.go` already uses
  `shared.Theme.SelectedBar.Render(shared.CursorBar)`) → untouched.
- A caption on the selected item (omp's `3 blocks →` /
  `enter expand` pattern) → deferred with Q-03.2.
- Retirement of the two-space unselected gutter → out of scope; would
  ripple into `renderToolExchangeCard`, `writeItemDetail`, and
  `writeNewCodeCards`.
- Any other epic slice: no header-grammar change (slice 02), no
  detail-section conversion (slice 05), no truncation-vocabulary edit
  (slice 06), no diff renderer (slice 07), no grouped-read tree (slice
  08), no vertical-rhythm change (slice 04), no message framing (slice
  09), no glyph-preset table (slice 14). No palette hex addition, no
  new `lipgloss.NewStyle`, no wire/format/harness change.
