package config

import "jig/internal/telemetry"

// telemetryDefaultsFromEnv builds the [telemetry] "built-in defaults" layer
// by resolving telemetry.Config from process env only — no prefs file, no
// workflow [telemetry] contribution — matching today's zero-config behavior
// for a caller with only OTEL_*/JIG_TELEMETRY_* env vars set and no
// config.toml. Load merges the user/project config.toml layers on top of
// this base (merge.go's mergeTelemetry), and applyTelemetryKillSwitch forces
// the result off afterward if OTEL_SDK_DISABLED is active.
func telemetryDefaultsFromEnv() (TelemetryConfig, error) {
	cfg, err := telemetry.ResolveConfig(telemetry.OSEnv(), telemetry.Prefs{}, telemetry.TelemetryFields{})
	if err != nil {
		return TelemetryConfig{}, err
	}
	return fromTelemetryConfig(cfg), nil
}

// fromTelemetryConfig converts telemetry.Config into TelemetryConfig. Paired
// with ToTelemetryConfig below as the single named converter this package
// owns; internal/telemetry does not grow its own mirror of this mapping.
func fromTelemetryConfig(c telemetry.Config) TelemetryConfig {
	return TelemetryConfig{
		Mode:               string(c.Mode),
		ServiceName:        c.ServiceName,
		ResourceAttributes: c.ResourceAttributes,
		OTLPEndpoint:       c.OTLPEndpoint,
		OTLPProtocol:       c.OTLPProtocol,
		OTLPHeaders:        c.OTLPHeaders,
		OTLPInsecure:       c.OTLPInsecure,
		MetricsExporter:    c.MetricsExporter,
		TracesExporter:     c.TracesExporter,
		PrometheusAddr:     c.PrometheusAddr,
		PrometheusPath:     c.PrometheusPath,
	}
}

// ToTelemetryConfig converts the merged [telemetry] table back into
// telemetry.Config for telemetry.Init. Exported so cmd/jig's setupTelemetry
// can call it directly (Task 4.6) instead of internal/telemetry growing its
// own mirror of this mapping.
func (c TelemetryConfig) ToTelemetryConfig() telemetry.Config {
	return telemetry.Config{
		Mode:               telemetry.Mode(c.Mode),
		ServiceName:        c.ServiceName,
		ResourceAttributes: c.ResourceAttributes,
		OTLPEndpoint:       c.OTLPEndpoint,
		OTLPProtocol:       c.OTLPProtocol,
		OTLPHeaders:        c.OTLPHeaders,
		OTLPInsecure:       c.OTLPInsecure,
		MetricsExporter:    c.MetricsExporter,
		TracesExporter:     c.TracesExporter,
		PrometheusAddr:     c.PrometheusAddr,
		PrometheusPath:     c.PrometheusPath,
	}
}

// applyTelemetryKillSwitch forces Mode off when OTEL_SDK_DISABLED is active,
// regardless of what any [telemetry] layer — including an explicit
// config.toml value — set it to. Called once, as the final step after every
// [telemetry] layer (env-derived base, user config, project config) has
// been merged, mirroring telemetry.ResolveConfig's own kill-switch check so
// a config.toml mode override can never silently re-enable telemetry the
// kill switch disabled.
func applyTelemetryKillSwitch(c TelemetryConfig) TelemetryConfig {
	if telemetry.KillSwitchActive() {
		c.Mode = string(telemetry.ModeOff)
	}
	return c
}
