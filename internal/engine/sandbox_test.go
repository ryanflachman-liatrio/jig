package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const d04DockerWorkflow = `[workflow]
name='d04-synthetic'
version='1'
[[sandbox]]
name='box'
runtime='docker'
image='alpine'
cpus=0.5
memory='6MiB'
env={COLOR='synthetic-private-value'}
[[step]]
id='work'
type='command'
run='true'
sandbox='box'
`

type d04Spy struct{ executions, validations, resolutions atomic.Int32 }

func (s *d04Spy) Execute(context.Context, StepRequest, Reporter) (*step.Result, error) {
	s.executions.Add(1)
	return &step.Result{Status: step.StatusSucceeded}, nil
}
func (s *d04Spy) Validate(context.Context, StepRequest, Reporter) (bool, string, error) {
	s.validations.Add(1)
	return true, "", nil
}
func (s *d04Spy) ResolveIntegration(context.Context, IntegrationResolutionRequest, Reporter) (*step.Result, error) {
	s.resolutions.Add(1)
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func d04Decode(t *testing.T, source string) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestD04OfflineAdmission(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "skipped"}[skipped], func(t *testing.T) {
			source := d04DockerWorkflow + "secrets=['synthetic']\n[step.validate]\ncommand='true'\n"
			if skipped {
				source = strings.Replace(source, "[[step]]", "[[step]]\nid='ready'\ntype='command'\nrun='true'\noutput_type={enum=['yes','no']}\n[[step]]", 1)
				source = strings.Replace(source, "run='true'\nsandbox='box'", "run='true'\nsandbox='box'\ndepends_on=['ready']\nwhen=\"ready == 'no'\"", 1)
			}
			wf := d04Decode(t, source)
			spy := &d04Spy{}
			root := filepath.Join(t.TempDir(), "must-not-exist")
			m := NewManager(spy, root)
			m.SetIntegrationResolver(spy)
			var secrets atomic.Int32
			m.SetSecretResolver(func(string) (string, error) { secrets.Add(1); return "", errors.New("must not resolve") })
			run, err := m.Start(wf)
			var unsupported *SandboxRuntimeError
			if run != nil || !errors.As(err, &unsupported) || unsupported.StepID != "work" {
				t.Fatalf("admission: run=%v err=%v", run, err)
			}
			if spy.executions.Load()+spy.validations.Load()+spy.resolutions.Load()+secrets.Load() != 0 {
				t.Fatal("unauthorized phase launched")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("denial created run/worktree state")
			}
			t.Log("sandbox_runtime_unsupported; workload=0 validator=0 resolver=0 secret=0; no storage created")
		})
	}
	// Direct manager callers cannot turn unresolved selection/defaults into host.
	for _, defaults := range []bool{false, true} {
		wf := d04Decode(t, strings.Replace(d04DockerWorkflow, "sandbox='box'", "sandbox='host'", 1))
		wf.Steps[0].SnapshotSandbox = nil
		name := "box"
		if defaults {
			wf.Steps[0].Sandbox = nil
			wf.Defaults.Sandbox = &name
		} else {
			wf.Steps[0].Sandbox = &name
		}
		if _, err := NewManager(&d04Spy{}, "").Start(wf); err == nil {
			t.Fatal("unresolved selection fell back to host")
		}
	}
}

