package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"jig/internal/datastore"
	"jig/internal/manifest"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

type workflowSnapshot struct {
	SourcePath    string                  `json:"source_path,omitempty"`
	BaseDir       string                  `json:"base_dir,omitempty"`
	SHA256        string                  `json:"sha256"`
	TOML          string                  `json:"toml"`
	ModuleSources []workflow.ModuleSource `json:"module_sources,omitempty"`
	Meta          workflow.Meta           `json:"meta"`
	Defaults      workflow.Defaults       `json:"defaults"`
	PublicSteps   []workflow.Step         `json:"public_steps,omitempty"`
	ExpandedSteps []workflow.Step         `json:"expanded_steps,omitempty"`
}

func persistWorkflowSnapshot(runDir string, wf *workflow.Workflow) error {
	if runDir == "" || wf == nil {
		return nil
	}
	path, source := wf.Source()
	if source == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(source))
	snap := workflowSnapshot{
		SourcePath:    path,
		BaseDir:       filepath.Dir(path),
		SHA256:        hex.EncodeToString(sum[:]),
		TOML:          source,
		ModuleSources: wf.ModuleSources(),
		Meta:          wf.Meta,
		Defaults:      wf.Defaults,
		PublicSteps:   wf.PublicSteps(),
		ExpandedSteps: wf.Steps,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(datastore.WorkflowSnapshotPath(runDir), data, 0o644)
}

func loadWorkflowSnapshot(runDir string) (*workflow.Workflow, error) {
	data, err := os.ReadFile(datastore.WorkflowSnapshotPath(runDir))
	if err != nil {
		return nil, err
	}
	var snap workflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode workflow snapshot: %w", err)
	}
	sum := sha256.Sum256([]byte(snap.TOML))
	if hex.EncodeToString(sum[:]) != snap.SHA256 {
		return nil, fmt.Errorf("workflow snapshot checksum mismatch")
	}
	for _, source := range snap.ModuleSources {
		sum := sha256.Sum256([]byte(source.TOML))
		if source.Path == "" || source.SHA256 == "" || hex.EncodeToString(sum[:]) != source.SHA256 {
			return nil, fmt.Errorf("workflow snapshot module checksum mismatch")
		}
	}
	if len(snap.ExpandedSteps) > 0 {
		return workflow.RestoreExpanded(snap.Meta, snap.Defaults, snap.PublicSteps, snap.ExpandedSteps, snap.ModuleSources), nil
	}
	return workflow.DecodeLocked(snap.TOML, snap.BaseDir, snap.SourcePath, snap.ModuleSources)
}

func acquireRunLock(runDir string) (*os.File, error) {
	if runDir == "" {
		return nil, nil
	}
	f, err := os.OpenFile(datastore.SchedulerLockPath(runDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("run already has a live scheduler")
	}
	return f, nil
}

func releaseRunLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// Resume restores an unfinished run parked exclusively on document-review
// gates. The narrow boundary is intentional: no worker or backend session is
// alive after the owning process exits, while review gates are fully durable.
func (m *Manager) Resume(runID string, legacyWorkflow *workflow.Workflow) (*Run, error) {
	if runID == "" || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return nil, fmt.Errorf("engine: invalid run id %q", runID)
	}
	m.mu.Lock()
	if _, exists := m.runs[runID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("run %s already has a scheduler", runID)
	}
	m.mu.Unlock()

	runDir := m.RunDir(runID)
	lock, err := acquireRunLock(runDir)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Run, error) {
		releaseRunLock(lock)
		return nil, err
	}

	seq, events, err := readJournal(runDir)
	if err != nil {
		return fail(err)
	}
	wf, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		if !os.IsNotExist(err) || legacyWorkflow == nil {
			return fail(fmt.Errorf("resume workflow: %w", err))
		}
		wf = legacyWorkflow
	}
	restored, sessions, err := restoreReviewCheckpoint(runDir, wf, events)
	if err != nil {
		return fail(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	inbox := make(chan schedMsg, 64)
	run := &Run{ID: runID, cancel: cancel, inbox: inbox, done: make(chan struct{}), runLock: lock}

	m.mu.Lock()
	if _, exists := m.runs[runID]; exists {
		m.mu.Unlock()
		cancel()
		return fail(fmt.Errorf("run %s already has a scheduler", runID))
	}
	subs := append([]sub(nil), m.subs...)
	m.runs[runID] = run
	m.mu.Unlock()

	w, err := manifest.NewWriter(runDir)
	if err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		cancel()
		return fail(err)
	}
	repoRoot := filepath.Dir(filepath.Clean(m.root))
	onDone := func(snap RunSnapshot) {
		run.finalSnap = snap
		releaseRunLock(run.runLock)
		run.runLock = nil
		close(run.done)
	}
	s := newScheduler(wf, runID, inbox, subs, m.exec, cancel, w, runDir, m.root, repoRoot, onDone)
	s.resolver = m.resolver
	s.seq = seq
	s.states = restored
	s.reviewSessions = sessions
	for i := range wf.Steps {
		if restored[wf.Steps[i].ID].Status == step.StatusSkipped && wf.Steps[i].When != "" {
			s.skippedByGuard[wf.Steps[i].ID] = true
		}
	}
	if err := s.restoreRunBranch(); err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return fail(fmt.Errorf("restore run branch: %w", err))
	}
	go s.runLoop(ctx)
	return run, nil
}

