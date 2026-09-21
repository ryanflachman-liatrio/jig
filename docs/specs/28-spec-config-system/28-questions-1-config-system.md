# 28 Questions Round 1 - Config System

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

## 1. What settings does v1 actually cover?

Jig has no general config today. Three settings already live in ad-hoc per-project files: `.jig/notifications.toml` (TOML, `BurntSushi/toml`), `.jig/tui.json` (JSON: `SimpleMode`, `CompactToolGroups`), and telemetry (env-vars only, deliberately OTel-standard). There's also the `--ascii` glyph flag and harness/backend selection (ACP/Claude/Cursor, model/effort) that are currently flag- or workflow-file-driven.

- [ ] (A) New config file only — covers genuinely new settings (e.g. default harness/model/effort, default glyph preset) and leaves `tui.json`/`notifications.toml`/telemetry env-vars exactly as they are today, untouched.
- [x] (B) Consolidate everything — migrate `tui.json` and `notifications.toml` into the new `config.toml` as sub-tables, deprecate the old files, keep telemetry on env-vars (OTel convention).
- [ ] (C) New file + adopt `tui.json`'s settings only (drop the separate JSON file, keep notifications separate since it's already TOML and project-scoped by design).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**
- `(A)` keeps this spec's blast radius small and demoable: one new package, one new file format precedent already proven in the codebase (`BurntSushi/toml`), no migration/back-compat code for existing consumers.
- `(B)`/`(C)` require writing migration or dual-read logic for existing files and touch `internal/tui/prefs` and `internal/notification` consumers directly — that's a second spec's worth of work and risk (silent precedence bugs between old and new file if done partially).
- If you already know you want `tui.json` folded in, `(C)` is the smallest safe version of that — say so and I'll scope it in.

## 2. Where do the user and project config files live?

- [x] (A) User: `~/.config/jig/config.toml` (XDG, matches yazi/lazygit). Project: `.jig/config.toml` (matches jig's existing `.jig/` convention for `notifications.toml`, `tui.json`, worktrees).
- [ ] (B) User: `~/.jigrc` (dotfile style, no subdirectory). Project: `jig.toml` at repo root (visible, not hidden).
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**
- `(A)` follows XDG convention (yazi, lazygit both do this) and reuses the `.jig/` directory jig already established for project-local state, so `jig init`'s existing `.gitignore` handling and directory creation can be extended rather than duplicated.
- `(B)` invents two new location conventions the codebase doesn't otherwise use.

## 3. How does project config override user config?

- [x] (A) Deep merge per-key: project file only needs to declare the keys it wants to change; unset keys fall through to user config, which falls through to built-in defaults (yazi/lazygit/nushell pattern).
- [ ] (B) Whole-file replace: if a project `config.toml` exists, it fully replaces the user config (no merging).
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**
- `(A)` is what every researched tool (yazi, lazygit, nushell) does and is why their docs explicitly tell users "only put the keys you want to change" in overrides — it's the whole point of having a layered config instead of one file per level.
- `(B)` forces every project file to duplicate the full user config to avoid losing settings, which defeats the purpose of an override layer and is exactly the anti-pattern those tools' docs warn against.

## 4. Should CLI flags/env vars still win over both config files?

- [x] (A) Yes — precedence is built-in defaults → user config → project config → env vars → CLI flags (flags always win, matching current jig behavior where flags are explicit per-invocation intent).
- [ ] (B) No — config file values win even over flags, once set.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**
- `(A)` matches the universal convention (and jig's own existing per-invocation flags like `--ascii`) that the most specific, most recently expressed intent wins — a one-off flag shouldn't be silently overridden by a config file.
- `(B)` would surprise users who pass an explicit flag and see it ignored.

## 5. Do we need a `jig config` subcommand in this spec?

- [x] (A) Yes, minimal: `jig config show` prints the fully-merged effective config (with a note on which layer each value came from, if easy; otherwise just the merged values) — this is the debugging affordance every researched tool considers essential.
- [ ] (B) No command in this spec — just the loading/merging library, wired into existing call sites; a `config show`/`config init` UX can be a follow-up spec.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**
- `(A)` is cheap (the merge logic already produces a struct; printing it is a small addition) and directly addresses the #1 cross-cutting best practice from research (ghostty's `+show-config`, nushell's `$env.config`) — without it, users have no way to debug why a setting isn't taking effect.
- Deferring to `(B)` saves little effort now but leaves the feature without a way to verify it works end-to-end for the Proof Artifacts in this spec.

## 6. Which settings should the new config actually expose in v1 (given answer to Q1)?

Assuming Q1 = (A) (new settings only, not migrating existing files):

- [ ] (A) Harness/backend defaults only: default harness (ACP/Claude/Cursor), default model, default effort — these are currently only settable per-workflow-file or per-flag, and choosing one has clear user-level "I always want X" value.
- [ ] (B) Harness/backend defaults + default glyph preset (replaces the `--ascii` flag's implicit default) + a `[ui]` table reserved for future TUI-level settings (empty/documented placeholder in v1).
- [ ] (C) Other (describe) — e.g. narrower (just harness) or broader (also security/sentinel settings from spec 10, which the codebase survey couldn't confirm even have a config struct yet).
- [x] (D) I don't have a firm list yet — let the spec propose 2-3 settings as the minimum "just right" slice and treat the rest as an explicit non-goal/future-work list.

**Recommended answer(s):** [(D)]

**Why these are recommended:**
- `(D)` keeps the spec honest about what's actually validated end-to-end (a small number of real settings) while making room for the config *system* itself — the loader, merge logic, precedence, `config show` — to be the real deliverable, which is what "a dedicated config for jig" is asking for.
- `(A)`/`(B)` lock in specific settings before we've confirmed jig's actual pain points; if you already know exactly which settings matter most to you day-to-day, say so and I'll use that as the fixed list instead of `(D)`'s proposal.
