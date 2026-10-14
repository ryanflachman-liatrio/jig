//go:build darwin || linux

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestAuthoringPublicationCrash(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "publication-helper")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "test", "-c", "-o", binary, "./internal/datastore")
	build.Dir = filepath.Join(cwd, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s %v", out, err)
	}
	for _, checkpoint := range []string{"blob_write", "manifest_write", "before_commit", "after_commit", "after_commit_empty_journal"} {
		t.Run(checkpoint, func(t *testing.T) {
			dir, wf := captureFixture(t, "[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\n")
			root := filepath.Join(dir, ".jig")
			rd, err := datastore.RunDir(root, "crash")
			if err != nil {
				t.Fatal(err)
			}
			prepared, assets, err := workflow.PrepareAuthoring(wf, dir)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newRunAuthoring(assets)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := encodeWorkflowSnapshot(prepared, authority.reference())
			if err != nil {
				t.Fatal(err)
			}
			inputs := t.TempDir()
			writeCaptureFixture(t, inputs, "workflow.json", snapshot)
			writeCaptureFixture(t, inputs, "manifest.json", authority.manifest)
			blobs := map[string][]byte{}
			for _, b := range assets.Manifest().Blobs {
				blobs[b.SHA256], _ = assets.Blob(b.SHA256)
			}
			raw, err := json.Marshal(blobs)
			if err != nil {
				t.Fatal(err)
			}
			writeCaptureFixture(t, inputs, "blobs.json", raw)
			writeJournal(t, rd, []Event{RunStarted{RunID: "crash", Workflow: "capture", Steps: []string{"worker"}}, StepStatus{RunID: "crash", StepID: "worker", From: step.StatusPending, To: step.StatusRunning}})
			if checkpoint == "after_commit_empty_journal" {
				writeJournal(t, rd, nil)
			}
			outside := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(outside, []byte("UNTOUCHED"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestAuthoringPublicationCrashHelper$", "-test.v")
			helperCheckpoint := checkpoint
			if helperCheckpoint == "after_commit_empty_journal" {
				helperCheckpoint = "after_commit"
			}
			cmd.Env = append(os.Environ(), "JIG_TEST_AUTHORING_CRASH_HELPER=1", "JIG_TEST_AUTHORING_DIR="+rd, "JIG_TEST_AUTHORING_INPUTS="+inputs, "JIG_TEST_AUTHORING_CHECKPOINT="+helperCheckpoint)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan bool, 1)
			go func() {
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if scan.Text() == "CAPTURE_READY" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					_ = cmd.Wait()
					t.Fatalf("no checkpoint: %s", stderr.String())
				}
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("checkpoint timeout")
			}
			if lock, err := AcquireRunLease(rd); err == nil {
				lock.Close()
				t.Fatal("helper lease was not held")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("helper was not interrupted")
			}
			lock, err := AcquireRunLease(rd)
			if err != nil {
				t.Fatalf("lease survived exit: %v", err)
			}
			lock.Close()
			if err := os.Remove(filepath.Join(dir, "script.sh")); err != nil {
				t.Fatal(err)
			}
			calls := make(chan StepRequest, 1)
			m := NewManager(captureExecFunc(func(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
				calls <- req
				return &step.Result{Status: step.StatusSucceeded}, nil
			}), root)
			workerCount := 0
			run, err := m.Resume("crash")
			if checkpoint != "after_commit" {
				if checkpoint == "after_commit_empty_journal" {
					if _, _, err := loadAuthoringSnapshot(rd); err != nil {
						t.Fatalf("complete capture unavailable: %v", err)
					}
				}
				if run != nil || err == nil || len(m.Runs()) != 0 || len(calls) != 0 {
					t.Fatalf("partial authorized: %v %v", run, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { run.Cancel(); run.Wait() })
				run.Recover("worker", RecoverRetry, "")
				snap := waitCaptureRun(t, run)
				if snap.Failed || len(calls) != 1 {
					t.Fatalf("complete recovery: %+v", snap)
				}
				workerCount = 1
				req := <-calls
				raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
				if err != nil || string(raw) != "echo ORIGINAL\n" {
					t.Fatalf("capture: %q %v", raw, err)
				}
			}
			raw, err = os.ReadFile(outside)
			if err != nil || string(raw) != "UNTOUCHED" {
				t.Fatal("outside sentinel changed")
			}
			t.Logf("checkpoint=%s: actual process killed and waited; lease released; workers=%d; outside sentinel unchanged", checkpoint, workerCount)
		})
	}
}
