package tui

import (
	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/tui/detail"
	"jig/internal/tui/monitor"
	"jig/internal/tui/palette"
	"jig/internal/tui/runs"
	"jig/internal/tui/selector"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

// clipboardStateProbe is exported through this package's internal tests to
// inspect notice/busy state without depending on the private struct name.
var _ = clipboardState{}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m2, cmd, ok := m.handleGlobalKey(msg); ok {
			return m2, cmd
		}

	case tea.WindowSizeMsg:
		return m.updateWindowSize(msg)

	// ── engine events ─────────────────────────────────────────────────────
	case monitor.EngineEventMsg:
		return m.updateEngineEvent(msg)

	// The monitor's live-clock tick is routed unconditionally (like engine events)
	// so the loop keeps advancing even while the user is on another screen; the
	// monitor stops re-arming it once no step is running.
	case monitor.TickMsg:
		var mc tea.Cmd
		m.monitor, mc = m.monitor.Update(msg)
		return m, mc

	// ── navigation ────────────────────────────────────────────────────────
	case selector.ShowDetailMsg:
		// Standalone selector emit (tests); Home uses openDetailOverlay instead.
		return m.openDetailOverlay(msg.Path)

	case detail.BackMsg:
		if m.showDetailOverlay {
			m.showDetailOverlay = false
			return m, nil
		}
		m.active = screenHome
		return m, nil

	case detail.ShowRunsMsg:
		// Legacy detail→runs: keep filtering Home's runs pane and stay on Home.
		m.runs = m.runs.WithWorkflowContext(msg.Workflow, msg.Wf)
		m.homeFocus = homeRuns
		m.showDetailOverlay = false
		m.active = screenHome
		return m, nil

	case runs.BackMsg:
		m.homeFocus = homeWorkflows
		m.active = screenHome
		return m, nil

	case monitor.RequestLeaveConfirmMsg:
		m.leaveConfirm = true
		return m, nil

	case monitor.ShowHomeMsg:
		m.active = screenHome
		m.showDetailOverlay = false
		m.leaveConfirm = false
		return m, nil

	// Backward-compatible alias: older monitor leave still typed ShowRunsMsg.
	case monitor.ShowRunsMsg:
		m.active = screenHome
		m.showDetailOverlay = false
		m.leaveConfirm = false
		return m, nil

	case runsHydratedMsg:
		m.runs = m.runs.Hydrate(msg.runs)
		return m, nil

	case homeWorkflowLoadedMsg:
		return m.updateHome(msg)

	case runResumedMsg:
		if msg.err != nil {
			m.runs = m.runs.SetNotice(msg.err.Error())
			m.active = screenHome
			m.homeFocus = homeRuns
			return m, nil
		}
		m.handles[msg.runID] = msg.run
		m.runs = m.runs.MarkLive(msg.runID)
		m.monitor = monitor.New(msg.runID).WithPrefs(m.manager.Root())
		m.monitor.RunDir = m.manager.RunDir(msg.runID)
		m.monitor = m.monitor.WithJournal(msg.events)
		m.monitor.SetRun(msg.run)
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.monitor = m.monitor.FocusPendingInput()
		m.active = screenMonitor
		return m, m.monitor.EnsureFrame()

	case runs.ShowMonitorMsg:
		return m.openMonitor(msg.RunID)

	// ── monitor responses (forwarded to the run handle) ───────────────────
	case monitor.ReviewVerdictMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Resolve(msg.StepID, msg.Verdict)
		}
		return m, nil

	case monitor.ReviewSubmissionMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ResolveReview(msg.StepID, msg.Submission)
		}
		return m, nil

	case monitor.AgentInputMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.SendInput(msg.StepID, msg.Text)
		}
		return m, nil

	case monitor.AgentQuestionResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.AnswerQuestion(msg.StepID, msg.Response)
		}
		return m, nil

	case monitor.RecoverResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Recover(msg.StepID, msg.Action, msg.Text)
		}
		return m, nil

	case monitor.ResolveIntegrationResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ResolveIntegration(msg.StepID, msg.Abort)
		}
		return m, nil

	case monitor.ResolveIntegrationWithAgentMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ResolveIntegrationWithAgent(msg.StepID)
		}
		return m, nil

	case monitor.FinalMergeResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.FinalMerge(msg.Approve)
		}
		return m, nil

	case monitor.StopStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Stop(msg.StepID)
		}
		return m, nil

	case monitor.ResumeStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Resume(msg.StepID, msg.Message)
		}
		return m, nil

	case monitor.RequestResetMsg:
		return m.handleResetRequest(msg)

	case monitor.ResetStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Reset(msg.StepID)
		}
		return m, nil

	case monitor.UserInputResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ProvideUserInput(msg.StepID, msg.As, msg.Text)
		}
		return m, nil

	case runs.RequestDeleteMsg:
		m.confirmDelete = true
		m.pendingDeleteID = msg.RunID
		return m, nil

	case detail.StartRunMsg:
		return m.startRun(msg.Wf)

	case runs.StartRunMsg:
		return m.startRun(msg.Wf)

	case runs.ResumeRunMsg:
		return m, resumeRunCmd(m.manager, msg.RunID)

	// ── clipboard ────────────────────────────────────────────────────────
	// A ClipboardRequest is admitted here before any active-screen handler
	// runs, so navigation cannot orphan a pending request nor let a stale
	// completion overwrite a newer one.
	case shared.ClipboardRequest:
		return m.admitClipboard(msg)

	case clipboardResultMsg:
		return m.completeClipboard(msg)

	case clipboardImmediateNoticeMsg:
		return m.applyImmediateNotice(msg)

	case clipboardNoticeMsg:
		return m, nil
	}

	// All other messages go to the active screen.
	var cmd tea.Cmd
	switch m.active {
	case screenHome:
		m, cmd = m.updateHome(msg)
	case screenMonitor:
		m.monitor, cmd = m.monitor.Update(msg)
	}
	return m, cmd
}

