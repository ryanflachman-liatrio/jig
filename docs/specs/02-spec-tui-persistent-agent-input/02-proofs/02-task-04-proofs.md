# Task 04 Proofs — Per-kind entry rendering & response routing

## Task Summary

This task wires the active queue entry's `kind` into both rendering (`gateStrip`)
and key handling (`updateGate`), so each entry kind presents its own UI and
routes submits to the correct downstream message type. It also adds QuestionCancel
(`q` → "cancelled") and per-entry compose isolation for review entries.

## What This Task Proves

- Submitting an `InputRequest` entry emits `agentInputMsg` for that entry's
  `stepID` and shrinks the queue; the next entry auto-advances.
- Pressing `q` on a question entry delivers `agentQuestionResponseMsg{answer:"cancelled"}`
  without emitting `showRunsMsg` (user stays in Monitor).
- Composing a message on one review entry does not bleed `composing` state or
  draft into a second queued review entry.
- The gate strip never shows diff content (diff renders in the Transcript panel,
  Task 5); only verdict choices and `[m]` appear in the gate.
- Submit paths no longer clear `promptTextarea` by hand — `removeEntryAt` now
  owns the textarea lifecycle via `loadActiveTextarea()`.

## Evidence Summary

- `TestGateSubmitRouting` PASS — two InputRequest entries drain correctly in order.
- `TestQuestionCancel` PASS — `q` delivers `"cancelled"`, queue empties, no redirect.
- `TestReviewComposeIsolation` PASS — composing on entry 0 does not affect entry 1.
- `unit4-drain.txt` artifact shows `[1 / 2]` → `[1 / 1]` → empty placeholder.
- Full TUI suite passes with `-race`.

## Artifact: TestGateSubmitRouting

**What it proves:** Submitting the active InputRequest entry emits `agentInputMsg`
with the correct `stepID` and shrinks the queue; submitting the second entry
routes to the second `stepID`.

**Why it matters:** Core correctness of per-entry response routing — the whole
queue model collapses to a no-op if submits go to the wrong step.

**Command:**

```bash
go test ./internal/tui -run TestGateSubmitRouting -v
```

**Result summary:** PASS — entry "a" routed first, then entry "b", queue empty
after both submits.

```
=== RUN   TestGateSubmitRouting
--- PASS: TestGateSubmitRouting (0.00s)
PASS
ok  	jig/internal/tui	0.472s
```

## Artifact: TestQuestionCancel

**What it proves:** `q` on a question entry delivers `agentQuestionResponseMsg`
with `answer == "cancelled"`, removes the entry, and emits no `showRunsMsg`.

**Why it matters:** Without this, a user stuck on a question has no escape — the
only option would be to kill the process.

**Command:**

```bash
go test ./internal/tui -run TestQuestionCancel -v
```

**Result summary:** PASS — `"cancelled"` delivered, queue empty, no redirect.

```
=== RUN   TestQuestionCancel
--- PASS: TestQuestionCancel (0.00s)
PASS
ok  	jig/internal/tui	0.472s
```

## Artifact: TestReviewComposeIsolation

**What it proves:** Starting compose on review entry 0 does not set `composing`
or carry over `draft` to review entry 1 after navigating with `]`.

**Why it matters:** Per-entry `composing` is the key invariant that makes the queue
model safe — a single shared bool would corrupt the second entry's UI state.

**Command:**

```bash
go test ./internal/tui -run TestReviewComposeIsolation -v
```

**Result summary:** PASS — entry 1 has `composing == false` and empty `draft`
after navigating from composing entry 0.

```
=== RUN   TestReviewComposeIsolation
--- PASS: TestReviewComposeIsolation (0.00s)
PASS
ok  	jig/internal/tui	0.472s
```

## Artifact: unit4-drain.txt — Queue drain sequence

**What it proves:** The drain sequence correctly renders `[1 / 2]`, then `[1 / 1]`
after first submit, then the empty placeholder with `focus = focusSteps`.

**Why it matters:** The `[N / M]` counter and auto-advance are the visible feedback
that the queue is draining in order.

**Artifact path:** `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit4-drain.txt`

**Result summary:** Three frames showing `[1/2]` → `[1/1]` → empty placeholder.
Panel height is identical in all three frames (no layout shift).

## Artifact: Full TUI test suite with race detector

**What it proves:** All TUI tests pass with no data races after the Task 4 changes.

**Command:**

```bash
gofmt -l -w . && go vet ./... && go test ./internal/tui -race
```

**Result summary:** Clean format, no vet warnings, all tests pass.

```
ok  	jig/internal/tui	1.813s
```

## Reviewer Conclusion

Task 4 is complete: `gateStrip` and `updateGate` correctly switch on the active
entry's kind, submits route to the right step, question cancel is delivered, and
compose isolation holds across review entries. The proof artifacts — three passing
tests plus the drain-sequence screenshot — provide full traceability to the spec's
Unit 4 functional requirements.
