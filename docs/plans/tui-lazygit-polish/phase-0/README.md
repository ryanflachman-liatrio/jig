# Phase 0 — Happy path

Make the primary operator loop obvious before adding chrome.

| Item | Title | Visual | File |
|---|---|---|---|
| 0.1 | Flatten home navigation | Yes | [0.1-flatten-home-navigation.md](0.1-flatten-home-navigation.md) |
| 0.2 | Gate first-wait attention | Yes | [0.2-gate-first-wait-attention.md](0.2-gate-first-wait-attention.md) |
| 0.3 | Context footer actions | Yes | [0.3-context-footer-actions.md](0.3-context-footer-actions.md) |
| 0.4 | Unified Esc back | Behavior | [0.4-unified-esc-back.md](0.4-unified-esc-back.md) |

## Exit criteria

- Home is the only pre-run surface required for start → monitor
- First pending gate is visually and focus-obvious
- Footers never lie about enabled actions
- Esc is a one-level stack; `q` leaves Monitor

## Suggested order

`0.4` can land in parallel with `0.3`. `0.1` first if it unblocks empty-state copy.
`0.2` after Monitor leave/enter still stable post-`0.1`.
