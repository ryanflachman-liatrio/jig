package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// agentConflictWorkflow is conflictWorkflow with agent steps, which the agent
// conflict resolver requires.
const agentConflictWorkflow = `
[workflow]
name = "conflict"
version = "0.1"

[[step]]
id = "a"
type = "agent"
skill = "write-a"
isolation = "worktree"

[[step]]
id = "b"
type = "agent"
skill = "write-b"
isolation = "worktree"
`

// blockingResolver parks inside ResolveIntegration until released or the
// context is cancelled, then writes a resolution into the conflicted paths.
type blockingResolver struct {
	started   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
}

func newBlockingResolver() *blockingResolver {
	return &blockingResolver{
		started:   make(chan struct{}, 1),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}, 1),
	}
}

func (r *blockingResolver) ResolveIntegration(ctx context.Context, req IntegrationResolutionRequest, _ Reporter) (*step.Result, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
		r.cancelled <- struct{}{}
		return nil, ctx.Err()
	}
	for _, p := range req.Conflicts {
		if err := os.WriteFile(filepath.Join(req.Worktree, p), []byte("resolved\n"), 0o644); err != nil {
			return nil, err
		}
	}
	if out, err := gitCmd(req.Worktree, "add", "-A"); err != nil {
		return &step.Result{Status: step.StatusFailed, Err: out}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func startAgentConflictRun(t *testing.T, resolver IntegrationResolver) (*Run, <-chan Event) {
	t.Helper()
	repo := t.TempDir()
	initRepo(t, repo)
	wf, err := workflow.Decode(agentConflictWorkflow, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &composeExec{writes: map[string]map[string]string{
		"a": {"shared": "a\n"},
		"b": {"shared": "b\n"},
	}}
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	mgr.SetIntegrationResolver(resolver)
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	return run, ch
}

func waitStarted(t *testing.T, r *blockingResolver) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(15 * time.Second):
		t.Fatal("resolver never started")
	}
}

// TestAgentIntegrationResolverDoesNotBlockScheduler proves the agent conflict
// resolver runs off the scheduler goroutine: snapshots are served and a
// competing manual resolve is rejected while the agent works, and the agent's
// outcome re-presents the gate once it finishes.
func TestAgentIntegrationResolverDoesNotBlockScheduler(t *testing.T) {
	resolver := newBlockingResolver()
	run, ch := startAgentConflictRun(t, resolver)

	deadline := time.After(30 * time.Second)
	var conflictStep string
	sawBusy, sawProposal := false, false
loop:
	for {
		select {
		case e := <-ch:
			switch e := e.(type) {
			case IntegrationConflictRequest:
				if conflictStep == "" {
					conflictStep = e.StepID
					run.ResolveIntegrationWithAgent(e.StepID)
					waitStarted(t, resolver)

					snapped := make(chan struct{})
					go func() { run.Snapshot(); close(snapped) }()
					select {
					case <-snapped:
					case <-time.After(5 * time.Second):
						t.Fatal("scheduler blocked while the agent resolver ran")
					}
					run.ResolveIntegration(e.StepID, true) // must be rejected, not abort
					run.ResolveIntegrationWithAgent(e.StepID)
					continue
				}
				if strings.Contains(e.Resolution, "agent proposal staged") {
					sawProposal = true
					run.ResolveIntegration(e.StepID, false)
				}
			case RunError:
				if strings.Contains(e.Err, "in progress") && !sawBusy {
					sawBusy = true
					close(resolver.release)
				}
			case FinalMergeRequest:
				run.FinalMerge(false)
			case RunFinished:
				if e.Failed {
					t.Errorf("run should succeed after agent resolution; got Failed=true")
				}
				break loop
			}
		case <-deadline:
			t.Fatal("timeout waiting for RunFinished")
		}
	}
	if !sawBusy {
		t.Error("a resolve during agent resolution should be rejected with a RunError")
	}
	if !sawProposal {
		t.Error("agent outcome should re-present the integration gate")
	}
}

// TestAgentIntegrationResolverObservesCancel proves the resolver receives the
// run context: cancelling the run cancels an in-flight resolution.
func TestAgentIntegrationResolverObservesCancel(t *testing.T) {
	resolver := newBlockingResolver()
	run, ch := startAgentConflictRun(t, resolver)

	deadline := time.After(30 * time.Second)
	for {
		select {
		case e := <-ch:
			if icr, ok := e.(IntegrationConflictRequest); ok {
				run.ResolveIntegrationWithAgent(icr.StepID)
				waitStarted(t, resolver)
				run.Cancel()
				select {
				case <-resolver.cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("resolver context was not cancelled with the run")
				}
			}
			if _, ok := e.(RunFinished); ok {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for RunFinished")
		}
	}
}
