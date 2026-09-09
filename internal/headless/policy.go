package headless

import (
	"fmt"

	"jig/internal/engine"
)

// Policy decides how to answer park-path events. Recovery/conflict defaults
// are abort; merge requires explicit flags (or --ci → discard).
type Policy struct {
	ApproveMerge bool
	DiscardMerge bool
	CI           bool

	// OnRecovery is abort|retry|skip (default abort). Empty → abort.
	OnRecovery string
	// OnConflict is abort only (default). Empty → abort.
	OnConflict string

	// awaitingApprove is set after FinalMerge(true) so a subsequent RunError
	// (approve conflict) can discard or fail-closed instead of hanging.
	awaitingApprove bool
	mergeHandled    bool
}

// Handle applies a native settle action or returns a typed GateError for
// unexpected human gates. Returning nil means "continue waiting for settle."
// The caller always waits for RunFinished after Cancel.
func (p *Policy) Handle(run *engine.Run, ev engine.Event) error {
	switch e := ev.(type) {
	case engine.ReviewRequest:
		return &GateError{
			Code:    "gate_review",
			Message: "human review required; headless fails closed",
			StepID:  e.StepID,
		}
	case engine.PromptRequest:
		return &GateError{
			Code:    "gate_prompt",
			Message: fmt.Sprintf("user prompt %q required; headless fails closed", e.Label),
			StepID:  e.StepID,
		}
	case engine.InputRequest:
		return &GateError{
			Code:    "gate_input",
			Message: "block_on input required; headless fails closed",
			StepID:  e.StepID,
		}
	case engine.AgentQuestion:
		return &GateError{
			Code:    "gate_question",
			Message: "AskUserQuestion required; headless fails closed (prefer profile = \"@autonomous\")",
			StepID:  e.StepID,
		}
	case engine.RecoveryRequest:
		if p.OnRecovery == RecoveryResume && !e.CanResume {
			return &GateError{Code: "gate_recovery_resume", Message: "recovery session cannot be resumed", StepID: e.StepID}
		}
		run.Recover(e.StepID, p.recoveryAction(), "")
		return nil
	case engine.IntegrationConflictRequest:
		// Abort → recovery cascade (Spec 19 D19). The ensuing RecoveryRequest
		// is handled above; do not claim conflict abort alone settles the run.
		// Headless only supports abort (no agent-then-human finalize); CLI
		// rejects other values before Run.
		run.ResolveIntegration(e.StepID, true)
		return nil
	case engine.FinalMergeRequest:
		return p.handleFinalMerge(run)
	case engine.RunError:
		return p.handleRunError(run, e)
	default:
		return nil
	}
}

func (p *Policy) recoveryAction() string {
	switch p.OnRecovery {
	case RecoveryRetry:
		return engine.RecoverRetry
	case RecoverySkip:
		return engine.RecoverSkip
	case RecoveryResume:
		return engine.RecoverResume
	default:
		return engine.RecoverAbort
	}
}

func (p *Policy) handleFinalMerge(run *engine.Run) error {
	if p.mergeHandled {
		return nil
	}
	switch {
	case p.ApproveMerge:
		p.mergeHandled = true
		p.awaitingApprove = true
		run.FinalMerge(true)
		return nil
	case p.DiscardMerge || p.CI:
		p.mergeHandled = true
		run.FinalMerge(false)
		return nil
	default:
		return &GateError{
			Code:    "gate_merge",
			Message: "final merge requires --approve-merge, --discard-merge, or --ci",
		}
	}
}

// handleRunError covers the approve-merge conflict path: FinalMerge(true) can
// emit RunError and stay parked. Headless must not hang like the TUI.
func (p *Policy) handleRunError(run *engine.Run, e engine.RunError) error {
	if !p.awaitingApprove {
		return nil // non-merge RunErrors ride to RunFinished / Failed
	}
	p.awaitingApprove = false
	if p.DiscardMerge || p.CI {
		run.FinalMerge(false)
		return nil
	}
	return &GateError{
		Code:    "gate_merge_conflict",
		Message: fmt.Sprintf("approve-merge failed (%s); pass --discard-merge or --ci to settle", e.Err),
	}
}
