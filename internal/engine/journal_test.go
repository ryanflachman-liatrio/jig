package engine

import (
	"encoding/json"
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
				RunID:          "r1",
				StepID:         "fix",
				From:           step.StatusPending,
				To:             step.StatusRunning,
				Attempt:        1,
				Iteration:      2,
				RecoveryAction: RecoverSkip,
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
			name: "RouteSelected",
			ev:   RouteSelected{RunID: "r1", StepID: "fix", RouteIndex: 1, Goto: "research", Iteration: 2, Max: 3},
		},
		{
			name: "RouteCapExceeded",
			ev:   RouteCapExceeded{RunID: "r1", StepID: "fix", RouteIndex: 1, Goto: "research", Iteration: 4, Max: 3},
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
	RouteSelected{RunID: "r", StepID: "s", RouteIndex: 1, Goto: "s", Iteration: 1, Max: 3},
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
	FanOutExpanded{
		SchemaVersion:  FanOutExpandedVersion,
		RunID:          "r",
		FamilyID:       "analyze",
		Generation:     0,
		Iteration:      0,
		ManifestDigest: "deadbeef",
		Instances: []FanOutInstanceDescriptor{
			{InstanceID: "analyze.__fanout__.g000.r000.i0000", Index: 0, ItemSHA256: "aaa"},
		},
	},
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

// TestFanOutExpandedRoundTrip proves FanOutExpanded marshals/unmarshals
// through the journal envelope codec, preserving instance order and every
// field, and lands under the stable "fan_out_expanded" kind.
func TestFanOutExpandedRoundTrip(t *testing.T) {
	ev := FanOutExpanded{
		SchemaVersion:  FanOutExpandedVersion,
		RunID:          "run-1",
		FamilyID:       "analyze",
		Generation:     0,
		Iteration:      1,
		ManifestDigest: "sha256:abcdef",
		Instances: []FanOutInstanceDescriptor{
			{InstanceID: "analyze.__fanout__.g000.r001.i0000", Index: 0, ItemSHA256: "sha-a"},
			{InstanceID: "analyze.__fanout__.g000.r001.i0001", Index: 1, ItemSHA256: "sha-b"},
		},
	}
	line, err := MarshalEnvelope(5, ev)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	env, decoded, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if env.Kind != "fan_out_expanded" {
		t.Errorf("Kind = %q, want fan_out_expanded", env.Kind)
	}
	got, ok := decoded.(FanOutExpanded)
	if !ok {
		t.Fatalf("decoded type = %T, want FanOutExpanded", decoded)
	}
	if got.FamilyID != ev.FamilyID || got.Generation != ev.Generation || got.Iteration != ev.Iteration ||
		got.ManifestDigest != ev.ManifestDigest {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, ev)
	}
	if len(got.Instances) != len(ev.Instances) {
		t.Fatalf("Instances len = %d, want %d", len(got.Instances), len(ev.Instances))
	}
	for i := range ev.Instances {
		if got.Instances[i] != ev.Instances[i] {
			t.Errorf("Instances[%d] = %+v, want %+v", i, got.Instances[i], ev.Instances[i])
		}
	}
}

// TestFanOutExpandedRejectsUnknownVersion proves a manifest/event written by
// a newer, incompatible jig version fails closed at decode time rather than
// being silently misinterpreted — the one event kind with its own schema
// version, since it is the authoritative creation record for runtime children.
func TestFanOutExpandedRejectsUnknownVersion(t *testing.T) {
	line := []byte(`{"seq":1,"ts":"2026-01-01T00:00:00Z","kind":"fan_out_expanded","data":{"schema_version":99,"family_id":"analyze"}}`)
	_, _, err := UnmarshalEnvelope(line)
	if err == nil {
		t.Fatal("expected an error decoding an unsupported fan_out_expanded schema_version")
	}
}

// TestFanOutExpandedNoRawItemData proves the marshaled event never carries
// the raw item value — only ids, positions, and digests — regardless of what
// a caller might mistakenly try to stash on the struct via JSON round-trip.
// The check is structural: FanOutInstanceDescriptor has no field capable of
// holding a raw item, so an encoded instance can only ever contain
// instance_id/index/item_sha256 keys.
func TestFanOutExpandedNoRawItemData(t *testing.T) {
	ev := FanOutExpanded{
		SchemaVersion: FanOutExpandedVersion,
		RunID:         "run-1",
		FamilyID:      "analyze",
		Instances: []FanOutInstanceDescriptor{
			{InstanceID: "analyze.__fanout__.g000.r000.i0000", Index: 0, ItemSHA256: "sha-a"},
		},
	}
	line, err := MarshalEnvelope(1, ev)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	env, _, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	var raw struct {
		Instances []map[string]any `json:"instances"`
	}
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		t.Fatalf("decode raw instances: %v", err)
	}
	if len(raw.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(raw.Instances))
	}
	allowed := map[string]bool{"instance_id": true, "index": true, "item_sha256": true}
	for key := range raw.Instances[0] {
		if !allowed[key] {
			t.Errorf("unexpected key %q in instance descriptor — only id/index/digest are permitted, never raw item data", key)
		}
	}
}

// TestFanOutExpandedBackwardCompatibleReplay proves an older reader that only
// understands RunStarted.Steps (the static author graph) still gets a valid
// baseline when a journal also contains a FanOutExpanded event: the unknown
// kind is skipped, never treated as a fatal replay error, and RunStarted
// itself is untouched by the addition.
func TestFanOutExpandedBackwardCompatibleReplay(t *testing.T) {
	started := RunStarted{RunID: "run-1", Workflow: "fanout", Steps: []string{"discover", "analyze", "synthesize"}}
	startedLine, err := MarshalEnvelope(1, started)
	if err != nil {
		t.Fatalf("MarshalEnvelope(RunStarted): %v", err)
	}
	expanded := FanOutExpanded{
		SchemaVersion: FanOutExpandedVersion,
		RunID:         "run-1",
		FamilyID:      "analyze",
		Instances: []FanOutInstanceDescriptor{
			{InstanceID: "analyze.__fanout__.g000.r000.i0000", Index: 0, ItemSHA256: "sha-a"},
		},
	}
	expandedLine, err := MarshalEnvelope(2, expanded)
	if err != nil {
		t.Fatalf("MarshalEnvelope(FanOutExpanded): %v", err)
	}

	// An "old reader" simulation: decode every envelope, but only look at
	// RunStarted.Steps, ignoring any kind it doesn't recognize (as
	// UnmarshalEnvelope's unknown-kind contract already guarantees for a kind
	// truly absent from its decoders map — here we confirm a *known* kind on
	// the new side degrades to the same "usable baseline" shape).
	var baseline []string
	for _, line := range [][]byte{startedLine, expandedLine} {
		_, ev, err := UnmarshalEnvelope(line)
		if err != nil {
			t.Fatalf("UnmarshalEnvelope: %v", err)
		}
		if rs, ok := ev.(RunStarted); ok {
			baseline = rs.Steps
		}
	}
	if len(baseline) != 3 || baseline[0] != "discover" || baseline[2] != "synthesize" {
		t.Fatalf("baseline = %v, want the static author graph unchanged by FanOutExpanded", baseline)
	}
}