func restoreReviewCheckpoint(runDir string, wf *workflow.Workflow, events []Event) (map[string]*step.State, map[string]review.Session, error) {
	if wf == nil {
		return nil, nil, fmt.Errorf("resume: workflow is unavailable")
	}
	states := make(map[string]*step.State, len(wf.Steps))
	for i := range wf.Steps {
		states[wf.Steps[i].ID] = &step.State{ID: wf.Steps[i].ID, Status: step.StatusPending}
	}
	requests := make(map[string]ReviewRequest)
	verdicts := make(map[string]string)
	var started *RunStarted
	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			copy := event
			started = &copy
		case RunFinished:
			return nil, nil, fmt.Errorf("resume: run is already finished")
		case StepStatus:
			state := states[event.StepID]
			if state == nil {
				return nil, nil, fmt.Errorf("resume: workflow no longer contains step %q", event.StepID)
			}
			state.Status = event.To
			state.Attempt = event.Attempt
			state.Iteration = event.Iteration
			state.Generation = event.Generation
			if event.Cost != nil {
				state.SpentUSD = *event.Cost
			}
			if event.Tokens > state.SpentTokens {
				state.SpentTokens = event.Tokens
			}
			if terminalStatus(event.To) {
				state.Result = loadPersistedResult(runDir, event.StepID, event.To, event.Err, event.Subtype)
			}
			if event.To != step.StatusAwaitingReview {
				delete(requests, event.StepID)
			}
		case ReviewRequest:
			requests[event.StepID] = event
		case ReviewSubmitted:
			verdicts[event.StepID] = event.Verdict
			delete(requests, event.StepID)
		}
	}
	if started == nil || started.Workflow != wf.Meta.Name || len(started.Steps) != len(wf.Steps) {
		return nil, nil, fmt.Errorf("resume: workflow does not match the historical run")
	}
	for i, id := range started.Steps {
		if wf.Steps[i].ID != id {
			return nil, nil, fmt.Errorf("resume: workflow step order changed at %q", id)
		}
	}
	for id, verdict := range verdicts {
		if state := states[id]; state != nil {
			if state.Result == nil {
				state.Result = &step.Result{Status: state.Status}
			}
			state.Result.Verdict = verdict
		}
	}

	sessions := make(map[string]review.Session)
	for id, state := range states {
		switch state.Status {
		case step.StatusPending, step.StatusSucceeded, step.StatusSkipped:
		case step.StatusAwaitingReview:
			req, ok := requests[id]
			if !ok {
				return nil, nil, fmt.Errorf("resume: step %q is waiting for non-review input", id)
			}
			sess, err := restoreReviewSession(runDir, req)
			if err != nil {
				return nil, nil, err
			}
			sessions[id] = sess
		default:
			return nil, nil, fmt.Errorf("resume: step %q is %s; only quiescent review gates can resume", id, state.Status)
		}
	}
	if len(sessions) == 0 {
		return nil, nil, fmt.Errorf("resume: run has no pending review gate")
	}
	return states, sessions, nil
}

func terminalStatus(status step.Status) bool {
	switch status {
	case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		return true
	default:
		return false
	}
}

func loadPersistedResult(runDir, stepID string, status step.Status, resultErr, subtype string) *step.Result {
	result := &step.Result{Status: status, Err: resultErr, Subtype: subtype}
	if data, err := os.ReadFile(datastore.OutputJSONPath(runDir, stepID)); err == nil {
		result.Structured = data
	}
	if path := datastore.OutputPath(runDir, stepID); fileExists(path) {
		result.OutputPath = path
	}
	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func restoreReviewSession(runDir string, req ReviewRequest) (review.Session, error) {
	docs := append([]review.Document(nil), req.Documents...)
	for i := range docs {
		path := docs[i].SnapshotPath
		if path == "" || !fileExists(path) {
			path = filepath.Join(datastore.ReviewDocumentsDir(runDir, req.StepID, req.RoundID), docs[i].ID+reviewExtension(docs[i].Format))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return review.Session{}, fmt.Errorf("resume review %q: load document %q: %w", req.StepID, docs[i].ID, err)
		}
		docs[i].SnapshotPath = path
		docs[i].Content = string(data)
		if review.Digest(docs[i].Content) != docs[i].SHA256 {
			return review.Session{}, fmt.Errorf("resume review %q: document %q checksum mismatch", req.StepID, docs[i].ID)
		}
	}
	return review.Session{StepID: req.StepID, RoundID: req.RoundID, Documents: docs}, nil
}

func (s *scheduler) restoreRunBranch() error {
	if s.repoRoot == "" {
		return nil
	}
	if _, err := currentHEAD(s.repoRoot); err != nil {
		return nil
	}
	branch := runBranchName(s.wf.Meta.Name, s.runID)
	if _, err := gitCmd(s.repoRoot, "rev-parse", "--verify", branch); err != nil {
		return fmt.Errorf("integration branch %s is missing", branch)
	}
	wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, "_run")
	// A crashed process can leave the run branch registered to its old worktree.
	// The scheduler lock establishes single ownership among resume-capable jig processes.
	_ = removeWorktree(s.repoRoot, wtPath)
	_, _ = gitCmd(s.repoRoot, "worktree", "prune")
	base, err := createWorktreeAt(s.repoRoot, wtPath, branch, branch)
	if err != nil {
		return err
	}
	s.runBranch = branch
	s.runWorktree = wtPath
	s.runBaseSHA = base
	if out, err := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		s.baseBranch = strings.TrimSpace(out)
	}
	if s.baseBranch != "" {
		if out, err := gitCmd(s.repoRoot, "merge-base", branch, s.baseBranch); err == nil {
			s.runBaseSHA = strings.TrimSpace(out)
		}
	}
	commits, err := stepCommitsFromLog(s.repoRoot, branch)
	if err != nil {
		_ = removeWorktree(s.repoRoot, wtPath)
		s.runWorktree = ""
		return err
	}
	s.stepCommits = commits
	return nil
}
