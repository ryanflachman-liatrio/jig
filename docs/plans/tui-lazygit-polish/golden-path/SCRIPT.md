# Golden-path script — TUI Lazygit polish

**Goal:** Cold start → answer a gate → return Home in ≤60s, keys only.  
**Workflow:** `.agents/jig/golden-path.toml` (command + review; no live agent).  
**Contracts:** Esc/`q` leave Monitor from Steps → Home (A3/C4). First wait
auto-focuses Gate (A5).

Use this script when reviewing any TUI change that touches navigation, focus,
footer, or review chrome. Failures are itemized UX bugs, not “feels off.”

## Preconditions

- Repo root is the working directory
- `go run ./cmd/jig` (or built `./jig`) available
- Terminal ≥ 100 cols recommended; for review embed shots use ≥ 160 cols
- No prior hung jig process holding the TTY

## Key-by-key

```text
1. jig                              → Home (first workflow auto-selected; A1)
2. / then type golden-path, enter   → filter workflows (optional if already first)
3. j/k until golden-path highlighted → workflows pane
4. r                                → run starts; Monitor opens
5. observe Steps + Transcript       → seed command runs; follow LIVE if shown
6. wait for gate                    → Gate chrome: [GATE] · awaiting review
                                      (first wait auto-focuses Gate)
7. enter                            → [REVIEW] workspace opens
                                      (≥160: Steps stay visible beside Review)
8. j/k / { } browse docs (optional) → confirm panel chrome
9. S                                → summary / decision
10. 1                               → choose accept
11. enter                           → submit review
12. wait for run settle             → Steps show gate done
13. q                               → leave Monitor → Home; run listed
```

Alternate short path (skip workspace): at step 6, if the compact gate offers
`1-9 decision` without opening review, press `1` to accept — still counts as
answering the gate. Prefer `enter` → Review when validating 2.1 chrome.

## Esc ladder smoke (optional add-on)

```text
From Steps focus:  esc → Home
From Transcript:   esc → Steps; q → Home
From open Review:  esc → Gate (still in Monitor); q from Steps → Home
Dirty compose + q/esc-leave → confirm modal (A6)
```

## Simple mode check

Default footer tag is `simple`. Advanced chords (`/`, `F`, `[`, `]`, `o`, `n/N`)
are absent from the footer but remain in `ctrl+k`. Toggle with `ctrl+shift+a`
(persists in `.jig/tui.json`).

## Reference frames to capture

Text proofs (regenerate with `go test ./internal/tui -run TestGoldenPathProofFrames`):

| Shot | File |
|---|---|
| 1 Home dual pane | [01-home.txt](01-home.txt) |
| 2 Monitor LIVE | [02-monitor-live.txt](02-monitor-live.txt) |
| 3 Gate needs input (blurred) | [03-gate-blurred.txt](03-gate-blurred.txt) |
| 4 Gate focused | [04-gate-focused.txt](04-gate-focused.txt) |
| 5 Review embedded wide ≥160 | [05-review-wide.txt](05-review-wide.txt) |
| 6 Footer simple mode | [06-footer-simple.txt](06-footer-simple.txt) |

Optional: replace with screenshots/asciinema under the same basenames (`.png` / `.cast`).


## PR checklist snippet

Copy into TUI PRs that touch navigation / focus / footer / review:

```markdown
### TUI golden-path check
- [ ] Ran `docs/plans/tui-lazygit-polish/golden-path/SCRIPT.md` against `golden-path.toml`
- [ ] No unexpected Esc / focus jump / missing footer verb
- [ ] First gate wait was visually obvious without opening `?` more than once
- [ ] Leave Monitor (Esc/`q` from Steps) returned to Home
```
