// Package tui implements jig's Bubble Tea program. It opens on a workflow
// picker that scans .agents/jig for workflow files; selecting one shows a
// read-only view of its steps and validation status. Pressing r in the detail
// view starts a dry run using the FakeExecutor and opens the run monitor.
package tui

import (
	"context"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/workflow"
)

// screen identifies which sub-model is currently driving the UI.
type screen int

const (
	screenSelector screen = iota
	screenDetail
	screenRuns
	screenMonitor
)

// ── messages ─────────────────────────────────────────────────────────────────

// showDetailMsg asks the root to open the detail screen for a workflow file.
type showDetailMsg struct{ path string }

// backToSelectorMsg asks the root to return to the selector.
type backToSelectorMsg struct{}

// startRunMsg asks the root to start a new run for the given workflow.
type startRunMsg struct{ wf *workflow.Workflow }

// showRunsMsg asks the root to switch to the runs list.
type showRunsMsg struct{}

// showMonitorMsg asks the root to open the monitor for a specific run.
type showMonitorMsg struct{ runID string }

// runsHydratedMsg carries runs recovered from disk at startup, one seq-ordered
// event slice per run (oldest first), for the runs list to fold in. It is
// produced once by hydrateRunsCmd so a fresh session shows runs from earlier
// sessions instead of an empty list.
type runsHydratedMsg struct{ runs [][]engine.Event }

// reviewVerdictMsg is emitted by the monitor when the user selects a verdict
// for a review step. The root delivers it to the run via Run.Resolve.
type reviewVerdictMsg struct {
	runID   string
	stepID  string
	verdict string
}

// userInputResponseMsg is emitted by the monitor when the user submits text
// for a from="user" input. The root delivers it via Run.ProvideUserInput.
type userInputResponseMsg struct {
	runID  string
	stepID string
	as     string
	text   string
}

// reviewMessageMsg is emitted by the monitor when the user submits a free-text
// message to the reviewed agent step. The root delivers it via Run.Message.
type reviewMessageMsg struct {
	runID  string
	stepID string
	text   string
}

// agentInputMsg is emitted by the monitor when the user submits a response to
// an agent step blocked by block_on. The root delivers it via Run.SendInput.
type agentInputMsg struct {
	runID  string
	stepID string
	text   string
}

// agentQuestionResponseMsg is emitted by the monitor when the user answers an
// AskUserQuestion call. The root delivers it via Run.AnswerQuestion.
type agentQuestionResponseMsg struct {
	runID     string
	stepID    string
	toolUseID string
	answer    string
}

// recoverResponseMsg is emitted by the monitor when the user picks a recovery
// action for a step parked in awaiting_recovery. The root delivers it via
// Run.Recover. action is engine.RecoverRetry / RecoverResume / RecoverAbort;
// text is optional guidance for the resume path.
type recoverResponseMsg struct {
	runID  string
	stepID string
	action string
	text   string
}

// resolveIntegrationResponseMsg is emitted by the monitor when the user resolves
// or aborts a step parked on an integration conflict. The root delivers it via
// Run.ResolveIntegration. abort=false finishes the merge; abort=true fails the step.
type resolveIntegrationResponseMsg struct {
	runID  string
	stepID string
	abort  bool
}

// finalMergeResponseMsg is emitted by the monitor when the user answers the
// final-merge gate. The root delivers it via Run.FinalMerge: approve lands the
// run branch onto the base; discard leaves the run branch in place.
type finalMergeResponseMsg struct {
	runID   string
	approve bool
}

// stopStepMsg is emitted by the monitor when the user presses the stop key on
// a running step. The root delivers it via Run.Stop (spec 07 B1 / spec 08 C4).
type stopStepMsg struct {
	runID  string
	stepID string
}

// resumeStepMsg is emitted by the monitor when the user presses the resume key
// on a stopped step. The root delivers it via Run.Resume (spec 07 B2 / spec 08 C4).
type resumeStepMsg struct {
	runID   string
	stepID  string
	message string
}

// requestResetMsg is emitted by the monitor when the user presses the reset key
// on a terminal/stopped step. The root resolves the closure via Run.ClosureOf
// and either confirms immediately (empty downstream) or shows a confirmation.
type requestResetMsg struct {
	runID  string
	stepID string
}

// resetStepMsg is emitted after the user confirms a reset (or immediately when
// the closure has no downstream steps). The root delivers it via Run.Reset.
type resetStepMsg struct {
	runID  string
	stepID string
}

// showResetConfirmMsg is emitted by the root to the monitor when the closure
// has downstream steps, asking the monitor to show the confirmation gate entry.
type showResetConfirmMsg struct {
	runID   string
	stepID  string
	closure []string // all steps that will be reset (incl. target)
}

// engineEventMsg wraps one engine.Event for delivery as a tea.Msg.
// isLive distinguishes which channel the event arrived on so the root can
// re-arm the correct drain loop after processing.
type engineEventMsg struct {
	event  engine.Event
	isLive bool
}

// ── root model ───────────────────────────────────────────────────────────────

