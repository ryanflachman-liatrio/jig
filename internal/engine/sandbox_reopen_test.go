package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04ReopenPolicyLifecycle(t *testing.T) {
	root, rd, _, _ := savedCaptureRun(t)
	allow := d04HostPolicy(t, true)
	var permitted atomic.Bool
	var loads atomic.Int32
	spy := &d04Spy{}
	mgr := NewManager(spy, root)
	mgr.SetIntegrationResolver(spy)
	var secrets atomic.Int32
	mgr.SetSecretResolver(func(string) (string, error) { secrets.Add(1); return "", nil })
	mgr.SetSandboxEpochLoader(func(ctx context.Context, wf *workflow.Workflow, p *SandboxPolicyState) (SandboxAuthorizer, *SandboxPolicyState, error) {
		loads.Add(1)
		if !permitted.Load() {
			return nil, nil, &sandbox.Failure{Reason: "sandbox_policy_denied"}
		}
		if p == nil {
			p = builtinSandboxState(wf)
		}
		return allow, p, nil
	})
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Resume("saved")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { run.Cancel(); run.Wait() }()
	b := run.Snapshot().PolicyBlock
	if b == nil {
		t.Fatal("missing block")
	}
	select {
	case ev := <-ctrl:
		if _, ok := ev.(SandboxPolicyBlocked); !ok {
			t.Fatalf("first publication %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no block")
	}
	events, err := ReplayJournalRaw(rd)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if _, ok := e.(SandboxPolicyBlocked); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("publication preceded journal")
	}
	original := run.Snapshot().Steps
	for _, action := range []string{RecoverRetry, RecoverResume, RecoverSkip, RecoverAbort} {
		run.Recover("worker", action, "")
	}
	run.Reset("worker")
	run.Resume("worker", "")
	run.Resolve("worker", "yes")
	run.ProvideUserInput("worker", "x", "y")
	run.SendInput("worker", "x")
	run.ResolveIntegrationWithAgent("worker")
	run.ResolveIntegration("worker", false)
	run.FinalMerge(true)
	if !reflect.DeepEqual(original, run.Snapshot().Steps) {
		t.Fatal("command bypass changed parked state")
	}
	if spy.executions.Load()+spy.validations.Load()+spy.resolutions.Load()+secrets.Load() != 0 {
		t.Fatal("blocked effect")
	}
	// Denied recheck consumes no step attempt, returns a new block epoch.
	run.RecheckSandboxPolicy(b.Epoch)
	next := waitD04Block(t, run, b.Epoch+1)
	if !reflect.DeepEqual(original, run.Snapshot().Steps) {
		t.Fatal("recheck consumed attempt")
	}
	permitted.Store(true)
	run.RecheckSandboxPolicy(b.Epoch) // stale UI action must not authorize
	if run.Snapshot().PolicyBlock == nil {
		t.Fatal("stale recheck cleared block")
	}
	run.RecheckSandboxPolicy(next.Epoch)
	deadline := time.After(3 * time.Second)
	for run.Snapshot().PolicyBlock != nil {
		select {
		case <-deadline:
			t.Fatal("grant timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !reflect.DeepEqual(original, run.Snapshot().Steps) || spy.executions.Load() != 0 {
		t.Fatal("grant answered underlying recovery")
	}
	run.Recover("worker", RecoverRetry, "")
	done := make(chan RunSnapshot, 1)
	go func() { done <- run.Wait() }()
	select {
	case snap := <-done:
		if snap.Failed {
			t.Fatalf("failed %+v", snap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("work did not settle")
	}
	if spy.executions.Load() != 2 || loads.Load() != 3 {
		t.Fatalf("executions=%d policy loads=%d", spy.executions.Load(), loads.Load())
	}
	t.Log("block durable before publication; all bypasses fenced; denied recheck adds no attempt; grant retains recovery; original two steps complete")
}
func waitD04Block(t *testing.T, r *Run, epoch uint64) *SandboxPolicyBlocked {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b := r.Snapshot().PolicyBlock
		if b != nil && b.Epoch >= epoch {
			return b
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("block result timeout")
	return nil
}
func TestD04ReopenPolicyAbortDuringRecheck(t *testing.T) {
	root, _, _, _ := savedCaptureRun(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	exited := make(chan struct{})
	mgr := NewManager(&d04Spy{}, root)
	mgr.SetSandboxEpochLoader(func(ctx context.Context, _ *workflow.Workflow, _ *SandboxPolicyState) (SandboxAuthorizer, *SandboxPolicyState, error) {
		if calls.Add(1) == 1 {
			return nil, nil, errors.New("deny")
		}
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, nil, ctx.Err()
	})
	run, err := mgr.Resume("saved")
	if err != nil {
		t.Fatal(err)
	}
	b := run.Snapshot().PolicyBlock
	run.RecheckSandboxPolicy(b.Epoch)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no check")
	}
	for i := 0; i < 10; i++ {
		run.RecheckSandboxPolicy(b.Epoch)
	}
	run.AbortSandboxPolicy(b.Epoch)
	done := make(chan struct{})
	go func() { run.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("abort blocked by policy I/O")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("policy I/O not cancelled")
	}
	if calls.Load() != 2 {
		t.Fatal("rechecks not coalesced")
	}
}
func TestD04ReopenPolicyJournalFailure(t *testing.T) {
	wf := d04Decode(t, "[workflow]\nname='p'\nversion='1'\n[[step]]\nid='x'\ntype='command'\nrun='true'\n")
	writer, err := manifest.NewWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	s := newScheduler(wf, "test", make(chan schedMsg, 8), nil, &d04Spy{}, func() {}, writer, "", "", "", nil)
	s.policyBlock = &SandboxPolicyBlocked{RunID: "test", Epoch: 1, Reason: "sandbox_policy_denied"}
	s.policyChecking = true
	sandboxRecheckResult{epoch: 1, state: builtinSandboxState(wf)}.execute(s)
	if s.policyBlock == nil || s.fatalJournalErr == nil {
		t.Fatal("failed grant removed fence")
	}
	if err := s.authorizeSandboxStep(&wf.Steps[0]); err == nil {
		t.Fatal("direct executable bypass")
	}
}
func TestD04ReopenSnapshotPolicyIntegrity(t *testing.T) {
	wf := d04Decode(t, d04DockerWorkflow)
	p := builtinSandboxState(wf)
	p.Source = SandboxPolicySource{Kind: "explicit", Path: "/synthetic/operator/policy.toml"}
	p.Effective["work"] = SandboxEffective{NanoCPUs: 500000000, MemoryBytes: 6 << 20, PIDs: 64, CPUSource: "authored", MemorySource: "authored", PIDsSource: "policy_default", Platform: "linux/amd64"}
	p.Binding = &SandboxBinding{URI: "unix:///synthetic/docker.sock", DaemonID: "synthetic", API: "1.54", NativePlatform: "linux/amd64", EndpointSource: "flag"}
	raw, err := encodeWorkflowSnapshot(wf, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Effective["work"] = SandboxEffective{}
	p.Binding.DaemonID = "changed"
	if _, err := DecodeWorkflowSnapshot(raw); err != nil {
		t.Fatal(err)
	}
	var snap workflowSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Sandbox.Policy.Binding.DaemonID != "synthetic" || snap.Sandbox.Policy.Effective["work"].PIDs != 64 {
		t.Fatal("snapshot aliases caller")
	}
	for _, kind := range []string{"version", "coverage", "binding", "source", "effective", "provenance", "digest"} {
		t.Run(kind, func(t *testing.T) {
			var damaged workflowSnapshot
			json.Unmarshal(raw, &damaged)
			switch kind {
			case "version":
				damaged.SandboxVersion = 99
			case "coverage":
				delete(damaged.Sandbox.Policy.Effective, "work")
			case "binding":
				damaged.Sandbox.Policy.Binding = nil
			case "source":
				damaged.Sandbox.Policy.Source.Kind = "project"
			case "effective":
				v := damaged.Sandbox.Policy.Effective["work"]
				v.PIDs = 0
				damaged.Sandbox.Policy.Effective["work"] = v
			case "provenance":
				v := damaged.Sandbox.Policy.Effective["work"]
				v.CPUSource = "policy_default"
				damaged.Sandbox.Policy.Effective["work"] = v
			case "digest":
				damaged.Sandbox.SHA256 = "bad"
			}
			if kind != "digest" {
				damaged.Sandbox.SHA256 = sandboxSnapshotDigest(damaged.Sandbox)
			}
			bad, _ := json.Marshal(damaged)
			if _, err := DecodeWorkflowSnapshot(bad); err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
	t.Log("synthetic Docker capture: authored/effective/binding separate; immutable; complete coverage/version/digest verified without external reads")
}
func TestD04ReopenSnapshotHostPersistence(t *testing.T) {
	for _, persist := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "disk"}[persist], func(t *testing.T) {
			root := ""
			if persist {
				root = filepath.Join(t.TempDir(), ".jig")
			}
			wf := d04Decode(t, "[workflow]\nname='p'\nversion='1'\n[[step]]\nid='x'\ntype='command'\nrun='true'\n")
			m := NewManager(&d04Spy{}, root)
			r, err := m.Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			r.Wait()
			if persist {
				data, err := os.ReadFile(filepath.Join(m.RunDir(r.ID), "workflow.json"))
				if err != nil {
					t.Fatal(err)
				}
				var snap workflowSnapshot
				json.Unmarshal(data, &snap)
				if snap.SandboxVersion != 2 || len(snap.Sandbox.Policy.Effective) != 1 {
					t.Fatal("missing policy snapshot")
				}
			}
		})
	}
}

func TestD04ReopenPolicyMixedParks(t *testing.T) {
	root, rd, _, _ := savedCaptureRun(t)
	writeJournal(t, rd, []Event{RunStarted{RunID: "saved", Workflow: "capture", Steps: []string{"worker", "future"}}, StepStatus{RunID: "saved", StepID: "worker", To: "awaiting_recovery"}, RecoveryRequest{RunID: "saved", StepID: "worker", Err: "keep recovery"}, StepStatus{RunID: "saved", StepID: "future", To: "stopped"}})
	mgr := NewManager(&d04Spy{}, root)
	mgr.SetSandboxAuthorizer(d04HostPolicy(t, false))
	run, err := mgr.Resume("saved")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { run.Cancel(); run.Wait() }()
	snap := run.Snapshot()
	if snap.PolicyBlock == nil || snap.Steps[0].Status != "awaiting_recovery" || snap.Steps[1].Status != "stopped" {
		t.Fatalf("parks replaced %+v", snap)
	}
	// Snapshot projections never expose mutable policy block storage.
	snap.PolicyBlock.Reason = "changed"
	if run.Snapshot().PolicyBlock.Reason == "changed" {
		t.Fatal("snapshot aliases live policy")
	}
}

func TestD04ReopenSnapshotEpochHistory(t *testing.T) {
	wf := d04Decode(t, "[workflow]\nname='p'\nversion='1'\n[[step]]\nid='x'\ntype='command'\nrun='true'\n")
	original := builtinSandboxState(wf)
	current := original.Clone()
	current.Source = SandboxPolicySource{Kind: "explicit", Path: "/synthetic/reselected.toml"}
	current.Fingerprint = authoringDigest([]byte("changed current policy"))
	restored, err := restoredSandboxPolicy(wf, original, []Event{SandboxPolicyCleared{RunID: "r", Epoch: 1, Policy: current}})
	if err != nil || restored.Source != current.Source {
		t.Fatalf("lost explicit reselection: %+v %v", restored, err)
	}
	current.Effective["x"] = SandboxEffective{PIDs: 1}
	if restored.Effective["x"].PIDs != 0 {
		t.Fatal("epoch aliases journal")
	}
	if _, err := restoredSandboxPolicy(wf, original, []Event{SandboxPolicyCleared{Policy: current}}); err == nil {
		t.Fatal("grant changed locked resources")
	}
}

func TestD04ReopenPolicyConsumerGuard(t *testing.T) {
	root, rd, _, _ := savedCaptureRun(t)
	before, err := os.ReadFile(filepath.Join(rd, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	spy := &d04Spy{}
	m := NewManager(spy, root)
	rejected := errors.New("human gate not eligible for automation")
	if r, err := m.ResumeWithGuard("saved", func(blocked bool) error {
		if blocked {
			t.Fatal("unexpected policy block")
		}
		return rejected
	}); r != nil || !errors.Is(err, rejected) {
		t.Fatalf("guard: %v %v", r, err)
	}
	after, err := os.ReadFile(filepath.Join(rd, "journal.jsonl"))
	if err != nil || string(before) != string(after) || spy.executions.Load() != 0 {
		t.Fatal("eligibility rejection restored work or published events")
	}
	m.SetSandboxAuthorizer(d04HostPolicy(t, false))
	r, err := m.ResumeWithGuard("saved", func(blocked bool) error {
		if !blocked {
			t.Fatal("guard missed policy denial")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().PolicyBlock == nil {
		t.Fatal("guard granted permission")
	}
	r.Cancel()
	r.Wait()
}
