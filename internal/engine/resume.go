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
	"jig/internal/interaction"
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

// Resume restores every durable unfinished park under one scheduler. Workers
// interrupted mid-flight are moved to recovery; parks that already existed
// before process death are rehydrated in place.
func (m *Manager) Resume(runID string) (*Run, error) {
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
		return fail(fmt.Errorf("resume workflow: %w", err))
	}
	checkpoint, err := restoreUnfinishedCheckpoint(runDir, wf, events)
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
	s.states = checkpoint.states
	s.reviewSessions = checkpoint.reviewSessions
	s.restoredHold = checkpoint.hasParks()
	for stepID := range checkpoint.recoverySkipped {
		s.skippedByOperator[stepID] = true
	}
	for i := range wf.Steps {
		if checkpoint.states[wf.Steps[i].ID].Status == step.StatusSkipped && wf.Steps[i].When != "" {
			s.skippedByGuard[wf.Steps[i].ID] = true
		}
	}
	if err := s.restoreRunBranch(len(checkpoint.integrations) > 0); err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return fail(fmt.Errorf("restore run branch: %w", err))
	}
	// Journal the entire recovery transaction before runLoop so a crash during
	// reopen cannot lose the decision surface (Spec 20 D18). The events are
	// fanned out only after the batch is durable.
	reopenEvents, err := s.rehydrateParks(checkpoint)
	if err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return fail(err)
	}
	if err := s.emitBatch(reopenEvents); err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		if s.runWorktree != "" && len(checkpoint.integrations) == 0 {
			_ = removeWorktree(s.repoRoot, s.runWorktree)
			s.runWorktree = ""
		}
		_ = w.Close()
		cancel()
		return fail(fmt.Errorf("journal reopened parks: %w", err))
	}
	go s.runLoop(ctx)
	return run, nil
}

// processInterruptedErr is the durable sentinel written when Resume parks a
// worker that was mid-flight when the owning jig process exited (Spec 20 D14).
const processInterruptedErr = "process exited while step was running"

// processInterruptedRecoveryAction makes a recovery park idempotently
// repairable if a process exits between the batch's newline record boundaries.
// It is internal journal provenance, not an operator-selectable action.
const processInterruptedRecoveryAction = "process_interrupted"

const lostInputRecoveryAction = "lost_input"

type unfinishedCheckpoint struct {
	states              map[string]*step.State
	reviewSessions      map[string]review.Session
	interrupted         map[string]bool
	recoveries          map[string]RecoveryRequest
	inputs              map[string]InputRequest
	questions           map[string][]AgentQuestion
	integrations        map[string]IntegrationConflictRequest
	stopped             map[string]bool
	missingInputPayload map[string]string
	recoverySkipped     map[string]bool
}

func (c *unfinishedCheckpoint) hasParks() bool {
	return len(c.reviewSessions) > 0 || len(c.interrupted) > 0 || len(c.recoveries) > 0 ||
		len(c.inputs) > 0 || len(c.questions) > 0 || len(c.integrations) > 0 ||
		len(c.stopped) > 0 || len(c.missingInputPayload) > 0
}

