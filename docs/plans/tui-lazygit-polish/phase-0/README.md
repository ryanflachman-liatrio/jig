# Phase 0 — Happy path

Make the primary operator loop obvious before adding chrome.

**Order (locked F1):** `0.1 → 0.4 → 0.3 → 0.2`

| Item | Title | Visual | File |
|---|---|---|---|
| 0.1 | Flatten home navigation | Yes | [0.1-flatten-home-navigation.md](0.1-flatten-home-navigation.md) |
| 0.4 | Unified Esc / leave to Home | Behavior | [0.4-unified-esc-back.md](0.4-unified-esc-back.md) |
| 0.3 | Context footer actions | Yes | [0.3-context-footer-actions.md](0.3-context-footer-actions.md) |
| 0.2 | Gate first-wait attention | Yes | [0.2-gate-first-wait-attention.md](0.2-gate-first-wait-attention.md) |

## Exit criteria

- Home is the only pre-run surface required for start → monitor
- First pending gate (any kind) is visually and focus-obvious on enter/resume
- Footers never advertise disabled actions; ordering + remapped labels honest
- From Steps, Esc and `q` both leave Monitor → Home (dirty-buffer confirm when needed)
- From Transcript, Esc → Steps; `q` → Home
- Nested Esc dismisses one inward layer only

## Next

Open `docs/specs/18-…` covering 0.1–0.4 contracts before implementation PRs.
