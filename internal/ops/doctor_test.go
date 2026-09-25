package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/workflow"
)

func TestDoctorCIGatesAndSecrets(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "doctor"
version = "0.1"

[[step]]
id = "command"
type = "command"
run = "true"
secrets = ["api-key"]

[[step]]
id = "review"
type = "review"
depends_on = ["command"]
output_type = { enum = ["approve", "reject"] }
  [[step.review]]
  source = "diff"
  label = "Changes"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	secret := "do-not-print-this"
	report := Doctor(DoctorOptions{
		Root: t.TempDir(), Workflow: wf, CI: true,
		LookupEnv: func(key string) (string, bool) { return secret, key == "JIG_SECRET_API_KEY" },
		LookPath:  func(binary string) (string, error) { return "", fmt.Errorf("missing") },
	})
	if report.OK {
		t.Fatal("doctor unexpectedly passed a review workflow under CI")
	}
	encoded := fmt.Sprintf("%#v", report)
	if strings.Contains(encoded, secret) {
		t.Fatal("doctor leaked a secret value")
	}
	foundGate, foundSecret := false, false
	for _, check := range report.Checks {
		foundGate = foundGate || check.ID == "ci.human_gate" && check.Status == CheckFail
		foundSecret = foundSecret || check.ID == "secret.environment" && check.Status == CheckPass
	}
	if !foundGate || !foundSecret {
		t.Fatalf("checks = %#v", report.Checks)
	}
}

// TestDoctorClaudeUserSettings proves doctor warns when user-level Claude
// settings carry auth or env keys that settingSources: ["project"] drops, and
// that the warning never echoes a value.
func TestDoctorClaudeUserSettings(t *testing.T) {
	const synthetic = "sk-test-example-invalid"
	for _, tc := range []struct {
		name     string
		settings string
		wantKeys []string
	}{
		{name: "env", settings: `{"env": {"ANTHROPIC_API_KEY": "` + synthetic + `"}}`, wantKeys: []string{"env"}},
		{name: "apiKeyHelper", settings: `{"apiKeyHelper": "/bin/echo ` + synthetic + `"}`, wantKeys: []string{"apiKeyHelper"}},
		{name: "both", settings: `{"env": {"X": "` + synthetic + `"}, "apiKeyHelper": "` + synthetic + `"}`, wantKeys: []string{"env", "apiKeyHelper"}},
		{name: "neither", settings: `{"model": "haiku", "permissions": {"allow": ["Bash(ls)"]}}`},
		{name: "no settings file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tc.settings != "" {
				if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report := Doctor(DoctorOptions{
				Root:     t.TempDir(),
				LookPath: func(binary string) (string, error) { return "/usr/bin/" + binary, nil },
			})
			if encoded := fmt.Sprintf("%#v", report); strings.Contains(encoded, synthetic) || strings.Contains(encoded, "ANTHROPIC_API_KEY") {
				t.Fatalf("doctor leaked a settings value or env variable name: %s", encoded)
			}
			var warning *Check
			for i := range report.Checks {
				if report.Checks[i].ID == "backend.claude.user_settings" {
					warning = &report.Checks[i]
				}
			}
			if len(tc.wantKeys) == 0 {
				if warning != nil {
					t.Fatalf("unexpected warning: %+v", *warning)
				}
				return
			}
			if warning == nil || warning.Status != CheckWarn {
				t.Fatalf("checks = %#v, want a user_settings warning", report.Checks)
			}
			for _, key := range tc.wantKeys {
				if !strings.Contains(warning.Message, key) {
					t.Fatalf("warning %q does not name %s", warning.Message, key)
				}
			}
			if !strings.Contains(warning.Message, `settingSources: ["project"]`) {
				t.Fatalf("warning %q does not explain settingSources", warning.Message)
			}
		})
	}
}
