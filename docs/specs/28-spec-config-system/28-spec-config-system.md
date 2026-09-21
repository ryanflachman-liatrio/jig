# 28-spec-config-system.md

## Introduction/Overview

Jig has no general-purpose configuration system. Settings that exist today are scattered across ad-hoc, project-local-only files in different formats (`.jig/notifications.toml`, `.jig/tui.json`), per-invocation CLI flags (`--ascii`) with no way to set a lasting default, or environment variables (`OTEL_*`, `JIG_TELEMETRY_*`) that must be re-set on every machine and CI job. This feature introduces a single, layered TOML configuration system for jig: a user-level file for personal defaults and a project-level file for repo-specific overrides, merged together with built-in defaults and CLI flags, following the proven layered-override pattern used by tools like yazi, lazygit, and nushell. It consolidates the two existing ad-hoc settings files and the telemetry env-var surface into this new system, and adds one new, genuinely useful setting: a persisted glyph-preset default.

## Goals

- Provide one documented, TOML-based way to configure jig at the user level (`~/.config/jig/config.toml`) and override it at the project level (`.jig/config.toml`).
- Merge configuration in layers — built-in defaults → user config → project config → CLI flags — so a project or user file only needs to declare the keys it wants to change.
- Consolidate `.jig/tui.json`, `.jig/notifications.toml`, and the `internal/telemetry` env-var surface into the new config system, removing the old file formats/loaders where they're fully superseded.
- Add a `jig config show` command that prints the fully merged, effective configuration, so users can debug what value is actually in effect.
- Add a persisted glyph-preset default (`[ui] glyph_preset`) so `--ascii`'s effect no longer has to be re-specified on every invocation.

## User Stories

- **As a jig user**, I want to set my preferred glyph preset once in a personal config file, so that I don't have to pass `--ascii` every time I run jig on a terminal that doesn't render Unicode well.
- **As a jig user working across multiple repos**, I want a project-level config file that overrides only the settings relevant to that project (e.g. its notification destinations), so that my personal defaults still apply everywhere else.
- **As a jig user debugging unexpected behavior**, I want a command that shows me the fully merged configuration jig is actually using, so that I can tell whether a setting is coming from my user config, the project config, an environment variable, or a flag.
- **As a jig operator**, I want to set OpenTelemetry exporter defaults in my config file instead of re-declaring the same `OTEL_*` environment variables on every machine and CI job, while still being able to override them per-environment with env vars when I need to.
- **As a jig maintainer**, I want the TUI display preferences, notification settings, and telemetry settings to live in the same config system as everything else, so that there is one format and one loading path to maintain instead of three.

## Demoable Units of Work

### Unit 1: Layered Config Core (loader, merge, `config show`)

**Purpose:** Establish the `internal/config` package that defines the config schema, loads and TOML-parses the user and project files, merges them with built-in defaults, and exposes the result through a new `jig config show` subcommand. This is the foundation every other unit builds on.

**Functional Requirements:**
- The system shall define a typed `Config` struct with nested tables for `[ui]`, `[tui]`, `[notifications]`, and `[telemetry]` (schemas finalized in Units 2–4).
- The system shall load configuration from `~/.config/jig/config.toml` (user level) and `.jig/config.toml` (project level, relative to the working repository), where either or both files may be absent.
- The system shall merge configuration in this precedence order, lowest to highest: built-in defaults → user config → project config → CLI flags, where each layer only needs to set the keys it wants to change and unset keys fall through to the next layer.
- The system shall treat a missing user or project config file as "use defaults for this layer," not an error.
- The system shall return a sanitized error (no raw parser internals or file contents) when a present config file fails to parse or fails schema validation, consistent with the existing `internal/notification` error-handling pattern (`ErrConfigUnreadable` / `ErrConfigInvalid`-style fixed reasons). As a narrow, deliberate exception to that pattern's "deliberately unwrapped" rule, the returned error wraps the fixed-reason sentinel with `%w` plus the offending file's path (e.g. `fmt.Errorf("%w: %s", ErrConfigInvalid, path)`) so a user with two possible config files (user-level and project-level) can tell which one failed — the path is not sensitive, only file contents/parser internals are, and those remain fully discarded. Wrapping stays limited to the sentinel and path: the underlying parser/filesystem error itself is never wrapped or logged. `errors.Is(err, ErrConfigInvalid)` / `errors.Is(err, ErrConfigUnreadable)` continue to work against the wrapped error.
- The system shall provide a `jig config show` subcommand that prints the fully merged effective configuration as flat TOML (no per-value layer annotation in v1).
- The user shall be able to point jig at an alternate user-level config file path via a single global `--config` flag, parsed once in `cmd/jig/main.go`'s existing pre-dispatch flag scan (the same mechanism that parses `--ascii` today) rather than being registered separately on each subcommand's `flag.NewFlagSet`. When passed, `--config <path>` substitutes only the user-level layer; the project-level `.jig/config.toml` (if present) still loads and merges on top of it as usual. This flag applies uniformly across every config-consuming entry point: bare `jig` (TUI), `run`, `notifications`, and `config show` — the only four places that read merged config today.

