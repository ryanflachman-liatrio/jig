# 28-tasks-config-system.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/config/config.go` | New: `Config`, `UIConfig`, `TUIConfig`, `NotificationsConfig`, `TelemetryConfig` struct definitions and `Default()`. |
| `internal/config/config_test.go` | New: unit tests for defaults, per-table shapes. |
| `internal/config/load.go` | New: path resolution (XDG user path, `.jig/config.toml` project path), single-file TOML load with sanitized, path-wrapped errors. |
| `internal/config/load_test.go` | New: missing-file-not-error, invalid-TOML `errors.Is` cases, path resolution cases. |
| `internal/config/merge.go` | New: per-key deep merge across defaults → user → project → flag layers; `Load(configFlag, root string) (Config, error)` entry point. |
| `internal/config/merge_test.go` | New: layering precedence tests (user override, project-over-user). |
| `internal/config/glyph.go` | New: `ResolveGlyphPreset(tristate, Config) shared.Preset` resolver and the tri-state type. |
| `internal/config/glyph_test.go` | New: flag/config precedence tests, enum case-sensitivity. |
| `internal/config/telemetry.go` | New: env-derived `[telemetry]` base layer + final kill-switch enforcement after merge. |
| `internal/config/telemetry_test.go` | New: env-base-layer, config-overrides-env, kill-switch-forces-off cases. |
| `docs/ARCHITECTURE.md` | Add `internal/config` package table row. |
| `cmd/jig/main.go` | Add `--config` to the pre-dispatch flag scan; change `applyGlobalPresetFlag` to return tri-state; add `config` subcommand dispatch; call the shared config-load + glyph-resolve helper once, uniformly. |
| `cmd/jig/config.go` | New: `runConfig`/`configShow` — `flag.NewFlagSet`-based `jig config show`, flat-TOML output via `BurntSushi/toml`. |
| `cmd/jig/config_test.go` | New: CLI-level tests for `config show` output and error exit codes. |
| `cmd/jig/run.go` | Load merged config for the `run` entry point (glyph preset + telemetry/notifications wiring). |
| `cmd/jig/notifications.go` | `notificationsCheck` reads `[notifications]` from merged `Config` instead of `notification.LoadLocalConfig`. |
| `cmd/jig/notifications_test.go` | Update for config-sourced notifications input. |
| `cmd/jig/wire.go` | `Runtime.resolveBindings` reads merged `Config.Notifications` instead of `notification.LoadLocalConfig`. |
| `cmd/jig/wire_test.go` | Update fixtures for config-sourced bindings resolution. |
| `cmd/jig/telemetry.go` | `setupTelemetry` consumes `Config.Telemetry` from `internal/config.Load` instead of `telemetry.LoadPrefs`. |
| `cmd/jig/preset_test.go` | Update for tri-state `applyGlobalPresetFlag` return shape. |
| `internal/notification/config.go` | Remove `LoadLocalConfig` (disk loader) and its file-reading sentinels' disk path; keep `LocalConfig`, `ParseLocalConfig`/`validate` reusable by `internal/config`. |
| `internal/notification/config_test.go` | Remove disk-loader tests; keep parse/validate tests. |
| `internal/notification/readiness.go` | `Inspect` accepts a `LocalConfig` sourced from merged `Config` rather than loading it itself. |
| `internal/notification/readiness_test.go` | Update fixtures accordingly. |
| `internal/tui/prefs/prefs.go` | Delete: `.jig/tui.json` load/save superseded by `[tui]` config. |
| `internal/tui/prefs/prefs_test.go` | Delete with the package. |
| `internal/tui/monitor/monitor_model.go` | Replace `WithPrefs(jigRoot)` with a config-sourced constructor; make `ToggleSimpleMode` session-only (no on-disk save). |
| `internal/tui/monitor/monitor_tool_group_test.go` | Update `prefs.Load`/`WithPrefs` usage to the new config-sourced entry point. |
| `internal/tui/monitor/phase2_polish_test.go` | Update `prefs.FileName`/`WithPrefs` usage to the new config-sourced entry point. |
| `internal/tui/root.go` | Add a `WithTUIPrefs(config.TUIConfig)`-style `Option` alongside `WithTelemetryMode`. |
| `internal/tui/root_update.go` | Replace the three `monitor.New(...).WithPrefs(m.manager.Root())` call sites with the config-sourced path. |
| `internal/telemetry/config.go` | Export `KillSwitchActive() bool`; keep `ResolveConfig`'s existing env/Prefs behavior unchanged for standalone callers. |
| `internal/telemetry/config_test.go` | Add `KillSwitchActive` tests. |
| `internal/telemetry/prefs.go` | Delete: `.jig/telemetry.json` `LoadPrefs`/`SavePrefs` superseded by `[telemetry]` config. |
| `docs/specs/28-spec-config-system/28-audit-config-system.md` | New: mandatory planning audit report (Phase 4). |

