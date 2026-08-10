package engine

import (
	"context"

	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/workflow"
)

// Executor is defined in engine (the consumer) and implemented in runner.
// Dependency inversion keeps engine free of os/exec and SDK imports.
type Executor interface {
	Execute(ctx context.Context, req StepRequest, report Reporter) (*step.Result, error)
}

// StepRequest is the data a worker needs to execute one step.
// @ref inputs are resolved to paths / inlined values before dispatch.
type StepRequest struct {
	RunID    string
	Step     *workflow.Step
	Inputs   []ResolvedInput
	Feedback string // loop feedback step ID, when re-running via [step.loop]
	// WorkflowContext is the pre-rendered "Workflow context" preamble, prepended
	// at the front of the agent's single user turn. It is "" when there is none:
	// a non-agent step, or an agent step with inject_context off (Unit 4). An
	// empty value leaves the built prompt byte-identical to the pre-feature form.
	WorkflowContext string
	Worktree        string // "" when isolation = none (Phase 5+)
	ArtifactDir     string // absolute path under .jig/runs/<runID>/artifacts; "" disables file writes

	// TranscriptPath is the absolute path to this step's transcript.jsonl. When
	// non-empty the runner captures the full message stream there; "" disables
	// transcript writes (persistence off, e.g. tests with no run dir).
	TranscriptPath string
	// Iteration and Attempt tag every transcript entry so loop iterations and
	// retries (which append to the same file) can be distinguished on read.
	Iteration int
	Attempt   int

	// ResumeSessionID, when non-empty, causes the agent runner to resume the
	// given session (WithResume + WithContinueConversation) and use Message as
	// the query prompt instead of the freshly-built full prompt.
	ResumeSessionID string
	Message         string

	// Guard, when non-nil, activates the Tier-1 deterministic firewall for this
	// step. The runner registers a WithCanUseTool callback that calls Guard.Check
	// before each tool executes. The guard also forces PermissionModeDefault (the
	// only mode in which the SDK callback actually fires — acceptEdits bypasses it).
	Guard *sentinel.Guard
	// FindingsPath is the absolute path to the per-run findings.jsonl. When
	// non-empty the runner appends a Finding record for every blocked/escalated
	// tool call. "" disables persistence (persistence-off path).
	FindingsPath string
}

// ResolvedInput pairs an original workflow.Input with its resolved value.
type ResolvedInput struct {
	Ref   workflow.Input
	Value string // resolved path, or inlined content when Ref.Inline is true
}

// Reporter carries live signals out of an in-flight execution back to the
// scheduler, which tags them with run/step IDs and routes them through emit().
type Reporter interface {
	Output(delta string)
	ToolCall(tool, detail string)
	// Message signals that the step's transcript advanced by one entry (seq).
	// It carries no payload — content is written directly to the transcript
	// file by the runner; this is a liveness-only nudge for the TUI to refresh.
	Message(seq, iteration int)
	// Question delivers a dynamic AskUserQuestion from the agent to the scheduler
	// and blocks until the human provides an answer. Returns the user's answer text,
	// or "" if ctx is cancelled first (the step was stopped, the run was cancelled,
	// or a security finding escalated the step) — so the caller unwinds instead of
	// hanging forever.
	Question(ctx context.Context, toolUseID string, questions []AgentQuestionItem) string
	// Finding routes a SecurityFinding through the ctrl channel (must-not-drop).
	// Called by the runner when the Tier-1 guard or Tier-2 monitor fleet produces
	// a finding for this step.
	Finding(sf SecurityFinding)
}
