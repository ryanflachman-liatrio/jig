# Task 03 Proofs — Paneled streaming chat client

## Task Summary

This task restructures the standalone streaming chat client (`chatModel`, reached
via `go run ./cmd/jig`) into **two titled panels** using the shared `panel()`
helper and the same focus-border convention as the run monitor: a "Conversation"
output panel wrapping the viewport and a "Message" input panel wrapping the
textarea. The loose header/turn/status chrome is folded into the panel titles —
turn info into the Conversation title (`Conversation · Turn 2 of 3` when more than
one turn exists), a transient `connecting…` that drops once the client connects
(never a persistent "Connected"), and `Message · responding…` on the input title
while a turn streams (no spinner glyph in the border edge). The input panel now
**owns its frame**: the shared `newInputTextarea` gained a `withoutBorder()`
option so the chat's textarea does not draw a second box inside the Message panel.
A fatal connection/stream error renders as its **own full-width line beneath the
panels** — the chat's analogue of the monitor's gate strip — never inside a
truncatable title. The old `headerView`, `turnIndicatorView`, and `statusLineView`
methods are deleted. `chatModel.View()` continues to return `tea.View` (it is a
standalone root model). All styling flows from the existing `styles.go` theme
tokens; no new hex, no bare package-level style vars.

## What This Task Proves

- The chat renders as two `lipgloss.JoinVertical`'d titled panels — "Conversation"
  above "Message" — with only the focused panel's border drawn primary (Charple),
  toggled by the existing `focusInput`/`focusOutput` keys (`esc`/`i`/`enter`).
- Turn info and connection/streaming state live in the panel titles: the
  Conversation title reads `Conversation · Turn N of M` when `len(m.turns) > 1`,
  `Conversation · connecting…` before connect (dropping to plain `Conversation`
  once connected — never "Connected"), and the Message title reads
  `Message · responding…` while streaming, `Message` when idle.
- The Message panel owns its frame — the textarea's own border is suppressed via
  `withoutBorder()` — so there is no double box; every rendered line is exactly
  the terminal width (borders stay aligned).
- A fatal error renders as a full-width line beneath the panels, never inside a
  title.
- No regression: the full suite passes (under `-race`), `go vet` is clean, and
  `gofmt -l .` reports no files.

## Evidence Summary

- `go test ./internal/tui -run 'TestChatPanels|TestChatStreamingTitle|TestChatConnectingTitle'`
  passes — one test per Unit-3 functional requirement (two titled panels +
  focus-border reuse; streaming indicator + turn info in titles; transient
  connecting title).
- `go test ./... -race` passes across every package; `go vet ./...` is clean;
  `gofmt -l .` prints nothing.
- Rendered text screenshots show the two-panel chat layout with the Conversation
  title carrying turn info and the Message title carrying the streaming state,
  each line width-matched to the border.

## Artifact: Two-panel + focus-toggle test

**What it proves:** Both panel titles ("Conversation", "Message") render, and the
`focusInput`/`focusOutput` toggle (`esc`) moves the primary (Charple) border from
the Message panel to the Conversation panel.

**Why it matters:** This is the core Unit-3 layout change — the loose header /
viewport / status / textarea / footer stack becomes two focus-aware titled panels
reusing the shared `panel()` helper.

**Command:**

~~~bash
go test ./internal/tui -run TestChatPanels -v
~~~

**Result summary:** Passes. It asserts both titles appear, that the default
`focusInput` colors the "Message" title line with the Charple primary SGR
(`\x1b[38;2;107;80;255m`) while the "Conversation" line is not primary, and that
after `esc` (focus → output) the primary border moves to the Conversation panel
and off the Message panel.

~~~
--- PASS: TestChatPanels (0.00s)
~~~

## Artifact: Streaming-title + turn-info test

**What it proves:** While a turn streams the Message title reads
`Message · responding…` and clears to `Message` when idle; the Conversation title
carries `Turn 2 of 2` only when more than one turn exists.

**Why it matters:** The old `statusLineView` spinner label and `turnIndicatorView`
line are folded into the panel titles (no animated glyph in the border edge), per
the Unit-3 functional requirements.

**Command:**

~~~bash
go test ./internal/tui -run TestChatStreamingTitle -v
~~~

**Result summary:** Passes. With a single idle turn the input title is `Message`
and the view carries no `responding…`/`Turn ` text; with two turns and
`streaming = true` the input title is `Message · responding…` and the Conversation
title is `Conversation · Turn 2 of 2`, both appearing in the rendered view.

~~~
--- PASS: TestChatStreamingTitle (0.00s)
~~~

## Artifact: Connecting-title test

**What it proves:** The Conversation title reads `Conversation · connecting…`
before the client connects and drops to plain `Conversation` once connected —
never a persistent "Connected".

**Why it matters:** Requirement 3.2 explicitly forbids a persistent "Connected"
status; the connecting hint is transient and lives only in the title.

**Command:**

~~~bash
go test ./internal/tui -run TestChatConnectingTitle -v
~~~

**Result summary:** Passes. Asserts the pre-connect title, the post-connect drop
to plain `Conversation`, and that the title never contains "Connected".

~~~
--- PASS: TestChatConnectingTitle (0.00s)
~~~

