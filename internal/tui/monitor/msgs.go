package monitor

import (
	"time"

	"jig/internal/engine"
	"jig/internal/interaction"
)

// ShowRunsMsg asks the root to switch to the runs list.
type ShowRunsMsg struct{}

// ReviewVerdictMsg is emitted by the monitor when the user selects a verdict
// for a review step. The root delivers it to the run via Run.Resolve.
type ReviewVerdictMsg struct {
	RunID   string
	StepID  string
	Verdict string
}

// UserInputResponseMsg is emitted by the monitor when the user submits text
// for a from="user" input. The root delivers it via Run.ProvideUserInput.
type UserInputResponseMsg struct {
	RunID  string
	StepID string
	As     string
	Text   string
}

// ReviewMessageMsg is emitted by the monitor when the user submits a free-text
// message to the reviewed agent step. The root delivers it via Run.Message.
type ReviewMessageMsg struct {
	RunID  string
	StepID string
	Text   string
}

// AgentInputMsg is emitted by the monitor when the user submits a response to
// an agent step blocked by block_on. The root delivers it via Run.SendInput.
type AgentInputMsg struct {
	RunID  string
	StepID string
	Text   string
}

// AgentQuestionResponseMsg is emitted by the monitor when the user answers an
// AskUserQuestion call. The root delivers it via Run.AnswerQuestion.
type AgentQuestionResponseMsg struct {
	RunID    string
	StepID   string
	Response interaction.QuestionResponse
}

// RecoverResponseMsg is emitted by the monitor when the user picks a recovery
// action for a step parked in awaiting_recovery. The root delivers it via
// Run.Recover. Action is engine.RecoverRetry / RecoverResume / RecoverAbort;
// Text is optional guidance for the resume path.
type RecoverResponseMsg struct {
	RunID  string
	StepID string
	Action string
	Text   string
}

// ResolveIntegrationResponseMsg is emitted by the monitor when the user resolves
// or aborts a step parked on an integration conflict. The root delivers it via
// Run.ResolveIntegration. Abort=false finishes the merge; Abort=true fails the step.
type ResolveIntegrationResponseMsg struct {
	RunID  string
	StepID string
	Abort  bool
}

// FinalMergeResponseMsg is emitted by the monitor when the user answers the
// final-merge gate. The root delivers it via Run.FinalMerge: Approve lands the
// run branch onto the base; discard leaves the run branch in place.
type FinalMergeResponseMsg struct {
	RunID   string
	Approve bool
}

// StopStepMsg is emitted by the monitor when the user presses the stop key on
// a running step. The root delivers it via Run.Stop.
type StopStepMsg struct {
	RunID  string
	StepID string
}

// ResumeStepMsg is emitted by the monitor when the user presses the resume key
// on a stopped step. The root delivers it via Run.Resume.
type ResumeStepMsg struct {
	RunID   string
	StepID  string
	Message string
}

// RequestResetMsg is emitted by the monitor when the user presses the reset key
// on a terminal/stopped step. The root resolves the closure via Run.ClosureOf
// and either confirms immediately (empty downstream) or shows a confirmation.
type RequestResetMsg struct {
	RunID  string
	StepID string
}

// ResetStepMsg is emitted after the user confirms a reset (or immediately when
// the closure has no downstream steps). The root delivers it via Run.Reset.
type ResetStepMsg struct {
	RunID  string
	StepID string
}

// ShowResetConfirmMsg is sent by root to the monitor to display a confirmation
// gate entry when the closure has downstream steps.
type ShowResetConfirmMsg struct {
	RunID   string
	StepID  string
	Closure []string // all steps that will be reset (incl. target)
}

// EngineEventMsg wraps one engine.Event for delivery as a tea.Msg.
// IsLive distinguishes which channel the event arrived on so the root can
// re-arm the correct drain loop after processing.
type EngineEventMsg struct {
	Event  engine.Event
	IsLive bool
}

// TickMsg drives the frame loop (was monitorTickMsg in the tui package).
type TickMsg time.Time