**Proof Artifacts:**
- `CLI: jig config show` with no config files present prints the built-in defaults, demonstrating defaults load correctly.
- `CLI: jig config show` with only `~/.config/jig/config.toml` set to a non-default value prints that value, demonstrating user-level override works.
- `CLI: jig config show` with both a user config and a `.jig/config.toml` setting the same key to different values prints the project value, demonstrating project overrides user.
- `CLI: jig config show` run against a config file containing invalid TOML prints a sanitized, actionable error that names the offending file's path (but not its contents) and a non-zero exit code, demonstrating error handling.
- `CLI: jig --config /tmp/alt.toml config show` prints values from `/tmp/alt.toml` merged with any present `.jig/config.toml`, demonstrating the global `--config` override substitutes only the user-level layer.

### Unit 2: Migrate TUI Preferences into `[tui]`

**Purpose:** Move `SimpleMode` and `CompactToolGroups` (currently in `.jig/tui.json`) into the new config system's `[tui]` table, removing the JSON file and its loader.

**Functional Requirements:**
- The system shall expose `SimpleMode` and `CompactToolGroups` as keys under `[tui]` in the merged config, sourced through the Unit 1 loader/merge pipeline.
- The system shall remove the `.jig/tui.json` file format and its dedicated `Load`/`Save` loader (`internal/tui/prefs`), replacing all call sites with reads from the merged `Config`.
- The TUI shall behave identically to today when `[tui]` is unset in both user and project config: built-in defaults are `simple_mode = true`, `compact_tool_groups = false`, matching `prefs.Default()`'s existing asymmetric pair exactly.

**Proof Artifacts:**
- `CLI: jig config show` after setting `[tui] compact_tool_groups = true` in `.jig/config.toml` shows the value, and launching the TUI in that project demonstrates the compact tool-group rendering is active.
- `Test: internal/config and internal/tui package tests pass with .jig/tui.json removed from the codebase`, demonstrating the old path is fully retired, not just superseded.

### Unit 3: Migrate Notification Config into `[notifications]` and Add `[ui]` Glyph-Preset Default

**Purpose:** Move the existing `.jig/notifications.toml` schema into the new config system's `[notifications]` table, and add the new `[ui]` table with a persisted glyph-preset default — the feature's one new user-facing setting.

**Functional Requirements:**
- The system shall expose the existing notification settings (destinations/enablement, per current `.jig/notifications.toml` schema) under `[notifications]` in the merged config, sourced through the Unit 1 loader/merge pipeline.
- The system shall remove the standalone `.jig/notifications.toml` file format and its dedicated loader (`internal/notification.LoadLocalConfig`), replacing call sites with reads from the merged `Config`.
- The system shall expose `[ui] glyph_preset` as a string enum (`"ascii"` | `"unicode"`), matched case-sensitively (lowercase only — `"ASCII"` is a validation error, not a normalized match), consistent with the existing case-sensitive enum-matching precedent for `notification.DestinationType`.
- An invalid `glyph_preset` value (anything other than exactly `"ascii"`/`"unicode"`) shall produce the same sanitized `ErrConfigInvalid`-style error as any other schema validation failure.
- Glyph-preset resolution shall happen once at startup in `cmd/jig/main.go`'s existing pre-dispatch sequence, split into two steps to correctly support an explicit flag overriding a non-default config value:
  1. `applyGlobalPresetFlag`'s `os.Args` scan is changed to produce a **tri-state** result (explicit-ASCII / explicit-Unicode / unset) instead of directly calling `shared.SetPreset`. Today's scan silently drops explicit `--ascii=false` into a no-op (`if seen && value` — see `cmd/jig/main.go:181-236`); that collapsing of explicit-false into "unset" must be removed so the two states are distinguishable.
  2. A separate resolver (e.g. `config.ResolveGlyphPreset(tristate, cfg Config) Preset`) takes that tri-state plus the merged `Config` and applies precedence: explicit flag (either state) wins over `[ui] glyph_preset`; an unset flag falls back to the merged config's value; the built-in default (no flag, no config) is `"unicode"` (today's no-flag behavior). This resolver is what actually calls `shared.SetPreset`, and is the single call site used by all four config-consuming entry points named in Unit 1 FR (bare `jig`, `run`, `notifications`, `config show`), since they all pass through the same pre-dispatch sequence.

