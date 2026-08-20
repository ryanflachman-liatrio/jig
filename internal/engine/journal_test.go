package engine

import (
	"reflect"
	"testing"
	"time"

	"jig/internal/interaction"
	"jig/internal/step"
)

func TestJournalRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{
			name: "RunStarted",
			ev:   RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"a", "b"}},
		},
		{
			name: "RunFinished ok",
			ev:   RunFinished{RunID: "r1", Failed: false},
		},
		{
			name: "RunFinished failed",
			ev:   RunFinished{RunID: "r1", Failed: true},
		},
		{
			name: "StepStatus",
			ev: StepStatus{
				RunID:     "r1",
				StepID:    "fix",
				From:      step.StatusPending,
				To:        step.StatusRunning,
				Attempt:   1,
				Iteration: 2,
			},
		},
		{
			name: "StepOutput",
			ev:   StepOutput{RunID: "r1", StepID: "fix", Delta: "partial text"},
		},
		{
			name: "StepToolCall",
			ev:   StepToolCall{RunID: "r1", StepID: "fix", Tool: "Edit", Detail: "some/file.go"},
		},
		{
			name: "StepMessage",
			ev:   StepMessage{RunID: "r1", StepID: "fix", Seq: 7, Iteration: 2},
		},
		{
			name: "GateResult pass",
			ev:   GateResult{RunID: "r1", StepID: "validate", Passed: true, Detail: "exit 0"},
		},
		{
			name: "LoopFired",
			ev:   LoopFired{RunID: "r1", StepID: "fix", Goto: "research", Iteration: 2, Max: 3},
		},
		{
			name: "ReviewRequest",
			ev:   ReviewRequest{RunID: "r1", StepID: "review", Choices: []string{"approve", "revise"}},
		},
		{
			name: "RunError",
			ev:   RunError{RunID: "r1", Err: "unexpected panic"},
		},
		{
			name: "RecoveryRequest",
			ev:   RecoveryRequest{RunID: "r1", StepID: "fix", Err: "boom", CanResume: true},
		},
		{
			name: "IntegrationConflictRequest",
			ev:   IntegrationConflictRequest{RunID: "r1", StepID: "impl", Paths: []string{"a.go", "b.go"}},
		},
		{
			name: "FinalMergeRequest",
			ev:   FinalMergeRequest{RunID: "r1", RunBranch: "jig/wf/run-1", Base: "main"},
		},
	}

	for i, tc := range cases {
		line, err := MarshalEnvelope(i+1, tc.ev)
		if err != nil {
			t.Fatalf("%s: MarshalEnvelope: %v", tc.name, err)
		}

		env, got, err := UnmarshalEnvelope(line)
		if err != nil {
			t.Fatalf("%s: UnmarshalEnvelope: %v", tc.name, err)
		}
		if env.Seq != i+1 {
			t.Errorf("%s: seq: want %d, got %d", tc.name, i+1, env.Seq)
		}
		if env.Ts.IsZero() {
			t.Errorf("%s: ts is zero", tc.name)
		}
		if env.Ts.Location() != time.UTC {
			t.Errorf("%s: ts not UTC", tc.name)
		}
		if got == nil {
			t.Fatalf("%s: decoded event is nil", tc.name)
		}
		// Structural equality: re-marshal both and compare JSON.
		wantJSON, _ := MarshalEnvelope(i+1, tc.ev)
		gotLine, _ := MarshalEnvelope(i+1, got)
		// Compare the data fields only (ts would differ on a second marshal).
		envWant, _, _ := UnmarshalEnvelope(wantJSON)
		envGot, _, _ := UnmarshalEnvelope(gotLine)
		if string(envWant.Data) != string(envGot.Data) {
			t.Errorf("%s: data mismatch\n  want: %s\n   got: %s",
				tc.name, envWant.Data, envGot.Data)
		}
		if envWant.Kind != envGot.Kind {
			t.Errorf("%s: kind: want %q, got %q", tc.name, envWant.Kind, envGot.Kind)
		}
	}
}

func TestUnmarshalEnvelope_UnknownKind(t *testing.T) {
	// Unknown kinds return (env, nil, nil) for forward-compatibility: a journal
	// written by a newer jig can be replayed by an older one, skipping new kinds.
	line := []byte(`{"seq":1,"ts":"2026-01-01T00:00:00Z","kind":"future_event","data":{}}`)
	_, e, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("expected nil error for unknown kind, got: %v", err)
	}
	if e != nil {
		t.Fatalf("expected nil event for unknown kind, got: %T", e)
	}
}

// TestStepsResetRoundTrip verifies that StepsReset marshals and unmarshals
// correctly through the journal envelope codec.
func TestStepsResetRoundTrip(t *testing.T) {
	ev := StepsReset{
		RunID:    "run-42",
		Target:   "impl",
		Closure:  []string{"impl", "review", "merge"},
		RewindTo: "abc1234def5678",
	}
	line, err := MarshalEnvelope(99, ev)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}

	env, got, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if got == nil {
		t.Fatal("decoded event is nil")
	}
	if env.Kind != "steps_reset" {
		t.Errorf("kind = %q; want %q", env.Kind, "steps_reset")
	}
	sr, ok := got.(StepsReset)
	if !ok {
		t.Fatalf("decoded type = %T; want StepsReset", got)
	}
	if sr.RunID != ev.RunID {
		t.Errorf("RunID = %q; want %q", sr.RunID, ev.RunID)
	}
	if sr.Target != ev.Target {
		t.Errorf("Target = %q; want %q", sr.Target, ev.Target)
	}
	if sr.RewindTo != ev.RewindTo {
		t.Errorf("RewindTo = %q; want %q", sr.RewindTo, ev.RewindTo)
	}
	if len(sr.Closure) != len(ev.Closure) {
		t.Fatalf("Closure len = %d; want %d", len(sr.Closure), len(ev.Closure))
	}
	for i := range ev.Closure {
		if sr.Closure[i] != ev.Closure[i] {
			t.Errorf("Closure[%d] = %q; want %q", i, sr.Closure[i], ev.Closure[i])
		}
	}
}

