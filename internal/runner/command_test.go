package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// noopReporter satisfies engine.Reporter, recording the output deltas it sees
// and whether Message was signalled.
type noopReporter struct {
	deltas  []string
	message bool
}

func (r *noopReporter) Output(delta string)          { r.deltas = append(r.deltas, delta) }
func (r *noopReporter) ToolCall(tool, detail string) {}
func (r *noopReporter) Message(seq, iteration int)   { r.message = true }
func (r *noopReporter) Question(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
}
func (r *noopReporter) Finding(_ engine.SecurityFinding) {}

func TestCommandExecutor_Success(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "hi",
			Type: workflow.StepCommand,
			Run:  "echo hello",
		},
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Status != step.StatusSucceeded {
		t.Errorf("want succeeded, got %q (err: %q)", result.Status, result.Err)
	}
	// Output should have been streamed via the reporter.
	combined := strings.Join(rep.deltas, "")
	if !strings.Contains(combined, "hello") {
		t.Errorf("expected 'hello' in output, got %q", combined)
	}
}

func TestCommandExecutor_Failure(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "bad",
			Type: workflow.StepCommand,
			Run:  "exit 1",
		},
	}
	result, err := exec.Execute(context.Background(), req, &noopReporter{})
	if err != nil {
		t.Fatalf("unexpected error (want nil, step failure expressed in result): %v", err)
	}
	if result.Status != step.StatusFailed {
		t.Errorf("want failed, got %q", result.Status)
	}
}

func TestCommandExecutor_MultilineScript(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:     "script",
			Type:   workflow.StepCommand,
			Script: "echo line1\necho line2",
		},
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusSucceeded {
		t.Errorf("want succeeded, got %q", result.Status)
	}
	combined := strings.Join(rep.deltas, "")
	if !strings.Contains(combined, "line1") || !strings.Contains(combined, "line2") {
		t.Errorf("expected both lines in output, got %q", combined)
	}
}

// TestCommandExecutor_ScriptResolvedFromRepoRoot verifies a single-line `script`
// is read as a file resolved against RepoRoot (the project root) — not against
// the execution cwd, and not misread as an inline shell body. This is the fix
// for the validate/runtime path divergence: the runner locates the script at the
// same anchor the validator checked.
func TestCommandExecutor_ScriptResolvedFromRepoRoot(t *testing.T) {
	root := t.TempDir()
	// Script lives at <root>/scripts/hello.sh; the step references it repo-root
	// relative. The runner must join it onto RepoRoot to find it.
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "hello.sh"), []byte("echo from-script\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Execute from an unrelated cwd to prove resolution is anchored to RepoRoot,
	// not the cwd: the script path would not resolve relative to this dir.
	exec := NewCommandExecutor(t.TempDir())
	req := engine.StepRequest{
		RepoRoot: root,
		Step: &workflow.Step{
			ID:     "s",
			Type:   workflow.StepCommand,
			Script: "scripts/hello.sh",
		},
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusSucceeded {
		t.Errorf("want succeeded, got %q (err: %q)", result.Status, result.Err)
	}
	if combined := strings.Join(rep.deltas, ""); !strings.Contains(combined, "from-script") {
		t.Errorf("expected script body to run; got output %q", combined)
	}
}

func TestCommandExecutor_ContextCancellation(t *testing.T) {
	exec := NewCommandExecutor("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:  "slow",
			Run: "sleep 10",
		},
	}
	result, err := exec.Execute(ctx, req, &noopReporter{})
	// Either an error or a failed result is acceptable; the important thing is
	// that Execute returns (not hangs) and does not report success.
	if err == nil && result != nil && result.Status == step.StatusSucceeded {
		t.Error("cancelled execution should not succeed")
	}
}

// TestCommandExecutor_TranscriptCapture verifies a command step's combined
// output is persisted as a system/text transcript entry and that rep.Message is
// signalled so an open chat view refreshes (Phase 6).
func TestCommandExecutor_TranscriptCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "greet",
			Type: workflow.StepCommand,
			Run:  "echo transcript-hello",
		},
		TranscriptPath: path,
		Iteration:      2,
		Attempt:        1,
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusSucceeded {
		t.Fatalf("want succeeded, got %q (err: %q)", result.Status, result.Err)
	}
	if !rep.message {
		t.Error("expected rep.Message to be signalled after the transcript write")
	}

	r, err := transcript.Open(path)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	entries, err := r.Window(0, 10)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Role != transcript.RoleSystem {
		t.Errorf("want role %q, got %q", transcript.RoleSystem, e.Role)
	}
	if e.Iteration != 2 || e.Attempt != 1 {
		t.Errorf("want iter/attempt 2/1, got %d/%d", e.Iteration, e.Attempt)
	}
	if len(e.Blocks) != 1 || e.Blocks[0].Type != transcript.BlockText {
		t.Fatalf("want one text block, got %+v", e.Blocks)
	}
	if !strings.Contains(e.Blocks[0].Text, "transcript-hello") {
		t.Errorf("transcript block missing output, got %q", e.Blocks[0].Text)
	}
}

// TestCommandExecutor_TranscriptCaptureOnFailure verifies a failing command's
// output is still persisted (the record survives a non-zero exit).
func TestCommandExecutor_TranscriptCaptureOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "boom",
			Type: workflow.StepCommand,
			Run:  "echo before-fail; exit 3",
		},
		TranscriptPath: path,
	}
	result, err := exec.Execute(context.Background(), req, &noopReporter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusFailed {
		t.Fatalf("want failed, got %q", result.Status)
	}

	r, err := transcript.Open(path)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	entries, err := r.Window(0, 10)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Blocks[0].Text, "before-fail") {
		t.Fatalf("failing command output not captured: %+v", entries)
	}
}

// TestCommandExecutor_NoTranscriptWhenPersistenceOff verifies an empty
// TranscriptPath (persistence off) writes no file and does not signal Message.
func TestCommandExecutor_NoTranscriptWhenPersistenceOff(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{ID: "quiet", Type: workflow.StepCommand, Run: "echo hi"},
	}
	rep := &noopReporter{}
	if _, err := exec.Execute(context.Background(), req, rep); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.message {
		t.Error("rep.Message must not be signalled when persistence is off")
	}
}

// TestCommandExecutor_NoTranscriptForEmptyOutput verifies a command that emits
// nothing does not create an empty transcript entry.
func TestCommandExecutor_NoTranscriptForEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step:           &workflow.Step{ID: "silent", Type: workflow.StepCommand, Run: "true"},
		TranscriptPath: path,
	}
	if _, err := exec.Execute(context.Background(), req, &noopReporter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no transcript file for empty output, stat err = %v", err)
	}
}

func TestCommandExecutor_NeitherRunNorScript(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "empty",
			Type: workflow.StepCommand,
		},
	}
	result, err := exec.Execute(context.Background(), req, &noopReporter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusFailed {
		t.Errorf("want failed for empty command, got %q", result.Status)
	}
}
