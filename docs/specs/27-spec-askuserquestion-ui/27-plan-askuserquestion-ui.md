# Plan: AskUserQuestion panel UI redesign

Status: draft for review — not yet a committed spec. Purpose is to pick a
direction before writing the SDD workflow spec.

## Current state (as-built)

Code:

- `internal/tui/question/model.go` — `question.Model` state machine
  (`phaseField` → `phaseCustom` → `phaseReview`). `optionRows()` computes
  `m.height - 5` visible option rows; `ensureCursorVisible()` scrolls within
  that.
- `internal/tui/question/view.go` — flat text rendering: optional
  "Question X of Y" hint, `[Header]` line, prompt line, then one plain text
  line per option (`▶`/`>` cursor glyph, `[ ]`/`[x]` only for multi-select),
  an `Other…` pseudo-row if `AllowCustom`, and `↑ more` / `↓ more` hints when
  content overflows.
- `internal/tui/monitor/monitor_layout.go:gateBodyHeight()` — **the panel
  height is fixed and not derived from the current question's option
  count.** It's `max(textareaCase, reviewCase)`:
  - `textareaCase = gateHeaderRows(2) + 1 + gateTextareaRows(4) + taVFrame`
  - `reviewCase = gateHeaderRows(2) + 1 + maxReviewChoices(4) + 1 + 1`
  - The code comment explicitly excludes `inputKindQuestion` from this max
    because it's "unbounded" — it just inherits whatever height falls out
    and scrolls internally.
- `internal/tui/monitor/monitor_gate_view.go:gateOverlay()` — wraps the
  question view in the generic gate `Panel` shared by every gate kind, with
  two fixed rows on top (`[i/n] Label: subject`, `Required: …` hint).
- Multiple questions (`request.Fields`) are always **paginated one at a
  time**, never shown together, with a final review screen listing
  `Prompt: answer` for every field.

## Problems this plan addresses

1. **Height is disconnected from content.** A 2-option question and a
   10-option question get the *same* panel height today (it's driven by the
   textarea/review sizing math, not the question being shown), so short
   questions look sparse and long ones scroll immediately.
2. **No visual hierarchy.** Options are plain indented text with a cursor
   glyph; single-select and multi-select look almost identical except for
   `[ ]`/`[x]`.
3. **No at-a-glance sense of progress** across multiple questions beyond a
   text hint ("Question 2 of 4"), and no way to see more than one question
   at once even when they'd easily fit.

## Proposed height model (shared by all options below)

Replace the fixed `gateBodyHeight()` case for `inputKindQuestion` with a
per-question dynamic calculation, recomputed whenever the visible
question/cursor changes:

```
contentRows   = headerRow(0|1) + promptRows + optionCount + otherRow(0|1)
visibleRows   = min(contentRows, maxOptionBudget)
panelHeight   = gateHeaderRows + progressRow(0|1) + visibleRows + scrollHintRow(0|1) + footerPadding
maxOptionBudget = clamp(terminalHeight * 0.6, 4, 10)   // never cramped, never full-screen
```

- 2–4 options → compact panel (no wasted rows).
- 10+ options → capped at `maxOptionBudget`, internal scroll (existing
  `ensureCursorVisible` logic), `↑`/`↓ more` hints as today.
- Recompute on every `advance()`/`previous()` between fields, not just on
  terminal resize — this is the one-line fix that makes "toggle through
  answers without overflowing" actually work, independent of which layout
  option below is picked.

## Option A — Dynamic-height single question (minimal change)

Keep today's one-question-at-a-time pagination and text-based rendering,
but make the panel resize itself per question using the model above, and
add lightweight visual polish (bracketed option rows, filled marker for
single-select, checkbox for multi-select).

```
┌─ [GATE] · awaiting answer ──────────────────────────────┐
│ [2/4] Deploy target: Which environment?                 │
│ Required: choose one                                    │
│                                                          │
│  ( ) staging                                             │
│  (●) production                                          │
│  ( ) canary                                              │
│                                                          │
│ enter confirm · ↑/↓ move · b back · q cancel            │
└──────────────────────────────────────────────────────────┘
```

Same question, 11 options — panel grows only to the budget cap, then scrolls:

```
┌─ [GATE] · awaiting answer ──────────────────────────────┐
│ [2/4] Deploy target: Which environment?                 │
│ Required: choose one                    ↑ 3 more above  │
│                                                          │
│  ( ) us-west-2                                           │
│  ( ) us-east-1                                           │
│  (●) eu-west-1                                           │
│  ( ) eu-central-1                                        │
│  ( ) ap-southeast-1                                      │
│  ( ) ap-northeast-1                                      │
│                                          ↓ 4 more below  │
│ enter confirm · ↑/↓ move · b back · q cancel            │
└──────────────────────────────────────────────────────────┘
```

Multi-select variant, checkboxes instead of radio glyphs:

```
│  [x] unit                                                │
│  [x] integration                                         │
│  [ ] e2e                                                 │
│  [ ] load                                                │
│ space toggle · enter confirm · ↑/↓ move                 │
```

**Pros:** smallest diff, reuses existing state machine and scroll logic
almost verbatim, only touches `gateBodyHeight()` + `view.go` line rendering.
**Cons:** doesn't solve "several short questions could just be shown
together" — still one field per screen no matter how small.

## Option B — Stacked compact view with pagination fallback

When the *whole* answer set is small (e.g. all remaining fields together
fit under `maxOptionBudget`, a good default: ≤3 fields and ≤4 options each),
render every question in one panel as a compact stacked form instead of
paginating. Falls back to Option A's one-at-a-time view automatically once
content would overflow the budget.

All-fit case (3 short questions, shown together, tab/↑↓ moves between
question blocks, active block highlighted):

```
┌─ [GATE] · awaiting answer ──────────────────────────────┐
│ Required: answer all 3                                   │
│                                                          │
│ ▌Environment                                             │
│  (●) staging      ( ) production      ( ) canary         │
│                                                          │
│  Run tests?                                               │
│  [x] unit   [x] integration   [ ] e2e   [ ] load          │
│                                                          │
│  Notify on completion?                                    │
│  (●) yes          ( ) no                                  │
│                                                          │
│ tab/shift-tab question · ↑/↓ move · enter confirm all    │
└──────────────────────────────────────────────────────────┘
```

Overflow case (5 questions, 6 options each — falls back to per-question
pagination exactly like Option A, so the height guarantee never breaks):

```
┌─ [GATE] · awaiting answer ──────────────────────────────┐
│ [3/5] Which regions? (multi-select)                      │
│ Required: choose at least one                             │
│  [x] us-west-2                                             │
│  [ ] us-east-1                                             │
│  ...                                                        │
└──────────────────────────────────────────────────────────┘
```

**Pros:** meaningfully better for the common case of 2–3 short questions
(one screen, one `enter` to submit all); still bounded because it falls
back to per-question paging past the threshold, so it inherits the same
height guarantee as Option A.
**Cons:** two rendering code paths to maintain (stacked-form mode +
paginated mode); cross-question keyboard nav (tab between blocks, arrow
inside a block) is new interaction surface, more testing.

## Option C — Sidebar wizard (question rail + focused options)

For requests with many questions (say 4+), add a narrow left rail listing
all questions with a status glyph (`✓` answered, `▸` current, `·` pending);
the right side shows only the current question's options, sized per the
dynamic height model. Best orientation for long multi-step forms; single
questions render with no rail (falls back to Option A's layout).

```
┌─ [GATE] · awaiting answer ────────────────────────────────────┐
│ ✓ Environment      │ Run which test suites?                   │
│ ✓ Notify           │ Required: choose at least one             │
│ ▸ Test suites       │                                           │
│ · Rollout %         │  [x] unit                                 │
│ · Owner             │  [x] integration                          │
│                     │  [ ] e2e                                  │
│                     │  [ ] load                                 │
│                     │                                           │
├─────────────────────┴───────────────────────────────────────────┤
│ tab next field · space toggle · enter confirm · b back          │
└───────────────────────────────────────────────────────────────┘
```

**Pros:** best for genuinely long forms (5+ questions) — user always sees
how many are left and can jump back to fix an earlier answer without
walking the whole review screen. Rail width is fixed and small, so it
composes cleanly with the same per-question height formula on the right.
**Cons:** biggest structural change (new split-pane rendering inside the
gate panel, new focus model between rail and options, rail eats horizontal
width so narrow terminals need a collapse rule); overkill for the common
1–2 question case, so it likely needs to coexist with Option A anyway for
small requests — effectively "A, plus C when field count crosses a
threshold."

## Recommendation

Ship the **dynamic height fix** (the "Proposed height model" section) as
its own change regardless of which visual option is picked — it's the
actual fix for "overflow while toggling through answers" and is a small,
low-risk diff against `gateBodyHeight()`.

On top of that, **Option A now, Option B as a fast follow**: A is a
same-shape change to existing code and immediately fixes the overflow/
sparse-panel problem for every question. B is worth doing next because most
real `AskUserQuestion` calls are 1–3 short questions, where "one screen, one
confirm" is a real usability win. C is worth keeping on the shelf — only
build it if/when a caller starts asking 5+ questions in one gate, which
isn't observed today.

## Open questions for the next round

1. What's the actual distribution of field counts / option counts in real
   `AskUserQuestion` calls today? (Would sharpen the Option B threshold and
   confirm C isn't needed yet.)
2. Should multi-select confirm require visiting every field (today's
   `advance`/`previous` walk) or should Option B's "confirm all" from a
   stacked view skip fields the user never touched, using defaults?
3. Any terminal-width constraint we need for Option C's rail on the
   existing minimum-supported terminal size?
