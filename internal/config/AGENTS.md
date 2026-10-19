# internal/config

This package owns jig's `config.toml` schema and its layering. `Load` merges
built-in defaults → user file → project file; CLI flags are applied by the
caller on top of the returned `Config` (see `ResolveGlyphPreset`).

## Adding a key

1. Add the field to its table's struct with a `toml:"snake_case"` tag. A new
   table gets its own struct, a field on `Config`, and a line in `Merge`.
2. Choose the field's **presence** type, because merge treats the Go zero
   value as "unset, fall through to the lower layer":
   - Strings, slices, maps: the zero value is unset.
   - Booleans, and any value where explicit zero must win over a lower layer:
     use a pointer (`*bool`), plus an `XOrDefault()` method that holds the
     built-in default. `TUIConfig.SimpleMode` is the model.
3. Extend the table's `mergeX` function by hand. Merge is explicit per field,
   never reflection. Maps merge per key; slices replace whole. The result
   owns its slices, maps, and pointers: clone them so mutating a merged
   `Config` never writes through to a layer (`mergeOpener` is the model).
4. If the value has a restricted domain, extend `Config.validate`.

Done when `merge_test.go` covers overlay-wins, zero-falls-through, and (for
pointer fields) explicit-false-overrides-true, and `load_test.go` covers a
file that sets the key plus a file with an invalid value.

## Error sanitization

Config files can hold secrets (`[telemetry] otlp_headers`). Errors from
`loadFile` carry a fixed sentinel (`ErrConfigInvalid`, `ErrConfigUnreadable`)
and the file path, and nothing from the file's contents or the parser.

A validation error may add detail only when it names keys and accepted values,
never the configured value: `backendError` and `keysError` are the model.
Give a new detail-bearing error its own type and surface it in `loadFile`
the same way; every other validation failure returns bare `ErrConfigInvalid`.

## Layering rules

- **Persistence-off:** an empty `root` gives an empty project path and an empty
  layer. `ProjectConfigPath` returns `""` rather than joining.
- **Missing file** is an empty layer. A present but unreadable or invalid file
  fails `Load`; unknown keys are invalid (`md.Undecoded()`).
- **User path** follows XDG (`$XDG_CONFIG_HOME`, else `~/.config`) on every
  OS, deliberately not `os.UserConfigDir`, which means `~/Library/Application
  Support` on macOS.
- **Telemetry is inverted:** its defaults layer comes from `OTEL_*` /
  `JIG_TELEMETRY_*` env vars at load time, so config files override env.
  `OTEL_SDK_DISABLED` still wins over everything via
  `applyTelemetryKillSwitch`, applied once after the final merge.
  `TelemetryConfig` mirrors `telemetry.Config` field for field; change both
  converters in `telemetry.go` together.

## Dependency direction

Config converts into other packages' types (`ToLocalConfig`,
`ToTelemetryConfig`); those packages do not import config. Keep that
direction: put a converter here rather than adding a config import in the
consumer.

[keys] validation here is shape-only. Whether an action ID exists and whether
chords collide depend on the TUI registry and are checked by
`tui.ConfigureKeymap`.
