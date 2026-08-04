# Bug: gate navigation-freeze — code contradicts ADR 0002 and CONTEXT.md

## Summary

[ADR-0002](adr/0002-gates-are-nonblocking-focus-regions.md) and the `CONTEXT.md`
**Gate** definition both state that a pending gate does **not** freeze navigation:
the user can move focus between the Steps and Transcript panels to read the context
a verdict is *about* while the gate waits, and "the earlier navigation-freeze was a
bug this feature removes." **The code does the opposite for every gate kind.** The
non-blocking design described in the docs was never implemented, or has regressed.

## Evidence

In `internal/tui/monitor.go`, `monitorModel.Update`'s `tea.KeyPressMsg` case opens
with a chain of early-return guards, one per pending gate, each intercepting *all*
keys while its gate is pending:

- `pendingInput != nil` — ~l.227: `enter` submits, `esc` leaves to the runs list,
  every other key is routed to the textarea. Returns early.
- `pendingQuestion != nil` — ~l.254: digit keys select/toggle, `enter` confirms,
  `esc`/`q` cancels to the runs list, all else swallowed by `return m, nil` (l.305).
- `pendingPrompt != nil || composingMessage` — ~l.308: `enter` submits, `esc`
  cancels compose, all else routed to the textarea. Returns early.
- `pendingReview != nil` — ~l.364: digit keys pick a verdict, `m` composes a
  message, `esc`/`q` leaves to the runs list, all else swallowed by
  `return m, nil` (l.391).

There is no `focusedRegion` state, and these guards run at the top of the key handler
before any navigation code, so **no key can move focus to the Steps or Transcript
panels while a gate is pending.** The only exit is `esc`/`q`, which leaves the
monitor entirely for the runs list rather than moving focus within it.

The comment at l.361–363 ("A pending review freezes navigation — only a verdict,
message compose, or esc … is accepted") is therefore *accurate about the code* — it
is the documentation (ADR-0002, `CONTEXT.md`) that describes an unbuilt behavior.

## Fix direction

Resolved by the monitor state-machine cleanup agreed in the TUI pattern review:

1. Introduce an explicit `focusedRegion` (Steps | Transcript | Gate).
2. Collapse the four `pending*` pointers into one discriminated gate-state field
   (exactly one gate kind is ever active).
3. Route keys per focused region through a single `transition()` point that owns
   focus/blur/reset side-effects.
4. Auto-focus the gate the moment it appears — so answering stays a zero-navigation
   default — while allowing `tab`/`left`/`right` to move focus to the panels to read
   context, exactly as ADR-0002 intends.
5. Delete the now-redundant l.361–363 comment as part of the change.

Until then, either the code or the docs is lying; ADR-0002 and `CONTEXT.md` reflect
the intended design, so the code is what should change.
