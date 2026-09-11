# Task 03 Proofs - Palette catalog parity regression tests

## Task Summary

This task converts today's implicit "the palette always reflects the active
screen's reachable keybindings" guarantee into explicit, table-driven
regression tests, so a future refactor of `homeHelpSections()` or
`helpSections()` can't silently drop a reachable sub-state's commands from
the `ctrl+k` palette — including the two new direct-execution commands from
Task 02.

## What This Task Proves

- `homeHelpSections()` returns the expected section titles for every
  reachable Home sub-state: Workflows focus, Runs focus, and the Detail
  overlay.
- `PaletteSections()` (Monitor's full, simple-mode-independent catalog)
  returns the expected section/binding set for every reachable Monitor
  focus/gate combination: Steps focus, Transcript focus (chat and file
  selection), and each gate `entry.kind` covered by the spec — request,
  question, review (not open), and review (open, with an active workspace).
- "Go to Home" is present in every one of those Monitor states, and "Go to
  Monitor" is present/absent in the Home states exactly per the Task 02 rule.
- These tests run as part of the ordinary `go test ./...` suite — no new
  build tags, flags, or manual steps.

## Evidence Summary

- `go test ./internal/tui/... -run TestPalette` and the two new parity tests
  (`TestHomeHelpSectionsParityAcrossSubstates`,
  `TestMonitorPaletteCatalogParityAcrossStates`) all pass.
- The full repository test suite (`go test ./...`) passes with no
  regressions, and all quality gates (`gofmt`, `go vet`, `go build`) are
  clean.

## Artifact: Home catalog parity across all three reachable sub-states

**What it proves:** `homeHelpSections()` returns exactly `["Workflows",
"Global"]` on Workflows focus, `["Runs", "Global"]` on Runs focus, and
`["Workflow", "Global"]` for the Detail overlay — matching today's behavior
exactly, now locked in by a test instead of only by inspection.

**Command:**

```bash
go test ./internal/tui/... -run TestHomeHelpSectionsParityAcrossSubstates -v
```

**Result summary:** All three sub-state subtests pass.

```
=== RUN   TestHomeHelpSectionsParityAcrossSubstates
=== RUN   TestHomeHelpSectionsParityAcrossSubstates/Workflows_focus
=== RUN   TestHomeHelpSectionsParityAcrossSubstates/Runs_focus
=== RUN   TestHomeHelpSectionsParityAcrossSubstates/Detail_overlay
--- PASS: TestHomeHelpSectionsParityAcrossSubstates (0.00s)
    --- PASS: TestHomeHelpSectionsParityAcrossSubstates/Workflows_focus (0.00s)
    --- PASS: TestHomeHelpSectionsParityAcrossSubstates/Runs_focus (0.00s)
    --- PASS: TestHomeHelpSectionsParityAcrossSubstates/Detail_overlay (0.00s)
PASS
ok  	jig/internal/tui	0.434s
```

## Artifact: Monitor catalog parity across every focus/gate combination

**What it proves:** `PaletteSections()` returns the expected section and a
representative, state-specific binding description for Steps focus,
Transcript focus (chat selection and file selection), and each of the four
gate `entry.kind` states enumerated by the spec — and "Go to Home" is present
in every one of them, without exception.

**Command:**

```bash
go test ./internal/tui/monitor/... -run TestMonitorPaletteCatalogParityAcrossStates -v
```

**Result summary:** All seven sub-state subtests pass, each asserting both
the state-specific binding (e.g. "submit" for a request gate, "1-9
decision" for an unopened review gate, "mark reviewed" for an open review
workspace) and the always-present "Go to Home" extra.

```
=== RUN   TestMonitorPaletteCatalogParityAcrossStates
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Steps_focus
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Transcript_focus,_chat_selection
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Transcript_focus,_file_selection
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Gate:_request
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Gate:_question
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Gate:_review,_not_open
=== RUN   TestMonitorPaletteCatalogParityAcrossStates/Gate:_review,_open_workspace
--- PASS: TestMonitorPaletteCatalogParityAcrossStates (0.01s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Steps_focus (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Transcript_focus,_chat_selection (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Transcript_focus,_file_selection (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Gate:_request (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Gate:_question (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Gate:_review,_not_open (0.00s)
    --- PASS: TestMonitorPaletteCatalogParityAcrossStates/Gate:_review,_open_workspace (0.00s)
PASS
ok  	jig/internal/tui/monitor	2.211s
```

## Artifact: `go test ./internal/tui/... -run TestPalette` (spec-named proof command)

**What it proves:** The spec's own named proof command — every test whose
name starts with `TestPalette` — passes, covering both the direct-execution
dispatch tests and the "Go to Monitor" presence/navigation tests together.

**Command:**

```bash
go test ./internal/tui/... -run TestPalette -v
```

**Result summary:** All matching tests pass across the `tui`, `tui/monitor`,
and `tui/palette` packages.

```
=== RUN   TestPaletteGoToMonitorOpensSelectedRun
--- PASS: TestPaletteGoToMonitorOpensSelectedRun (0.00s)
=== RUN   TestPaletteGoToMonitorAbsentWithoutSelection
--- PASS: TestPaletteGoToMonitorAbsentWithoutSelection (0.00s)
=== RUN   TestPaletteCommandIDsAreUnique
--- PASS: TestPaletteCommandIDsAreUnique (0.00s)
PASS
ok  	jig/internal/tui	0.434s
...
=== RUN   TestPaletteFilterAndEsc
--- PASS: TestPaletteFilterAndEsc (0.00s)
=== RUN   TestPaletteEnterDispatchesKey
--- PASS: TestPaletteEnterDispatchesKey (0.00s)
=== RUN   TestPaletteEnterInvokesRunWhenSet
--- PASS: TestPaletteEnterInvokesRunWhenSet (0.00s)
=== RUN   TestPaletteEnterFallsBackToDispatchKeyWhenRunNil
--- PASS: TestPaletteEnterFallsBackToDispatchKeyWhenRunNil (0.00s)
PASS
ok  	jig/internal/tui/palette	2.453s
```

## Artifact: Full repository test suite and quality gates

**What it proves:** No task in this spec introduced a regression anywhere
else in the repository, and every quality gate the repo enforces
(`gofmt`, `go vet`, `go build`) stays clean.

**Command:**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./...
```

**Result summary:** `gofmt`/`go vet`/`go build` produced no output; every
package's tests pass.

```
ok  	jig/cmd/jig	(cached)
ok  	jig/internal/datastore	(cached)
ok  	jig/internal/engine	(cached)
ok  	jig/internal/harness	(cached)
ok  	jig/internal/headless	(cached)
ok  	jig/internal/helpchat	(cached)
ok  	jig/internal/interaction	(cached)
?   	jig/internal/manifest	[no test files]
ok  	jig/internal/notification	(cached)
ok  	jig/internal/ops	(cached)
?   	jig/internal/review	[no test files]
ok  	jig/internal/runexport	(cached)
ok  	jig/internal/runner	(cached)
ok  	jig/internal/scaffold	(cached)
ok  	jig/internal/sentinel	(cached)
ok  	jig/internal/step	(cached)
ok  	jig/internal/telemetry	(cached)
ok  	jig/internal/toolcall	(cached)
ok  	jig/internal/transcript	(cached)
ok  	jig/internal/tui	1.143s
ok  	jig/internal/tui/chart	(cached)
ok  	jig/internal/tui/chat	(cached)
ok  	jig/internal/tui/detail	(cached)
ok  	jig/internal/tui/diffview	(cached)
ok  	jig/internal/tui/monitor	1.438s
ok  	jig/internal/tui/palette	(cached)
ok  	jig/internal/tui/prefs	(cached)
ok  	jig/internal/tui/question	(cached)
ok  	jig/internal/tui/review	(cached)
ok  	jig/internal/tui/runs	(cached)
ok  	jig/internal/tui/selector	(cached)
ok  	jig/internal/tui/shared	(cached)
ok  	jig/internal/workflow	(cached)
```

## Reviewer Conclusion

The palette catalog's coverage of every reachable Home/Monitor sub-state —
including the two new direct-execution commands — is now an explicit,
enforced contract in the ordinary test suite, not just an implicit property
of the code. Combined with Tasks 01 and 02's proofs and this clean full-suite
run, the fuzzy command palette feature is complete and regression-tested.
