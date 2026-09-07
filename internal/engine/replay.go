package engine

import (
	"bufio"
	"os"
	"path/filepath"

	"jig/internal/datastore"
	"jig/internal/review"
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
	_, events, err := readJournal(runDir)
	if err != nil {
		return nil, err
	}
	events = hydrateReviewDocuments(runDir, events)
	return reconcileInterruptedRun(runDir, events), nil
}

// ReplayJournalRaw returns only events durably written by the original
// scheduler. Ownership views use it to distinguish an unfinished paused run
// from a terminal run without adding display-only crash reconciliation events.
func ReplayJournalRaw(runDir string) ([]Event, error) {
	_, events, err := readJournal(runDir)
	return events, err
}

func hydrateReviewDocuments(runDir string, events []Event) []Event {
	hydrated := append([]Event(nil), events...)
	for i, event := range hydrated {
		req, ok := event.(ReviewRequest)
		if !ok {
			continue
		}
		req.Documents = append([]review.Document(nil), req.Documents...)
		for j := range req.Documents {
			path := req.Documents[j].SnapshotPath
			data, err := os.ReadFile(path)
			if err != nil {
				path = filepath.Join(datastore.ReviewDocumentsDir(runDir, req.StepID, req.RoundID), req.Documents[j].ID+reviewExtension(req.Documents[j].Format))
				data, err = os.ReadFile(path)
			}
			if err != nil || review.Digest(string(data)) != req.Documents[j].SHA256 {
				continue
			}
			req.Documents[j].SnapshotPath = path
			req.Documents[j].Content = string(data)
			if req.Documents[j].Format == "diff" {
				req.Diff = string(data)
			}
		}
		hydrated[i] = req
	}
	return hydrated
}

// readJournal returns only durable events. Resume must not consume the virtual
// failure events ReplayJournal adds for display, because those events were never
// committed and would incorrectly make an interrupted run terminal.
func readJournal(runDir string) (int, []Event, error) {
	f, err := os.Open(datastore.JournalPath(runDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	defer f.Close()

	var events []Event
	lastSeq := 0
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			if env, e, err := UnmarshalEnvelope([]byte(line)); err == nil && e != nil {
				if env.Seq > lastSeq {
					lastSeq = env.Seq
				}
				events = append(events, e)
			}
		}
		if readErr != nil {
			break // io.EOF or a read error; either way, stop with what we have.
		}
	}
	return lastSeq, events, nil
}

// reconcileInterruptedRun supplies terminal events that could not be journaled
// when the jig process disappeared while an SDK-backed step was in flight — but
// only for **orphaned** runs that cannot reopen (Spec 20 D7). Reopenable runs
// (workflow.json present with a RunStarted) stay unfinished so Monitor / Runs
// can offer Resume instead of a virtual RunFinished.
func reconcileInterruptedRun(runDir string, events []Event) []Event {
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
	// Reopenable: durable workflow snapshot exists. Do not invent terminal events.
	if fileExists(datastore.WorkflowSnapshotPath(runDir)) {
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
