package runner

import (
	"context"
	"os"
	"reflect"
	"testing"

	"jig/internal/sentinel"
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
	defs, err := BuiltinMonitors()
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
			if _, err := parseBuiltinMonitor("test", tc.data, noCallDispatcher{}); err == nil {
				t.Fatal("expected construction error")
			}
		})
	}
}
