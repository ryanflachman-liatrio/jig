package engine

import (
	"context"
	"io"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// validationWork is owned by the scheduler. The worker only closes done and
// returns this pointer as an opaque identity token; it never reads its fields.
type validationWork struct {
	cancel      context.CancelFunc
	done        chan struct{}
	completion  stepDoneMsg
	nextHandler int
}

type validationDoneMsg struct {
	stepID   string
	owner    *validationWork
	passed   bool
	detail   string
	timedOut bool
}

func (validationDoneMsg) isSchedMsg() {}

func (s *scheduler) startValidation(m stepDoneMsg, wfStep *workflow.Step) {
	ctx, cancel := context.WithCancel(s.runCtx)
	if !m.deadline.IsZero() {
		cancel()
		ctx, cancel = context.WithDeadline(s.runCtx, m.deadline)
	}
	work := &validationWork{cancel: cancel, done: make(chan struct{}), completion: m}
	s.rt(m.stepID).validation = work
	s.stepCancels[m.stepID] = cancel
	// The execution worker released its slots before reaching this handler.
	// Charge validation before returning to the dispatch loop so concurrency,
	// family barriers, and reset quiescence include the gate's lifetime.
	s.acquireSlots(wfStep)
	validation := *wfStep.Validate
	gateStep := workflow.Step{Output: wfStep.Output, Validate: &validation}
	dir := s.executionDirForStep(m.stepID)
	inbox, runCtx, done := s.inbox, s.runCtx, work.done
	go func() {
		defer close(done)
		defer cancel()
		passed, detail := runGate(ctx, &gateStep, dir)
		msg := validationDoneMsg{stepID: m.stepID, owner: work, passed: passed, detail: detail,
			timedOut: ctx.Err() == context.DeadlineExceeded}
		select {
		case inbox <- msg:
		case <-runCtx.Done():
		}
	}()
}

func (m validationDoneMsg) execute(s *scheduler) {
	rt := s.rt(m.stepID)
	if rt.validation != m.owner || m.owner == nil {
		return // a stale completion must not release a replacement's resources
	}
	rt.validation = nil
	m.owner.cancel()
	delete(s.stepCancels, m.stepID)
	wfStep := s.stepByID(m.stepID)
	s.releaseSlots(wfStep)
	defer s.releaseExecutionViewForStep(m.stepID)
	if s.exitParkedOrStopped(m.owner.completion) {
		return
	}
	if s.states[m.stepID].Status != step.StatusValidating {
		return
	}
	if m.timedOut {
		m.passed, m.detail = false, "step timeout exceeded during validation"
	}
	if err := s.emit(GateResult{RunID: s.runID, StepID: m.stepID, Passed: m.passed, Detail: m.detail}); err != nil {
		return
	}
	if !m.passed {
		result := ensureResult(s, m.stepID)
		result.Status = step.StatusFailed
		if result.Err == "" {
			result.Err = m.detail
		}
		if m.timedOut && s.scheduleAutomaticRetry(m.stepID, wfStep, stepDoneMsg{result: result, timedOut: true}) {
			return
		}
		s.applyFailurePolicy(m.stepID, wfStep)
		return
	}
	s.continuePostExecChain(m.owner.completion, wfStep, m.owner.nextHandler)
}

func (s *scheduler) stopValidations() {
	for _, rt := range s.runtime {
		if rt.validation != nil {
			rt.validation.cancel()
		}
	}
	for _, rt := range s.runtime {
		if rt.validation != nil {
			<-rt.validation.done
		}
	}
}

// gateOutput keeps a bounded diagnostic prefix while draining both pipes.
// exec.Cmd uses one copy goroutine when stdout and stderr share this writer.
type gateOutput struct {
	text      strings.Builder
	truncated bool
}

const maxGateOutput = 64 << 10

func (w *gateOutput) Write(p []byte) (int, error) {
	n := min(len(p), maxGateOutput-w.text.Len())
	w.text.Write(p[:n])
	w.truncated = w.truncated || n < len(p)
	return len(p), nil
}

func (w *gateOutput) String() string {
	if w.truncated {
		return w.text.String() + "\n[validation output truncated]"
	}
	return w.text.String()
}

// gateReader checks cancellation between regular-file reads. Special files are
// rejected before reading so a FIFO cannot strand shutdown waiting for a writer.
type gateReader struct {
	ctx context.Context
	io.Reader
}

func (r gateReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
