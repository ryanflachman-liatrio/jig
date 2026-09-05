# Target composition (north-star visual)

Reference frame after Phases 0–2. Individual items may land subsets; this is the
intended end state.

## Monitor — running, transcript focused

```text
╭─ ab12 › Steps ────────────╮╭─ implement › [TRANSCRIPT] · LIVE ─╮
│   plan      ✓ done        ││                                    │
│ ▌ implement ● running     ││  Operator guidance…                │
│   review    ○ pending     ││                                    │
│                           ││  Assistant prose that stays        │
│                           ││  readable as primary content.      │
│                           ││                                    │
│                           ││  ▸ Tool Read internal/tui/root.go  │
│                           ││                                    │
╰─ Iron ────────────────────╯╰─ Charple ──────────────────────────╯
 ab12cd34 · feature · implement · running · 12.4k tok · $0.08
  running · j/k scroll · f follow · enter expand · esc steps · ? more
```

## Monitor — first gate wait

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
 ab12cd34 · feature · review · awaiting review · 18.1k tok · $0.12
  awaiting review · 1-9 decision · enter open · esc blur · ? more
```

## Home

```text
╭─ Workflows ──────────────────╮╭─ Runs · feature ───────────────────────╮
│ ▌ feature.toml               ││ ▌ ab12cd34  running  2/5               │
│   hotfix.toml                ││   98fe76aa  done     5/5               │
╰──────────────────────────────╯╰────────────────────────────────────────╯
  j/k move · tab pane · enter open · r new run · ? help · ctrl+c quit
```

## Review embedded (wide)

```text
╭─ ab12 › Steps ─╮╭─ [REVIEW] · design.md · 1/3 · 2 comments ─╮
│ ▌ review ●     ││ ▌ ○ design.md   2                         │
│                ││   ○ tasks.md                              │
│                ││ ─ source ───────────────────────────────  │
│                ││ ▌ 14 │ export func …                      │
╰────────────────╯╰───────────────────────────────────────────╯
 ab12cd34 · feature · review · awaiting review
  S finish · j/k move · c comment · esc close · ? more
```
