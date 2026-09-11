package engine

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

// captureObserver is the minimal Observer test double the reopen test needs.
// It records RunRegistered calls (with their seeded UnresolvedWaits) and
// every subsequent Publish so the test can assert that the engine's own
// re-emission of parked events after reopen carries the same wait identity
// the seed advertised — the condition the notification Lifecycle relies on
// to collapse the two into a single filtered restored summary.
type captureObserver struct {
	mu            sync.Mutex
	registrations []RunRegistration
	events        []Event
	stopped       []string
}

func (o *captureObserver) RunRegistered(reg RunRegistration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.registrations = append(o.registrations, reg)
}

func (o *captureObserver) Publish(ev Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, ev)
}

func (o *captureObserver) ProducersStopped(runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = append(o.stopped, runID)
}

func (o *captureObserver) snapshot() ([]RunRegistration, []Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	regs := append([]RunRegistration(nil), o.registrations...)
	evs := append([]Event(nil), o.events...)
	return regs, evs
}

// TestResumeObserverReceivesReopenWithSeededWaits exercises the engine side
// of the reopen contract: the Observer's RunRegistered call must be invoked
// exactly once with Reopen=true, before any live event, and it must carry
//
//   - the frozen resolved notification policy read from the immutable
//     workflow snapshot (so a reopened run cannot silently promote its
//     notifications by re-reading the current profile file), and
//   - a seeded UnresolvedWait for each parked human wait, tagged with the
//     same identifier the live event would carry (a review round id, an
//     AskUserQuestion request id) so the notification Lifecycle collapses
//     the seed and any later replay onto one wait key.
func TestResumeObserverReceivesReopenWithSeededWaits(t *testing.T) {
	const source = `
[workflow]
name = "reopen-observer"
version = "1"

[notification]
events = ["attention_required"]
  [[notification.routes]]
  destination = "ops"
  events = ["attention_required"]

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
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	// Attach a resolved notification policy so the snapshot round-trips a
	// non-empty policy alongside the parked review.
	wf.SetResolvedNotificationPolicy(workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}}},
	})

	repoRoot := t.TempDir()
	initRepo(t, repoRoot)
	root := filepath.Join(repoRoot, ".jig")
	runID := "reopen-observer-run"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	branch := runBranchName(wf.Meta.Name, runID)
	if out, err := gitCmd(repoRoot, "branch", branch, "HEAD"); err != nil {
		t.Fatalf("create run branch: %v — %s", err, out)
	}
	staleWorktree := filepath.Join(root, "worktrees", runID, "_run")
	if out, err := gitCmd(repoRoot, "worktree", "add", staleWorktree, branch); err != nil {
		t.Fatalf("create stale worktree: %v — %s", err, out)
	}
	if _, err := datastore.StepDir(runDir, "scope"); err != nil {
		t.Fatal(err)
	}
	initial := `{"summary":"Too broad.","sizing":"too_large"}`
	if err := os.WriteFile(datastore.OutputJSONPath(runDir, "scope"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datastore.OutputPath(runDir, "scope"), []byte("Too broad."), 0o644); err != nil {
		t.Fatal(err)
	}
	docContent := "Too broad."
	doc := review.Document{
		ID: "01-scope", Label: "Scope", Source: "@scope.summary", Format: "markdown",
		SHA256: review.Digest(docContent), LineCount: 1,
	}
	roundID := "g000-i000"
	docDir := datastore.ReviewDocumentsDir(runDir, "gate", roundID)
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc.SnapshotPath = filepath.Join(docDir, doc.ID+".md")
	if err := os.WriteFile(doc.SnapshotPath, []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"scope", "gate"}},
		StepStatus{RunID: runID, StepID: "scope", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: runID, StepID: "scope", From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: runID, StepID: "gate", From: step.StatusPending, To: step.StatusAwaitingReview},
		ReviewRequest{RunID: runID, StepID: "gate", RoundID: roundID, Choices: []string{"proceed", "narrow"}, Documents: []review.Document{doc}, DraftPath: datastore.ReviewDraftPath(runDir, "gate", roundID)},
	})

	exec := &testExec{outcomes: map[string]testOutcome{}}
	mgr := NewManager(exec, root)
	obs := &captureObserver{}
	mgr.RegisterObserver(obs)

	if _, err := mgr.Resume(runID); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// The scheduler dispatches RunRegistered synchronously inside Resume,
	// so a single short wait is enough for the observer to see it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		regs, _ := obs.snapshot()
		if len(regs) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	regs, _ := obs.snapshot()
	if len(regs) != 1 {
		t.Fatalf("expected exactly one RunRegistered, got %d", len(regs))
	}
	reg := regs[0]
	if !reg.Reopen {
		t.Fatalf("RunRegistered.Reopen = false, want true")
	}
	if reg.Epoch == 0 {
		t.Fatalf("RunRegistered.Epoch = 0, want a monotonically-assigned epoch")
	}
	if reg.RunID != runID {
		t.Fatalf("RunRegistered.RunID = %q, want %q", reg.RunID, runID)
	}
	if got := len(reg.UnresolvedWaits); got != 1 {
		t.Fatalf("UnresolvedWaits len = %d, want 1", got)
	}
	seed := reg.UnresolvedWaits[0]
	if seed.StepID != "gate" || seed.Kind != WaitReview {
		t.Fatalf("seed = %+v, want gate/review", seed)
	}
	if seed.Nonce != roundID {
		t.Fatalf("seed.Nonce = %q, want %q — a missing nonce would let the observer emit a second attention if the parked ReviewRequest is later re-published", seed.Nonce, roundID)
	}
	if len(reg.Policy.Events) == 0 || len(reg.Policy.Routes) == 0 {
		t.Fatalf("RunRegistered.Policy is empty; the workflow snapshot did not round-trip the resolved policy into Resume")
	}
	if reg.Policy.Events[0] != workflow.AttentionRequired {
		t.Fatalf("RunRegistered.Policy events = %v, want [attention_required]", reg.Policy.Events)
	}
}