### Notes

- Unit tests live alongside the code they test (`_test.go` in the same package), matching repository convention.
- Use `go test ./internal/config ./internal/tui/... ./internal/notification ./internal/telemetry ./cmd/jig -count=1` for focused runs during implementation; finish with root `go build ./cmd/jig && go test ./... && go vet ./...` per `docs/TESTING.md`.
- `internal/config` may import `internal/notification` and `internal/telemetry` types/validation helpers (one direction only) to avoid duplicating schema/validation logic; neither of those packages may import `internal/config`.
- Follow `internal/notification/config.go`'s sanitized-error precedent for all `internal/config` parse/validation failures, with the one explicit exception from the spec: wrap the fixed sentinel with the offending file's path via `%w`, never the underlying parser/filesystem error.

## Tasks

### [x] 1.0 Layered Config Core: `internal/config` package, merge pipeline, `jig config show`, global `--config` flag

#### 1.0 Proof Artifact(s)

- CLI: `jig config show` with no config files present prints the built-in defaults, demonstrating defaults load correctly.
- CLI: `jig config show` with only `~/.config/jig/config.toml` set to a non-default value prints that value, demonstrating user-level override works.
- CLI: `jig config show` with both a user config and a `.jig/config.toml` setting the same key to different values prints the project value, demonstrating project overrides user.
- CLI: `jig config show` run against a config file containing invalid TOML prints a sanitized, actionable error naming the offending file's path (not its contents) and returns a non-zero exit code.
- CLI: `jig --config /tmp/alt.toml config show` prints values from `/tmp/alt.toml` merged with any present `.jig/config.toml`, demonstrating the global `--config` flag substitutes only the user-level layer.
- Test: `internal/config` package tests cover default-only, user-only, project-over-user, missing-file-is-not-error, and invalid-TOML/`errors.Is` cases.
- Diff: `docs/ARCHITECTURE.md`'s package table gains an `internal/config` row.

#### 1.0 Tasks

