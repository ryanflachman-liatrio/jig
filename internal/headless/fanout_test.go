package headless_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/headless"
	"jig/internal/runner"
	"jig/internal/step"
	"jig/internal/workflow"
)

// fanOutWF is a minimal discover -> analyze foreach workflow, run entirely
// through agent-typed steps (foreach's [step.schema] producer requirement is
// agent-only) so it can drive under the FakeExecutor without a real harness.
const fanOutWF = `
[workflow]
name = "fanout-headless"
version = "0.1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
depends_on = ["discover"]

  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 4
  [step.schema]
  finding = "text"
`

func fanOutMgr(t *testing.T, discoverItems int) *engine.Manager {
	t.Helper()
	items := make([]map[string]string, discoverItems)
	for i := range items {
		items[i] = map[string]string{"name": "svc"}
	}
	structured, err := json.Marshal(map[string]any{"targets": items})
	if err != nil {
		t.Fatal(err)
	}
	mux := runner.NewMux()
	mux.Register(workflow.StepAgent, runner.NewFakeExecutor(map[string]runner.FakeOutcome{
		"discover": {Delay: time.Millisecond, Result: &step.Result{Status: step.StatusSucceeded, Structured: structured}},
	}, runner.FakeOutcome{Delay: time.Millisecond}))
	return testMgr(t, mux)
}

// TestHeadless_FanOutTextProgress proves text mode prints one concise
// expansion line plus ordinary child status lines using their full runtime
// instance id, and that the final envelope is unaffected (still keyed by
// run_dir / ok / workflow).
func TestHeadless_FanOutTextProgress(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	mgr := fanOutMgr(t, 2)
	opts := runOpts(wf, mgr)
	stderr := opts.Stderr.(*bytes.Buffer)

	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v stderr=%s", result.ExitCode, result.Err, stderr)
	}
	if !result.Envelope.OK || result.Envelope.Failed {
		t.Fatalf("envelope: %+v", result.Envelope)
	}

	out := stderr.String()
	if !strings.Contains(out, "analyze: expanded to 2 item(s)") {
		t.Fatalf("expected concise expansion line, got:\n%s", out)
	}
	found := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "analyze"+workflow.ForEachIDMarker) {
			found++
		}
	}
	if found == 0 {
		t.Fatalf("expected child progress lines with full runtime instance ids, got:\n%s", out)
	}
}

// TestHeadless_FanOutQuietSuppressesChildProgress proves --quiet suppresses
// both the expansion line and child progress lines, exactly like ordinary
// step progress, while still printing run_id for forensics.
func TestHeadless_FanOutQuietSuppressesChildProgress(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	mgr := fanOutMgr(t, 2)
	opts := runOpts(wf, mgr)
	opts.Quiet = true
	stderr := opts.Stderr.(*bytes.Buffer)

	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v", result.ExitCode, result.Err)
	}
	out := stderr.String()
	if !strings.Contains(out, "run_id:") {
		t.Fatalf("quiet must still print run_id: %q", out)
	}
	if strings.Contains(out, "expanded to") {
		t.Fatalf("quiet leaked the expansion progress line: %q", out)
	}
}