**Non-goal carried from Q6 of the design review:** `[harness]` (backend/model/effort defaults) is explicitly **not** part of this config system. Backend/model/effort selection remains workflow-file-only (`internal/workflow/load.go`'s existing step → workflow `[defaults]` → hardcoded fallback cascade), untouched by this spec. No `--backend`/`--model`/`--effort` CLI flags are added.

**Proof Artifacts:**
- `CLI: jig config show` after setting `[notifications]` values in `.jig/config.toml` shows them merged in, demonstrating the migrated schema round-trips correctly.
- `CLI: jig config show` after setting `[ui] glyph_preset = "ascii"` in the user config shows the value, and running `jig` (any subcommand) without `--ascii` demonstrates ASCII glyphs are used by default.
- `CLI: jig --ascii=false` with `[ui] glyph_preset = "ascii"` set in config demonstrates the explicit flag still wins over config.

### Unit 4: Migrate Telemetry Config into `[telemetry]`

**Purpose:** Fold `internal/telemetry`'s existing `OTEL_*`/`JIG_TELEMETRY_*` env-var surface into the new config system's `[telemetry]` table, so telemetry defaults can live in `config.toml` alongside everything else, while preserving env vars as a still-functional fallback for anyone who doesn't adopt config files (CI, containers, existing deploy manifests).

**Functional Requirements:**
- The system shall expose every field currently resolved by `internal/telemetry.ResolveConfig` (`ServiceName`, `ResourceAttributes`, `OTLPEndpoint`, `OTLPProtocol`, `OTLPHeaders`, `OTLPInsecure`, `MetricsExporter`, `TracesExporter`, `PrometheusAddr`, `PrometheusPath`, `Mode`) as keys under `[telemetry]` in the merged config.
- Unlike other tables, `[telemetry]`'s "built-in defaults" layer is populated by reading today's `OTEL_*`/`JIG_TELEMETRY_*` env vars at resolution time (not a hardcoded zero value), so a machine/CI job with only env vars set and no `config.toml` behaves exactly as it does today.
- User config and project config layer on top of that env-var-derived base exactly like any other table: an explicit `[telemetry]` value in `config.toml` overrides the corresponding env var, not the other way around. This is a deliberate inversion of today's env-var-is-authoritative behavior, adopted so config.toml is a real override surface for telemetry rather than a second, lower-priority place to set the same thing.
- `OTEL_SDK_DISABLED` remains an unconditional, env-var-only kill switch that overrides every other telemetry setting regardless of what `config.toml` says. It does not get a `config.toml` equivalent. Because `[telemetry] mode` (among other fields) is itself a settable `config.toml` key (FR above) that can layer on top of the env-derived base *after* today's inline kill-switch check (`internal/telemetry/config.go:319-322`) would have run, that inline check alone is not sufficient once config.toml layering is introduced — a `config.toml` `mode` override applied after the base layer could silently re-enable telemetry the kill switch just disabled. To prevent that, `internal/telemetry` exports a small `KillSwitchActive() bool` (reading and bool-parsing `OTEL_SDK_DISABLED`) that is called twice: once inside `ResolveConfig` as today (so telemetry keeps working standalone, unchanged, for callers with no config.toml in play), and once more by `internal/config` as the final step after merging the `[telemetry]` table across all layers, forcing `Mode` to off if the kill switch is active — regardless of what any layer, including `config.toml`, set `Mode` to. This keeps the env/bool-parsing logic in one place rather than duplicating it between packages.
- `[telemetry] otlp_headers` is included in the migrated schema like every other field, as a deliberate, called-out exception to the "no secrets in config.toml" default posture (see Non-Goals and Security Considerations) — operators who set bearer-token-style OTLP headers should be aware this value is no longer env-var-only once set in a config file.
- `internal/telemetry.ResolveConfig`'s env-var-reading logic is not deleted (unlike `tui.json`/`notifications.toml`'s loaders) — it becomes the input to the new base layer described above, since env vars remain a first-class, always-available fallback for telemetry specifically.