func restoreUnfinishedCheckpoint(runDir string, wf *workflow.Workflow, events []Event) (*unfinishedCheckpoint, error) {
	if wf == nil {
		return nil, fmt.Errorf("resume: workflow is unavailable")
	}
	states := make(map[string]*step.State, len(wf.Steps))
	for i := range wf.Steps {
		states[wf.Steps[i].ID] = &step.State{ID: wf.Steps[i].ID, Status: step.StatusPending}
	}
	reviewRequests := make(map[string]ReviewRequest)
	recoveryRequests := make(map[string]RecoveryRequest)
	inputRequests := make(map[string]InputRequest)
	questions := make(map[string][]AgentQuestion)
	integrationRequests := make(map[string]IntegrationConflictRequest)
	verdicts := make(map[string]string)
	recoverySkipped := make(map[string]bool)
	interruptedRecovery := make(map[string]bool)
	lostInputRecovery := make(map[string]bool)
	var started *RunStarted
	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			copy := event
			started = &copy
		case RunFinished:
			return nil, fmt.Errorf("resume: run is already finished")
		case StepStatus:
			state := states[event.StepID]
			if state == nil {
				return nil, fmt.Errorf("resume: workflow no longer contains step %q", event.StepID)
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
			if event.To == step.StatusFailed && event.RecoveryAction == RecoverSkip {
				recoverySkipped[event.StepID] = true
			} else if event.To != step.StatusFailed {
				delete(recoverySkipped, event.StepID)
			}
			if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == processInterruptedRecoveryAction {
				interruptedRecovery[event.StepID] = true
			} else {
				delete(interruptedRecovery, event.StepID)
			}
			if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == lostInputRecoveryAction {
				lostInputRecovery[event.StepID] = true
			} else {
				delete(lostInputRecovery, event.StepID)
			}
			if event.To != step.StatusAwaitingReview {
				delete(reviewRequests, event.StepID)
			}
			if event.To != step.StatusAwaitingRecovery {
				delete(recoveryRequests, event.StepID)
			}
			if event.To != step.StatusNeedsInput {
				delete(inputRequests, event.StepID)
				delete(questions, event.StepID)
			}
			if event.To != step.StatusAwaitingIntegration {
				delete(integrationRequests, event.StepID)
			}
		case ReviewRequest:
			reviewRequests[event.StepID] = event
		case ReviewSubmitted:
			verdicts[event.StepID] = event.Verdict
			delete(reviewRequests, event.StepID)
		case RecoveryRequest:
			recoveryRequests[event.StepID] = event
		case InputRequest:
			inputRequests[event.StepID] = event
		case AgentQuestion:
			questions[event.StepID] = upsertQuestion(questions[event.StepID], event)
		case AgentQuestionResolved:
			questions[event.StepID] = removeQuestion(questions[event.StepID], event.RequestID)
		case IntegrationConflictRequest:
			integrationRequests[event.StepID] = event
		}
	}
	if started == nil || started.Workflow != wf.Meta.Name || len(started.Steps) != len(wf.Steps) {
		return nil, fmt.Errorf("resume: workflow does not match the historical run")
	}
	for i, id := range started.Steps {
		if wf.Steps[i].ID != id {
			return nil, fmt.Errorf("resume: workflow step order changed at %q", id)
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

	checkpoint := &unfinishedCheckpoint{
		states:              states,
		reviewSessions:      make(map[string]review.Session),
		interrupted:         make(map[string]bool),
		recoveries:          make(map[string]RecoveryRequest),
		inputs:              make(map[string]InputRequest),
		questions:           make(map[string][]AgentQuestion),
		integrations:        make(map[string]IntegrationConflictRequest),
		stopped:             make(map[string]bool),
		missingInputPayload: make(map[string]string),
		recoverySkipped:     recoverySkipped,
	}
	for i := range wf.Steps {
		id := wf.Steps[i].ID
		state := states[id]
		switch state.Status {
		case step.StatusPending, step.StatusSucceeded, step.StatusSkipped, step.StatusFailed:
		case step.StatusAwaitingReview:
			req, ok := reviewRequests[id]
			if !ok {
				return nil, fmt.Errorf("resume: step %q has no durable review request", id)
			}
			sess, err := restoreReviewSession(runDir, req)
			if err != nil {
				return nil, err
			}
			checkpoint.reviewSessions[id] = sess
		case step.StatusRunning, step.StatusValidating:
			checkpoint.interrupted[id] = true
		case step.StatusAwaitingRecovery:
			req, ok := recoveryRequests[id]
			if !ok {
				errText := "step was awaiting recovery when the jig process exited"
				if interruptedRecovery[id] {
					errText = processInterruptedErr
				} else if lostInputRecovery[id] {
					errText = "process exited while step needed input, but its durable input could not be restored"
				}
				req = RecoveryRequest{RunID: started.RunID, StepID: id, Err: errText}
			}
			checkpoint.recoveries[id] = req
		case step.StatusNeedsInput:
			switch {
			case len(questions[id]) > 0:
				checkpoint.questions[id] = questions[id]
			case inputRequests[id].StepID != "":
				checkpoint.inputs[id] = inputRequests[id]
			default:
				checkpoint.missingInputPayload[id] = "process exited while step needed input, but no durable input request was found"
			}
		case step.StatusAwaitingIntegration:
			checkpoint.integrations[id] = integrationRequests[id]
		case step.StatusStopped:
			checkpoint.stopped[id] = true
		default:
			return nil, fmt.Errorf("resume: step %q has unsupported durable status %s", id, state.Status)
		}
	}
	if !checkpoint.hasParks() && len(recoverySkipped) == 0 {
		return nil, fmt.Errorf("resume: run has no reopenable park")
	}
	return checkpoint, nil
}

func upsertQuestion(existing []AgentQuestion, question AgentQuestion) []AgentQuestion {
	for i := range existing {
		if existing[i].Request.ID == question.Request.ID {
			existing[i] = question
			return existing
		}
	}
	return append(existing, question)
}

func removeQuestion(existing []AgentQuestion, requestID string) []AgentQuestion {
	for i := range existing {
		if existing[i].Request.ID == requestID {
			return append(existing[:i], existing[i+1:]...)
		}
	}
	return existing
}

func (s *scheduler) rehydrateParks(checkpoint *unfinishedCheckpoint) ([]Event, error) {
	var events []Event
	for i := range s.wf.Steps {
		stepID := s.wf.Steps[i].ID
		state := s.states[stepID]
		switch state.Status {
		case step.StatusRunning, step.StatusValidating:
			events = append(events, s.parkInterruptedWorker(stepID)...)
		case step.StatusAwaitingRecovery:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
			req := checkpoint.recoveries[stepID]
			if req.Err == "" {
				req.Err = "step was awaiting recovery when the jig process exited"
			}
			if state.Result != nil {
				state.Result.Err = req.Err
				state.Result.Status = step.StatusFailed
				req.CanResume = s.stepCanResume(stepID, state.Result.SessionID)
			} else {
				req.CanResume = false
			}
			req.RunID = s.runID
			req.StepID = stepID
			events = append(events, req)
		case step.StatusStopped:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
		case step.StatusNeedsInput:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
			errText := checkpoint.missingInputPayload[stepID]
			if errText == "" && (state.Result == nil || !s.stepCanResume(stepID, state.Result.SessionID)) {
				errText = "process exited while step needed input, but its agent session cannot be resumed"
			}
			if errText != "" {
				events = append(events, s.parkLostInput(stepID, errText)...)
				continue
			}
			if pending := checkpoint.questions[stepID]; len(pending) > 0 {
				s.pendingQuestions[stepID] = make(map[string]pendingQuestion, len(pending))
				for _, question := range pending {
					question.RunID = s.runID
					s.pendingQuestions[stepID][question.Request.ID] = pendingQuestion{request: question.Request, restored: true}
					events = append(events, question)
				}
			} else {
				request := checkpoint.inputs[stepID]
				request.RunID = s.runID
				events = append(events, request)
			}
		case step.StatusAwaitingIntegration:
			paths := mergeConflictPaths(s.runWorktree)
			if len(paths) == 0 {
				return nil, fmt.Errorf("resume: step %q is awaiting integration but the run worktree has no unresolved conflict markers", stepID)
			}
			request := checkpoint.integrations[stepID]
			events = append(events, s.integrationConflictRequest(stepID, paths, request.Resolution))
		}
	}
	return events, nil
}

func (s *scheduler) restoreParkSession(stepID string) {
	state := s.states[stepID]
	if state == nil {
		return
	}
	if state.Result == nil {
		state.Result = loadPersistedResult(s.runDir, stepID, state.Status, "", "")
	}
	if info, err := datastore.ReadSession(s.runDir, stepID); err == nil && info.SessionID != "" {
		state.Result.SessionID = info.SessionID
	}
}

func (s *scheduler) reattachStepWorktree(stepID string) {
	st := s.stepByID(stepID)
	if st == nil || st.Isolation != workflow.IsolationWorktree || s.jigRoot == "" {
		return
	}
	path := filepath.Join(s.jigRoot, "worktrees", s.runID, stepID)
	if !directoryExists(path) {
		return
	}
	s.worktrees[stepID] = path
	if !registeredWorktree(s.repoRoot, path, s.stepBranchName(stepID)) {
		return
	}
	if base, err := gitCmd(path, "merge-base", "HEAD", s.runBranch); err == nil {
		if base = strings.TrimSpace(base); base != "" {
			s.wtBaseSHAs[stepID] = base
		}
	}
}

func (s *scheduler) parkLostInput(stepID, errText string) []Event {
	state := s.states[stepID]
	if state == nil || state.Status != step.StatusNeedsInput {
		return nil
	}
	if state.Result == nil {
		state.Result = &step.Result{}
	}
	state.Result.Status = step.StatusFailed
	state.Result.Err = errText
	status := s.transitionEvent(stepID, step.StatusNeedsInput, step.StatusAwaitingRecovery, lostInputRecoveryAction)
	return []Event{status, RecoveryRequest{RunID: s.runID, StepID: stepID, Err: errText}}
}

func restoredQuestionMessage(responses []interaction.QuestionResponse) string {
	data, err := json.Marshal(responses)
	if err != nil {
		return "The jig process exited while you were waiting for AskUserQuestion input. Continue using the operator's submitted answers."
	}
	return "The jig process exited while you were waiting for AskUserQuestion input. Continue using these operator responses: " + string(data)
}

// parkInterruptedWorker prepares the durable events that move an interrupted
// running/validating step onto the recovery gate. The caller persists all such
// events as one batch before starting the scheduler. Attempt is not bumped here
// (Spec 20 D15) — that waits for Recover(retry|resume).
func (s *scheduler) parkInterruptedWorker(stepID string) []Event {
	state := s.states[stepID]
	if state == nil {
		return nil
	}
	from := state.Status
	if from != step.StatusRunning && from != step.StatusValidating && from != step.StatusAwaitingRecovery {
		return nil
	}
	if state.Result == nil {
		state.Result = &step.Result{Status: step.StatusFailed}
	}
	if info, err := datastore.ReadSession(s.runDir, stepID); err == nil && info.SessionID != "" {
		state.Result.SessionID = info.SessionID
	}
	state.Result.Err = processInterruptedErr
	state.Result.Status = step.StatusFailed

	s.reattachStepWorktree(stepID)

	status := s.transitionEvent(stepID, from, step.StatusAwaitingRecovery, processInterruptedRecoveryAction)
	return []Event{status, RecoveryRequest{
		RunID:     s.runID,
		StepID:    stepID,
		Err:       processInterruptedErr,
		CanResume: s.stepCanResume(stepID, state.Result.SessionID),
	}}
}

// stepCanResume reports whether Recover(resume) should be offered: durable
// SessionID plus CapSessionResume when the executor can answer, and a present
// mutator worktree when the step requires one (D5 / D10).
func (s *scheduler) stepCanResume(stepID, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	if s.failedSessionResume[stepID] {
		return false
	}
	st := s.stepByID(stepID)
	if st == nil || st.Type != workflow.StepAgent {
		return false
	}
	support, ok := s.exec.(SessionResumeSupport)
	if !ok || !support.SupportsSessionResume(st.Backend, st.Transport) {
		return false
	}
	if st.Isolation == workflow.IsolationWorktree {
		path := s.worktrees[stepID]
		if path == "" && s.jigRoot != "" {
			path = filepath.Join(s.jigRoot, "worktrees", s.runID, stepID)
		}
		if path == "" || !directoryExists(path) || s.wtBaseSHAs[stepID] == "" {
			return false
		}
	}
	return true
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

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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

func (s *scheduler) restoreRunBranch(preserveConflict bool) error {
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
	var base string
	if preserveConflict {
		// Recreating the run worktree would discard the index/conflict index
		// state that is the integration gate's durable payload.
		if !registeredWorktree(s.repoRoot, wtPath, branch) {
			return fmt.Errorf("integration worktree for %s is missing or is not registered to %s", s.runID, branch)
		}
		if len(mergeConflictPaths(wtPath)) == 0 {
			return fmt.Errorf("integration worktree for %s has no unresolved conflict markers", s.runID)
		}
		var err error
		base, err = currentHEAD(wtPath)
		if err != nil {
			return fmt.Errorf("read integration worktree HEAD: %w", err)
		}
	} else {
		// A crashed process can leave the run branch registered to its old worktree.
		// The scheduler lock establishes single ownership among resume-capable jig processes.
		_ = removeWorktree(s.repoRoot, wtPath)
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
		var err error
		base, err = createWorktreeAt(s.repoRoot, wtPath, branch, branch)
		if err != nil {
			return err
		}
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
