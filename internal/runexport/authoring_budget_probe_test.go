package runexport

import (
	"bytes"
	"context"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"os"
	"path/filepath"
	"testing"
)

// Exercise the selected-source budget with a production capture snapshot.
// Export must not follow its authoring reference.
func TestAuthoringAssetSnapshotExportBudgetProbe(t *testing.T) {
	wf, err := workflow.Decode("[workflow]\nname='budget'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	manager := engine.NewManager(authoringBudgetExecutor{}, filepath.Join(t.TempDir(), ".jig"))
	run, err := manager.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	if run.Wait().Failed {
		t.Fatal("fixture failed")
	}
	dir := manager.RunDir(run.ID)
	// Pure export decoding must not follow a valid reference to unavailable data.
	if err := os.RemoveAll(filepath.Join(dir, "authoring")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "workflow.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const snapshotLimit = 32 << 20
	data = append(data, bytes.Repeat([]byte(" "), snapshotLimit-len(data))...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	inv, err := collectInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, n := range []int64{maxTotalInput, snapshotLimit, snapshotLimit - 1} {
		budget := newBudget(n)
		wf, _, gap := readWorkflowSnapshot(root, inv, budget)
		if n >= snapshotLimit {
			if wf == nil || gap != nil || budget.remaining != n-snapshotLimit {
				t.Fatalf("budget=%d remainder=%d gap=%+v", n, budget.remaining, gap)
			}
		} else if wf != nil || gap == nil {
			t.Fatal("exhausted selected-source budget silently succeeded")
		}
	}
	t.Logf("32 MiB snapshot fits 256 MiB aggregate reader; one-byte-short remaining budget reports gap; authoring reference not read")
}

type authoringBudgetExecutor struct{}

func (authoringBudgetExecutor) Execute(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error) {
	return &step.Result{Status: step.StatusSucceeded}, nil
}
