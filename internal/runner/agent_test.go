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

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
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
func (r *captureReporter) Question(_ context.Context, _ string, _ []engine.AgentQuestionItem) string {
	return ""
}
func (r *captureReporter) Finding(sf engine.SecurityFinding) {
	r.findings = append(r.findings, sf)
}

// scriptChan turns a fixed message list into the closed channel captureStream
// consumes — mimicking a completed SDK stream with no live connection.
func scriptChan(msgs ...claudecode.Message) <-chan claudecode.Message {
	ch := make(chan claudecode.Message, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return ch
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// TestCaptureStream_RichCapture drives captureStream with a scripted assistant
// turn (thinking + text + tool_use), a tool_result user message, and a success
// result, then asserts the transcript holds the expected ordered entries with
// correct block types and tool_use_id correlation.
func TestCaptureStream_RichCapture(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")

	assistant := &claudecode.AssistantMessage{
		Content: []claudecode.ContentBlock{
			&claudecode.ThinkingBlock{Thinking: "let me think"},
			&claudecode.TextBlock{Text: "Reading the file."},
			&claudecode.ToolUseBlock{ToolUseID: "toolu_1", Name: "Read", Input: map[string]any{"file_path": "main.go"}},
		},
	}
	user := &claudecode.UserMessage{
		Content: []claudecode.ContentBlock{
			&claudecode.ToolResultBlock{ToolUseID: "toolu_1", Content: "package main", IsError: boolPtr(false)},
		},
	}
	result := &claudecode.ResultMessage{IsError: false}

	rep := &captureReporter{}
	req := engine.StepRequest{
		Step:           &workflow.Step{},
		TranscriptPath: tPath,
		Iteration:      2,
		Attempt:        1,
	}

	res, err := captureStream(scriptChan(assistant, user, result), req, rep, time.Now(), "")
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
	user := &claudecode.UserMessage{
		Content: []claudecode.ContentBlock{
			&claudecode.ToolResultBlock{ToolUseID: "t1", Content: big, IsError: boolPtr(true)},
			&claudecode.ToolResultBlock{ToolUseID: "t2", Content: map[string]any{"ok": true}},
		},
	}
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	if _, err := captureStream(scriptChan(user, &claudecode.ResultMessage{}), req, &captureReporter{}, time.Now(), ""); err != nil {
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

// TestCaptureStream_Artifact verifies output.md is written from the raw_result
// base-schema field (not the streaming assistant text), and that an explicit
// step.Output path receives the same content as a named artifact copy.
func TestCaptureStream_Artifact(t *testing.T) {
	dir := t.TempDir()
	explicitOut := filepath.Join(dir, "explicit.md")
	tPath := filepath.Join(dir, "transcript.jsonl")

	result := &claudecode.ResultMessage{
		StructuredOutput: map[string]any{
			"raw_result":  "# Done\nthe prose answer",
			"summary":     "did the thing",
			"status":      "succeeded",
			"confidence":  "high",
			"issues":      []any{},
			"assumptions": []any{},
		},
	}
	req := engine.StepRequest{
		Step:           &workflow.Step{Output: explicitOut},
		TranscriptPath: tPath,
	}

	res, err := captureStream(scriptChan(result), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}

	// OutputPath points to the canonical run-dir output.md.
	wantOutputMD := filepath.Join(dir, "output.md")
	if res.OutputPath != wantOutputMD {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, wantOutputMD)
	}

	// Canonical output.md contains raw_result.
	got, err := os.ReadFile(wantOutputMD)
	if err != nil {
		t.Fatalf("output.md not written: %v", err)
	}
	if string(got) != "# Done\nthe prose answer" {
		t.Errorf("output.md = %q, want raw_result prose", got)
	}

	// output.json contains the full structured envelope.
	outJSON := filepath.Join(dir, "output.json")
	if _, err := os.Stat(outJSON); err != nil {
		t.Errorf("output.json not written: %v", err)
	}

	// The explicit output path also receives raw_result.
	gotExplicit, err := os.ReadFile(explicitOut)
	if err != nil {
		t.Fatalf("explicit output not written: %v", err)
	}
	if string(gotExplicit) != "# Done\nthe prose answer" {
		t.Errorf("explicit output = %q, want raw_result prose", gotExplicit)
	}
}

// TestCaptureStream_NoTranscript verifies that with an empty TranscriptPath no
// file is written and no liveness Message signals fire (persistence off path).
func TestCaptureStream_NoTranscript(t *testing.T) {
	dir := t.TempDir()
	assistant := &claudecode.AssistantMessage{Content: []claudecode.ContentBlock{
		&claudecode.TextBlock{Text: "hi"},
	}}
	rep := &captureReporter{}
	req := engine.StepRequest{Step: &workflow.Step{}} // TranscriptPath == ""

	if _, err := captureStream(scriptChan(assistant, &claudecode.ResultMessage{}), req, rep, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if len(rep.messages) != 0 {
		t.Errorf("want no Message signals with persistence off, got %d", len(rep.messages))
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(entries) != 0 {
		t.Errorf("no transcript file should be written; found %v", entries)
	}
}

// TestBuildOptions verifies each of a step's model/tool/permission fields is
// translated into the matching SDK option — the fix for the fields being
// parsed and validated but never reaching the SDK client.
func TestBuildOptions(t *testing.T) {
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
	opts, err := buildOptions(st)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	var got claudecode.Options
	for _, o := range opts {
		o(&got)
	}
	if got.Model == nil || *got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %v, want claude-opus-4-8", got.Model)
	}
	if got.FallbackModel == nil || *got.FallbackModel != "claude-sonnet-4-6" {
		t.Errorf("FallbackModel = %v, want claude-sonnet-4-6", got.FallbackModel)
	}
	if got.Effort == nil || *got.Effort != "high" {
		t.Errorf("Effort = %v, want high", got.Effort)
	}
	if got.MaxTurns != 20 {
		t.Errorf("MaxTurns = %d, want 20", got.MaxTurns)
	}
	if got.MaxThinkingTokens != 8000 {
		t.Errorf("MaxThinkingTokens = %d, want 8000", got.MaxThinkingTokens)
	}
	if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != 5.0 {
		t.Errorf("MaxBudgetUSD = %v, want 5.0", got.MaxBudgetUSD)
	}
	if got.PermissionMode == nil || *got.PermissionMode != claudecode.PermissionMode("acceptEdits") {
		t.Errorf("PermissionMode = %v, want acceptEdits", got.PermissionMode)
	}
	if !reflect.DeepEqual(got.AllowedTools, []string{"Read", "Grep"}) {
		t.Errorf("AllowedTools = %v, want [Read Grep]", got.AllowedTools)
	}
	if !reflect.DeepEqual(got.DisallowedTools, []string{"Bash"}) {
		t.Errorf("DisallowedTools = %v, want [Bash]", got.DisallowedTools)
	}
}

// TestBuildOptions_Empty verifies a zero-value step leaves every optional SDK
// field unset so the SDK's own defaults apply, but always sets OutputFormat
// to enforce the base schema.
func TestBuildOptions_Empty(t *testing.T) {
	opts, err := buildOptions(&workflow.Step{})
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	var got claudecode.Options
	for _, o := range opts {
		o(&got)
	}
	if got.Model != nil || got.FallbackModel != nil || got.Effort != nil ||
		got.PermissionMode != nil || got.MaxBudgetUSD != nil {
		t.Errorf("zero-value step should leave optional fields unset: %+v", got)
	}
	if got.MaxTurns != 0 || got.MaxThinkingTokens != 0 {
		t.Errorf("zero-value step should leave numeric fields at 0: %+v", got)
	}
	if len(got.AllowedTools) != 0 || len(got.DisallowedTools) != 0 {
		t.Errorf("zero-value step should leave tool lists empty: %+v", got)
	}
	// Base schema is always enforced — OutputFormat must be set even with no
	// declared [step.schema].
	if got.OutputFormat == nil || got.OutputFormat.Type != "json_schema" {
		t.Errorf("OutputFormat = %v, want json_schema (base schema always applied)", got.OutputFormat)
	}
	props, ok := got.OutputFormat.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("base schema properties missing: %v", got.OutputFormat.Schema)
	}
	for _, base := range []string{"summary", "status", "raw_result", "confidence", "issues", "assumptions"} {
		if _, ok := props[base]; !ok {
			t.Errorf("base schema missing required field %q: %v", base, props)
		}
	}
}

// TestBuildOptions_Schema verifies a step.schema is merged with the base schema
// and translated into a WithJSONSchema option. Both base and declared fields
// must appear in the compiled JSON Schema.
func TestBuildOptions_Schema(t *testing.T) {
	st := &workflow.Step{
		Schema: &workflow.Schema{Fields: []*workflow.Field{
			{Name: "passed", Type: workflow.FieldBool},
		}},
	}
	opts, err := buildOptions(st)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	var got claudecode.Options
	for _, o := range opts {
		o(&got)
	}
	if got.OutputFormat == nil || got.OutputFormat.Type != "json_schema" {
		t.Fatalf("OutputFormat = %v, want json_schema", got.OutputFormat)
	}
	props, ok := got.OutputFormat.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing or wrong type: %v", got.OutputFormat.Schema)
	}
	// Declared field must be present.
	if _, ok := props["passed"]; !ok {
		t.Errorf("schema missing declared field 'passed': %v", props)
	}
	// Base schema fields must also be present.
	for _, base := range []string{"summary", "status", "raw_result", "confidence", "issues", "assumptions"} {
		if _, ok := props[base]; !ok {
			t.Errorf("schema missing base field %q: %v", base, props)
		}
	}
}

// TestCaptureStream_StructuredOutput verifies a ResultMessage's StructuredOutput
// is captured into step.Result.Structured — the field block_on/when/loop.when
// guards read to evaluate schema-field conditions.
func TestCaptureStream_StructuredOutput(t *testing.T) {
	dir := t.TempDir()
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "transcript.jsonl")}

	result := &claudecode.ResultMessage{
		StructuredOutput: map[string]any{"needs_input": true, "question": "which threat model?"},
	}
	res, err := captureStream(scriptChan(result), req, &captureReporter{}, time.Now(), "")
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

// TestCaptureStream_AssistantError verifies an AssistantMessage.Error is
// surfaced as a system transcript entry rather than silently dropped.
func TestCaptureStream_AssistantError(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	rateLimited := claudecode.AssistantMessageErrorRateLimit
	assistant := &claudecode.AssistantMessage{
		Content: []claudecode.ContentBlock{&claudecode.TextBlock{Text: "hold on"}},
		Error:   &rateLimited,
	}
	if _, err := captureStream(scriptChan(assistant, &claudecode.ResultMessage{}), req, &captureReporter{}, time.Now(), ""); err != nil {
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

// TestCaptureStream_Subtype verifies ResultMessage.Subtype lands on step.Result
// for both the success and failure paths, and that policy-limit subtypes produce
// descriptive human-readable error messages.
func TestCaptureStream_Subtype(t *testing.T) {
	dir := t.TempDir()
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "success.jsonl")}

	ok := &claudecode.ResultMessage{Subtype: "success"}
	res, err := captureStream(scriptChan(ok), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Subtype != "success" {
		t.Errorf("success Subtype = %q, want success", res.Subtype)
	}

	// error_max_turns: descriptive prefix + SDK Errors appended.
	req2 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "fail.jsonl")}
	failed := &claudecode.ResultMessage{IsError: true, Subtype: "error_max_turns", Errors: []string{"hit turn limit"}}
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
		t.Errorf("failure Err = %q, want it to include the SDK Errors detail", res2.Err)
	}

	// error_max_budget_usd: descriptive prefix, no SDK detail.
	req3 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "budget.jsonl")}
	budget := &claudecode.ResultMessage{IsError: true, Subtype: "error_max_budget_usd"}
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

