# Spec 18 — TUI Lazygit Phase 0 (nav / focus contracts)

**Status:** Implementation  
**Plan:** [`docs/plans/tui-lazygit-polish/`](../../plans/tui-lazygit-polish/)  
**Decisions:** [`DECISIONS.md`](../../plans/tui-lazygit-polish/DECISIONS.md) (authoritative)  
**Order:** `0.1 → 0.4 → 0.3 → 0.2`

## Summary

Make jig’s primary operator loop obvious: **Home ↔ Monitor**, honest Esc/`q`
ladder, footers that only advertise live actions, and first-wait gate focus for
every gate kind.

## Contracts

### Home (0.1)

- Cold start lands on **Home** (workflows | runs). Auto-select first workflow;
  right pane lists that workflow’s runs (including hydrate-on-startup).
- `enter` on workflow → focus Runs pane. `d` on Workflows → Detail/chart
  **overlay**. `d` on Runs → delete confirm. `enter` on run → Monitor. `r` /
  `R` / `/` preserved. Narrow terminals stack panes vertically.
- Empty runs CTA is local (“Press r to start a run”), never “go to Detail”.
- Leave-Monitor destination is Home. Amend ADR 0004; do **not** implement the
  Screen interface in this change.

### Esc / leave (0.4)

```text
compose/edit (dirty → confirm) → review browse → gate → Steps
Steps Esc/`q` → Home
Transcript Esc → Steps; Transcript `q` → Home
help / help-agent / delete confirm / reset confirm: Esc closes that layer only
```

Drop `h` / `backspace` as Monitor leave keys.

### Footers (0.3)

- First help section for current focus = compact footer source (Home + Monitor).
- Order: primary verb → navigation → secondary → `more`. Never lead with quit.
- Remapped keys update labels in the same Update cycle as enablement.
- At 80 cols: ≥3 contextual actions + more when contextual actions exist.

### First-wait gate (0.2)

- `FocusPendingInput` / `focusInputOnArrival` consumed by **all** gate kinds once
  after enter/resume. Later arrivals do not steal focus (ADR 0002).
- Focused pending: `[GATE] · …` (Charple). Blurred pending: `GATE · needs input`
  (Iron border). Queue depth `(N pending)` when `len(queue) > 1`.
- Footer/status never reads only `running` while any gate is pending.

## Non-goals

Engine/transcript/harness changes; theme redesign; ADR 0004 Screen interface;
help-agent IA redesign; `internal/tui/chat` in the root graph.
