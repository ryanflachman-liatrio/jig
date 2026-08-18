package harness

import (
	"strings"
	"testing"
)

func TestFor(t *testing.T) {
	tests := []struct {
		name      string
		backend   string
		transport string
		wantName  string
		wantErr   bool
	}{
		{name: "claude sdk", backend: "claude", transport: "sdk", wantName: "claude"},
		{name: "empty defaults to claude sdk", wantName: "claude"},
		{name: "claude empty transport is sdk", backend: "claude", wantName: "claude"},
		{name: "claude acp", backend: "claude", transport: "acp", wantName: "acp"},
		{name: "cursor acp", backend: "cursor", transport: "acp", wantName: "acp"},
		{name: "cursor empty transport is acp", backend: "cursor", wantName: "acp"},
		{name: "cursor invalid transport", backend: "cursor", transport: "sdk", wantErr: true},
		{name: "unknown backend", backend: "gemini", transport: "acp", wantErr: true},
		{name: "unknown transport", backend: "claude", transport: "grpc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := For(tt.backend, tt.transport)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("For(%q, %q) error = nil, want an error", tt.backend, tt.transport)
				}
				if !strings.Contains(err.Error(), "want") {
					t.Errorf("error = %q, want it to list valid backend/transport names", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("For(%q, %q) error = %v", tt.backend, tt.transport, err)
			}
			if h.Name() != tt.wantName {
				t.Errorf("For(%q, %q).Name() = %q, want %q", tt.backend, tt.transport, h.Name(), tt.wantName)
			}
		})
	}
}
