# Task 03 Proofs — Queue navigation, focus & per-entry draft preservation

## Task Summary

This task implements gate-entry navigation (`tab`/`shift+tab` cycle entries, not
regions), per-entry draft preservation across navigation, the `[N / M]  step-id
(kind)` header above every active entry body, and the unified esc-blur handler
that replaces the per-kind → `showRunsMsg` branches (ADR 0005 §esc-blurs).

## What This Task Proves

- `tab`/`shift+tab` while the gate is focused cycle `activeInputIdx` (mod len) and
  do not cycle focus regions — the gate stays focused.
- Each entry's textarea content is saved to `draft` on navigation away and
  restored when returning to that entry.
- `right`/`left` (panel exit) also sync the draft before leaving, so arrow-exit
  preserves in-progress text the same way tab does.
- `esc` while the gate is focused blurs to `focusSteps` without navigating away
  from the monitor and without clearing the queue (Decision 6 / ADR 0005).
- A `[N / M]  step-id  (kind)` header renders above every active entry body.
- `removeEntryAt` rebuilds the active textarea after index adjustment so callers
  never need to call `loadActiveTextarea` separately.

## Evidence Summary

- `TestGateDraftPreservation` passes — tab/shift+tab preserve drafts across
  navigation; arrow-exit (right key) also syncs the draft (task 3.8).
- `TestGateEscBlurs` passes — esc blurs to `focusSteps`, queue unchanged, no
  `showRunsMsg` emitted.
- `TestGateNavFrames` passes — `[1 / 2]` and `[2 / 2]` headers render correctly;
  shift+tab from index 0 wraps to the last entry.
- `artifacts/unit3-nav.txt` captures three `View()` frames showing the header
  progression.
- `go test ./... -race` is clean; `gofmt` and `go vet ./internal/tui` are clean.

## Artifact: TestGateDraftPreservation

**What it proves:** Per-entry draft text survives `tab`/`shift+tab` navigation and
arrow-key panel exit (task 3.8 variant).

**Why it matters:** Without `syncActiveTextarea` / `loadActiveTextarea`, switching
entries would discard in-progress text, making multi-entry queues unusable.

**Command:**

```bash
go test ./internal/tui -race -run TestGateDraftPreservation -v
```

**Result summary:** PASS — draft "hello" survives tab-away and is restored on
return; draft "world" survives right-arrow exit and is restored after re-entering
the gate.

```
=== RUN   TestGateDraftPreservation
--- PASS: TestGateDraftPreservation (0.00s)
PASS
ok  	jig/internal/tui	1.560s
```

## Artifact: TestGateEscBlurs

**What it proves:** `esc` in the gate blurs to `focusSteps` and does not emit
`showRunsMsg` or clear the queue (ADR 0005 §esc-blurs).

**Why it matters:** The old behavior navigated away to the runs list on every esc;
the new behavior keeps the user in the monitor with the entry still pending.

**Command:**

```bash
go test ./internal/tui -race -run TestGateEscBlurs -v
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
`tab`/`shift+tab` (including wrap) advance `activeInputIdx` as expected.

**Why it matters:** Confirms the header is visible and correctly tracks position
in the queue; the artifact is the evidence for the validation phase.

**Command:**

```bash
go test ./internal/tui -race -run TestGateNavFrames -v
```

**Artifact path:** `artifacts/unit3-nav.txt`

**Result summary:** PASS — `[1 / 2]` on entry 0; `[2 / 2]` after tab; `[2 / 2]`
after `shift+tab` wraps from index 0.

```
=== RUN   TestGateNavFrames
--- PASS: TestGateNavFrames (0.04s)
PASS
```

Frame 1 (initial — `[1 / 2]`):

```
│ [1 / 2]  a  (input)                                                          │
```

Frame 2 (after tab — `[2 / 2]`):

```
│ [2 / 2]  b  (input)                                                          │
```

Frame 3 (shift+tab from `[1 / 2]` wraps to `[2 / 2]`):

```
│ [2 / 2]  b  (input)                                                          │
```

## Artifact: Full test suite

**What it proves:** No regressions in the TUI package or any other package.

**Command:**

```bash
go test ./... && go test ./internal/tui -race
```

**Result summary:** All packages pass; race detector clean.

```
ok  	jig/internal/datastore
ok  	jig/internal/engine
ok  	jig/internal/runner
ok  	jig/internal/transcript
ok  	jig/internal/tui
ok  	jig/internal/workflow
```

## Reviewer Conclusion

Task 3.0 is fully implemented and verified: entry-cycling via `tab`/`shift+tab`
works with draft preservation (including the arrow-exit variant); `esc` blurs
cleanly without navigating away; the `[N / M]` header renders for all queue
sizes; and `removeEntryAt` is the single place that rebuilds the textarea after a
removal. All existing tests continue to pass.
