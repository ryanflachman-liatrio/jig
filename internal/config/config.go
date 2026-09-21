// Package config owns jig's layered configuration schema: a typed Config
// struct, TOML loading for the user- and project-level files, and the
// per-key merge that combines built-in defaults, the user file, the project
// file, and CLI flags into one effective value. See docs/ARCHITECTURE.md.
package config

// Config is the fully-typed, merged configuration surface. Each nested table
// corresponds to one TOML table ([ui], [tui], [notifications], [telemetry]).
// Fields are added to the nested structs by the unit that owns them; this
// file only establishes the shape every layer merges against.
type Config struct {
	UI            UIConfig            `toml:"ui"`
	TUI           TUIConfig           `toml:"tui"`
	Notifications NotificationsConfig `toml:"notifications"`
	Telemetry     TelemetryConfig     `toml:"telemetry"`
}

// UIConfig holds general presentation defaults not owned by a more specific
// table.
type UIConfig struct{}

// TUIConfig holds terminal-UI display preferences (formerly .jig/tui.json).
type TUIConfig struct{}

// NotificationsConfig holds operator notification bindings (formerly
// .jig/notifications.toml).
type NotificationsConfig struct{}

// TelemetryConfig holds OpenTelemetry/Prometheus exporter defaults (formerly
// OTEL_*/JIG_TELEMETRY_* env vars only).
type TelemetryConfig struct{}

// Default returns the built-in defaults layer: the lowest-precedence input
// to Merge. Every field not set by a documented non-zero default keeps its
// Go zero value.
func Default() Config {
	return Config{}
}
