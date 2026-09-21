# Task 03 Proofs - Notifications config migration and `[ui] glyph_preset`

## Task Summary

This task folds `.jig/notifications.toml` into `config.toml`'s `[notifications]`
table and adds a config-driven glyph vocabulary default (`[ui] glyph_preset`)
that sits underneath the existing `--ascii` flag. `internal/notification` no
longer reads any file itself — `internal/config.Load` is now the single owner
of every on-disk config read, matching Unit 1's original design goal.

## What This Task Proves

- `[notifications]` in `config.toml` fully replaces the standalone
  `notifications.toml` file: same schema (reusing `notification.Destination`
  directly), same validation, same layered-merge behavior as every other
  table.
- `Runtime` and `jig notifications check` both resolve bindings from the
  merged `config.Config` rather than each independently reading a file from
  disk.
- `[ui] glyph_preset` is case-sensitive (`"ascii"`/`"unicode"` only) and
  composes correctly with `--ascii`: an explicit flag (true or false) always
  wins; an unset flag falls back to config; absent both, Unicode is the
  default.
- The old `notification.LoadLocalConfig` disk loader and `.jig/notifications.toml`
  path are fully retired with no remaining references outside historical
  spec docs.

## Evidence Summary

- `jig config show` displays a hand-written `[ui] glyph_preset = "ascii"` and
  `[notifications]` table exactly as configured, proving the schema loads and
  merges correctly.
- `jig notifications check` resolves the same `[notifications]` table: a
  missing secret reports `url_secret_missing`; supplying it reports `ready` —
  proving the config-sourced path (no more `deps.ReadFile` injection) behaves
  identically to the old file-based flow.
- `go test ./internal/config ./internal/notification ./cmd/jig -count=1` and
  `go vet` on the same packages both pass, including new
  `TestGlyphPresetCaseSensitivity`, `TestResolveGlyphPreset`, and
  `TestApplyResolvedGlyphPreset_SwitchesPreset` cases covering the tri-state
  flag/config precedence rules.
- The grep sweep for `notifications.toml`/`LoadLocalConfig` returns only
  historical/documentation-comment references, no live code paths.

## Artifact: `config show` round-trips `[ui]` and `[notifications]`

**What it proves:** The merged `Config` struct decodes a hand-written
`config.toml`'s `[ui] glyph_preset` and `[notifications]` table (including a
nested `[[notifications.destination]]` array reusing
`notification.Destination`) without loss.

**Why it matters:** This is the schema-level round trip every other artifact
in this task depends on.

**Command:**

```bash
cat .jig/config.toml
# [ui]
# glyph_preset = "ascii"
#
# [notifications]
# enabled = true
# [[notifications.destination]]
# id = "ops"
# type = "webhook"
# enabled = true
# url_secret = "ops-url"

JIG_SECRET_OPS_URL="https://example.invalid/hook" ./jig config show --root .jig
```

**Result summary:** `config show` printed back `glyph_preset = "ascii"` and the
full `[notifications]` table including the destination array, confirming the
TOML round-trips through `internal/config.Load` → `Merge` → `toml.Encoder`
intact.

```toml
[ui]
  glyph_preset = "ascii"

[tui]

[notifications]
  enabled = true

  [[notifications.destination]]
    id = "ops"
    type = "webhook"
    enabled = true
    url_secret = "ops-url"

[telemetry]
```

## Artifact: `notifications check` resolves bindings from the merged config, not a dedicated file

**What it proves:** `notificationsCheck` now calls `loadEffectiveConfig` and
passes `Config.Notifications.ToLocalConfig()` into `notification.Inspect`
instead of `Inspect` reading `notifications.toml` off disk itself.

**Why it matters:** This is the behavioral proof that Runtime and the CLI
share one config-loading path (Unit 1's original goal) instead of each
notification consumer owning its own file read.

**Command:**

```bash
# Same .jig/config.toml as above, no secret set yet
./jig notifications check wf.toml --root .jig
```

**Result summary:** Reports `status: not_ready` / `url_secret_missing` because
the secret env var is absent — proving the destination came from `config.toml`
(not a stray leftover `notifications.toml`, which does not exist in this demo
directory at all).

```
notification policy events: attention_required, run_failed
notifications enabled: true
status: not_ready
destination ops: type=webhook enabled=true events=[attention_required, run_failed] status=url_secret_missing
Local readiness only; delivery is not verified and desktop visibility is not guaranteed. No notifications were sent.
```

```bash
JIG_SECRET_OPS_URL="https://example.invalid/hook" ./jig notifications check wf.toml --root .jig
```

```
notification policy events: attention_required, run_failed
notifications enabled: true
status: ready
destination ops: type=webhook enabled=true events=[attention_required, run_failed] status=ready
Local readiness only; delivery is not verified and desktop visibility is not guaranteed. No notifications were sent.
```

## Artifact: Glyph preset precedence is unit-tested deterministically

**What it proves:** `config.ResolveGlyphPreset` and `applyGlobalPresetFlag`'s
tri-state result correctly implement "explicit flag always wins; unset flag
falls back to config; default is Unicode" — including the case an end-to-end
CLI smoke test cannot show, since headless run/notifications-check text output
carries no rendered glyphs to diff visually.

**Why it matters:** The flag/config precedence rule is exactly what an
end-to-end CLI run can't visibly demonstrate (no icon glyphs appear in
headless text output), so the deterministic unit-level proof is the
authoritative evidence for this behavior, per Task 3.8/3.9's requirement.

