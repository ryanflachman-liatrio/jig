package telemetry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMetricPrefix(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"lowercase ok", "jig", false},
		{"mixed ok", "MyPrefix_1", false},
		{"underscore ok", "a_b_c", false},
		{"digit ok", "abc123", false},
		{"digit first", "1abc", true},
		{"punct rejected", "a.b", true},
		{"space rejected", "a b", true},
		{"too long", strings.Repeat("a", 33), true},
		{"just at bound", strings.Repeat("a", 32), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMetricPrefix(tc.prefix)
			if tc.wantErr && err == nil {
				t.Errorf("want err, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
		})
	}
}

func TestValidateResourceAttrKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"dotted", "service.name", false},
		{"snake", "deployment_environment", false},
		{"single", "env", false},
		{"empty", "", true},
		{"uppercase", "Service.Name", true},
		{"leading digit", "1env", true},
		{"punct", "service-name", true},
		{"space", "a b", true},
		{"leading dot", ".foo", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateResourceAttrKey(tc.key)
			if tc.wantErr && err == nil {
				t.Errorf("want err, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
		})
	}
}

func TestResolveConfigPrecedence(t *testing.T) {
	// Env > prefs > workflow. Confirm each layer overrides the one below.
	tel := TelemetryFields{Enabled: true, MetricPrefix: "wf", ResourceAttributes: map[string]string{"a": "1"}}
	prefs := Prefs{
		Mode:           string(ModeProm),
		PrometheusAddr: "127.0.0.1:9464",
		MetricPrefix:   "prefs",
		ResourceAttributes: map[string]string{"a": "prefs", "b": "2"},
	}
	env := MapEnv(map[string]string{
		"OTEL_SERVICE_NAME":            "envsvc",
		"OTEL_RESOURCE_ATTRIBUTES":     "a=env,c=3",
		"JIG_TELEMETRY_MODE":           "both",
		"JIG_TELEMETRY_PROMETHEUS_ADDR": "127.0.0.1:9500",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  "https://collector:4317",
	})

	cfg, err := ResolveConfig(env, prefs, tel)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Mode != ModeBoth {
		t.Errorf("Mode = %q, want both", cfg.Mode)
	}
	if cfg.ServiceName != "envsvc" {
		t.Errorf("ServiceName = %q, want envsvc", cfg.ServiceName)
	}
	if cfg.PrometheusAddr != "127.0.0.1:9500" {
		t.Errorf("PrometheusAddr = %q, want env override", cfg.PrometheusAddr)
	}
	if cfg.MetricPrefix != "prefs" {
		t.Errorf("MetricPrefix = %q, want prefs (env has no override)", cfg.MetricPrefix)
	}
	if cfg.ResourceAttributes["a"] != "env" {
		t.Errorf("resource[a] = %q, want env (env override)", cfg.ResourceAttributes["a"])
	}
	if cfg.ResourceAttributes["b"] != "2" {
		t.Errorf("resource[b] = %q, want 2 (prefs only)", cfg.ResourceAttributes["b"])
	}
	if cfg.ResourceAttributes["c"] != "3" {
		t.Errorf("resource[c] = %q, want 3 (env only)", cfg.ResourceAttributes["c"])
	}
}

func TestResolveConfigKillSwitch(t *testing.T) {
	env := MapEnv(map[string]string{
		"OTEL_SDK_DISABLED":             "true",
		"JIG_TELEMETRY_MODE":            "both",
		"OTEL_EXPORTER_OTLP_ENDPOINT":   "https://collector",
		"JIG_TELEMETRY_PROMETHEUS_ADDR": "127.0.0.1:9464",
	})
	cfg, err := ResolveConfig(env, Prefs{}, TelemetryFields{})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Mode != ModeOff {
		t.Errorf("Mode = %q, want off (kill switch)", cfg.Mode)
	}
}

