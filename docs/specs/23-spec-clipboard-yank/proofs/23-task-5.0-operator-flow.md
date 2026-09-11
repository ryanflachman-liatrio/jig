# 23-task-5.0-operator-flow.md — Proof for parent task 5.0

This artifact assembles the machine-checkable proofs for parent task 5.0
(final integration and documentation of `y` / `Y` clipboard copies in the
TUI). Per-parent-task evidence for tasks 1.0 – 4.0 lives in each parent's
own commit and test scope; this file references those without duplicating
them, and captures only the additional integration/documentation/quality
signals earned in task 5.

## What this file covers

- Task 5.1 acceptance evidence: the table-driven matrix / overlay / busy /
  root-integration tests.
- Task 5.2 acceptance evidence: `docs/clipboard.md` and the README link.
- Task 5.3 acceptance evidence: `TestClipboardDocumentationContract`.
- Task 5.4 acceptance evidence: full-suite / race / vet / build / gofmt /
  workflow-validate outputs, with any pre-existing failure named
  explicitly and separated from work owned by this spec.
- Task 5.5 acceptance evidence: what can and cannot be verified by an
  autonomous agent without a real interactive terminal.
- Task 5.6 acceptance evidence: this document, cross-linked to the
  parent-task proofs and to the tracking rows in
  [`docs/plans/open-goals.md`](../../../plans/open-goals.md).

## Task 5.1 — help/dispatch matrix, overlays, busy, integration

- `TestClipboardHelpDispatchMatrix` walks every enabled/disabled `y` /
  `Y` mapping across Runs, Monitor (messages, overview), and Review
  (source, preview, diff, composer). For each row it drives the
  minimum model, asserts the resulting `shared.ClipboardRequest`
  surface (or absence), and cross-checks the contextual help binding.
- `TestClipboardOverlayEditorConfirmationPriority` proves the help
  overlay, command palette, and delete-confirm modal each swallow
  `y` / `Y` before either surface can admit a request.
- `TestClipboardBusyRefusesOverlappingRequest` gates a slow loader and
  verifies a second request records the `another copy is in progress`
  notice without stealing the busy slot.
- `TestClipboardRootIntegration` admits a slow request on Runs,
  navigates to Monitor while the loader blocks, then releases and
  completes it — proving pending IDs survive navigation and the notice
  still names the original run-ID target.

Full log: [`23-task-5.0-tui-clipboard-tests.txt`](23-task-5.0-tui-clipboard-tests.txt).
Regenerate with:

```bash
go test ./internal/tui/... -run 'TestClipboard' -count=1 -v
```

Expected outcome: every test in the log ends with `--- PASS:`. No new
warnings.

## Task 5.2 — [`docs/clipboard.md`](../../../clipboard.md) + README link

- Introduces the operator-facing clipboard reference: mapping table
  (Runs / Monitor · messages, files, overview / Review · source, range,
  preview, diff / composer), source semantics, feedback strings, byte
  limits, sanitization contract, OSC52 terminal + tmux prerequisites,
  and troubleshooting.
- The README `Documentation` section now links to
  [`docs/clipboard.md`](../../../clipboard.md).
- [`docs/plans/open-goals.md`](../../../plans/open-goals.md) updates
  B1 (Clipboard yank / OSC52) and T2 (Copy / yank selected transcript
  item) to the actual implemented state and calls out the deferred
  Monitor file-line-selection follow-up.

## Task 5.3 — `TestClipboardDocumentationContract`

`internal/tui/clipboard_documentation_contract_test.go` pins the
documented mapping and limits against the code:

- Every `shared.ClipboardSurface*` enum value must appear in
  [`docs/clipboard.md`](../../../clipboard.md) as a bare word.
- Both byte limits (raw byte count and human unit) must match
  `ClipboardMaxPayloadBytes` and `ClipboardMaxTranscriptScanBytes`
  from [`internal/tui/shared/clipboard.go`](../../../../internal/tui/shared/clipboard.go).
