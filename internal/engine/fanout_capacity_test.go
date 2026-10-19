package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// TestForEach_PreExecutionFailureReleasesFamilySlot proves a fan-out child
// that fails before its worker starts (here: a named secret with no resolver)
// gives back its family slot. With max_parallel = 1 a leaked slot would keep
// every later sibling from ever dispatching.
func TestForEach_PreExecutionFailureReleasesFamilySlot(t *testing.T) {
	toml := strings.Replace(foreachTOML(4, "  max_parallel = 1"),
		`skill = "skills/analyze"`, `skill = "skills/analyze"
secrets = ["API_TOKEN"]`, 1)
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	producer, _ := json.Marshal(map[string]any{"targets": []any{target("a", "a"), target("b", "b")}})
	exec := &fanOutExec{producer: map[string]json.RawMessage{"discover": producer}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	parked := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(parked) < 2 {
		select {
		case e := <-ch:
			if rr, ok := e.(RecoveryRequest); ok && strings.Contains(rr.StepID, workflow.ForEachIDMarker) {
				if !strings.Contains(rr.Err, "secret") {
					t.Fatalf("child %s failed for %q, want the missing-secret failure", rr.StepID, rr.Err)
				}
				parked[rr.StepID] = true
			}
		case <-deadline:
			t.Fatalf("only %d of 2 children reached recovery; a failed child kept its family slot", len(parked))
		}
	}
	run.Cancel()
}
