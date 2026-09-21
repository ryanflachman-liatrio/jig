package config

// Load resolves and merges every configuration layer, lowest to highest
// precedence: built-in defaults -> user config -> project config. CLI flags
// are the caller's responsibility to apply on top of the returned Config
// (see cmd/jig's glyph-preset resolver, which consumes this Config as a
// fallback rather than letting it override an explicit flag).
//
// userPathOverride, when non-empty, substitutes the resolved user-level
// path (the --config flag). root is the caller's actual persistence root,
// including "" for persistence-off; Load never substitutes a hardcoded
// root itself.
func Load(userPathOverride, root string) (Config, error) {
	userPath := userPathOverride
	if userPath == "" {
		p, err := UserConfigPath()
		if err != nil {
			return Config{}, err
		}
		userPath = p
	}

	userCfg, err := loadFile(userPath)
	if err != nil {
		return Config{}, err
	}

	projectCfg, err := loadFile(ProjectConfigPath(root))
	if err != nil {
		return Config{}, err
	}

	merged := Merge(Default(), userCfg)
	merged = Merge(merged, projectCfg)
	return merged, nil
}

// Merge combines base and overlay per key/per table: a zero-value field in
// overlay falls through to base's value; a non-zero field in overlay wins.
// Each table's merge is written out explicitly per CONVENTIONS.md ("typed
// structs, no stringly-typed maps") rather than via reflection, and is
// extended as each unit adds fields to that table.
func Merge(base, overlay Config) Config {
	return Config{
		UI:            mergeUI(base.UI, overlay.UI),
		TUI:           mergeTUI(base.TUI, overlay.TUI),
		Notifications: mergeNotifications(base.Notifications, overlay.Notifications),
		Telemetry:     mergeTelemetry(base.Telemetry, overlay.Telemetry),
	}
}

func mergeUI(base, overlay UIConfig) UIConfig {
	out := base
	if overlay.GlyphPreset != "" {
		out.GlyphPreset = overlay.GlyphPreset
	}
	return out
}

func mergeTUI(base, overlay TUIConfig) TUIConfig {
	out := base
	if overlay.SimpleMode != nil {
		out.SimpleMode = overlay.SimpleMode
	}
	if overlay.CompactToolGroups != nil {
		out.CompactToolGroups = overlay.CompactToolGroups
	}
	return out
}

func mergeNotifications(base, overlay NotificationsConfig) NotificationsConfig {
	out := base
	if overlay.Enabled {
		out.Enabled = overlay.Enabled
	}
	if len(overlay.Destinations) > 0 {
		out.Destinations = overlay.Destinations
	}
	return out
}

// mergeTelemetry is a placeholder identity merge until Task 4.0 gives
// TelemetryConfig fields and an env-derived base layer (internal/config/telemetry.go).
func mergeTelemetry(base, overlay TelemetryConfig) TelemetryConfig {
	return base
}
