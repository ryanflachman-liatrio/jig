package tui

import (
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/monitor"
)

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
