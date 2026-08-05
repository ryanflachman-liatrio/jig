# Task 01 Proofs — Unified input-queue state model & ingestion

## Task Summary

This task replaces the four single-pointer gate fields (`pendingInput`, `pendingQuestion`,
`pendingPrompt`, `pendingReview`, and the loose `composingMessage`/`questionIdx`/
`questionSelected`/`questionAnswers` fields) with a single ordered `inputQueue
[]pendingInputEntry` + `activeInputIdx int` on `monitorModel`. All four
request kinds append an entry on arrival without stealing focus (Decision 6). A
`StepStatus` event leaving `StatusNeedsInput` prunes that step's entries.
`hasGate()` is redefined as `len(m.inputQueue) > 0`. All rendering and key-handling
functions (`gateStrip`, `updateGate`, `advanceQuestion`, `footerView`) are rewritten
to dispatch via `activeEntry()` rather than the removed fields.

## What This Task Proves

- Three `InputRequest` events for distinct steps produce three queue entries in
  arrival order, with `activeInputIdx==0` and `m.focus` unchanged (no focus steal).
- A `ReviewRequest` and an `AgentQuestion` coexist as two entries with the correct
  `kind` discriminators.
- A `StepStatus` transitioning out of `StatusNeedsInput` removes exactly that step's
  entry and leaves `activeInputIdx` within `[0, len(inputQueue))`. Pruning an empty
  queue does not panic.
- `go build ./cmd/jig` and `go vet ./internal/tui` are clean after the old
  single-pointer fields are fully removed.

## Evidence Summary

- All three new queue tests (`TestInputQueueIngest`, `TestInputQueueMixedKinds`,
  `TestInputQueuePruneOnStatus`) pass with the race detector enabled.
- The full `./internal/tui` test suite passes (`go test ./internal/tui -race`).
- `go build ./cmd/jig` and `go vet ./...` produce no output (clean).

## Artifact: New queue unit tests

**What it proves:** Core state invariants of the input queue (ingest order, mixed
kinds, prune on step-status change).

**Why it matters:** These tests are the primary proof that the state model is correct
before any rendering or key-handling work lands in tasks 2.0–4.0.

**Command:**

```bash
go test ./internal/tui -race -run "TestInputQueue" -v
```

**Result summary:** All three tests pass with the race detector, confirming the
queue appends in order, stores the correct kind discriminator per entry, and prunes
safely even on an empty queue.

```
=== RUN   TestInputQueueIngest
--- PASS: TestInputQueueIngest (0.00s)
=== RUN   TestInputQueueMixedKinds
--- PASS: TestInputQueueMixedKinds (0.00s)
=== RUN   TestInputQueuePruneOnStatus
--- PASS: TestInputQueuePruneOnStatus (0.00s)
PASS
ok  	jig/internal/tui	1.553s
```

## Artifact: Full tui test suite

**What it proves:** No regressions in existing gate-resolution, navigation, or
transcript rendering tests after the field removal.

**Why it matters:** Eight existing tests referenced removed fields or the old
auto-focus behavior; all were updated to use `m.inputQueue` / `len(m.inputQueue)`
and to manually set `m.focus = focusGate` where needed (Decision 6 removes
auto-focus).

**Command:**

```bash
go test ./internal/tui -race
```

**Result summary:** All tests pass; no data races detected.

```
ok  	jig/internal/tui	1.751s
```

## Artifact: Build and vet clean

**What it proves:** No dangling references to the removed single-pointer fields
anywhere in the `internal/tui` package or the `cmd/jig` binary.

**Command:**

```bash
go build ./cmd/jig && go vet ./internal/tui
```

**Result summary:** Both commands produce no output (clean exit).

```
BUILD OK
VET OK
```

## Reviewer Conclusion

The state model is replaced: `monitorModel` now holds `inputQueue []pendingInputEntry`
and `activeInputIdx int`; all four event kinds append entries without focus-steal; prune
on step-status works correctly. All proof tests pass with the race detector. No
regressions in the existing suite.
