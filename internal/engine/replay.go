package engine

import (
	"bufio"
	"fmt"
	"io"
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
// persistence off. An incomplete trailing record is tolerated as a crash-torn
// tail. A newline-complete corrupt record is not skipped: display replay marks
// the decodable prefix orphaned, while strict resume rejects the journal.
func ReplayJournal(runDir string) ([]Event, error) {
	_, events, err := readJournal(runDir)
	if err != nil {
		if len(events) == 0 {
			return nil, err
		}
		events = hydrateReviewDocuments(runDir, events)
		return reconcileInterruptedRun(runDir, events, true), nil
	}
	events = hydrateReviewDocuments(runDir, events)
	return reconcileInterruptedRun(runDir, events, false), nil
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
	lineNumber := 0
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			env, e, decodeErr := UnmarshalEnvelope([]byte(line))
			if decodeErr != nil {
				// A crash can interrupt the final append. Only that unterminated
				// tail is discardable; corruption in a complete record is fatal.
				if readErr == io.EOF {
					break
				}
				return lastSeq, events, fmt.Errorf("decode journal line %d: %w", lineNumber, decodeErr)
			}
			if e != nil {
				if env.Seq > lastSeq {
					lastSeq = env.Seq
				}
				events = append(events, e)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return lastSeq, events, readErr
			}
			break
		}
	}
	return lastSeq, events, nil
}

// reconcileInterruptedRun supplies terminal events that could not be journaled
// when the jig process disappeared while an SDK-backed step was in flight — but
// only for **orphaned** runs that cannot reopen (Spec 20 D7). Reopenable runs
// (workflow.json present with a RunStarted) stay unfinished so Monitor / Runs
// can offer Resume instead of a virtual RunFinished.
func reconcileInterruptedRun(runDir string, events []Event, forceOrphan bool) []Event {
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
	if finished {
		return events
	}
	// Only a successfully decoded snapshot can make the run reopenable. A present
	// but corrupt snapshot is an orphan just like a missing one (Spec 20 D7).
	if !forceOrphan && started != nil {
		if _, err := loadWorkflowSnapshot(runDir); err == nil {
			return events
		}
	}

	ordered := make([]string, 0, len(states))
	seen := make(map[string]bool, len(states))
	runID := ""
	for _, event := range events {
		status, ok := event.(StepStatus)
		if !ok {
			continue
		}
		if runID == "" {
			runID = status.RunID
		}
		if !seen[status.StepID] {
			seen[status.StepID] = true
			ordered = append(ordered, status.StepID)
		}
	}
	if started != nil {
		runID = started.RunID
		ordered = started.Steps
	}
	if runID == "" {
		return events
	}

	var recovered []Event
	for _, stepID := range ordered {
		from := states[stepID]
		if from != step.StatusRunning && from != step.StatusValidating {
			continue
		}
		prior := lastStatus[stepID]
		recovered = append(recovered, StepStatus{
			RunID:      runID,
			StepID:     stepID,
			From:       from,
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
		RunID:  runID,
		Failed: true,
	})...)
}