// TestCaptureStream_ErrorResult verifies an error ResultMessage yields a failed
// Result and records a result entry.
func TestCaptureStream_ErrorResult(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	req := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: tPath}

	errMsg := &claudecode.ResultMessage{IsError: true, Result: strPtr("boom")}
	res, err := captureStream(scriptChan(errMsg), req, &captureReporter{}, time.Now(), "")
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

// TestSessionIDCapturedAtStart proves the spec-07 B2 fix: the SDK session id is
// captured from an early stream event (or the init SystemMessage) and survives a
// mid-turn stop. Here the stream carries a StreamEvent with a session id and then
// closes *before* any ResultMessage — exactly what a cancelled worker sees. The
// returned Result must still carry the session id so the engine can resume.
func TestSessionIDCapturedAtStart(t *testing.T) {
	streamEvt := &claudecode.StreamEvent{
		SessionID: "sess-early",
		Event:     map[string]any{"type": "message_start"},
	}
	// No ResultMessage: the channel closes after the stream event, mimicking a
	// context cancellation cutting the turn short.
	res, err := captureStream(scriptChan(streamEvt), engine.StepRequest{Step: &workflow.Step{}}, &captureReporter{}, time.Now(), "")
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

// TestSessionIDCapturedFromSystemMessage verifies the init SystemMessage is also
// honored as an early session-id source.
func TestSessionIDCapturedFromSystemMessage(t *testing.T) {
	sys := &claudecode.SystemMessage{Subtype: "init", Data: map[string]any{"session_id": "sess-sys"}}
	res, err := captureStream(scriptChan(sys), engine.StepRequest{Step: &workflow.Step{}}, &captureReporter{}, time.Now(), "")
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
	turnOne := &claudecode.AssistantMessage{Content: []claudecode.ContentBlock{&claudecode.TextBlock{Text: "turn one"}}}
	if _, err := captureStream(scriptChan(turnOne, &claudecode.ResultMessage{}), req, &captureReporter{}, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	r, _ := transcript.Open(tPath)
	firstCount := len(mustWindow(t, r))

	// Resumed turn: the human message that triggered the resume plus a new answer.
	turnTwo := &claudecode.AssistantMessage{Content: []claudecode.ContentBlock{&claudecode.TextBlock{Text: "turn two"}}}
	if _, err := captureStream(scriptChan(turnTwo, &claudecode.ResultMessage{}), req, &captureReporter{}, time.Now(), "continue please"); err != nil {
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

// TestRewriteAskUserQuestion verifies that "AskUserQuestion" is rewritten to
// the MCP-qualified name and other tool names pass through unchanged.
func TestRewriteAskUserQuestion(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"Read", "Grep"}, []string{"Read", "Grep"}},
		{[]string{"AskUserQuestion"}, []string{"mcp__jig__AskUserQuestion"}},
		{[]string{"Read", "AskUserQuestion", "Bash"}, []string{"Read", "mcp__jig__AskUserQuestion", "Bash"}},
		{[]string{"AskUserQuestion", "AskUserQuestion"}, []string{"mcp__jig__AskUserQuestion", "mcp__jig__AskUserQuestion"}},
	}
	for _, tc := range cases {
		got := rewriteAskUserQuestion(tc.in)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("rewriteAskUserQuestion(%v) = %v, want %v", tc.in, got, tc.want)
		}
		// Must not mutate the input slice.
		for _, t2 := range tc.in {
			if t2 == "mcp__jig__AskUserQuestion" {
				t.Errorf("input slice was mutated")
			}
		}
	}
}

