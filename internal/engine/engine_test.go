package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

// capturingExec records the Inputs slice received per step and delegates
// execution to an embedded testExec.
type capturingExec struct {
	testExec
	mu     sync.Mutex
	inputs map[string][]ResolvedInput // stepID → Inputs received
}

func (e *capturingExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	e.mu.Lock()
	if e.inputs == nil {
		e.inputs = make(map[string][]ResolvedInput)
	}
	e.inputs[req.Step.ID] = req.Inputs
	e.mu.Unlock()
	return e.testExec.Execute(ctx, req, rep)
}

// outputPathExec wraps capturingExec and additionally sets result.OutputPath
// for one named step, simulating an agent that writes an artifact file.
type outputPathExec struct {
	capturingExec
	stepID     string
	outputPath string
}

func (e *outputPathExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	r, err := e.capturingExec.Execute(ctx, req, rep)
	if r != nil && req.Step.ID == e.stepID {
		r.OutputPath = e.outputPath
	}
	return r, err
}

// inputCapturingStructuredExec combines structured-output injection with
// input capture so that tests can verify what inputs a downstream step receives.
type inputCapturingStructuredExec struct {
	structuredExec
	mu     sync.Mutex
	inputs map[string][]ResolvedInput
}

func (e *inputCapturingStructuredExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	e.mu.Lock()
	if e.inputs == nil {
		e.inputs = make(map[string][]ResolvedInput)
	}
	e.inputs[req.Step.ID] = req.Inputs
	e.mu.Unlock()
	return e.structuredExec.Execute(ctx, req, rep)
}

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

