package headless_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/headless"
	"jig/internal/interaction"
	"jig/internal/runner"
	"jig/internal/step"
	"jig/internal/workflow"
)

var (
	osWriteFile = os.WriteFile
	execCommand = exec.Command
)

func decodeWF(t *testing.T, toml string) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func fastFake(outcomes map[string]runner.FakeOutcome) *runner.FakeExecutor {
	def := runner.FakeOutcome{Delay: time.Millisecond}
	if outcomes == nil {
		outcomes = map[string]runner.FakeOutcome{}
	}
	return runner.NewFakeExecutor(outcomes, def)
}

func testMgr(t *testing.T, exec engine.Executor) *engine.Manager {
	t.Helper()
	return engine.NewManager(exec, "")
}

func runOpts(wf *workflow.Workflow, mgr *engine.Manager) headless.Options {
	var stdout, stderr bytes.Buffer
	return headless.Options{
		Workflow: wf,
		Manager:  mgr,
		Output:   headless.OutputText,
		Stdout:   &stdout,
		Stderr:   &stderr,
	}
}

func TestHeadless_SuccessDAG(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "ok"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(nil))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	stdout := opts.Stdout.(*bytes.Buffer)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v stderr=%s", result.ExitCode, result.Err, opts.Stderr)
	}
	if !result.Envelope.OK || result.Envelope.Failed {
		t.Fatalf("envelope: %+v", result.Envelope)
	}
	if result.Envelope.RunID == "" {
		t.Fatal("missing run_id")
	}
	if !strings.Contains(stdout.String(), "run_id=") {
		t.Fatalf("stdout summary missing: %q", stdout.String())
	}
}

func TestHeadless_ReviewGateExit3(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "review-gate"
version = "0.1"

[[step]]
id = "prep"
type = "command"
run = "echo prep"

[[step]]
id = "gate"
type = "review"
depends_on = ["prep"]
output_type = { enum = ["approve", "reject"] }

[[step.review]]
source = "diff"
label = "Prep"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(nil))
	mux.Register(workflow.StepReview, fastFake(nil))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitGate {
		t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitGate, result.Err)
	}
	var g *headless.GateError
	if result.Err == nil {
		t.Fatal("expected GateError")
	}
	_ = g
	if result.Envelope.Error == nil || result.Envelope.Error.Code != "gate_review" {
		t.Fatalf("envelope error: %+v", result.Envelope.Error)
	}
}

func TestHeadless_RecoveryAbortExit1(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "fail-recover"
version = "0.1"

[[step]]
id = "boom"
type = "command"
run = "echo boom"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(map[string]runner.FakeOutcome{
		"boom": {Delay: time.Millisecond, Fail: true},
	}))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitFailed {
		t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitFailed, result.Err)
	}
	if result.Envelope.OK {
		t.Fatal("expected ok=false")
	}
}

func TestHeadless_TimeoutExit4(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "slow"
version = "0.1"

[[step]]
id = "slow"
type = "command"
run = "echo slow"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(map[string]runner.FakeOutcome{
		"slow": {Delay: time.Hour},
	}))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	opts.Timeout = 50 * time.Millisecond
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitTimeout {
		t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitTimeout, result.Err)
	}
}

func TestHeadless_CancelContextExit130(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "cancel-me"
version = "0.1"

[[step]]
id = "slow"
type = "command"
run = "echo slow"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(map[string]runner.FakeOutcome{
		"slow": {Delay: time.Hour},
	}))
	mgr := testMgr(t, mux)

	ctx, cancel := context.WithCancel(context.Background())
	opts := runOpts(wf, mgr)

	done := make(chan headless.Result, 1)
	go func() { done <- headless.Run(ctx, opts) }()

	// Give the step a moment to start, then cancel (signal path).
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		if result.ExitCode != headless.ExitInterrupted {
			t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitInterrupted, result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for cancelled run to settle")
	}
}

func TestHeadless_JSONOutput(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "json-ok"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(nil))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	opts.Output = headless.OutputJSON
	stdout := opts.Stdout.(*bytes.Buffer)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v", result.ExitCode, result.Err)
	}
	var env headless.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.RunID == "" || env.Workflow != "json-ok" {
		t.Fatalf("envelope: %+v", env)
	}
}

func TestHeadless_MergeWithoutFlagsExit3(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wf := decodeWF(t, `
[workflow]
name = "land"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"
`)
	// Executor that writes a file so the run branch gains a commit.
	exec := &writeExec{file: "created.txt", content: "from step\n"}
	mgr := engine.NewManager(exec, filepath.Join(repo, ".jig"))

	// Manager starts with cwd as repo root? Engine uses the process cwd for
	// git detection — chdir into the repo for this test.
	t.Chdir(repo)

	opts := runOpts(wf, mgr)
	stderr := opts.Stderr.(*bytes.Buffer)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitGate {
		t.Fatalf("exit=%d want %d err=%v stderr=%s", result.ExitCode, headless.ExitGate, result.Err, stderr.String())
	}
	if result.Envelope.Error == nil || result.Envelope.Error.Code != "gate_merge" {
		t.Fatalf("want gate_merge, got %+v", result.Envelope.Error)
	}
}

