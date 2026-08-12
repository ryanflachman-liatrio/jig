package runs

import (
	"time"

	"charm.land/bubbles/v2/viewport"

	"jig/internal/step"
	"jig/internal/workflow"
)

// Model is the run-list screen: one row per active/completed run, updated in
// real-time as engine events arrive.
type Model struct {
	rows   []runRow
	index  map[string]int // runID → rows position
	cursor int
	wf     *workflow.Workflow // held for StartRunMsg when the user presses r

	keys  runsKeys
	vp    viewport.Model
	ready bool

	width  int
	height int
}

type runRow struct {
	id       string
	workflow string
	statuses map[string]step.Status // stepID → current status
	total    int
	done     bool
	failed   bool
	started  time.Time
}

func NewModel() Model {
	return Model{index: make(map[string]int), keys: defaultKeys()}
}

// WithWorkflow returns a copy of the model with the workflow set so the user
// can press r to start another run without returning to the detail screen.
func (m Model) WithWorkflow(wf *workflow.Workflow) Model {
	m.wf = wf
	return m
}
