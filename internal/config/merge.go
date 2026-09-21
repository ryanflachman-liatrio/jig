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

	// [telemetry]'s built-in-defaults layer is the one exception to a
	// hardcoded zero value (Unit 4): it is populated from today's env vars
	// at resolution time, so config.toml's user/project layers below can
	// override an env-derived value instead of the other way around.
	base := Default()
	base.Telemetry, err = telemetryDefaultsFromEnv()
	if err != nil {
		return Config{}, err
	}

	merged := Merge(base, userCfg)
	merged = Merge(merged, projectCfg)
	merged.Telemetry = applyTelemetryKillSwitch(merged.Telemetry)
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

// mergeTelemetry merges base (the env-derived defaults layer for the first
// call, or a lower-precedence config.toml layer for the second) with
// overlay per key, matching every other table's convention: overlay's
// non-zero fields win, zero fields fall through to base. Map fields merge
// per key (mergeStringMap) rather than replacing the whole map, matching
// telemetry.ResolveConfig's own additive header/resource-attribute merge.
func mergeTelemetry(base, overlay TelemetryConfig) TelemetryConfig {
	out := base
	if overlay.Mode != "" {
		out.Mode = overlay.Mode
	}
	if overlay.ServiceName != "" {
		out.ServiceName = overlay.ServiceName
	}
	out.ResourceAttributes = mergeStringMap(out.ResourceAttributes, overlay.ResourceAttributes)
	if overlay.OTLPEndpoint != "" {
		out.OTLPEndpoint = overlay.OTLPEndpoint
	}
	if overlay.OTLPProtocol != "" {
		out.OTLPProtocol = overlay.OTLPProtocol
	}
	out.OTLPHeaders = mergeStringMap(out.OTLPHeaders, overlay.OTLPHeaders)
	if overlay.OTLPInsecure {
		out.OTLPInsecure = overlay.OTLPInsecure
	}
	if overlay.MetricsExporter != "" {
		out.MetricsExporter = overlay.MetricsExporter
	}
	if overlay.TracesExporter != "" {
		out.TracesExporter = overlay.TracesExporter
	}
	if overlay.PrometheusAddr != "" {
		out.PrometheusAddr = overlay.PrometheusAddr
	}
	if overlay.PrometheusPath != "" {
		out.PrometheusPath = overlay.PrometheusPath
	}
	return out
}

// mergeStringMap merges overlay's keys onto a copy of base, per key,
// leaving base untouched. An empty overlay returns base as-is (no
// allocation for the common no-override case).
func mergeStringMap(base, overlay map[string]string) map[string]string {
	if len(overlay) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}