// handleGlobalKey intercepts quit and the help modal before any screen sees the
// key. Returns (model, cmd, true) when the key is consumed; (m, nil, false)
// when it should fall through to the active screen's Update.
func (m rootModel) handleGlobalKey(msg tea.KeyPressMsg) (rootModel, tea.Cmd, bool) {
	if keybind.Matches(msg, shared.KeyQuit) {
		return m, tea.Quit, true
	}
	// Dirty-leave confirm (0.4 A6): y leaves to Home; anything else cancels.
	if m.leaveConfirm {
		switch msg.String() {
		case "y":
			m.leaveConfirm = false
			m.monitor = m.monitor.DiscardDirtyCompose()
			m.active = screenHome
			m.showDetailOverlay = false
		default:
			m.leaveConfirm = false
		}
		return m, nil, true
	}
	// Delete-confirm is a blocking modal: it swallows every key except y/n/esc.
	// Checked before help so "?" is also swallowed while the confirm is open.
	if m.confirmDelete {
		switch msg.String() {
		case "y":
			m, cmd := m.handleDeleteConfirmed()
			return m, cmd, true
		default:
			m.confirmDelete = false
			m.pendingDeleteID = ""
		}
		return m, nil, true
	}
	// Palette owns its keys while open (filter / navigate / run / esc).
	if m.palette.Open() {
		var cmd tea.Cmd
		m.palette, cmd, _ = m.palette.Update(msg)
		return m, cmd, true
	}
	// Help owns its scrolling keys while open, so the screen underneath cannot
	// move. F1 opens it during text capture without stealing a printable "?".
	if m.showHelp {
		sections := m.activeProvider().helpSections()
		page := shared.HelpOverlayPageSize(m.width, m.height, sections)
		switch {
		case keybind.Matches(msg, shared.KeyHelp),
			keybind.Matches(msg, shared.KeyHelpTyping),
			msg.String() == "esc":
			m.showHelp = false
			m.helpOffset = 0
		case msg.String() == "up" || msg.String() == "k":
			m.helpOffset = max(m.helpOffset-1, 0)
		case msg.String() == "down" || msg.String() == "j":
			m.helpOffset = min(m.helpOffset+1, shared.HelpOverlayMaxOffset(m.width, m.height, sections))
		case msg.String() == "pgup":
			m.helpOffset = max(m.helpOffset-page, 0)
		case msg.String() == "pgdown":
			m.helpOffset = min(m.helpOffset+page, shared.HelpOverlayMaxOffset(m.width, m.height, sections))
		case msg.String() == "home":
			m.helpOffset = 0
		case msg.String() == "end":
			m.helpOffset = shared.HelpOverlayMaxOffset(m.width, m.height, sections)
		}
		return m, nil, true
	}
	capturesText := m.activeProvider().capturesText()
	if (!capturesText && keybind.Matches(msg, shared.KeyHelp)) ||
		(capturesText && keybind.Matches(msg, shared.KeyHelpTyping)) {
		m.showHelp = true
		m.helpOffset = 0
		return m, nil, true
	}
	if !capturesText && keybind.Matches(msg, shared.KeyPalette) {
		m.palette = m.palette.Show(m.paletteCommands())
		return m, nil, true
	}
	return m, nil, false
}

// paletteCommands builds the currently-enabled action catalog from the active
// screen's palette sections (full catalog even when Monitor simple mode hides
// advanced bindings from the footer/help overlay).
func (m rootModel) paletteCommands() []palette.Command {
	var out []palette.Command
	for _, sec := range m.activeProvider().paletteSections() {
		out = append(out, palette.FromBindings(sec.Title, sec.Bindings)...)
	}
	return out
}

