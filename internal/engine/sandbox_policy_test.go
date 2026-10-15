package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d04HostPolicy(t *testing.T, allow bool) *sandbox.Policy {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "policy.toml")
	raw := "version=1\nallow_host=false\n"
	if allow {
		raw = "version=1\nallow_host=true\n"
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := sandbox.Load(sandbox.Selection{ExplicitPath: path, Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestD04PolicyAdmission(t *testing.T) {
	for _, kind := range []string{"command", "check", "agent", "review"} {
		t.Run(kind, func(t *testing.T) {
			wf := d04Decode(t, "[workflow]\nname='policy'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='true'\nsecrets=['synthetic']\n[step.validate]\ncommand='true'\n")
			wf.Steps[0].Type = workflow.StepType(kind)
			spy := &d04Spy{}
			root := filepath.Join(t.TempDir(), "absent")
			m := NewManager(spy, root)
			m.SetSandboxAuthorizer(d04HostPolicy(t, false))
			m.SetIntegrationResolver(spy)
			secretCalls := 0
			m.SetSecretResolver(func(string) (string, error) { secretCalls++; return "synthetic", nil })
			run, err := m.Start(wf)
			var failure *sandbox.Failure
			if run != nil || !errors.As(err, &failure) || failure.Field != "allow_host" {
				t.Fatalf("run=%v err=%v", run, err)
			}
			if spy.executions.Load()+spy.validations.Load()+spy.resolutions.Load() != 0 || secretCalls != 0 {
				t.Fatal("unauthorized call")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("storage created on denial")
			}
			t.Log("allow_host denied; workload=0 validator=0 resolver=0 secrets=0; storage absent")
		})
	}
}
func TestD04PolicySkippedAndChildRequests(t *testing.T) {
	wf := d04Decode(t, d04DockerWorkflow)
	m := NewManager(&d04Spy{}, "")
	m.SetSandboxAuthorizer(d04HostPolicy(t, true))
	_, err := m.Start(wf)
	var failure *sandbox.Failure
	if !errors.As(err, &failure) || failure.Field != "docker" {
		t.Fatalf("policy must precede runtime guard: %v", err)
	}
	// Explicit nested host remains a separately checked expanded request even
	// when the parent is a Docker selection. Use an authorizer that permits
	// Docker and denies host to prove complete-graph ordering.
	host := workflow.Step{ID: "module.child", Type: workflow.StepCommand, Run: "true", When: "false"}
	wf.Steps = append(wf.Steps, host)
	m.SetSandboxAuthorizer(d04DenyHost{})
	_, err = m.Start(wf)
	if err == nil || !strings.Contains(err.Error(), "module.child") {
		t.Fatalf("explicit child was not checked: %v", err)
	}
}

type d04DenyHost struct{}

func (d04DenyHost) Authorize(st *workflow.Step, _, _ []string) error {
	if st.ResolvedSandbox().Profile == nil {
		return &sandbox.Failure{Reason: "sandbox_policy_denied", Field: "allow_host", StepID: st.ID}
	}
	return nil
}
func TestD04PolicyExecutionBoundaries(t *testing.T) {
	policy := d04HostPolicy(t, false)
	st := &workflow.Step{ID: "work", Type: workflow.StepAgent}
	spy := &d04Spy{}
	s := newScheduler(&workflow.Workflow{Steps: []workflow.Step{*st}}, "synthetic", make(chan schedMsg, 8), nil, spy, func() {}, nil, "", "", "", nil)
	s.sandboxAuthorizer = policy
	s.resolver = spy
	if _, err := s.acquireExecutionWorkspace(st); err == nil {
		t.Fatal("workspace bypass")
	}
	s.states[st.ID].Status = step.StatusAwaitingIntegration
	s.handleResolveIntegrationWithAgent(resolveIntegrationWithAgentMsg{stepID: st.ID})
	if spy.resolutions.Load() != 0 {
		t.Fatal("resolver bypass")
	}
	req := s.buildRequest(st, "synthetic", "", "", "", "")
	if err := AuthorizeSandboxExecution(req.SandboxAuthorizer, req.Step, nil, nil); err == nil {
		t.Fatal("worker lost policy")
	}
	// A denied direct secret-resolution call also fails before the resolver.
	secretCalls := 0
	s.secretResolver = func(string) (string, error) { secretCalls++; return "synthetic", nil }
	st.Secrets = []string{"synthetic"}
	if _, err := s.resolveSecrets(st); err == nil || secretCalls != 0 {
		t.Fatal("secret boundary bypass")
	}
}
func TestD04PolicyHostSecretsAndEpoch(t *testing.T) {
	wf := d04Decode(t, "[workflow]\nname='policy-host'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='true'\nsecrets=['synthetic']\n")
	spy := &d04Spy{}
	m := NewManager(spy, "")
	policy := d04HostPolicy(t, true)
	m.SetSandboxAuthorizer(policy)
	calls := 0
	m.SetSecretResolver(func(string) (string, error) { calls++; return "synthetic-value", nil })
	// A loaded policy is immutable; changing the file cannot revoke mid-epoch.
	if err := os.WriteFile(policy.Source().Path, []byte("version=1\nallow_host=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-run.done:
	case <-ctx.Done():
		t.Fatal("host run did not settle")
	}
	if calls != 1 || spy.executions.Load() != 1 || run.Snapshot().Failed {
		t.Fatalf("calls=%d executions=%d", calls, spy.executions.Load())
	}
	t.Log("allowed host secret behavior retained; policy file edits do not mutate active epoch")
}

func TestD04PolicyNestedHostOverride(t *testing.T) {
	dir := t.TempDir()
	root := "[workflow]\nname='nested-policy'\nversion='1'\n[[sandbox]]\nname='box'\nruntime='docker'\nimage='alpine'\n[defaults]\nsandbox='box'\n[[step]]\nid='module'\ntype='subworkflow'\nmodule='child.toml'\n"
	child := "[module]\nschema_version=1\n[module.exports.result]\nref='@work'\n[[step]]\nid='work'\ntype='command'\nrun='true'\nsandbox='host'\n"
	for name, data := range map[string]string{"root.toml": root, "child.toml": child} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := workflow.Load(filepath.Join(dir, "root.toml"))
	if err != nil {
		t.Fatal(err)
	}
	spy := &d04Spy{}
	m := NewManager(spy, "")
	m.SetSandboxAuthorizer(d04DenyHost{})
	_, err = m.Start(wf)
	var failure *sandbox.Failure
	if !errors.As(err, &failure) || failure.Field != "allow_host" || !strings.Contains(failure.StepID, "work") {
		t.Fatalf("nested host escaped: %v", err)
	}
	if spy.executions.Load() != 0 {
		t.Fatal("nested escape launched")
	}
	t.Log("loaded module's explicit host override denied before parent runtime-support check")
}
