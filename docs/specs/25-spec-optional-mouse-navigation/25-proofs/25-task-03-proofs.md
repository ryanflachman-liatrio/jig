# Task 03 Proofs - Detail scrolling and mouse isolation

## Task Summary

Detail accepts only plain vertical wheels inside its rendered content viewport, while root and Monitor consume mouse input at modal and text-capture boundaries.

## What This Task Proves

- Detail list and chart modes scroll exactly three lines with clamping and unchanged horizontal/mode state.
- Detail clicks and malformed, modified, horizontal, chrome, footer, and out-of-bounds events are no-ops.
- Root overlays, confirmations, filtering, and Monitor interaction owners prevent base-panel pass-through.
- A pending but unfocused Gate does not unnecessarily disable ordinary panel navigation.

## Evidence Summary

Focused Detail, Monitor, and root tests pass with synthetic content. No new handler emits an action command, goroutine, terminal reader, or engine event.

## Artifact: Input-isolation test suite

**What it proves:** Pointer routing obeys overlay, focus, text-capture, coordinate, and message-kind boundaries.

**Why it matters:** These boundaries prevent mouse input from becoming an alternate scheduler or editor owner.

**Command:**

~~~bash
GOCACHE=<temporary-cache> go test ./internal/tui/detail ./internal/tui/monitor ./internal/tui -run 'Test(DetailMouse|MonitorMouse|RootMouse|HomeMouse|RootViewMouse)'
~~~

**Result summary:** All three packages passed.

~~~text
ok  jig/internal/tui/detail
ok  jig/internal/tui/monitor
ok  jig/internal/tui
~~~

## Artifact: Detail and exclusion capture

**What it proves:** Detail is the sole modal exception and retains keyboard close; every other active overlay/text-capture surface consumes mouse input.

**Why it matters:** It demonstrates FR-11 through FR-14 at the user-visible routing boundary.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-3-detail-input-isolation.txt`

**Result summary:** Both Detail modes move only vertically by three lines, while underlying Home state and excluded Monitor/root surfaces remain unchanged.

## Reviewer Conclusion

The combined evidence demonstrates bounded Detail scrolling and conservative input isolation without affecting existing keyboard or engine paths.