func TestD04OfflineSnapshotOwnership(t *testing.T) {
	wf := d04Decode(t, d04DockerWorkflow)
	prepared, _, err := workflow.PrepareAuthoring(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	original := prepared.Steps[0].ResolvedSandbox()
	wf.Steps[0].SnapshotSandbox.Profile.Env["COLOR"] = "changed"
	*wf.Steps[0].SnapshotSandbox.Profile.CPUs = 1
	if !reflect.DeepEqual(original, prepared.Steps[0].ResolvedSandbox()) {
		t.Fatal("prepared graph aliases caller")
	}
	raw, err := encodeWorkflowSnapshot(prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkflowSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, decoded.Steps[0].ResolvedSandbox()) {
		t.Fatal("snapshot changed request")
	}
	child := cloneForEachTemplate(&prepared.Steps[0], "work[0]")
	child.SnapshotSandbox.Profile.Env["COLOR"] = "child"
	if !reflect.DeepEqual(original, prepared.Steps[0].ResolvedSandbox()) {
		t.Fatal("fan-out child aliases template")
	}
	s := &scheduler{states: map[string]*step.State{"work": {}}, preResolvedInputs: map[string][]ResolvedInput{}}
	request := s.buildRequest(&prepared.Steps[0], "synthetic", "", "", "", "")
	request.Step.SnapshotSandbox.Profile.Env["COLOR"] = "executor"
	if !reflect.DeepEqual(original, prepared.Steps[0].ResolvedSandbox()) {
		t.Fatal("executor aliases scheduler")
	}
	t.Log("authored presence, normalized image and lexical origin survive snapshot; caller, child and executor mutations isolated")
}

func TestD04OfflineSnapshotIntegrity(t *testing.T) {
	for _, damage := range []string{"section", "version", "inner_version", "coverage", "extra", "digest", "normalized", "mismatch"} {
		t.Run(damage, func(t *testing.T) {
			raw, err := encodeWorkflowSnapshot(d04Decode(t, d04DockerWorkflow), nil)
			if err != nil {
				t.Fatal(err)
			}
			var snap workflowSnapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "section":
				snap.Sandbox = nil
			case "version":
				snap.SandboxVersion = 2
			case "inner_version":
				snap.Sandbox.Version = 2
			case "coverage":
				delete(snap.Sandbox.Requests, "work")
			case "extra":
				snap.Sandbox.Requests["extra"] = snap.Sandbox.Requests["work"]
			case "digest":
				snap.Sandbox.SHA256 = strings.Repeat("0", 64)
			case "normalized":
				r := snap.Sandbox.Requests["work"]
				r.Profile.NormalizedImage = "wrong"
				snap.Sandbox.Requests["work"] = r
				b, _ := json.Marshal(snap.Sandbox.Requests)
				snap.Sandbox.SHA256 = authoringDigest(b)
			case "mismatch":
				snap.ExpandedSteps[0].SnapshotSandbox.Profile.Env["COLOR"] = "altered"
			}
			raw, err = json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkflowSnapshot(raw); !errors.Is(err, workflow.CaptureIntegrity) {
				t.Fatalf("corruption accepted: %v", err)
			}
		})
	}
}

func TestD04OfflineReopenAndLegacy(t *testing.T) {
	// Persist Docker only as a synthetic snapshot, never as an executed run.
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.toml")
	if err := os.WriteFile(path, []byte(d04DockerWorkflow), 0600); err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".jig")
	rd, err := datastore.RunDir(root, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(rd, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, rd, []Event{RunStarted{RunID: "synthetic", Workflow: "d04-synthetic", Steps: []string{"work"}}, StepStatus{RunID: "synthetic", StepID: "work", From: "pending", To: "running"}})
	if err := os.WriteFile(path, []byte("BROKEN CHECKOUT"), 0600); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadWorkflowSnapshot(rd)
	if err != nil || stored.Steps[0].ResolvedSandbox().Profile.Image != "alpine" {
		t.Fatalf("display consulted checkout: %v", err)
	}
	spy := &d04Spy{}
	_, err = NewManager(spy, root).Resume("synthetic")
	var unsupported *SandboxRuntimeError
	if !errors.As(err, &unsupported) || spy.executions.Load() != 0 {
		t.Fatalf("reopen fallback: %v", err)
	}
	for _, legacyDocker := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_host", true: "legacy_sandbox"}[legacyDocker], func(t *testing.T) {
			root, rd, manifest, snap := savedCaptureRun(t)
			snap.SandboxVersion = 0
			snap.Sandbox = nil
			for i := range snap.ExpandedSteps {
				snap.ExpandedSteps[i].SnapshotSandbox = nil
			}
			for i := range snap.PublicSteps {
				snap.PublicSteps[i].SnapshotSandbox = nil
			}
			if legacyDocker {
				snap.ExpandedSteps[0].Sandbox = new(string)
				*snap.ExpandedSteps[0].Sandbox = "box"
			}
			resignCaptureRecords(t, rd, manifest, snap)
			m := NewManager(&d04Spy{}, root)
			run, err := m.Resume("saved")
			if legacyDocker {
				if err == nil {
					run.Cancel()
					t.Fatal("sandbox-bearing legacy reopened")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			run.Cancel()
			select {
			case <-run.done:
			case <-time.After(3 * time.Second):
				t.Fatal("host legacy cancellation hung")
			}
		})
	}
}

func TestD04OfflinePersistenceOff(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	source := strings.Replace(d04DockerWorkflow, "sandbox='box'", "sandbox='host'", 1)
	spy := &d04Spy{}
	run, err := NewManager(spy, "").Start(d04Decode(t, source))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.done:
	case <-time.After(3 * time.Second):
		run.Cancel()
		t.Fatal("host run hung")
	}
	if spy.executions.Load() != 1 {
		t.Fatal("host baseline did not execute")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("persistence-off wrote files: %v %v", entries, err)
	}
	if err := persistWorkflowSnapshot("", d04Decode(t, d04DockerWorkflow)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("empty snapshot root wrote relative files")
	}
	t.Log("host workload executed once; no relative run/artifact/sandbox files")
}
