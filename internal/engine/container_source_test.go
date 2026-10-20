package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type d05SourceExec struct{ identities chan [2]string }

func (e *d05SourceExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	head, err := gitCmdContext(ctx, req.ExecutionDir, "rev-parse", "HEAD")
	e.identities <- [2]string{req.SourceCommit, strings.TrimSpace(head)}
	return &step.Result{Status: step.StatusSucceeded}, err
}
func TestD05DispatchPinsSourceCommit(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	wf, err := workflow.Decode("[workflow]\nname='source'\nversion='1'\n[[step]]\nid='reader'\ntype='command'\nrun='true'\nisolation='none'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	e := &d05SourceExec{identities: make(chan [2]string, 1)}
	run, err := NewManager(e, filepath.Join(repo, ".jig")).Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Cancel(); run.Wait() })
	snapshot := run.Wait()
	if snapshot.Failed {
		t.Fatal("source dispatch failed")
	}
	select {
	case pair := <-e.identities:
		if pair[0] == "" || pair[0] != pair[1] {
			t.Fatalf("source identity not tied to execution view: %v", pair)
		}
	default:
		t.Fatal("no source dispatched")
	}
}
