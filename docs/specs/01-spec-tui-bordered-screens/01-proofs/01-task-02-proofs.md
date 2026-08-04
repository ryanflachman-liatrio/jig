# Task 02 Proofs — Two-panel run monitor with focus-aware borders and non-blocking gates

## Task Summary

This task re-lays the run monitor into **two side-by-side titled panels** — a left
"Steps" panel (the step list) and a right panel titled with the selected step's id
(falling back to "Transcript") — shown simultaneously, replacing the old
`mode`/`modeList`/`modeChat` toggle with a `focus ∈ {Steps, Transcript, Gate}`
region field that colors only the focused region's border primary (Charple). Human-
in-the-loop **gates** (review verdict, `block_on` input, AskUserQuestion, `from="user"`
prompt) now render as a **non-blocking full-width strip** beneath the panels per
[ADR 0002](../../../adr/0002-gates-are-nonblocking-focus-regions.md): a gate auto-
focuses on arrival but the user can `tab`/`←`/`→` away to read the transcript a
verdict is about and back, and every existing resolution/cancellation semantic is
retained. The transcript reloads **eagerly** on every Steps selection change, the
glamour wrap width and per-block render cache are driven off the **Transcript
panel's inner width**, and a narrow-terminal (`< ~76` col) fallback shows only the
focused panel full-width. All styling flows from the existing `styles.go` panel
tokens; no new hex, no bare package-level style vars.

## What This Task Proves

- The monitor renders both panels at once, `lipgloss.JoinHorizontal`'d, with the
  left titled "Steps" and the right titled with the cursor's step id; only the
  focused region's border is primary, and `tab`/`shift+tab`/`←`/`→` move focus.
- Gates are non-blocking (ADR 0002): a focus-switch key still moves focus while a
  gate is pending, gate keys resolve with the correct response, and `esc`/`q`
  cancellation of an AskUserQuestion emits `agentQuestionResponseMsg{answer:"cancelled"}`
  so the reporter goroutine never hangs.
- Moving the Steps cursor eagerly re-points and reloads the Transcript panel to the
  newly-selected step (resetting per-step block-cursor/expand state and the render
  cache, since `blockKey` seq restarts per step file).
- Panels re-fit on every `WindowSizeMsg` with no line exceeding the terminal width,
  including the narrow single-panel fallback.
- No regression: the full suite passes under `-race`, `go vet` is clean, and
  `gofmt -l .` reports no files.

## Evidence Summary

- `go test ./internal/tui -run 'TestMonitorTwoPanel|TestMonitorGateNonBlocking|TestMonitorEagerReload|TestMonitorResizeRefits'`
  passes — one test per Unit-2 functional requirement, plus the resize re-fit.
- The pre-existing monitor gate tests were updated for the focus model and still
  pass (review auto-focus, AskUserQuestion select/multi-select/cancel, clears-on-resume).
- `go test ./... -race` passes across every package; `go vet ./...` is clean;
  `gofmt -l .` prints nothing.
- Rendered text screenshots show the two-panel layout with a highlighted focused
  border, the review gate strip beneath the panels with focus on the Transcript,
  and the narrow single-panel fallback with an intact border.

## Artifact: Two-panel + focus-toggle test

**What it proves:** Both panel titles render and `tab` moves the primary border from
the left (Steps) panel to the right (Transcript) panel.

**Why it matters:** This is the core Unit-2 layout change — two simultaneous,
focus-aware panels replacing the old mode toggle.

**Command:**

~~~bash
go test ./internal/tui -run TestMonitorTwoPanel -v
~~~

**Result summary:** Passes. It asserts the "Steps" title and the selected step id
appear, that the default `focusSteps` colors the left title with the Charple
primary SGR (`\x1b[38;2;107;80;255m`), and that after `tea.KeyPressMsg{Code: tea.KeyTab}`
the primary run moves to the right of the Steps label.