- The inclusive-limit wording ("exactly the limit succeeds") is
  present so a future soft-cap misread cannot happen silently.
- The no-clipboard-read / no-helper promise (`never reads`, `xclip`,
  `pbcopy`, `OSC52`) is present so the sanitizer / root cannot
  silently regain a helper path.
- `docs/plans/open-goals.md` still references spec 23 for B1 and T2.
- The README still links [`docs/clipboard.md`](../../../clipboard.md).

Regenerate with:

```bash
go test ./internal/tui -run TestClipboardDocumentationContract -count=1 -v
```

## Task 5.4 — full suite, race, vet, build, gofmt, workflow validate

- `go test ./...` — [`23-task-5.0-full-test-suite.txt`](23-task-5.0-full-test-suite.txt).
- `go test ./internal/tui/... -race` — [`23-task-5.0-tui-race.txt`](23-task-5.0-tui-race.txt).
- `go vet ./...` — [`23-task-5.0-go-vet.txt`](23-task-5.0-go-vet.txt) (empty output ≡ success).
- `go build ./cmd/jig` — [`23-task-5.0-go-build.txt`](23-task-5.0-go-build.txt) (empty output ≡ success).
- `gofmt -l internal/tui/` returns no output on the current tree after
  the trailing formatting commit
  `chore(gofmt): reformat clipboard-yank changed files`.
- Workflow validation — [`23-task-5.0-workflow-validate.txt`](23-task-5.0-workflow-validate.txt).

### Outstanding failures explicitly separated

The full-suite log shows two packages that fail on this branch. Both
failures reproduce on `origin/main` when the same commands are run,
so they are pre-existing and unrelated to spec 23:

- `jig/internal/engine` — timeout-based integration tests
  (`TestReadOnlyStepReceivesRunExecutionView`,
  `TestRunSnapshotFileReviewAcceptance`,
  `TestScheduler_WorktreeBranchReuseAcrossRuns`, `TestResetLinearTip`,
  `TestResetFanOut`, `TestForEachReset_*`,
  `TestFinalMergeDiscardLeavesBase`). These fail with
  `timeout waiting for RunFinished` / `timeout waiting for ReviewRequest`
  when the engine test binary is under load. Running the same test set
  individually with a shorter timeout passes. This is an existing flake
  in the engine integration tests and is not touched by any change
  under `internal/tui/...`.
- `jig/internal/harness` — `TestTier2MixedACPHarnessRunKeepsStepWindowsIsolated`
  fails with connection-close noise on the ACP integration layer.
  Reproduced against `origin/main` with all clipboard changes stashed
  (see `git stash push` diagnostic run in the working session).

Neither of the two failing packages imports `internal/tui/...` or
`internal/tui/shared/clipboard.go`. Spec 23 does not modify code in
either package. Marking these as pre-existing is honest, not a claim
that they should be ignored on `main`.

## Task 5.5 — synthetic end-to-end paste walkthrough

**Status: partially complete.** Programmatic verification of every
copy surface up to and including the `tea.SetClipboard` command seam
is done in the unit and integration tests captured under this
spec. The additional acceptance criterion for 5.5 — visually
observing that pasting into another application produces exactly
the expected bytes — requires a real interactive terminal that can
be watched. That is out of scope for a cloud agent working in a
non-interactive VM.

The following programmatic evidence stands in for the observation:

- `internal/tui/shared/clipboard_test.go`'s
  `TestClipboardEligibilityAndLimits` walks every sanitizer
  boundary (empty, invalid UTF-8, NUL, ANSI CSI/OSC, exact limit,
  oversized, unicode-preserved, tabs+CRLF-preserved).
- `internal/tui/runs/clipboard_test.go`,
  `internal/tui/monitor/clipboard_test.go`,
  `internal/tui/monitor/review_clipboard_test.go`, and
  `internal/tui/review/clipboard_test.go` each assert exact payload
  bytes for their surface: run ID (no trailing newline), output file
  (CRLF preserved), transcript snapshot (every block header + JSON
  body in source order), transcript item (paired use+result), review
  source line / range / block / hunk / file diff / document
  (byte-identical to the immutable round content).
