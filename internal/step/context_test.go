package step

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenPath is the committed render golden — the byte-for-byte assertion target
// shared with the spec's "Rendered format" block. Keeping the test pointed at
// the committed proof artifact means the golden and the proof cannot drift.
const goldenPath = "../../docs/specs/03-spec-step-context-assembly/03-proofs/1.0-render-golden.txt"

// fullyPopulated is the struct described by task 1.0's proof: two upstream (one
// succeeded with a purpose, one failed), two downstream (one propagated-purpose
// review, one guarded graph-derived agent), own purpose + notes, and a revise
// re-run at Iteration == 1 under max_iterations = 3.
func fullyPopulated() StepContext {
	return StepContext{
		WorkflowName:  "feature",
		StepID:        "plan",
		Purpose:       "turn research findings into an ordered implementation plan.",
		Notes:         "prefer the smallest change that satisfies the acceptance criteria.",
		Iteration:     1,
		MaxIterations: 3,
		RerunReason:   "re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.",
		Upstream: []ContextNeighbor{
			{ID: "intake", Kind: "agent", Status: "succeeded", Purpose: "classify the request and extract research areas"},
			{ID: "research_backend", Kind: "agent", Status: "failed"},
		},
		Downstream: []ContextNeighbor{
			{ID: "plan_review", Kind: "human", Purpose: "a person reviews your summary and approves or requests revisions"},
			{ID: "implement", Kind: "agent", Conditional: "approved == true", Fields: []string{"tasks"}},
		},
	}
}

// TestStepContextRenderGolden locks the exact byte layout against the committed
// golden. A mismatch here means either the renderer or the golden changed.
func TestStepContextRenderGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	got := fullyPopulated().Render()
	if got != string(want) {
		t.Errorf("Render() does not match golden %s\n--- got ---\n%q\n--- want ---\n%q", goldenPath, got, string(want))
	}
	// The golden ends at the delimiter with no trailing newline; guard it so a
	// stray newline in either the file or the renderer is caught explicitly.
	if strings.HasSuffix(got, "\n") {
		t.Error("Render() output must not end with a trailing newline")
	}
}

// TestStepContextRenderStable guards the determinism guarantee: the same input
// renders identically every time (no map iteration leaks into the output).
func TestStepContextRenderStable(t *testing.T) {
	first := fullyPopulated().Render()
	for i := 0; i < 100; i++ {
		if got := fullyPopulated().Render(); got != first {
			t.Fatalf("Render() not stable at iteration %d:\n--- first ---\n%q\n--- got ---\n%q", i, first, got)
		}
	}
}

// TestStepContextRenderOmits covers the graceful-omission rules: the zero value,
// a topology-only struct, and a pipeline-entry struct.
func TestStepContextRenderOmits(t *testing.T) {
	t.Run("zero value renders empty", func(t *testing.T) {
		if got := (StepContext{}).Render(); got != "" {
			t.Errorf("zero value Render() = %q, want empty", got)
		}
	})

	t.Run("topology-only omits optional lines", func(t *testing.T) {
		// Upstream + downstream but no purpose/notes, no rerun, first run.
		c := StepContext{
			WorkflowName: "feature",
			StepID:       "plan",
			Upstream:     []ContextNeighbor{{ID: "intake", Kind: "agent", Status: "succeeded"}},
			Downstream:   []ContextNeighbor{{ID: "review", Kind: "human", Fields: []string{"summary"}}},
		}
		got := c.Render()
		for _, absent := range []string{"Purpose:", "Notes:", "State:", "iteration"} {
			if strings.Contains(got, absent) {
				t.Errorf("topology-only Render() should omit %q, got:\n%s", absent, got)
			}
		}
		// The blocks that *are* present must still render.
		for _, present := range []string{"Upstream (already complete):", "Downstream (what your output feeds):"} {
			if !strings.Contains(got, present) {
				t.Errorf("topology-only Render() should contain %q, got:\n%s", present, got)
			}
		}
	})

	t.Run("pipeline-entry renders position line and delimiter only", func(t *testing.T) {
		got := StepContext{WorkflowName: "feature", StepID: "intake"}.Render()
		want := "## Workflow context\n\nYou are step `intake` in workflow `feature`.\n\n---"
		if got != want {
			t.Errorf("pipeline-entry Render() =\n%q\nwant\n%q", got, want)
		}
	})
}