func TestResolveConfigWorkflowEnabledWithoutExporter(t *testing.T) {
	// A workflow that opts in without a corresponding env / prefs exporter
	// target must fail at load time so the operator does not get silence.
	_, err := ResolveConfig(MapEnv(nil), Prefs{}, TelemetryFields{Enabled: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no exporter configured") {
		t.Errorf("error = %v; want 'no exporter configured' hint", err)
	}
}

func TestResolveConfigInvalidMetricPrefixFromEnv(t *testing.T) {
	// Prefs sets an illegal prefix; env layer does not override.
	_, err := ResolveConfig(MapEnv(nil), Prefs{MetricPrefix: "1bad"}, TelemetryFields{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveConfigDeriveModeFromOTelExporters(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Mode
	}{
		{"metrics=prometheus", map[string]string{"OTEL_METRICS_EXPORTER": "prometheus"}, ModeProm},
		{"metrics=otlp", map[string]string{"OTEL_METRICS_EXPORTER": "otlp"}, ModeOTLP},
		{"traces=otlp", map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, ModeOTLP},
		{"both", map[string]string{"OTEL_METRICS_EXPORTER": "prometheus", "OTEL_TRACES_EXPORTER": "otlp"}, ModeBoth},
		{"none", map[string]string{"OTEL_METRICS_EXPORTER": "none"}, ModeOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ResolveConfig(MapEnv(tc.env), Prefs{}, TelemetryFields{})
			if err != nil {
				t.Fatalf("ResolveConfig: %v", err)
			}
			if cfg.Mode != tc.want {
				t.Errorf("Mode = %q, want %q", cfg.Mode, tc.want)
			}
		})
	}
}

func TestResolveConfigUnknownModeIsOff(t *testing.T) {
	cfg, err := ResolveConfig(MapEnv(map[string]string{"JIG_TELEMETRY_MODE": "bogus"}), Prefs{}, TelemetryFields{})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Mode != ModeOff {
		t.Errorf("Mode = %q, want off (unknown collapses)", cfg.Mode)
	}
}

func TestParseKVListAndBool(t *testing.T) {
	got := parseKVList("a=1, b = two,,x=,=noop,c=3")
	if got["a"] != "1" || got["b"] != "two" || got["c"] != "3" || got["x"] != "" {
		t.Errorf("parseKVList: %+v", got)
	}
	if len(got) != 4 {
		t.Errorf("parseKVList len = %d, want 4", len(got))
	}
	trueCases := []string{"1", "true", "TRUE", "yes", "on"}
	for _, v := range trueCases {
		if !parseBool(v) {
			t.Errorf("parseBool(%q) = false", v)
		}
	}
	falseCases := []string{"", "0", "false", "no", "off", "bogus"}
	for _, v := range falseCases {
		if parseBool(v) {
			t.Errorf("parseBool(%q) = true", v)
		}
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Prefs{
		Mode:           string(ModeProm),
		PrometheusAddr: "127.0.0.1:9464",
		MetricPrefix:   "jig",
	}
	if err := SavePrefs(dir, want); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	got := LoadPrefs(dir)
	if got.Mode != want.Mode || got.PrometheusAddr != want.PrometheusAddr || got.MetricPrefix != want.MetricPrefix {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestPrefsMissingFileReturnsZero(t *testing.T) {
	got := LoadPrefs(t.TempDir())
	if got.Mode != "" || got.PrometheusAddr != "" {
		t.Errorf("missing file should yield zero, got %+v", got)
	}
}

func TestPrefsCorruptFileReturnsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PrefsFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := LoadPrefs(dir)
	if got.Mode != "" {
		t.Errorf("corrupt file should yield zero, got %+v", got)
	}
}

func TestPrefsEmptyRootIsNoOp(t *testing.T) {
	if err := SavePrefs("", Prefs{Mode: "prom"}); err != nil {
		t.Errorf("SavePrefs(\"\"): %v", err)
	}
	if got := LoadPrefs(""); got.Mode != "" {
		t.Errorf("LoadPrefs(\"\") = %+v, want zero", got)
	}
	if PrefsPath("") != "" {
		t.Errorf("PrefsPath(\"\") = %q, want empty", PrefsPath(""))
	}
}

func TestValidateResolvedNoOnWarnPanic(t *testing.T) {
	// Ensure validateResolved's warn path is safe when Config has no OnWarn.
	env := MapEnv(map[string]string{"JIG_TELEMETRY_MODE": "prom"})
	// Should not panic; warning goes to stderr.
	if _, err := ResolveConfig(env, Prefs{}, TelemetryFields{}); err != nil {
		var e *os.PathError
		if errors.As(err, &e) {
			t.Fatalf("unexpected err: %v", err)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]string{"b": "", "a": "", "c": ""})
	want := []string{"a", "b", "c"}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("SortedKeys[%d] = %q, want %q", i, got[i], k)
		}
	}
}
