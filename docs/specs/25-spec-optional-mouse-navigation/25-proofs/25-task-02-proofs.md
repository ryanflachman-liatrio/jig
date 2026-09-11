# Task 02 Proofs - Monitor pointer-targeted navigation

## Task Summary

Monitor now maps clicks and wheels to the visible Steps or Transcript content rectangle, including variable-height flattened rows and narrow single-panel layouts.

## What This Task Proves

- Clicks select ordinary, fan-out child, and file rows by rendered physical line.
- Steps wheels move three flattened rows; Transcript wheels move three rendered lines.
- Pointer wheels preserve keyboard focus, and transcript clicks change only focus.
- Transcript follow disables above bottom and restores at bottom across synthetic finalized appends.
- Hidden narrow-layout panels and unsupported input remain non-targets.

## Evidence Summary

Focused Monitor tests pass with wide, narrow, scrolled, variable-height, file-preview, and streaming fixtures. No pointer route produces a lifecycle command.

## Artifact: Focused Monitor tests

**What it proves:** The owner-local row mapping and panel geometry match selection, preview, viewport, and follow behavior.

**Why it matters:** It provides executable coverage for FR-05 through FR-10.

**Command:**

~~~bash
GOCACHE=<temporary-cache> go test ./internal/tui/monitor -run 'TestMonitorMouse|TestTranscriptMouseWheel|TestTranscriptScrollHotkeys'
~~~

**Result summary:** The Monitor package passed.

~~~text
ok  jig/internal/tui/monitor
~~~

## Artifact: Wide/narrow and streaming capture

**What it proves:** Pointer ownership follows rendered geometry at both required sizes while keyboard navigation and bounded transcript following remain intact.

**Why it matters:** It demonstrates the feature at the user-visible panel boundary without real run data.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-2-monitor-mouse-navigation.txt`

**Result summary:** Synthetic rows and transcript content show exact three-row/line movement, hidden-panel exclusion, and no lifecycle actions.

## Reviewer Conclusion

The combined evidence demonstrates pointer-targeted Monitor navigation without introducing a second selection, scrolling, or scheduling owner.
