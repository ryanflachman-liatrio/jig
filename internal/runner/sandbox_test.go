package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04PolicyDirectEntryPoints(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(path, []byte("version=1\nallow_host=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := sandbox.Load(sandbox.Selection{ExplicitPath: path, Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "must-not-exist")
	calls := 0
	factory := func(string) (harness.Harness, error) { calls++; return nil, errors.New("must not open") }
	command := NewCommandExecutor(root)
	agent := NewAgentExecutor(factory)
	check := NewCheckExecutor(root)
	mux := NewMux()
	mux.Register(workflow.StepCommand, command)
	st := &workflow.Step{ID: "work", Type: workflow.StepCommand, Run: "touch " + marker, Validate: &workflow.Validate{Command: "touch " + marker}}
	req := engine.StepRequest{Step: st, SandboxAuthorizer: policy}
	for _, exec := range []engine.Executor{command, agent, check, mux} {
		_, err := exec.Execute(context.Background(), req, nil)
		var failure *sandbox.Failure
		if !errors.As(err, &failure) || failure.Field != "allow_host" {
			t.Fatalf("execute %T: %v", exec, err)
		}
		validator := exec.(engine.Validator)
		_, _, err = validator.Validate(context.Background(), req, nil)
		if !errors.As(err, &failure) || failure.Field != "allow_host" {
			t.Fatalf("validate %T: %v", exec, err)
		}
	}
	_, err = NewIntegrationResolver(factory).ResolveIntegration(context.Background(), engine.IntegrationResolutionRequest{Step: st, Worktree: root, SandboxAuthorizer: policy}, nil)
	if err == nil {
		t.Fatal("resolver permitted")
	}
	// A review-origin validator retains the same selected request and policy.
	st.Type = workflow.StepReview
	if _, _, err := agent.Validate(context.Background(), req, nil); err == nil {
		t.Fatal("review-origin validator bypass")
	}
	// Missing policy never lets a direct Docker request run on host either.
	wf, err := workflow.Decode(`[workflow]
name='direct'
version='1'
[[sandbox]]
name='box'
runtime='docker'
image='alpine'
[[step]]
id='work'
type='command'
run='true'
sandbox='box'
`, "")
	if err != nil {
		t.Fatal(err)
	}
	req = engine.StepRequest{Step: &wf.Steps[0]}
	for _, exec := range []engine.Executor{command, agent, check, mux} {
		_, err := exec.Execute(context.Background(), req, nil)
		var unsupported *engine.SandboxRuntimeError
		if !errors.As(err, &unsupported) {
			t.Fatalf("unsupported %T: %v", exec, err)
		}
	}
	if calls != 0 {
		t.Fatalf("harness calls=%d", calls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("command/validator executed")
	}
	t.Log("command/check/agent/mux workloads and validators plus resolver denied; harness=0 command=0 validator=0")
}
