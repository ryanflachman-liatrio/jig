# Task 2.0 Proofs — StepsReset Audit Event and Generation Provenance

## Task Summary

This task proves that the data-model plumbing needed by Task 3.0's reset
execution is fully in place: `Generation` provenance in `step.State` and
`transcript.Entry`, the `StepsReset` audit event in the journal codec, and the
`── re-run N ──` TUI separator in `chatBody()`.

## What This Task Proves

- `step.State` carries a `Generation int` field for tracking manual re-runs.
- `transcript.Entry` carries a `Generation int` field (JSON `"gen"`) that
  round-trips through the writer and reader correctly.
- `StepStatus` events carry `Generation` so the TUI receives it on every transition.
- `StepsReset{RunID, Target, Closure, RewindTo}` encodes as kind `"steps_reset"`
  and round-trips through `MarshalEnvelope` / `UnmarshalEnvelope` without loss.
- An unknown event kind returns `(env, nil, nil)` — no error — so journals from
  newer jig versions replay gracefully in older ones.
- A journal containing `steps_reset` + subsequent `step_status` events replays
  all events correctly with no entries silently dropped.
- `chatBody()` emits `── re-run N ──` separators when `Generation` increases,
  matching the existing `── iteration ──` / `── retry ──` separator pattern.

## Evidence Summary

All three targeted tests pass. The `StepsReset` round-trip, post-reset replay,
and `Generation` transcript-field tests cover the four requirements above.

## Artifact: TestStepsResetRoundTrip

**What it proves:** `StepsReset` marshals to `kind: "steps_reset"` and all
fields (RunID, Target, Closure slice, RewindTo SHA) survive the codec round-trip.

**Command:** `go test -v -count=1 -run TestStepsResetRoundTrip ./internal/engine/`

**Result summary:** PASS — all fields verified including the Closure string slice.

## Artifact: TestReplayPostReset

**What it proves:** A journal containing a `steps_reset` entry followed by
`step_status` events with `Generation: 1` replays all 10 events in order;
`StepsReset` fields are intact and the post-reset `StepStatus` carries the
correct `Generation`.

**Command:** `go test -v -count=1 -run TestReplayPostReset ./internal/engine/`

**Result summary:** PASS — 10 events returned, `StepsReset.Target == "a"`,
`StepsReset.RewindTo == "deadbeef"`, post-reset `StepStatus.Generation == 1`.

## Artifact: TestGenerationField

**What it proves:** A transcript `Entry` with `Generation: 2` written by
`transcript.Writer.Append` is read back with `Generation == 2` intact.

**Command:** `go test -v -count=1 -run TestGenerationField ./internal/transcript/`

**Result summary:** PASS — `entries[0].Generation == 2`.

## Artifact: Unknown-kind forward-compatibility

**What it proves:** `UnmarshalEnvelope` with kind `"future_event"` returns
`(env, nil, nil)` — the existing `TestUnmarshalEnvelope_UnknownKind` test was
updated to assert this behaviour (previously it asserted an error, which was the
old contract before the forward-compat change).

**Raw output:**

```
=== RUN   TestStepsResetRoundTrip
--- PASS: TestStepsResetRoundTrip (0.00s)
=== RUN   TestReplayPostReset
--- PASS: TestReplayPostReset (0.00s)
PASS
ok  	jig/internal/engine	0.185s
=== RUN   TestGenerationField
--- PASS: TestGenerationField (0.00s)
PASS
ok  	jig/internal/transcript	0.211s
```

## Reviewer Conclusion

The data-model and journal layer for spec 08 is complete. `Generation` is
threaded through `step.State`, `transcript.Entry`, `StepStatus`, and the TUI
separator. The `StepsReset` event is registered in the codec and replays
correctly. Task 3.0 can now journal and bump `Generation` without any missing
plumbing.
