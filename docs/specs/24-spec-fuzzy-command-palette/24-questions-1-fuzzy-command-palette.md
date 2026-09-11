# 24 Questions Round 1 - Fuzzy Command Palette

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

## 1. Scope of "richer named actions"

`docs/plans/open-goals.md` (B3) describes today's palette (`internal/tui/palette`) as "substring + key-redispatch" and asks for "richer named actions." The current `Command` struct only carries a display `Binding` and a `Key` string that gets re-dispatched as a keypress (`internal/tui/palette/palette.go:19-25`), and the catalog is built purely from each screen's existing keybindings (`FromBindings`, `internal/tui/palette/palette.go:196-221`). There's no way to list a command that isn't already bound to a single key on the active screen.

What should "richer named actions" mean for this spec?

- [ ] (A) Infrastructure only: generalize `Command` to support direct execution (e.g. a `Run func() tea.Cmd` field, as originally sketched in `docs/plans/tui-lazygit-polish/phase-1/1.3-command-palette.md`) alongside the existing key-redispatch path, but don't surface any new commands in the catalog this round — Home/Monitor still only show their current keybindings through the palette.
- [ ] (B) Add a small curated set of new global actions to the existing Home + Monitor palette scope (e.g. "Go to Home", "Go to Monitor", "Toggle simple/advanced mode", "Show help") that aren't tied to a single-key binding today, using the new direct-execution path.
- [x] (C) Same as (B), and also extend the `ctrl+k` palette itself to open from the remaining screens that don't have it today (runs list/selector, step detail overlay, review), surfacing each screen's existing keybindings there too.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(B)]

**Why these are recommended:**

- (A) is real plumbing work but produces nothing a user can see or demo — it fails the spec's own bar of an end-to-end, demoable slice.
- (B) delivers the visible "richer" value the goal calls for while staying inside the palette's already-locked v1 scope (Monitor + Home; see the locked decisions in `docs/plans/tui-lazygit-polish/phase-1/1.3-command-palette.md`), keeping this a single, appropriately-sized spec.
- (C) bundles a second, separable project (closing the screen-coverage gap for the remaining screens) into this spec. That coverage gap is real but is its own vertical slice and risks pushing this spec toward "too large" (multiple interconnected surfaces). It can follow as a fast, low-risk follow-up once (B) lands the direct-execution plumbing it would reuse.
- If you want the screen-coverage work included now anyway, say so explicitly and it will be added as a third Demoable Unit.

## 2. Fuzzy ranking and match highlighting

Today `refilter` does an ordered `strings.Contains` scan and keeps catalog order (`internal/tui/palette/palette.go:118-135`). `github.com/sahilm/fuzzy` is already an indirect dependency (pulled in by `charm.land/bubbles/v2`'s own list filter), and it returns both a match score and the matched rune indexes per candidate — the same building blocks `fzf`, `k9s`, and Bubbles' own list filtering use to rank results and highlight why something matched.

How should the fuzzy upgrade behave?

- [x] (A) Rank visible commands by fuzzy match score (best match first, replacing today's fixed catalog order while filtering) and highlight the matched characters inline in each row's title — the standard `fzf`/`k9s` feel.
- [ ] (B) Keep the existing catalog order (grouped by section) and use fuzzy matching only to decide which commands are included, without reordering or highlighting matches.
- [ ] (C) Rank by fuzzy score like (A), but skip character highlighting (score affects order only, no visual diff in the row).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- (A) is the actual behavior the goal is naming when it references `fzf` in `docs/plans/open-goals.md` — order-preserving inclusion-only filtering (B) is barely different from today's substring behavior and wouldn't read as a real upgrade.
- Match highlighting is nearly free here: `sahilm/fuzzy.Match` already returns the matched indexes, so (A) doesn't add meaningful implementation cost over (C) while giving the operator visible feedback on *why* a row matched — the detail that makes fuzzy pickers feel trustworthy instead of "sometimes finds things."
- (C) is a reasonable fallback if inline highlighting turns out to clash with the existing `theme.SelectedLine` / `theme.Help.Key` styling, but that's a rendering detail to resolve during implementation, not a reason to skip ranking.

## 3. Match targets for filtering

Today filtering only looks at `Title` and `Binding` (`internal/tui/palette/palette.go:124-127`). Should the fuzzy filter also match against the command's section/category label (the `prefix` passed to `FromBindings`, e.g. "Step", "Run"), so typing a category name like "run" surfaces all run-related commands even if "run" isn't in the title itself?

- [x] (A) Yes — include the section/category label as part of the fuzzy match text for each command.
- [ ] (B) No — keep matching scoped to Title + Binding only, same fields as today.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- (A) costs nothing extra to implement (the category is already available at catalog-build time in `paletteCommands`/`FromBindings`) and matches how `k9s` and most command palettes let you search by category as well as name.
- (B) is safer if category words are noisy or overlap heavily with titles, but nothing in the current catalog suggests that risk; it can be tightened later if false-positive matches turn out to be a real problem.