- [x] 1.1 Create `internal/config/config.go` defining `Config` with nested `UI`, `TUI`, `Notifications`, `Telemetry` fields (`UIConfig`, `TUIConfig`, `NotificationsConfig`, `TelemetryConfig` structs, empty/minimal for now) and a `Default() Config` returning built-in defaults (zero values except where a table has a documented non-zero default).
- [x] 1.2 Create `internal/config/load.go` with `UserConfigPath() (string, error)` (uses `os.UserHomeDir()` + `$XDG_CONFIG_HOME` fallback to `~/.config`, joins `jig/config.toml`) and `ProjectConfigPath(root string) string` (joins `root`, `config.toml` directly — `root` is already the `.jig` directory itself in every existing caller, e.g. `Runtime.root`, `runRun`'s `--root`, matching `notification.LoadLocalConfig`'s and `prefs.Path`'s existing join pattern, not `root/.jig/config.toml`). **Persistence-off guard:** `ProjectConfigPath("")` must return `""` without joining, matching `notification.LoadLocalConfig`'s `if root == "" { return ... }` precedent (`AGENTS.md`: "do not join an empty root into an unintended relative write").
- [x] 1.3 Add `ErrConfigUnreadable`/`ErrConfigInvalid` sentinels and a `loadFile(path string) (Config, bool, error)` helper in `internal/config/load.go`: returns `(zero, false, nil)` for a missing file or for `path == ""` (persistence-off/no-layer), parses present files with `github.com/BurntSushi/toml`, runs schema validation, and on failure returns `fmt.Errorf("%w: %s", ErrConfigInvalid/ErrConfigUnreadable, path)` — never wrapping the underlying parser/filesystem error.
- [x] 1.4 Create `internal/config/merge.go` with a per-key deep-merge function across `Config` values (unset/zero keys in an overlay fall through to the base) and `Load(configFlagPath, root string) (Config, error)` that composes `Default()` → user layer (flag path override or resolved `UserConfigPath()`) → project layer (`ProjectConfigPath(root)`). `root` is always the caller's actual persistence root (including `""` for persistence-off) — `Load` must never substitute a hardcoded `.jig` itself; that choice belongs to each call site (Task 1.6).
- [x] 1.5 In `cmd/jig/main.go`, extend the pre-dispatch flag scan to also recognize `--config <path>`/`--config=<path>`, stripping it from `os.Args` the same way `--ascii` is stripped, and store the resolved path for the config-load call in 1.6.
- [x] 1.6 Add a shared `loadEffectiveConfig(root string) (config.Config, error)` helper in `cmd/jig` (e.g. in `main.go` or a small new file) that calls `internal/config.Load` with the `--config` path (if any) and the **caller-supplied** `root`. Call it once from each of the four entry points, each passing the root it already owns: bare `jig` (`.jig`, before `tui.New`), `runRun` (its parsed `*root`, default `.jig` but overridable), `runNotifications`/`notificationsCheck` (its parsed `*root`), and the new `runConfig` (1.7) (its own `--root`-equivalent, default `.jig`). Do not introduce a second hardcoded `.jig` literal inside the helper itself.
- [x] 1.7 Add `cmd/jig/config.go` with `runConfig(args []string) int` dispatching `jig config show` via `flag.NewFlagSet("config show", ...)` (include a `--root` flag, default `.jig`, matching the `run`/`notifications` convention), calling `loadEffectiveConfig(*root)`, and printing the result as flat TOML via `toml.NewEncoder(os.Stdout).Encode(cfg)`; wire `"config"` into `main.go`'s subcommand switch.
- [x] 1.8 Write `internal/config` package tests (`config_test.go`, `load_test.go`, `merge_test.go`) covering: defaults-only, user-file override, project-over-user precedence, missing-file-not-error for either layer, invalid TOML producing a sanitized, path-naming, `errors.Is`-compatible error, and a persistence-off case (`root == ""`) asserting `Load` returns defaults-plus-user-layer only, with no error and no path join.
- [x] 1.9 Write `cmd/jig/config_test.go` covering `jig config show`'s default output, an override via a temp user config path (using `--config`), and the non-zero exit code on invalid TOML.
- [x] 1.10 Add an `internal/config` row to `docs/ARCHITECTURE.md`'s package table describing its responsibility and boundary (owns schema, TOML loading, layered merge; consumed by `cmd/jig`, `internal/tui`, `internal/notification` call sites, and `internal/telemetry`).
- [x] 1.11 Run `gofmt -w` on changed files and `go build ./cmd/jig && go test ./internal/config ./cmd/jig -count=1 && go vet ./internal/config ./cmd/jig`; capture the Proof Artifacts above.

### [x] 2.0 Migrate TUI Preferences into `[tui]`, remove `.jig/tui.json`

#### 2.0 Proof Artifact(s)

- CLI: `jig config show` after setting `[tui] compact_tool_groups = true` in `.jig/config.toml` shows the value.
- CLI/Screenshot: launching the TUI in that project demonstrates compact tool-group rendering is active.
- Test: `internal/config` and `internal/tui` package tests pass with `.jig/tui.json` and `internal/tui/prefs` removed from the codebase.
- CLI: `grep -rn "tui.json\|tui/prefs" --include="*.go" .` returns no matches outside historical `docs/specs`, demonstrating the old format is fully retired.

#### 2.0 Tasks

- [x] 2.1 Add `SimpleMode bool` (`toml:"simple_mode"`) and `CompactToolGroups bool` (`toml:"compact_tool_groups"`) to `TUIConfig` in `internal/config/config.go`; set `Default().TUI` to `simple_mode = true, compact_tool_groups = false`, matching `prefs.Default()` exactly.
- [x] 2.2 In `internal/tui/root.go`, add a `WithTUIPrefs(cfg config.TUIConfig) Option` that stores the resolved `[tui]` values on the root model (alongside the existing `telemetryMode string` field pattern).
- [x] 2.3 In `internal/tui/monitor/monitor_model.go`, replace `WithPrefs(jigRoot string) Model` with a config-sourced constructor (e.g. `WithTUIConfig(cfg config.TUIConfig) Model`) that sets `simpleMode`/`compactToolGroups` directly from the passed struct instead of calling `prefs.Load`.
- [x] 2.4 Update the three call sites in `internal/tui/root_update.go` (`monitor.New(...).WithPrefs(m.manager.Root())`) to use the new config-sourced constructor, threading the root model's stored `[tui]` config through.
- [x] 2.5 In `cmd/jig/main.go`, pass the merged config's `[tui]` table into `tui.New(...)` via the new `WithTUIPrefs` option (loaded through `loadEffectiveConfig()` from Task 1).
- [x] 2.6 Change `Model.ToggleSimpleMode`/`savePrefs` in `monitor_model.go` to update in-memory state only (no `prefs.Save` call); update the doc comment to state the toggle is session-only now that `config.toml` is a hand-edited file, not a runtime-write target.
- [x] 2.7 Delete `internal/tui/prefs/prefs.go` and `internal/tui/prefs/prefs_test.go`; remove the package's import from `monitor_model.go` and any other importer.
- [x] 2.8 Update `internal/tui/monitor/monitor_tool_group_test.go` and `phase2_polish_test.go` to construct monitor models via the new config-sourced entry point instead of `prefs.Load`/`prefs.FileName`/`WithPrefs`.
- [x] 2.9 Add `internal/config` tests for `[tui]` table defaults and override merge; run `go test ./internal/tui/... ./internal/config -count=1 && go vet ./internal/tui/... ./internal/config`.
- [x] 2.10 Manually verify (or script) the CLI/Screenshot Proof Artifact: set `[tui] compact_tool_groups = true` in a scratch `.jig/config.toml`, run `jig config show` to confirm the value, and launch the TUI to confirm compact rendering.
- [x] 2.11 Run the grep sweep (`grep -rn "tui.json\|tui/prefs" --include="*.go" .`) and confirm no non-historical matches remain.

### [x] 3.0 Migrate Notification Config into `[notifications]`; add `[ui] glyph_preset` default

#### 3.0 Proof Artifact(s)

- CLI: `jig config show` after setting `[notifications]` values in `.jig/config.toml` shows them merged in, demonstrating the migrated schema round-trips correctly.
- CLI: `jig config show` after setting `[ui] glyph_preset = "ascii"` in the user config shows the value, and running `jig` (any subcommand) without `--ascii` demonstrates ASCII glyphs are used by default.
- CLI: `jig --ascii=false` with `[ui] glyph_preset = "ascii"` set in config demonstrates the explicit flag still wins over config.
- Test: `internal/config` tests cover case-sensitive `glyph_preset` enum validation (`"ASCII"` rejected) and the tri-state flag/config precedence resolver.
- Test: `internal/notification` and `cmd/jig` package tests pass with `.jig/notifications.toml` and its dedicated loader removed.
- CLI: `grep -rn "notifications.toml\|LoadLocalConfig" --include="*.go" .` returns no matches outside historical `docs/specs`, demonstrating the old format is fully retired.

#### 3.0 Tasks

- [x] 3.1 Add `NotificationsConfig` fields to `internal/config/config.go` mirroring `notification.LocalConfig`'s schema (`Enabled bool`; `Destinations []Destination`-equivalent with `ID`, `Type`, `Enabled`, `URLSecret`, `BearerSecret`, reusing `notification.DestinationType` and `notification.Destination` directly rather than redeclaring the shape).
- [x] 3.2 In `internal/config/load.go`'s schema validation step, call `notification`'s existing alias/type/destination validation (export or reuse `LocalConfig.validate()`'s logic) so `[notifications]` failures produce the same sanitized `ErrConfigInvalid`-style error as any other table.
- [x] 3.3 In `internal/notification/config.go`, delete `LoadLocalConfig` (the disk-reading function) and its now-unused file-path constant; keep `LocalConfig`, `ParseLocalConfig`, and `validate()` exported/available for `internal/config` to call.
- [x] 3.4 Update `cmd/jig/wire.go`'s `Runtime.resolveBindings` to read `Config.Notifications` (loaded once at `Runtime` construction via `loadEffectiveConfig(root)`, using the same `root` already passed into `newRuntime(root, tel)`) instead of calling `notification.LoadLocalConfig(r.root, os.ReadFile)`.
- [x] 3.5 Update `cmd/jig/notifications.go`'s `notificationsCheck`/`runNotifications` to load the merged config via `loadEffectiveConfig(*root)` (using `notificationsCheck`'s own `--root` flag value, not a hardcoded path) and pass its `Notifications` table into `notification.Inspect` instead of having `Inspect` load from disk itself.
- [x] 3.6 Add `GlyphPreset string` (`toml:"glyph_preset"`) to `UIConfig` in `internal/config/config.go`, defaulting to `"unicode"`; validate it is exactly `"ascii"` or `"unicode"` (case-sensitive, no normalization) in `internal/config/load.go`'s validation step, returning `ErrConfigInvalid` otherwise.
- [x] 3.7 In `cmd/jig/main.go`, change `applyGlobalPresetFlag` to return a tri-state result (e.g. an `int`/small enum: explicit-ASCII, explicit-Unicode, unset) instead of calling `shared.SetPreset` directly; remove the `if seen && value` collapse so explicit `--ascii=false` is distinguishable from "unset."
- [x] 3.8 Create `internal/config/glyph.go` with the tri-state type and `ResolveGlyphPreset(tristate, cfg Config) shared.Preset`: explicit flag (either state) wins; unset flag falls back to `cfg.UI.GlyphPreset`; no flag and no config value defaults to Unicode. Call this resolver once in `main.go`'s pre-dispatch sequence (after `loadEffectiveConfig()`), applied uniformly across bare `jig`, `run`, `notifications`, and `config show`.
- [x] 3.9 Update `cmd/jig/preset_test.go` for the new tri-state return shape of `applyGlobalPresetFlag`; add `internal/config/glyph_test.go` covering flag-wins-over-config, config-wins-when-flag-unset, and default-is-unicode cases.
- [x] 3.9a Add a case to `internal/config/load_test.go` (the file that owns schema validation, per Task 1.3/3.6 — not `glyph_test.go`, which only covers the flag/config resolver) asserting `[ui] glyph_preset = "ASCII"` (and another non-exact value) produces `ErrConfigInvalid`, while `"ascii"` and `"unicode"` decode without error. This is the spec's explicit case-sensitivity FR for Unit 3 and was previously untested.
- [x] 3.10 Update `internal/notification/config_test.go` (remove disk-loader-specific cases, keep parse/validate cases) and `internal/notification/readiness_test.go`/`cmd/jig/notifications_test.go`/`cmd/jig/wire_test.go` for the config-sourced notifications path, including a persistence-off (`root == ""`) case for `notificationsCheck` and `Runtime.resolveBindings` now that both flow through `internal/config.Load`.
- [x] 3.11 Run `go test ./internal/config ./internal/notification ./cmd/jig -count=1 && go vet ./internal/config ./internal/notification ./cmd/jig`.
- [x] 3.12 Run the grep sweep (`grep -rn "notifications.toml\|LoadLocalConfig" --include="*.go" .`) and confirm no non-historical matches remain; capture the CLI Proof Artifacts (notifications round-trip, glyph-preset default, flag-overrides-config).

### [x] 4.0 Migrate Telemetry Config into `[telemetry]` with env-var base layer and kill-switch precedence

#### 4.0 Proof Artifact(s)

- CLI: `jig config show` with no env vars and no config set shows telemetry off (`mode` unset/off), matching today's zero-config behavior.
- CLI: `jig config show` with `OTEL_EXPORTER_OTLP_ENDPOINT` set as an env var and no config override shows that env var's value, demonstrating the env-var-derived base layer.
- CLI: `jig config show` with both `OTEL_EXPORTER_OTLP_ENDPOINT` set as an env var and `[telemetry] otlp_endpoint` set to a different value in `.jig/config.toml` shows the config value, demonstrating config now overrides env vars for this table.
- CLI: `jig run <workflow>` with `OTEL_SDK_DISABLED=true` and telemetry otherwise fully enabled via `config.toml` produces no telemetry output, demonstrating the kill switch overrides config unconditionally.
- Test: `internal/telemetry` tests cover `KillSwitchActive`, the env-derived base layer, and config-over-env override for at least one field; `internal/config` tests cover the final kill-switch enforcement step after table merge.

#### 4.0 Tasks

- [x] 4.1 Add `TelemetryConfig` fields to `internal/config/config.go` mirroring every field `telemetry.ResolveConfig` currently resolves (`ServiceName`, `ResourceAttributes`, `OTLPEndpoint`, `OTLPProtocol`, `OTLPHeaders`, `OTLPInsecure`, `MetricsExporter`, `TracesExporter`, `PrometheusAddr`, `PrometheusPath`, `Mode`), with `toml:"..."` tags using the same names as the FR list (snake_case).
- [x] 4.2 In `internal/telemetry/config.go`, export `KillSwitchActive() bool` (extracted from the inline `OTEL_SDK_DISABLED` check at the current kill-switch site) and call it from inside `ResolveConfig` in place of the inline check, preserving today's standalone behavior exactly.
- [x] 4.3 Create `internal/config/telemetry.go` with a function that builds the `[telemetry]` "built-in defaults" layer by calling `telemetry.ResolveConfig(telemetry.OSEnv(), telemetry.Prefs{}, telemetry.TelemetryFields{})` (env-only, no prefs/workflow contribution) and converts the result into a `TelemetryConfig` value. Own both directions of this conversion in `internal/config` (e.g. `fromTelemetryConfig(telemetry.Config) TelemetryConfig` and `(TelemetryConfig) toTelemetryConfig() telemetry.Config`) as the single named converter pair Task 4.6 reuses — `internal/telemetry` must not grow its own mirror of this mapping.
- [x] 4.4 Extend `internal/config/merge.go`'s per-key merge so the `[telemetry]` table's base layer (4.3) is used in place of a hardcoded zero value, with user-config and project-config `[telemetry]` values layering on top per-key exactly like every other table (config wins over the env-derived base).
- [x] 4.5 In `internal/config/telemetry.go`, add the final step: after all `[telemetry]` layers are merged, call `telemetry.KillSwitchActive()` once more and force the merged `Mode` to off when it reports true, regardless of what any layer set.
- [x] 4.6 Update `cmd/jig/telemetry.go`'s `setupTelemetry(ctx, root)` to call `loadEffectiveConfig(root)`, convert `Config.Telemetry` back to `telemetry.Config` via Task 4.3's `toTelemetryConfig()` converter, and pass it directly to `telemetry.Init`, rather than calling `telemetry.LoadPrefs`/`telemetry.ResolveConfig` itself. `setupTelemetry` keeps accepting `root` as a parameter (already true today) so `runRun`'s own `--root` value keeps flowing through unchanged.
- [x] 4.7 Add `internal/telemetry/config_test.go` cases for `KillSwitchActive` (true/false/unset env values); add `internal/config/telemetry_test.go` cases for env-base-only, config-overrides-env for at least `otlp_endpoint`, and kill-switch-forces-mode-off-after-config-override.
- [x] 4.8 Run `go test ./internal/telemetry ./internal/config ./cmd/jig -count=1 && go vet ./internal/telemetry ./internal/config ./cmd/jig`; capture the four CLI Proof Artifacts, including the `OTEL_SDK_DISABLED` override-wins-over-config-enabled case with a real `jig run` invocation.

### [x] 5.0 Retire `.jig/telemetry.json` prefs and close out cross-cutting verification

#### 5.0 Proof Artifact(s)

- Test: `cmd/jig` and `internal/telemetry` package tests pass with `.jig/telemetry.json` (`internal/telemetry/prefs.go`'s `LoadPrefs`/`SavePrefs`) removed and `cmd/jig/telemetry.go`'s `setupTelemetry` reading the merged `Config` instead.
- CLI: `grep -rn "telemetry.json\|LoadPrefs\|SavePrefs" --include="*.go" .` returns no matches outside historical `docs/specs`.
- CLI: `go build ./cmd/jig && go test ./... && go vet ./...` passes at the repository root, demonstrating the full migration is coherent end to end.
- Diff: final review confirms zero remaining references to `.jig/tui.json`, `.jig/notifications.toml`, or `.jig/telemetry.json` in non-historical code/docs, satisfying the spec's format-consolidation success metric.

#### 5.0 Tasks

- [x] 5.1 Delete `internal/telemetry/prefs.go` (`PrefsFileName`, `PrefsPath`, `LoadPrefs`, `SavePrefs`); confirm the `Prefs` struct in `config.go` remains only as `ResolveConfig`'s in-memory parameter type (no remaining disk-file callers).
- [x] 5.2 Update `internal/telemetry/config_test.go` to remove the `SavePrefs`-round-trip test (`TestSavePrefsRoundTrip`-style case at line ~209/242) and any other disk-file-specific assertions; keep `ResolveConfig`/`Prefs{}`-as-value tests.
- [x] 5.3 Confirm `cmd/jig/telemetry.go` (updated in Task 4.6) has no remaining `telemetry.LoadPrefs` call; remove the now-stale doc comment on `setupTelemetry` referencing `.jig/telemetry.json`.
- [x] 5.4 Run the grep sweep (`grep -rn "telemetry.json\|LoadPrefs\|SavePrefs" --include="*.go" .`) and confirm no non-historical matches remain.
- [x] 5.5 Sweep `docs/*.md` (`docs/observability.md`, `docs/operations.md`, `docs/TUI.md`, `docs/adr/0012-observability-export.md`) for references to `.jig/tui.json`, `.jig/notifications.toml`, or `.jig/telemetry.json` and update them to describe the `config.toml` `[tui]`/`[notifications]`/`[telemetry]` tables instead; leave historical ADR/plan narrative describing past decisions unedited.
- [x] 5.6 Run the full repository check sequence from `docs/TESTING.md`: `gofmt -l` on all changed files (fix any hits), `go build ./cmd/jig`, `go test ./...`, `go vet ./...`.
- [x] 5.7 Do a final targeted diff review confirming only intended files changed and every Unit 1-4 Proof Artifact is reproducible from the final state of the branch.