**Proof Artifacts:**
- `CLI: jig config show` with no env vars and no config set shows telemetry off (`mode` unset/off), matching today's zero-config behavior.
- `CLI: jig config show` with `OTEL_EXPORTER_OTLP_ENDPOINT` set as an env var and no config override shows that env var's value, demonstrating the env-var-derived base layer.
- `CLI: jig config show` with both `OTEL_EXPORTER_OTLP_ENDPOINT` set as an env var and `[telemetry] otlp_endpoint` set to a different value in `.jig/config.toml` shows the config value, demonstrating config now overrides env vars for this table.
- `CLI: jig run` with `OTEL_SDK_DISABLED=true` and telemetry otherwise fully enabled via `config.toml` produces no telemetry output, demonstrating the kill switch overrides config unconditionally.

## Non-Goals (Out of Scope)

1. **No automatic migration tool**: jig will not read old `.jig/tui.json` or `.jig/notifications.toml` files and auto-convert them into the new `config.toml`. This is a pre-1.0, actively-developed tool; users recreate the handful of settings they had manually. This is a clearly-labeled assumption, not an open question — it keeps this spec from needing a one-time migration command that would otherwise be thrown away.
2. **No `[harness]` table**: backend/model/effort defaults are explicitly not part of this config system (see Unit 3's carried non-goal). This was originally proposed as the spec's headline new capability and was deliberately cut during design review in favor of leaving backend/model/effort entirely workflow-file-driven.
3. **No security/sentinel settings**: settings related to spec 10's agent security monitoring are not part of this config system in v1.
4. **No config hot-reload**: changes to config files take effect on the next jig invocation/TUI launch, not live while a session is running.
5. **No config file generation/scaffolding command** (e.g. `jig config init` to write a starter file): `jig config show` covers the debugging need for this spec; a guided-setup command can be a follow-up.
6. **No secrets/env-var interpolation inside TOML values, with one called-out exception**: config values are literal; anything needing a secret continues to use an environment variable read directly by the consuming code — except `[telemetry] otlp_headers` (Unit 4), which is deliberately included as a literal value despite commonly carrying an auth token, per explicit product decision during design review.
7. **No Docker/container step-execution config**: a separate, not-yet-specced epic (running a step's agent inside a spun-up container) is out of scope for this spec entirely. If/when that epic lands, whether it needs its own config table is a decision for that spec, not an extension made here.

## Design Considerations

No specific UI/UX design requirements — this is a CLI/config feature. The one user-facing surface is `jig config show`'s printed output, which must be valid, flat TOML (so it can be piped/diffed) with the same key layout as the input files — no per-value layer annotation (defaults/user/project/flag/env) in v1, since that would require comment-based annotation and add parser-fragility risk for a "nice to have."

## Repository Standards

- Follow `docs/CONVENTIONS.md`: explicit typed structs (no stringly-typed maps) for the config schema; interfaces only at the consumption boundary; resolve precedence/inheritance once in the loader rather than at each call site, mirroring the existing precedence-resolution pattern in `internal/workflow/load.go`.
- Follow the existing `internal/notification/config.go` pattern for TOML parsing (`github.com/BurntSushi/toml`, already a direct dependency — no new library needed) and for sanitized, fixed-reason config errors. Note this pattern deliberately does **not** wrap the underlying parser/filesystem error with `%w` — it discards it entirely and returns the bare sentinel (`ErrConfigUnreadable` / `ErrConfigInvalid`), because that underlying error can embed arbitrary local file content, including credentials. This is an intentional, security-motivated exception to CONVENTIONS.md's general "wrap errors with `%w`" rule, not an oversight — apply it the same way in `internal/config`.
- Add a `config` package entry to `docs/ARCHITECTURE.md`'s package table.
- CLI subcommands in `cmd/jig/main.go` are currently dispatched via per-subcommand `flag.NewFlagSet` (stdlib `flag`, no cobra/pflag) — `jig config show` should follow this same dispatch pattern. The new `--config` flag is the one exception: it's parsed globally, pre-dispatch, alongside `--ascii` (see Unit 1).

## Technical Considerations

- **Package location**: new `internal/config` package owns the `Config` struct, TOML loading, and layered merge logic. It becomes a dependency of `internal/tui/prefs`'s former call sites, `internal/notification`'s former call sites, `internal/telemetry`'s `ResolveConfig`, and the glyph-preset resolution point in `cmd/jig/main.go`.
- **User config path resolution**: use `os.UserHomeDir()` plus `$XDG_CONFIG_HOME` (falling back to `~/.config`) to locate `~/.config/jig/config.toml`, matching XDG convention used by yazi and lazygit. The global `--config` flag substitutes this resolved path.
- **Project config path resolution**: `.jig/config.toml` relative to the current working directory, matching how `.jig/tui.json` and `.jig/notifications.toml` are found today (both are passed a literal `".jig"` root string from `cmd/jig/main.go`, not resolved via any repo-root-walking helper).
- **Merge algorithm**: per-key/per-table deep merge (not whole-file replace) — a layer only overrides the specific keys it sets; unset keys and unset sub-tables fall through to the next lower layer. This is the pattern common to yazi, lazygit, and nushell's config systems and is what makes partial override files useful.
- **Flags still win**: the merge produces defaults for flags to fall back to, not values that override an explicitly-passed flag. `--ascii`'s "was this flag explicitly set?" check continues to use the existing hand-rolled `os.Args` scan in `applyGlobalPresetFlag` rather than `flag.Visit`, since no code path in this repo uses `flag.Visit` today and `--ascii`/`--config` are parsed pre-dispatch, not through a `flag.NewFlagSet`. That scan changes shape per Unit 3: it now returns a tri-state result instead of calling `shared.SetPreset` directly, so a separate resolver can let explicit `--ascii=false` override a config-set `glyph_preset = "ascii"` (see Unit 3).
- **`[telemetry]`'s inverted precedence is table-specific**: every other table's "built-in defaults" are hardcoded zero values; `[telemetry]`'s defaults layer is populated dynamically from `OTEL_*`/`JIG_TELEMETRY_*` env vars at resolution time, with `config.toml` layered on top able to override them. `OTEL_SDK_DISABLED` sits outside this entirely as an unconditional kill switch. See Unit 4.
- **Removal, not deprecation shims — except telemetry**: per repo convention, `.jig/tui.json` and `.jig/notifications.toml` loading code is deleted outright, not kept behind a compatibility flag, since there are no external users to preserve compatibility for. `internal/telemetry`'s env-var-reading code is the one exception: it is not deleted, because it becomes the base-layer input for `[telemetry]` (see Unit 4) rather than a superseded format.

## Security Considerations

- Config files may contain notification destination details (e.g. webhook URLs) carried over from the old `notifications.toml` schema — these are already treated as project-local, gitignored state today (`.jig/` is gitignored per `jig init`) and that treatment continues unchanged for `.jig/config.toml`.
- Config parse/validation errors must not echo raw file contents back to the user (consistent with the existing notification config's sanitized-error pattern), since a config file could contain sensitive values.
- `jig config show` prints the effective merged config, including any values from `.jig/config.toml` — this is an intentional debugging affordance, not a new information-disclosure risk, since the file is already locally readable by the same user running the command. This now includes `[telemetry] otlp_headers`, which may contain an OTLP collector auth token: `jig config show`'s output should be treated by users the same way they'd treat printing an env var containing a secret, since this spec deliberately allows that value into `config.toml` (Non-Goal #6).
- `OTEL_SDK_DISABLED` is preserved as an env-var-only, config-independent kill switch specifically so an operator can always force telemetry off without depending on a config file being present, readable, or correctly parsed.

## Success Metrics

1. **Format consolidation**: zero remaining references to `.jig/tui.json` or `.jig/notifications.toml` in the codebase after implementation (both formats fully retired, not dual-supported). `internal/telemetry`'s env-var reading is *not* part of this metric — it's retained by design as `[telemetry]`'s base layer.
2. **Working layering**: `jig config show` correctly demonstrates all precedence layers (defaults/env-var-base for telemetry, user, project, flag) across the Unit 1–4 proof artifacts.
3. **No new dependency**: config parsing continues to use `github.com/BurntSushi/toml`, already present in `go.mod`.

## Open Questions

None remaining — all open questions from the original draft (annotated vs. flat `jig config show` output; TOML key naming for the harness table) were resolved during design review: flat-only output (Design Considerations), and the harness table was cut entirely (Non-Goals #2) rather than needing a naming convention.
