// Package config owns jig's layered configuration schema: a typed Config
// struct, TOML loading for the user- and project-level files, and the
// per-key merge that combines built-in defaults, the user file, the project
// file, and CLI flags into one effective value. See docs/ARCHITECTURE.md.
package config

import "github.com/ryanflachman-liatrio/jig/internal/notification"

// Config is the fully-typed, merged configuration surface. Each nested table
// corresponds to one TOML table ([ui], [tui], [keys], [notifications],
// [telemetry], [helpchat], [security]).
// Fields are added to the nested structs by the unit that owns them; this
// file only establishes the shape every layer merges against.
type Config struct {
	UI  UIConfig  `toml:"ui"`
	TUI TUIConfig `toml:"tui"`
	// Keys remaps named TUI actions to chords, keyed by action ID
	// ("monitor.copy_item" = "ctrl+y"). See docs/TUI.md for the IDs.
	Keys          map[string]KeyChords `toml:"keys"`
	Notifications NotificationsConfig  `toml:"notifications"`
	Telemetry     TelemetryConfig      `toml:"telemetry"`
	HelpChat      HelpChatConfig       `toml:"helpchat"`
	Security      SecurityConfig       `toml:"security"`
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

// Accepted [ui] glyph_preset values.
const (
	GlyphPresetASCII   = "ascii"
	GlyphPresetUnicode = "unicode"
)

// TUIConfig holds terminal-UI display preferences.
//
// Boolean preferences are pointers so a higher layer's explicit false is not
// confused with "unset" and still overrides a lower layer's true
// (CONVENTIONS.md: distinguish absent values from explicit false).
type TUIConfig struct {
	Opener            OpenerConfig `toml:"opener"`
	SimpleMode        *bool        `toml:"simple_mode"`
	CompactToolGroups *bool        `toml:"compact_tool_groups"`
	// Bell rings the terminal bell when a run-monitor gate starts waiting.
	// Opt-in because unrequested noise is worse than a missed pulse.
	Bell *bool `toml:"bell"`
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
// the built-in default (true) when unset. Safe to call on a zero-value
// TUIConfig.
func (c TUIConfig) CompactToolGroupsOrDefault() bool {
	if c.CompactToolGroups != nil {
		return *c.CompactToolGroups
	}
	return true
}

// BellOrDefault resolves Bell, falling back to the built-in default (false)
// when unset. Safe to call on a zero-value TUIConfig.
func (c TUIConfig) BellOrDefault() bool {
	if c.Bell != nil {
		return *c.Bell
	}
	return false
}

// NotificationsConfig holds operator notification bindings, reusing
// notification's own destination types rather than redeclaring their shape.
type NotificationsConfig struct {
	// Enabled is a pointer so a project file can disable notifications a
	// user file enabled.
	Enabled      *bool                      `toml:"enabled"`
	Destinations []notification.Destination `toml:"destination"`
}

// EnabledOrDefault resolves Enabled, falling back to the built-in default
// (false) when unset. Safe to call on a zero-value NotificationsConfig.
func (c NotificationsConfig) EnabledOrDefault() bool {
	return c.Enabled != nil && *c.Enabled
}

// ToLocalConfig converts to notification.LocalConfig, the shape
// notification.Inspect/ResolveBindings consume. internal/config owns this
// conversion; internal/notification has no config-package dependency.
func (c NotificationsConfig) ToLocalConfig() notification.LocalConfig {
	return notification.LocalConfig{Enabled: c.EnabledOrDefault(), Destinations: c.Destinations}
}

// validate delegates to notification.LocalConfig's own alias/type/destination
// schema checks so [notifications] failures produce the same ErrConfigInvalid
// as any other table.
func (c NotificationsConfig) validate() error {
	return c.ToLocalConfig().Validate()
}

// TelemetryConfig mirrors every field telemetry.ResolveConfig resolves.
// Unlike every other table, its built-in-defaults layer is populated from
// OTEL_*/JIG_TELEMETRY_* env vars at Load time (see telemetry.go), and
// config.toml layers override that env-derived base.
type TelemetryConfig struct {
	Mode               string            `toml:"mode"`
	ServiceName        string            `toml:"service_name"`
	ResourceAttributes map[string]string `toml:"resource_attributes"`
	OTLPEndpoint       string            `toml:"otlp_endpoint"`
	OTLPProtocol       string            `toml:"otlp_protocol"`
	// OTLPHeaders is the one field that may hold a secret (bearer tokens);
	// operators who set it should protect config.toml accordingly.
	OTLPHeaders map[string]string `toml:"otlp_headers"`
	// OTLPInsecure is a pointer so a config file can turn off an
	// env-enabled insecure transport.
	OTLPInsecure    *bool  `toml:"otlp_insecure"`
	MetricsExporter string `toml:"metrics_exporter"`
	TracesExporter  string `toml:"traces_exporter"`
	PrometheusAddr  string `toml:"prometheus_addr"`
	PrometheusPath  string `toml:"prometheus_path"`
}

// Default returns the built-in defaults layer: the lowest-precedence input
// to Merge. It is deliberately the zero value: a non-zero default lives in
// its field's XOrDefault method, because a value set here would be
// indistinguishable from a configured one during Merge.
func Default() Config {
	return Config{}
}