// handleDeleteConfirmed executes a confirmed run deletion: removes the row
// from the runs list, cancels the run if it is still live (deferring the
// directory delete until RunFinished arrives), or deletes the directory
// immediately for a finished run. If the monitor is currently showing the
// deleted run, navigates back to Home.
func (m rootModel) handleDeleteConfirmed() (rootModel, tea.Cmd) {
	m.confirmDelete = false
	runID := m.pendingDeleteID
	m.pendingDeleteID = ""

	m.runs = m.runs.DeleteRun(runID)

	if m.monitor.RunID == runID {
		m.active = screenHome
	}

	if run, ok := m.handles[runID]; ok {
		run.Cancel()
		m.pendingDeletions[runID] = true
		delete(m.handles, runID)
	} else {
		datastore.DeleteRun(m.manager.Root(), runID)
	}
	return m, nil
}

func (m rootModel) updateWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	var sc, dc, rc, mc tea.Cmd
	m.selector, sc = m.selector.Update(msg)
	if m.detail.Ready || m.showDetailOverlay {
		m.detail, dc = m.detail.Update(msg)
	}
	m.runs, rc = m.runs.Update(msg)
	m.monitor, mc = m.monitor.Update(msg)
	m = m.sizeHomeChildren()
	if m.showHelp {
		m.helpOffset = min(m.helpOffset, shared.HelpOverlayMaxOffset(m.width, m.height, m.activeProvider().helpSections()))
	}
	return m, tea.Batch(sc, dc, rc, mc)
}

func (m rootModel) updateEngineEvent(msg monitor.EngineEventMsg) (tea.Model, tea.Cmd) {
	var rc, mc tea.Cmd
	m.runs, rc = m.runs.Update(msg)
	m.monitor, mc = m.monitor.Update(msg)
	var rearm tea.Cmd
	if msg.IsLive {
		rearm = waitForLiveEventCmd(m.liveEvents)
	} else {
		rearm = waitForCtrlEventCmd(m.ctrlEvents)
	}
	// Delete the run directory once the scheduler goroutine has stopped writing
	// to it — signalled by RunFinished for a run in the pending-deletions set.
	if fin, ok := msg.Event.(engine.RunFinished); ok {
		if m.pendingDeletions[fin.RunID] {
			datastore.DeleteRun(m.manager.Root(), fin.RunID)
			delete(m.pendingDeletions, fin.RunID)
		}
	}
	return m, tea.Batch(rearm, rc, mc)
}

// openMonitor navigates to the monitor for runID, seeding it from a live
// snapshot or replayed journal when first opened. Returning to the same run
// preserves the existing monitor state so events that arrived off-screen are
// already reflected.
func (m rootModel) openMonitor(runID string) (tea.Model, tea.Cmd) {
	if m.monitor.RunID != runID {
		m.monitor = monitor.New(runID).WithPrefs(m.manager.Root())
		// RunDir lets the monitor read per-step transcripts from disk. Set it
		// before WithSnapshot so it preserves it.
		m.monitor.RunDir = m.manager.RunDir(runID)
		// Seed with a snapshot so already-completed or in-progress steps show up
		// immediately. A run from an earlier session has no handle, so fall back to
		// replaying its journal — the same events a Snapshot would carry.
		if run, ok := m.handles[runID]; ok {
			m.monitor = m.monitor.WithSnapshot(run.Snapshot())
			m.monitor.SetRun(run)
		} else if evs, err := engine.ReplayJournal(m.monitor.RunDir); err == nil && len(evs) > 0 {
			m.monitor = m.monitor.WithJournal(evs)
		}
	}
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.monitor = m.monitor.FocusPendingInput()
	m.active = screenMonitor
	// A run seeded from a snapshot/journal may already have a step running (or the
	// prior frame loop fell silent while off-screen); restart the live clock.
	return m, m.monitor.EnsureFrame()
}

func (m rootModel) handleResetRequest(msg monitor.RequestResetMsg) (tea.Model, tea.Cmd) {
	run, ok := m.handles[msg.RunID]
	if !ok {
		return m, nil
	}
	closure := run.ClosureOf(msg.StepID)
	if len(closure) <= 1 {
		// Linear tip: reset immediately, no confirmation needed.
		run.Reset(msg.StepID)
		return m, nil
	}
	// Mid-graph reset: ask the monitor to show the confirmation gate entry.
	var monCmd tea.Cmd
	m.monitor, monCmd = m.monitor.Update(monitor.ShowResetConfirmMsg{
		RunID:   msg.RunID,
		StepID:  msg.StepID,
		Closure: closure,
	})
	return m, monCmd
}

func (m rootModel) startRun(wf *workflow.Workflow) (tea.Model, tea.Cmd) {
	if wf == nil {
		return m, nil
	}
	run, err := m.manager.Start(wf)
	if err != nil {
		return m, nil
	}
	m.handles[run.ID] = run
	m.runs = m.runs.WithWorkflow(wf)
	// Navigate straight to the monitor so prompts and review gates are visible immediately.
	m.monitor = monitor.New(run.ID).WithPrefs(m.manager.Root())
	m.monitor.RunDir = m.manager.RunDir(run.ID)
	m.monitor.SetRun(run)
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.monitor = m.monitor.FocusPendingInput()
	m.active = screenMonitor
	m.showDetailOverlay = false
	return m, nil
}
