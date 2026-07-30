package workflow

import "testing"

func TestDecodeMeta(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantOK   bool
		wantName string
		wantErr  bool
	}{
		{
			name: "full workflow table",
			data: `
[workflow]
name = "bugfix"
version = "1"
description = "Triage and fix a reported bug"

[[step]]
id = "triage"
type = "agent"
skill = "skills/triage"
`,
			wantOK:   true,
			wantName: "bugfix",
		},
		{
			name: "name only",
			data: `
[workflow]
name = "minimal"
`,
			wantOK:   true,
			wantName: "minimal",
		},
		{
			name:   "no workflow table",
			data:   "[defaults]\nmodel = \"claude\"\n",
			wantOK: false,
		},
		{
			name: "workflow table without name",
			data: `
[workflow]
version = "1"
description = "nameless"
`,
			wantOK: false,
		},
		{
			// A file that would fail full validation (missing step type) still
			// yields its meta: LoadMeta is a tolerant peek, not a validator.
			name: "invalid steps but valid meta",
			data: `
[workflow]
name = "partly-broken"

[[step]]
id = "oops"
`,
			wantOK:   true,
			wantName: "partly-broken",
		},
		{
			name:    "malformed toml",
			data:    "[workflow\nname = ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, ok, err := DecodeMeta(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && meta.Name != tt.wantName {
				t.Fatalf("name = %q, want %q", meta.Name, tt.wantName)
			}
		})
	}
}
