package headless_test

import (
	"context"
	"sync"
	"testing"

	"jig/internal/engine"
	"jig/internal/headless"
	"jig/internal/runner"
	"jig/internal/workflow"
)

type stubRegistrar struct {
	mu       sync.Mutex
	prepared []preparedRun
}

type preparedRun struct {
	runID  string
	policy workflow.NotificationPolicy
}

func (s *stubRegistrar) PrepareRun(runID string, policy workflow.NotificationPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = append(s.prepared, preparedRun{runID: runID, policy: policy})
}

func TestHeadlessRegistersNotificationsBeforeSettlement(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "notif-wf"
version = "0.1"

[notification]
events = ["run_failed", "run_succeeded"]
routes = [{ destination = "ops", events = ["run_failed"] }]

[[step]]
id = "a"
type = "command"
run = "echo hi"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(nil))
	mgr := engine.NewManager(mux, "")
	reg := &stubRegistrar{}
	opts := runOpts(wf, mgr)
	opts.Notifications = reg
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v", result.ExitCode, result.Err)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.prepared) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(reg.prepared))
	}
	prep := reg.prepared[0]
	if prep.runID == "" {
		t.Fatalf("missing runID")
	}
	if len(prep.policy.Events) != 2 {
		t.Fatalf("policy events=%d", len(prep.policy.Events))
	}
}
