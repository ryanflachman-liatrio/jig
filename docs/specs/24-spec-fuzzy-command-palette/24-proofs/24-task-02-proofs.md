# Task 02 Proofs - Direct-execution commands: "Go to Home" and "Go to Monitor"

## Task Summary

This task adds a direct-execution path to the palette (`Command.Run func()
tea.Cmd`) alongside the existing key-redispatch path, and ships the first two
commands that use it: "Go to Home" from anywhere in Monitor, and "Go to
Monitor" from anywhere in Home for the currently-selected run.

## What This Task Proves

- `palette.Model.Update`'s `"enter"` case invokes `cmd.Run()` directly when
  set, and falls back to the existing `DispatchKey(cmd.Key)` re-dispatch when
  it is nil — the two paths coexist without regressing each other.
- `runs.Model.SelectedID()` mirrors the existing `Open`-key row-selection
  logic and correctly reports `("", false)` for an empty list and an
  out-of-range cursor.
- "Go to Monitor" appears in the Home palette catalog exactly when a run is
  selected (regardless of which Home pane has focus), and is omitted when
  there are no runs or no selection — and, when invoked, opens the selected
  run via the same `runs.ShowMonitorMsg` path as pressing Enter on that row.
- "Go to Home" is reachable from Monitor's Steps focus, Transcript focus, and
  an open gate — and correctly routes through the dirty-compose confirmation
  gate (`RequestLeaveConfirmMsg`) instead of leaving silently when a review
  compose buffer has unsaved text, exactly like `leaveMonitor()`'s existing
  esc-key behavior.
- The two new commands' IDs (`home:go-to-monitor`, `monitor:go-to-home`) do
  not collide with any key-redispatch command's generated ID in the same
  catalog.

## Evidence Summary

- `go test ./internal/tui/... -run "...palette Run dispatch, SelectedID,
  Go-to-Home/Go-to-Monitor tests..."` — all pass (see command output below).
- A rendered `View()` frame shows "Go to Home" listed and selected in a real
  Monitor palette catalog, and "Go to Monitor" listed in a real Home palette
  catalog for a selected run.
- `gofmt -l .`, `go vet ./...`, and `go build ./...` are all clean.

## Artifact: Palette Run-dispatch coexistence (palette package)

**What it proves:** `Model.Update`'s `"enter"` case prefers `Run` when set,
and still falls back to `DispatchKey` when `Run` is nil — the two dispatch
paths coexist correctly.

**Command:**

```bash
go test ./internal/tui/palette/... -run "TestPaletteEnterInvokesRunWhenSet|TestPaletteEnterFallsBackToDispatchKeyWhenRunNil" -v
```

**Result summary:** Both tests pass — `Run` fires and closes the palette
without emitting a `DispatchKey` message when set; the unaffected
key-redispatch command still dispatches `KeyPressMsg{'r'}` when `Run` is nil.

```
=== RUN   TestPaletteEnterInvokesRunWhenSet
--- PASS: TestPaletteEnterInvokesRunWhenSet (0.00s)
=== RUN   TestPaletteEnterFallsBackToDispatchKeyWhenRunNil
--- PASS: TestPaletteEnterFallsBackToDispatchKeyWhenRunNil (0.00s)
PASS
ok  	jig/internal/tui/palette	3.243s
```

## Artifact: `runs.Model.SelectedID()` accessor

**What it proves:** The new accessor returns the selected run id on a valid
cursor and `("", false)` for an empty list or an out-of-range cursor.

**Command:**

```bash
go test ./internal/tui/runs/... -run TestSelectedID -v
```

**Result summary:** All three subtests pass.

```
=== RUN   TestSelectedID
=== RUN   TestSelectedID/empty_list
=== RUN   TestSelectedID/valid_cursor
=== RUN   TestSelectedID/out_of_range_cursor
--- PASS: TestSelectedID (0.00s)
    --- PASS: TestSelectedID/empty_list (0.00s)
    --- PASS: TestSelectedID/valid_cursor (0.00s)
    --- PASS: TestSelectedID/out_of_range_cursor (0.00s)
PASS
ok  	jig/internal/tui/runs	3.260s
```

## Artifact: "Go to Monitor" presence, omission, and navigation (Home/root)

**What it proves:** "Go to Monitor" opens the selected run through the real
`rootModel.Update` navigation path, is present regardless of Home pane focus
when a run is selected, is absent with no runs, and its ID doesn't collide
with any other command in the same catalog.

**Command:**