// collectEventsAborting drains ch until RunFinished, and on the first
// RecoveryRequest it aborts the run via run.Recover. Since an unrecoverable
// failure now parks the step for a human decision instead of tearing down the
// run, this reproduces the pre-recovery "a failed step fails the run" contract
// for tests that assert terminal failure.
func collectEventsAborting(t *testing.T, ch <-chan Event, run *Run, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	aborted := false
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if rr, ok := e.(RecoveryRequest); ok && !aborted {
				aborted = true
				run.Recover(rr.StepID, RecoverAbort, "")
			}
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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// A failure now parks the step in awaiting_recovery; aborting at the recovery
	// gate reproduces the terminal-failure outcome.
	events := collectEventsAborting(t, ch, run, 5*time.Second)

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

// TestScheduler_FailedStatusCarriesReason locks in that a step's failure reason
// (Result.Err) rides the StepStatus transition to Failed, so the TUI can surface
// why a run failed without re-reading result.json.
func TestScheduler_FailedStatusCarriesReason(t *testing.T) {
	const toml = `
[workflow]
name = "fail-reason"
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
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// The failure reason rides the Failed transition, which now occurs when the
	// recovery gate is aborted.
	events := collectEventsAborting(t, ch, run, 5*time.Second)

	var failEv *StepStatus
	for i := range events {
		if ss, ok := events[i].(StepStatus); ok && ss.StepID == "bad" && ss.To == step.StatusFailed {
			failEv = &ss
		}
	}
	if failEv == nil {
		t.Fatal("no StepStatus{To: Failed} event for step bad")
	}
	if failEv.Err != "scripted failure" {
		t.Errorf("failed StepStatus.Err = %q, want %q", failEv.Err, "scripted failure")
	}
}

// dirCheckExec records, per step, whether the step's transcript directory
// existed at dispatch time. It reproduces the runner's mid-execution transcript
// write, which fails if the engine hasn't created steps/<id>/ first.
type dirCheckExec struct {
	mu      sync.Mutex
	present map[string]bool
}

func (e *dirCheckExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	ok := true
	if req.TranscriptPath != "" {
		_, statErr := os.Stat(filepath.Dir(req.TranscriptPath))
		ok = statErr == nil
	}
	e.mu.Lock()
	e.present[req.Step.ID] = ok
	e.mu.Unlock()
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// TestScheduler_StepDirExistsBeforeExecute guards the fix for a review/agent
// step failing with "transcript open: … no such file or directory": the engine
// must create steps/<id>/ before dispatching, not rely on the manifest writer's
// terminal-event StepDir call.
func TestScheduler_StepDirExistsBeforeExecute(t *testing.T) {
	const toml = `
[workflow]
name = "stepdir"
version = "0.1"

[[step]]
id = "only"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &dirCheckExec{present: make(map[string]bool)}
	mgr := NewManager(exec, filepath.Join(t.TempDir(), ".jig"))
	_, ch := mgr.Subscribe()

	if _, err = mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	exec.mu.Lock()
	defer exec.mu.Unlock()
	if !exec.present["only"] {
		t.Error("step directory did not exist when the executor was dispatched")
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
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// a fails and parks for recovery; aborting there fails the run without ever
	// running b.
	events := collectEventsAborting(t, ch, run, 5*time.Second)

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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()
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
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// After exhausting automatic retries the step parks for recovery; aborting
	// there yields the terminal failure this test asserts.
	events := collectEventsAborting(t, ch, run, 5*time.Second)

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

// failCostExec returns a failing Result carrying a fixed cost/token usage on
// every call, so a retried step accrues once per attempt.
type failCostExec struct {
	cost   float64
	tokens int
}

func (e *failCostExec) Execute(_ context.Context, _ StepRequest, _ Reporter) (*step.Result, error) {
	usage := map[string]any{"input_tokens": float64(e.tokens)}
	return &step.Result{
		Status:       step.StatusFailed,
		Err:          "boom",
		TotalCostUSD: &e.cost,
		Usage:        &usage,
	}, nil
}

// TestRunSnapshotCostCumulative verifies the run total counts every attempt: a
// step that fails and retries is billed for each attempt, so a reset/retry adds
// to the total rather than refunding the earlier attempt (see step.State.SpentUSD).
func TestRunSnapshotCostCumulative(t *testing.T) {
	const toml = `
[workflow]
name = "retry-cost"
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

	exec := &failCostExec{cost: 0.001, tokens: 500}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// 1 original + 2 retries = 3 attempts, then abort at the recovery gate.
	collectEventsAborting(t, ch, run, 5*time.Second)

	snap := run.Snapshot()
	if want := 3 * 0.001; !floatNear(snap.TotalCostUSD, want) {
		t.Errorf("TotalCostUSD = %v, want %v (3 attempts, not refunded)", snap.TotalCostUSD, want)
	}
	if want := 3 * 500; snap.TotalTokens != want {
		t.Errorf("TotalTokens = %v, want %v (3 attempts)", snap.TotalTokens, want)
	}
}

func floatNear(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

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
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// A failed gate parks the step for recovery; aborting there fails the run and
	// carries the gate detail onto the Failed transition.
	events := collectEventsAborting(t, ch, run, 5*time.Second)

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

	// The failing StepStatus must carry the gate detail as its reason so the TUI
	// can explain why the gate rejected the step.
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && ss.StepID == "check" && ss.To == step.StatusFailed {
			if ss.Err != gateResult.Detail {
				t.Errorf("failed StepStatus.Err = %q, want gate detail %q", ss.Err, gateResult.Detail)
			}
		}
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
	_, ch := mgr.Subscribe()

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

// ── Phase 3 tests ─────────────────────────────────────────────────────────────

// verdictExec extends testExec to return a configurable Verdict in the Result.
type verdictExec struct {
	testExec
	verdicts map[string]string // stepID → verdict string
}

func (e *verdictExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	r, err := e.testExec.Execute(ctx, req, rep)
	if r != nil && e.verdicts != nil {
		if v, ok := e.verdicts[req.Step.ID]; ok {
			r.Verdict = v
		}
	}
	return r, err
}

// TestScheduler_WhenGuard_Skip verifies that a step with a when guard that
// evaluates to false is skipped, and the run still finishes (not deadlocked).
func TestScheduler_WhenGuard_Skip(t *testing.T) {
	const toml = `
[workflow]
name = "guard-skip"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
output_type = { enum = ["yes", "no"] }

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]
when = "a == 'yes'"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &verdictExec{
		testExec: testExec{outcomes: map[string]testOutcome{
			"a": {delay: delay},
		}},
		verdicts: map[string]string{"a": "no"},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// b must be skipped, not run.
	gotB := findStatus(events, "b")
	if len(gotB) == 0 || gotB[len(gotB)-1] != step.StatusSkipped {
		t.Errorf("step b should be skipped; got %v", gotB)
	}
	for _, s := range gotB {
		if s == step.StatusRunning {
			t.Error("step b must not run when guard is false")
		}
	}

	// Run finishes without failure (a succeeded; b was skipped by guard, not failed).
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_WhenGuard_SequencingDepRuns verifies that a step whose only
// dependency on a guard-skipped step is sequencing (no @ref input) still runs.
// This is the common pattern for optional gates: the downstream step lists the
// gate in depends_on to prevent races, but does not consume its output.
func TestScheduler_WhenGuard_SequencingDepRuns(t *testing.T) {
	const toml = `
[workflow]
name = "guard-sequence"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
output_type = { enum = ["yes", "no"] }

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]
when = "a == 'yes'"

[[step]]
id = "c"
type = "command"
run = "echo c"
depends_on = ["b"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &verdictExec{
		testExec: testExec{outcomes: map[string]testOutcome{
			"a": {delay: delay},
		}},
		verdicts: map[string]string{"a": "no"},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// b must be skipped (guard false).
	got := findStatus(events, "b")
	if len(got) == 0 || got[len(got)-1] != step.StatusSkipped {
		t.Errorf("step b should be skipped (guard false); got %v", got)
	}
	// c depends on b for sequencing only (no @b.* input) — it must run.
	got = findStatus(events, "c")
	if len(got) == 0 || got[len(got)-1] != step.StatusSucceeded {
		t.Errorf("step c should succeed (sequencing-only dep on guard-skipped b); got %v", got)
	}
	// Run finishes not-failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_WhenGuard_DataDepCascades verifies that a step which references
// a guard-skipped step's output (@ref input) is cascade-skipped — it can never
// resolve its inputs when the producer didn't run.
func TestScheduler_WhenGuard_DataDepCascades(t *testing.T) {
	const toml = `
[workflow]
name = "guard-cascade"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
output_type = { enum = ["yes", "no"] }

[[step]]
id = "b"
type = "agent"
skill = "echo-b"
depends_on = ["a"]
when = "a == 'yes'"
[step.schema]
result = "text"

[[step]]
id = "c"
type = "command"
run = "echo c"
depends_on = ["b"]
inputs = ["@b.result"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &verdictExec{
		testExec: testExec{outcomes: map[string]testOutcome{
			"a": {delay: delay},
		}},
		verdicts: map[string]string{"a": "no"},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 5*time.Second)

	// Both b and c must be skipped: b by guard, c by cascade (needs @b.result).
	for _, id := range []string{"b", "c"} {
		got := findStatus(events, id)
		if len(got) == 0 || got[len(got)-1] != step.StatusSkipped {
			t.Errorf("step %q should be skipped; got %v", id, got)
		}
	}
	// Run finishes not-failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_ReviewStep verifies that a review step parks at awaiting_review,
// emits ReviewRequest, and transitions to succeeded when a verdict is delivered.
func TestScheduler_ReviewStep(t *testing.T) {
	const toml = `
[workflow]
name = "review-test"
version = "0.1"

[[step]]
id = "prep"
type = "command"
run = "echo prep"

[[step]]
id = "check"
type = "review"
depends_on = ["prep"]
review = "@prep"
output_type = { enum = ["approve", "reject"] }
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExec{outcomes: map[string]testOutcome{
		"prep": {delay: delay},
	}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for ReviewRequest, then deliver a verdict.
	var events []Event
	deadline := time.After(5 * time.Second)
	resolved := false
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if rr, ok := e.(ReviewRequest); ok && rr.StepID == "check" && !resolved {
				resolved = true
				run.Resolve("check", "approve")
			}
			if _, ok := e.(RunFinished); ok {
				goto done
			}
		case <-deadline:
			t.Fatal("timeout waiting for review step to complete")
		}
	}
done:
	if !resolved {
		t.Error("ReviewRequest was never emitted for step 'check'")
	}

	// check must have been awaiting_review then succeeded.
	gotCheck := findStatus(events, "check")
	hasAwaiting := false
	for _, s := range gotCheck {
		if s == step.StatusAwaitingReview {
			hasAwaiting = true
		}
	}
	if !hasAwaiting {
		t.Errorf("step check must enter awaiting_review; got %v", gotCheck)
	}
	if len(gotCheck) == 0 || gotCheck[len(gotCheck)-1] != step.StatusSucceeded {
		t.Errorf("step check should end succeeded; got %v", gotCheck)
	}

	// Run must finish not-failed.
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// structuredExec wraps testExec to inject a scripted step.Result.Structured
// payload and SessionID for one step, changing what it returns on the second
// call — simulating an agent that resumes its session after human input and
// reports a different structured answer the second time.
type structuredExec struct {
	testExec
	stepID    string
	sessionID string
	responses []string // raw JSON, one per call; the last is reused once exhausted
	mu        sync.Mutex
	calls     int
}

func (e *structuredExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	r, err := e.testExec.Execute(ctx, req, rep)
	if r == nil || req.Step.ID != e.stepID {
		return r, err
	}
	e.mu.Lock()
	i := e.calls
	if i >= len(e.responses) {
		i = len(e.responses) - 1
	}
	e.calls++
	e.mu.Unlock()
	r.SessionID = e.sessionID
	r.Structured = []byte(e.responses[i])
	return r, err
}

// TestScheduler_BlockOn verifies that block_on parks an agent step at
// StatusNeedsInput when its schema-field guard evaluates true against the
// executor's real Structured output, and that delivering human input via
// Run.SendInput resumes the step and lets it (and its dependents) succeed
// once the guard evaluates false. This exercises evalGuard's field-path decode
// branch with real data — previously untested.
func TestScheduler_BlockOn(t *testing.T) {
	const toml = `
[workflow]
name = "block-on-test"
version = "0.1"

[[step]]
id = "chat"
type = "agent"
skill = "chat"
block_on = "chat.needs_input"

  [step.schema]
  needs_input = "bool"

[[step]]
id = "after"
type = "command"
run = "echo after"
depends_on = ["chat"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &structuredExec{
		testExec:  testExec{outcomes: map[string]testOutcome{"chat": {delay: delay}, "after": {delay: delay}}},
		stepID:    "chat",
		sessionID: "sess-1",
		responses: []string{`{"needs_input":true}`, `{"needs_input":false}`},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var events []Event
	deadline := time.After(5 * time.Second)
	sentInput := false
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if ir, ok := e.(InputRequest); ok && ir.StepID == "chat" && !sentInput {
				sentInput = true
				run.SendInput("chat", "use the untrusted threat model")
			}
			if _, ok := e.(RunFinished); ok {
				goto done
			}
		case <-deadline:
			t.Fatal("timeout waiting for block_on step to complete")
		}
	}
done:
	if !sentInput {
		t.Fatal("InputRequest was never emitted for step 'chat'")
	}

	gotChat := findStatus(events, "chat")
	hasNeedsInput := false
	for _, s := range gotChat {
		if s == step.StatusNeedsInput {
			hasNeedsInput = true
		}
	}
	if !hasNeedsInput {
		t.Errorf("step chat must enter needs_input; got %v", gotChat)
	}
	if len(gotChat) == 0 || gotChat[len(gotChat)-1] != step.StatusSucceeded {
		t.Errorf("step chat should end succeeded once needs_input is false; got %v", gotChat)
	}

	gotAfter := findStatus(events, "after")
	if len(gotAfter) == 0 || gotAfter[len(gotAfter)-1] != step.StatusSucceeded {
		t.Errorf("downstream step 'after' should run once chat unblocks; got %v", gotAfter)
	}

	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_NeedsInput_NoDownstream guards against premature run termination
// when the only step transitions to StatusNeedsInput. Without the anyPendingRunnable
// fix, inFlight drops to 0 with no pending steps and the scheduler declares RunFinished
// before the user can answer, crashing the run.
func TestScheduler_NeedsInput_NoDownstream(t *testing.T) {
	const toml = `
[workflow]
name = "needs-input-only"
version = "0.1"

[[step]]
id       = "chat"
type     = "agent"
skill    = "test"
block_on = "chat.needs_input"

  [step.schema]
  needs_input = "bool"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &structuredExec{
		testExec:  testExec{outcomes: map[string]testOutcome{"chat": {delay: delay}}},
		stepID:    "chat",
		sessionID: "sess-1",
		responses: []string{`{"needs_input":true}`, `{"needs_input":false}`},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var events []Event
	deadline := time.After(5 * time.Second)
	sentInput := false
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			// If RunFinished arrives before we've sent input, the bug is live.
			if _, ok := e.(RunFinished); ok {
				if !sentInput {
					t.Fatal("run terminated prematurely before user sent input")
				}
				goto done
			}
			if ir, ok := e.(InputRequest); ok && ir.StepID == "chat" && !sentInput {
				sentInput = true
				run.SendInput("chat", "here is the clarification")
			}
		case <-deadline:
			t.Fatal("timeout waiting for block_on step to complete")
		}
	}
done:
	if !sentInput {
		t.Fatal("InputRequest was never emitted for step 'chat'")
	}

	gotChat := findStatus(events, "chat")
	hasNeedsInput := false
	for _, s := range gotChat {
		if s == step.StatusNeedsInput {
			hasNeedsInput = true
		}
	}
	if !hasNeedsInput {
		t.Errorf("step chat must enter needs_input; got %v", gotChat)
	}
	if len(gotChat) == 0 || gotChat[len(gotChat)-1] != step.StatusSucceeded {
		t.Errorf("step chat should end succeeded after input; got %v", gotChat)
	}

	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}, got %v", last)
	}
}

// TestScheduler_Loop verifies that a [step.loop] back-edge re-runs the loop
// body while the condition holds, and terminates once max_iterations is reached.
func TestScheduler_Loop(t *testing.T) {
	// a → b, b has a loop back to a with max_iterations = 2.
	// The condition always holds (a always returns "yes").
	// Expected: a,b run → loop fires (iter 1) → a,b run → loop fires (iter 2) →
	// a,b run → iter 2 >= max 2 → run aborted with RunError.
	const toml = `
[workflow]
name = "loop-test"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
output_type = { enum = ["yes", "no"] }

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]

  [step.loop]
  when = "a == 'yes'"
  goto = "a"
  max_iterations = 2
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &verdictExec{
		testExec: testExec{outcomes: map[string]testOutcome{
			"a": {delay: delay},
			"b": {delay: delay},
		}},
		verdicts: map[string]string{"a": "yes"},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	_, err = mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 10*time.Second)

	// LoopFired must be emitted at least twice.
	var loopFires []LoopFired
	for _, e := range events {
		if lf, ok := e.(LoopFired); ok {
			loopFires = append(loopFires, lf)
		}
	}
	if len(loopFires) < 2 {
		t.Errorf("expected at least 2 LoopFired events; got %d", len(loopFires))
	}

	// Run must end failed (exceeded max_iterations → abort).
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok || !rf.Failed {
		t.Errorf("want RunFinished{Failed:true} (loop cap exceeded); got %v", last)
	}

	// RunError must have been emitted.
	var hasError bool
	for _, e := range events {
		if _, ok := e.(RunError); ok {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected RunError event when loop exceeds max_iterations")
	}
}

// recordingExec captures every StepRequest it receives (across retries/loops)
// and returns a configurable verdict so loop conditions can hold.
type recordingExec struct {
	mu       sync.Mutex
	requests []StepRequest
	verdicts map[string]string
}

func (e *recordingExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.mu.Unlock()
	r := &step.Result{Status: step.StatusSucceeded}
	if v, ok := e.verdicts[req.Step.ID]; ok {
		r.Verdict = v
	}
	return r, nil
}

func (e *recordingExec) reqsFor(stepID string) []StepRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []StepRequest
	for _, r := range e.requests {
		if r.Step.ID == stepID {
			out = append(out, r)
		}
	}
	return out
}

// TestScheduler_StepRequestTranscript verifies that, when persistence is
// enabled, dispatch plumbs a per-step TranscriptPath plus the current loop
// Iteration into each StepRequest — the Phase 2 engine contract that lets the
// runner (Phase 3) write to the right transcript file tagged by iteration.
func TestScheduler_StepRequestTranscript(t *testing.T) {
	// a → b, b loops back to a (max 2). The loop body {a,b} re-runs with an
	// incrementing Iteration; we assert the last dispatch of "a" reflects it.
	const toml = `
[workflow]
name = "transcript-plumb"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
output_type = { enum = ["yes", "no"] }

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]

  [step.loop]
  when = "a == 'yes'"
  goto = "a"
  max_iterations = 2
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	jigRoot := t.TempDir()
	exec := &recordingExec{verdicts: map[string]string{"a": "yes"}}
	mgr := NewManager(exec, jigRoot)
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 10*time.Second)

	runDir := filepath.Join(jigRoot, "runs", run.ID)
	wantPath := datastore.TranscriptPath(runDir, "a")

	reqs := exec.reqsFor("a")
	if len(reqs) < 2 {
		t.Fatalf("expected step a to be dispatched at least twice (looped); got %d", len(reqs))
	}
	for i, r := range reqs {
		if r.TranscriptPath != wantPath {
			t.Errorf("dispatch %d: TranscriptPath = %q, want %q", i, r.TranscriptPath, wantPath)
		}
	}
	// The final dispatch of the loop body must carry a non-zero Iteration.
	last := reqs[len(reqs)-1]
	if last.Iteration == 0 {
		t.Errorf("final dispatch of looped step a: Iteration = 0, want > 0")
	}
}

// TestScheduler_StepRequestNoTranscript verifies the root == "" path (no
// persistence) leaves TranscriptPath empty so tests without a run dir still work.
func TestScheduler_StepRequestNoTranscript(t *testing.T) {
	const toml = `
[workflow]
name = "no-persist"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &recordingExec{}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 10*time.Second)

	reqs := exec.reqsFor("a")
	if len(reqs) == 0 {
		t.Fatal("step a was never dispatched")
	}
	if reqs[0].TranscriptPath != "" {
		t.Errorf("TranscriptPath = %q, want empty when persistence off", reqs[0].TranscriptPath)
	}
}

// ── Input resolution tests ────────────────────────────────────────────────────

// TestScheduler_RefFieldInput verifies that a @step.field input extracts a
// JSON-encoded field value from the upstream step's structured output.
func TestScheduler_RefFieldInput(t *testing.T) {
	const toml = `
[workflow]
name = "ref-field-test"
version = "0.1"

[[step]]
id    = "a"
type  = "agent"
skill = "a"

  [step.schema]
  areas = { list = "text" }

[[step]]
id         = "b"
type       = "agent"
skill      = "b"
depends_on = ["a"]
inputs     = ["@a.areas"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &inputCapturingStructuredExec{
		structuredExec: structuredExec{
			testExec:  testExec{outcomes: map[string]testOutcome{"a": {delay: delay}, "b": {delay: delay}}},
			stepID:    "a",
			sessionID: "sess-1",
			responses: []string{`{"areas":["backend","frontend"]}`},
		},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	if _, err = mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	exec.mu.Lock()
	inputs := exec.inputs["b"]
	exec.mu.Unlock()

	if len(inputs) != 1 {
		t.Fatalf("step b: expected 1 input, got %d", len(inputs))
	}
	want := `["backend","frontend"]`
	if inputs[0].Value != want {
		t.Errorf("step b input Value = %q, want %q", inputs[0].Value, want)
	}
}

// TestScheduler_PathInput verifies that a literal file path input is passed
// through unchanged as ResolvedInput.Value.
func TestScheduler_PathInput(t *testing.T) {
	const toml = `
[workflow]
name = "path-input-test"
version = "0.1"

[[step]]
id     = "b"
type   = "agent"
skill  = "b"
inputs = ["testdata/request.md"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &capturingExec{testExec: testExec{outcomes: map[string]testOutcome{"b": {delay: delay}}}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	if _, err = mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	exec.mu.Lock()
	inputs := exec.inputs["b"]
	exec.mu.Unlock()

	if len(inputs) != 1 {
		t.Fatalf("step b: expected 1 input, got %d", len(inputs))
	}
	if inputs[0].Value != "testdata/request.md" {
		t.Errorf("step b input Value = %q, want %q", inputs[0].Value, "testdata/request.md")
	}
}

// TestScheduler_BareRefInput verifies that a bare @step ref (no field path)
// resolves to the upstream step's OutputPath.
func TestScheduler_BareRefInput(t *testing.T) {
	const toml = `
[workflow]
name = "bare-ref-test"
version = "0.1"

[[step]]
id    = "a"
type  = "agent"
skill = "a"

[[step]]
id         = "b"
type       = "agent"
skill      = "b"
depends_on = ["a"]
inputs     = ["@a"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &outputPathExec{
		capturingExec: capturingExec{
			testExec: testExec{outcomes: map[string]testOutcome{"a": {delay: delay}, "b": {delay: delay}}},
		},
		stepID:     "a",
		outputPath: "/tmp/a-out.txt",
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	if _, err = mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	exec.mu.Lock()
	inputs := exec.inputs["b"]
	exec.mu.Unlock()

	if len(inputs) != 1 {
		t.Fatalf("step b: expected 1 input, got %d", len(inputs))
	}
	if inputs[0].Value != "/tmp/a-out.txt" {
		t.Errorf("step b input Value = %q, want %q", inputs[0].Value, "/tmp/a-out.txt")
	}
}

// ── Post-execution handler unit tests ────────────────────────────────────────

// TestPostExecHandler_ValidateGate tests phRunValidateGate in isolation:
// passing gate → decisionContinue, failing gate → decisionFailed.
func TestPostExecHandler_ValidateGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("gate passes", func(t *testing.T) {
		const toml = `
[workflow]
name = "val-pass"
version = "0.1"

[[step]]
id   = "check"
type = "command"
run  = "echo hi"

[step.validate]
command = "true"
`
		wf, err := workflow.Decode(toml, "")
		if err != nil {
			t.Fatal(err)
		}
		s := newScheduler(wf, "test", make(chan schedMsg, 4), nil, nil, cancel, nil, "", "", "", func(RunSnapshot) {})
		s.states["check"].Status = step.StatusRunning
		s.states["check"].Result = &step.Result{Status: step.StatusSucceeded}

		m := stepDoneMsg{stepID: "check", result: s.states["check"].Result}
		decision := phRunValidateGate(s, m, s.stepByID("check"))
		if decision != decisionContinue {
			t.Errorf("passing gate: want decisionContinue, got %v", decision)
		}
	})

	t.Run("gate fails", func(t *testing.T) {
		const toml = `
[workflow]
name = "val-fail"
version = "0.1"

[[step]]
id   = "check"
type = "command"
run  = "echo hi"

[step.validate]
command = "false"
`
		wf, err := workflow.Decode(toml, "")
		if err != nil {
			t.Fatal(err)
		}
		s := newScheduler(wf, "test", make(chan schedMsg, 4), nil, nil, cancel, nil, "", "", "", func(RunSnapshot) {})
		s.states["check"].Status = step.StatusRunning
		s.states["check"].Result = &step.Result{Status: step.StatusSucceeded}

		m := stepDoneMsg{stepID: "check", result: s.states["check"].Result}
		decision := phRunValidateGate(s, m, s.stepByID("check"))
		if decision != decisionFailed {
			t.Errorf("failing gate: want decisionFailed, got %v", decision)
		}
		if s.states["check"].Result.Err == "" {
			t.Error("failing gate must record error detail in Result.Err")
		}
	})

	_ = ctx
}

// TestPostExecHandler_BlockOn tests phCheckBlockOn in isolation:
// when block_on evaluates true against the step's own structured output, the
// handler parks the step at StatusNeedsInput and returns decisionNeedsInput.
func TestPostExecHandler_BlockOn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toml = `
[workflow]
name = "block-on-unit"
version = "0.1"

[[step]]
id       = "mystep"
type     = "agent"
skill    = "test"
block_on = "mystep.status == 'blocked'"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(wf, "test", make(chan schedMsg, 4), nil, nil, cancel, nil, "", "", "", func(RunSnapshot) {})
	s.states["mystep"].Status = step.StatusRunning
	s.states["mystep"].Result = &step.Result{
		Status:     step.StatusSucceeded,
		Structured: []byte(`{"status":"blocked"}`),
	}

	m := stepDoneMsg{stepID: "mystep", result: s.states["mystep"].Result}
	decision := phCheckBlockOn(s, m, s.stepByID("mystep"))
	if decision != decisionNeedsInput {
		t.Errorf("want decisionNeedsInput, got %v", decision)
	}
	if s.states["mystep"].Status != step.StatusNeedsInput {
		t.Errorf("want step status NeedsInput, got %v", s.states["mystep"].Status)
	}

	_ = ctx
}

// costExec is a testExec variant whose outcomes carry TotalCostUSD and a token
// usage map so TestRunSnapshotCost can verify the snapshot sums them correctly.
type costOutcome struct {
	cost   float64
	tokens int
}

type costExec struct {
	outcomes map[string]costOutcome
}

func (e *costExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	out := e.outcomes[req.Step.ID]
	res := &step.Result{Status: step.StatusSucceeded}
	if out.cost != 0 {
		res.TotalCostUSD = &out.cost
	}
	if out.tokens != 0 {
		// Split across input/output the way the SDK reports usage; TokenCount sums
		// the buckets back to the total.
		usage := map[string]any{
			"input_tokens":  float64(out.tokens - out.tokens/4),
			"output_tokens": float64(out.tokens / 4),
		}
		res.Usage = &usage
	}
	return res, nil
}

// TestRunSnapshotCost verifies that RunSnapshot.TotalCostUSD sums per-step
// costs, and that steps without cost contribute 0 (not causing a nil-deref).
func TestRunSnapshotCost(t *testing.T) {
	const toml = `
[workflow]
name = "cost-test"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "true"

[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]

[[step]]
id = "c"
type = "command"
run = "true"
depends_on = ["b"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	exec := &costExec{outcomes: map[string]costOutcome{
		"a": {cost: 0.001, tokens: 1000},
		"b": {cost: 0.002, tokens: 2000},
		// "c" has no cost/usage reported: TotalCostUSD stays nil, tokens 0
	}}
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Terminal StepStatus events must carry the per-step cost/token figures so the
	// monitor can total them live and on journal replay.
	gotCost := map[string]float64{}
	gotTokens := map[string]int{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case e := <-ctrl:
			if ss, ok := e.(StepStatus); ok && ss.To == step.StatusSucceeded {
				if ss.Cost != nil {
					gotCost[ss.StepID] = *ss.Cost
				}
				gotTokens[ss.StepID] = ss.Tokens
			}
			if _, ok := e.(RunFinished); ok {
				snap := run.Snapshot()
				if !snap.Done {
					t.Fatal("expected run to be done after RunFinished")
				}
				const wantCost = 0.001 + 0.002
				if snap.TotalCostUSD != wantCost {
					t.Errorf("TotalCostUSD = %v, want %v", snap.TotalCostUSD, wantCost)
				}
				if want := 1000 + 2000; snap.TotalTokens != want {
					t.Errorf("TotalTokens = %v, want %v", snap.TotalTokens, want)
				}
				if gotCost["a"] != 0.001 || gotCost["b"] != 0.002 {
					t.Errorf("StepStatus.Cost = %v, want a=0.001 b=0.002", gotCost)
				}
				if gotTokens["a"] != 1000 || gotTokens["b"] != 2000 {
					t.Errorf("StepStatus.Tokens = %v, want a=1000 b=2000", gotTokens)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for RunFinished")
		}
	}
}

// securityEscalateExec is an executor whose target step emits one or more
// SecurityFinding ctrl events via rep.Finding, then optionally blocks until
// ctx is cancelled. It is used to test the critical-finding escalation path.
type securityEscalateExec struct {
	stepID   string
	findings []SecurityFinding // emitted sequentially before blocking
	block    bool              // if true, block on ctx.Done after emitting
}

func (e *securityEscalateExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	if req.Step.ID != e.stepID {
		if err := sleepCtx(ctx, time.Millisecond); err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
	for _, sf := range e.findings {
		rep.Finding(sf)
	}
	if e.block {
		<-ctx.Done()
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// TestCriticalEscalation verifies the four escalation scenarios documented in
// spec 10, task 6.4/6.5.
func TestCriticalEscalation(t *testing.T) {
	// Use type="command" to pass the validator; the custom executor is called
	// regardless of the declared step type in engine tests.
	const toml = `
[workflow]
name = "security-escalation"
version = "1.0"
[[step]]
id = "impl"
type = "command"
run = "true"
`

	t.Run("critical finding on running step → StatusAwaitingRecovery → abort cleans up", func(t *testing.T) {
		wf, err := workflow.Decode(toml, "")
		if err != nil {
			t.Fatal(err)
		}
		exec := &securityEscalateExec{
			stepID: "impl",
			findings: []SecurityFinding{{
				Tier: "guard", Monitor: "secret-in-write",
				Severity: "critical", Action: "escalated", Fingerprint: "fp-crit-1",
			}},
			block: true,
		}
		mgr := NewManager(exec, "")
		_, ctrl := mgr.Subscribe()
		run, err := mgr.Start(wf)
		if err != nil {
			t.Fatal(err)
		}

		var gotRR RecoveryRequest
		deadline := time.After(5 * time.Second)
	loop1:
		for {
			select {
			case e := <-ctrl:
				switch ev := e.(type) {
				case RecoveryRequest:
					gotRR = ev
					break loop1
				case RunFinished:
					t.Fatal("run finished before RecoveryRequest from critical security finding")
				}
			case <-deadline:
				t.Fatal("timeout waiting for RecoveryRequest")
			}
		}

		if gotRR.StepID != "impl" {
			t.Errorf("RecoveryRequest.StepID = %q, want impl", gotRR.StepID)
		}

		snap := run.Snapshot()
		var parked bool
		for _, s := range snap.Steps {
			if s.ID == "impl" && s.Status == step.StatusAwaitingRecovery {
				parked = true
			}
		}
		if !parked {
			t.Errorf("step should be parked at awaiting_recovery; snapshot = %+v", snap.Steps)
		}

		run.Recover("impl", RecoverAbort, "")
		drainUntilFinished(t, ctrl, 5*time.Second)
	})

	t.Run("non-critical finding → no RecoveryRequest", func(t *testing.T) {
		wf, err := workflow.Decode(toml, "")
		if err != nil {
			t.Fatal(err)
		}
		exec := &securityEscalateExec{
			stepID: "impl",
			findings: []SecurityFinding{{
				Tier: "guard", Monitor: "secret-in-write",
				Severity: "high", Action: "blocked", Fingerprint: "fp-high-1",
			}},
			block: false,
		}
		mgr := NewManager(exec, "")
		_, ctrl := mgr.Subscribe()
		if _, err := mgr.Start(wf); err != nil {
			t.Fatal(err)
		}

		// Run should finish normally without a RecoveryRequest.
		var gotRR bool
		drainChecked(t, ctrl, 5*time.Second, func(e Event) bool {
			if _, ok := e.(RecoveryRequest); ok {
				gotRR = true
			}
			_, done := e.(RunFinished)
			return done
		})
		if gotRR {
			t.Error("non-critical finding should not produce a RecoveryRequest")
		}
	})

	t.Run("duplicate fingerprints → park once", func(t *testing.T) {
		wf, err := workflow.Decode(toml, "")
		if err != nil {
			t.Fatal(err)
		}
		const fp = "fp-dup-1"
		exec := &securityEscalateExec{
			stepID: "impl",
			findings: []SecurityFinding{
				{Tier: "guard", Monitor: "secret-in-write", Severity: "critical", Action: "escalated", Fingerprint: fp},
				{Tier: "guard", Monitor: "secret-in-write", Severity: "critical", Action: "escalated", Fingerprint: fp},
			},
			block: true,
		}
		mgr := NewManager(exec, "")
		_, ctrl := mgr.Subscribe()
		run, err := mgr.Start(wf)
		if err != nil {
			t.Fatal(err)
		}

		var rrCount int
		deadline := time.After(5 * time.Second)
	loop3:
		for {
			select {
			case e := <-ctrl:
				if _, ok := e.(RecoveryRequest); ok {
					rrCount++
					if rrCount == 1 {
						// Give a moment for a possible second RR to arrive.
						time.Sleep(50 * time.Millisecond)
						break loop3
					}
				}
			case <-deadline:
				t.Fatal("timeout waiting for RecoveryRequest")
			}
		}
		if rrCount != 1 {
			t.Errorf("want exactly 1 RecoveryRequest for duplicate fingerprints, got %d", rrCount)
		}

		run.Recover("impl", RecoverAbort, "")
		drainUntilFinished(t, ctrl, 5*time.Second)
	})
}

// drainChecked drains ctrl until pred returns true or timeout.
func drainChecked(t *testing.T, ctrl <-chan Event, timeout time.Duration, pred func(Event) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ctrl:
			if pred(e) {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for run to finish")
		}
	}
}
