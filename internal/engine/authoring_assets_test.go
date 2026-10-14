package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type captureExecFunc func(context.Context, StepRequest, Reporter) (*step.Result, error)

func (f captureExecFunc) Execute(ctx context.Context, r StepRequest, p Reporter) (*step.Result, error) {
	return f(ctx, r, p)
}
func writeCaptureFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func captureFixture(t *testing.T, source string) (string, *workflow.Workflow) {
	t.Helper()
	dir := t.TempDir()
	writeCaptureFixture(t, dir, "script.sh", []byte("echo ORIGINAL\n"))
	writeCaptureFixture(t, dir, "workflow.toml", []byte("[workflow]\nname='capture'\nversion='1'\n"+source))
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, wf
}
func waitCaptureRun(t *testing.T, r *Run) RunSnapshot {
	t.Helper()
	done := make(chan RunSnapshot, 1)
	go func() { done <- r.Wait() }()
	select {
	case s := <-done:
		return s
	case <-time.After(10 * time.Second):
		r.Cancel()
		t.Fatal("run did not finish")
		return RunSnapshot{}
	}
}
func TestStartAuthoringAssetFailure(t *testing.T) {
	for _, mode := range []string{"missing_late_script", "oversized_late_script", "run_directory", "lease", "journal", "publication"} {
		t.Run(mode, func(t *testing.T) {
			dir, wf := captureFixture(t, "[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='late'\ntype='command'\nscript='script.sh'\n")
			root := filepath.Join(dir, ".jig")
			var starts atomic.Int32
			m := NewManager(captureExecFunc(func(context.Context, StepRequest, Reporter) (*step.Result, error) {
				starts.Add(1)
				return &step.Result{Status: step.StatusSucceeded}, nil
			}), root)
			switch mode {
			case "missing_late_script":
				if err := os.Remove(filepath.Join(dir, "script.sh")); err != nil {
					t.Fatal(err)
				}
			case "oversized_late_script":
				writeCaptureFixture(t, dir, "script.sh", bytes.Repeat([]byte("x"), (1<<20)+1))
			case "run_directory":
				writeCaptureFixture(t, dir, ".jig", []byte("not a directory"))
			case "lease", "journal":
				m.startCheck = func(stage, runDir string) error {
					if stage == mode {
						target := datastore.SchedulerLockPath(runDir)
						if mode == "journal" {
							target = datastore.JournalPath(runDir)
						}
						return os.Mkdir(target, 0700)
					}
					return nil
				} // actual open must fail on the directory
			case "publication":
				m.publishAuthoring = func(string, []byte, map[string][]byte, []byte) error { return errors.New("injected storage failure") }
			}
			run, err := m.Start(wf)
			if err == nil || run != nil || starts.Load() != 0 || len(m.Runs()) != 0 {
				t.Fatalf("err=%v registered=%d workers=%d", err, len(m.Runs()), starts.Load())
			}
			ids, _ := m.PersistedRuns()
			for _, id := range ids {
				rd := m.RunDir(id)
				if mode != "lease" {
					held, err := RunLockState(rd)
					if err != nil || held {
						t.Fatalf("lease leak=%v err=%v", held, err)
					}
				}
				if _, err := os.Stat(datastore.WorkflowSnapshotPath(rd)); !os.IsNotExist(err) {
					t.Fatal("failed preparation committed workflow")
				}
			}
			t.Logf("%s: error=%v workers=0 registered=0", mode, err)
		})
	}
}
func TestAuthoringCaptureFatalBoundaries(t *testing.T) {
	for _, mode := range []string{"manifest_before_dispatch", "blob_before_dispatch", "blob_last_worker", "manifest_last_worker", "snapshot_last_worker"} {
		t.Run(mode, func(t *testing.T) {
			dir, wf := captureFixture(t, "[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\ndepends_on=['ready']\non_failure='retry'\nmax_retries=3\n[[step]]\nid='later'\ntype='command'\nrun='true'\ndepends_on=['worker']\non_failure='continue'\n")
			ready := make(chan StepRequest, 1)
			release := make(chan struct{})
			var starts atomic.Int32
			target := "ready"
			if strings.Contains(mode, "last_worker") {
				target = "worker"
				wf.Steps = wf.Steps[:2]
			}
			exec := captureExecFunc(func(ctx context.Context, r StepRequest, _ Reporter) (*step.Result, error) {
				starts.Add(1)
				if r.Step.ID == target {
					ready <- r
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return &step.Result{Status: step.StatusSucceeded}, nil
			})
			m := NewManager(exec, filepath.Join(dir, ".jig"))
			run, err := m.Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			var req StepRequest
			select {
			case req = <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("no worker barrier")
			}
			path := "authoring/manifest.json"
			if strings.HasPrefix(mode, "blob") {
				for _, b := range req.Assets.Manifest().Bindings {
					if b.Kind == workflow.AssetScript {
						path = "authoring/blobs/" + b.SHA256
						break
					}
				}
			}
			if strings.HasPrefix(mode, "snapshot") {
				path = "workflow.json"
			}
			if err := os.WriteFile(filepath.Join(m.RunDir(run.ID), path), []byte("DAMAGED"), 0600); err != nil {
				t.Fatal(err)
			}
			writeCaptureFixture(t, dir, "script.sh", []byte("echo SUBSTITUTE\n"))
			close(release)
			snap := waitCaptureRun(t, run)
			if !snap.Failed {
				t.Fatal("capture failure accepted")
			}
			expected := int32(1)
			if target == "worker" {
				expected = 2
			}
			if starts.Load() != expected {
				t.Fatalf("starts=%d want=%d", starts.Load(), expected)
			}
			run.Recover("worker", RecoverRetry, "")
			if starts.Load() != expected {
				t.Fatal("recovery bypassed capture failure")
			}
			_, events, err := readJournal(m.RunDir(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, ev := range events {
				if e, ok := ev.(RunError); ok && strings.HasPrefix(e.Err, "authoring capture:") {
					found = true
				}
			}
			if !found {
				t.Fatal("missing durable capture error")
			}
			t.Logf("%s: starts=%d; no retry/recovery/late success accepted", mode, starts.Load())
		})
	}
}

func TestAuthoringCaptureCancelsAndRejectsLateSuccess(t *testing.T) {
	dir, wf := captureFixture(t, "[defaults]\nmax_parallel=2\n[[step]]\nid='detect'\ntype='command'\nscript='script.sh'\non_failure='continue'\n[[step]]\nid='sibling'\ntype='command'\nrun='true'\n")
	entered := make(chan StepRequest, 2)
	release := make(chan struct{})
	cancelled := make(chan struct{})
	late := make(chan struct{})
	m := NewManager(captureExecFunc(func(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
		entered <- req
		if req.Step.ID == "detect" {
			<-release
		} else {
			<-ctx.Done()
			close(cancelled)
			<-late
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}), filepath.Join(dir, ".jig"))
	run, err := m.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Cancel(); run.Wait() })
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("dispatch barrier")
		}
	}
	if err := os.Remove(filepath.Join(m.RunDir(run.ID), "authoring/manifest.json")); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("sibling was not cancelled")
	}
	run.Recover("detect", RecoverRetry, "")
	run.Recover("detect", RecoverSkip, "")
	close(late)
	snap := waitCaptureRun(t, run)
	if !snap.Failed {
		t.Fatal("late success cleared fatal state")
	}
	for _, st := range snap.Steps {
		if st.Status == step.StatusSucceeded {
			t.Fatal("accepted a late contribution")
		}
	}
	t.Log("both workers started; damaged capture cancelled sibling; late success and retry/skip rejected")
}

