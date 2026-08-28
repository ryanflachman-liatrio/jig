package detail

import "jig/internal/workflow"

// BackMsg is emitted when the user leaves the detail screen.
type BackMsg struct{}

// ShowRunsMsg asks the root to show runs for the selected workflow.
type ShowRunsMsg struct {
	Workflow string
	Wf       *workflow.Workflow
}

// StartRunMsg is emitted when the user presses r to run the current workflow.
type StartRunMsg struct{ Wf *workflow.Workflow }
