package harness

import (
	"strings"
	"testing"
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