~~~
--- PASS: TestMonitorTwoPanel (0.00s)
~~~

## Artifact: Non-blocking gate test

**What it proves:** A pending gate does not freeze navigation (tab moves focus away,
`j` then navigates the Steps cursor), gate keys resolve with the correct response,
and `esc` cancellation delivers the `"cancelled"` response.

**Why it matters:** ADR 0002 — the reviewer can read the transcript a verdict is
about while the gate waits, and no reporter goroutine hangs on cancellation.

**Command:**

~~~bash
go test ./internal/tui -run TestMonitorGateNonBlocking -v
~~~

**Result summary:** Passes. With an AskUserQuestion pending it asserts `tab` moves
focus off `focusGate`, the question stays pending, `j` moves the Steps cursor,
returning to the gate and pressing `2` emits `agentQuestionResponseMsg{answer:…Beta}`,
and `esc` emits a batch containing `agentQuestionResponseMsg{answer:"cancelled"}`.

~~~
--- PASS: TestMonitorGateNonBlocking (0.00s)
~~~

## Artifact: Eager transcript reload + resize re-fit tests

**What it proves:** Moving the Steps cursor switches the Transcript panel body to the
new step's transcript, and a second `WindowSizeMsg` re-fits both panels with no
line wider than the terminal.

**Why it matters:** Resolved Decisions 10 (eager reload) and the responsive-layout
success metric (no overflow / broken borders on resize, including narrow).

**Command:**

~~~bash
go test ./internal/tui -run 'TestMonitorEagerReload|TestMonitorResizeRefits' -v
~~~

**Result summary:** Both pass. `TestMonitorEagerReload` writes two same-seq
transcripts and asserts the body switches from `ALPHA-BODY` to `BETA-BODY` on a
cursor move (also exercising the per-step render-cache reset that fixes a `blockKey`
seq collision). `TestMonitorResizeRefits` drives 100×30, 70×20, 120×40 and asserts
no line exceeds the width.

~~~
--- PASS: TestMonitorEagerReload (0.03s)
--- PASS: TestMonitorResizeRefits (0.00s)
~~~

## Artifact: No-regression gates (race / vet / gofmt)

**What it proves:** The change introduces no test, race, vet, or formatting
regressions.

**Why it matters:** Repo standards require `go test ./... -race`, `go vet ./...`,
and `gofmt -l .` clean for this TUI/concurrency work.

**Command:**

~~~bash
go test ./... -race && go vet ./... && gofmt -l .
~~~

**Result summary:** Every package reports `ok` under `-race`; vet exits 0;
`gofmt -l .` prints nothing.

~~~
ok  	jig/internal/datastore	1.961s
ok  	jig/internal/engine	3.233s
ok  	jig/internal/runner	4.589s
ok  	jig/internal/transcript	3.684s
ok  	jig/internal/tui	5.680s
ok  	jig/internal/workflow	6.391s
# vet: clean (exit 0)
# gofmt -l .: (no output)
~~~

## Artifact: Two-panel screenshot

**What it proves:** The Steps panel and the transcript panel render side by side,
with the focused (Transcript) panel's border highlighted and the right panel titled
with the selected step id ("generate").

**Why it matters:** The primary Unit-2 visual outcome — simultaneous panels with a
focus-colored border.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/2.0-two-panel.txt`

~~~
╭─ Steps ──────────────────────╮╭─ generate ───────────────────────────────────────────────────╮
│   ✓  plan      succeeded     ││   ●  running                                                 │
│ ▌ ●  generate  running       ││                                                              │
│   ○  review    pending       ││   #1 assistant                                               │
│   ○  test      pending       ││   ▌ ▸ ◇ reasoning  Planning the change                       │
│                              ││                                                              │
│                              ││   I'll add the new field and wire up validation.             │
│                              ││                                                              │
│                              ││   ▌ ▸ ▸ Edit  {"file_path":"workflow.go"}                    │
│                              ││                                                              │
│                              ││   #2 user                                                    │
│                              ││   ▌ ▸ ↳ result  edit applied                                 │
│                              ││                                                              │
╰──────────────────────────────╯╰──────────────────────────────────────────────────────────────╯
  running  ·  tab/←/→ focus  •  j/k scroll  •  n/N block  •  enter expand  •  o all
~~~

## Artifact: Gate-strip screenshot

**What it proves:** A review gate renders as a full-width strip beneath the two
panels, above the footer, while keyboard focus sits on the Transcript panel — the
gate did not freeze navigation. The footer shows "tab to gate" to return.

**Why it matters:** Demonstrates the non-blocking gate (ADR 0002) as a third,
navigable focus region.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/2.0-gate.txt`

