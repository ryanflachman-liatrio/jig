# Locked decisions — TUI Lazygit polish

**Status:** Locked (2026-09-05)  
**Source:** operator answers + agent recommendations after plan audit against
current `internal/tui` + ADRs 0002/0004.

Legend: **(you)** = operator choice · **(rec)** = agent recommendation accepted
into the plan.

Implementers treat this file as authoritative when an item file disagrees — then
fix the item file.

---

## A. Phase 0 navigation / focus contracts

| ID | Decision | Who |
|---|---|---|
| **A1** | Cold start **auto-selects the first workflow**; right pane shows that workflow’s runs | (you) |
| **A2** | Detail/chart becomes a **`d` overlay** (not a root screen); chart stays in that overlay | (you) |
| **A3** | Leave-Monitor destination is **Home** (not Runs). From **Steps**, **both Esc and `q` → Home**. From **Transcript**, Esc still → Steps (one level); **`q` → Home**. | (you) |
| **A4** | Drop `h` and `backspace` as Monitor leave keys; leave chords are **Esc** (Steps) and **`q`** (Steps or Transcript) only | (you) |
| **A5** | **First wait after enter/resume auto-focuses Gate for any gate kind** (`ReviewRequest`, `InputRequest`, `AgentQuestion`, `PromptRequest`, `RecoveryRequest`, `IntegrationConflictRequest`, `FinalMergeRequest`). Later arrivals while Monitor is already open do **not** steal focus (ADR 0002) | (rec) |
| **A6** | Dirty compose/review buffer + leave: **confirm modal on dirty `q`/Esc-leave** | (you) |
| **A7** | **Do not** implement ADR 0004 Screen interface inside 0.1. Keep switch-based root; **update ADR 0004** to reflect Home↔Monitor (Detail/Runs demoted) | (rec) |

### A3 interpretation

Operator chose “both Esc and `q` → Home.” Combined with C4, that means **Steps**
leave-Monitor keys are Esc **and** `q` (both → Home). **Transcript Esc remains
one-level back to Steps**; Transcript `q` → Home. This preserves the inward Esc
ladder while making leave-Monitor destination Home (not Runs).

### Why A5 (rec)

Today `FocusPendingInput` arms `focusInputOnArrival`, but only `PromptRequest`
consumes it. First `ReviewRequest` / `InputRequest` / `AgentQuestion` /
`RecoveryRequest` / etc. after an empty enter stays on Steps/Transcript — the
exact miss 0.2 claims to fix. Extending consumption to all kinds matches
“first wait impossible to miss” without revoking ADR 0002’s non-steal rule for
*subsequent* arrivals.

### Why A7 (rec)

0.1 already rewires the screen graph. Folding in a Screen-interface refactor
doubles blast radius. ADR 0004’s motivation (four switch sites) remains valid
but its “four screens” assumption is obsolete after Home — amend the ADR, ship
the interface later if duplication still hurts.

---

## B. Assumption verdicts

| ID | Verdict | Plan consequence |
|---|---|---|
| **B1** | True — empty Runs still points at Detail | 0.1/1.4 keep CTA rewrite |
| **B2** | False — “first wait” ≠ covered for Review/Input | 0.2 must extend `focusInputOnArrival` consumption beyond `PromptRequest` (A5) |
| **B3** | False as stated — `CompactHint` already skips `!Enabled()` | Rewrite 0.3 problem to: ordering, width budget, remapped labels, Home parity |
| **B4** | True — review full-body replaces Monitor | 2.1 remains valid |
| **B5** | False — narrow Monitor is single focused panel, not stacked | 0.1 Home stacking is **new**; Monitor narrow stays single-panel |
| **B6** | False — no `.jig/tui.json` yet | 2.3 introduces it (C5) |
| **B7** | Weak — `review-ui-demo.toml` is review-only | 2.4 adds/extends a deterministic cold-start→gate demo |
| **B8** | Tighten deps | See phase READMEs; 2.1 after 0.4+1.2; 2.2 after 1.1+1.2; 2.3 after 0.3+1.3 |
| **B9** | Mostly true | Call out root routing + `.jig/tui.json` as intentional contract changes |
| **B10** | True iff demo is deterministic | F4 = command/review-only golden path |

---

## C. Plan-internal contradictions

| ID | Decision | Who |
|---|---|---|
| **C1** | **Status line = identity + tokens/cost**; **footer = state + keys** (no duplicate state string) | (you) |
| **C2** | **TARGET updated now** to match end-state after 2.2 (slim titles; LIVE/unseen on status) | (you) |
| **C3** | Focused: `[GATE] · awaiting review` · Blurred pending: `GATE · needs input` (unbracketed; Iron border per E1) | (you) |
| **C4** | **0.4 changed**: Esc from Steps **leaves to Home** (same destination as `q`; not a no-op). Transcript Esc remains Steps-only. | (you) |
| **C5** | Simple mode **default ON**; persist in **`.jig/tui.json`** | (you) |

