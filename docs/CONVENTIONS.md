# Code Conventions

Design and structural conventions used in this codebase. Each entry names a
principle, shows what to look for, and gives the rule. These were distilled
from real decisions made during development — not aspirational guidelines.

---

## File organisation

### One concern per file

A file earns its name from its single theme. When a file mixes concerns —
rendering and event handling, layout math and keyboard dispatch — it becomes
the right place for nothing and a hard place to navigate.

**Signal:** a file over ~400 lines that doesn't have a single clear theme is
carrying too much. File size is a symptom, not the disease; the disease is
mixed concerns.

**Rule:** name each file after what it does (`monitor_gate.go`,
`monitor_transcript.go`), not after what it contains (`monitor_helpers.go`).
If you can't name the file without using "and" or "util", split it.

### Same-package file splitting over subpackages

Go's unit of encapsulation is the package, but packages can span many files.
When a model or system grows large, split into multiple files within the same
package rather than forcing a subpackage boundary.

A subpackage is only worth the disruption when:
- the API surface is stable and consumed by multiple other packages, or
- you want to enforce that callers cannot reach unexported internals.

When tests already have package-level access to unexported types, a subpackage
boundary forces you to export those types (or move the tests), adding churn
with no architectural benefit.

**Rule:** prefer `monitor_*.go` (same package, many files) over
`internal/tui/monitor/` (new package) when the model is only consumed by one
caller and the tests rely on unexported access.

### The root model is the permanent exception

The compositor model (`rootModel` in `internal/tui/`) holds every screen
subpackage and is the package's public entry point (`tui.New`). It can never
be extracted into its own subpackage — doing so would require every screen
subpackage to import `tui/root`, which in turn imports them, creating a cycle.

The root model therefore stays in `internal/tui/` indefinitely. It is split
across same-package files by concern (`root.go`, `root_update.go`,
`root_cmds.go`), but it has no subpackage directory of its own.

**Rule:** every screen model eventually gets its own subpackage. The root
compositor stays in the parent package permanently — it is the only file in
`internal/tui/` that is not a candidate for subpackage extraction.

### Subpackages require a shared foundation package first

A subpackage creates a package boundary. If the code being extracted depends
on presentation primitives (`theme`, `panel()`, icon constants, `hintString()`)
that also live in the parent package, extracting it naively produces a circular
import: the parent imports the subpackage for its model; the subpackage imports
the parent for its primitives.

The solution is a foundation package — `internal/tui/shared/` — that contains
only shared presentation primitives and depends on nothing in `internal/tui/`.
Both the parent and each subpackage import `shared`; neither imports the other.

```
internal/tui/shared/   ← styles, panel, input, keys, help overlay
       ↑                  no imports from tui/
internal/tui/          ← root, selector, detail, runs, chat
       ↑                  imports shared
internal/tui/monitor/  ← monitor model, msgs, keys
                          imports shared, not tui
```

**Rule:** before extracting a subpackage, check whether it references any
primitives from the parent. If it does, those primitives belong in `shared`,
not the parent. Extract `shared` first, then extract the subpackage.

### Migration: thin aliases let existing files migrate gradually

Moving everything to `shared` at once would require updating hundreds of
`theme.X`, `panel(...)`, `IconSuccess` references across all existing files in
one commit. Instead, keep the existing files working via thin local aliases
in the parent package:

```go
// internal/tui/styles.go — bridge during migration
package tui
import "jig/internal/tui/shared"
const IconSuccess = shared.IconSuccess  // const alias
var theme = shared.Theme               // var alias for the singleton
```

```go
// internal/tui/panel.go — bridge during migration
package tui
import "jig/internal/tui/shared"
func panel(title, body string, width, height int, focused bool) string {
    return shared.Panel(title, body, width, height, focused)
}
func panelFrame() (int, int) { return shared.PanelFrame() }
```

Each screen (`selector`, `detail`, `runs`, `chat`) can then be moved to its
own subpackage independently, switching to `shared.X` directly when it does.
The aliases are removed when the last file in the parent that uses them moves.

**Rule:** during a multi-step migration, use const/var/func aliases to keep
the parent compiling. Remove each alias when no remaining file in the parent
needs it.

### Constants live with their concern

Don't collect unrelated constants into a single block at the top of the file
or in a shared `constants.go`. A constant belongs in the file that defines the
concern it governs.

