package operatorcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const syntheticToken = "sk-test-example-invalid"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestInspect(t *testing.T) {
	type want struct {
		class Class
		file  string // relative to the case's temp dir
		key   string
		index int
	}
	for _, tc := range []struct {
		name    string
		backend string
		files   map[string]string
		env     map[string]string // values may reference {dir}
		cwd     string            // relative to the project root
		want    []want
	}{
		{
			name:    "cursor unrestricted approval mode is blanket",
			backend: "cursor",
			files:   map[string]string{"home/.cursor/cli-config.json": `{"approvalMode": "unrestricted"}`},
			want:    []want{{Blanket, "home/.cursor/cli-config.json", "approvalMode", -1}},
		},
		{
			name:    "cursor permissions.json is blanket",
			backend: "cursor",
			files:   map[string]string{"home/.cursor/permissions.json": `{}`},
			want:    []want{{Blanket, "home/.cursor/permissions.json", "permissions.json", -1}},
		},
		{
			name:    "cursor user allow entries are narrow",
			backend: "cursor",
			files:   map[string]string{"home/.cursor/cli-config.json": `{"approvalMode": "allowlist", "permissions": {"allow": ["Read(**)", "Shell(curl -H 'Bearer ` + syntheticToken + `')"]}}`},
			want: []want{
				{Narrow, "home/.cursor/cli-config.json", "permissions.allow", 0},
				{Narrow, "home/.cursor/cli-config.json", "permissions.allow", 1},
			},
		},
		{
			name:    "cursor project cli.json entries from root to cwd are narrow",
			backend: "cursor",
			cwd:     "pkg/sub",
			files: map[string]string{
				"project/.cursor/cli.json":         `{"permissions": {"allow": ["Shell(ls)"]}}`,
				"project/pkg/sub/.cursor/cli.json": `{"permissions": {"allow": ["Shell(` + syntheticToken + `)"]}}`,
				"elsewhere/.cursor/cli.json":       `{"permissions": {"allow": ["Shell(ls)"]}}`,
			},
			want: []want{
				{Narrow, "project/.cursor/cli.json", "permissions.allow", 0},
				{Narrow, "project/pkg/sub/.cursor/cli.json", "permissions.allow", 0},
			},
		},
		{
			name:    "cursor CURSOR_CONFIG_DIR wins over XDG and HOME",
			backend: "cursor",
			env:     map[string]string{"CURSOR_CONFIG_DIR": "{dir}/custom", "XDG_CONFIG_HOME": "{dir}/xdg"},
			files: map[string]string{
				"custom/cli-config.json":        `{"approvalMode": "unrestricted"}`,
				"xdg/cursor/cli-config.json":    `{"permissions": {"allow": ["Shell(ls)"]}}`,
				"home/.cursor/permissions.json": `{}`,
			},
			want: []want{{Blanket, "custom/cli-config.json", "approvalMode", -1}},
		},
		{
			name:    "cursor XDG_CONFIG_HOME wins over HOME",
			backend: "cursor",
			env:     map[string]string{"XDG_CONFIG_HOME": "{dir}/xdg"},
			files: map[string]string{
				"xdg/cursor/cli-config.json":    `{"permissions": {"allow": ["Shell(ls)"]}}`,
				"home/.cursor/permissions.json": `{}`,
			},
			want: []want{{Narrow, "xdg/cursor/cli-config.json", "permissions.allow", 0}},
		},
		{
			name:    "codex non-user approvals_reviewer is blanket",
			backend: "codex",
			files:   map[string]string{"home/.codex/config.toml": `approvals_reviewer = "auto"`},
			want:    []want{{Blanket, "home/.codex/config.toml", "approvals_reviewer", -1}},
		},
		{
			name:    "codex user approvals_reviewer is not a finding",
			backend: "codex",
			files:   map[string]string{"home/.codex/config.toml": "approvals_reviewer = \"user\"\nmodel = \"fixture\""},
		},
		{
			name:    "codex CODEX_HOME and CODEX_CONFIG reviewers are blanket",
			backend: "codex",
			env:     map[string]string{"CODEX_HOME": "{dir}/codexhome", "CODEX_CONFIG": `{"approvals_reviewer": "auto"}`},
			files: map[string]string{
				"codexhome/config.toml":   `approvals_reviewer = "auto"`,
				"home/.codex/config.toml": `approvals_reviewer = "auto"`,
			},
			want: []want{
				{Blanket, "codexhome/config.toml", "approvals_reviewer", -1},
				{Blanket, "CODEX_CONFIG", "approvals_reviewer", -1},
			},
		},
		{
			name:    "codex allow prefix rules are narrow, other decisions are not",
			backend: "codex",
			files: map[string]string{
				"home/.codex/rules/default.rules": `# prefix_rule(pattern = ["commented"])
prefix_rule(pattern = ["git", "status"], decision = "allow")
prefix_rule(pattern = ["rm"], decision = "forbidden")
prefix_rule(
    pattern = ["deploy", "--token=` + syntheticToken + `"],
    justification = "call (with parens) inside a string",
)
`,
				"project/.codex/rules/project.rules": `prefix_rule(pattern = ["make"], decision = "prompt")
prefix_rule(pattern = ["ls"])`,
			},
			want: []want{
				{Narrow, "home/.codex/rules/default.rules", "prefix_rule", 0},
				{Narrow, "home/.codex/rules/default.rules", "prefix_rule", 2},
				{Narrow, "project/.codex/rules/project.rules", "prefix_rule", 1},
			},
		},
		{
			name:    "claude has nothing to inspect",
			backend: "claude",
			files:   map[string]string{"home/.cursor/permissions.json": `{}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}
			vars := map[string]string{"HOME": filepath.Join(dir, "home")}
			for k, v := range tc.env {
				vars[k] = strings.ReplaceAll(v, "{dir}", dir)
			}
			root := filepath.Join(dir, "project")
			cwd := filepath.Join(root, tc.cwd)
			got, err := Inspect(tc.backend, root, cwd, envOf(vars))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("findings = %+v, want %d", got, len(tc.want))
			}
			for i, w := range tc.want {
				file := w.file
				if file != "CODEX_CONFIG" {
					file = filepath.Join(dir, w.file)
				}
				if got[i].Class != w.class || got[i].File != file || got[i].Key != w.key || got[i].Index != w.index {
					t.Fatalf("finding %d = %+v, want %+v", i, got[i], w)
				}
				if !strings.Contains(got[i].String(), file) {
					t.Fatalf("finding %q does not name its file", got[i])
				}
				if w.index >= 0 && !strings.Contains(got[i].String(), fmt.Sprintf("entry %d", w.index)) {
					t.Fatalf("finding %q does not name its entry index", got[i])
				}
			}
			if all := fmt.Sprintf("%+v", got); strings.Contains(all, syntheticToken) {
				t.Fatalf("a finding carried the synthetic token: %s", all)
			}
		})
	}
}

func TestInspectFailsClosedOnMalformedConfig(t *testing.T) {
	for _, tc := range []struct{ backend, file, content string }{
		{"cursor", "home/.cursor/cli-config.json", `{"approvalMode": `},
		{"codex", "home/.codex/config.toml", `approvals_reviewer = `},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, tc.file), tc.content)
			_, err := Inspect(tc.backend, filepath.Join(dir, "project"), "", envOf(map[string]string{"HOME": filepath.Join(dir, "home")}))
			if err == nil || !strings.Contains(err.Error(), tc.file[strings.LastIndex(tc.file, "/")+1:]) {
				t.Fatalf("err = %v, want an error naming the malformed file", err)
			}
		})
	}
}
