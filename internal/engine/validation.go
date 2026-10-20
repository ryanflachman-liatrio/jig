package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// ExecutionUncertainError means the managed workload's termination could not
// be confirmed. It is a durable fence, not an automatically retryable failure.
type ExecutionUncertainError struct{}

func (*ExecutionUncertainError) Error() string { return "managed execution termination uncertain" }

type validationStartMsg struct {
	stepID    string
	execution uint64
	result    *step.Result
	reply     chan error
}

func (validationStartMsg) isSchedMsg() {}

func (s *scheduler) matchesExecution(id string, execution uint64) bool {
	active, ok := s.activeExecutions[id]
	return ok && execution != 0 && active == execution
}

func acknowledgeValidation(ctx context.Context, inbox chan<- schedMsg, id string, execution uint64, result *step.Result) error {
	// Ownership transfers only the copy; the runner may edit its result after
	// the acknowledgement without racing snapshots of the main result.
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("copy main result before validation: %w", err)
	}
	var copyResult step.Result
	if err := json.Unmarshal(data, &copyResult); err != nil {
		return fmt.Errorf("copy main result before validation: %w", err)
	}
	reply := make(chan error, 1)
	select {
	case inbox <- validationStartMsg{stepID: id, execution: execution, result: &copyResult, reply: reply}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m validationStartMsg) execute(s *scheduler) {
	if s.containerUncertain(m.stepID) {
		m.reply <- &ExecutionUncertainError{}
		return
	}
	state := s.states[m.stepID]
	if !s.matchesExecution(m.stepID, m.execution) || state == nil || state.Status != step.StatusRunning || s.stopping[m.stepID] || s.fatalCaptureErr != nil {
		m.reply <- fmt.Errorf("validation acknowledgement rejected")
		return
	}
	state.Result = m.result
	st := s.stepByID(m.stepID)
	if err := s.authorizeSandboxStep(st); err != nil {
		m.reply <- err
		return
	}
	if m.result.SessionID != "" && s.runDir != "" {
		if err := datastore.WriteSession(s.runDir, m.stepID, datastore.SessionInfo{SessionID: m.result.SessionID, Backend: st.Backend, Attempt: state.Attempt, Iteration: state.Iteration, Generation: state.Generation}); err != nil {
			m.reply <- fmt.Errorf("persist session before validation: %w", err)
			return
		}
	}
	if !s.transition(m.stepID, state.Status, step.StatusValidating) {
		m.reply <- fmt.Errorf("persist validating transition")
		return
	}
	m.reply <- nil
}

// A run-wide cancellation can stop draining result reports before workers have
// returned. Keep their roots until independent, bounded shutdown confirms exit.
type workerExit struct {
	done      chan struct{}
	uncertain bool
}

func (s *scheduler) confirmWorkersBeforeCleanup() {
	if len(s.workerExits) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for id, exit := range s.workerExits {
		uncertain := false
		select {
		case <-exit.done:
			uncertain = exit.uncertain
		case <-ctx.Done():
			uncertain = true
		}
		if uncertain {
			state := s.states[id]
			if state.Result == nil {
				state.Result = &step.Result{}
			}
			state.Result.ExecutionUncertain = true
			state.Result.Status = step.StatusFailed
			state.Result.Err = "managed execution termination uncertain"
		}
	}
}

// Capacity is released only after an identified worker returns, or an admitted
// preparation fails before any worker starts. Family slots follow the same rule.
func (s *scheduler) releaseWorkerCapacity(id string) {
	s.inFlight--
	st := s.stepByID(id)
	if st != nil && st.ResourceClass != "" {
		s.classInFlight[st.ResourceClass]--
	}
	if st != nil && st.Isolation == workflow.IsolationWorktree {
		s.mutatingInFlight--
	} else {
		s.readOnlyInFlight--
	}
	if state := s.states[id]; state != nil && state.ParentID != "" {
		if family := s.fanOutFamilies[state.ParentID]; family != nil {
			family.InFlight--
		}
	}
}
