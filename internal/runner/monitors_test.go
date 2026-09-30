package runner

import (
	"context"
	"os"
	"reflect"
	"testing"

	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
)

type noCallDispatcher struct{}

func (noCallDispatcher) Dispatch(context.Context, sentinel.MonitorSpec, string) (sentinel.MonitorResult, error) {
	return sentinel.MonitorResult{}, nil
}

func TestBuiltinMonitorsPortableRoster(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	defs, err := BuiltinMonitors(config.SecurityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, def := range defs {
		ids = append(ids, def.Monitor)
		if def.Spec.Model != monitorModel {
			t.Errorf("%s model = %q", def.Monitor, def.Spec.Model)
		}
		if def.Spec.Prompt == "" {
			t.Errorf("%s prompt is empty", def.Monitor)
		}
		if def.Dispatcher == nil || def.Circuit == nil {
			t.Errorf("%s is not dispatchable", def.Monitor)
		}
	}
	want := []string{"prompt-injection", "stuck-loop", "exfil-pattern"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if got, _ := os.Getwd(); got == cwd {
		t.Fatal("test did not change away from source checkout")
	}
}

func TestParseBuiltinMonitorRejectsMalformedDefinitions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"malformed frontmatter", []byte("---\nmodel\n---\nprompt")},
		{"empty prompt", []byte("---\nmodel: haiku\n---\n")},
		{"wrong model", []byte("---\nmodel: opus\n---\nprompt")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseBuiltinMonitor("test", tc.data, monitorModel, noCallDispatcher{}); err == nil {
				t.Fatal("expected construction error")
			}
		})
	}
}

func TestBuiltinMonitorsResolveConfiguredModel(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.SecurityConfig
		want string
	}{
		{"default claude", config.SecurityConfig{}, monitorModel},
		{"claude override", config.SecurityConfig{MonitorModel: "claude-sonnet-5-5"}, "claude-sonnet-5-5"},
		{"codex default", config.SecurityConfig{MonitorBackend: "codex"}, ""},
		{"codex model", config.SecurityConfig{MonitorBackend: "codex", MonitorModel: "gpt-5-codex"}, "gpt-5-codex"},
		{"cursor default", config.SecurityConfig{MonitorBackend: "cursor"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := BuiltinMonitors(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, def := range defs {
				if def.Spec.Model != tc.want {
					t.Errorf("%s model = %q, want %q", def.Monitor, def.Spec.Model, tc.want)
				}
				if a, ok := def.Dispatcher.(*MonitorAdapter); !ok || a.backend != tc.cfg.MonitorBackendOrDefault() {
					t.Errorf("%s dispatcher = %#v, want adapter on %s", def.Monitor, def.Dispatcher, tc.cfg.MonitorBackendOrDefault())
				}
			}
		})
	}
}

func TestBuiltinMonitorsRequireCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		caps    harness.CapabilitySet
		missing string
	}{
		{"no structured output", harness.NewCapabilitySet(harness.CapPermissionCallback), "CapStructuredOutput"},
		{"no permission callback", harness.NewCapabilitySet(harness.CapStructuredOutput), "CapPermissionCallback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness.FakeHarness{NameVal: "fake", Caps: tc.caps}
			_, err := builtinMonitors("codex", "", h, noCallDispatcher{})
			if err == nil {
				t.Fatal("expected a capability error")
			}
			for _, want := range []string{"codex", tc.missing} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
