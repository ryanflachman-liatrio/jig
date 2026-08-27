package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

func TestResumeHistoricalReviewContinuesLoopWithSavedComment(t *testing.T) {
	const source = `
[workflow]
name = "resume-review"
version = "1"

[[step]]
id = "scope"
type = "agent"
skill = "scope"
  [step.schema]
  sizing = { enum = ["too_large", "just_right"] }

[[step]]
id = "gate"
type = "review"
depends_on = ["scope"]
when = "scope.sizing != 'just_right'"
output_type = { enum = ["proceed", "narrow"] }
  [[step.review]]
  source = "@scope.summary"
  label = "Scope"
  [step.loop]
  when = "gate == 'narrow'"
  goto = "scope"
  max_iterations = 2
  feedback = "@gate"

[[step]]
id = "after"
type = "command"
run = "true"
depends_on = ["scope", "gate"]
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := t.TempDir()
	initRepo(t, repoRoot)
	root := filepath.Join(repoRoot, ".jig")
	runID := "historical-review"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	branch := runBranchName(wf.Meta.Name, runID)
	if out, err := gitCmd(repoRoot, "branch", branch, "HEAD"); err != nil {
		t.Fatalf("create historical run branch: %v — %s", err, out)
	}
	staleWorktree := filepath.Join(root, "worktrees", runID, "_run")
	if out, err := gitCmd(repoRoot, "worktree", "add", staleWorktree, branch); err != nil {
		t.Fatalf("create stale historical worktree: %v — %s", err, out)
	}
	if _, err := datastore.StepDir(runDir, "scope"); err != nil {
		t.Fatal(err)
	}
	initial := `{"summary":"The proposal is too broad.","sizing":"too_large"}`
	if err := os.WriteFile(datastore.OutputJSONPath(runDir, "scope"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datastore.OutputPath(runDir, "scope"), []byte("The proposal is too broad."), 0o644); err != nil {
		t.Fatal(err)
	}
	docContent := "The proposal is too broad."
	doc := review.Document{
		ID: "01-scope", Label: "Scope", Source: "@scope.summary", Format: "markdown",
		SHA256: review.Digest(docContent), LineCount: 1,
	}
	docDir := datastore.ReviewDocumentsDir(runDir, "gate", "g000-i000")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc.SnapshotPath = filepath.Join(docDir, doc.ID+".md")
	if err := os.WriteFile(doc.SnapshotPath, []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"scope", "gate", "after"}},
		StepStatus{RunID: runID, StepID: "scope", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: runID, StepID: "scope", From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: runID, StepID: "gate", From: step.StatusPending, To: step.StatusAwaitingReview},
		ReviewRequest{RunID: runID, StepID: "gate", RoundID: "g000-i000", Choices: []string{"proceed", "narrow"}, Documents: []review.Document{doc}, DraftPath: datastore.ReviewDraftPath(runDir, "gate", "g000-i000")},
	})

	exec := &feedbackCapturingStructuredExec{structuredExec: structuredExec{
		testExec: testExec{outcomes: map[string]testOutcome{}},
		stepID:   "scope", responses: []string{`{"summary":"Now focused.","sizing":"just_right"}`},
	}}
	mgr := NewManager(exec, root)
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Resume(runID, wf)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	comment := "Limit the first spec to local execution."
	run.ResolveReview("gate", review.Submission{
		StepID: "gate", RoundID: "g000-i000", Verdict: "narrow",
		Documents: []review.DocumentRecord{doc.Record()}, Reviewed: []string{doc.ID},
		Comments: []review.Comment{{
			ID: "C001", Kind: review.KindNote,
			Anchor: review.Anchor{DocumentID: doc.ID, SHA256: doc.SHA256, StartLine: 1, EndLine: 1, Quote: docContent},
			Body:   comment,
		}},
	})
	events := collectEvents(t, ctrl, 5*time.Second)
	if got := findStatus(events, "scope"); len(got) < 2 || got[0] != step.StatusPending || got[1] != step.StatusRunning {
		t.Fatalf("resumed scope statuses = %v, want pending then running", got)
	}
	exec.mu.Lock()
	feedback := append([]string(nil), exec.feedback...)
	exec.mu.Unlock()
	if len(feedback) != 1 || !strings.Contains(feedback[0], comment) {
		t.Fatalf("resumed feedback = %q, want saved review comment", feedback)
	}
	snap := run.Snapshot()
	if !snap.Done || snap.Failed {
		t.Fatalf("resumed snapshot = %+v, want successful completion", snap)
	}
}

func TestResumeRejectsInterruptedWorker(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "worker"
version = "1"
[[step]]
id = "agent"
type = "agent"
skill = "agent"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	runDir, err := datastore.RunDir(root, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "interrupted", Workflow: "worker", Steps: []string{"agent"}},
		StepStatus{RunID: "interrupted", StepID: "agent", From: step.StatusPending, To: step.StatusRunning},
	})
	mgr := NewManager(&testExec{}, root)
	if _, err := mgr.Resume("interrupted", wf); err == nil || !strings.Contains(err.Error(), "only quiescent review gates") {
		t.Fatalf("Resume error = %v", err)
	}
}

func TestStartPersistsWorkflowSnapshotForFutureResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.toml")
	if err := os.WriteFile(path, []byte(`
[workflow]
name = "snapshotted"
version = "1"
[[step]]
id = "done"
type = "command"
run = "true"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".jig")
	mgr := NewManager(&testExec{}, root)
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ctrl, 5*time.Second)
	loaded, err := loadWorkflowSnapshot(mgr.RunDir(run.ID))
	if err != nil {
		t.Fatalf("loadWorkflowSnapshot: %v", err)
	}
	if loaded.Meta.Name != wf.Meta.Name || len(loaded.Steps) != 1 {
		t.Fatalf("workflow snapshot = %+v", loaded)
	}
}
