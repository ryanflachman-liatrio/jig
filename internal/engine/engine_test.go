package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// testExec is a minimal Executor used only in engine tests.
// runner.FakeExecutor is the feature-complete version for TUI dry-run.
type testExec struct {
	outcomes map[string]testOutcome
}

type testOutcome struct {
	delay time.Duration
	fail  bool
}

func (e *testExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	out, ok := e.outcomes[req.Step.ID]
	if !ok {
		out = testOutcome{delay: time.Millisecond}
	}
	if out.delay > 0 {
		select {
		case <-time.After(out.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if out.fail {
		return &step.Result{Status: step.StatusFailed, Err: "scripted failure"}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// collectEvents drains ch until RunFinished or timeout, returning all events.
func collectEvents(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if _, ok := e.(RunFinished); ok {
				return events
			}
		case <-deadline:
			t.Fatal("timeout waiting for RunFinished")
			return nil
		}
	}
}

// findEvents filters events by type name.
func findStatus(events []Event, stepID string) []step.Status {
	var out []step.Status
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && ss.StepID == stepID {
			out = append(out, ss.To)
		}
	}
	return out
}

const delay = 5 * time.Millisecond

func TestScheduler_Linear(t *testing.T) {
	const toml = `
[workflow]
name = "linear"
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
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"a": {delay: delay},
		"b": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// Verify sequential ordering: a must fully complete before b starts.
	gotA := findStatus(events, "a")
	gotB := findStatus(events, "b")
	if len(gotA) != 2 || gotA[0] != step.StatusRunning || gotA[1] != step.StatusSucceeded {
		t.Errorf("step a statuses: want [running succeeded], got %v", gotA)
	}
	if len(gotB) != 2 || gotB[0] != step.StatusRunning || gotB[1] != step.StatusSucceeded {
		t.Errorf("step b statuses: want [running succeeded], got %v", gotB)
	}
	// Find the position of a's succeeded and b's first running.
	aSuccIdx, bRunIdx := -1, -1
	for i, e := range events {
		if ss, ok := e.(StepStatus); ok {
			if ss.StepID == "a" && ss.To == step.StatusSucceeded {
				aSuccIdx = i
			}
			if ss.StepID == "b" && ss.To == step.StatusRunning {
				bRunIdx = i
			}
		}
	}
	if aSuccIdx == -1 || bRunIdx == -1 || bRunIdx <= aSuccIdx {
		t.Errorf("b must start after a succeeds: a_succ@%d, b_run@%d", aSuccIdx, bRunIdx)
	}
	// Run must finish not-failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

func TestScheduler_Parallel(t *testing.T) {
	const toml = `
[workflow]
name = "parallel"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"

[[step]]
id = "b"
type = "command"
run = "echo b"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"a": {delay: delay},
		"b": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// Both steps must appear in the event stream.
	gotA := findStatus(events, "a")
	gotB := findStatus(events, "b")
	if len(gotA) == 0 || len(gotB) == 0 {
		t.Errorf("both steps must emit events; a=%v b=%v", gotA, gotB)
	}
	// Both must succeed.
	if gotA[len(gotA)-1] != step.StatusSucceeded || gotB[len(gotB)-1] != step.StatusSucceeded {
		t.Errorf("both steps must succeed; a=%v b=%v", gotA, gotB)
	}
}

func TestScheduler_MaxParallel(t *testing.T) {
	const toml = `
[workflow]
name = "throttled"
version = "0.1"

[defaults]
max_parallel = 1

[[step]]
id = "a"
type = "command"
run = "echo a"

[[step]]
id = "b"
type = "command"
run = "echo b"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"a": {delay: delay},
		"b": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// With max_parallel=1, the two steps must not overlap: one must finish before
	// the other starts.
	running := 0
	for _, e := range events {
		ss, ok := e.(StepStatus)
		if !ok {
			continue
		}
		if ss.To == step.StatusRunning {
			running++
		}
		if ss.To == step.StatusSucceeded || ss.To == step.StatusFailed {
			running--
		}
		if running > 1 {
			t.Error("max_parallel=1 violated: two steps ran simultaneously")
		}
	}
}

func TestScheduler_StepFails_RunFails(t *testing.T) {
	const toml = `
[workflow]
name = "fail-test"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"bad": {fail: true},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	got := findStatus(events, "bad")
	if len(got) == 0 || got[len(got)-1] != step.StatusFailed {
		t.Errorf("step bad should be failed, got %v", got)
	}
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true}, got %v", last)
	}
}

