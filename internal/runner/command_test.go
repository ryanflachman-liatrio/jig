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

func TestCheckExecutor_ReturnsTypedFailureAndEvidence(t *testing.T) {
	dir := t.TempDir()
	exec := NewCheckExecutor(dir)
	result, err := exec.Execute(context.Background(), engine.StepRequest{
		Step: &workflow.Step{
			ID:   "quality",
			Type: workflow.StepCheck,
			Run:  "printf '{\"schema_version\":1,\"outcome\":\"fail\",\"findings\":[{\"id\":\"test-failure\",\"severity\":\"error\",\"message\":\"broken\"}]}' > findings.json; exit 1",
			Findings: &workflow.CheckFindings{
				SchemaVersion: workflow.CheckFindingsSchemaVersion,
				File:          "findings.json",
				RequiredTools: []string{"sh"},
			},
		},
		TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
	}, &noopReporter{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != step.StatusSucceeded || result.Verdict != "fail" {
		t.Fatalf("result = %+v, want succeeded check verdict fail", result)
	}
	data, err := os.ReadFile(result.OutputPath)
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	if !strings.Contains(string(data), `"outcome":"fail"`) {
		t.Fatalf("findings = %s", data)
	}
}

func TestCheckExecutor_ProtocolErrorsAreTypedAndKeepAttemptArtifacts(t *testing.T) {
	const valid = `{"schema_version":1,"outcome":"pass","findings":[]}`
	cases := []struct {
		name          string
		findings      string
		requiredTools []string
		wantVerdict   string
	}{
		{name: "valid protocol", findings: valid, requiredTools: []string{"sh"}, wantVerdict: "pass"},
		{name: "reserved skip outcome", findings: `{"schema_version":1,"outcome":"skip","findings":[]}`, requiredTools: []string{"sh"}, wantVerdict: "error"},
		{name: "malformed finding", findings: `{"schema_version":1,"outcome":"fail","findings":[{"id":"missing-message","severity":"error"}]}`, requiredTools: []string{"sh"}, wantVerdict: "error"},
		{name: "missing required tool", findings: valid, requiredTools: []string{"jig-test-tool-that-does-not-exist"}, wantVerdict: "error"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(tt.findings), 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := NewCheckExecutor(dir).Execute(context.Background(), engine.StepRequest{
				Step: &workflow.Step{
					ID:   "quality",
					Type: workflow.StepCheck,
					Run:  "printf command-log; exit 42",
					Findings: &workflow.CheckFindings{
						SchemaVersion: workflow.CheckFindingsSchemaVersion,
						File:          "findings.json",
						RequiredTools: tt.requiredTools,
					},
				},
				TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
				Iteration:      7,
				Attempt:        3,
			}, &noopReporter{})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Status != step.StatusSucceeded || result.Verdict != tt.wantVerdict {
				t.Fatalf("result = %+v, want succeeded/%s", result, tt.wantVerdict)
			}
			if _, err := os.Stat(filepath.Join(dir, "evidence", "iteration-007-attempt-003.log")); err != nil {
				t.Fatalf("missing immutable command log: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "evidence", "iteration-007-attempt-003.findings.json")); err != nil {
				t.Fatalf("missing immutable findings: %v", err)
			}
		})
	}
}

func TestCheckExecutor_SnapshotsDeclaredArtifacts(t *testing.T) {
	worktree := t.TempDir()
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "coverage.out"), []byte("mode: set\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "findings.json"), []byte(`{"schema_version":1,"outcome":"pass","findings":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewCheckExecutor(worktree).Execute(context.Background(), engine.StepRequest{
		Step: &workflow.Step{
			ID:   "tests",
			Type: workflow.StepCheck,
			Run:  "true",
			Findings: &workflow.CheckFindings{
				SchemaVersion: workflow.CheckFindingsSchemaVersion,
				File:          "findings.json",
				RequiredTools: []string{"sh"},
				Artifacts:     map[string]string{"coverage_profile": "coverage.out"},
			},
		},
		Worktree:       worktree,
		TranscriptPath: filepath.Join(runDir, "transcript.jsonl"),
		Iteration:      2,
		Attempt:        1,
	}, &noopReporter{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	artifact := result.Artifacts["coverage_profile"]
	if artifact == "" {
		t.Fatal("coverage_profile was not exported")
	}
	if strings.HasPrefix(artifact, worktree) {
		t.Fatalf("artifact path %q points into producer worktree", artifact)
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(data) != "mode: set\n" {
		t.Fatalf("snapshot = %q, want coverage data", data)
	}
}

func TestCheckArtifactConsumerReadsSnapshotAcrossIsolatedWorktrees(t *testing.T) {
	producerWorktree := t.TempDir()
	consumerWorktree := t.TempDir()
	runDir := t.TempDir()
	producer := NewCheckExecutor(producerWorktree)
	result, err := producer.Execute(context.Background(), engine.StepRequest{
		Step: &workflow.Step{
			ID:   "tests",
			Type: workflow.StepCheck,
			Run:  `printf 'coverage-from-producer\n' > coverage.out; printf '{"schema_version":1,"outcome":"pass","findings":[]}' > findings.json`,
			Findings: &workflow.CheckFindings{
				SchemaVersion: workflow.CheckFindingsSchemaVersion,
				File:          "findings.json",
				RequiredTools: []string{"sh", "printf"},
				Artifacts:     map[string]string{"coverage_profile": "coverage.out"},
			},
		},
		Worktree:       producerWorktree,
		TranscriptPath: filepath.Join(runDir, "producer-transcript.jsonl"),
	}, &noopReporter{})
	if err != nil || result.Status != step.StatusSucceeded || result.Verdict != "pass" {
		t.Fatalf("producer Execute = %+v, %v", result, err)
	}
	artifact := result.Artifacts["coverage_profile"]
	if artifact == "" || strings.HasPrefix(artifact, producerWorktree) {
		t.Fatalf("artifact = %q, want run-owned snapshot outside producer worktree", artifact)
	}

	consumer := NewCommandExecutor(consumerWorktree)
	consumerResult, err := consumer.Execute(context.Background(), engine.StepRequest{
		Step: &workflow.Step{ID: "coverage", Type: workflow.StepCommand, Run: `test "$(cat "$JIG_INPUT_COVERAGE_PROFILE")" = "coverage-from-producer"`},
		Inputs: []engine.ResolvedInput{{
			Ref:   workflow.Input{Ref: "tests", Artifact: "coverage_profile", As: "coverage_profile"},
			Value: artifact,
		}},
		Worktree:       consumerWorktree,
		TranscriptPath: filepath.Join(runDir, "consumer-transcript.jsonl"),
	}, &noopReporter{})
	if err != nil || consumerResult.Status != step.StatusSucceeded {
		t.Fatalf("consumer Execute = %+v, %v", consumerResult, err)
	}
}

func TestCommandExecutor_ExposesArtifactInputsAsNamedEnvironment(t *testing.T) {
	dir := t.TempDir()
	result, err := NewCommandExecutor(dir).Execute(context.Background(), engine.StepRequest{
		Step: &workflow.Step{ID: "coverage", Type: workflow.StepCommand, Run: "printf %s \"$JIG_INPUT_COVERAGE_PROFILE\""},
		Inputs: []engine.ResolvedInput{{
			Ref:   workflow.Input{Ref: "tests", Artifact: "coverage_profile", As: "coverage_profile"},
			Value: "/run/evidence/coverage.out",
		}},
		TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
	}, &noopReporter{})
	if err != nil || result.Status != step.StatusSucceeded {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
	reader, err := transcript.Open(filepath.Join(dir, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reader.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Blocks[0].Text != "/run/evidence/coverage.out" {
		t.Fatalf("command transcript = %+v, want artifact path", entries)
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
