# Plan: TUI Lazygit-grade polish

**Status:** Proposed  
**Risk:** Medium (mostly TUI; Phase 0 touches navigation contracts)  
**Primary packages:** `internal/tui`, `internal/tui/monitor`, `internal/tui/runs`,
`internal/tui/selector`, `internal/tui/detail`, `internal/tui/review`,
`internal/tui/shared`  
**Motivation:** jig's TUI has solid bones (titled panels, focus borders,
contextual footers, `?` help) but still feels like a capable prototype rather
than Lazygit. The gap is cognitive load: too many screens, remapped keys by
focus, and waiting states that are easy to miss.

## North star

One composition. Focus always obvious. Waiting state impossible to miss. Keys
match what is on screen. Esc always means "back one level."

```text
╭─ run · feature › Steps ─╮╭─ implement › Transcript · LIVE ─╮
│ ▌ plan      ● running   ││ assistant prose…                 │
│   implement ○ waiting   ││ ▸ Tool Read path.go              │
│   review    ○           ││                                  │
╰─────────────────────────╯╰──────────────────────────────────╯
╭─ GATE · awaiting review ────────────────────────────────────╮
│ 1 approve  2 revise  3 abort     enter open review           │
╰─────────────────────────────────────────────────────────────╯
  awaiting review · tab panels · enter open · ? more · ctrl+c quit
```

## Benchmarks to steal from

| TUI | Steal |
|---|---|
| **Lazygit** | Always-visible panels, context footer, one-level Esc, modal confirm |
| **k9s** | `:` command palette for the long tail |
| **Helix** | “Which keys work next?” discoverability |
| **Crush** | Closest agent-domain peer on Charm stack |

## Phase map

| Phase | Goal | Items |
|---|---|---|
| **0 — Happy path** | Make the primary loop obvious | [phase README](phase-0/README.md) · [0.1](phase-0/0.1-flatten-home-navigation.md) · [0.2](phase-0/0.2-gate-first-wait-attention.md) · [0.3](phase-0/0.3-context-footer-actions.md) · [0.4](phase-0/0.4-unified-esc-back.md) |
| **1 — Lazygit feel** | Status, focus, commands, empty states | [phase README](phase-1/README.md) · [1.1](phase-1/1.1-persistent-status-line.md) · [1.2](phase-1/1.2-panel-focus-badges.md) · [1.3](phase-1/1.3-command-palette.md) · [1.4](phase-1/1.4-empty-states-cta.md) |
| **2 — Remaining friction** | Unify chrome, simplify, dogfood | [phase README](phase-2/README.md) · [2.1](phase-2/2.1-unify-review-panel-chrome.md) · [2.2](phase-2/2.2-breadcrumb-truncation.md) · [2.3](phase-2/2.3-simple-mode.md) · [2.4](phase-2/2.4-golden-path-demo-dogfood.md) |

End-state visual: [TARGET-COMPOSITION.md](TARGET-COMPOSITION.md).

Ship Phase 0 before Phase 1. Phase 2 can overlap Phase 1 once 0.3/0.4 land
(footer + Esc contracts stabilize chrome work).

## Non-goals

- Reworking the engine, transcript JSONL schema, or harness selection
- Theme redesign / light mode
- Mouse-first interaction (keyboard remains primary)
- Preserving every current key chord forever (pre-v1; prefer correct UX)

## Success criteria (program-level)

1. A new operator can pick a workflow, start a run, answer a gate, and leave the
   monitor without opening `?` more than once.
2. Every focused region advertises only currently valid actions in the footer.
3. Esc never jumps two navigation levels.
4. A pending gate is visually dominant within one second of first wait.
5. Review workspace uses the same panel/footer grammar as the rest of Monitor.

## How to use these files

Each item file is an implementable unit: problem, decisions, surfaces, acceptance
checks, and (when visual) before/after ASCII frames. Prefer opening a formal
`docs/specs/18-…` only after Phase 0 decisions are accepted.