// allEventInstances enumerates one representative value for every Event union
// member. TestEventExhaustiveness uses this to verify the journal handles them all.
// ADD NEW EVENT TYPES HERE — the test will fail if eventKind returns "unknown"
// or if no decoder exists for the kind.
var allEventInstances = []Event{
	RunStarted{RunID: "r", Workflow: "w", Steps: []string{"a"}},
	RunFinished{RunID: "r", Failed: false},
	StepStatus{RunID: "r", StepID: "s", From: step.StatusPending, To: step.StatusRunning},
	StepOutput{RunID: "r", StepID: "s", Delta: "hello"},
	StepToolCall{RunID: "r", StepID: "s", Tool: "Bash", Detail: "ls"},
	StepMessage{RunID: "r", StepID: "s", Seq: 1, Iteration: 0},
	GateResult{RunID: "r", StepID: "s", Passed: true},
	LoopFired{RunID: "r", StepID: "s", Goto: "s", Iteration: 1, Max: 3},
	ReviewRequest{RunID: "r", StepID: "s", Choices: []string{"approve"}},
	InputRequest{RunID: "r", StepID: "s"},
	RunError{RunID: "r", Err: "boom"},
	RecoveryRequest{RunID: "r", StepID: "s", Err: "failed"},
	IntegrationConflictRequest{RunID: "r", StepID: "s", Paths: []string{"file.go"}},
	FinalMergeRequest{RunID: "r", RunBranch: "b", Base: "main"},
	PromptRequest{RunID: "r", StepID: "s", Label: "Enter value", As: "val"},
	AgentQuestion{RunID: "r", StepID: "s", Request: interaction.QuestionRequest{
		ID: "q1", Fields: []interaction.QuestionField{{ID: "ok", Prompt: "ok?", Kind: interaction.FieldText}},
	}},
	AgentQuestionResolved{RunID: "r", StepID: "s", RequestID: "q1", Action: interaction.ActionAccept},
	StepsReset{RunID: "r", Target: "s", Closure: []string{"s"}, RewindTo: "abc"},
	SecurityFinding{RunID: "r", StepID: "s", Tier: "guard", Monitor: "secret-leak", Severity: "high", Action: "blocked", Fingerprint: "fp"},
}

// TestEventExhaustiveness verifies every Event union member:
//  1. Has a non-empty, non-"unknown" kind in eventKind.
//  2. Has a matching entry in the decoders map.
//  3. Round-trips through MarshalEnvelope → UnmarshalEnvelope.
//
// This is the guard the first draft (spec 10) assumed already existed.
// Add a row to allEventInstances when adding a new event type.
func TestEventExhaustiveness(t *testing.T) {
	for i, ev := range allEventInstances {
		typeName := reflect.TypeOf(ev).Name()

		kind := eventKind(ev)
		if kind == "" || kind == "unknown" {
			t.Errorf("[%d] %s: eventKind returned %q — add a case to eventKind", i, typeName, kind)
			continue
		}

		if _, ok := decoders[kind]; !ok {
			t.Errorf("[%d] %s: kind %q has no decoder in decoders map — add one", i, typeName, kind)
			continue
		}

		line, err := MarshalEnvelope(i+1, ev)
		if err != nil {
			t.Errorf("[%d] %s: MarshalEnvelope error: %v", i, typeName, err)
			continue
		}
		_, decoded, err := UnmarshalEnvelope(line)
		if err != nil {
			t.Errorf("[%d] %s: UnmarshalEnvelope error: %v", i, typeName, err)
			continue
		}
		if decoded == nil {
			t.Errorf("[%d] %s: UnmarshalEnvelope returned nil event (unknown kind %q)", i, typeName, kind)
		}
	}
}

// TestSecurityFindingJournal specifically verifies SecurityFinding round-trips
// and that its ctrl-channel intent is preserved through the journal path.
func TestSecurityFindingJournal(t *testing.T) {
	ev := SecurityFinding{
		RunID: "run1", StepID: "step1",
		Tier: "guard", Monitor: "secret-leak",
		Severity: "critical", Action: "blocked",
		Fingerprint: "abcdef123456",
	}
	line, err := MarshalEnvelope(1, ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	env, decoded, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded == nil {
		t.Fatal("decoded event is nil")
	}
	if env.Kind != "security_finding" {
		t.Errorf("Kind = %q; want security_finding", env.Kind)
	}
	sf, ok := decoded.(SecurityFinding)
	if !ok {
		t.Fatalf("decoded type = %T; want SecurityFinding", decoded)
	}
	if sf.StepID != ev.StepID || sf.Fingerprint != ev.Fingerprint || sf.Severity != ev.Severity {
		t.Errorf("round-trip mismatch: got %+v, want %+v", sf, ev)
	}
}