func TestAuthoringCaptureFinalLanding(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeCaptureFixture(t, repo, "script.sh", []byte("echo ORIGINAL\n"))
	writeCaptureFixture(t, repo, "workflow.toml", []byte("[workflow]\nname='capture-landing'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\nisolation='worktree'\n"))
	wf, err := workflow.Load(filepath.Join(repo, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := currentHEAD(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(captureExecFunc(func(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "contribution.txt"), []byte("retained prior integration"), 0600); err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}), filepath.Join(repo, ".jig"))
	_, ctrl := m.Subscribe()
	run, err := m.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Cancel(); run.Wait() })
	branch := ""
	for branch == "" {
		select {
		case e := <-ctrl:
			if gate, ok := e.(FinalMergeRequest); ok {
				branch = gate.RunBranch
			}
		case <-time.After(10 * time.Second):
			t.Fatal("final merge gate not reached")
		}
	}
	integrated := gitOutput(repo, "rev-parse", branch)
	if err := os.Remove(filepath.Join(m.RunDir(run.ID), "authoring/manifest.json")); err != nil {
		t.Fatal(err)
	}
	run.FinalMerge(true)
	snap := waitCaptureRun(t, run)
	if !snap.Failed {
		t.Fatal("damaged run landed")
	}
	after, err := currentHEAD(repo)
	if err != nil || after != before {
		t.Fatalf("base changed: %v", err)
	}
	if got := gitOutput(repo, "rev-parse", branch); got != integrated {
		t.Fatal("previously integrated history lost")
	}
	t.Log("damage while final gate awaited approval: base unchanged; existing run-branch commit retained")
}

