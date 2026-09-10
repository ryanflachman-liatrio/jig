package scaffold

import "testing"

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "parent traversal", input: "../escape", wantErr: true},
		{name: "slash", input: "a/b", wantErr: true},
		{name: "backslash", input: `a\b`, wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "double dot", input: "..", wantErr: true},
		{name: "uppercase is normalized", input: "Mixed-Case", want: "mixed-case"},
		{name: "valid", input: "api_v2.workflow", want: "api_v2.workflow"},
		{name: "space", input: "two words", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateName(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateName(%q) unexpectedly succeeded with %q", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateName(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ValidateName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestDefaultName(t *testing.T) {
	got := DefaultName("/tmp/My-Project")
	if got != "my-project" {
		t.Fatalf("DefaultName = %q, want %q", got, "my-project")
	}
}
