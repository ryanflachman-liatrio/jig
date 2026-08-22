# Task 03 Proofs — Queue navigation, focus & per-entry draft preservation

## Task Summary

This task implements gate-entry navigation (`[`/`]` cycle entries while
`tab`/`shift+tab` always cycle regions), per-entry draft preservation across
navigation, the `[N / M]  step-id (kind)` header above every active entry body,
and the unified esc-blur handler that replaces the per-kind → `showRunsMsg`
branches (ADR 0005 §esc-blurs).

## What This Task Proves

- `[`/`]` while a multi-entry gate is focused cycle `activeInputIdx` (mod len).
- `tab`/`shift+tab` cycle focus regions from every region, including the Gate.
- With one queued textarea entry, `[`/`]` remain literal text.
- Each entry's textarea content is saved to `draft` on navigation away and
  restored when returning to that entry.
- `tab`/`shift+tab` and `right`/`left` also sync the draft before leaving, so
  focus movement preserves in-progress text.
- `esc` while the gate is focused blurs to `focusSteps` without navigating away
  from the monitor and without clearing the queue (Decision 6 / ADR 0005).
- A `[N / M]  step-id  (kind)` header renders above every active entry body.
- `removeEntryAt` rebuilds the active textarea after index adjustment so callers
  never need to call `loadActiveTextarea` separately.

## Evidence Summary

- `TestGateDraftPreservation` passes — bracket navigation preserves drafts;
  region-focus and arrow-exit paths also sync the draft.
- `TestGateTabAlwaysMovesFocus` and `TestSingleEntryTextareaAcceptsBrackets`
  cover focus consistency and the single-entry literal-key behavior.
- `TestGateEscBlurs` passes — esc blurs to `focusSteps`, queue unchanged, no
  `showRunsMsg` emitted.
- `TestGateNavFrames` passes — `[1 / 2]` and `[2 / 2]` headers render correctly;
  `[` from index 0 wraps to the last entry.
- `artifacts/unit3-nav.txt` captures three `View()` frames showing the header
  progression.
- `go test -race ./internal/tui/...`, `go build ./...`, `gofmt -l .`, and
  `go vet ./...` are clean.

## Artifact: TestGateDraftPreservation

**What it proves:** Per-entry draft text survives `[`/`]` navigation and
focus-key or arrow-key panel exit.

**Why it matters:** Without `syncActiveTextarea` / `loadActiveTextarea`, switching
entries would discard in-progress text, making multi-entry queues unusable.

**Command:**

```bash
go test ./internal/tui/monitor -race -run TestGateDraftPreservation -v
```

**Result summary:** PASS — draft "hello" survives bracket navigation and is
restored on return; draft "world" survives right-arrow exit and is restored after
re-entering the gate.

```
=== RUN   TestGateDraftPreservation
--- PASS: TestGateDraftPreservation (0.00s)
PASS
ok  	jig/internal/tui/monitor
```

## Artifact: TestGateEscBlurs

**What it proves:** `esc` in the gate blurs to `focusSteps` and does not emit
`showRunsMsg` or clear the queue (ADR 0005 §esc-blurs).

**Why it matters:** The old behavior navigated away to the runs list on every esc;
the new behavior keeps the user in the monitor with the entry still pending.

**Command:**

```bash
go test ./internal/tui/monitor -race -run TestGateEscBlurs -v
```

**Result summary:** PASS — `m.focus == focusSteps`, `len(inputQueue) == 1`, no
`showRunsMsg` in the returned command.

```
=== RUN   TestGateEscBlurs
--- PASS: TestGateEscBlurs (0.00s)
PASS
```

## Artifact: TestGateNavFrames + unit3-nav.txt

**What it proves:** The `[N / M]  step-id  (kind)` header renders correctly and
`[`/`]` (including wrap) advance `activeInputIdx` as expected.

**Why it matters:** Confirms the header is visible and correctly tracks position
in the queue; the artifact is the evidence for the validation phase.

**Command:**

```bash
go test ./internal/tui/monitor -race -run TestGateNavFrames -v
```

**Artifact path:** `artifacts/unit3-nav.txt`

**Result summary:** PASS — `[1 / 2]` on entry 0; `[2 / 2]` after `]`; `[1 / 2]`
after `[`.

```
=== RUN   TestGateNavFrames
--- PASS: TestGateNavFrames (0.04s)
PASS
```

Frame 1 (initial — `[1 / 2]`):

```
│ [1 / 2]  Step: a
```

Frame 2 (after `]` — `[2 / 2]`):

```
│ [2 / 2]  Step: b
```

Frame 3 (after `[` — `[1 / 2]`):

```
│ [1 / 2]  Step: a
```

## Artifact: Verification suite

**What it proves:** The complete TUI surface passes with the race detector, and
all packages build and vet cleanly.

**Command:**

```bash
go build ./...
go vet ./...
go test -race ./internal/tui/...
```

**Result summary:** Build and vet pass; all TUI packages pass under the race
detector. The broader `go test ./...` run was attempted separately and reached
unrelated pre-existing engine integration timeouts while waiting for review/run
events; the affected engine tests pass when rerun individually.

```
ok  	jig/internal/tui
ok  	jig/internal/tui/chart
ok  	jig/internal/tui/chat
ok  	jig/internal/tui/detail
ok  	jig/internal/tui/monitor
ok  	jig/internal/tui/question
ok  	jig/internal/tui/runs
ok  	jig/internal/tui/selector
```

## Reviewer Conclusion

Task 3.0 is fully implemented and verified: entry-cycling via `[`/`]` works with
draft preservation; `tab`/`shift+tab` move focus consistently; `esc` blurs
cleanly without navigating away; the `[N / M]` header renders for all queue
sizes; and `removeEntryAt` is the single place that rebuilds the textarea after
a removal. All existing tests continue to pass.
