package runexport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// P3 probes the existing selected-source reader's budget without adding an
// authoring source or changing production snapshot/export semantics.
func TestAuthoringAssetSnapshotExportBudgetProbe(t *testing.T) {
	_, dir := makeRunStore(t)
	writeWorkflowSnapshot(t, dir)
	path := filepath.Join(dir, "workflow.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// An unavailable reference must never become an additional selected source.
	raw["authoring"] = json.RawMessage(`{"version":1,"path":"/unavailable/private/manifest.json"}`)
	data, err = json.Marshal(raw)
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