```bash
go test ./internal/tui/... -run "TestPaletteGoToMonitorOpensSelectedRun|TestPaletteGoToMonitorAbsentWithoutSelection|TestHomePaletteGoToMonitorPresenceAcrossSubstates|TestPaletteCommandIDsAreUnique" -v
```

**Result summary:** All four tests pass, including the direct navigation
assertion that invoking the command's `Run()` transitions `root.active` to
`screenMonitor` for the correct run ID.

```
=== RUN   TestPaletteGoToMonitorOpensSelectedRun
--- PASS: TestPaletteGoToMonitorOpensSelectedRun (0.00s)
=== RUN   TestPaletteGoToMonitorAbsentWithoutSelection
--- PASS: TestPaletteGoToMonitorAbsentWithoutSelection (0.00s)
=== RUN   TestPaletteCommandIDsAreUnique
--- PASS: TestPaletteCommandIDsAreUnique (0.00s)
=== RUN   TestHomePaletteGoToMonitorPresenceAcrossSubstates
--- PASS: TestHomePaletteGoToMonitorPresenceAcrossSubstates (0.01s)
PASS
ok  	jig/internal/tui	2.839s
```

## Artifact: "Go to Home" reachability and dirty-compose confirmation (Monitor)

**What it proves:** "Go to Home" is present and returns `ShowHomeMsg` from
Steps focus, Transcript focus, and an open-but-clean review gate; when a
review compose buffer has unsaved text, it instead returns
`RequestLeaveConfirmMsg` — the same dirty-compose confirmation `leaveMonitor()`
already gives the esc key (A6) — even after the operator has since tabbed
away to Transcript focus.

**Command:**

```bash
go test ./internal/tui/monitor/... -run "TestGoToHomeExtraFromStepsFocus|TestGoToHomeExtraFromTranscriptFocus|TestGoToHomeExtraFromOpenGate|TestGoToHomeExtraRespectsDirtyComposeConfirm" -v
```

**Result summary:** All four tests pass, including the dirty-compose case,
which opens a real review workspace, opens the comment composer, types
unsaved text, moves focus to Transcript, and confirms the direct-execution
command still routes through the confirmation gate.

```
=== RUN   TestGoToHomeExtraFromStepsFocus
--- PASS: TestGoToHomeExtraFromStepsFocus (0.00s)
=== RUN   TestGoToHomeExtraFromTranscriptFocus
--- PASS: TestGoToHomeExtraFromTranscriptFocus (0.00s)
=== RUN   TestGoToHomeExtraFromOpenGate
--- PASS: TestGoToHomeExtraFromOpenGate (0.00s)
=== RUN   TestGoToHomeExtraRespectsDirtyComposeConfirm
--- PASS: TestGoToHomeExtraRespectsDirtyComposeConfirm (0.00s)
PASS
ok  	jig/internal/tui/monitor	3.102s
```

## Artifact: Rendered palette frames — both new commands discoverable

**What it proves:** Both commands are visible, real entries in their
respective screens' actual palette catalogs (built from the real
`PaletteSections()`/`SelectedID()` production code), not just internal test
assertions.

**Artifact path:** `docs/specs/24-spec-fuzzy-command-palette/artifacts/palette-demo.txt`

**Result summary:** Filtering Monitor's real palette catalog (built from
`monitor.New(...).PaletteSections()`, seeded with a run) for "go home" ranks
"Go to Home" to the top with its matched characters highlighted. The Home
catalog (seeded with one selected run via `runs.Model.SelectedID()`) lists
"Go to Monitor" alongside the existing "focus runs"/"new run" commands.

```
                ╭──────────────────────────────────────────────╮
                │  Commands                                    │
                │  > go home                                   │
                │  › Go to Home                                │
                │                                              │
                │  type to filter · enter run · esc            │
                ╰──────────────────────────────────────────────╯

                ╭──────────────────────────────────────────────╮
                │  Commands                                    │
                │  >                                           │
                │  › focus runs                    enter       │
                │    new run                       r           │
                │    Go to Monitor                             │
                │                                              │
                │  type to filter · enter run · esc            │
                ╰──────────────────────────────────────────────╯
```

## Artifact: Quality gates

**Command:**

```bash
gofmt -l . && go vet ./... && go build ./...
```

**Result summary:** All three commands produced no output (clean) and exited
successfully.

## Reviewer Conclusion

The direct-execution path coexists cleanly with the existing key-redispatch
path, and both new commands are wired through the real production
navigation/confirmation code — not a parallel mechanism — with automated
coverage for presence, omission, navigation outcome, and the A6
dirty-compose-confirmation edge case, plus a visual proof that both commands
are genuinely discoverable in their respective palettes.
