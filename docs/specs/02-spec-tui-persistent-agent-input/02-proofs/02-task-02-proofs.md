# Task 02 Proofs — Persistent, fixed-height gate panel & empty-state placeholder

## Task Summary

This task makes the gate strip always render — even when no agent input is pending —
at a constant derived height so the Steps and Transcript panels never resize when
input arrives or departs. It also shows a "No pending agent inputs" placeholder and
keeps the gate border blurred (unfocusable) while the queue is empty.

## What This Task Proves

- The gate panel renders at a fixed height whether the input queue is empty or not.
- The Steps and Transcript panel heights are identical before and after an
  `InputRequest` arrives (no layout shift, Spec Unit 2 / Success Metric 4).
- With an empty queue, `cycleFocus(+1)` from `focusTranscript` wraps to `focusSteps`
  without landing on `focusGate` (the empty gate is inert, not focusable via tab).
- `gateBodyHeight()` derives its return value from named constants
  (`gateTextareaRows`, `gateHeaderRows`, `maxReviewChoices`) — no magic numbers.

## Evidence Summary

- `TestGateFixedHeight` passes: gate height is the same (10 lines) with an empty
  queue and after an `InputRequest` arrives; panel heights are equal in both states.
- `TestCycleFocusSkipsEmptyGate` passes: empty gate is skipped by `cycleFocus`.
- `artifacts/unit2-empty-strip.txt` shows the "Agent input" panel with the
  placeholder at the bottom of the layout.
- `go build ./cmd/jig` and `go vet ./internal/tui` are clean.
- Full `go test ./internal/tui -race` passes with no regressions.

---

## Artifact: TestGateFixedHeight

**What it proves:** The gate strip height and the main panel heights are unchanged
before and after an `InputRequest` transitions the gate from empty-placeholder to
an active entry.

**Why it matters:** This is the core Unit 2 invariant — the layout must not shift
when a step becomes blocked on human input.

**Command:**

```bash
go test ./internal/tui -race -run TestGateFixedHeight -v
```

**Result summary:** PASS — gate height remained 10 before and after arrival;
`panelH` was identical in both states.

```
=== RUN   TestGateFixedHeight
--- PASS: TestGateFixedHeight (0.02s)
PASS
ok      jig/internal/tui        1.584s
```

---

## Artifact: TestCycleFocusSkipsEmptyGate

**What it proves:** `cycleFocus(+1)` from `focusTranscript` with an empty queue
returns `focusSteps`, not `focusGate`.

**Why it matters:** The spec says an empty gate must be inert — the user should
not be able to tab into it when no input is pending.

**Command:**

```bash
go test ./internal/tui -race -run TestCycleFocusSkipsEmptyGate -v
```

**Result summary:** PASS.

```
=== RUN   TestCycleFocusSkipsEmptyGate
--- PASS: TestCycleFocusSkipsEmptyGate (0.00s)
PASS
ok      jig/internal/tui        1.584s
```

---

## Artifact: Empty-strip View() capture

**What it proves:** The gate panel renders with the "No pending agent inputs"
placeholder, fills the fixed panel height, and has a blurred (dim) border matching
the non-focused state.

**Why it matters:** Verifies the visual presentation of the empty-state without
running a real terminal.

**Artifact path:** `artifacts/unit2-empty-strip.txt`

**Result summary:** The "Agent input" panel appears at the bottom of the layout
with the placeholder text. The panel height matches the active-entry height
(10 lines total), confirming no layout shift.

```
╭─ Steps ──────────────────────╮╭─ a ──────────────────────────────────────────╮
│ ▌ ○  a   pending           — ││   ○  pending                                 │
│   ○  b   pending           — ││                                              │
│   ○  c   pending           — ││   transcript unavailable (persistence off)   │
...
╰──────────────────────────────╯╰──────────────────────────────────────────────╯
╭─ Agent input ────────────────────────────────────────────────────────────────╮
│                                                                              │
│   No pending agent inputs                                                    │
│                                                                              │
│    ... (padding to fixed height) ...                                         │
╰──────────────────────────────────────────────────────────────────────────────╯
  running  ·  tab/←/→ focus  •  j/k select  •  esc runs list  •  ctrl+c quit
```

---

## Artifact: Full regression suite

**What it proves:** No existing tests regressed.

**Command:**

```bash
gofmt -l -w . && go vet ./... && go test ./internal/tui -race
```

**Result summary:** `ok  jig/internal/tui` — all tests pass, race detector clean.

---

## Reviewer Conclusion

The gate panel now always renders at `gateBodyHeight() + vFrame = 10` lines.
Layout stability is proven by the fixed-height test: panel heights are byte-equal
before and after gate activation. The empty-gate inertness is proven by the
cycleFocus test. The `gateBodyHeight()` derivation uses only named constants,
satisfying the "no magic-number heights" repository standard.