// Explicit barriers in the existing post-execution chain isolate integration
// and completion checks after the result-acceptance check has already passed.
func TestAuthoringCaptureIntegrationAndCompletion(t *testing.T) {
	for _, boundary := range []string{"integration", "completion"} {
		t.Run(boundary, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			writeCaptureFixture(t, repo, "script.sh", []byte("echo ORIGINAL\n"))
			wf, err := workflow.Decode("[workflow]\nname='capture-boundary'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\nisolation='worktree'\n", repo)
			if err != nil {
				t.Fatal(err)
			}
			wf, assets, err := workflow.PrepareAuthoring(wf, repo)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newRunAuthoring(assets)
			if err != nil {
				t.Fatal(err)
			}
			rd, err := datastore.RunDir(filepath.Join(repo, ".jig"), "boundary")
			if err != nil {
				t.Fatal(err)
			}
			if err := authority.publish(rd, wf, datastore.PublishAuthoring); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan RunSnapshot, 1)
			ex := captureExecFunc(func(_ context.Context, r StepRequest, _ Reporter) (*step.Result, error) {
				return &step.Result{Status: step.StatusSucceeded}, os.WriteFile(filepath.Join(r.Worktree, "contribution.txt"), []byte("new contribution"), 0600)
			})
			s := newScheduler(wf, "boundary", make(chan schedMsg, 64), nil, ex, cancel, nil, rd, filepath.Join(repo, ".jig"), repo, func(snap RunSnapshot) { done <- snap })
			s.authoring = authority
			damage := func(s *scheduler, _ stepDoneMsg, _ *workflow.Step) postExecDecision {
				if err := os.Remove(filepath.Join(rd, "authoring/manifest.json")); err != nil {
					panic(err)
				}
				return decisionContinue
			}
			if boundary == "integration" {
				s.postExecChain = []postExecHandler{damage, phSquashMergeIntegration}
			} else {
				s.postExecChain = []postExecHandler{phSquashMergeIntegration, damage}
			}
			go s.run(ctx)
			select {
			case snap := <-done:
				if !snap.Failed {
					t.Fatal("damage accepted")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("boundary did not fail")
			}
			if boundary == "integration" && len(s.stepCommits) != 0 {
				t.Fatal("integrated after damage")
			}
			if boundary == "completion" && len(s.stepCommits) != 1 {
				t.Fatal("lost previously integrated commit")
			}
			t.Logf("%s: capture failure; retained integration commits=%d", boundary, len(s.stepCommits))
		})
	}
}

// A controller can stop after recording the fatal cause but before RunFinished.
// Restoring intact bytes must not erase that durable fence.
func TestAuthoringCaptureFatalCauseSurvivesReopen(t *testing.T) {
	dir, wf := captureFixture(t, "[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\n")
	root := filepath.Join(dir, ".jig")
	rd, err := datastore.RunDir(root, "fatal")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(rd, wf); err != nil {
		t.Fatal(err)
	}
	writer, err := manifest.NewWriter(rd)
	if err != nil {
		t.Fatal(err)
	}
	s := &scheduler{writer: writer, runID: "fatal"}
	if err := s.emit(RunStarted{RunID: "fatal", Workflow: "capture", Steps: []string{"worker"}}); err != nil {
		t.Fatal(err)
	}
	s.failCapture(workflow.CaptureIntegrity)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil, root)
	run, err := m.Resume("fatal")
	if run != nil || !errors.Is(err, workflow.CaptureIntegrity) || !strings.Contains(err.Error(), "new run") || len(m.Runs()) != 0 {
		t.Fatalf("run=%v err=%v", run, err)
	}
	held, err := RunLockState(rd)
	if err != nil || held {
		t.Fatalf("lease leak: %v", err)
	}
}
