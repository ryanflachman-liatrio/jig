package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// JournalRecord preserves the durable envelope metadata alongside its decoded
// event. Event is nil for a well-formed envelope whose kind is unknown to this
// version, allowing inspection tools to retain its timestamp without guessing.
type JournalRecord struct {
	Seq       int
	Timestamp time.Time
	Kind      string
	Event     Event
}

const interruptedSDKSessionErr = "agent SDK session terminated abruptly before reporting a terminal result"

const orphanedRunErr = "run cannot be reopened because its durable workflow snapshot or journal is unavailable"

// ReplayJournal reads runDir's journal.jsonl and returns the events it recorded,
// in seq order. It is the read side of the "state = fold(journal)" invariant:
// the engine journals every event before fan-out (see internal/manifest), so
// folding the returned events through the same handlers a live run drives
// reconstructs that run's state. This is how the TUI rebuilds its run list — and
// a per-run monitor — for runs from earlier sessions, where no in-memory Run
// handle exists to Snapshot().
//
// A missing/empty journal for a persisted run directory produces a synthetic
// failed orphan history so the run remains visible. An incomplete trailing
// record is tolerated as a crash-torn tail. A newline-complete corrupt record
// is not skipped: display replay marks the decodable prefix orphaned, while
// strict resume rejects the journal.
func ReplayJournal(runDir datastore.Run) ([]Event, error) {
	_, events, err := readJournal(runDir)
	if err != nil {
		if len(events) == 0 {
			if orphan := orphanTerminalHistory(runDir); len(orphan) > 0 {
				return orphan, nil
			}
			return nil, err
		}
		events = hydrateReviewDocuments(runDir, events)
		return reconcileInterruptedRun(runDir, events, true), nil
	}
	if len(events) == 0 {
		return orphanTerminalHistory(runDir), nil
	}
	events = hydrateReviewDocuments(runDir, events)
	return reconcileInterruptedRun(runDir, events, false), nil
}

func orphanTerminalHistory(runDir datastore.Run) []Event {
	if !runDir.Enabled() {
		return nil
	}
	info, err := os.Stat(runDir.Dir())
	if err != nil || !info.IsDir() {
		return nil
	}
	runID := filepath.Base(runDir.Dir())
	if runID == "." || runID == string(filepath.Separator) || runID == "" {
		return nil
	}
	workflowName := ""
	var stepIDs []string
	if wf, loadErr := loadWorkflowSnapshot(runDir); loadErr == nil && wf != nil {
		workflowName = wf.Meta.Name
		stepIDs = make([]string, 0, len(wf.Steps))
		for i := range wf.Steps {
			stepIDs = append(stepIDs, wf.Steps[i].ID)
		}
	}
	events := []Event{RunStarted{RunID: runID, Workflow: workflowName, Steps: stepIDs}}
	for _, stepID := range stepIDs {
		events = append(events, StepStatus{
			RunID: runID, StepID: stepID, From: step.StatusPending, To: step.StatusFailed, Err: orphanedRunErr,
		})
	}
	return append(events, RunFinished{RunID: runID, Failed: true})
}

// ReplayJournalRaw returns only events durably written by the original
// scheduler. Ownership views use it to distinguish an unfinished paused run
// from a terminal run without adding display-only crash reconciliation events.
func ReplayJournalRaw(runDir datastore.Run) ([]Event, error) {
	_, events, err := readJournal(runDir)
	return events, err
}

// ReplayJournalRecords returns only records written by the scheduler, retaining
// envelope timestamps and any decodable prefix before a complete corrupt line.
// A crash-torn final line is ignored in the same way as ReplayJournalRaw.
func ReplayJournalRecords(runDir datastore.Run) ([]JournalRecord, error) {
	_, records, err := readJournalRecords(runDir)
	return records, err
}

func hydrateReviewDocuments(runDir datastore.Run, events []Event) []Event {
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
				path = filepath.Join(runDir.Step(req.StepID).Review(req.RoundID).Documents(), req.Documents[j].ID+reviewExtension(req.Documents[j].Format))
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
func readJournal(runDir datastore.Run) (int, []Event, error) {
	seq, records, err := readJournalRecords(runDir)
	events := make([]Event, 0, len(records))
	for _, record := range records {
		if record.Event != nil {
			events = append(events, record.Event)
		}
	}
	return seq, events, err
}

// readJournalStrict is readJournal for resume. Display readers skip unknown
// kinds so a newer journal still renders, but resuming would silently drop the
// state those records carry, so here an unknown kind is an error.
func readJournalStrict(runDir datastore.Run) (int, []Event, error) {
	seq, records, err := readJournalRecords(runDir)
	if err != nil {
		return seq, nil, err
	}
	events := make([]Event, 0, len(records))
	for _, record := range records {
		if record.Event == nil {
			return seq, nil, fmt.Errorf("journal seq %d: %w %q", record.Seq, ErrUnknownJournalKind, record.Kind)
		}
		events = append(events, record.Event)
	}
	return seq, events, nil
}

func readJournalRecords(runDir datastore.Run) (int, []JournalRecord, error) {
	if !runDir.Enabled() {
		return 0, nil, nil
	}
	f, err := os.Open(runDir.Journal())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	defer f.Close()

	var records []JournalRecord
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
				return lastSeq, records, fmt.Errorf("decode journal line %d: %w", lineNumber, decodeErr)
			}
			if env.Seq > lastSeq {
				lastSeq = env.Seq
			}
			// Older journals carry time only in the envelope. Keep that recorded
			// precision instead of substituting the time of replay.
			if status, ok := e.(StepStatus); ok && status.At.IsZero() {
				status.At = env.Ts
				e = status
			}
			records = append(records, JournalRecord{Seq: env.Seq, Timestamp: env.Ts, Kind: env.Kind, Event: e})
		}
		if readErr != nil {
			if readErr != io.EOF {
				return lastSeq, records, readErr
			}
			break
		}
	}
	return lastSeq, records, nil
}

// reconcileInterruptedRun supplies terminal events that could not be journaled
// when the jig process disappeared while an SDK-backed step was in flight — but
// only for **orphaned** runs that cannot reopen (Spec 20 D7). Reopenable runs
// (workflow.json present with a RunStarted) stay unfinished so Monitor / Runs
// can offer Resume instead of a virtual RunFinished.
func reconcileInterruptedRun(runDir datastore.Run, events []Event, forceOrphan bool) []Event {
	var started *RunStarted
	finished := false
	states := make(map[string]step.Status)
	lastStatus := make(map[string]StepStatus)

	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			startedEvent := event
			started = &startedEvent
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
		for _, stepID := range ordered {
			if _, ok := states[stepID]; !ok {
				states[stepID] = step.StatusPending
			}
		}
	}
	if runID == "" {
		return events
	}

	var recovered []Event
	for _, stepID := range ordered {
		from := states[stepID]
		if terminalStatus(from) {
			continue
		}
		prior := lastStatus[stepID]
		reason := orphanedRunErr
		if from == step.StatusRunning || from == step.StatusValidating {
			reason = interruptedSDKSessionErr
		}
		recovered = append(recovered, StepStatus{
			At:         prior.At,
			RunID:      runID,
			StepID:     stepID,
			From:       from,
			To:         step.StatusFailed,
			Attempt:    prior.Attempt,
			Iteration:  prior.Iteration,
			Generation: prior.Generation,
			Err:        reason,
		})
	}
	return append(append([]Event{}, events...), append(recovered, RunFinished{
		RunID:  runID,
		Failed: true,
	})...)
}
