package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
[[step.route]]
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

func TestResumeParksInterruptedWorkerOnRecovery(t *testing.T) {
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
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	if err := datastore.WriteSession(runDir, "agent", datastore.SessionInfo{
		SessionID: "sess-crash", Backend: "claude", Transport: "sdk",
	}); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "interrupted", Workflow: "worker", Steps: []string{"agent"}},
		StepStatus{RunID: "interrupted", StepID: "agent", From: step.StatusPending, To: step.StatusRunning, Attempt: 1},
	})
	exec := &testExec{}
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Resume("interrupted", wf)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	recovery := waitRecoveryRequest(t, ch, 2*time.Second)
	if recovery.StepID != "agent" {
		t.Fatalf("RecoveryRequest.StepID = %q", recovery.StepID)
	}
	if recovery.Err != processInterruptedErr {
		t.Fatalf("Err = %q, want %q", recovery.Err, processInterruptedErr)
	}
	// testExec does not implement SessionResumeSupport → SessionID alone allows resume.
	if !recovery.CanResume {
		t.Fatal("CanResume should be true with durable session.json")
	}
	snap := run.Snapshot()
	agentState := snapshotStep(snap, "agent")
	if agentState.Status != step.StatusAwaitingRecovery {
		t.Fatalf("status = %s, want awaiting_recovery", agentState.Status)
	}
	if agentState.Attempt != 1 {
		t.Fatalf("Attempt = %d, want 1 (not bumped until Recover)", agentState.Attempt)
	}

	run.Recover("agent", RecoverRetry, "")
	events := collectEvents(t, ch, 5*time.Second)
	if got := findStatus(events, "agent"); len(got) < 1 || got[len(got)-1] != step.StatusSucceeded {
		t.Fatalf("after retry statuses = %v, want succeeded", got)
	}
	info, err := datastore.ReadSession(runDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != "" {
		t.Fatalf("Recover(retry) left session.json %q", info.SessionID)
	}
	final := run.Snapshot()
	if !final.Done || final.Failed {
		t.Fatalf("snapshot = %+v, want successful completion", final)
	}
}

