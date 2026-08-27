package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/tui/monitor"
	"jig/internal/workflow"
)

// hydrateRunsCmd reads the runs persisted on disk and folds each durable journal into
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
			evs, err := engine.ReplayJournalRaw(mgr.RunDir(id))
			if err != nil || len(evs) == 0 {
				continue
			}
			groups = append(groups, evs)
		}
		return runsHydratedMsg{runs: groups}
	}
}

func resumeRunCmd(mgr *engine.Manager, runID, workflowName string) tea.Cmd {
	return func() tea.Msg {
		var fallback *workflow.Workflow
		if _, err := os.Stat(datastore.WorkflowSnapshotPath(mgr.RunDir(runID))); os.IsNotExist(err) {
			var loadErr error
			fallback, loadErr = findWorkflowByName(filepath.Join(filepath.Dir(filepath.Clean(mgr.Root())), ".agents", "jig"), workflowName)
			if loadErr != nil {
				return runResumedMsg{runID: runID, err: loadErr}
			}
		}
		run, err := mgr.Resume(runID, fallback)
		if err != nil {
			return runResumedMsg{runID: runID, err: err}
		}
		events, err := engine.ReplayJournal(mgr.RunDir(runID))
		return runResumedMsg{runID: runID, run: run, events: events, err: err}
	}
}

func findWorkflowByName(root, name string) (*workflow.Workflow, error) {
	var matches []*workflow.Workflow
	var matchingLoadErr error
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return filepath.SkipAll
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".toml") {
			return nil
		}
		meta, ok, err := workflow.LoadMeta(path)
		if err != nil || !ok || meta.Name != name {
			return nil
		}
		wf, err := workflow.Load(path)
		if err != nil {
			matchingLoadErr = err
			return nil
		}
		matches = append(matches, wf)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if matchingLoadErr != nil {
			return nil, fmt.Errorf("cannot resume: workflow %q is invalid: %w", name, matchingLoadErr)
		}
		return nil, fmt.Errorf("cannot resume: workflow %q was not found", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("cannot resume: workflow name %q is ambiguous", name)
	}
	return matches[0], nil
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
