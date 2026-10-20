package engine

import (
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// TestStepKindsCoverExecutableTypes requires a stepKinds entry for every step
// type the engine can receive. Subworkflow steps are expanded at load time.
func TestStepKindsCoverExecutableTypes(t *testing.T) {
	for _, typ := range []workflow.StepType{workflow.StepAgent, workflow.StepCommand, workflow.StepCheck, workflow.StepReview} {
		kind, ok := stepKinds[typ]
		if !ok || kind.dispatch == nil {
			t.Errorf("step type %q has no stepKinds entry with a dispatch", typ)
		}
	}
	if _, ok := stepKinds[workflow.StepSubworkflow]; ok {
		t.Error("subworkflow steps are expanded at load and must not be dispatchable")
	}
}
