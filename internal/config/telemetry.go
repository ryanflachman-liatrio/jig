package config

import "github.com/ryanflachman-liatrio/jig/internal/telemetry"

// telemetryDefaultsFromEnv builds the [telemetry] defaults layer by
// resolving telemetry.Config from process env only (no prefs file, no
// workflow [telemetry] contribution). Load merges config.toml layers on top.
func telemetryDefaultsFromEnv() (TelemetryConfig, error) {
	cfg, err := telemetry.ResolveConfig(telemetry.OSEnv(), telemetry.Prefs{}, telemetry.TelemetryFields{})
	if err != nil {
		return TelemetryConfig{}, err
	}
	return fromTelemetryConfig(cfg), nil
}

// fromTelemetryConfig converts telemetry.Config into TelemetryConfig; it is
// the inverse of ToTelemetryConfig, so change the two together.
func fromTelemetryConfig(c telemetry.Config) TelemetryConfig {
	insecure := c.OTLPInsecure
	return TelemetryConfig{
		Mode:               string(c.Mode),
		ServiceName:        c.ServiceName,
		ResourceAttributes: c.ResourceAttributes,
		OTLPEndpoint:       c.OTLPEndpoint,
		OTLPProtocol:       c.OTLPProtocol,
		OTLPHeaders:        c.OTLPHeaders,
		OTLPInsecure:       &insecure,
		MetricsExporter:    c.MetricsExporter,
		TracesExporter:     c.TracesExporter,
		PrometheusAddr:     c.PrometheusAddr,
		PrometheusPath:     c.PrometheusPath,
	}
}

// ToTelemetryConfig converts the merged [telemetry] table back into
// telemetry.Config for telemetry.Init.
func (c TelemetryConfig) ToTelemetryConfig() telemetry.Config {
	return telemetry.Config{
		Mode:               telemetry.Mode(c.Mode),
		ServiceName:        c.ServiceName,
		ResourceAttributes: c.ResourceAttributes,
		OTLPEndpoint:       c.OTLPEndpoint,
		OTLPProtocol:       c.OTLPProtocol,
		OTLPHeaders:        c.OTLPHeaders,
		OTLPInsecure:       c.OTLPInsecure != nil && *c.OTLPInsecure,
		MetricsExporter:    c.MetricsExporter,
		TracesExporter:     c.TracesExporter,
		PrometheusAddr:     c.PrometheusAddr,
		PrometheusPath:     c.PrometheusPath,
	}
}

// applyTelemetryKillSwitch forces Mode off when OTEL_SDK_DISABLED is active,
// whatever any layer set. Load applies it once, after the final merge, so a
// config.toml mode can never re-enable telemetry the kill switch disabled.
func applyTelemetryKillSwitch(c TelemetryConfig) TelemetryConfig {
	if telemetry.KillSwitchActive() {
		c.Mode = string(telemetry.ModeOff)
	}
	return c
}