// TestBuildOptions_AskUserQuestion verifies that "AskUserQuestion" in AllowedTools
// is rewritten to "mcp__jig__AskUserQuestion" in the SDK options.
func TestBuildOptions_AskUserQuestion(t *testing.T) {
	st := &workflow.Step{
		AllowedTools: []string{"Read", "AskUserQuestion", "Grep"},
	}
	opts, err := buildOptions(st)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	var got claudecode.Options
	for _, o := range opts {
		o(&got)
	}
	want := []string{"Read", "mcp__jig__AskUserQuestion", "Grep"}
	if strings.Join(got.AllowedTools, ",") != strings.Join(want, ",") {
		t.Errorf("AllowedTools = %v, want %v", got.AllowedTools, want)
	}
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

func floatPtr(f float64) *float64 { return &f }

// TestBlockedAndRedacted proves the transcript-redaction and finding-production
// paths when the Tier-1 guard is active. The test uses a scripted SDK channel
// (no live Claude Code CLI required) carrying an AssistantMessage whose
// ToolUseBlock input contains a fake AWS key.
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

	// AssistantMessage: two tool_use blocks — one with a secret, one clean.
	secretInput := map[string]any{
		"file_path": "config.txt",
		"content":   "aws_access_key_id = " + fakeKey,
	}
	cleanInput := map[string]any{
		"file_path": "main.go",
		"content":   "package main\n",
	}
	secretInputJSON, _ := json.Marshal(secretInput)
	cleanInputJSON, _ := json.Marshal(cleanInput)

	assistant := &claudecode.AssistantMessage{
		Content: []claudecode.ContentBlock{
			&claudecode.ToolUseBlock{ToolUseID: "tu1", Name: "Write", Input: secretInput},
			&claudecode.ToolUseBlock{ToolUseID: "tu2", Name: "Write", Input: cleanInput},
		},
	}
	result := &claudecode.ResultMessage{IsError: false}

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

	res, err := captureStream(scriptChan(assistant, result), req, rep, time.Now(), "")
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
	// Original clean input confirms the unredacted block is unchanged.
	_ = string(secretInputJSON)

	// Block 1: clean Write — input must be byte-identical to the original JSON.
	b1 := string(blocks[1].Input)
	if !strings.Contains(b1, "main.go") || strings.Contains(b1, "aws-key") {
		t.Errorf("transcript block 1 (clean) was unexpectedly modified: %s", b1)
	}
	_ = string(cleanInputJSON)

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
// SDK ResultMessage. A result without cost yields a nil pointer (not $0.00).
func TestCostCapture(t *testing.T) {
	dir := t.TempDir()

	// Success path: ResultMessage carries cost.
	cost := 0.0034
	ok := &claudecode.ResultMessage{TotalCostUSD: floatPtr(cost)}
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

	// Success path: ResultMessage without cost yields nil pointer (not 0.0).
	noCost := &claudecode.ResultMessage{}
	req2 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "t2.jsonl")}
	res2, err := captureStream(scriptChan(noCost), req2, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res2.TotalCostUSD != nil {
		t.Errorf("TotalCostUSD = %v, want nil for unreported cost", res2.TotalCostUSD)
	}

	// Error path: cost is preserved even on error ResultMessage.
	errMsg := &claudecode.ResultMessage{IsError: true, TotalCostUSD: floatPtr(0.0012)}
	req3 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "t3.jsonl")}
	res3, err := captureStream(scriptChan(errMsg), req3, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res3.TotalCostUSD == nil || *res3.TotalCostUSD != 0.0012 {
		t.Errorf("error-path TotalCostUSD = %v, want 0.0012", res3.TotalCostUSD)
	}
}
