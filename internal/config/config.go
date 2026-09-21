// Package config owns jig's layered configuration schema: a typed Config
// struct, TOML loading for the user- and project-level files, and the
// per-key merge that combines built-in defaults, the user file, the project
// file, and CLI flags into one effective value. See docs/ARCHITECTURE.md.
package config

import "jig/internal/notification"

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
type UIConfig struct {
	// GlyphPreset selects the glyph vocabulary (shared.SymbolPreset) when no
	// --ascii/--ascii=<bool> flag is passed. Exactly "ascii" or "unicode"
	// (case-sensitive, no normalization); "" means unset (see glyph.go's
	// ResolveGlyphPreset). Defaults to "unicode".
	GlyphPreset string `toml:"glyph_preset"`
}

// TUIConfig holds terminal-UI display preferences (formerly .jig/tui.json).
//
// Both fields are pointers so a lower layer's explicit false can be
// overridden by an explicit false at a higher layer without being confused
// with "unset" (CONVENTIONS.md: distinguish absent values from explicit
// false). This matters most for SimpleMode, whose built-in default is true.
type TUIConfig struct {
	SimpleMode        *bool `toml:"simple_mode"`
	CompactToolGroups *bool `toml:"compact_tool_groups"`
}

// SimpleModeOrDefault resolves SimpleMode, falling back to the built-in
// default (true) when unset. Safe to call on a zero-value TUIConfig.
func (c TUIConfig) SimpleModeOrDefault() bool {
	if c.SimpleMode != nil {
		return *c.SimpleMode
	}
	return true
}

// CompactToolGroupsOrDefault resolves CompactToolGroups, falling back to
// the built-in default (false) when unset. Safe to call on a zero-value
// TUIConfig.
func (c TUIConfig) CompactToolGroupsOrDefault() bool {
	if c.CompactToolGroups != nil {
		return *c.CompactToolGroups
	}
	return false
}

// NotificationsConfig holds operator notification bindings (formerly
// .jig/notifications.toml), reusing notification's own destination types
// rather than redeclaring their shape.
type NotificationsConfig struct {
	Enabled      bool                       `toml:"enabled"`
	Destinations []notification.Destination `toml:"destination"`
}

// ToLocalConfig converts to notification.LocalConfig, the shape
// notification.Inspect/ResolveBindings consume. internal/config owns this
// conversion; internal/notification has no config-package dependency.
func (c NotificationsConfig) ToLocalConfig() notification.LocalConfig {
	return notification.LocalConfig{Enabled: c.Enabled, Destinations: c.Destinations}
}

// validate delegates to notification.LocalConfig's own alias/type/destination
// schema checks so [notifications] failures produce the same ErrConfigInvalid
// as any other table.
func (c NotificationsConfig) validate() error {
	return c.ToLocalConfig().Validate()
}

// TelemetryConfig holds OpenTelemetry/Prometheus exporter defaults (formerly
// OTEL_*/JIG_TELEMETRY_* env vars only).
type TelemetryConfig struct{}

// Default returns the built-in defaults layer: the lowest-precedence input
// to Merge. Every field not set by a documented non-zero default keeps its
// Go zero value.
func Default() Config {
	return Config{}
}