func TestHeadless_DiscardMergeSuccess(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wf := decodeWF(t, `
[workflow]
name = "land-discard"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"
`)
	exec := &writeExec{file: "created.txt", content: "from step\n"}
	mgr := engine.NewManager(exec, filepath.Join(repo, ".jig"))
	t.Chdir(repo)

	opts := runOpts(wf, mgr)
	opts.DiscardMerge = true
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v envelope=%+v", result.ExitCode, result.Err, result.Envelope)
	}
}

func TestHeadless_StartTimeWarningListsReview(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "warn"
version = "0.1"

[[step]]
id = "prep"
type = "command"
run = "echo prep"

[[step]]
id = "gate"
type = "review"
depends_on = ["prep"]
output_type = { enum = ["approve", "reject"] }

[[step.review]]
source = "diff"
label = "x"
`)
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, fastFake(nil))
	mux.Register(workflow.StepReview, fastFake(nil))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	stderr := opts.Stderr.(*bytes.Buffer)
	_ = headless.Run(context.Background(), opts)
	if !strings.Contains(stderr.String(), `step "gate"`) {
		t.Fatalf("expected gate warning in stderr: %s", stderr.String())
	}
}

func TestHeadless_BlockOnGateExit3(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "block-on"
version = "0.1"

[[step]]
id = "chat"
type = "agent"
skill = "chat"
block_on = "chat.needs_input"

  [step.schema]
  needs_input = "bool"
`)
	exec := &structuredFake{
		responses: []string{`{"needs_input":true}`},
	}
	mgr := testMgr(t, exec)
	opts := runOpts(wf, mgr)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitGate {
		t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitGate, result.Err)
	}
	if result.Envelope.Error == nil || result.Envelope.Error.Code != "gate_input" {
		t.Fatalf("want gate_input, got %+v", result.Envelope.Error)
	}
}

func TestHeadless_AgentQuestionGateExit3(t *testing.T) {
	wf := decodeWF(t, `
[workflow]
name = "ask"
version = "0.1"

[[step]]
id = "ask"
type = "command"
run = "true"
`)
	exec := &questionExec{}
	mgr := testMgr(t, exec)
	opts := runOpts(wf, mgr)
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitGate {
		t.Fatalf("exit=%d want %d err=%v", result.ExitCode, headless.ExitGate, result.Err)
	}
	if result.Envelope.Error == nil || result.Envelope.Error.Code != "gate_question" {
		t.Fatalf("want gate_question, got %+v", result.Envelope.Error)
	}
}

func TestHeadless_ConflictAbortCascadeExit1(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wf := decodeWF(t, `
[workflow]
name = "conflict"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"

[[step]]
id = "b"
type = "command"
run = "echo b"
isolation = "worktree"
`)
	exec := &conflictExec{writes: map[string]map[string]string{
		"a": {"shared": "a\n"},
		"b": {"shared": "b\n"},
	}}
	mgr := engine.NewManager(exec, filepath.Join(repo, ".jig"))
	t.Chdir(repo)

	opts := runOpts(wf, mgr)
	opts.DiscardMerge = true // in case merge fires after abort path somehow
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitFailed {
		t.Fatalf("exit=%d want %d err=%v envelope=%+v", result.ExitCode, headless.ExitFailed, result.Err, result.Envelope)
	}
}

// questionExec blocks on reporter.Question so the engine emits AgentQuestion.
type questionExec struct{}

func (e *questionExec) Execute(ctx context.Context, _ engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	_ = rep.Question(ctx, interaction.QuestionRequest{
		ID: "q1",
		Fields: []interaction.QuestionField{{
			ID: "answer", Prompt: "?", Kind: interaction.FieldText,
		}},
	})
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// conflictExec writes overlapping files from parallel worktree steps.
type conflictExec struct {
	writes map[string]map[string]string
}

func (e *conflictExec) Execute(_ context.Context, req engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
	for name, content := range e.writes[req.Step.ID] {
		if req.Worktree != "" {
			_ = writeFile(filepath.Join(req.Worktree, name), content)
		}
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// writeExec writes a file into the step worktree so git integration creates a commit.
type writeExec struct {
	file, content string
}

func (e *writeExec) Execute(_ context.Context, req engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
	if req.Worktree != "" {
		_ = writeFile(filepath.Join(req.Worktree, e.file), e.content)
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// structuredFake returns scripted JSON structured output for agent-like steps.
type structuredFake struct {
	responses []string
	calls     int
}

func (e *structuredFake) Execute(_ context.Context, _ engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
	i := e.calls
	if i >= len(e.responses) {
		i = len(e.responses) - 1
	}
	e.calls++
	return &step.Result{
		Status:     step.StatusSucceeded,
		SessionID:  "sess",
		Structured: []byte(e.responses[i]),
	}, nil
}

func writeFile(path, content string) error {
	return osWriteFile(path, []byte(content), 0o644)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@jig.test"},
		{"git", "-C", dir, "config", "user.name", "Jig Test"},
	}
	for _, args := range cmds {
		if out, err := execCommand(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v — %s", args, err, out)
		}
	}
	seed := filepath.Join(dir, "seed.txt")
	if err := writeFile(seed, "seed"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "init"},
	} {
		if out, err := execCommand(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v — %s", args, err, out)
		}
	}
}
