# Task 02 Proofs - Running-step thinking pulse

## Task Summary

This task replaces the absence of any running-step indicator with a
fixed-width animated pulse on the currently running step's active (trailing)
thinking item. The pulse is quantized to the existing 100ms Monitor frame
loop with no second ticker, and the persistent "reasoning" text label always
accompanies the glyph so the running state is never conveyed by animation
alone.

## What This Task Proves

- Every frame in both the default and ASCII-fallback pulse glyph sets is
  exactly one visible cell wide, so nothing shifts as the pulse animates
  (FR-10.5).
- The pulse frame selected for a given timestamp is a pure, deterministic
  function of that timestamp — no package-level counter or second ticker
  (quantization requirement, FR-10.4).
- The running step's trailing thinking item actually animates across
  `TickMsg`s once `TickMsg` is wired to dirty the Transcript panel only when
  a pulse is active (FR-10.4/FR-10.6).
- A settled thinking item (step not running, or not the trailing item) always
  renders the plain, non-animated `◇ reasoning` label and never dirties the
  Transcript panel on tick (FR-10.7).
- The ASCII-fallback glyph set is non-empty and single-cell at every index
  (FR-10.8).

## Evidence Summary

- Six focused tests across `monitor_transcript_pulse_test.go` and
  `monitor_transcript_thinking_test.go` cover FR-10.4 through FR-10.8, and all
  pass.
- `go vet` reports no issues.
- The full repository test suite (`go test ./...`) passes with these changes
  in place.
- `gofmt -l` reports no diffs on any changed file.

## Artifact: Focused pulse and running/settled tests

**What it proves:** FR-10.5 (equal-width frames in both glyph sets),
FR-10.8 (non-empty ASCII fallback), the pure-timestamp quantization
requirement, FR-10.4 (the running trailing item's glyph advances across
ticks), and FR-10.7 (a settled item never animates and never triggers a
Transcript-panel repaint).

**Command:**

```bash
go test ./internal/tui/monitor/... -run "Pulse|Thinking" -v
```

**Result summary:** All six pulse/thinking tests pass, including the two
model-level tests that drive real `TickMsg`s through `Update` (matching
`docs/TESTING.md`'s "drive `tea.Msg` through `Update`" guidance rather than
asserting on the pure helper alone).

```
=== RUN   TestPulseFramesAreSingleCell
=== RUN   TestPulseFramesAreSingleCell/default
=== RUN   TestPulseFramesAreSingleCell/ascii
--- PASS: TestPulseFramesAreSingleCell (0.00s)
    --- PASS: TestPulseFramesAreSingleCell/default (0.00s)
    --- PASS: TestPulseFramesAreSingleCell/ascii (0.00s)
=== RUN   TestPulseFramesASCIINonEmpty
--- PASS: TestPulseFramesASCIINonEmpty (0.00s)
=== RUN   TestPulseFrameIsPureFunctionOfTimestamp
--- PASS: TestPulseFrameIsPureFunctionOfTimestamp (0.00s)
=== RUN   TestPulseFrameEmptySetReturnsEmpty
--- PASS: TestPulseFrameEmptySetReturnsEmpty (0.00s)
=== RUN   TestThinkingPulseAnimatesForRunningTrailingItem
--- PASS: TestThinkingPulseAnimatesForRunningTrailingItem (0.02s)
=== RUN   TestThinkingSettledItemNeverAnimates
--- PASS: TestThinkingSettledItemNeverAnimates (0.01s)
PASS
ok  	jig/internal/tui/monitor	0.532s
```

## Artifact: No regression across the full test suite

**What it proves:** Wiring `TickMsg` to conditionally dirty the Transcript
panel, and adding the `running` field/trailing derivation to
`buildTranscriptItems`, did not break any existing Monitor or repository
behavior.

**Command:**

```bash
go build ./cmd/jig && go test ./... && go vet ./...
```

**Result summary:** Every package passes, including `internal/tui/monitor`
(owns all changed/new code and tests) and `internal/tui/shared` (owns the new
glyph vars).

```
ok  	jig/cmd/jig	1.577s
...
ok  	jig/internal/tui/monitor	3.331s
...
ok  	jig/internal/tui/shared	4.169s
...
ok  	jig/internal/workflow	(cached)
```

`go vet ./...` produced no output.

## Artifact: Formatting check

**What it proves:** Only intentionally changed files were touched, and they
are `gofmt`-clean.

**Command:**

```bash
gofmt -l internal/tui/monitor/*.go internal/tui/shared/*.go
```

**Result summary:** No output — every file in both packages is
`gofmt`-formatted.

## Reviewer Conclusion

A running step's active thinking item now visibly pulses at the existing
100ms cadence with an equal-width glyph and a persistent "reasoning" text
label at every frame, and the pulse stops the instant the step is no longer
running. The Transcript panel is dirtied on tick only while a pulse is
actually active, avoiding a blanket per-tick rerender. Together with Task 1.0,
FR-10.1 through FR-10.8 all have deterministic test evidence, and the full
test suite/`go vet` confirm no regression.