func TestScheduler_TransitiveFailure(t *testing.T) {
	// When a fails, b (depending on a) should never run, and the run finishes failed.
	const toml = `
[workflow]
name = "transitive-fail"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "false"

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"a": {fail: true},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// b should never have run.
	gotB := findStatus(events, "b")
	for _, s := range gotB {
		if s == step.StatusRunning {
			t.Error("step b must not run when its dep a failed")
		}
	}
	// Run must be failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true}, got %v", last)
	}
}

func TestScheduler_Cancel(t *testing.T) {
	const toml = `
[workflow]
name = "cancel-test"
version = "0.1"

[[step]]
id = "slow"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"slow": {delay: 10 * time.Second},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the step to start, then cancel.
	waitForRunning(t, ch, "slow", 2*time.Second)
	run.Cancel()

	events := collectEvents(t, ch, 3*time.Second)
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true} after cancel, got %v", last)
	}
}

// waitForRunning drains ch until the step reaches StatusRunning or timeout.
func waitForRunning(t *testing.T, ch <-chan Event, stepID string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if ss, ok := e.(StepStatus); ok && ss.StepID == stepID && ss.To == step.StatusRunning {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for step %q to be running", stepID)
		}
	}
}

func TestScheduler_MultipleRuns(t *testing.T) {
	const toml = `
[workflow]
name = "multi"
version = "0.1"

[[step]]
id = "x"
type = "command"
run = "echo x"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"x": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	run1, _ := mgr.Start(wf)
	run2, _ := mgr.Start(wf)

	if run1.ID == run2.ID {
		t.Error("concurrent runs must have distinct IDs")
	}

	// Collect two RunFinished events (one per run).
	finished := 0
	deadline := time.After(5 * time.Second)
	for finished < 2 {
		select {
		case e := <-ch:
			if _, ok := e.(RunFinished); ok {
				finished++
			}
		case <-deadline:
			t.Fatal("timeout waiting for both runs to finish")
		}
	}
}

func TestScheduler_Snapshot(t *testing.T) {
	const toml = `
[workflow]
name = "snap"
version = "0.1"

[[step]]
id = "s"
type = "command"
run = "echo s"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"s": {delay: 50 * time.Millisecond},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()
	run, _ := mgr.Start(wf)

	// Wait for step to start running, then snapshot.
	waitForRunning(t, ch, "s", 2*time.Second)
	snap := run.Snapshot()

	if snap.ID != run.ID {
		t.Errorf("snapshot ID mismatch: %q vs %q", snap.ID, run.ID)
	}
	if snap.Workflow != "snap" {
		t.Errorf("snapshot workflow: %q", snap.Workflow)
	}
	found := false
	for _, st := range snap.Steps {
		if st.ID == "s" && st.Status == step.StatusRunning {
			found = true
		}
	}
	if !found {
		t.Errorf("expected step s to be running in snapshot: %+v", snap.Steps)
	}

	// Drain to avoid goroutine leak in test.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		select {
		case e := <-ch:
			if _, ok := e.(RunFinished); ok {
				return
			}
		case <-ctx.Done():
			t.Fatal("timeout draining after snapshot test")
		}
	}
}

// ── Phase 2 tests ─────────────────────────────────────────────────────────────

// TestScheduler_RetryPolicy verifies that a step with on_failure = "retry" is
// re-dispatched up to max_retries times before the run is failed.
func TestScheduler_RetryPolicy(t *testing.T) {
	const toml = `
[workflow]
name = "retry-test"
version = "0.1"

[[step]]
id = "flaky"
type = "command"
run = "false"
on_failure = "retry"
max_retries = 2
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	// Count how many times Execute is called for "flaky".
	callCount := 0
	exec := &countingExec{
		inner: &testExec{outcomes: map[string]testOutcome{
			"flaky": {fail: true},
		}},
		onExecute: func(id string) { callCount++ },
	}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// With max_retries=2 the step should be dispatched 3 times (1 original + 2 retries).
	if callCount != 3 {
		t.Errorf("expected 3 execute calls (1 + 2 retries), got %d", callCount)
	}

	// Count how many times "flaky" became pending (retry resets it to pending).
	pendingCount := 0
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && ss.StepID == "flaky" && ss.To == step.StatusPending {
			pendingCount++
		}
	}
	if pendingCount != 2 {
		t.Errorf("expected 2 pending transitions (one per retry), got %d", pendingCount)
	}

	// Final status must be failed.
	got := findStatus(events, "flaky")
	if len(got) == 0 || got[len(got)-1] != step.StatusFailed {
		t.Errorf("step flaky should end failed, got %v", got)
	}
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true}, got %v", last)
	}
}

// TestScheduler_ContinuePolicy verifies that a failing step with on_failure =
// "continue" allows dependent steps to run and the run finishes with Failed=true.
func TestScheduler_ContinuePolicy(t *testing.T) {
	const toml = `
[workflow]
name = "continue-test"
version = "0.1"

[[step]]
id = "soft"
type = "command"
run = "false"
on_failure = "continue"

[[step]]
id = "after"
type = "command"
run = "echo after"
depends_on = ["soft"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"soft":  {fail: true},
		"after": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// "after" must have run despite "soft" failing.
	gotAfter := findStatus(events, "after")
	ranAfter := false
	for _, s := range gotAfter {
		if s == step.StatusRunning || s == step.StatusSucceeded {
			ranAfter = true
		}
	}
	if !ranAfter {
		t.Errorf("step 'after' should have run after 'soft' with continue policy; statuses: %v", gotAfter)
	}
	if len(gotAfter) == 0 || gotAfter[len(gotAfter)-1] != step.StatusSucceeded {
		t.Errorf("step 'after' should succeed; got %v", gotAfter)
	}

	// Run overall must be failed (because "soft" failed).
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true} because soft failed; got %v", last)
	}
}

