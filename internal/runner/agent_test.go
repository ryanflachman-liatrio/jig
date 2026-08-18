package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/interaction"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// captureReporter records the liveness signals captureStream emits so tests can
// assert on rep.Message / rep.Output without a live scheduler.
type captureReporter struct {
	messages []captureMsg
	deltas   []string
	findings []engine.SecurityFinding
}

type captureMsg struct{ seq, iteration int }

func (r *captureReporter) Output(delta string)          { r.deltas = append(r.deltas, delta) }
func (r *captureReporter) ToolCall(tool, detail string) {}
func (r *captureReporter) Message(seq, iteration int) {
	r.messages = append(r.messages, captureMsg{seq, iteration})
}
func (r *captureReporter) Question(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
}
func (r *captureReporter) Finding(sf engine.SecurityFinding) {
	r.findings = append(r.findings, sf)
}

// scriptChan turns a fixed event list into the closed channel captureStream
// consumes — mimicking a completed harness event stream with no live backend.
func scriptChan(evts ...harness.Event) <-chan harness.Event {
	ch := make(chan harness.Event, len(evts))
	for _, e := range evts {
		ch <- e
	}
	close(ch)
	return ch
}

func floatPtr(f float64) *float64 { return &f }

// TestCaptureStream_RichCapture drives captureStream with a scripted assistant
// turn (thinking + text + tool_use), a tool_result user turn, and a success
// result, then asserts the transcript holds the expected ordered entries with
// correct block types and tool_use_id correlation.
func TestCaptureStream_RichCapture(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")

	inputJSON, _ := json.Marshal(map[string]any{"file_path": "main.go"})
	events := []harness.Event{
		{Type: harness.EventThinking, Text: "let me think"},
		{Type: harness.EventText, Text: "Reading the file."},
		{Type: harness.EventToolUse, ToolUseID: "toolu_1", Name: "Read", Input: inputJSON},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventToolResult, ToolUseID: "toolu_1", Content: "package main", IsError: false},
		{Type: harness.EventUserEnd},
		{Type: harness.EventResult},
	}

	rep := &captureReporter{}
	req := engine.StepRequest{
		Step:           &workflow.Step{},
		TranscriptPath: tPath,
		Iteration:      2,
		Attempt:        1,
	}

	res, err := captureStream(scriptChan(events...), req, rep, time.Now(), "")
	if err != nil {
		t.Fatalf("captureStream: %v", err)
	}
	if res.Status != step.StatusSucceeded {
		t.Fatalf("want success, got %q: %s", res.Status, res.Err)
	}

	r, _ := transcript.Open(tPath)
	entries, err := r.Window(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (assistant, user); got %d", len(entries))
	}

	// Entry 1: assistant, in order thinking → text → tool_use.
	a := entries[0]
	if a.Role != transcript.RoleAssistant {
		t.Errorf("entry 1 role = %q, want assistant", a.Role)
	}
	if a.Iteration != 2 || a.Attempt != 1 {
		t.Errorf("entry 1 iter/attempt = %d/%d, want 2/1", a.Iteration, a.Attempt)
	}
	wantTypes := []transcript.BlockType{transcript.BlockThinking, transcript.BlockText, transcript.BlockToolUse}
	if len(a.Blocks) != 3 {
		t.Fatalf("entry 1: want 3 blocks, got %d", len(a.Blocks))
	}
	for i, wt := range wantTypes {
		if a.Blocks[i].Type != wt {
			t.Errorf("entry 1 block %d type = %q, want %q", i, a.Blocks[i].Type, wt)
		}
	}
	tu := a.Blocks[2]
	if tu.Name != "Read" || tu.ToolUseID != "toolu_1" {
		t.Errorf("tool_use = %q/%q, want Read/toolu_1", tu.Name, tu.ToolUseID)
	}
	if !strings.Contains(string(tu.Input), "main.go") {
		t.Errorf("tool_use input missing file_path: %s", tu.Input)
	}

	// Entry 2: user tool_result, correlated by tool_use_id.
	u := entries[1]
	if u.Role != transcript.RoleUser || len(u.Blocks) != 1 {
		t.Fatalf("entry 2 = %q with %d blocks, want user with 1", u.Role, len(u.Blocks))
	}
	tr := u.Blocks[0]
	if tr.Type != transcript.BlockToolResult || tr.ToolUseID != "toolu_1" {
		t.Errorf("tool_result = %q/%q, want tool_result/toolu_1", tr.Type, tr.ToolUseID)
	}
	if tr.Content != "package main" || tr.IsError {
		t.Errorf("tool_result content/isError = %q/%v", tr.Content, tr.IsError)
	}

	// Two entries appended ⇒ two liveness signals, carrying seq and iteration.
	if len(rep.messages) != 2 {
		t.Fatalf("want 2 Message signals, got %d", len(rep.messages))
	}
	if rep.messages[0].seq != 1 || rep.messages[1].seq != 2 {
		t.Errorf("Message seqs = %v, want 1,2", rep.messages)
	}
	if rep.messages[0].iteration != 2 {
		t.Errorf("Message iteration = %d, want 2", rep.messages[0].iteration)
	}
}

