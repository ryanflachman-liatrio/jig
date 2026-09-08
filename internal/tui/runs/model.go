package runs

import (
	"time"

	"charm.land/bubbles/v2/viewport"

	"jig/internal/step"
	"jig/internal/tui/shared"
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
	id          string
	workflow    string
	statuses    map[string]step.Status // stepID → current status
	total       int
	done        bool
	failed      bool
	paused      bool // unfinished historical run (review and/or crash)
	interrupted bool // Spec 20: unfinished with running/validating workers
	started     time.Time
}

func NewModel() Model {
	return Model{index: make(map[string]int), keys: defaultKeys()}
}

// Keys exposes run-list bindings for Home's composed help/footer.
func (m Model) Keys() runsKeys { return m.keys }

// WorkflowName is the filter label shown in the Home Runs pane title.
func (m Model) WorkflowName() string { return m.workflowName }

// Workflow is the loaded definition used to start a new run from Home.
func (m Model) Workflow() *workflow.Workflow { return m.wf }

// SetPaneSize fits the viewport to a titled panel outer size (Home owns the
// shared footer, so no footer row is reserved here).
func (m Model) SetPaneSize(width, height int) Model {
	m.width, m.height = width, height
	hFrame, vFrame := shared.PanelFrame()
	w := width - hFrame
	h := height - vFrame
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
		m.ready = true
	} else {
		m.vp.SetWidth(w)
		m.vp.SetHeight(h)
	}
	return m.syncViewport()
}

// PaneBody is the run list (or empty CTA) for an embedded Home pane.
func (m Model) PaneBody(width, height int) string {
	m = m.SetPaneSize(width, height)
	if len(m.visibleRows()) == 0 {
		name := m.workflowName
		if name == "" {
			name = "this workflow"
		}
		return "  No runs yet for " + name + ".\n\n  Press r to start a run."
	}
	if m.ready {
		return m.vp.View()
	}
	return m.rowsBody()
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
		// A synthetic orphan row can have no workflow identity when both its
		// journal prefix and workflow snapshot are unavailable. Keep that
		// ownership record visible in filtered Home panes rather than losing it;
		// the row is terminal and therefore cannot offer Resume.
		if row.workflow == "" || row.workflow == m.workflowName {
			rows = append(rows, row)
		}
	}
	return rows
}
