package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// RunSnapshot is the Memento pattern's memento: an opaque, point-in-time
// summary of a run's state, safe to read from any goroutine because it is a
// plain value copy, not a reference into the scheduler's live state.
// scheduler (via its snapshot() method below) is the originator that knows
// how to capture and restore this state; Manager.Snapshot()/Run.Snapshot()
// and replay.go are the caretakers that request, store, and hand back
// mementos without inspecting or mutating their contents.
type RunSnapshot struct {
	ID           string
	Workflow     string
	Steps        []step.State
	Done         bool
	Failed       bool
	TotalCostUSD float64 // sum of all steps' TotalCostUSD; 0.0 when none reported
	TotalTokens  int     // sum of all steps' token counts; 0 when none reported
}

// Run is the caller's handle to a live scheduler goroutine.
type Run struct {
	ID      string
	cancel  context.CancelFunc
	inbox   chan schedMsg
	runLock *RunLease
	// done is closed by the scheduler goroutine before it exits.
	// finalSnap is written before done is closed; reads after observing
	// done closed see the written value (Go memory model, channel close).
	done      chan struct{}
	finalSnap RunSnapshot
	// causeMu guards causeHint, updated only from Run public methods; the
	// scheduler reads it after ctx.Done to distinguish deliberate interruption
	// from an unclassified engine-level cancellation.
	causeMu   sync.Mutex
	causeHint CompletionCause
}

// Cancel terminates the run. In-flight workers receive context cancellation.
// The recorded completion cause is CauseCancelled.
func (r *Run) Cancel() { r.CancelWithCause(CauseCancelled) }

// CancelWithCause terminates the run and records a semantic cause used only
// for the terminating RunFinished event's classification. The first non-empty
// cause wins so a later interrupt does not overwrite a deliberate policy
// rejection. Empty causes fall back to CauseCancelled.
func (r *Run) CancelWithCause(cause CompletionCause) {
	if cause == "" {
		cause = CauseCancelled
	}
	r.causeMu.Lock()
	if r.causeHint == "" {
		r.causeHint = cause
	}
	r.causeMu.Unlock()
	r.cancel()
}

// causeSnapshot reads the recorded cancellation cause without holding the
// scheduler's inbox open.
func (r *Run) causeSnapshot() CompletionCause {
	r.causeMu.Lock()
	defer r.causeMu.Unlock()
	return r.causeHint
}

// Wait blocks until the scheduler goroutine exits and returns the final
// snapshot. It is safe to call after Cancel — headless clients must wait for
// settle rather than returning on the cancel alone (ctrl channels never close).
func (r *Run) Wait() RunSnapshot {
	<-r.done
	return r.finalSnap
}

// Stop halts one running step's worker without ending the run (spec 07 B1). The
// step's partial work is preserved and it parks at step.StatusStopped; the run
// stays alive and becomes quiescent (no worker in flight). Stopping a step that
// is not currently running is a no-op. Resume the step with Run.Resume.
func (r *Run) Stop(stepID string) { r.inbox <- stopMsg{stepID: stepID} }

// Resume brings a step parked at step.StatusStopped back up (spec 07 B2). When
// the stopped step captured an SDK session id its agent session is continued
// with message as the new turn; otherwise the step restarts fresh (a documented
// degrade, since the SDK cannot always surface a session id at cancel time).
// Resuming a step that is not stopped is a no-op.
func (r *Run) Resume(stepID, message string) {
	r.inbox <- resumeMsg{stepID: stepID, message: message}
}

// ClosureOf returns the reset set for stepID — the step itself plus every
// step that transitively depends on it, in declaration order. This is the
// same set handleReset would invalidate, and the TUI uses it to compute the
// blast-radius count for the reset confirmation dialog. Returns nil when the
// run is already settled (the scheduler goroutine has exited).
func (r *Run) ClosureOf(stepID string) []string {
	select {
	case <-r.done:
		return nil
	default:
	}
	reply := make(chan []string, 1)
	select {
	case r.inbox <- closureReqMsg{stepID: stepID, reply: reply}:
		select {
		case cl := <-reply:
			return cl
		case <-r.done:
			return nil
		}
	case <-r.done:
		return nil
	}
}

// Reset rewinds the run branch to before stepID's transitive depends_on
// closure, replays independent survivor commits, returns the closure to pending,
// and bumps each reset step's Generation counter (spec 08 C2). Only valid on
// an unfinished, quiescent run (no worker in flight); settled runs and in-flight
// runs and persistence-off runs return a typed error.
func (r *Run) Reset(stepID string) (ResetResult, error) {
	select {
	case <-r.done:
		return ResetResult{}, &ResetError{Code: "settled", Target: stepID, Err: fmt.Errorf("run is already settled")}
	default:
	}
	reply := make(chan resetReply, 1)
	select {
	case r.inbox <- resetMsg{stepID: stepID, reply: reply}:
		select {
		case result := <-reply:
			return result.result, result.err
		case <-r.done:
			return ResetResult{}, &ResetError{Code: "settled", Target: stepID, Err: fmt.Errorf("run settled before reset completed")}
		}
	case <-r.done:
		return ResetResult{}, &ResetError{Code: "settled", Target: stepID, Err: fmt.Errorf("run is already settled")}
	}
}

