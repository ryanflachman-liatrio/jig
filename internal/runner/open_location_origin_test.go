package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestAgentOpenLocationOrigin(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	actualCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	explicit, repo := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name, execution, repo, want string
		persist                     bool
	}{
		{"execution wins", explicit, repo, explicit, true},
		{"repo fallback", "", repo, repo, true},
		{"cwd fallback", "", "", actualCwd, true},
		{"relative execution", "relative", repo, filepath.Join(actualCwd, "relative"), true},
		{"persistence off", explicit, repo, explicit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []harness.Event{{Type: harness.EventText, Text: "synthetic"}, {Type: harness.EventAssistantEnd}, {Type: harness.EventResult}}
			h := &harness.FakeHarness{Caps: harness.NewCapabilitySet(harness.CapPermissionCallback), Sess: harness.NewFakeSession(events)}
			path := ""
			if tc.persist {
				path = filepath.Join(t.TempDir(), "transcript.jsonl")
			}
			rep := &captureReporter{}
			result, err := NewAgentExecutorFixed(h).Execute(t.Context(), engine.StepRequest{Step: &workflow.Step{ID: "edit"}, ExecutionDir: tc.execution, RepoRoot: tc.repo, TranscriptPath: path, Generation: 2, Iteration: 3, Attempt: 4}, rep)
			if err != nil || result.Status != step.StatusSucceeded {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if h.OpenSpec.Cwd != tc.want || !filepath.IsAbs(h.OpenSpec.Cwd) {
				t.Fatalf("cwd=%q want %q", h.OpenSpec.Cwd, tc.want)
			}
			if !tc.persist {
				if len(rep.messages) != 0 {
					t.Fatal("persistence-off emitted durable messages")
				}
				entries, err := os.ReadDir(cwd)
				if err != nil || len(entries) != 0 {
					t.Fatalf("unexpected persistence-off artifacts: %v, %v", entries, err)
				}
				return
			}
			reader, err := transcript.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := reader.Tail(10)
			if err != nil || len(entries) == 0 {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			for _, entry := range entries {
				if entry.ExecutionDir != h.OpenSpec.Cwd || entry.Generation != 2 || entry.Iteration != 3 || entry.Attempt != 4 {
					t.Fatalf("origin mismatch: %+v", entry)
				}
			}
		})
	}
}