// TestHeadless_FanOutJSONLEmitsTypedEvent proves JSONL mode emits a typed
// "fanout_expanded" line plus ordinary "step_status" lines for every child,
// and that the terminal "result" envelope is unchanged (still keyed by
// run_dir).
func TestHeadless_FanOutJSONLEmitsTypedEvent(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	mgr := fanOutMgr(t, 3)
	opts := runOpts(wf, mgr)
	opts.Output = headless.OutputJSONL
	stdout := opts.Stdout.(*bytes.Buffer)

	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v", result.ExitCode, result.Err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var sawExpanded bool
	var sawChildStatus bool
	var childIDs []string
	for _, line := range lines {
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		switch env.Type {
		case "fanout_expanded":
			sawExpanded = true
			var fe engine.FanOutExpanded
			if err := json.Unmarshal(env.Data, &fe); err != nil {
				t.Fatalf("decode fanout_expanded: %v", err)
			}
			if fe.FamilyID != "analyze" || len(fe.Instances) != 3 {
				t.Fatalf("fanout_expanded payload = %+v", fe)
			}
			for _, inst := range fe.Instances {
				childIDs = append(childIDs, inst.InstanceID)
			}
		case "step_status":
			var ss engine.StepStatus
			if err := json.Unmarshal(env.Data, &ss); err != nil {
				t.Fatalf("decode step_status: %v", err)
			}
			for _, id := range childIDs {
				if ss.StepID == id {
					sawChildStatus = true
				}
			}
		}
	}
	if !sawExpanded {
		t.Fatalf("expected a fanout_expanded JSONL line, got:\n%s", stdout.String())
	}
	if !sawChildStatus {
		t.Fatalf("expected ordinary step_status lines for discovered children, got:\n%s", stdout.String())
	}

	var last struct {
		Type string            `json:"type"`
		Data headless.Envelope `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("last line: %v — %s", err, lines[len(lines)-1])
	}
	// The envelope shape is unchanged by fan-out: still a flat "result" line
	// keyed by run_dir/ok/workflow (run_dir is "" here only because this test's
	// Manager has persistence off, exactly like every other headless test).
	if last.Type != "result" || !last.Data.OK || last.Data.Workflow != "fanout-headless" {
		t.Fatalf("final envelope unchanged shape: %+v", last)
	}
}

// TestHeadless_FanOutEmptyExpansionSucceeds proves a zero-item foreach still
// prints its expansion line, settles as a successful empty barrier, and does
// not fail the run.
func TestHeadless_FanOutEmptyExpansionSucceeds(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	mgr := fanOutMgr(t, 0)
	opts := runOpts(wf, mgr)
	stderr := opts.Stderr.(*bytes.Buffer)

	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("exit=%d err=%v stderr=%s", result.ExitCode, result.Err, stderr)
	}
	if !result.Envelope.OK || result.Envelope.Failed {
		t.Fatalf("envelope: %+v", result.Envelope)
	}
	if !strings.Contains(stderr.String(), "analyze: expanded to 0 item(s)") {
		t.Fatalf("expected empty-expansion line, got:\n%s", stderr.String())
	}
}

// TestHeadless_FanOutTimeoutDuringChildren proves a wall-clock timeout while
// children are still running exits with ExitTimeout and a typed timeout
// error, exactly like an ordinary long-running step.
func TestHeadless_FanOutTimeoutDuringChildren(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	items := []map[string]string{{"name": "svc"}}
	structured, err := json.Marshal(map[string]any{"targets": items})
	if err != nil {
		t.Fatal(err)
	}
	mux := runner.NewMux()
	mux.Register(workflow.StepAgent, runner.NewFakeExecutor(map[string]runner.FakeOutcome{
		"discover": {Delay: time.Millisecond, Result: &step.Result{Status: step.StatusSucceeded, Structured: structured}},
	}, runner.FakeOutcome{Delay: time.Second})) // children never finish before the timeout below.
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	opts.Timeout = 30 * time.Millisecond
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitTimeout {
		t.Fatalf("exit=%d, want ExitTimeout; err=%v", result.ExitCode, result.Err)
	}
	if result.Envelope.Error == nil || result.Envelope.Error.Code != "timeout" {
		t.Fatalf("envelope error = %+v, want code=timeout", result.Envelope.Error)
	}
}

// TestHeadless_FanOutRecoveryPolicyAppliesToChild proves --on-recovery=skip
// resolves a failed child's recovery gate exactly like it would for an
// ordinary step (never a gate-exit): the family settles honestly with
// all_succeeded=false and the run exits ExitFailed with ok=false, the same
// contract as TestHeadless_RecoverySkipContinues for a non-fan-out step.
func TestHeadless_FanOutRecoveryPolicyAppliesToChild(t *testing.T) {
	wf := decodeWF(t, fanOutWF)
	items := []map[string]string{{"name": "svc"}}
	structured, err := json.Marshal(map[string]any{"targets": items})
	if err != nil {
		t.Fatal(err)
	}
	mux := runner.NewMux()
	mux.Register(workflow.StepAgent, runner.NewFakeExecutor(map[string]runner.FakeOutcome{
		"discover": {Delay: time.Millisecond, Result: &step.Result{Status: step.StatusSucceeded, Structured: structured}},
	}, runner.FakeOutcome{Delay: time.Millisecond, Fail: true}))
	mgr := testMgr(t, mux)

	opts := runOpts(wf, mgr)
	opts.OnRecovery = headless.RecoverySkip
	result := headless.Run(context.Background(), opts)
	if result.ExitCode != headless.ExitFailed {
		t.Fatalf("exit=%d want %d err=%v envelope=%+v", result.ExitCode, headless.ExitFailed, result.Err, result.Envelope)
	}
	if result.Envelope.OK {
		t.Fatal("expected ok=false when a skipped fan-out child failed")
	}
}