// TestCaptureStream_StructuredToolResultTruncated verifies structured tool
// content is JSON-encoded to a string and that oversized content is truncated
// with the truncated flag set at write time.
func TestCaptureStream_StructuredToolResultTruncated(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")

	big := strings.Repeat("x", transcript.DefaultMaxBlockBytes+100)
	events := []harness.Event{
		{Type: harness.EventToolResult, ToolUseID: "t1", Content: big, IsError: true},
		{Type: harness.EventToolResult, ToolUseID: "t2", Content: `{"ok":true}`},
		{Type: harness.EventUserEnd},
		{Type: harness.EventResult},
	}
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	if _, err := captureStream(scriptChan(events...), req, &captureReporter{}, time.Now(), ""); err != nil {
		t.Fatal(err)
	}

	r, _ := transcript.Open(tPath)
	entries, _ := r.Window(0, 0)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	blocks := entries[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("want 2 tool_result blocks, got %d", len(blocks))
	}
	if !blocks[0].Truncated || len(blocks[0].Content) > transcript.DefaultMaxBlockBytes {
		t.Errorf("oversized result not truncated: truncated=%v len=%d", blocks[0].Truncated, len(blocks[0].Content))
	}
	if !blocks[0].IsError {
		t.Errorf("is_error flag lost")
	}
	if blocks[1].Content != `{"ok":true}` {
		t.Errorf("structured content = %q, want JSON-encoded", blocks[1].Content)
	}
}

// TestCaptureStream_Artifact verifies the engine writes raw_result.md from the
// agent's last text turn (no agent Write tool required), that OutputPath points
// to it, and that an explicit Step.Output path receives the same content.
func TestCaptureStream_Artifact(t *testing.T) {
	dir := t.TempDir()
	explicitOut := filepath.Join(dir, "explicit.md")
	tPath := filepath.Join(dir, "transcript.jsonl")
	proseText := "# Done\nthe prose answer"

	structured, _ := json.Marshal(map[string]any{
		"summary":     "did the thing",
		"status":      "succeeded",
		"confidence":  "high",
		"issues":      []any{},
		"assumptions": []any{},
	})
	events := []harness.Event{
		{Type: harness.EventText, Text: proseText},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventResult, Structured: structured},
	}
	req := engine.StepRequest{
		Step:           &workflow.Step{Output: explicitOut},
		TranscriptPath: tPath,
	}

	res, err := captureStream(scriptChan(events...), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}

	// OutputPath points to engine-written raw_result.md.
	wantRaw := filepath.Join(dir, "raw_result.md")
	if res.OutputPath != wantRaw {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, wantRaw)
	}
	got, err := os.ReadFile(wantRaw)
	if err != nil {
		t.Fatalf("raw_result.md not written: %v", err)
	}
	if string(got) != proseText {
		t.Errorf("raw_result.md = %q, want %q", string(got), proseText)
	}

	// output.json contains the structured envelope.
	if _, err := os.Stat(filepath.Join(dir, "output.json")); err != nil {
		t.Errorf("output.json not written: %v", err)
	}

	// output.md is rendered from structured metadata fields.
	outMD, err := os.ReadFile(filepath.Join(dir, "output.md"))
	if err != nil {
		t.Fatalf("output.md not written: %v", err)
	}
	for _, want := range []string{"## Status", "succeeded", "## Summary"} {
		if !strings.Contains(string(outMD), want) {
			t.Errorf("output.md missing %q", want)
		}
	}

	// Explicit output path receives the same prose content.
	gotExplicit, err := os.ReadFile(explicitOut)
	if err != nil {
		t.Fatalf("explicit output not written: %v", err)
	}
	if string(gotExplicit) != proseText {
		t.Errorf("explicit output = %q, want prose text", gotExplicit)
	}
}

// TestCaptureStream_NoTranscript verifies that with an empty TranscriptPath no
// file is written and no liveness Message signals fire (persistence off path).
func TestCaptureStream_NoTranscript(t *testing.T) {
	dir := t.TempDir()
	events := []harness.Event{
		{Type: harness.EventText, Text: "hi"},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventResult},
	}
	rep := &captureReporter{}
	req := engine.StepRequest{Step: &workflow.Step{}} // TranscriptPath == ""

	if _, err := captureStream(scriptChan(events...), req, rep, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if len(rep.messages) != 0 {
		t.Errorf("want no Message signals with persistence off, got %d", len(rep.messages))
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(entries) != 0 {
		t.Errorf("no transcript file should be written; found %v", entries)
	}
}

// TestBuildSessionSpec verifies each of a step's model/tool/permission fields
// is translated into the matching SessionSpec field — the fix for the fields
// being parsed and validated but never reaching the backend.
func TestBuildSessionSpec(t *testing.T) {
	st := &workflow.Step{
		Model:             "claude-opus-4-8",
		FallbackModel:     "claude-sonnet-4-6",
		Effort:            workflow.EffortHigh,
		MaxTurns:          20,
		MaxThinkingTokens: 8000,
		MaxBudgetUSD:      5.0,
		PermissionMode:    "acceptEdits",
		AllowedTools:      []string{"Read", "Grep"},
		DisallowedTools:   []string{"Bash"},
	}
	allCaps := harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapUserQuestion, harness.CapSessionResume, harness.CapStructuredOutput, harness.CapPartialStreaming)
	spec, err := buildSessionSpec(st, allCaps)
	if err != nil {
		t.Fatalf("buildSessionSpec: %v", err)
	}
	if spec.Model != "claude-opus-4-8" {
		t.Errorf("Model = %v, want claude-opus-4-8", spec.Model)
	}
	if spec.FallbackModel != "claude-sonnet-4-6" {
		t.Errorf("FallbackModel = %v, want claude-sonnet-4-6", spec.FallbackModel)
	}
	if spec.Effort != "high" {
		t.Errorf("Effort = %v, want high", spec.Effort)
	}
	if spec.MaxTurns != 20 {
		t.Errorf("MaxTurns = %d, want 20", spec.MaxTurns)
	}
	if spec.MaxThinkingTokens != 8000 {
		t.Errorf("MaxThinkingTokens = %d, want 8000", spec.MaxThinkingTokens)
	}
	if spec.MaxBudgetUSD != 5.0 {
		t.Errorf("MaxBudgetUSD = %v, want 5.0", spec.MaxBudgetUSD)
	}
	if spec.PermissionMode != "acceptEdits" {
		t.Errorf("PermissionMode = %v, want acceptEdits", spec.PermissionMode)
	}
	if !reflect.DeepEqual(spec.AllowedTools, []string{"Read", "Grep"}) {
		t.Errorf("AllowedTools = %v, want [Read Grep]", spec.AllowedTools)
	}
	if !reflect.DeepEqual(spec.DisallowedTools, []string{"Bash"}) {
		t.Errorf("DisallowedTools = %v, want [Bash]", spec.DisallowedTools)
	}
	if !spec.Partial {
		t.Errorf("Partial = false, want true (unconditional partial streaming)")
	}
}

