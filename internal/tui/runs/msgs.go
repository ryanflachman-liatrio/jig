package runs

import "jig/internal/workflow"

// BackMsg is emitted when the user leaves the runs screen.
type BackMsg struct{}

// ShowMonitorMsg is emitted when the user selects a run to open.
type ShowMonitorMsg struct{ RunID string }

// StartRunMsg is emitted when the user presses r to start another run.
type StartRunMsg struct{ Wf *workflow.Workflow }

// RequestDeleteMsg is emitted when the user presses d to delete a run.
// Root receives it, sets the confirm overlay, and handles the actual deletion.
type RequestDeleteMsg struct{ RunID string }
