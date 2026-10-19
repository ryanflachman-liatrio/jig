package engine

// journalSummary is the run-level reading of a journal shared by the display
// and classification paths: its lifecycle bookends and each step's latest
// status. Resume needs far more (parks, fan-out, results) and folds the
// journal itself in restoreUnfinishedCheckpoint.
type journalSummary struct {
	started  *RunStarted
	finished bool
	// runID comes from RunStarted, else the first StepStatus.
	runID string
	// order lists step ids by their first StepStatus.
	order []string
	// last is each step's most recent StepStatus.
	last map[string]StepStatus
}

func summarizeJournal(events []Event) journalSummary {
	sum := journalSummary{last: make(map[string]StepStatus)}
	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			startedEvent := event
			sum.started = &startedEvent
		case RunFinished:
			sum.finished = true
		case StepStatus:
			if _, seen := sum.last[event.StepID]; !seen {
				sum.order = append(sum.order, event.StepID)
			}
			if sum.runID == "" {
				sum.runID = event.RunID
			}
			sum.last[event.StepID] = event
		}
	}
	if sum.started != nil {
		sum.runID = sum.started.RunID
	}
	return sum
}
