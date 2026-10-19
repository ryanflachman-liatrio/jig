package engine

import "github.com/ryanflachman-liatrio/jig/internal/step"

// UnfinishedKind classifies an unfinished historical run for Runs-list copy
// (Spec 20): review-paused vs crash-interrupted vs other unfinished parks.
type UnfinishedKind int

const (
	UnfinishedNone UnfinishedKind = iota
	UnfinishedReview
	UnfinishedInterrupted
	UnfinishedOther
)

// ClassifyUnfinished folds durable events and reports how an unfinished run
// should present when there is no live scheduler. Finished journals yield
// UnfinishedNone.
func ClassifyUnfinished(events []Event) UnfinishedKind {
	sum := summarizeJournal(events)
	if sum.finished {
		return UnfinishedNone
	}
	hasReview := false
	hasInterrupted := false
	hasOther := false
	for _, last := range sum.last {
		switch {
		case last.To == step.StatusAwaitingReview:
			hasReview = true
		case last.To.IsInFlight():
			hasInterrupted = true
		case last.To.IsParked():
			hasOther = true
		}
	}
	if hasInterrupted {
		return UnfinishedInterrupted
	}
	if hasReview {
		return UnfinishedReview
	}
	if hasOther {
		return UnfinishedOther
	}
	return UnfinishedNone
}
