# 24 Questions Round 2 - Fuzzy Command Palette

Your round-1 answers picked (C) for scope — add new global actions *and* extend `ctrl+k` to screens that don't have it today. Tracing the actual code to turn that into concrete Demoable Units surfaced two corrections worth confirming before the spec is written, so this round only covers those.

## 1. There is no missing-screen gap to close

`rootModel` only has two top-level screens (`screenHome`, `screenMonitor` — `internal/tui/root.go:27-28`), and `handleGlobalKey` (`internal/tui/root_update.go:236`) — where `ctrl+k` is wired — runs for every screen unconditionally. Both screens already build their palette catalog from a **state-dependent** function, not a static one:

- Home: `homeHelpSections()` (`internal/tui/home.go:68-98`) branches on `showDetailOverlay` / `homeFocus`, so Workflows, Runs, and the Detail overlay are all already included.
- Monitor: `PaletteSections()` → `helpSections(false)` (`internal/tui/monitor/monitor_model.go:824-932`) branches on `focus` and gate `entry.kind`, and when a review workspace is open it appends `entry.workspace.Help()` directly (`monitor_model.go:998-1007`) — so review-open commands are already in the catalog too, not just planned for it (the phase-1 doc's "review commands when review open" note is already shipped).

So "extend the palette to the remaining screens" doesn't correspond to an actual gap in the code today — there's no screen or sub-state whose keybindings are missing from the `ctrl+k` catalog.

Given that, how should this half of the unit be handled?

- [ ] (A) Drop it — no screens are missing, so scope on this point reverts to just the new global actions (question 2) plus the fuzzy-matching work from round 1.
- [x] (B) Replace it with a verification unit: add table-driven regression tests asserting `paletteCommands()` / `PaletteSections()` return the expected catalog for every reachable Home/Monitor sub-state (Workflows, Runs, Detail overlay; Steps, Transcript, each gate `entry.kind` including review-open) — turning today's implicit, easy-to-silently-break parity into an explicit, tested guarantee, with the test run as the Proof Artifact.
- [ ] (C) Other — describe a concrete coverage gap you're aware of that isn't accounted for above.

**Recommended answer(s):** [(B)]

**Why these are recommended:**

- (A) is honest but throws away the intent behind picking (C) in round 1 — you clearly wanted assurance that palette coverage is solid across screens.
- (B) delivers that assurance for real: it's a genuine, demoable, low-risk addition (tests, no behavior change) that locks in the parity so a future refactor of `homeHelpSections`/`helpSections` can't quietly drop a state's bindings from the palette without a test failing.
- (C) is available if you know of an actual gap (e.g. a specific sub-state) I didn't find by reading the code.

## 2. Concrete new global actions

Checking the two example "new" actions from round 1's question 1 against the code, two of the four already exist in today's palette:

- "Toggle simple/advanced mode" — already bound (`ctrl+shift+a`) and unconditionally included in Monitor's `helpSections` under a "Mode" section (`monitor_model.go:917-925`).
- "Show help" — already bound (`?` / F1) and included in every screen's "Global" section via `shared.GlobalHelpBindings` (`internal/tui/shared/keys.go:42-54`).

So those don't need new work. The two that are genuinely missing today are cross-screen jumps that currently require being in a specific focus/leaf state first:

- **"Go to Home"** — from Monitor, returning to Home today goes through focus-scoped "leave" keys (e.g. `TransLeave` only works from the Transcript focus; you'd need `TransToSteps` first from elsewhere) rather than one direct action from any Monitor state.
- **"Go to Monitor"** — from Home, opening the Monitor for the currently-selected run requires focusing the Runs pane (`tab`) and pressing `enter`; there's no one-step jump from wherever you are in Home (e.g. while focused on Workflows).

Which of these should the spec add as new `Run func() tea.Cmd`-backed palette commands (not tied to a single-focus keybinding)?

- [x] (A) Both "Go to Home" and "Go to Monitor" as described above.
- [ ] (B) Only "Go to Home" (Monitor entry is already reasonably close via Runs pane + Enter, so the value is lower).
- [ ] (C) Other — name different or additional actions instead.

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- Both are the same shape of gap — a one-step jump across the Home/Monitor boundary that today requires first being in (or navigating to) a specific focus state — so implementing one buys most of the plumbing for the other.
- Together they're the smallest concrete, genuinely-new slice of "richer named actions": visible, demoable, and not a duplicate of something the palette already offers.
- "Go to Monitor" needs a target run; recommend defaulting it to the currently-selected run in the Runs pane (same run `enter` would open), and disabling/omitting the command when Home has no runs or none is selected — flagged here as an assumption rather than a fourth question since it directly mirrors existing `Enter`-to-open behavior.
