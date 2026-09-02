package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// sddAcceptanceExec makes the fixture's scheduling and check protocol
// observable while leaving the engine, worktree, route, and evidence paths real.
type sddAcceptanceExec struct {
	mu sync.Mutex

	calls            map[string]int
	researchStarted  map[string]bool
	allResearchReady chan struct{}
	releaseResearch  chan struct{}
	researchOnce     sync.Once
}

func newSDDAcceptanceExec() *sddAcceptanceExec {
	return &sddAcceptanceExec{
		calls:            make(map[string]int),
		researchStarted:  make(map[string]bool),
		allResearchReady: make(chan struct{}),
		releaseResearch:  make(chan struct{}),
	}
}

func (e *sddAcceptanceExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	id := req.Step.ID
	e.mu.Lock()
	e.calls[id]++
	call := e.calls[id]
	if id == "spec__research_backend" || id == "spec__research_frontend" {
		e.researchStarted[id] = true
		if len(e.researchStarted) == 2 {
			e.researchOnce.Do(func() { close(e.allResearchReady) })
		}
	}
	e.mu.Unlock()

	switch id {
	case "spec__research_backend", "spec__research_frontend":
		select {
		case <-e.releaseResearch:
			return &step.Result{Status: step.StatusSucceeded}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "implementation__select_task":
		return &step.Result{Status: step.StatusSucceeded, Verdict: "ready"}, nil
	case "implementation__implement_code":
		if err := os.WriteFile(filepath.Join(req.Worktree, fmt.Sprintf("implementation-%d.txt", call)), []byte(fmt.Sprintf("attempt %d\n", call)), 0o644); err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	case "implementation__quality":
		outcome := "pass"
		if call == 1 {
			outcome = "fail"
		}
		if err := writeAcceptanceCheckEvidence(req, outcome); err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded, Verdict: outcome}, nil
	default:
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
}

func writeAcceptanceCheckEvidence(req StepRequest, outcome string) error {
	dir := filepath.Join(filepath.Dir(req.TranscriptPath), "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	prefix := fmt.Sprintf("generation-%03d-iteration-%03d-attempt-%03d", req.Generation, req.Iteration, req.Attempt)
	if err := os.WriteFile(filepath.Join(dir, prefix+".log"), []byte("quality "+outcome+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, prefix+".findings.json"), []byte(fmt.Sprintf(`{"schema_version":1,"outcome":"%s","findings":[]}`, outcome)), 0o644)
}

func (e *sddAcceptanceExec) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[id]
}

func TestSDDAcceptanceFixture(t *testing.T) {
	if _, err := os.Stat("testdata/sdd-acceptance/workflow.toml"); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	initRepo(t, repo)
	userChange := []byte("unrelated user work\n")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), userChange, 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(repo, "fixtures")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"workflow.toml", "spec.toml", "implementation.toml"} {
		data, err := os.ReadFile(filepath.Join("testdata", "sdd-acceptance", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixtureDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := workflow.Load(filepath.Join(fixtureDir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}

	exec := newSDDAcceptanceExec()
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-exec.allResearchReady:
		close(exec.releaseResearch)
	case <-time.After(5 * time.Second):
		t.Fatal("research fan-out did not dispatch concurrently")
	}

	var events []Event
	checkpoints := 0
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-ctrl:
			events = append(events, event)
			switch event := event.(type) {
			case ReviewRequest:
				if event.StepID != "implementation__checkpoint" {
					continue
				}
				if exec.callCount("implementation__quality") < 2 {
					t.Fatal("a failed check reached approval before remediation passed")
				}
				checkpoints++
				if checkpoints == 1 {
					run.Resolve(event.StepID, "redo")
				} else {
					if data, err := os.ReadFile(filepath.Join(repo, "seed.txt")); err != nil || string(data) != string(userChange) {
						t.Fatalf("redo changed unrelated user work: %q, %v", data, err)
					}
					if matches, err := filepath.Glob(filepath.Join(repo, "implementation-*.txt")); err != nil || len(matches) != 0 {
						t.Fatalf("redo integrated selected-task changes into the user worktree: %v", err)
					}
					run.Resolve(event.StepID, "continue")
				}
			case FinalMergeRequest:
				run.FinalMerge(false)
			case RunFinished:
				goto finished
			}
		case <-deadline:
			t.Fatal("timeout waiting for SDD acceptance fixture")
		}
	}

finished:
	if checkpoints != 2 {
		t.Fatalf("checkpoint count = %d, want redo then continue", checkpoints)
	}
	if got := exec.callCount("implementation__select_task"); got != 1 {
		t.Errorf("selected task %d times, want 1 across redo", got)
	}
	if got := exec.callCount("implementation__implement_code"); got != 3 {
		t.Errorf("implement_code ran %d times, want failure remediation plus redo", got)
	}
	if got := exec.callCount("implementation__quality"); got != 3 {
		t.Errorf("quality ran %d times, want one fail and two passing attempts", got)
	}
	if !containsRoute(events, "implementation__quality", "implementation__implement_code") {
		t.Error("missing automatic failed-check remediation route")
	}
	if !containsRoute(events, "implementation__checkpoint", "implementation__implement_code") {
		t.Error("missing redo route to the already-selected task")
	}

	evidenceDir := filepath.Join(mgr.RunDir(run.ID), "steps", "implementation__quality", "evidence")
	findings, err := filepath.Glob(filepath.Join(evidenceDir, "generation-*-iteration-*-attempt-*.findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(evidenceDir, "generation-*-iteration-*-attempt-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 || len(logs) != 3 {
		t.Fatalf("check evidence findings/logs = %d/%d, want 3/3", len(findings), len(logs))
	}
	for _, path := range append(findings, logs...) {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Errorf("evidence %q is not independently inspectable: %v", path, err)
		}
	}

	// Changing the source module after the run cannot alter the graph a resume
	// reconstructs: it reads the expanded execution graph captured at start.
	if err := os.WriteFile(filepath.Join(fixtureDir, "implementation.toml"), []byte("[module]\nschema_version = 1\n[[step]]\nid = \"changed\"\ntype = \"command\"\nrun = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	locked, err := loadWorkflowSnapshot(mgr.RunDir(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(locked.Steps))
	for _, st := range locked.Steps {
		ids = append(ids, st.ID)
	}
	if strings.Contains(strings.Join(ids, ","), "implementation__changed") || !strings.Contains(strings.Join(ids, ","), "implementation__implement_code") {
		t.Fatalf("locked expanded graph changed after module edit: %v", ids)
	}
}

func containsRoute(events []Event, stepID, target string) bool {
	for _, event := range events {
		if route, ok := event.(RouteSelected); ok && route.StepID == stepID && route.Goto == target {
			return true
		}
	}
	return false
}

const (
	acceptanceSpec  = "# Run specification\n\nThis is the integrated specification.\n"
	acceptanceTasks = "# Implementation tasks\n\n1. Implement the run snapshot.\n"
	acceptanceAudit = "Audit passed: the specification and task plan agree.\n"
)

// snapshotReviewExec models the three production roles in the smallest useful
// fixture: two mutating authors and a read-only auditor. The auditor reads from
// its dispatch directory, so a fallback to the user's checkout fails the test.
type snapshotReviewExec struct {
	mu       sync.Mutex
	auditDir string
}

func (e *snapshotReviewExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	structured := func(values map[string]string) (*step.Result, error) {
		data, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded, Structured: data}, nil
	}
	write := func(path, content string) error {
		if req.Worktree == "" {
			return fmt.Errorf("%s has no mutable worktree", req.Step.ID)
		}
		path = filepath.Join(req.Worktree, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}

	switch req.Step.ID {
	case "write_spec":
		if err := write("docs/spec.md", acceptanceSpec); err != nil {
			return nil, err
		}
		return structured(map[string]string{"spec_path": "docs/spec.md"})
	case "write_tasks":
		if err := write("docs/tasks.md", acceptanceTasks); err != nil {
			return nil, err
		}
		return structured(map[string]string{"task_path": "docs/tasks.md"})
	case "audit":
		if req.Worktree != "" {
			return nil, fmt.Errorf("read-only audit received mutable worktree %q", req.Worktree)
		}
		if req.ExecutionDir == "" {
			return nil, fmt.Errorf("read-only audit has no execution snapshot")
		}
		for path, want := range map[string]string{
			"docs/spec.md":  acceptanceSpec,
			"docs/tasks.md": acceptanceTasks,
		} {
			data, err := os.ReadFile(filepath.Join(req.ExecutionDir, path))
			if err != nil {
				return nil, fmt.Errorf("audit read %q: %w", path, err)
			}
			if got := string(data); got != want {
				return nil, fmt.Errorf("audit read %q = %q, want run snapshot %q", path, got, want)
			}
		}
		e.mu.Lock()
		e.auditDir = req.ExecutionDir
		e.mu.Unlock()
		return structured(map[string]string{"audit_report": acceptanceAudit})
	default:
		return nil, fmt.Errorf("unexpected acceptance step %q", req.Step.ID)
	}
}

// TestRunSnapshotFileReviewAcceptance proves the boundary shared by workers and
// human reviews: both consume the files integrated into the run branch, never
// coincidentally matching files in the user's checkout.
func TestRunSnapshotFileReviewAcceptance(t *testing.T) {
	fixture, err := os.ReadFile("testdata/sdd-acceptance/run-snapshots-review.toml")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Decode(string(fixture), "")
	if err != nil {
		t.Fatalf("decode acceptance fixture: %v", err)
	}

	repo := t.TempDir()
	initRepo(t, repo)
	userPath := filepath.Join(repo, "docs", "spec.md")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("# User checkout\n\nWrong document.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &snapshotReviewExec{}
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	_, events := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	wantDocs := []struct {
		label, source, content string
	}{
		{"Audit report", "@audit.audit_report", acceptanceAudit},
		{"Specification", "@write_spec.spec_path (docs/spec.md)", acceptanceSpec},
		{"Task plan", "@write_tasks.task_path (docs/tasks.md)", acceptanceTasks},
	}
	var observed []Event
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-events:
			observed = append(observed, event)
			switch event := event.(type) {
			case ReviewRequest:
				if event.StepID != "review" {
					t.Fatalf("review step = %q, want review", event.StepID)
				}
				if len(event.Documents) != len(wantDocs) {
					t.Fatalf("review documents = %#v", event.Documents)
				}
				for i, want := range wantDocs {
					doc := event.Documents[i]
					if doc.Label != want.label || doc.Source != want.source || doc.Content != want.content {
						t.Fatalf("document %d = %#v, want label=%q source=%q content=%q", i, doc, want.label, want.source, want.content)
					}
					data, err := os.ReadFile(doc.SnapshotPath)
					if err != nil || string(data) != want.content {
						t.Fatalf("snapshot %q = %q, %v; want %q", doc.SnapshotPath, data, err, want.content)
					}
				}
				run.Resolve("review", "approve")
			case FinalMergeRequest:
				run.FinalMerge(false)
			case RunFinished:
				if event.Failed {
					t.Fatal("acceptance run finished failed")
				}
				goto finished
			}
		case <-deadline:
			t.Fatal("timeout waiting for run snapshot review acceptance")
		}
	}

finished:
	exec.mu.Lock()
	auditDir := exec.auditDir
	exec.mu.Unlock()
	if auditDir == "" {
		t.Fatal("audit did not receive an execution snapshot")
	}
	if got, err := os.ReadFile(userPath); err != nil || strings.Contains(string(got), "integrated specification") {
		t.Fatalf("user checkout document changed to %q, %v", got, err)
	}
	if statuses := findStatus(observed, "review"); len(statuses) == 0 || statuses[len(statuses)-1] != step.StatusSucceeded {
		t.Fatalf("review statuses = %v, want final succeeded", statuses)
	}
}
