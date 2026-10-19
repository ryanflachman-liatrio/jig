package engine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// unfinishedScenarios are synthetic journals for a two-step run (b depends on
// a). They pin how the three independent journal folds — ClassifyUnfinished,
// reconcileInterruptedRun, and restoreUnfinishedCheckpoint — currently read the
// same history, so consolidating them into one fold cannot silently change
// behavior. Artifact checks (review documents, conflict markers in the run
// worktree) happen later in Resume and are covered by resume_test.go.
var unfinishedScenarios = []struct {
	name   string
	events []Event
	// classify is ClassifyUnfinished's result.
	classify UnfinishedKind
	// reconcileAdded is how many virtual events display replay appends.
	reconcileAdded int
	// restore summarizes restoreUnfinishedCheckpoint, or "error: <substring>".
	restore string
}{
	{
		name:           "pending only",
		events:         []Event{runStartedAB()},
		classify:       UnfinishedNone,
		reconcileAdded: 0,
		restore:        "error: no reopenable park",
	},
	{
		name:           "interrupted running",
		events:         []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning)},
		classify:       UnfinishedInterrupted,
		reconcileAdded: 0,
		restore:        "a=running,b=pending | interrupted:a",
	},
	{
		name:           "interrupted validating",
		events:         []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning), status("a", step.StatusRunning, step.StatusValidating)},
		classify:       UnfinishedInterrupted,
		reconcileAdded: 0,
		restore:        "a=validating,b=pending | interrupted:a",
	},
	{
		name: "awaiting recovery",
		events: []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning),
			status("a", step.StatusRunning, step.StatusAwaitingRecovery),
			RecoveryRequest{RunID: "r", StepID: "a", Err: "boom"}},
		classify:       UnfinishedOther,
		reconcileAdded: 0,
		restore:        "a=awaiting_recovery,b=pending | recoveries:a",
	},
	{
		name: "stopped",
		events: []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning),
			status("a", step.StatusRunning, step.StatusStopped)},
		classify:       UnfinishedOther,
		reconcileAdded: 0,
		restore:        "a=stopped,b=pending | stopped:a",
	},
	{
		name: "needs input without payload",
		events: []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning),
			status("a", step.StatusRunning, step.StatusNeedsInput),
			InputRequest{RunID: "r", StepID: "a", Reason: "need answer"}},
		classify:       UnfinishedOther,
		reconcileAdded: 0,
		restore:        "a=needs_input,b=pending | inputs:a",
	},
	{
		name: "awaiting review without session artifact",
		events: []Event{runStartedAB(), status("a", step.StatusPending, step.StatusAwaitingReview),
			ReviewRequest{RunID: "r", StepID: "a", Choices: []string{"approve"}}},
		classify:       UnfinishedReview,
		reconcileAdded: 0,
		restore:        "a=awaiting_review,b=pending | reviewSessions:a",
	},
	{
		name: "awaiting integration without conflict markers",
		events: []Event{runStartedAB(), status("a", step.StatusPending, step.StatusRunning),
			status("a", step.StatusRunning, step.StatusAwaitingIntegration),
			IntegrationConflictRequest{RunID: "r", StepID: "a", Paths: []string{"x.go"}}},
		classify:       UnfinishedOther,
		reconcileAdded: 0,
		restore:        "a=awaiting_integration,b=pending | integrations:a",
	},
	{
		name: "succeeded then finished",
		events: []Event{runStartedAB(),
			status("a", step.StatusPending, step.StatusSucceeded),
			status("b", step.StatusPending, step.StatusSucceeded),
			RunFinished{RunID: "r"}},
		classify:       UnfinishedNone,
		reconcileAdded: 0,
		restore:        "error: already finished",
	},
	{
		name: "partial success, nothing parked",
		events: []Event{runStartedAB(),
			status("a", step.StatusPending, step.StatusSucceeded)},
		classify:       UnfinishedNone,
		reconcileAdded: 0,
		restore:        "error: no reopenable park",
	},
}

func TestUnfinishedFoldsGolden(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "golden"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "a"
[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]
`, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range unfinishedScenarios {
		t.Run(sc.name, func(t *testing.T) {
			runDir := datastore.RunAt(t.TempDir())
			if err := persistWorkflowSnapshot(runDir, wf); err != nil {
				t.Fatal(err)
			}
			if got := ClassifyUnfinished(sc.events); got != sc.classify {
				t.Errorf("ClassifyUnfinished = %d, want %d", got, sc.classify)
			}
			if got := len(reconcileInterruptedRun(runDir, sc.events, false)) - len(sc.events); got != sc.reconcileAdded {
				t.Errorf("reconcileInterruptedRun added %d events, want %d", got, sc.reconcileAdded)
			}
			got := summarizeCheckpoint(restoreUnfinishedCheckpoint(runDir, wf, sc.events))
			if strings.HasPrefix(sc.restore, "error: ") {
				if !strings.HasPrefix(got, "error: ") || !strings.Contains(got, strings.TrimPrefix(sc.restore, "error: ")) {
					t.Errorf("restore = %q, want %q", got, sc.restore)
				}
			} else if got != sc.restore {
				t.Errorf("restore =\n  %s\nwant\n  %s", got, sc.restore)
			}
		})
	}
}

func runStartedAB() RunStarted {
	return RunStarted{RunID: "r", Workflow: "golden", Steps: []string{"a", "b"}}
}

func status(id string, from, to step.Status) StepStatus {
	return StepStatus{RunID: "r", StepID: id, From: from, To: to}
}

// summarizeCheckpoint renders a checkpoint as "a=<status>,b=<status> | parks"
// where parks lists each non-empty park registry and its step ids.
func summarizeCheckpoint(c *unfinishedCheckpoint, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	ids := make([]string, 0, len(c.states))
	for id := range c.states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var states []string
	for _, id := range ids {
		states = append(states, fmt.Sprintf("%s=%s", id, c.states[id].Status))
	}
	var parks []string
	v := reflect.ValueOf(c).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if f.Kind() != reflect.Map || f.Len() == 0 || strings.HasPrefix(name, "fanOut") || name == "states" {
			continue
		}
		var keys []string
		for _, k := range f.MapKeys() {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		parks = append(parks, name+":"+strings.Join(keys, "+"))
	}
	return strings.Join(states, ",") + " | " + strings.Join(parks, " ")
}