## Artifact: No-regression gates (race / vet / gofmt)

**What it proves:** The change introduces no test, race, vet, or formatting
regressions.

**Why it matters:** Repo standards require `go test ./... -race`, `go vet ./...`,
and `gofmt -l .` clean for this TUI/streaming work.

**Command:**

~~~bash
go test ./... -race && go vet ./... && gofmt -l .
~~~

**Result summary:** Every package reports `ok` under `-race`; vet exits 0;
`gofmt -l .` prints nothing.

~~~
ok  	jig/internal/datastore
ok  	jig/internal/engine
ok  	jig/internal/runner
ok  	jig/internal/transcript
ok  	jig/internal/tui
ok  	jig/internal/workflow
# vet: clean (exit 0)
# gofmt -l .: (no output)
~~~

## Artifact: Idle chat screenshot

**What it proves:** The chat renders as a "Conversation" output panel above a
"Message" input panel. The Conversation title carries `· Turn 2 of 2` (turn info
folded into the border edge), the Message panel is a single clean box (the
textarea's own border is suppressed — no double box), and the footer sits plainly
below the panels. Every rendered line is exactly the terminal width (80 cells).

**Why it matters:** The primary Unit-3 visual outcome — two titled panels with the
old loose chrome folded into their titles.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/3.0-chat.txt`

~~~
╭─ Conversation · Turn 2 of 2 ─────────────────────────────────────────────────╮
│ You                                                                          │
│ Now wire it into the loader.                                                 │
│                                                                              │
│                                                                              │
│   The loader now defaults and validates the field. All tests pass.           │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ Message ────────────────────────────────────────────────────────────────────╮
│ ┃ Ask Claude... (enter to send, alt+enter for newline, ctrl+c to quit)       │
│ ┃                                                                            │
│ ┃                                                                            │
╰──────────────────────────────────────────────────────────────────────────────╯
enter send • alt+enter newline • esc/i switch focus • ctrl+c quit (100%)
~~~

## Artifact: Streaming chat screenshot

**What it proves:** Mid-stream the Message panel title reads `· responding…` (the
streaming indicator in the title, no spinner glyph in the border edge) while the
Conversation title continues to carry the turn info. The streaming answer appends
as raw text in the Conversation body.

**Why it matters:** Demonstrates the streaming state folded into the input title,
replacing the removed `statusLineView` spinner label.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/3.0-chat-streaming.txt`

~~~
╭─ Conversation · Turn 2 of 2 ─────────────────────────────────────────────────╮
│ You                                                                          │
│ Now wire it into the loader.                                                 │
│                                                                              │
│ Wiring the loader now — defaulting the field, then                           │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
│                                                                              │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ Message · responding… ──────────────────────────────────────────────────────╮
│ ┃ Ask Claude... (enter to send, alt+enter for newline, ctrl+c to quit)       │
│ ┃                                                                            │
│ ┃                                                                            │
╰──────────────────────────────────────────────────────────────────────────────╯
enter send • alt+enter newline • esc/i switch focus • ctrl+c quit (100%)
~~~

## Reviewer Conclusion

The Unit-3 functional requirements are met and independently demonstrable: the
standalone chat is now two `lipgloss.JoinVertical`'d titled panels ("Conversation"
+ "Message") with a focus-colored border reusing the existing
`focusInput`/`focusOutput` toggle; the header, turn-indicator, and status-line
chrome is folded into the panel titles (turn info, transient `connecting…`, and
`responding…`); the Message panel owns its frame (the textarea border is
suppressed to avoid a double box); a fatal error renders beneath the panels rather
than inside a title; and `chatModel.View()` still returns `tea.View`. Every FR maps
to a passing test, screenshots show aligned width-matched borders, and the
no-regression gates (`-race` / vet / gofmt) are clean.

## Notable Findings / Reviewer Callouts

- **`gradientTitle`, `gradientText`, and `rgb` were removed.** `gradientTitle` was
  already dead after Task 2.0 removed its only caller. Its helper `gradientText`
  (and in turn `rgb`) had exactly one remaining caller — the chat's
  `statusLineView` streaming label — which this task deletes. With no callers left,
  all three were removed from `styles.go` (and the now-unused `fmt`/`strings`
  imports pruned). `GradFrom`/`GradTo` theme tokens are retained as exported
  struct fields (harmless, consistent with other unused tokens).
- **Textarea border suppression via a shared option.** Rather than blanking the
  border at the chat call site (which would fight the shared `SetStyles` setup),
  `newInputTextarea` gained a `withoutBorder()` variadic option backed by a new
  `theme.Textarea.Borderless` style. The monitor/gate callers are unchanged (they
  still get the bordered textarea), so no double-box regression is possible there.
- **`StatusLine`/`TurnIndicator` theme fields are now unused** (their only
  consumers were the removed chat methods). They are left in the `Styles` struct
  as inert tokens to avoid scope creep into unrelated theme surface; Go permits
  unused struct fields, so build/vet stay clean. A future cleanup could drop them.
- **The spinner field is retained.** `chatModel.spinner` is still ticked in
  `Update` (streaming logic unchanged per the task constraint) but is no longer
  rendered, since the streaming state now lives in the Message panel title.
