package tui

import (
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/monitor"
)

// hydrateRunsCmd reads persisted runs off the UI goroutine. ReplayJournal keeps
// valid reopenable histories raw, but adds display-only terminal reconciliation
// for orphaned histories whose snapshot is missing/corrupt or whose journal is
// corrupt. That prevents the Runs screen from advertising an R action that
// Manager.Resume must reject.
func hydrateRunsCmd(mgr *engine.Manager) tea.Cmd {
	return func() tea.Msg {
		ids, err := mgr.PersistedRuns()
		if err != nil || len(ids) == 0 {
			return runsHydratedMsg{}
		}
		groups := make([][]engine.Event, 0, len(ids))
		for _, id := range ids {
			evs, err := engine.ReplayJournal(mgr.RunDir(id))
			if err != nil || len(evs) == 0 {
				continue
			}
			groups = append(groups, evs)
		}
		return runsHydratedMsg{runs: groups}
	}
}

func resumeRunCmd(mgr *engine.Manager, runID string) tea.Cmd {
	return func() tea.Msg {
		run, err := mgr.Resume(runID)
		if err != nil {
			return runResumedMsg{runID: runID, err: err}
		}
		events, err := engine.ReplayJournal(mgr.RunDir(runID))
		return runResumedMsg{runID: runID, run: run, events: events, err: err}
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
		return monitor.EngineEventMsg{Event: e, IsLive: true}
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
		return monitor.EngineEventMsg{Event: e, IsLive: false}
	}
}
