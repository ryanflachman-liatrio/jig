package engine

import (
	"testing"

	"jig/internal/step"
)

func TestClassifyUnfinished(t *testing.T) {
	cases := []struct {
		name string
		evs  []Event
		want UnfinishedKind
	}{
		{
			name: "finished",
			evs: []Event{
				RunStarted{RunID: "r", Workflow: "w", Steps: []string{"a"}},
				StepStatus{StepID: "a", To: step.StatusSucceeded},
				RunFinished{RunID: "r"},
			},
			want: UnfinishedNone,
		},
		{
			name: "review paused",
			evs: []Event{
				RunStarted{RunID: "r", Workflow: "w", Steps: []string{"a", "g"}},
				StepStatus{StepID: "a", To: step.StatusSucceeded},
				StepStatus{StepID: "g", To: step.StatusAwaitingReview},
			},
			want: UnfinishedReview,
		},
		{
			name: "interrupted prefers crash over review",
			evs: []Event{
				RunStarted{RunID: "r", Workflow: "w", Steps: []string{"a", "g", "w"}},
				StepStatus{StepID: "a", To: step.StatusSucceeded},
				StepStatus{StepID: "g", To: step.StatusAwaitingReview},
				StepStatus{StepID: "w", To: step.StatusRunning},
			},
			want: UnfinishedInterrupted,
		},
		{
			name: "spec21 park",
			evs: []Event{
				RunStarted{RunID: "r", Workflow: "w", Steps: []string{"a"}},
				StepStatus{StepID: "a", To: step.StatusNeedsInput},
			},
			want: UnfinishedOther,
		},
		{
			name: "latest terminal state wins over historical running",
			evs: []Event{
				RunStarted{RunID: "r", Workflow: "w", Steps: []string{"worker", "gate"}},
				StepStatus{StepID: "worker", To: step.StatusRunning},
				StepStatus{StepID: "worker", To: step.StatusSucceeded},
				StepStatus{StepID: "gate", To: step.StatusAwaitingReview},
			},
			want: UnfinishedReview,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyUnfinished(tc.evs); got != tc.want {
				t.Fatalf("ClassifyUnfinished = %v, want %v", got, tc.want)
			}
		})
	}
}