- `internal/tui/clipboard.go`'s `completeClipboard` calls
  `shared.DefaultClipboardCommand`, which is `tea.SetClipboard`.
  `TestClipboardBusyRefusesOverlappingRequest` and
  `TestClipboardRootIntegration` exercise the whole admit → loader →
  complete → emit path.

**What is not yet verified in this proof:** that a paste into a
downstream editor produces the same bytes on macOS Terminal.app,
iTerm2, kitty, Alacritty, tmux, and mosh under both dark and light
themes. That verification, when done, should be recorded here with:

- Exact terminal + tmux/screen configuration.
- The fixture used to generate each copy.
- The recorded expected bytes and the pasted bytes (side by side or
  as `diff -u` output).
- A screenshot or screencast if the OSC52 write is not visible in the
  paste target.

The `TestClipboardFixture` scaffold referenced in task 1.6 has not
been added yet; adding it is a follow-up if a future maintainer
wants to run the end-to-end walkthrough repeatedly.

## Task 5.6 — proof assembly

This document is the assembly. Parent-task proofs are captured in
their commit messages:

- Task 1.0 — `1d4f65e clipboard-yank: shared OSC52 flow, run ID, file,
  transcript, item copies`.
- Task 2.0 — same commit (whole-transcript export).
- Task 3.0 — same commit (per-item copy).
- Task 4.0 — `612cc3f clipboard-yank: review line, range, hunk, file,
  block, and document copies`.
- Task 5.0 — `2430a2d clipboard-yank: help/dispatch matrix,
  documentation contract, docs/clipboard.md` and this proof.

Cross-reference of proof text/captures against credentials or private
clipboard content: none of the fixtures use real secrets, and no
test payload contains data that resembles a token or private key.
The synthetic content in `internal/tui/monitor/monitor_test.go`,
`internal/tui/monitor/review_clipboard_test.go`, and
`internal/tui/clipboard_test.go` was written specifically for this
spec.

FR coverage cross-check (matches the mapping table in
[`23-tasks-clipboard-yank.md`](../23-tasks-clipboard-yank.md)):

| FR | Where verified |
|----|----------------|
| FR-1 (run ID) | `TestClipboardRunID`, `TestClipboardHelpDispatchMatrix/runs/*` |
| FR-2 (whole transcript) | `TestClipboardTranscriptSnapshot`, `TestClipboardTranscriptBoundsAndErrors` |
| FR-3 (file) | `TestClipboardFilePayload`, `TestClipboardFileSnapshot` |
| FR-4 (routing / OSC52 seam) | `TestClipboardRootIntegration`, `TestClipboardBusyRefusesOverlappingRequest` |
| FR-5 (eligibility / limits) | `TestClipboardEligibilityAndLimits` |
| FR-6 (busy / file-snapshot / review draft immutability) | `TestClipboardBusyRefusesOverlappingRequest`, review copy tests |
| FR-7 (item) | `TestClipboardItemPayloads`, `TestClipboardItemPageBoundary` |
| FR-8 / FR-9 (unknown / truncated / missing / stale) | transcript + item tests |
| FR-10 (view preservation) | `TestReviewWorkspaceCopy*ForwardsRequestWithoutDraft`, matrix / integration |
| FR-11 – FR-13 (review source / preview / diff) | review clipboard tests + matrix |
| FR-14 (focus / precedence / draft) | `TestClipboardOverlayEditorConfirmationPriority`, gate copy tests |
| FR-15 (availability / help) | `TestClipboardHelpDispatchMatrix` |
| FR-16 (documentation) | `TestClipboardDocumentationContract` and [`docs/clipboard.md`](../../../clipboard.md) |

Outstanding validation limitations recorded above under Task 5.4
(pre-existing engine and harness test flakiness) and Task 5.5
(interactive terminal paste observation not available to a
non-interactive agent).
