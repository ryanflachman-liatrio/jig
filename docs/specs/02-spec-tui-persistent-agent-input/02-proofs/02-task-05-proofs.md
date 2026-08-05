# Task 05 Proofs — Review diff rendered in the Transcript panel

## Task Summary

This task moves the review diff out of the gate strip and into the Transcript
panel, so reviewers can read the diff while the gate shows only verdict choices
and the new diff-location hint. Queue navigation (tab/shift+tab) and Steps
navigation remain independent — cycling entries never moves the cursor or
reloads the transcript.

## What This Task Proves

- Selecting a review step in the Steps list renders the diff in the Transcript
  panel via `chatBody()`, not in the gate strip.
- Verdict choices (`[1] approve`, `[2] reject`) are absent from the Transcript
  body — they live exclusively in the gate entry.
- Tab-cycling queue entries does not call `reloadTranscript` or change `cursor`
  (Decision 2); `activeInputIdx` is unchanged after a `reloadTranscript` call.
- The gate's review body shows a `diff shown in Transcript — select this step`
  hint so the reviewer knows where to look.
- Full TUI test suite passes with the race detector.

## Evidence Summary

- `TestReviewDiffInTranscript` PASS — diff in chatBody, choices absent, `activeInputIdx` unchanged.
- `TestMonitorChatReviewFallback` updated and PASS — now asserts diff present, choices absent.
- `unit5-review-diff.txt` shows diff in Transcript panel and gate with only choices + hint.
- Race detector clean.

## Artifact: TestReviewDiffInTranscript

**What it proves:** Selecting a review step loads the diff into the Transcript
panel while leaving `activeInputIdx` unchanged (navigations are independent).

**Why it matters:** This is the core correctness proof for Decision 2 — the two
navigation axes (step cursor vs queue index) must never interfere.

**Command:**

```bash
go test ./internal/tui -run TestReviewDiffInTranscript -v
```

**Result summary:** PASS — diff markers present in chatBody; verdict choices
absent; `activeInputIdx` identical before and after `reloadTranscript`.

```
=== RUN   TestReviewDiffInTranscript
--- PASS: TestReviewDiffInTranscript (0.00s)
PASS
ok  	jig/internal/tui	0.475s
```

## Artifact: TestMonitorChatReviewFallback (updated)

**What it proves:** The existing review fallback test now asserts the new contract:
diff and "proposed changes" heading in Transcript; no verdict choices.

**Command:**

```bash
go test ./internal/tui -run TestMonitorChatReviewFallback -v
```

**Result summary:** PASS — the updated assertions confirm the diff renders and
choices do not bleed into the Transcript panel.

```
=== RUN   TestMonitorChatReviewFallback
--- PASS: TestMonitorChatReviewFallback (0.00s)
PASS
ok  	jig/internal/tui	0.475s
```

## Artifact: unit5-review-diff.txt — Split layout screenshot

**What it proves:** The Transcript panel shows the diff with the `proposed changes`
heading; the gate strip shows only verdict choices, `[m] message`, and the
diff-location hint — no diff in the gate.

**Why it matters:** This is the visual proof that the layout split works
end-to-end, not just in unit tests.

**Artifact path:** `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit5-review-diff.txt`

**Result summary:** Transcript shows `@@ -1,3 +1,3 @@` diff; gate shows
`[1] approve`, `[2] reject`, `[m] message`, and `diff shown in Transcript —
select this step` hint.

## Artifact: Full TUI test suite with race detector

**What it proves:** All TUI tests pass with no data races after Task 5 changes.

**Command:**

```bash
gofmt -l -w . && go vet ./... && go test ./internal/tui -race
```

**Result summary:** Clean format, no vet warnings, all tests pass.

```
ok  	jig/internal/tui	1.777s
```

## Reviewer Conclusion

Task 5 is complete: the review diff is now rendered exclusively in the Transcript
panel, verdict choices are absent from Transcript, the diff-location hint guides
the reviewer, and navigation independence (Decision 2) is enforced both in code
comments and verified by `TestReviewDiffInTranscript`.
