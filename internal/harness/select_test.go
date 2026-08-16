package harness

import "testing"

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		unset   bool
		want    string
		wantErr bool
	}{
		{name: "unset defaults to claude", unset: true, want: "claude"},
		{name: "explicit claude", val: "claude", want: "claude"},
		{name: "acp", val: "acp", want: "acp"},
		{name: "unknown value errors", val: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unset {
				t.Setenv("JIG_HARNESS", "")
			} else {
				t.Setenv("JIG_HARNESS", tt.val)
			}
			h, err := FromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FromEnv() error = nil, want an error for %q", tt.val)
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv() error = %v", err)
			}
			if h.Name() != tt.want {
				t.Errorf("FromEnv().Name() = %q, want %q", h.Name(), tt.want)
			}
		})
	}
}