```go
// monitor_layout.go — layout constants live here because resize() uses them
const (
    stepsMinWidth           = 32
    transcriptMinInnerWidth = 40
)

// monitor_steps.go — step-list constants live here
const (
    listBodyHeaderLines = 0
    stepRowLines        = 2
)
```

**Rule:** ask "which file's functions read this constant most?" — put it there.

### Pure helpers stay near their primary caller

Stateless utility functions (`humanTokens`, `fenceJSON`, `collapseLine`,
`clipReason`, `withBar`) are not general enough to share. A grab-bag
`util.go` or `helpers.go` is a convenience today and a navigation tax forever.

**Rule:** put a helper in the file that contains its primary caller. If two
files share a helper, the helper belongs in whichever file it shapes more.

---

## Dispatch patterns

### Parallel switch statements are a design smell

When three or more functions all contain a `switch x { case A: ... case B:
... }` over the same discriminant, adding a new case means editing three
places. The cases are coupled by the discriminant but the code is scattered.

In this codebase the gate input kinds (`inputKindRequest`, `inputKindReview`,
`inputKindRecovery`, ...) appeared as parallel switch arms in `updateGate`,
`gateStrip`, and `footerView`. Adding a ninth kind would have required three
separate edits, with no compiler help to catch a missed arm.

**Fix:** extract each arm into a named method, reduce each switch to a
one-line dispatch:

```go
// Before: 300-line switch with inline logic per arm
switch entry.kind {
case inputKindRequest:
    // 20 lines inline
case inputKindQuestion:
    // 60 lines inline
// ...
}

// After: dispatch to named methods, logic lives in one place per kind
switch entry.kind {
case inputKindRequest:  return m.updateGateRequest(msg, entry)
case inputKindQuestion: return m.updateGateQuestion(msg, entry)
// ...
}
```

**Rule:** if the same discriminant drives three or more switch statements
across the codebase, name each arm as a method. Adding a new case then
means writing one function per concern, not editing N switch blocks.

### Named dispatch, not inline arms

A switch arm that contains substantial logic should be a named function. The
switch statement itself then reads as a table of contents — you can scan it
to understand the shape of the system, then dive into the specific method you
care about.

**Rule:** if a switch arm is longer than ~5 lines, name it.

---

## Bubble Tea model structure

### The model struct stays unified

Bubble Tea requires one model that implements `Update`/`View`. Splitting the
struct into embedded sub-structs to "reduce size" produces indirection chains
(`m.gate.inputQueue`, `m.transcript.chatBlocks`) that add cognitive load
without reducing coupling — the methods still need access to the whole model.

**Rule:** keep the `monitorModel` struct as one flat declaration. Distribute
*methods* across files by concern; the struct stays in `monitor_model.go`.

### Value receivers with shared maps

Go's value receiver copies the struct, but maps are reference types — a map
write inside a value-receiver method persists across copies. This codebase
uses value receivers on `monitorModel` throughout and relies on this property
for the render cache (`chatRendered`) and expand state (`chatExpand`).

The maps are invalidated wholesale (reassigned) on width change, not
mutated field-by-field, so the pattern is safe. But it is non-obvious.

**Rule:** if a value-receiver method writes to a map field, that's intentional
and correct. When replacing a map, use a pointer receiver (or return a new
model) so the reassignment is visible at the call site.

---

## Refactoring discipline

### Mechanical split before behavioural cleanup

When splitting a large file, do it in two passes:

1. **Move** — copy functions verbatim into their new files, delete the
   originals, fix imports, verify build and tests pass. No logic changes.
2. **Clean** — improve naming, extract helpers, simplify dispatch, now that
   each file is small enough to reason about in isolation.

Mixing both passes in one commit makes the diff unreadable and makes it hard
to bisect a regression.

**Rule:** split first (green tests), clean second (green tests again). Commit
each pass separately.

### Tests don't need to change when splitting files within a package

If the new files are in the same package as the old file, every test that
compiled before will compile after — package-level visibility is unchanged.
If you find yourself exporting symbols or moving tests to make a same-package
file split work, that's a signal the refactor is adding a package boundary
that doesn't belong yet.

When moving code to a true subpackage, tests must move with it. White-box
tests (those that access unexported fields) belong in the subpackage itself
(`package monitor`), not in `package monitor_test`. This preserves the same
access they had before, and avoids the need to export internal state just to
satisfy a test.
