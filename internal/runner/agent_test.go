package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// captureReporter records the liveness signals captureStream emits so tests can
// assert on rep.Message / rep.Output without a live scheduler.
type captureReporter struct {
	messages []captureMsg
	deltas   []string
}

type captureMsg struct{ seq, iteration int }

func (r *captureReporter) Output(delta string)          { r.deltas = append(r.deltas, delta) }
func (r *captureReporter) ToolCall(tool, detail string) {}
func (r *captureReporter) Message(seq, iteration int) {
	r.messages = append(r.messages, captureMsg{seq, iteration})
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

// TestCaptureStream_Artifact verifies the output artifact is derived from the
// final assistant text blocks (not intermediate turns).
func TestCaptureStream_Artifact(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.md")

	first := &claudecode.AssistantMessage{Content: []claudecode.ContentBlock{
		&claudecode.TextBlock{Text: "working on it"},
		&claudecode.ToolUseBlock{ToolUseID: "t1", Name: "Read", Input: map[string]any{}},
	}}
	final := &claudecode.AssistantMessage{Content: []claudecode.ContentBlock{
		&claudecode.TextBlock{Text: "# Done\n"},
		&claudecode.TextBlock{Text: "final answer"},
	}}
	req := engine.StepRequest{
		Step:           &workflow.Step{Output: outPath},
		TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
	}

	res, err := captureStream(scriptChan(first, final, &claudecode.ResultMessage{}), req, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.OutputPath != outPath {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, outPath)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Done\nfinal answer" {
		t.Errorf("artifact = %q, want final assistant text", got)
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
// field unset so the SDK's own defaults apply, rather than sending zero values.
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
	if got.OutputFormat != nil {
		t.Errorf("zero-value step should leave OutputFormat unset: %+v", got.OutputFormat)
	}
}

// TestBuildOptions_Schema verifies a step.schema is translated into a
// WithJSONSchema option carrying the equivalent JSON Schema document.
func TestBuildOptions_Schema(t *testing.T) {
	st := &workflow.Step{
		Schema: &workflow.Schema{Fields: []*workflow.Field{
			{Name: "summary", Type: workflow.FieldText},
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
	if _, ok := props["summary"]; !ok {
		t.Errorf("schema missing summary field: %v", props)
	}
	if _, ok := props["passed"]; !ok {
		t.Errorf("schema missing passed field: %v", props)
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

// TestCaptureStream_Subtype verifies ResultMessage.Subtype and Errors land on
// step.Result on both the success and failure paths.
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

	req2 := engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "fail.jsonl")}
	failed := &claudecode.ResultMessage{IsError: true, Subtype: "error_max_turns", Errors: []string{"hit turn limit"}}
	res2, err := captureStream(scriptChan(failed), req2, &captureReporter{}, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Subtype != "error_max_turns" {
		t.Errorf("failure Subtype = %q, want error_max_turns", res2.Subtype)
	}
	if !strings.Contains(res2.Err, "hit turn limit") {
		t.Errorf("failure Err = %q, want it to contain the Errors detail", res2.Err)
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
