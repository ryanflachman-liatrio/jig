package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

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
	// reopen produces the durable reopen events for one parked step id — static
	// or a runtime fan-out child alike, reusing the exact Specs 20/21 park-reopen
	// path (docs/plans/a8-dynamic-foreach-fan-out.md, "Durability, replay, and
	// crash reopen": "A child ... reopens through the same Specs 20/21 path as
	// a static step").
	reopen := func(stepID string) error {
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
				return nil
			}
			if pending := checkpoint.questions[stepID]; len(pending) > 0 {
				s.rt(stepID).questions = make(map[string]pendingQuestion, len(pending))
				for _, question := range pending {
					question.RunID = s.runID
					s.rt(stepID).questions[question.Request.ID] = pendingQuestion{request: question.Request, restored: true}
					events = append(events, question)
				}
			} else {
				request := checkpoint.inputs[stepID]
				request.RunID = s.runID
				events = append(events, request)
			}
		case step.StatusAwaitingIntegration:
			paths := mergeConflictPaths(s.branch.worktree)
			if len(paths) == 0 {
				return fmt.Errorf("resume: step %q is awaiting integration but the run worktree has no unresolved conflict markers", stepID)
			}
			request := checkpoint.integrations[stepID]
			events = append(events, s.integrationConflictRequest(stepID, paths, request.Resolution))
		case step.StatusAwaitingReview:
			// Review parks reopen from checkpoint.reviewSessions (see Resume).
		default:
			if state.Status.IsParked() || state.Status.IsInFlight() {
				return fmt.Errorf("resume: step %q has status %s with no reopen path", stepID, state.Status)
			}
		}
		return nil
	}

	for i := range s.wf.Steps {
		stepID := s.wf.Steps[i].ID
		// A foreach family left Running is a barrier, not a worker — see the
		// matching special case in restoreUnfinishedCheckpoint. Its children
		// are reopened independently below.
		if isOpenFamilyBarrier(&s.wf.Steps[i], s.states[stepID].Status) {
			// Resume the barrier's clock after the process-absence interval. Like
			// restored gates, this projection is published in the recovery batch.
			events = append(events, s.transitionEvent(stepID, step.StatusRunning, step.StatusRunning, ""))
			continue
		}
		if err := reopen(stepID); err != nil {
			return nil, err
		}
	}
	for _, familyID := range checkpoint.fanOut.order {
		fam := checkpoint.fanOut.families[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			if err := reopen(childID); err != nil {
				return nil, err
			}
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
	if info, err := s.runDir.Step(stepID).ReadSession(); err == nil && info.SessionID != "" {
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
	wt := stepWorktree{path: path}
	defer func() { s.worktrees[stepID] = wt }()
	if !registeredWorktree(s.repoRoot, path, s.stepBranchName(stepID)) {
		return
	}
	if base, err := gitCmd(path, "merge-base", "HEAD", s.branch.name); err == nil {
		wt.base = strings.TrimSpace(base)
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
	if info, err := s.runDir.Step(stepID).ReadSession(); err == nil && info.SessionID != "" {
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
	if s.rt(stepID).failedSessionResume {
		return false
	}
	st := s.stepByID(stepID)
	if !hasAgentSession(st) {
		return false
	}
	support, ok := s.exec.(SessionResumeSupport)
	if !ok || !support.SupportsSessionResume(st.Backend) {
		return false
	}
	if st.Isolation == workflow.IsolationWorktree {
		wt := s.worktrees[stepID]
		path := wt.path
		if path == "" && s.jigRoot != "" {
			path = filepath.Join(s.jigRoot, "worktrees", s.runID, stepID)
		}
		if path == "" || !directoryExists(path) || wt.base == "" {
			return false
		}
	}
	return true
}

func restoreReviewSession(runDir datastore.Run, req ReviewRequest) (review.Session, error) {
	docs := append([]review.Document(nil), req.Documents...)
	for i := range docs {
		path := docs[i].SnapshotPath
		if path == "" || !fileExists(path) {
			path = filepath.Join(runDir.Step(req.StepID).Review(req.RoundID).Documents(), docs[i].ID+reviewExtension(docs[i].Format))
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