**Command:**

```bash
go test ./internal/config ./cmd/jig -count=1 -v \
  -run 'TestResolveGlyphPreset|TestApplyResolvedGlyphPreset_SwitchesPreset|TestGlyphPresetCaseSensitivity'
```

**Result summary:** All four `ResolveGlyphPreset` cases (flag-wins-ASCII,
flag-wins-Unicode, config-wins-when-unset, default-unicode) and all four
`applyResolvedGlyphPreset` round-trip cases pass, along with the five
`glyph_preset` case-sensitivity cases (`"ascii"`/`"unicode"` accepted,
`"ASCII"`/`"Unicode"`/`"nerd-font"` rejected).

```
--- PASS: TestResolveGlyphPreset (0.00s)
    --- PASS: TestResolveGlyphPreset/flag_wins_over_config_unicode_value (0.00s)
    --- PASS: TestResolveGlyphPreset/explicit_unicode_flag_wins_over_config_ascii_value (0.00s)
    --- PASS: TestResolveGlyphPreset/config_wins_when_flag_unset (0.00s)
    --- PASS: TestResolveGlyphPreset/default_is_unicode_with_no_flag_and_no_config (0.00s)
--- PASS: TestGlyphPresetCaseSensitivity (0.03s)
    --- PASS: TestGlyphPresetCaseSensitivity/ascii (0.01s)
    --- PASS: TestGlyphPresetCaseSensitivity/unicode (0.00s)
    --- PASS: TestGlyphPresetCaseSensitivity/ASCII (0.01s)
    --- PASS: TestGlyphPresetCaseSensitivity/Unicode (0.00s)
    --- PASS: TestGlyphPresetCaseSensitivity/nerd-font (0.01s)
--- PASS: TestApplyResolvedGlyphPreset_SwitchesPreset (0.00s)
    --- PASS: TestApplyResolvedGlyphPreset_SwitchesPreset/absent_keeps_Unicode (0.00s)
    --- PASS: TestApplyResolvedGlyphPreset_SwitchesPreset/ascii_flag_flips_to_ASCII (0.00s)
    --- PASS: TestApplyResolvedGlyphPreset_SwitchesPreset/explicit_unicode_flag_wins_over_config_ascii (0.00s)
    --- PASS: TestApplyResolvedGlyphPreset_SwitchesPreset/unset_flag_falls_back_to_config_ascii (0.00s)
```

## Artifact: Full package test/vet run and grep sweep

**What it proves:** Every package touched by this unit passes tests and
`go vet`, and the old `.jig/notifications.toml`/`LoadLocalConfig` surface has
no remaining live references.

**Command:**

```bash
go test ./internal/config ./internal/notification ./cmd/jig -count=1
go vet ./internal/config ./internal/notification ./cmd/jig
grep -rn "notifications.toml\|LoadLocalConfig" --include="*.go" .
```

**Result summary:** All three test suites pass; `go vet` is clean; the grep
sweep's only remaining hits are doc-comment references explaining the
migration history (`internal/config/config.go`, `internal/config/load.go`)
and a test helper's doc comment (`cmd/jig/notifications_test.go`) — no live
code path still touches the retired file or function.

```
ok  	jig/internal/config	0.728s
ok  	jig/internal/notification	2.586s
ok  	jig/cmd/jig	2.245s
```

```
cmd/jig/notifications_test.go:26:// wrapNotificationsTOML nests raw notifications.toml-shaped content (the
internal/config/config.go:61:// .jig/notifications.toml), reusing notification's own destination types
internal/config/load.go:46:// notification.LoadLocalConfig's and prefs.Path's existing join convention.
```

Two unrelated pre-existing failures were observed in the full `go test ./...`
run — `internal/harness`'s `TestTier2ObservesEveryACPHarness` (flaky ACP
subprocess timing) and `internal/tui/monitor`'s
`TestBoundaryBannerFoldsIntoClosingItemLineRange` (banner row layout) — in
packages this task never touches. Confirmed by re-running both in isolation:
the harness test passed on retry, and the monitor test fails identically on
`git stash` with this task's changes removed, so neither is a regression from
this unit.

## Reviewer Conclusion

`[notifications]` in `config.toml` now fully replaces the standalone
`notifications.toml` file, and `[ui] glyph_preset` gives `--ascii` a
persistent config-driven fallback with correct, unit-tested precedence rules.
`internal/notification` is pure schema/validation/binding-resolution logic
with zero disk I/O of its own; `internal/config` is the single owner of every
on-disk read, exactly as Unit 1 intended.