// TestBuildSessionSpec_Empty verifies a zero-value step leaves every optional
// field unset so the harness's own defaults apply, but always sets Schema to
// enforce the base schema.
func TestBuildSessionSpec_Empty(t *testing.T) {
	allCaps := harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapUserQuestion, harness.CapSessionResume, harness.CapStructuredOutput, harness.CapPartialStreaming)
	spec, err := buildSessionSpec(&workflow.Step{}, allCaps)
	if err != nil {
		t.Fatalf("buildSessionSpec: %v", err)
	}
	if spec.Model != "" || spec.FallbackModel != "" || spec.Effort != "" ||
		spec.PermissionMode != "" || spec.MaxBudgetUSD != 0 {
		t.Errorf("zero-value step should leave optional fields unset: %+v", spec)
	}
	if spec.MaxTurns != 0 || spec.MaxThinkingTokens != 0 {
		t.Errorf("zero-value step should leave numeric fields at 0: %+v", spec)
	}
	if len(spec.AllowedTools) != 0 || len(spec.DisallowedTools) != 0 {
		t.Errorf("zero-value step should leave tool lists empty: %+v", spec)
	}
	// Base schema is always enforced — Schema must be set even with no
	// declared [step.schema].
	if spec.Schema == nil {
		t.Fatalf("Schema = nil, want base schema always applied")
	}
	props, ok := spec.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("base schema properties missing: %v", spec.Schema)
	}
	for _, base := range []string{"summary", "status", "confidence", "issues", "assumptions"} {
		if _, ok := props[base]; !ok {
			t.Errorf("base schema missing required field %q: %v", base, props)
		}
	}
}

// TestBuildSessionSpec_Schema verifies a step.schema is merged with the base
// schema and translated into SessionSpec.Schema. Both base and declared
// fields must appear in the compiled JSON Schema.
func TestBuildSessionSpec_Schema(t *testing.T) {
	st := &workflow.Step{
		Schema: &workflow.Schema{Fields: []*workflow.Field{
			{Name: "passed", Type: workflow.FieldBool},
		}},
	}
	allCaps := harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapUserQuestion, harness.CapSessionResume, harness.CapStructuredOutput, harness.CapPartialStreaming)
	spec, err := buildSessionSpec(st, allCaps)
	if err != nil {
		t.Fatalf("buildSessionSpec: %v", err)
	}
	if spec.Schema == nil {
		t.Fatalf("Schema = nil, want json schema")
	}
	props, ok := spec.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing or wrong type: %v", spec.Schema)
	}
	// Declared field must be present.
	if _, ok := props["passed"]; !ok {
		t.Errorf("schema missing declared field 'passed': %v", props)
	}
	// Base schema fields must also be present.
	for _, base := range []string{"summary", "status", "confidence", "issues", "assumptions"} {
		if _, ok := props[base]; !ok {
			t.Errorf("schema missing base field %q: %v", base, props)
		}
	}
}

// TestCaptureStream_StructuredOutput verifies a result event's Structured
// payload is captured into step.Result.Structured — the field block_on/when/
// loop.when guards read to evaluate schema-field conditions.
func TestCaptureStream_StructuredOutput(t *testing.T) {
	dir := t.TempDir()
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "transcript.jsonl")}

	structured, _ := json.Marshal(map[string]any{"needs_input": true, "question": "which threat model?"})
	res, err := captureStream(scriptChan(harness.Event{Type: harness.EventResult, Structured: structured}), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != step.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %s", res.Status, res.Err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Structured, &got); err != nil {
		t.Fatalf("Structured is not valid JSON: %v", err)
	}
	if got["needs_input"] != true {
		t.Errorf("Structured[needs_input] = %v, want true", got["needs_input"])
	}
	if got["question"] != "which threat model?" {
		t.Errorf("Structured[question] = %v", got["question"])
	}
}

// TestCaptureStream_AssistantError verifies an EventSystemText fired after an
// assistant turn is surfaced as a system transcript entry rather than silently
// dropped.
func TestCaptureStream_AssistantError(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	events := []harness.Event{
		{Type: harness.EventText, Text: "hold on"},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventSystemText, Text: "assistant error: rate_limit"},
		{Type: harness.EventResult},
	}
	if _, err := captureStream(scriptChan(events...), req, &captureReporter{}, time.Now(), ""); err != nil {
		t.Fatal(err)
	}

	r, _ := transcript.Open(tPath)
	entries, _ := r.Window(0, 0)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (assistant, system error); got %d", len(entries))
	}
	sys := entries[1]
	if sys.Role != transcript.RoleSystem {
		t.Errorf("entry 2 role = %q, want system", sys.Role)
	}
	if len(sys.Blocks) != 1 || !strings.Contains(sys.Blocks[0].Text, "rate_limit") {
		t.Errorf("entry 2 blocks = %v, want text containing rate_limit", sys.Blocks)
	}
}

