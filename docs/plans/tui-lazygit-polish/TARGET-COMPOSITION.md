# Target composition (north-star visual)

Reference frame after Phases 0–2. Individual items may land subsets; this is the
intended end state. Locked by [DECISIONS.md](DECISIONS.md) (C2: titles match
2.2 slim crumbs; C1: status ≠ footer state).

**Conventions**

- Short run id = 8 hex (`ab12cd34`) in status; titles may use a shorter prefix.
- Status line = identity + tokens/cost (no run-state word).
- Footer = state + keys only.
- Focused region: `[NAME]` badge; blurred pending gate: `GATE · needs input`
  (Iron border — not warning border).
- Security summary strip (if present) sits above the gate bar; not redesigned here.

## Monitor — running, transcript focused

```text
╭─ ab12 › Steps ────────────╮╭─ implement › [TRANSCRIPT] ───────╮
│   plan      ✓ done        ││                                    │
│ ▌ implement ● running     ││  Operator guidance…                │
│   review    ○ pending     ││                                    │
│                           ││  Assistant prose that stays        │
│                           ││  readable as primary content.      │
│                           ││                                    │
│                           ││  ▸ Tool Read internal/tui/root.go  │
│                           ││                                    │
╰─ Iron ────────────────────╯╰─ Charple ──────────────────────────╯
 ab12cd34 · feature · implement · 12.4k tok · $0.08 · LIVE
  running · j/k scroll · f follow · enter expand · esc home · ? more
```

## Monitor — first gate wait (gate focused)

```text
╭─ ab12 › Steps ────────────╮╭─ review › Transcript ─────────────╮
│   plan      ✓             ││ … prior output …                  │
│   implement ✓             ││                                   │
│ ▌ review    ● waiting     ││                                   │
╰─ Iron ────────────────────╯╰─ Iron ────────────────────────────╯
╭─ [GATE] · awaiting review ─────────────────────────────────────╮
│ 1 approve   2 revise   3 abort                                 │
│ enter open review workspace                                    │
╰─ Charple ──────────────────────────────────────────────────────╯
 ab12cd34 · feature · review · 18.1k tok · $0.12
  awaiting review · 1-9 decision · enter open · esc home · ? more
```

## Monitor — later wait (transcript focused; gate blurred but loud)

```text
╭─ ab12 › Steps ────────────╮╭─ implement › [TRANSCRIPT] ────────╮
│   implement ● running     ││ ▌ assistant: looking at tests…   │
│   review    ○             ││ ▸ Tool Grep TestFlatten          │
╰─ Iron ────────────────────╯╰─ Charple ─────────────────────────╯
╭─ GATE · needs input (2 pending) ───────────────────────────────╮
│ Agent asked a question — tab to answer                         │
╰─ Iron ─────────────────────────────────────────────────────────╯
 ab12cd34 · feature · implement · 12.4k tok · $0.08
  awaiting answer (2 pending) · tab gate · j/k scroll · ? more
```

## Home (cold start auto-selects first workflow)

```text
╭─ Workflows ──────────────────╮╭─ Runs · feature ───────────────────────╮
│ ▌ feature.toml               ││ ▌ ab12cd34  running  2/5               │
│   hotfix.toml                ││   98fe76aa  done     5/5               │
╰──────────────────────────────╯╰────────────────────────────────────────╯
  j/k move · tab pane · enter open · r new run · R resume · d detail · / filter · ? help · ctrl+c quit
```

## Review embedded (wide ≥ 160 cols)

```text
╭─ ab12 › Steps ─╮╭─ [REVIEW] · design.md · 1/3 · 2 comments ─╮
│ ▌ review ●     ││ ▌ ○ design.md   2                         │
│                ││   ○ tasks.md                              │
│                ││ ─ source ───────────────────────────────  │
│                ││ ▌ 14 │ export func …                      │
╰────────────────╯╰───────────────────────────────────────────╯
 GATE · awaiting review · workspace open
 ab12cd34 · feature · review · 18.1k tok · $0.12
  S finish · j/k move · c comment · esc close · ? more
```

## Review full-width (&lt; 160 cols)

```text
╭─ [REVIEW] · design.md · 1/3 ───────────────────────────────────╮
│ ▌ ○ design.md                                                  │
│   ○ tasks.md                                                   │
│ ─ source ───────────────────────────────────────────────────── │
│ …                                                              │
╰────────────────────────────────────────────────────────────────╯
 ab12cd34 · feature · review · 18.1k tok · $0.12
  S finish · j/k move · esc close · ? more
```
