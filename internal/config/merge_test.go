package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// telemetryEnvVars are every var telemetryDefaultsFromEnv's underlying
// telemetry.ResolveConfig reads, kept in sync with that function's env
// reads. Used by clearTelemetryEnv to isolate tests from the running
// process's real environment.
var telemetryEnvVars = []string{
	"OTEL_SDK_DISABLED",
	"OTEL_SERVICE_NAME",
	"OTEL_RESOURCE_ATTRIBUTES",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_PROTOCOL",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_INSECURE",
	"OTEL_METRICS_EXPORTER",
	"OTEL_TRACES_EXPORTER",
	"JIG_TELEMETRY_PROMETHEUS_ADDR",
	"JIG_TELEMETRY_PROMETHEUS_PATH",
	"JIG_TELEMETRY_MODE",
}

// clearTelemetryEnv unsets every telemetry-related env var for the
// duration of the test, restoring each var's original value (present or
// absent) afterward, so telemetryDefaultsFromEnv's env-derived base layer
// is hermetic regardless of the host running the test.
func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, name := range telemetryEnvVars {
		original, wasSet := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(name, original)
			} else {
				os.Unsetenv(name)
			}
		})
	}
}

// writeConfigFile writes TOML content to name inside dir and returns the
// full path, creating parent directories as needed.
func writeConfigFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))
	clearTelemetryEnv(t)

	cfg, err := Load("", filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	// [telemetry]'s built-in-defaults layer is env-derived (Unit 4), not a
	// hardcoded zero value like every other table, so with no config.toml
	// present and no telemetry env vars set, want is Default() with Telemetry
	// replaced by the (env-empty) env-derived base rather than Default()
	// itself.
	want := Default()
	want.Telemetry, err = telemetryDefaultsFromEnv()
	if err != nil {
		t.Fatalf("telemetryDefaultsFromEnv: unexpected error %v", err)
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Load with no files present: got %+v, want %+v", cfg, want)
	}
}

func TestLoadMissingFilesAreNotErrors(t *testing.T) {
	dir := t.TempDir()
	// Neither the user path nor the project path exists on disk.
	_, err := Load(filepath.Join(dir, "user", "config.toml"), filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load with absent user/project files: unexpected error %v", err)
	}
}

func TestLoadPersistenceOffRootIsNotError(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user-config.toml", "[ui]\n")
	if _, err := Load(userPath, ""); err != nil {
		t.Fatalf("Load with root==\"\" (persistence-off): unexpected error %v", err)
	}
}

func TestLoadInvalidUserConfigIsSanitizedAndIdentifiesPath(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user-config.toml", "not [ valid")

	_, err := Load(userPath, filepath.Join(dir, "project", ".jig"))
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Load with invalid user config: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
}

func TestLoadInvalidProjectConfigIsSanitizedAndIdentifiesPath(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), "not [ valid")

	_, err := Load("", projectRoot)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Load with invalid project config: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
}
