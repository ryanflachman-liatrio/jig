package ops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04PolicyDoctor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(path, []byte("version=1\nallow_host=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := sandbox.Load(sandbox.Selection{ExplicitPath: path, Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Decode("[workflow]\nname='doctor-policy'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='true'\nsecrets=['synthetic']\n", "")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	opts := DoctorOptions{Workflow: wf, Cwd: root, SandboxPolicy: p, LookupEnv: func(string) (string, bool) { calls++; return "PRIVATE_SENTINEL", true }, LookPath: func(string) (string, error) { calls++; return "", errors.New("PRIVATE_SENTINEL") }, RunGit: func(string, ...string) error { calls++; return nil }}
	report := Doctor(opts)
	if report.OK || calls != 0 {
		t.Fatalf("report=%+v calls=%d", report, calls)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sandbox_policy_denied", "allow_host", "auxiliary_host", "explicit"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
		t.Fatal("private value disclosed")
	}
	opts.SandboxPolicyError = errors.New("PRIVATE_SENTINEL")
	raw, _ = json.Marshal(Doctor(opts))
	if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
		t.Fatal("raw error disclosed")
	}
	t.Log("doctor reports operator source, allow_host denial and auxiliary-host scope; secret/tool/git calls=0")
}

func TestD04EndpointHostOnlyDoctor(t *testing.T) {
	wf, err := workflow.Decode("[workflow]\nname='host'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='true'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	opts := DoctorOptions{Workflow: wf, EndpointInput: sandbox.EndpointInput{EnvConfig: "relative-invalid", EnvContext: "PRIVATE_SENTINEL", EnvHost: "invalid"}}
	if checks := inspectSandboxDeployment(opts, wf, newSandboxReport(opts, wf, true)); len(checks) != 0 {
		t.Fatalf("host selected endpoint: %+v", checks)
	}
	opts.EndpointInput.Host = "unix:///synthetic/missing.sock"
	checks := inspectSandboxDeployment(opts, wf, newSandboxReport(opts, wf, true))
	if len(checks) != 1 || checks[0].Status != CheckFail || !strings.Contains(checks[0].Message, "sandbox_policy_denied") {
		t.Fatalf("explicit selection bypassed policy: %+v", checks)
	}
}

func TestD04DoctorStoreEffects(t *testing.T) {
	for _, mode := range []string{"read-only", "write-probe", "create-failure", "close-failure", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			missing := filepath.Join(root, "missing", "store")
			creates, removes := 0, 0
			effects := storeProbeEffects{create: func(dir, pattern string) (*os.File, error) {
				creates++
				if mode == "create-failure" {
					return nil, errors.New("synthetic failure")
				}
				f, err := os.CreateTemp(dir, pattern)
				if err == nil && mode == "close-failure" {
					f.Close()
				}
				return f, err
			}, remove: func(path string) error {
				removes++
				if mode == "cleanup-failure" {
					return errors.New("synthetic failure")
				}
				return os.Remove(path)
			}}
			result := inspectStoreAccess(missing, mode != "read-only", effects)
			switch mode {
			case "read-only":
				if result.Status != CheckPass || !strings.Contains(result.Message, "unverified") || creates != 0 || removes != 0 {
					t.Fatalf("default effects: %+v %d/%d", result, creates, removes)
				}
			case "write-probe":
				if result.Status != CheckPass || creates != 1 || removes != 1 {
					t.Fatalf("probe effects: %+v %d/%d", result, creates, removes)
				}
			default:
				if result.Status != CheckFail {
					t.Fatalf("failed probe passed: %+v", result)
				}
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatal("doctor created missing root")
			}
			t.Logf("create=%d remove=%d missing-root-created=false", creates, removes)
		})
	}
}