type rootModel struct {
	active   screen
	selector selectorModel
	detail   detailModel
	runs     runsModel
	monitor  monitorModel

	ctx        context.Context
	manager    *engine.Manager
	liveEvents <-chan engine.Event
	ctrlEvents <-chan engine.Event
	handles    map[string]*engine.Run // runID → Run handle, for Snapshot()

	// showHelp toggles the global "?" modal overlay (see help.go). It is owned by
	// the root so the same chord works on every screen; while set, the root
	// composites the overlay over the active screen and swallows all other keys.
	showHelp bool

	width  int
	height int
}

// activeProvider returns the help sections + text-capture state of the screen
// currently driving the UI.
func (m rootModel) activeProvider() helpProvider {
	switch m.active {
	case screenDetail:
		return m.detail
	case screenRuns:
		return m.runs
	case screenMonitor:
		return m.monitor
	default:
		return m.selector
	}
}

// New returns jig's root TUI model. mgr is the engine manager; it must be
// non-nil. The theme is dark-only, so no terminal-background detection is needed.
func New(ctx context.Context, mgr *engine.Manager) tea.Model {
	live, ctrl := mgr.Subscribe()
	return rootModel{
		active:     screenSelector,
		selector:   newSelectorModel(),
		runs:       newRunsModel(),
		ctx:        ctx,
		manager:    mgr,
		liveEvents: live,
		ctrlEvents: ctrl,
		handles:    make(map[string]*engine.Run),
	}
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(
		discoverWorkflowsCmd(workflowsDir),
		hydrateRunsCmd(m.manager),
		waitForLiveEventCmd(m.liveEvents),
		waitForCtrlEventCmd(m.ctrlEvents),
	)
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if keybind.Matches(msg, keyQuit) {
			return m, tea.Quit
		}
		// Help is a global modal owned by the root. While open it swallows every
		// key except its own toggle and esc, so nothing fires on the screen behind
		// it. When closed, "?" opens it — unless the active screen is capturing free
		// text, where "?" is a literal character.
		if m.showHelp {
			if keybind.Matches(msg, keyHelp) || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}
		if keybind.Matches(msg, keyHelp) && !m.activeProvider().capturesText() {
			m.showHelp = true
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		var sc, dc, rc, mc tea.Cmd
		m.selector, sc = m.selector.Update(msg)
		if m.detail.ready {
			m.detail, dc = m.detail.Update(msg)
		}
		m.runs, rc = m.runs.Update(msg)
		m.monitor, mc = m.monitor.Update(msg)
		return m, tea.Batch(sc, dc, rc, mc)

	// ── engine events ─────────────────────────────────────────────────────
	case engineEventMsg:
		var rc, mc tea.Cmd
		m.runs, rc = m.runs.Update(msg)
		m.monitor, mc = m.monitor.Update(msg)
		var rearm tea.Cmd
		if msg.isLive {
			rearm = waitForLiveEventCmd(m.liveEvents)
		} else {
			rearm = waitForCtrlEventCmd(m.ctrlEvents)
		}
		return m, tea.Batch(rearm, rc, mc)

	// The monitor's live-clock tick is routed unconditionally (like engine events)
	// so the loop keeps advancing even while the user is on another screen; the
	// monitor stops re-arming it once no step is running.
	case monitorTickMsg:
		var mc tea.Cmd
		m.monitor, mc = m.monitor.Update(msg)
		return m, mc

	// ── navigation ────────────────────────────────────────────────────────
	case showDetailMsg:
		m.detail = newDetailModel(msg.path)
		var sizeCmd tea.Cmd
		m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenDetail
		return m, tea.Batch(sizeCmd, m.detail.Init())

	case backToSelectorMsg:
		switch m.active {
		case screenDetail:
			m.active = screenSelector
		case screenRuns:
			// Go back to whatever workflow's detail we came from; if none, selector.
			if m.detail.loaded {
				m.active = screenDetail
			} else {
				m.active = screenSelector
			}
		default:
			m.active = screenSelector
		}
		return m, nil

	case showRunsMsg:
		m.active = screenRuns
		return m, nil

	case runsHydratedMsg:
		m.runs = m.runs.hydrate(msg.runs)
		return m, nil

	case showMonitorMsg:
		// Preserve monitor state when returning to the same run — events that
		// arrived while on other screens are already reflected, and we avoid
		// an unnecessary Snapshot() call.
		if m.monitor.runID != msg.runID {
			m.monitor = newMonitorModel(msg.runID)
			// runDir lets the monitor read per-step transcripts from disk. Set it
			// before withSnapshot so it preserves it.
			m.monitor.runDir = m.manager.RunDir(msg.runID)
			// Seed with a snapshot so already-completed or in-progress steps
			// show up immediately. Snapshot() is safe for completed runs. A run
			// from an earlier session has no handle, so fall back to replaying its
			// journal from disk — the same events a Snapshot would carry.
			if run, ok := m.handles[msg.runID]; ok {
				snap := run.Snapshot()
				m.monitor = m.monitor.withSnapshot(snap)
			} else if evs, err := engine.ReplayJournal(m.monitor.runDir); err == nil && len(evs) > 0 {
				m.monitor = m.monitor.withJournal(evs)
			}
		}
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenMonitor
		// A run seeded from a snapshot/journal may already have a step running (or the
		// prior frame loop fell silent while off-screen); restart the live clock.
		return m, m.monitor.ensureFrame()

	case reviewVerdictMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Resolve(msg.stepID, msg.verdict)
		}
		return m, nil

	case reviewMessageMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Message(msg.stepID, msg.text)
		}
		return m, nil

	case agentInputMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.SendInput(msg.stepID, msg.text)
		}
		return m, nil

	case agentQuestionResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.AnswerQuestion(msg.stepID, msg.toolUseID, msg.answer)
		}
		return m, nil

	case recoverResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Recover(msg.stepID, msg.action, msg.text)
		}
		return m, nil

	case resolveIntegrationResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.ResolveIntegration(msg.stepID, msg.abort)
		}
		return m, nil

	case finalMergeResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.FinalMerge(msg.approve)
		}
		return m, nil

	case stopStepMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Stop(msg.stepID)
		}
		return m, nil

	case resumeStepMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Resume(msg.stepID, msg.message)
		}
		return m, nil

	case requestResetMsg:
		run, ok := m.handles[msg.runID]
		if !ok {
			return m, nil
		}
		closure := run.ClosureOf(msg.stepID)
		if len(closure) <= 1 {
			// Linear tip: reset immediately, no confirmation needed.
			run.Reset(msg.stepID)
			return m, nil
		}
		// Mid-graph reset: ask the monitor to show the confirmation gate entry.
		var monCmd tea.Cmd
		m.monitor, monCmd = m.monitor.Update(showResetConfirmMsg{
			runID:   msg.runID,
			stepID:  msg.stepID,
			closure: closure,
		})
		return m, monCmd

	case resetStepMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Reset(msg.stepID)
		}
		return m, nil

	case userInputResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.ProvideUserInput(msg.stepID, msg.as, msg.text)
		}
		return m, nil

	case startRunMsg:
		if msg.wf == nil {
			return m, nil
		}
		run, err := m.manager.Start(msg.wf)
		if err != nil {
			return m, nil
		}
		m.handles[run.ID] = run
		m.runs = m.runs.withWorkflow(msg.wf)
		// Navigate straight to the monitor so prompts and review gates are visible immediately.
		m.monitor = newMonitorModel(run.ID)
		m.monitor.runDir = m.manager.RunDir(run.ID)
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenMonitor
		return m, nil
	}

	// All other messages go to the active screen.
	var cmd tea.Cmd
	switch m.active {
	case screenSelector:
		m.selector, cmd = m.selector.Update(msg)
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	case screenRuns:
		m.runs, cmd = m.runs.Update(msg)
	case screenMonitor:
		m.monitor, cmd = m.monitor.Update(msg)
	}
	return m, cmd
}