// TestCaptureStream_Subtype verifies EventResult.Subtype lands on step.Result
// for both the success and failure paths, and that policy-limit subtypes
// produce descriptive human-readable error messages (computed upstream by the
// harness, e.g. claude.go's subtypeErrText, and passed through via ErrText).
func TestCaptureStream_Subtype(t *testing.T) {
	dir := t.TempDir()
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "success.jsonl")}

	ok := harness.Event{Type: harness.EventResult, Subtype: "success"}
	res, err := captureStream(scriptChan(ok), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Subtype != "success" {
		t.Errorf("success Subtype = %q, want success", res.Subtype)
	}

	// error_max_turns: descriptive prefix + harness-computed detail, passed
	// straight through via ErrText.
	req2 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "fail.jsonl")}
	failed := harness.Event{
		Type:    harness.EventResult,
		IsError: true,
		Subtype: "error_max_turns",
		ErrText: "agent reached the maximum turn limit: hit turn limit",
	}
	res2, err := captureStream(scriptChan(failed), req2, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Subtype != "error_max_turns" {
		t.Errorf("failure Subtype = %q, want error_max_turns", res2.Subtype)
	}
	if !strings.Contains(res2.Err, "maximum turn limit") {
		t.Errorf("failure Err = %q, want descriptive turn-limit message", res2.Err)
	}
	if !strings.Contains(res2.Err, "hit turn limit") {
		t.Errorf("failure Err = %q, want it to include the harness Errors detail", res2.Err)
	}

	// error_max_budget_usd: descriptive prefix, no additional detail.
	req3 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "budget.jsonl")}
	budget := harness.Event{
		Type:    harness.EventResult,
		IsError: true,
		Subtype: "error_max_budget_usd",
		ErrText: "agent exceeded the maximum USD budget",
	}
	res3, err := captureStream(scriptChan(budget), req3, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res3.Subtype != "error_max_budget_usd" {
		t.Errorf("budget Subtype = %q, want error_max_budget_usd", res3.Subtype)
	}
	if !strings.Contains(res3.Err, "maximum USD budget") {
		t.Errorf("budget Err = %q, want descriptive budget message", res3.Err)
	}
}

// TestCaptureStream_ErrorResult verifies an error result event yields a failed
// Result and records a result entry.
func TestCaptureStream_ErrorResult(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	errEvt := harness.Event{Type: harness.EventResult, IsError: true, ErrText: "boom"}
	res, err := captureStream(scriptChan(errEvt), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != step.StatusFailed {
		t.Errorf("status = %q, want failed", res.Status)
	}
	if res.Err != "boom" {
		t.Errorf("err = %q, want boom", res.Err)
	}
	r, _ := transcript.Open(tPath)
	entries, _ := r.Window(0, 0)
	if len(entries) != 1 || entries[0].Role != transcript.RoleResult {
		t.Fatalf("want 1 result entry, got %v", entries)
	}
}

// TestSessionIDCapturedAtStart proves the spec-07 B2 fix: the backend session
// id is captured from an early EventSessionID and survives a mid-turn stop.
// Here the stream carries an EventSessionID and then closes *before* any
// EventResult — exactly what a cancelled worker sees. The returned Result must
// still carry the session id so the engine can resume.
func TestSessionIDCapturedAtStart(t *testing.T) {
	sessEvt := harness.Event{Type: harness.EventSessionID, SessionID: "sess-early"}
	// No EventResult: the channel closes after the session id event, mimicking
	// a context cancellation cutting the turn short.
	res, err := captureStream(scriptChan(sessEvt), engine.StepRequest{Step: &workflow.Step{}}, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatalf("captureStream: %v", err)
	}
	if res.Status != step.StatusFailed {
		t.Fatalf("status = %q, want failed (connection closed mid-turn)", res.Status)
	}
	if res.SessionID != "sess-early" {
		t.Fatalf("SessionID = %q, want sess-early (captured at start, survives a stop)", res.SessionID)
	}
}

// TestSessionIDCapturedFromSystemMessage verifies an early EventSessionID
// (e.g. from the backend's init message) is also honored as a session-id
// source.
func TestSessionIDCapturedFromSystemMessage(t *testing.T) {
	sessEvt := harness.Event{Type: harness.EventSessionID, SessionID: "sess-sys"}
	res, err := captureStream(scriptChan(sessEvt), engine.StepRequest{Step: &workflow.Step{}}, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatalf("captureStream: %v", err)
	}
	if res.SessionID != "sess-sys" {
		t.Fatalf("SessionID = %q, want sess-sys", res.SessionID)
	}
}

// TestCaptureStream_ResumeAppends proves the transcript is a log that a resume
// appends to, never truncates (spec 07 invariant). Two captureStream calls write
// to the same transcript path — the first turn, then a resumed turn — and the
// second call's entries are appended after the first's, which remain intact.
func TestCaptureStream_ResumeAppends(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	// First turn.
	turnOne := []harness.Event{
		{Type: harness.EventText, Text: "turn one"},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventResult},
	}
	if _, err := captureStream(scriptChan(turnOne...), req, &captureReporter{}, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	r, _ := transcript.Open(tPath)
	firstCount := len(mustWindow(t, r))

	// Resumed turn: the human message that triggered the resume plus a new answer.
	turnTwo := []harness.Event{
		{Type: harness.EventText, Text: "turn two"},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventResult},
	}
	if _, err := captureStream(scriptChan(turnTwo...), req, &captureReporter{}, time.Now(), "continue please"); err != nil {
		t.Fatal(err)
	}

	r2, _ := transcript.Open(tPath)
	entries := mustWindow(t, r2)
	if len(entries) <= firstCount {
		t.Fatalf("resume did not append: entries went from %d to %d", firstCount, len(entries))
	}
	// The first turn is still the first entry (not truncated/overwritten).
	if entries[0].Blocks[0].Text != "turn one" {
		t.Fatalf("first entry = %q, want the preserved 'turn one'", entries[0].Blocks[0].Text)
	}
	// The resumed human message and answer are appended after it.
	var sawContinue, sawTurnTwo bool
	for _, e := range entries[firstCount:] {
		for _, b := range e.Blocks {
			if b.Text == "continue please" {
				sawContinue = true
			}
			if b.Text == "turn two" {
				sawTurnTwo = true
			}
		}
	}
	if !sawContinue || !sawTurnTwo {
		t.Fatalf("appended entries missing resume content: sawContinue=%v sawTurnTwo=%v", sawContinue, sawTurnTwo)
	}
}