func TestResumeInterruptedValidatingParksRecovery(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "validate-crash"
version = "1"
[[step]]
id = "cmd"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	runDir, err := datastore.RunDir(root, "validating")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "validating", Workflow: "validate-crash", Steps: []string{"cmd"}},
		StepStatus{RunID: "validating", StepID: "cmd", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "validating", StepID: "cmd", From: step.StatusRunning, To: step.StatusValidating},
	})
	mgr := NewManager(&testExec{}, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Resume("validating", wf)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	rr := waitRecoveryRequest(t, ch, 2*time.Second)
	if rr.StepID != "cmd" {
		t.Fatalf("StepID = %q", rr.StepID)
	}
	if rr.CanResume {
		t.Fatal("command interrupt must not CanResume")
	}
	run.Recover("cmd", RecoverRetry, "")
	collectEvents(t, ch, 5*time.Second)
	if snap := run.Snapshot(); !snap.Done || snap.Failed {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestResumeMixedReviewAndInterruptedWorker(t *testing.T) {
	const source = `
[workflow]
name = "mixed"
version = "1"
[[step]]
id = "writer"
type = "agent"
skill = "writer"
[[step]]
id = "gate"
type = "review"
depends_on = ["writer"]
output_type = { enum = ["proceed"] }
  [[step.review]]
  source = "@writer.summary"
  label = "Doc"
[[step]]
id = "worker"
type = "command"
run = "true"
depends_on = ["gate"]
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := t.TempDir()
	initRepo(t, repoRoot)
	root := filepath.Join(repoRoot, ".jig")
	runID := "mixed-crash"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	branch := runBranchName(wf.Meta.Name, runID)
	if out, err := gitCmd(repoRoot, "branch", branch, "HEAD"); err != nil {
		t.Fatalf("branch: %v — %s", err, out)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	if _, err := datastore.StepDir(runDir, "writer"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datastore.OutputJSONPath(runDir, "writer"), []byte(`{"summary":"hi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datastore.OutputPath(runDir, "writer"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	docContent := "hi"
	doc := review.Document{
		ID: "01-doc", Label: "Doc", Source: "@writer.summary", Format: "markdown",
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
		RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"writer", "gate", "worker"}},
		StepStatus{RunID: runID, StepID: "writer", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: runID, StepID: "writer", From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: runID, StepID: "gate", From: step.StatusPending, To: step.StatusAwaitingReview},
		ReviewRequest{RunID: runID, StepID: "gate", RoundID: "g000-i000", Choices: []string{"proceed"}, Documents: []review.Document{doc}, DraftPath: datastore.ReviewDraftPath(runDir, "gate", "g000-i000")},
		StepStatus{RunID: runID, StepID: "worker", From: step.StatusPending, To: step.StatusRunning},
	})
	mgr := NewManager(&testExec{}, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Resume(runID, wf)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	rr := waitRecoveryRequest(t, ch, 2*time.Second)
	if rr.StepID != "worker" {
		t.Fatalf("RecoveryRequest.StepID = %q", rr.StepID)
	}
	snap := run.Snapshot()
	if snapshotStep(snap, "gate").Status != step.StatusAwaitingReview {
		t.Fatalf("gate = %s, want awaiting_review", snapshotStep(snap, "gate").Status)
	}
	if snapshotStep(snap, "worker").Status != step.StatusAwaitingRecovery {
		t.Fatalf("worker = %s, want awaiting_recovery", snapshotStep(snap, "worker").Status)
	}
	run.Recover("worker", RecoverRetry, "")
	run.ResolveReview("gate", review.Submission{
		StepID: "gate", RoundID: "g000-i000", Verdict: "proceed",
		Documents: []review.DocumentRecord{doc.Record()}, Reviewed: []string{doc.ID},
	})
	collectEvents(t, ch, 5*time.Second)
	if final := run.Snapshot(); !final.Done || final.Failed {
		t.Fatalf("final = %+v", final)
	}
}

func TestResumeRejectsSpec21Parks(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "parked"
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
	runDir, err := datastore.RunDir(root, "needs-input")
	if err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "needs-input", Workflow: "parked", Steps: []string{"agent"}},
		StepStatus{RunID: "needs-input", StepID: "agent", From: step.StatusPending, To: step.StatusNeedsInput},
	})
	mgr := NewManager(&testExec{}, root)
	if _, err := mgr.Resume("needs-input", wf); err == nil || !strings.Contains(err.Error(), "Spec 21") {
		t.Fatalf("Resume error = %v, want Spec 21 reject", err)
	}
}

func TestResumeRejectsFinishedRun(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "done"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	runDir, err := datastore.RunDir(root, "finished")
	if err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "finished", Workflow: "done", Steps: []string{"a"}},
		StepStatus{RunID: "finished", StepID: "a", From: step.StatusPending, To: step.StatusSucceeded},
		RunFinished{RunID: "finished", Failed: false},
	})
	mgr := NewManager(&testExec{}, root)
	if _, err := mgr.Resume("finished", wf); err == nil || !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("Resume error = %v", err)
	}
}

func TestResumeRecoverResumeUsesDurableSession(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "resume-sess"
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
	runDir, err := datastore.RunDir(root, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	if err := datastore.WriteSession(runDir, "agent", datastore.SessionInfo{SessionID: "durable-1"}); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "sess", Workflow: "resume-sess", Steps: []string{"agent"}},
		StepStatus{RunID: "sess", StepID: "agent", From: step.StatusPending, To: step.StatusRunning},
	})
	exec := &crashRecordingExec{result: &step.Result{Status: step.StatusSucceeded, SessionID: "durable-1"}}
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Resume("sess", wf)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitRecoveryRequest(t, ch, 2*time.Second)
	run.Recover("agent", RecoverResume, "keep going")
	collectEvents(t, ch, 5*time.Second)
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.reqs) != 1 {
		t.Fatalf("dispatch count = %d", len(exec.reqs))
	}
	if exec.reqs[0].ResumeSessionID != "durable-1" {
		t.Fatalf("ResumeSessionID = %q", exec.reqs[0].ResumeSessionID)
	}
	if !strings.Contains(exec.reqs[0].Message, "process exited while step was running") {
		t.Fatalf("recovery message = %q", exec.reqs[0].Message)
	}
}

