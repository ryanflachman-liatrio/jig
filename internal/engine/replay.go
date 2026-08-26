package engine

import (
	"bufio"
	"os"

	"jig/internal/datastore"
	"jig/internal/step"
)

const interruptedSDKSessionErr = "agent SDK session terminated abruptly before reporting a terminal result"

// ReplayJournal reads runDir's journal.jsonl and returns the events it recorded,
// in seq order. It is the read side of the "state = fold(journal)" invariant:
// the engine journals every event before fan-out (see internal/manifest), so
// folding the returned events through the same handlers a live run drives
// reconstructs that run's state. This is how the TUI rebuilds its run list — and
// a per-run monitor — for runs from earlier sessions, where no in-memory Run
// handle exists to Snapshot().
//
// A missing journal yields nil with no error: an empty run, or one recorded with
// persistence off. Lines that fail to decode are skipped rather than aborting the
// replay — a torn tail from a crash mid-write, or a kind emitted by a newer
// schema, still leaves every decodable event intact. The caller therefore always
// gets the best reconstruction the journal supports.
func ReplayJournal(runDir string) ([]Event, error) {
	f, err := os.Open(datastore.JournalPath(runDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			if _, e, err := UnmarshalEnvelope([]byte(line)); err == nil && e != nil {
				events = append(events, e)
			}
		}
		if readErr != nil {
			break // io.EOF or a read error; either way, stop with what we have.
		}
	}
	return reconcileInterruptedRun(events), nil
}

// reconcileInterruptedRun supplies terminal events that could not be journaled
// when the jig process disappeared while an SDK-backed step was in flight. The
// returned events are deliberately virtual: the original journal remains an
// accurate record of what was durably observed, while every replay consumer
// gets a truthful terminal state instead of displaying a permanently-running
// step with no owner left to update it.
func reconcileInterruptedRun(events []Event) []Event {
	var started *RunStarted
	finished := false
	states := make(map[string]step.Status)
	lastStatus := make(map[string]StepStatus)

	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			copy := event
			started = &copy
		case StepStatus:
			states[event.StepID] = event.To
			lastStatus[event.StepID] = event
		case RunFinished:
			finished = true
		}
	}
	if started == nil || finished {
		return events
	}

	var recovered []Event
	for _, stepID := range started.Steps {
		if states[stepID] != step.StatusRunning {
			continue
		}
		prior := lastStatus[stepID]
		recovered = append(recovered, StepStatus{
			RunID:      started.RunID,
			StepID:     stepID,
			From:       step.StatusRunning,
			To:         step.StatusFailed,
			Attempt:    prior.Attempt,
			Iteration:  prior.Iteration,
			Generation: prior.Generation,
			Err:        interruptedSDKSessionErr,
		})
	}
	if len(recovered) == 0 {
		return events
	}
	return append(append([]Event{}, events...), append(recovered, RunFinished{
		RunID:  started.RunID,
		Failed: true,
	})...)
}
