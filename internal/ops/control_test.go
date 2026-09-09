package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

func TestPreviewResetIsReadOnly(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-120000-reset123"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	const source = `
[workflow]
name = "reset-preview"
version = "1"

[[step]]
id = "a"
type = "command"
run = "true"

[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	writeCapturedWorkflow(t, runDir, source, wf)
	writeOpsJournal(t, runDir,
		engine.RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"a", "b"}},
		engine.StepStatus{RunID: runID, StepID: "a", To: step.StatusAwaitingRecovery},
	)
	before := directoryContents(t, runDir)
	preview, err := PreviewReset(root, runID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{preview.Steps[0].ID, preview.Steps[1].ID}; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("closure = %v", got)
	}
	after := directoryContents(t, runDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("preview mutated run directory\nbefore=%v\nafter=%v", before, after)
	}
	if _, err := os.Stat(datastore.SchedulerLockPath(runDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created scheduler lock: %v", err)
	}
}

func writeCapturedWorkflow(t *testing.T, runDir, source string, wf *workflow.Workflow) {
	t.Helper()
	sum := sha256.Sum256([]byte(source))
	snapshot := struct {
		SHA256        string            `json:"sha256"`
		TOML          string            `json:"toml"`
		Meta          workflow.Meta     `json:"meta"`
		Defaults      workflow.Defaults `json:"defaults"`
		PublicSteps   []workflow.Step   `json:"public_steps"`
		ExpandedSteps []workflow.Step   `json:"expanded_steps"`
	}{
		SHA256: hex.EncodeToString(sum[:]), TOML: source, Meta: wf.Meta, Defaults: wf.Defaults,
		PublicSteps: wf.PublicSteps(), ExpandedSteps: wf.Steps,
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datastore.WorkflowSnapshotPath(runDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func directoryContents(t *testing.T, root string) map[string]string {
	t.Helper()
	contents := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			contents[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
