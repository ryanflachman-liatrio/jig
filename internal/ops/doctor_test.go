package ops

import (
	"fmt"
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
