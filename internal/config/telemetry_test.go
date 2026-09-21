package config

import (
	"path/filepath"
	"testing"

	"jig/internal/telemetry"
)

func TestLoadTelemetryEnvBaseOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))
	clearTelemetryEnv(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector.example.invalid")

	cfg, err := Load("", filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.Telemetry.Mode != string(telemetry.ModeOTLP) {
		t.Fatalf("Mode = %q, want %q (derived from OTEL_METRICS_EXPORTER=otlp)", cfg.Telemetry.Mode, telemetry.ModeOTLP)
	}
	if cfg.Telemetry.OTLPEndpoint != "https://collector.example.invalid" {
		t.Fatalf("OTLPEndpoint = %q, want the env value (no config.toml override present)", cfg.Telemetry.OTLPEndpoint)
	}
}

func TestLoadTelemetryConfigOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))
	clearTelemetryEnv(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://env-collector.example.invalid")

	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), `[telemetry]
otlp_endpoint = "https://config-collector.example.invalid"
`)

	cfg, err := Load("", projectRoot)
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.Telemetry.OTLPEndpoint != "https://config-collector.example.invalid" {
		t.Fatalf("OTLPEndpoint = %q, want config.toml's value to win over the env-derived base", cfg.Telemetry.OTLPEndpoint)
	}
	// Mode is still env-derived: config.toml did not set [telemetry] mode,
	// so the base layer's otlp-derived Mode should survive the merge.
	if cfg.Telemetry.Mode != string(telemetry.ModeOTLP) {
		t.Fatalf("Mode = %q, want %q (config.toml did not override mode)", cfg.Telemetry.Mode, telemetry.ModeOTLP)
	}
}

func TestLoadTelemetryKillSwitchForcesModeOffAfterConfigOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))
	clearTelemetryEnv(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")

	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), `[telemetry]
mode = "both"
otlp_endpoint = "https://config-collector.example.invalid"
`)

	cfg, err := Load("", projectRoot)
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.Telemetry.Mode != string(telemetry.ModeOff) {
		t.Fatalf("Mode = %q, want off: OTEL_SDK_DISABLED must force Mode off even though config.toml set mode=\"both\"", cfg.Telemetry.Mode)
	}
}
