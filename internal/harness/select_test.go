package harness

import (
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
)

func TestFor(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		wantName string
		wantErr  bool
	}{
		{name: "claude uses acp harness", backend: "claude", wantName: "acp"},
		{name: "empty defaults to claude", wantName: "acp"},
		{name: "cursor uses cursor harness", backend: "cursor", wantName: "cursor"},
		{name: "codex uses codex harness", backend: "codex", wantName: "codex"},
		{name: "unknown backend", backend: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := For(tt.backend)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("For(%q) error = nil, want an error", tt.backend)
				}
				if !strings.Contains(err.Error(), "want") {
					t.Errorf("error = %q, want it to list valid backend names", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("For(%q) error = %v", tt.backend, err)
			}
			if h.Name() != tt.wantName {
				t.Errorf("For(%q).Name() = %q, want %q", tt.backend, h.Name(), tt.wantName)
			}
		})
	}
}

// TestForResolvesEveryAgentcfgBackend keeps agentcfg.Backends (the list
// config validation accepts) in lockstep with the backends For can build.
func TestForResolvesEveryAgentcfgBackend(t *testing.T) {
	for _, b := range agentcfg.Backends {
		if _, err := For(b); err != nil {
			t.Errorf("For(%q) = %v, want a harness", b, err)
		}
	}
}

func TestCapabilityString(t *testing.T) {
	tests := map[Capability]string{
		CapPermissionCallback: "CapPermissionCallback",
		CapUserQuestion:       "CapUserQuestion",
		CapSessionResume:      "CapSessionResume",
		CapStructuredOutput:   "CapStructuredOutput",
		CapPartialStreaming:   "CapPartialStreaming",
		Capability(1 << 7):    "Capability(128)",
	}
	for c, want := range tests {
		if got := c.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", uint8(c), got, want)
		}
	}
}

func TestRequire(t *testing.T) {
	tests := []struct {
		name    string
		caps    CapabilitySet
		require []Capability
		wantErr []string
	}{
		{"all present", NewCapabilitySet(CapStructuredOutput, CapPermissionCallback), []Capability{CapStructuredOutput, CapPermissionCallback}, nil},
		{"nothing required", 0, nil, nil},
		{"one missing", NewCapabilitySet(CapPermissionCallback), []Capability{CapStructuredOutput, CapPermissionCallback}, []string{"fake-backend", "CapStructuredOutput"}},
		{"two missing", 0, []Capability{CapSessionResume, CapPartialStreaming}, []string{"fake-backend", "CapSessionResume", "CapPartialStreaming"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Require(&FakeHarness{NameVal: "fake-backend", Caps: tc.caps}, tc.require...)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Require = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Require = nil, want error")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
