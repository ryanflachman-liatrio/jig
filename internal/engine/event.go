package engine

import "jig/internal/step"

// Event is the sealed vocabulary of state transitions the engine emits.
// Each type carries exactly its own fields; consumers type-switch.
// This is the tea.Msg / go/ast.Node pattern — zero allocation for the common
// case and exhaustive compile-time checking in type switches.
type Event interface{ isEvent() }

// RunStarted is emitted once per run, before any step is dispatched.
type RunStarted struct {
	RunID    string
	Workflow string
	Steps    []string // step IDs in workflow order
}

// RunFinished is emitted when the run reaches a terminal state.
type RunFinished struct {
	RunID  string
	Failed bool
}

// StepStatus records a single status transition for one step.
type StepStatus struct {
	RunID      string
	StepID     string
	From       step.Status
	To         step.Status
	Attempt    int
	Iteration  int
	Generation int // manual re-run count (step.State.Generation); 0 on first run
	// Err carries the failure reason when To == step.StatusFailed — the step
	// Result's error, or the [step.validate] gate detail when a gate failed.
	// Empty for every non-failing transition.
	Err string
	// Subtype is the SDK ResultMessage.Subtype for agent steps that fail due to
	// a policy limit (e.g. "error_max_turns", "error_max_budget_usd"). Empty for
	// non-agent failures and for all non-failing transitions.
	Subtype string
}

// StepOutput carries a streaming text delta from an in-flight agent step.
type StepOutput struct {
	RunID  string
	StepID string
	Delta  string
}

// StepToolCall carries observed tool-call metadata from an agent step.
//
// Deprecated: tool calls are now captured in full (name + input) in the
// per-step transcript (see internal/transcript). StepMessage signals that the
// transcript advanced; the TUI reads content from disk. StepToolCall is kept
// for journal back-compat and may be removed once no consumer relies on it.
type StepToolCall struct {
	RunID  string
	StepID string
	Tool   string
	Detail string
}

// StepMessage is a lightweight liveness signal that an agent step's transcript
// advanced by one entry. It carries no bulk content — the full message (text,
// reasoning, tool calls, tool results) lives in the per-step transcript.jsonl
// (see internal/transcript). A dropped StepMessage only means "refresh is one
// seq stale," corrected on the next read; this is why content never rides the
// drop-on-full event bus.
type StepMessage struct {
	RunID     string
	StepID    string
	Seq       int // transcript entry seq this refers to
	Iteration int
}

// GateResult is emitted when a [step.validate] gate completes.
type GateResult struct {
	RunID  string
	StepID string
	Passed bool
	Detail string
}

// LoopFired is emitted when a [step.loop] back-edge triggers.
type LoopFired struct {
	RunID     string
	StepID    string
	Goto      string
	Iteration int
	Max       int
}

// ReviewRequest asks the human to pick from Choices for a review step.
// Diff is non-empty when the step declares review = "diff"; it contains the
// full git diff of mutating steps reachable through the dependency chain.
// AllowMessage is true when the reviewed target is an agent step and the
// per-gate message cap has not been exhausted — the TUI offers a [m] action.
type ReviewRequest struct {
	RunID        string
	StepID       string
	Choices      []string
	Diff         string
	AllowMessage bool
}

// InputRequest is emitted when an agent step's block_on condition evaluates to
// true after it completes. The TUI surfaces a compose box so the human can
// provide free-text input; the agent resumes its session and re-runs.
type InputRequest struct {
	RunID  string
	StepID string
}

// RunError is an engine-level failure, not a step-level failure.
type RunError struct {
	RunID string
	Err   string
}

// RecoveryRequest is emitted when a step fails unrecoverably and the run pauses
// for a human recovery decision instead of aborting. The step is parked in
// step.StatusAwaitingRecovery; the run and any in-flight sibling steps stay
// alive. The TUI surfaces Err and offers: retry (re-run fresh), resume (re-run
// the failed agent session with the error fed back in — only when CanResume),
// or abort the run. The decision is delivered via Run.Recover.
type RecoveryRequest struct {
	RunID  string
	StepID string
	Err    string
	// CanResume is true when the failed step has a resumable agent session id —
	// i.e. an agent step that ran (vs. a worktree/setup failure with no session).
	CanResume bool
}

