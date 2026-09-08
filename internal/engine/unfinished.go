package engine

import "jig/internal/step"

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
	finished := false
	states := make(map[string]step.Status)
	for _, event := range events {
		switch event := event.(type) {
		case RunFinished:
			finished = true
		case StepStatus:
			states[event.StepID] = event.To
		}
	}
	if finished {
		return UnfinishedNone
	}
	hasReview := false
	hasInterrupted := false
	hasOther := false
	for _, status := range states {
		switch status {
		case step.StatusAwaitingReview:
			hasReview = true
		case step.StatusRunning, step.StatusValidating:
			hasInterrupted = true
		case step.StatusNeedsInput, step.StatusAwaitingRecovery,
			step.StatusAwaitingIntegration, step.StatusStopped:
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
