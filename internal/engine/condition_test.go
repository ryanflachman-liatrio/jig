package engine

import (
	"encoding/json"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

func TestEvalGuardCompoundTypedExpressions(t *testing.T) {
	producer := workflow.Step{
		ID:         "analyze",
		Type:       workflow.StepCommand,
		OutputType: workflow.OutputType{Kind: workflow.OutputText},
		Schema: &workflow.Schema{Fields: []*workflow.Field{
			{Name: "all_succeeded", Type: workflow.FieldBool},
			{Name: "count", Type: workflow.FieldNumber},
			{Name: "status", Type: workflow.FieldEnum, Enum: []string{"ready", "blocked"}},
		}},
	}
	scheduler := &scheduler{
		wf: &workflow.Workflow{Steps: []workflow.Step{producer}},
		states: map[string]*step.State{
			"analyze": {ID: "analyze", Result: &step.Result{Structured: json.RawMessage(`{"all_succeeded":true,"count":10,"status":"ready"}`)}},
		},
		structured: map[string]map[string]any{},
	}
	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "analyze.all_succeeded && analyze.count >= 2", want: true},
		{raw: "analyze.count < 2 || analyze.status == 'ready'", want: true},
		{raw: "analyze.count > 10", want: false},
		{raw: "analyze.count == 10", want: true},
		{raw: "analyze.count != 10", want: false},
		{raw: "analyze.status == 'blocked' || (analyze.count <= 10 && analyze.all_succeeded)", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			condition, err := workflow.ParseCondition(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := scheduler.evalGuard(condition); got != tt.want {
				t.Fatalf("evalGuard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestForEachCompoundAggregateGuardPersistenceOff(t *testing.T) {
	for _, tt := range []struct {
		name      string
		items     []map[string]string
		wantCalls int
	}{
		{name: "non-empty aggregate dispatches", items: []map[string]string{target("api", "services/api")}, wantCalls: 1},
		{name: "empty aggregate skips", wantCalls: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := foreachTOML(8, "") + `
[[step]]
id = "synthesize"
type = "command"
depends_on = ["analyze"]
when = "analyze.all_succeeded && analyze.count >= 1"
run = "true"
`
			wf, err := workflow.Decode(source, "")
			if err != nil {
				t.Fatal(err)
			}
			exec := &fanOutExec{producer: map[string]json.RawMessage{"discover": discoverStructured(tt.items...)}}
			manager := NewManager(exec, "")
			_, events := manager.Subscribe()
			if _, err := manager.Start(wf); err != nil {
				t.Fatal(err)
			}
			collected := collectEvents(t, events, 5*time.Second)
			if finished, ok := collected[len(collected)-1].(RunFinished); !ok || finished.Failed {
				t.Fatalf("last event = %+v, want successful RunFinished", collected[len(collected)-1])
			}
			if got := exec.callCount("synthesize"); got != tt.wantCalls {
				t.Fatalf("synthesize calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestEvalGuardShortCircuitsAndFailsClosed(t *testing.T) {
	scheduler := &scheduler{
		wf: &workflow.Workflow{Steps: []workflow.Step{{ID: "flag", Type: workflow.StepCommand, OutputType: workflow.OutputType{Kind: workflow.OutputBool}}}},
		states: map[string]*step.State{
			"flag": {ID: "flag", Result: &step.Result{Verdict: "true"}},
		},
		structured: map[string]map[string]any{},
	}
	for raw, want := range map[string]bool{
		"flag || missing.value == 'x'":   true,
		"flag == false && missing.value": false,
		"missing.value == 'x'":           false,
	} {
		condition, err := workflow.ParseCondition(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := scheduler.evalGuard(condition); got != want {
			t.Errorf("evalGuard(%q) = %v, want %v", raw, got, want)
		}
	}
}