// IntegrationConflictRequest is emitted when a step's squash-merge into the run
// branch conflicts with already-integrated work (spec 06 A2). The step is parked
// in step.StatusAwaitingIntegration; the run stays alive. Paths names the
// conflicted files the operator must resolve in the run worktree. The decision
// (resolve or abort) is delivered via Run.ResolveIntegration.
type IntegrationConflictRequest struct {
	RunID  string
	StepID string
	Paths  []string
}

// FinalMergeRequest is emitted at run end (all steps terminal) when the run
// branch carries at least one commit (spec 06 A3). It asks the human to land the
// run: approve merges RunBranch onto Base (the user's working branch); discard
// leaves the run branch in place for inspection and merges nothing. The decision
// is delivered via Run.FinalMerge. This gate is a pre-RunFinished completion
// step — the run has NOT emitted RunFinished yet and does not resume afterward.
type FinalMergeRequest struct {
	RunID     string
	RunBranch string
	Base      string
}

// PromptRequest asks the human to supply free-form text for a from="user" input.
// Multiple user inputs on one step are requested sequentially, each replacing
// the previous PromptRequest.
type PromptRequest struct {
	RunID  string
	StepID string
	Label  string
	As     string
}

// AgentQuestion is emitted when an in-flight agent step calls AskUserQuestion.
// The step transitions to StatusNeedsInput and waits for Run.AnswerQuestion.
type AgentQuestion struct {
	RunID     string
	StepID    string
	ToolUseID string
	Questions []AgentQuestionItem
}

type AgentQuestionItem struct {
	Header      string
	Question    string
	Options     []AgentQuestionOption
	MultiSelect bool
}

type AgentQuestionOption struct {
	Label       string
	Description string
}

// StepsReset is emitted when an operator resets a run to an earlier step
// (spec 08 C2/C3). It is an audit record of operator intent — the target step,
// the full closure that was invalidated, and the git commit the run branch was
// rewound to. State reconstruction relies on the journaled StepStatus
// transitions; StepsReset carries provenance the status stream cannot express
// and requires no state-changing fold handler (same design as LoopFired).
type StepsReset struct {
	RunID    string
	Target   string   // the step the operator chose to reset to
	Closure  []string // ordered reset set (target ∪ transitive dependents)
	RewindTo string   // git commit SHA the run branch was reset to
}

// SecurityFinding is emitted when the security layer (Tier-1 guard or Tier-2
// monitor fleet) produces a finding for a running step. It rides the ctrl
// channel (must-not-drop), not live, because a missed finding is not
// self-correcting the way a missed StepMessage is (see D4 in spec 10).
// The full finding detail is durable in findings.jsonl; this event carries
// only the fields needed to route a critical finding to the recovery gate.
type SecurityFinding struct {
	RunID       string `json:"run_id"`
	StepID      string `json:"step_id"`
	Tier        string `json:"tier"`     // "guard" | "monitor"
	Monitor     string `json:"monitor"`  // rule or monitor name
	Severity    string `json:"severity"` // "low" | "medium" | "high" | "critical"
	Action      string `json:"action"`   // "observed" | "blocked" | "escalated"
	Fingerprint string `json:"fingerprint"`
}

func (RunStarted) isEvent()                 {}
func (RunFinished) isEvent()                {}
func (StepStatus) isEvent()                 {}
func (StepOutput) isEvent()                 {}
func (StepToolCall) isEvent()               {}
func (StepMessage) isEvent()                {}
func (GateResult) isEvent()                 {}
func (LoopFired) isEvent()                  {}
func (ReviewRequest) isEvent()              {}
func (InputRequest) isEvent()               {}
func (RunError) isEvent()                   {}
func (RecoveryRequest) isEvent()            {}
func (IntegrationConflictRequest) isEvent() {}
func (FinalMergeRequest) isEvent()          {}
func (PromptRequest) isEvent()              {}
func (AgentQuestion) isEvent()              {}
func (StepsReset) isEvent()                 {}
func (SecurityFinding) isEvent()            {}