---

## D. Missing surfaces — scope

| ID | Surface | Scope | Who |
|---|---|---|---|
| **D1** | Help-agent (`ctrl+\`) | **Later** — document in 0.4 that Esc closes help-agent only; full IA with palette in a follow-up | (rec) |
| **D2** | Security summary strip | **Later** — keep existing layout reservation; TARGET notes strip may appear above gate; no redesign in this program | (rec) |
| **D3** | Delete-run confirm | **In scope** — Home runs pane keeps `d`; Esc closes confirm only (0.1 + 0.4) | (rec) |
| **D4** | Resume paused (`R`) | **In scope** — Home runs pane keeps `R` | (rec) |
| **D5** | Workflow filter `/` | **In scope** — on Home workflows pane (0.1) | (rec) |
| **D6** | All gate kinds in 0.2 | **In scope** — visuals/status/focus rules cover every kind, not review-only | (rec) |
| **D7** | Hydrate-on-startup | **In scope** — Home runs pane shows hydrated runs; acceptance in 0.1 | (rec) |
| **D8** | `internal/tui/chat` spike | **Out** | (rec) |
| **D9** | Reset confirm `y`/`n` | **In scope** — 0.4 ladder: Esc cancels confirm; does not leave Monitor | (rec) |
| **D10** | Palette vs `?` vs help-agent | **Clarify in 1.3** — `?` = reference, `ctrl+k` = run command, `ctrl+\` = help-agent (unchanged); no merge this program | (rec) |

---

## E. ADR / technical risks

| ID | Decision | Who |
|---|---|---|
| **E1** | Blurred pending gate: **title/status emphasis only**; border stays Iron. Focused gate gets Charple (and `[GATE]`). Avoids two “primary” borders fighting 1.2 | (rec) |
| **E2** | While review open: keep **stable one-line gate bar height**; change label/contents only (ADR 0002 no-reflow) | (rec) |
| **E3** | Wide embed: Steps \| Review only if total width ≥ **160**; otherwise full-width Review (Steps hidden until Esc). Document threshold in 2.1 | (rec) |
| **E4** | Palette v1: **`ctrl+k` only** (no `:` ) | (rec) |
| **E5** | Drop stale “open runs…” palette example; v1 actions retarget **Home / switch run** only after 0.1 | (rec) |
| **E6** | Config path = **`.jig/tui.json`** (matches C5; stays with other jig state under `.jig/`) | (rec) |
| **E7** | **Rewrite 0.3** problem statement per B3 | (rec) |

### Why E1

Warning-colored blurred borders compete with the focused panel’s Charple border.
Title badge + footer `tab gate` + status state (C1) already satisfy “impossible to
miss” without breaking “where do keys go?”

### Why E3

Half-panel review at ~100 cols forces review’s own docs\|source stack and feels
worse than today’s full-width workspace. A 160-col threshold preserves the
Lazygit “expand a panel” story on large terminals without regressing laptop widths.

---

## F. Process / ordering

| ID | Decision | Who |
|---|---|---|
| **F1** | Phase 0 order: **0.1 → 0.4 → 0.3 → 0.2** | (rec) |
| **F2** | After these locks: open **`docs/specs/18-…`** for Phase 0 nav/focus contracts, then implement | (rec) |
| **F3** | **One plan item ≈ one PR** | (rec) |
| **F4** | Golden path uses **deterministic** steps only (command / review) — no live agent flakiness | (rec) |

---

## G. Small locks

| ID | Decision | Who |
|---|---|---|
| **G1** | Short run id = **8 hex chars** (`ab12cd34`) in titles/status | (rec) |
| **G2** | Home `enter` on workflow = **focus Runs pane** (filtered); `d` = Detail overlay | (rec) |
| **G3** | Home `enter` on run = **open Monitor** always | (rec) |
| **G4** | Status step field: **pending gate’s step** if any gate pending; else **selected** step; else **running** step | (rec) |
| **G5** | Unseen `N new` lives on **status line only** (not panel title) | (rec) |
| **G6** | Simple-mode toggle: **`ctrl+shift+a`** + palette command “Enable/disable advanced controls” | (rec) |
| **G7** | Formal **Spec 18** for Phase 0; Phases 1–2 may stay plan-driven until promoted | (rec) |
| **G8** | Preserve muscle memory: **tab / shift-tab**, **1–9** gate decisions, **ctrl+c** quit, **ctrl+o** gate context jump. Esc/`q` remaps (leave → Home) and contextual `r` are intentional pre-v1 | (rec) |
