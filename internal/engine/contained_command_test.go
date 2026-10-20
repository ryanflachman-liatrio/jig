package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD05BuildFixtureFailsClosedBeforeQualification(t *testing.T) {
	wf, err := workflow.Load(filepath.Join("..", "..", "examples", "docker-build-jig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	spy := &d04Spy{}
	root := filepath.Join(t.TempDir(), "must-not-exist")
	m := NewManager(spy, root)
	run, err := m.Start(wf)
	var unsupported *SandboxRuntimeError
	if run != nil || !errors.As(err, &unsupported) {
		t.Fatalf("expected unsupported before effects, got run=%v err=%v", run, err)
	}
	if spy.executions.Load() != 0 || spy.validations.Load() != 0 {
		t.Fatal("unqualified workload dispatched")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unqualified workload created storage")
	}
}

func TestD05SandboxRequirementCopy(t *testing.T) {
	st := &workflow.Step{WorkspaceRequirements: []string{"lfs"}}
	copy := cloneSandboxStep(st)
	copy.WorkspaceRequirements[0] = "source_history"
	if st.WorkspaceRequirements[0] != "lfs" {
		t.Fatal("policy copy aliases caller requirements")
	}
}
