# Plan: TUI Lazygit-grade polish

**Status:** Decisions locked — see [DECISIONS.md](DECISIONS.md)  
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
match what is on screen. Esc/`q` leave Monitor to Home; Esc dismisses inward
layers when nested.

```text
╭─ ab12 › Steps ────────────╮╭─ implement › Transcript ─────────╮
│ ▌ plan      ● running     ││ assistant prose…                 │
│   implement ○ waiting     ││ ▸ Tool Read path.go              │
│   review    ○             ││                                  │
╰───────────────────────────╯╰──────────────────────────────────╯
╭─ [GATE] · awaiting review ────────────────────────────────────╮
│ 1 approve  2 revise  3 abort     enter open review           │
╰─────────────────────────────────────────────────────────────╯
 ab12cd34 · feature · review · 18.1k tok · $0.12
  awaiting review · 1-9 decision · enter open · ? more
```

## Benchmarks to steal from

| TUI | Steal |
|---|---|
| **Lazygit** | Always-visible panels, context footer, one-level Esc, modal confirm |
| **k9s** | `ctrl+k` command palette for the long tail |
| **Helix** | “Which keys work next?” discoverability |
| **Crush** | Closest agent-domain peer on Charm stack |

## Phase map

| Phase | Goal | Items |
|---|---|---|
| **0 — Happy path** | Make the primary loop obvious | [phase README](phase-0/README.md) · [0.1](phase-0/0.1-flatten-home-navigation.md) · [0.2](phase-0/0.2-gate-first-wait-attention.md) · [0.3](phase-0/0.3-context-footer-actions.md) · [0.4](phase-0/0.4-unified-esc-back.md) |
| **1 — Lazygit feel** | Status, focus, commands, empty states | [phase README](phase-1/README.md) · [1.1](phase-1/1.1-persistent-status-line.md) · [1.2](phase-1/1.2-panel-focus-badges.md) · [1.3](phase-1/1.3-command-palette.md) · [1.4](phase-1/1.4-empty-states-cta.md) |
| **2 — Remaining friction** | Unify chrome, simplify, dogfood | [phase README](phase-2/README.md) · [2.1](phase-2/2.1-unify-review-panel-chrome.md) · [2.2](phase-2/2.2-breadcrumb-truncation.md) · [2.3](phase-2/2.3-simple-mode.md) · [2.4](phase-2/2.4-golden-path-demo-dogfood.md) · [golden-path](golden-path/) |

End-state visual: [TARGET-COMPOSITION.md](TARGET-COMPOSITION.md).  
Locked choices: [DECISIONS.md](DECISIONS.md).

**Phase 0 order:** `0.1 → 0.4 → 0.3 → 0.2` (Home before leave semantics;
footer before first-wait chrome). Phase 2 may overlap Phase 1 once 0.3/0.4 land,
with item-level deps in each phase README.

**Process:** one plan item ≈ one PR. After these locks, open formal
`docs/specs/18-…` for Phase 0 nav/focus contracts before coding Phase 0.

## Non-goals

- Reworking the engine, transcript JSONL schema, or harness selection
- Theme redesign / light mode
- Mouse-first interaction (keyboard remains primary)
- Preserving every current key chord forever (pre-v1; prefer correct UX)
- Implementing ADR 0004 Screen interface in this program (amend ADR only)
- Redesigning help-agent (`ctrl+\`) or security-summary chrome (document Esc only)
- Bringing `internal/tui/chat` into the root graph

## Intentional contract changes (still TUI-facing)

- Root routing becomes **Home ↔ Monitor** (Detail/Runs demoted)
- New **`.jig/tui.json`** for simple-mode preference (Phase 2.3; under the existing `.jig/` tree)

## Success criteria (program-level)

1. A new operator can pick a workflow, start a run, answer a gate, and leave the
   monitor without opening `?` more than once (deterministic demo workflow).
2. Every focused region advertises only currently valid actions in the footer.
3. Esc dismisses one inward layer; from Steps, Esc/`q` both return Home (with
   dirty-buffer confirm when needed).
4. A pending gate is visually dominant within one second of first wait.
5. Review workspace uses the same panel/footer grammar as the rest of Monitor
   (wide terminals ≥160 cols keep Steps visible).

## How to use these files

1. Read [DECISIONS.md](DECISIONS.md) — authoritative locks.
2. Each item file is an implementable unit: problem, decisions, surfaces,
   acceptance checks, before/after frames.
3. Prefer opening formal `docs/specs/18-…` for Phase 0 after these locks; Phases
   1–2 may stay plan-driven until promoted.