func (m rootModel) View() tea.View {
	var content string
	switch m.active {
	case screenDetail:
		content = m.detail.View()
	case screenRuns:
		content = m.runs.View()
	case screenMonitor:
		content = m.monitor.View()
	default:
		content = m.selector.View()
	}
	// The help overlay is a global modal: composite it over the active screen (via
	// a lipgloss Canvas) so the screen shows through around the box, and the same
	// "?" chord surfaces context-appropriate keys everywhere.
	if m.showHelp {
		content = renderHelpOverlay(content, m.width, m.height, m.activeProvider().helpSections())
	}
	// v2 declares alt-screen and the full-screen background on the View itself
	// (the compositor paints BackgroundColor edge-to-edge, so nested styled
	// spans no longer punch holes in a screen-wide background — the reason the
	// Pepper canvas was blocked on v1). theme.Canvas is Charmtone Pepper.
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor = theme.Canvas
	return v
}

// hydrateRunsCmd reads the runs persisted on disk and replays each journal into
// its event stream, off the UI goroutine, so the runs list can show runs from
// earlier sessions at startup. It emits one runsHydratedMsg; runs whose journal
// is missing or undecodable are simply omitted. When persistence is off, the run
// list is empty and the message carries nothing.
func hydrateRunsCmd(mgr *engine.Manager) tea.Cmd {
	return func() tea.Msg {
		ids, err := mgr.PersistedRuns()
		if err != nil || len(ids) == 0 {
			return runsHydratedMsg{}
		}
		runs := make([][]engine.Event, 0, len(ids))
		for _, id := range ids {
			evs, err := engine.ReplayJournal(mgr.RunDir(id))
			if err != nil || len(evs) == 0 {
				continue
			}
			runs = append(runs, evs)
		}
		return runsHydratedMsg{runs: runs}
	}
}

// waitForLiveEventCmd drains one event from the live (liveness-signal) channel.
// The root re-arms it after each delivery, keeping a permanent drain loop running.
func waitForLiveEventCmd(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return engineEventMsg{event: e, isLive: true}
	}
}

// waitForCtrlEventCmd drains one event from the ctrl (critical-control) channel.
// The root re-arms it after each delivery, keeping a permanent drain loop running.
func waitForCtrlEventCmd(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return engineEventMsg{event: e, isLive: false}
	}
}
