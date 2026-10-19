package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// journalKinds pins every event's on-disk kind string. Kinds are a persisted
// format: renaming one makes existing journals unreadable. Add a row for a new
// event; never edit an existing one.
var journalKinds = map[string]string{
	"RunStarted":                 "run_started",
	"RunFinished":                "run_finished",
	"StepStatus":                 "step_status",
	"StepOutput":                 "step_output",
	"StepToolCall":               "step_tool_call",
	"StepMessage":                "step_message",
	"GateResult":                 "gate_result",
	"RouteSelected":              "route_selected",
	"RouteCapExceeded":           "route_cap_exceeded",
	"StepsReset":                 "steps_reset",
	"ReviewRequest":              "review_request",
	"ReviewSubmitted":            "review_submitted",
	"InputRequest":               "input_request",
	"RunError":                   "run_error",
	"RecoveryRequest":            "recovery_request",
	"IntegrationConflictRequest": "integration_conflict_request",
	"FinalMergeRequest":          "final_merge_request",
	"PromptRequest":              "prompt_request",
	"AgentQuestion":              "agent_question",
	"AgentQuestionResolved":      "agent_question_resolved",
	"SecurityFinding":            "security_finding",
	"FanOutExpanded":             "fan_out_expanded",
}

// TestJournalKindsAreStable checks each event's kind against the pinned list.
func TestJournalKindsAreStable(t *testing.T) {
	for _, ev := range allEventInstances {
		name := reflect.TypeOf(ev).Name()
		want, ok := journalKinds[name]
		if !ok {
			t.Errorf("%s has no pinned kind; add it to journalKinds", name)
			continue
		}
		if got := eventKind(ev); got != want {
			t.Errorf("%s kind = %q, want %q (kinds are on-disk format)", name, got, want)
		}
	}
}

// TestAllEventInstancesCoversEveryEvent parses the package for isEvent
// implementations so a new event cannot skip the exhaustiveness and kind tests
// by being left out of allEventInstances.
func TestAllEventInstancesCoversEveryEvent(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "isEvent" || fn.Recv == nil {
					continue
				}
				if ident, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
					declared = append(declared, ident.Name)
				}
			}
		}
	}
	listed := make(map[string]bool)
	for _, ev := range allEventInstances {
		listed[reflect.TypeOf(ev).Name()] = true
	}
	sort.Strings(declared)
	for _, name := range declared {
		if !listed[name] {
			t.Errorf("event %s is missing from allEventInstances", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no isEvent implementations; the parser filter is wrong")
	}
}