func waitRecoveryRequest(t *testing.T, ch <-chan Event, timeout time.Duration) RecoveryRequest {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			switch ev := e.(type) {
			case RecoveryRequest:
				return ev
			case RunFinished:
				t.Fatal("run finished before RecoveryRequest")
			}
		case <-deadline:
			t.Fatal("timeout waiting for RecoveryRequest")
		}
	}
}

func snapshotStep(snap RunSnapshot, id string) step.State {
	for _, st := range snap.Steps {
		if st.ID == id {
			return st
		}
	}
	return step.State{}
}

type crashRecordingExec struct {
	mu     sync.Mutex
	reqs   []StepRequest
	result *step.Result
}

func (e *crashRecordingExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	res := e.result
	if res == nil {
		res = &step.Result{Status: step.StatusSucceeded}
	}
	copy := *res
	return &copy, nil
}

type capAwareExec struct {
	canResume bool
	failOnce  bool
	mu        sync.Mutex
	calls     int
}

func (e *capAwareExec) SupportsSessionResume(_, _ string) bool { return e.canResume }

func (e *capAwareExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	n := e.calls
	e.calls++
	e.mu.Unlock()
	if e.failOnce && n == 0 {
		return &step.Result{Status: step.StatusFailed, Err: "boom", SessionID: "sess"}, nil
	}
	return &step.Result{Status: step.StatusSucceeded, SessionID: "sess"}, nil
}

func TestEnterRecoveryRespectsCapSessionResume(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "cap"
version = "1"
[[step]]
id = "agent"
type = "agent"
skill = "agent"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &capAwareExec{canResume: false, failOnce: true}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	var rr *RecoveryRequest
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case e := <-ch:
			switch ev := e.(type) {
			case RecoveryRequest:
				got := ev
				rr = &got
				break loop
			case RunFinished:
				t.Fatal("run finished before RecoveryRequest")
			}
		case <-deadline:
			t.Fatal("timeout waiting for RecoveryRequest")
		}
	}
	if rr.CanResume {
		t.Fatal("CanResume must be false without CapSessionResume")
	}
	run.Recover("agent", RecoverAbort, "")
	collectEvents(t, ch, 2*time.Second)
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

func TestWorkflowSnapshotLocksExpandedModuleSources(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "module.toml")
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(modulePath, `
[module]
schema_version = 1
[module.exports.result]
ref = "@original"
[[step]]
id = "original"
type = "command"
run = "true"
`)
	path := filepath.Join(dir, "workflow.toml")
	mustWrite(path, `
[workflow]
name = "locked-modules"
version = "1"
[[step]]
id = "part"
type = "subworkflow"
module = "module.toml"
`)
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

	// A resume must use the source captured above, not this changed checkout file.
	mustWrite(modulePath, `
[module]
schema_version = 1
[module.exports.result]
ref = "@changed"
[[step]]
id = "changed"
type = "command"
run = "true"
`)
	loaded, err := loadWorkflowSnapshot(mgr.RunDir(run.ID))
	if err != nil {
		t.Fatalf("loadWorkflowSnapshot: %v", err)
	}
	if len(loaded.ModuleSources()) != 1 || loaded.ModuleSources()[0].Path != modulePath {
		t.Fatalf("locked sources = %+v", loaded.ModuleSources())
	}
	for _, st := range loaded.Steps {
		if st.ID == "part__changed" {
			t.Fatalf("snapshot expanded changed module: %+v", loaded.Steps)
		}
		if st.ID == "part__original" {
			return
		}
	}
	t.Fatalf("snapshot did not retain original expansion: %+v", loaded.Steps)
}
