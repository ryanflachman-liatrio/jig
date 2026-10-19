package config

import (
	"maps"
	"slices"
)

// Merge combines base and overlay per key/per table: a zero-value field in
// overlay falls through to base's value; a non-zero field in overlay wins.
// Each table's merge is written out explicitly per CONVENTIONS.md ("typed
// structs, no stringly-typed maps") rather than via reflection. The result
// owns its maps, slices, and pointers: mutating it never writes through to
// base or overlay.
func Merge(base, overlay Config) Config {
	return Config{
		UI:            mergeUI(base.UI, overlay.UI),
		TUI:           mergeTUI(base.TUI, overlay.TUI),
		Keys:          mergeKeys(base.Keys, overlay.Keys),
		Notifications: mergeNotifications(base.Notifications, overlay.Notifications),
		Telemetry:     mergeTelemetry(base.Telemetry, overlay.Telemetry),
		HelpChat:      mergeHelpChat(base.HelpChat, overlay.HelpChat),
		Security:      mergeSecurity(base.Security, overlay.Security),
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
	out.Opener = mergeOpener(base.Opener, overlay.Opener)
	out.SimpleMode = mergeBool(base.SimpleMode, overlay.SimpleMode)
	out.CompactToolGroups = mergeBool(base.CompactToolGroups, overlay.CompactToolGroups)
	out.Bell = mergeBool(base.Bell, overlay.Bell)
	return out
}

// mergeKeys merges per action ID: a project-level remap of one action leaves
// the user's other remaps in place.
func mergeKeys(base, overlay map[string]KeyChords) map[string]KeyChords {
	if len(overlay) == 0 {
		return cloneKeys(base)
	}
	out := make(map[string]KeyChords, len(base)+len(overlay))
	for id, chords := range base {
		out[id] = slices.Clone(chords)
	}
	for id, chords := range overlay {
		out[id] = slices.Clone(chords)
	}
	return out
}

// cloneKeys deep-copies keys, preserving nil.
func cloneKeys(keys map[string]KeyChords) map[string]KeyChords {
	if keys == nil {
		return nil
	}
	out := make(map[string]KeyChords, len(keys))
	for id, chords := range keys {
		out[id] = slices.Clone(chords)
	}
	return out
}

func mergeNotifications(base, overlay NotificationsConfig) NotificationsConfig {
	out := base
	out.Enabled = mergeBool(base.Enabled, overlay.Enabled)
	if len(overlay.Destinations) > 0 {
		out.Destinations = overlay.Destinations
	}
	out.Destinations = slices.Clone(out.Destinations)
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
	out.OTLPInsecure = mergeBool(base.OTLPInsecure, overlay.OTLPInsecure)
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

// mergeStringMap merges overlay's keys onto a copy of base, per key.
func mergeStringMap(base, overlay map[string]string) map[string]string {
	out := maps.Clone(base)
	if len(overlay) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]string, len(overlay))
	}
	maps.Copy(out, overlay)
	return out
}

// mergeBool returns a fresh copy of overlay when set, else of base, so the
// merged pointer never aliases a layer.
func mergeBool(base, overlay *bool) *bool {
	src := base
	if overlay != nil {
		src = overlay
	}
	if src == nil {
		return nil
	}
	v := *src
	return &v
}
