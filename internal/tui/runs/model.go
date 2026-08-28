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
	rows         []runRow
	index        map[string]int // runID → rows position
	cursor       int
	wf           *workflow.Workflow // held for StartRunMsg when the user presses r
	workflowName string
	filtered     bool

	keys  runsKeys
	vp    viewport.Model
	ready bool

	width  int
	height int
	notice string
}

type runRow struct {
	id       string
	workflow string
	statuses map[string]step.Status // stepID → current status
	total    int
	done     bool
	failed   bool
	paused   bool
	started  time.Time
}

func NewModel() Model {
	return Model{index: make(map[string]int), keys: defaultKeys()}
}

// WithWorkflow returns a copy of the model with the workflow set so the user
// can press r to start another run without returning to the detail screen.
func (m Model) WithWorkflow(wf *workflow.Workflow) Model {
	if wf == nil {
		return m.WithWorkflowContext("", nil)
	}
	return m.WithWorkflowContext(wf.Meta.Name, wf)
}

// WithWorkflowContext scopes the visible list to one workflow while retaining
// other workflows' rows so switching detail screens does not require reloading
// persisted journals.
func (m Model) WithWorkflowContext(name string, wf *workflow.Workflow) Model {
	m.workflowName = name
	m.filtered = true
	m.wf = wf
	m.cursor = 0
	if m.ready {
		m.vp.GotoTop()
	}
	return m.syncViewport()
}

func (m Model) visibleRows() []runRow {
	if !m.filtered {
		return m.rows
	}
	rows := make([]runRow, 0, len(m.rows))
	for _, row := range m.rows {
		if row.workflow == m.workflowName {
			rows = append(rows, row)
		}
	}
	return rows
}