~~~
╭─ Steps ──────────────────────╮╭─ plan ───────────────────────────────────────────────────────╮
│ ▌ ○  plan      pending       ││   ○  pending                                                 │
│   ○  generate  pending       ││   no output yet                                              │
│   ○  review    pending       ││                                                              │
│   ○  test      pending       ││                                                              │
╰──────────────────────────────╯╰──────────────────────────────────────────────────────────────╯
╭─ Review — review ────────────────────────────────────────────────────────────────────────────╮
│   ── diff ─────────────────────────────                                                      │
│   @@ -10,3 +10,4 @@                                                                          │
│    func Validate() {                                                                         │
│   -    return nil                                                                            │
│   +    if x == nil { return errMissing }                                                     │
│   +    return nil                                                                            │
│                                                                                              │
│     [1] approve                                                                              │
│     [2] reject                                                                               │
│     [m] message                                                                              │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
  awaiting review  ·  tab to gate  •  tab/←/→ focus  •  j/k scroll  •  n/N block  •  enter expand
~~~

## Artifact: Narrow-terminal screenshot

**What it proves:** Below the ~76-col threshold the monitor renders only the focused
panel full-width, with an intact border; the focus key toggles which panel is shown.

**Why it matters:** Resolved Decision 14 — a graceful degradation of the two-panel
layout, not a return of the old `mode` toggle.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/2.0-narrow.txt`

~~~
╭─ Steps ──────────────────────────────────────────────────────╮
│ ▌ ✓  plan      succeeded         —                           │
│   ●  generate  running           0s                          │
│   ○  review    pending           —                           │
│   ○  test      pending           —                           │
│                                                              │
╰──────────────────────────────────────────────────────────────╯
  running  ·  tab/←/→ focus  •  j/k select  •  esc runs list  •
~~~

## Reviewer Conclusion

The Unit-2 functional requirements are met and independently demonstrable: the
monitor is now two side-by-side titled panels with a focus-colored border, gates are
non-blocking navigable strips beneath the panels (ADR 0002) with every resolution and
cancellation semantic retained, the transcript reloads eagerly on selection change,
the glamour wrap width and render cache follow the Transcript panel's inner width,
and a narrow fallback keeps the border intact. Every FR maps to a passing test, and
the no-regression gates (`-race` / vet / gofmt) are clean.

## Notable Findings / Reviewer Callouts

- **Fixed a latent `blockKey` render-cache collision.** `blockKey{seq, block}`
  restarts per step file, so caching a markdown render under it and then showing a
  different step with the same seq returned the wrong body. `reloadTranscript` now
  clears `chatRendered` alongside the other per-step view state. This surfaced
  precisely because Unit-2 switches transcripts eagerly between steps.
- **`gradientTitle` is now unused.** Its only call site (the old `chatBody` "chat"
  wordmark header) was removed per task 2.4. It is left in `styles.go` for the
  in-progress Unit-3 chat restructure; Go permits an unused package-level function,
  so build/vet stay clean.
- **Footer clipping.** `footerView` now applies `MaxWidth(m.width)` so a long hint
  line never overflows the panels and skews `JoinVertical`'s per-line width (which
  would break the box borders at narrow widths).
