# Phase 1 — Lazygit feel

Status, focus, commands, and empty states — the “this is a real app” layer.

| Item | Title | Visual | File | Status |
|---|---|---|---|---|
| 1.1 | Persistent status line | Yes | [1.1-persistent-status-line.md](1.1-persistent-status-line.md) | Implemented |
| 1.2 | Panel focus badges | Yes | [1.2-panel-focus-badges.md](1.2-panel-focus-badges.md) | Implemented |
| 1.3 | Command palette | Yes | [1.3-command-palette.md](1.3-command-palette.md) | Implemented |
| 1.4 | Empty states with CTA | Yes | [1.4-empty-states-cta.md](1.4-empty-states-cta.md) | Implemented |

Proof frames: [proofs/](proofs/).

## Exit criteria

- Identity/cost readable on status line without decoding panel titles
- Focus answerable without relying on color alone
- Long-tail actions reachable via `ctrl+k` palette
- Empty panes teach one next key

## Suggested order

`1.4` anytime after 0.1. `1.2` anytime after Phase 0 (pairs with 0.2). `1.1`
after 0.3 (footer state semantics). `1.3` after 0.3 (shared enablement inventory).

One item ≈ one PR.
