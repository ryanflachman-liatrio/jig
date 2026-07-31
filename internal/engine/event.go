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
	RunID     string
	StepID    string
	From      step.Status
	To        step.Status
	Attempt   int
	Iteration int
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
type ReviewRequest struct {
	RunID   string
	StepID  string
	Choices []string
	Diff    string
}

// RunError is an engine-level failure, not a step-level failure.
type RunError struct {
	RunID string
	Err   string
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

func (RunStarted) isEvent()    {}
func (RunFinished) isEvent()   {}
func (StepStatus) isEvent()    {}
func (StepOutput) isEvent()    {}
func (StepToolCall) isEvent()  {}
func (StepMessage) isEvent()   {}
func (GateResult) isEvent()    {}
func (LoopFired) isEvent()     {}
func (ReviewRequest) isEvent() {}
func (RunError) isEvent()      {}
func (PromptRequest) isEvent() {}
