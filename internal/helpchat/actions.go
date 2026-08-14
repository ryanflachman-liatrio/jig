package helpchat

// Action types dispatched by MCP tool handlers via DispatchFunc.
// The monitor's DispatchedMsg handler converts these to the corresponding
// monitor.*Msg types before re-queuing through the root model.
// Keeping these in helpchat (not monitor) avoids the helpchat↔monitor import cycle.

type RecoverAction struct {
	StepID string
	Action string // "retry", "resume", "abort"
	Text   string // optional guidance for resume
}

type ResetAction struct {
	StepID string
}

type StopAction struct {
	StepID string
}

type ResumeAction struct {
	StepID  string
	Message string
}

type ReviewVerdict struct {
	StepID  string
	Verdict string
}

type ReviewMessage struct {
	StepID string
	Text   string
}
