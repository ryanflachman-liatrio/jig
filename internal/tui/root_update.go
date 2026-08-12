package tui

import (
	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/monitor"
	"jig/internal/tui/runs"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

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
	case showDetailMsg:
		return m.openDetail(msg.path)

	case backToSelectorMsg:
		// Only detail emits this; selector has no back action.
		m.active = screenSelector
		return m, nil

	case runs.BackMsg:
		// Go back to whatever workflow's detail we came from; if none, selector.
		if m.detail.loaded {
			m.active = screenDetail
		} else {
			m.active = screenSelector
		}
		return m, nil

	case monitor.ShowRunsMsg:
		m.active = screenRuns
		return m, nil

	case runsHydratedMsg:
		m.runs = m.runs.Hydrate(msg.runs)
		return m, nil

	case runs.ShowMonitorMsg:
		return m.openMonitor(msg.RunID)

	// ── monitor responses (forwarded to the run handle) ───────────────────
	case monitor.ReviewVerdictMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Resolve(msg.StepID, msg.Verdict)
		}
		return m, nil

	case monitor.ReviewMessageMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Message(msg.StepID, msg.Text)
		}
		return m, nil

	case monitor.AgentInputMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.SendInput(msg.StepID, msg.Text)
		}
		return m, nil

	case monitor.AgentQuestionResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.AnswerQuestion(msg.StepID, msg.ToolUseID, msg.Answer)
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

	case startRunMsg:
		return m.startRun(msg.wf)

	case runs.StartRunMsg:
		return m.startRun(msg.Wf)
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

// handleGlobalKey intercepts quit and the help modal before any screen sees the
// key. Returns (model, cmd, true) when the key is consumed; (m, nil, false)
// when it should fall through to the active screen's Update.
func (m rootModel) handleGlobalKey(msg tea.KeyPressMsg) (rootModel, tea.Cmd, bool) {
	if keybind.Matches(msg, shared.KeyQuit) {
		return m, tea.Quit, true
	}
	// Help is a global modal owned by the root. While open it swallows every
	// key except its own toggle and esc, so nothing fires on the screen behind
	// it. When closed, "?" opens it — unless the active screen is capturing free
	// text, where "?" is a literal character.
	if m.showHelp {
		if keybind.Matches(msg, shared.KeyHelp) || msg.String() == "esc" {
			m.showHelp = false
		}
		return m, nil, true
	}
	if keybind.Matches(msg, shared.KeyHelp) && !m.activeProvider().capturesText() {
		m.showHelp = true
		return m, nil, true
	}
	return m, nil, false
}

func (m rootModel) updateWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	var sc, dc, rc, mc tea.Cmd
	m.selector, sc = m.selector.Update(msg)
	if m.detail.ready {
		m.detail, dc = m.detail.Update(msg)
	}
	m.runs, rc = m.runs.Update(msg)
	m.monitor, mc = m.monitor.Update(msg)
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
	return m, tea.Batch(rearm, rc, mc)
}

func (m rootModel) openDetail(path string) (tea.Model, tea.Cmd) {
	m.detail = newDetailModel(path)
	var sizeCmd tea.Cmd
	m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.active = screenDetail
	return m, tea.Batch(sizeCmd, m.detail.Init())
}

// openMonitor navigates to the monitor for runID, seeding it from a live
// snapshot or replayed journal when first opened. Returning to the same run
// preserves the existing monitor state so events that arrived off-screen are
// already reflected.
func (m rootModel) openMonitor(runID string) (tea.Model, tea.Cmd) {
	if m.monitor.RunID != runID {
		m.monitor = monitor.New(runID)
		// RunDir lets the monitor read per-step transcripts from disk. Set it
		// before WithSnapshot so it preserves it.
		m.monitor.RunDir = m.manager.RunDir(runID)
		// Seed with a snapshot so already-completed or in-progress steps show up
		// immediately. A run from an earlier session has no handle, so fall back to
		// replaying its journal — the same events a Snapshot would carry.
		if run, ok := m.handles[runID]; ok {
			m.monitor = m.monitor.WithSnapshot(run.Snapshot())
		} else if evs, err := engine.ReplayJournal(m.monitor.RunDir); err == nil && len(evs) > 0 {
			m.monitor = m.monitor.WithJournal(evs)
		}
	}
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
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
	m.monitor = monitor.New(run.ID)
	m.monitor.RunDir = m.manager.RunDir(run.ID)
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.active = screenMonitor
	return m, nil
}