// TestScheduler_GatePass verifies that a passing [step.validate] gate leads to
// StatusSucceeded without triggering failure policy.
func TestScheduler_GatePass(t *testing.T) {
	// Create a temp file for the output_exists / output_contains gate.
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(outPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	toml := `
[workflow]
name = "gate-pass-test"
version = "0.1"

[[step]]
id = "check"
type = "command"
run = "echo hi"
output = "` + outPath + `"

[step.validate]
output_exists = true
output_contains = "hello"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"check": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// Step must pass through validating and then succeed.
	got := findStatus(events, "check")
	hasValidating := false
	for _, s := range got {
		if s == step.StatusValidating {
			hasValidating = true
		}
	}
	if !hasValidating {
		t.Errorf("expected StatusValidating in transitions; got %v", got)
	}
	if len(got) == 0 || got[len(got)-1] != step.StatusSucceeded {
		t.Errorf("step check should succeed after gate pass; got %v", got)
	}

	// GateResult must be emitted with Passed=true.
	var gateResult *GateResult
	for _, e := range events {
		if gr, ok := e.(GateResult); ok && gr.StepID == "check" {
			gr := gr
			gateResult = &gr
		}
	}
	if gateResult == nil {
		t.Fatal("expected GateResult event for step 'check'")
	}
	if !gateResult.Passed {
		t.Errorf("gate should have passed; detail: %q", gateResult.Detail)
	}

	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_GateFail verifies that a failing [step.validate] gate triggers
// the failure policy (default abort).
func TestScheduler_GateFail(t *testing.T) {
	const toml = `
[workflow]
name = "gate-fail-test"
version = "0.1"

[[step]]
id = "check"
type = "command"
run = "echo hi"

[step.validate]
command = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"check": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// GateResult must be emitted with Passed=false.
	var gateResult *GateResult
	for _, e := range events {
		if gr, ok := e.(GateResult); ok && gr.StepID == "check" {
			gr := gr
			gateResult = &gr
		}
	}
	if gateResult == nil {
		t.Fatal("expected GateResult event for step 'check'")
	}
	if gateResult.Passed {
		t.Errorf("gate should have failed; detail: %q", gateResult.Detail)
	}

	// Step should be failed.
	got := findStatus(events, "check")
	if len(got) == 0 || got[len(got)-1] != step.StatusFailed {
		t.Errorf("step check should fail after gate failure; got %v", got)
	}

	// Run overall must be failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true}, got %v", last)
	}
}

// TestScheduler_ManifestWriter verifies that journal.jsonl is created and
// written when Manager.root is set.
func TestScheduler_ManifestWriter(t *testing.T) {
	const toml = `
[workflow]
name = "journal-test"
version = "0.1"

[[step]]
id = "x"
type = "command"
run = "echo x"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"x": {delay: delay},
	}}

	root := t.TempDir()
	mgr := NewManager(exec, root)
	ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	collectEvents(t, ch, 5*time.Second)

	// Find the run directory.
	runDir := filepath.Join(root, "runs", run.ID)
	journalPath := filepath.Join(runDir, "journal.jsonl")
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read journal.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 journal lines (RunStarted, StepStatus×2+, RunFinished), got %d: %s", len(lines), data)
	}
	// Each line must be valid JSON.
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			t.Errorf("journal line %d is not JSON: %q", i, line)
		}
	}

	// result.json must exist for step x.
	resultPath := filepath.Join(runDir, "steps", "x", "result.json")
	if _, err := os.Stat(resultPath); err != nil {
		t.Errorf("result.json not found for step x: %v", err)
	}
}

// countingExec wraps another Executor and calls onExecute for each dispatch.
type countingExec struct {
	inner     Executor
	onExecute func(stepID string)
}

func (c *countingExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	c.onExecute(req.Step.ID)
	return c.inner.Execute(ctx, req, rep)
}