type ResetResult struct {
	Target   string   `json:"target"`
	Closure  []string `json:"closure"`
	RewindTo string   `json:"rewind_sha"`
}

type ResetError struct {
	Code   string
	Target string
	Err    error
}

func (e *ResetError) Error() string {
	return fmt.Sprintf("reset %q (%s): %v", e.Target, e.Code, e.Err)
}

func (e *ResetError) Unwrap() error { return e.Err }

// Resolve delivers a human verdict for a review step (Phase 3+).
func (r *Run) Resolve(stepID, verdict string) {
	r.inbox <- reviewSubmissionMsg{stepID: stepID, submission: review.Submission{Verdict: verdict}}
}

func (r *Run) ResolveReview(stepID string, submission review.Submission) {
	r.inbox <- reviewSubmissionMsg{stepID: stepID, submission: submission}
}

// ProvideUserInput delivers text collected from the user for a from="user" input.
func (r *Run) ProvideUserInput(stepID, as, text string) {
	r.inbox <- userInputMsg{stepID: stepID, as: as, text: text}
}

// Message delivers a free-text human message to an awaiting_review gate, which
// routes it to the reviewed agent step for continued processing. The agent
// resumes its SDK session, re-runs, and the gate re-fires when done.
// SendInput delivers a human response to an agent step that is blocked by
// block_on. The agent resumes its session with the response as the query.
func (r *Run) SendInput(stepID, text string) {
	r.inbox <- agentInputMsg{stepID: stepID, text: text}
}

// AnswerQuestion delivers the user's response to an in-flight AskUserQuestion call.
func (r *Run) AnswerQuestion(stepID string, response interaction.QuestionResponse) {
	r.inbox <- agentQuestionAnswerMsg{stepID: stepID, response: response}
}

// Recover delivers a human recovery decision for a step parked in
// step.StatusAwaitingRecovery after an unrecoverable failure. action is one of:
//
//   - RecoverRetry  — re-run the step fresh (a new agent session / full prompt).
//   - RecoverResume — re-run the failed agent session, feeding the captured
//     error plus the optional text as guidance so it doesn't repeat the mistake.
//   - RecoverSkip   — accept the failure and continue: the step stays failed but
//     is treated as a transparent on_failure="continue" node so its dependents
//     proceed. The run is NOT torn down. This is the release valve for a step
//     whose failure a human judges non-fatal (e.g. a missing optional tool).
//   - RecoverAbort  — fail the step and abort the run (the old default behaviour).
//
// text is optional guidance, used only by RecoverResume.
func (r *Run) Recover(stepID, action, text string) {
	r.inbox <- recoverMsg{stepID: stepID, action: action, text: text}
}

// ResolveIntegration delivers a human decision for a step parked in
// step.StatusAwaitingIntegration after a squash-merge conflict (spec 06 A2).
// abort=false finishes the integration from the operator-resolved run worktree;
// abort=true fails the step, routing it to the recovery gate.
func (r *Run) ResolveIntegration(stepID string, abort bool) {
	r.inbox <- resolveIntegrationMsg{stepID: stepID, abort: abort}
}

// ResolveIntegrationWithAgent asks the configured resolver to prepare a
// proposal in the conflicted run worktree. Finalization remains a separate,
// explicit ResolveIntegration call after the operator reviews the result.
func (r *Run) ResolveIntegrationWithAgent(stepID string) {
	r.inbox <- resolveIntegrationWithAgentMsg{stepID: stepID}
}

// FinalMerge delivers the operator's decision for the final-merge gate (spec 06
// A3): approve=true lands the run branch onto the base working branch; approve=
// false discards it, leaving the run branch in place. Either way the run settles
// afterward (no resume).
func (r *Run) FinalMerge(approve bool) {
	r.inbox <- finalMergeMsg{approve: approve}
}

// Snapshot returns a point-in-time view of the run's state. For a live run
// the request routes through the scheduler's inbox (single-writer invariant).
// For a completed run it returns the cached final snapshot immediately,
// avoiding a deadlock on the no-longer-running scheduler goroutine.
func (r *Run) Snapshot() RunSnapshot {
	// Fast path: scheduler already exited and finalSnap is ready.
	select {
	case <-r.done:
		return r.finalSnap
	default:
	}
	// Slow path: ask the live scheduler. Also select on done in case the
	// scheduler exits between the fast-path check and the inbox send.
	reply := make(chan RunSnapshot, 1)
	select {
	case r.inbox <- snapshotReqMsg{reply: reply}:
		select {
		case snap := <-reply:
			return snap
		case <-r.done:
			return r.finalSnap
		}
	case <-r.done:
		return r.finalSnap
	}
}

// Recovery actions delivered via Run.Recover for a step parked in
// step.StatusAwaitingRecovery.
const (
	RecoverRetry  = "retry"  // re-run fresh (new session, full prompt)
	RecoverResume = "resume" // resume the failed agent session with the error + guidance
	RecoverSkip   = "skip"   // accept the failure and continue past it (like on_failure="continue")
	RecoverAbort  = "abort"  // fail the step and abort the run
)