func mustWindow(t *testing.T, r *transcript.Reader) []transcript.Entry {
	t.Helper()
	entries, err := r.Window(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestBuildAgentPromptEmptyContext is the regression lock: with an empty
// WorkflowContext the built prompt is byte-identical to the pre-feature
// four-part prompt (body → append → inputs → feedback). This guards the
// persistence-off / inject_context = false path.
func TestBuildAgentPromptEmptyContext(t *testing.T) {
	req := engine.StepRequest{
		Step:   &workflow.Step{AppendSystemPrompt: "Be concise."},
		Inputs: []engine.ResolvedInput{{Ref: workflow.Input{Path: "notes.md"}, Value: "/abs/notes.md"}},
	}
	const want = "Be concise.\n\n" +
		"The following inputs are provided for your task:\n\n" +
		"[Reference document]: /abs/notes.md"
	got := buildAgentPrompt(req)
	if got != want {
		t.Errorf("empty-context prompt changed (regression):\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
	if strings.Contains(got, "## Workflow context") || strings.HasPrefix(got, "\n") {
		t.Errorf("empty-context prompt must carry no preamble, got:\n%q", got)
	}
}

// TestBuildAgentPromptPrependsContext verifies a non-empty WorkflowContext is
// prepended at the very front, ended by its `---` delimiter, ahead of the body —
// and that it leaves the remaining four pieces byte-identical to the no-context
// prompt.
func TestBuildAgentPromptPrependsContext(t *testing.T) {
	const preamble = "## Workflow context\n\nYou are step `x` in workflow `y`.\n\n---"
	req := engine.StepRequest{
		Step:            &workflow.Step{AppendSystemPrompt: "Be concise."},
		Inputs:          []engine.ResolvedInput{{Ref: workflow.Input{Path: "notes.md"}, Value: "/abs/notes.md"}},
		WorkflowContext: preamble,
	}
	got := buildAgentPrompt(req)

	// Preamble at the front, delimiter then a blank line before the body.
	if !strings.HasPrefix(got, preamble+"\n\n") {
		t.Errorf("preamble not prepended at front with delimiter:\n%q", got)
	}
	// Everything after the preamble equals the no-context prompt, byte-for-byte.
	reqEmpty := req
	reqEmpty.WorkflowContext = ""
	if want := preamble + "\n\n" + buildAgentPrompt(reqEmpty); got != want {
		t.Errorf("prepend changed the body:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
	// Order preserved: context precedes the body.
	if strings.Index(got, "Workflow context") > strings.Index(got, "Be concise.") {
		t.Error("preamble must appear before the body")
	}
}

// TestBlockedAndRedacted proves the transcript-redaction and finding-production
// paths when the Tier-1 guard is active. The test drives a scripted harness
// event stream (no live Claude Code CLI required) carrying two tool_use events
// in one assistant turn, one whose input contains a fake AWS key.
//
// The test asserts:
//   - The transcript.jsonl entry has the key redacted (no raw AKIAIOSFODNN7EXAMPLE).
//   - The captureReporter received a SecurityFinding via rep.Finding.
//   - The findings.jsonl file on disk holds exactly one blocked finding.
//   - A tool_use block without secrets passes through unmodified.
func TestBlockedAndRedacted(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	fPath := filepath.Join(dir, "findings.jsonl")

	const fakeKey = "AKIAIOSFODNN7EXAMPLE"

	// Two tool_use events in one assistant turn — one with a secret, one clean.
	secretInputJSON, _ := json.Marshal(map[string]any{
		"file_path": "config.txt",
		"content":   "aws_access_key_id = " + fakeKey,
	})
	cleanInputJSON, _ := json.Marshal(map[string]any{
		"file_path": "main.go",
		"content":   "package main\n",
	})

	events := []harness.Event{
		{Type: harness.EventToolUse, ToolUseID: "tu1", Name: "Write", Input: secretInputJSON},
		{Type: harness.EventToolUse, ToolUseID: "tu2", Name: "Write", Input: cleanInputJSON},
		{Type: harness.EventAssistantEnd},
		{Type: harness.EventResult},
	}

	rep := &captureReporter{}
	guard := sentinel.NewGuard(nil) // nil allowlist = outbound rule disabled
	req := engine.StepRequest{
		RunID:          "run1",
		Step:           &workflow.Step{ID: "impl"},
		TranscriptPath: tPath,
		FindingsPath:   fPath,
		Guard:          guard,
		Iteration:      1,
	}

	res, err := captureStream(scriptChan(events...), req, rep, time.Now(), "")
	if err != nil {
		t.Fatalf("captureStream: %v", err)
	}
	if res.Status != step.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %s", res.Status, res.Err)
	}

	// --- Transcript assertions ---
	r, _ := transcript.Open(tPath)
	entries, _ := r.Window(0, 0)
	if len(entries) != 1 {
		t.Fatalf("want 1 transcript entry (assistant), got %d", len(entries))
	}
	blocks := entries[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("want 2 tool_use blocks, got %d", len(blocks))
	}

	// Block 0: secret Write — raw key must be absent, redacted form present.
	b0 := string(blocks[0].Input)
	if strings.Contains(b0, fakeKey) {
		t.Errorf("transcript block 0 contains raw key (not redacted): %s", b0)
	}
	if !strings.Contains(b0, "aws-key") {
		t.Errorf("transcript block 0 missing redaction marker 'aws-key': %s", b0)
	}

	// Block 1: clean Write — input must be byte-identical to the original JSON.
	b1 := string(blocks[1].Input)
	if !strings.Contains(b1, "main.go") || strings.Contains(b1, "aws-key") {
		t.Errorf("transcript block 1 (clean) was unexpectedly modified: %s", b1)
	}

	// --- Reporter assertions ---
	if len(rep.findings) != 1 {
		t.Fatalf("want 1 SecurityFinding emitted, got %d", len(rep.findings))
	}
	sf := rep.findings[0]
	if sf.Tier != "guard" {
		t.Errorf("finding Tier = %q, want guard", sf.Tier)
	}
	if sf.Action != "blocked" {
		t.Errorf("finding Action = %q, want blocked", sf.Action)
	}
	if sf.Monitor != "secret-in-write" {
		t.Errorf("finding Monitor = %q, want secret-in-write", sf.Monitor)
	}

	// --- findings.jsonl assertions ---
	fFindings, err := sentinel.ReadAll(fPath)
	if err != nil {
		t.Fatalf("ReadAll findings: %v", err)
	}
	if len(fFindings) != 1 {
		t.Fatalf("want 1 finding on disk, got %d", len(fFindings))
	}
	if strings.Contains(fFindings[0].Detail, fakeKey) {
		t.Errorf("finding Detail contains raw key: %s", fFindings[0].Detail)
	}
	if fFindings[0].Action != sentinel.ActionBlocked {
		t.Errorf("finding Action = %q, want blocked", fFindings[0].Action)
	}
}

// TestCostCapture verifies that captureStream populates TotalCostUSD from the
// harness result event. A result without cost yields a nil pointer (not $0.00).
func TestCostCapture(t *testing.T) {
	dir := t.TempDir()

	// Success path: result event carries cost.
	cost := 0.0034
	ok := harness.Event{Type: harness.EventResult, TotalCostUSD: floatPtr(cost)}
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "t1.jsonl")}
	res, err := captureStream(scriptChan(ok), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCostUSD == nil {
		t.Fatal("TotalCostUSD is nil, want non-nil pointer")
	}
	if *res.TotalCostUSD != cost {
		t.Errorf("TotalCostUSD = %v, want %v", *res.TotalCostUSD, cost)
	}

	// Success path: result event without cost yields nil pointer (not 0.0).
	noCost := harness.Event{Type: harness.EventResult}
	req2 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "t2.jsonl")}
	res2, err := captureStream(scriptChan(noCost), req2, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res2.TotalCostUSD != nil {
		t.Errorf("TotalCostUSD = %v, want nil for unreported cost", res2.TotalCostUSD)
	}

	// Error path: cost is preserved even on error result event.
	errEvt := harness.Event{Type: harness.EventResult, IsError: true, TotalCostUSD: floatPtr(0.0012)}
	req3 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "t3.jsonl")}
	res3, err := captureStream(scriptChan(errEvt), req3, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res3.TotalCostUSD == nil || *res3.TotalCostUSD != 0.0012 {
		t.Errorf("error-path TotalCostUSD = %v, want 0.0012", res3.TotalCostUSD)
	}
}

// TestBuildSessionSpec_CapabilityGated verifies Schema and Partial — set
// unconditionally for every step regardless of user intent — are only
// forwarded to the SessionSpec when the active harness advertises the
// matching capability, rather than always being set and left for Open to
// reject (spec 12 task 5.4).
func TestBuildSessionSpec_CapabilityGated(t *testing.T) {
	st := &workflow.Step{}

	full := harness.NewCapabilitySet(harness.CapStructuredOutput, harness.CapPartialStreaming)
	spec, err := buildSessionSpec(st, full)
	if err != nil {
		t.Fatalf("buildSessionSpec: %v", err)
	}
	if spec.Schema == nil {
		t.Errorf("Schema = nil, want base schema when CapStructuredOutput is advertised")
	}
	if !spec.Partial {
		t.Errorf("Partial = false, want true when CapPartialStreaming is advertised")
	}

	none := harness.NewCapabilitySet()
	spec2, err := buildSessionSpec(st, none)
	if err != nil {
		t.Fatalf("buildSessionSpec: %v", err)
	}
	if spec2.Schema != nil {
		t.Errorf("Schema = %v, want nil when CapStructuredOutput is not advertised", spec2.Schema)
	}
	if spec2.Partial {
		t.Errorf("Partial = true, want false when CapPartialStreaming is not advertised")
	}
}

// TestExecute_GuardSemantics is the spec 12 task 5.5 table test: it drives
// AgentExecutor.Execute against harnesses with varying capability sets and
// asserts the guard callback fires, is fail-closed rejected, or is bypassed
// as expected — without any live backend.
func TestExecute_GuardSemantics(t *testing.T) {
	guard := sentinel.NewGuard(nil)

	t.Run("guarded step + capable harness fires the callback", func(t *testing.T) {
		sess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
		h := &harness.FakeHarness{
			NameVal: "claude",
			Caps:    harness.NewCapabilitySet(harness.CapPermissionCallback),
			Sess:    sess,
		}
		e := NewAgentExecutorFixed(h)
		req := engine.StepRequest{Step: &workflow.Step{}, Guard: guard}
		res, err := e.Execute(context.Background(), req, &captureReporter{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != step.StatusSucceeded {
			t.Fatalf("status = %q, want succeeded: %s", res.Status, res.Err)
		}
		if h.OpenSpec.Permission == nil {
			t.Error("SessionSpec.Permission not set, want the guard callback wired")
		}
	})

	t.Run("guarded step + harness lacking CapPermissionCallback fails closed", func(t *testing.T) {
		h := &harness.FakeHarness{NameVal: "acp-nopermcap", Caps: harness.NewCapabilitySet()}
		e := NewAgentExecutorFixed(h)
		req := engine.StepRequest{Step: &workflow.Step{}, Guard: guard}
		res, err := e.Execute(context.Background(), req, &captureReporter{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != step.StatusFailed {
			t.Fatalf("status = %q, want failed", res.Status)
		}
		if !strings.Contains(res.Err, "CapPermissionCallback") {
			t.Errorf("Err = %q, want it to name CapPermissionCallback", res.Err)
		}
		if !strings.Contains(res.Err, "acp-nopermcap") {
			t.Errorf("Err = %q, want it to name the harness", res.Err)
		}
	})

	t.Run("acceptEdits step still wires the guard callback unchanged", func(t *testing.T) {
		sess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
		h := &harness.FakeHarness{
			NameVal: "claude",
			Caps:    harness.NewCapabilitySet(harness.CapPermissionCallback),
			Sess:    sess,
		}
		e := NewAgentExecutorFixed(h)
		req := engine.StepRequest{Step: &workflow.Step{PermissionMode: "acceptEdits"}, Guard: guard}
		res, err := e.Execute(context.Background(), req, &captureReporter{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != step.StatusSucceeded {
			t.Fatalf("status = %q, want succeeded: %s", res.Status, res.Err)
		}
		if h.OpenSpec.PermissionMode != "acceptEdits" {
			t.Errorf("PermissionMode = %q, want acceptEdits passed through unchanged", h.OpenSpec.PermissionMode)
		}
		if h.OpenSpec.Permission == nil {
			t.Error("SessionSpec.Permission not set, want the guard callback still wired under acceptEdits")
		}
	})

	t.Run("guarded step + AcpHarness real permission round-trip fires", func(t *testing.T) {
		var decided bool
		h := &harness.FakeHarness{
			NameVal: "acp",
			Caps:    harness.NewCapabilitySet(harness.CapPermissionCallback),
			Sess:    harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}}),
		}
		e := NewAgentExecutorFixed(h)
		req := engine.StepRequest{Step: &workflow.Step{}, Guard: guard}
		if _, err := e.Execute(context.Background(), req, &captureReporter{}); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		// Drive the wired callback directly, standing in for AcpHarness invoking
		// it mid-turn during the real session/request_permission round-trip
		// (Unit 4's acpSession.run) — proves the same PermissionFn wiring that
		// ClaudeHarness uses also reaches AcpHarness's Open call unmodified.
		if h.OpenSpec.Permission == nil {
			t.Fatal("SessionSpec.Permission not set")
		}
		dec := h.OpenSpec.Permission("Read", map[string]any{"file_path": "x"})
		decided = true
		if !decided || !dec.Allow {
			t.Errorf("decision = %+v, want Allow=true for a harmless Read", dec)
		}
	})
}

// TestExecute_DeclaredSchemaRequiresStructuredOutput verifies a step that
// explicitly declares [step.schema] fails closed against a harness lacking
// CapStructuredOutput, while a step with no declared schema (only the
// always-applied base schema) is left to buildSessionSpec's silent omission
// instead (spec 12 task 5.2/5.4).
func TestExecute_DeclaredSchemaRequiresStructuredOutput(t *testing.T) {
	h := &harness.FakeHarness{NameVal: "acp", Caps: harness.NewCapabilitySet()}
	e := NewAgentExecutorFixed(h)
	st := &workflow.Step{Schema: &workflow.Schema{Fields: []*workflow.Field{{Name: "passed", Type: workflow.FieldBool}}}}
	req := engine.StepRequest{Step: st}

	res, err := e.Execute(context.Background(), req, &captureReporter{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != step.StatusFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Err, "CapStructuredOutput") {
		t.Errorf("Err = %q, want it to name CapStructuredOutput", res.Err)
	}

	// No declared schema: the base schema is silently omitted, not rejected.
	h2 := &harness.FakeHarness{
		NameVal: "acp",
		Caps:    harness.NewCapabilitySet(),
		Sess:    harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}}),
	}
	e2 := NewAgentExecutorFixed(h2)
	req2 := engine.StepRequest{Step: &workflow.Step{}}
	res2, err := e2.Execute(context.Background(), req2, &captureReporter{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res2.Status != step.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (no explicit schema opt-in): %s", res2.Status, res2.Err)
	}
	if h2.OpenSpec.Schema != nil {
		t.Errorf("OpenSpec.Schema = %v, want nil (silently omitted)", h2.OpenSpec.Schema)
	}
}

// TestExecute_BlockOnRequiresSessionResume is the spec 12 task 5.6 fail-fast
// test: a step declaring block_on run against a harness lacking
// CapSessionResume is rejected at the first Open() call, before any partial
// execution — asserted here by confirming Open is never even reached (the
// FakeHarness's OpenSpec stays zero-valued and its session's events channel is
// never touched).
func TestExecute_BlockOnRequiresSessionResume(t *testing.T) {
	sess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
	h := &harness.FakeHarness{NameVal: "acp", Caps: harness.NewCapabilitySet(), Sess: sess}
	e := NewAgentExecutorFixed(h)
	req := engine.StepRequest{Step: &workflow.Step{BlockOn: "output.needs_input == true"}}

	res, err := e.Execute(context.Background(), req, &captureReporter{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != step.StatusFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Err, "block_on") || !strings.Contains(res.Err, "CapSessionResume") {
		t.Errorf("Err = %q, want it to name block_on and CapSessionResume", res.Err)
	}
	// No partial execution: Open was never called, so OpenSpec is still zero.
	if h.OpenSpec.Prompt != "" {
		t.Errorf("OpenSpec.Prompt = %q, want empty — Open should not have been called", h.OpenSpec.Prompt)
	}
	if sess.Closed {
		t.Error("session Close called, want the session to never have been opened")
	}
}

// TestExecute_SelectsHarnessPerStep proves AgentExecutor looks up the harness
// from the step's Backend/Transport on each Execute, so one executor can drive
// both SDK and ACP steps.
func TestExecute_SelectsHarnessPerStep(t *testing.T) {
	sdkSess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
	acpSess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
	sdkH := &harness.FakeHarness{NameVal: "claude", Caps: harness.NewCapabilitySet(), Sess: sdkSess}
	acpH := &harness.FakeHarness{NameVal: "acp", Caps: harness.NewCapabilitySet(), Sess: acpSess}

	var got []string
	e := NewAgentExecutor(func(backend, transport string) (harness.Harness, error) {
		got = append(got, backend+"/"+transport)
		switch transport {
		case "acp":
			return acpH, nil
		default:
			return sdkH, nil
		}
	})

	reqSDK := engine.StepRequest{Step: &workflow.Step{Backend: "claude", Transport: "sdk"}}
	if _, err := e.Execute(context.Background(), reqSDK, &captureReporter{}); err != nil {
		t.Fatalf("sdk Execute: %v", err)
	}
	reqACP := engine.StepRequest{Step: &workflow.Step{Backend: "claude", Transport: "acp"}}
	if _, err := e.Execute(context.Background(), reqACP, &captureReporter{}); err != nil {
		t.Fatalf("acp Execute: %v", err)
	}

	if len(got) != 2 || got[0] != "claude/sdk" || got[1] != "claude/acp" {
		t.Errorf("lookups = %v, want [claude/sdk claude/acp]", got)
	}
	if !sdkSess.Closed {
		t.Error("sdk session not closed — Open/Execute did not run against sdk harness")
	}
	if !acpSess.Closed {
		t.Error("acp session not closed — Open/Execute did not run against acp harness")
	}
}

// TestExecute_AcpProfile verifies the fail-closed gate outcomes for the ACP
// harness capability set, focusing on CapStructuredOutput (prompt-injection
// structured output, new in this harness).
func TestExecute_AcpProfile(t *testing.T) {
	acpCaps := harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput)

	t.Run("schema step + acp caps → passes (CapStructuredOutput advertised)", func(t *testing.T) {
		sess := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult}})
		h := &harness.FakeHarness{NameVal: "acp", Caps: acpCaps, Sess: sess}
		e := NewAgentExecutorFixed(h)
		st := &workflow.Step{Schema: &workflow.Schema{Fields: []*workflow.Field{{Name: "ok", Type: workflow.FieldBool}}}}
		req := engine.StepRequest{Step: st}
		res, err := e.Execute(context.Background(), req, &captureReporter{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != step.StatusSucceeded {
			t.Fatalf("status = %q, want succeeded (schema accepted via CapStructuredOutput): %s", res.Status, res.Err)
		}
	})

	t.Run("AskUserQuestion + acp caps without CapUserQuestion → rejection", func(t *testing.T) {
		h := &harness.FakeHarness{NameVal: "acp", Caps: acpCaps}
		e := NewAgentExecutorFixed(h)
		st := &workflow.Step{AllowedTools: []string{"AskUserQuestion"}}
		req := engine.StepRequest{Step: st}
		res, err := e.Execute(context.Background(), req, &captureReporter{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != step.StatusFailed {
			t.Fatalf("status = %q, want failed", res.Status)
		}
		if !strings.Contains(res.Err, "CapUserQuestion") {
			t.Errorf("Err = %q, want it to name CapUserQuestion", res.Err)
		}
	})

	t.Run("real NewAcpHarness() advertises expected capabilities", func(t *testing.T) {
		h := harness.NewAcpHarness()
		caps := h.Capabilities()
		for _, want := range []harness.Capability{harness.CapPermissionCallback, harness.CapUserQuestion, harness.CapPartialStreaming, harness.CapStructuredOutput} {
			if !caps.Has(want) {
				t.Errorf("AcpHarness.Capabilities() missing %v", want)
			}
		}
		for _, reject := range []harness.Capability{harness.CapSessionResume} {
			if caps.Has(reject) {
				t.Errorf("AcpHarness.Capabilities() unexpectedly has %v", reject)
			}
		}
	})
}
