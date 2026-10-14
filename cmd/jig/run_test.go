package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/headless"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestParseOutputMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want headless.OutputMode
		ok   bool
	}{
		{"text", headless.OutputText, true},
		{"json", headless.OutputJSON, true},
		{"jsonl", headless.OutputJSONL, true},
		{"yaml", "", false},
		{"", "", false},
	} {
		got, err := parseOutputMode(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("parseOutputMode(%q)=%q,%v want %q,nil", tc.in, got, err, tc.want)
			}
		} else if err == nil {
			t.Errorf("parseOutputMode(%q) expected error", tc.in)
		}
	}
}

func TestRunRun_UsageExit2(t *testing.T) {
	if code := runRun(nil); code != headless.ExitUsage {
		t.Fatalf("no args: exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--bogus"}); code != headless.ExitUsage {
		t.Fatalf("bad flag: exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--approve-merge", "--discard-merge", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("mutual exclusive: exit=%d want %d", code, headless.ExitUsage)
	}
}

func TestRunRun_LoadErrorExit1(t *testing.T) {
	code := runRun([]string{"/nonexistent/workflow.toml"})
	if code != headless.ExitFailed {
		t.Fatalf("missing file: exit=%d want %d", code, headless.ExitFailed)
	}
}

func TestParseRecoveryAndConflict(t *testing.T) {
	for _, ok := range []string{"abort", "retry", "skip"} {
		if err := parseRecoveryAction(ok); err != nil {
			t.Errorf("parseRecoveryAction(%q): %v", ok, err)
		}
	}
	if err := parseRecoveryAction("resume"); err == nil {
		t.Fatal("resume should be rejected")
	}
	if err := parseConflictAction("abort"); err != nil {
		t.Fatal(err)
	}
	if err := parseConflictAction("agent"); err == nil {
		t.Fatal("agent should be rejected")
	}
}

func TestRunRun_BadOnRecoveryExit2(t *testing.T) {
	if code := runRun([]string{"--on-recovery", "resume", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--on-conflict", "agent", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("exit=%d want %d", code, headless.ExitUsage)
	}
}

func TestReorderRunArgs_PolicyFlags(t *testing.T) {
	got, err := reorderRunArgs([]string{"wf.toml", "--on-recovery", "skip", "--on-conflict", "abort"})
	if err != nil {
		t.Fatal(err)
	}
	want := "--on-recovery skip --on-conflict abort wf.toml"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

// Capture CLI output using files, so diagnostics cannot deadlock a pipe. These
// tests are sequential because the CLI owns process stdout/stderr.
func authoringCLIOutput(t *testing.T, call func() int) (int, string) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, out
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	code := call()
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(data)
}

func TestRunRun_AuthoringAssetFailure(t *testing.T) {
	for _, mode := range []string{"load", "start", "storage"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "PRIVATE_PATH_MARKER.sh")
			if err := os.WriteFile(script, bytes.Repeat([]byte("PRIVATE_CONTENT_MARKER\n"), 50000), 0600); err != nil {
				t.Fatal(err)
			}
			source := "[workflow]\nname='capture-cli'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='PRIVATE_PATH_MARKER.sh'\n"
			if mode == "load" {
				source = "[workflow]\nname='capture-cli'\nversion='1'\n[[step]]\nid='worker'\ntype='agent'\nskill='.ssh/PRIVATE_PATH_MARKER'\n"
			}
			if mode == "storage" {
				source = "[workflow]\nname='capture-cli'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='touch SHOULD_NOT_DISPATCH'\n"
			}
			path := filepath.Join(dir, "workflow.toml")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dir, ".jig")
			if mode == "storage" {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "runs"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			code, output := authoringCLIOutput(t, func() int { return runRun([]string{path, "--root", root, "--output", "json", "--discard-merge"}) })
			if code != headless.ExitFailed {
				t.Fatalf("exit=%d output=%s", code, output)
			}
			reason := "limit"
			if mode == "load" {
				reason = "forbidden_path"
			}
			if mode == "storage" {
				reason = "storage"
			}
			if !strings.Contains(output, "authoring capture: "+reason) {
				t.Fatalf("missing safe reason: %s", output)
			}
			for _, private := range []string{dir, "PRIVATE_PATH_MARKER", "PRIVATE_CONTENT_MARKER"} {
				if strings.Contains(output, private) {
					t.Fatalf("diagnostic disclosed private marker: %s", output)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "SHOULD_NOT_DISPATCH")); !os.IsNotExist(err) {
				t.Fatal("dispatched after failed capture")
			}
			if mode == "start" && (!strings.Contains(output, `step "worker" script`) || strings.Contains(output, "run_id:")) {
				t.Fatalf("missing asset context or dispatched run: %s", output)
			}
			t.Logf("%s: exit=1 safe reason=%s; private content/path absent", mode, reason)
		})
	}
	t.Run("live_acceptance", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte("PRIVATE_CONTENT_MARKER\n"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "workflow.toml")
		source := "[workflow]\nname='capture-live'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\non_failure='retry'\nmax_retries=3\n"
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var starts atomic.Int32
		ex := authoringCLIExecutor(func(_ context.Context, r engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
			starts.Add(1)
			// A synthetic completed worker damages required storage at the result boundary.
			rd := filepath.Dir(filepath.Dir(filepath.Dir(r.TranscriptPath)))
			if err := os.WriteFile(filepath.Join(rd, "authoring/manifest.json"), []byte("DAMAGED"), 0600); err != nil {
				return nil, err
			}
			return &step.Result{Status: step.StatusSucceeded}, nil
		})
		manager := engine.NewManager(ex, filepath.Join(dir, ".jig"))
		var out, diagnostic bytes.Buffer
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		result := headless.Run(ctx, headless.Options{WorkflowPath: path, Manager: manager, Output: headless.OutputJSONL, Stdout: &out, Stderr: &diagnostic, DiscardMerge: true})
		if result.ExitCode != headless.ExitFailed || result.Envelope.OK || result.Envelope.Error == nil || result.Envelope.Error.Code != "run_failed" || starts.Load() != 1 {
			t.Fatalf("result=%+v starts=%d", result, starts.Load())
		}
		combined := out.String() + diagnostic.String()
		if !strings.Contains(combined, "authoring capture: integrity") {
			t.Fatal("missing capture run_error")
		}
		if strings.Contains(combined, "PRIVATE_CONTENT_MARKER") {
			t.Fatal("content leaked")
		}
		t.Log("live acceptance: existing run_failed envelope and exit=1; one dispatch; no retry")
	})
	t.Run("strict_reopen", func(t *testing.T) {
		dir := t.TempDir()
		wf, err := workflow.Decode("[workflow]\nname='capture-reopen'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n", "")
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		ex := authoringCLIExecutor(func(ctx context.Context, _ engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
			close(ready)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		manager := engine.NewManager(ex, filepath.Join(dir, ".jig"))
		r, err := manager.Start(wf)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Cancel(); r.Wait() })
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("worker barrier")
		}
		journalPath := filepath.Join(manager.RunDir(r.ID), "journal.jsonl")
		prefix, err := os.ReadFile(journalPath)
		if err != nil {
			t.Fatal(err)
		}
		r.Cancel()
		r.Wait()
		// Retain the interrupted controller prefix; cancellation above only drains
		// our fixture's resources, not the synthetic crash journal under test.
		if err := os.WriteFile(journalPath, prefix, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(manager.RunDir(r.ID), "authoring/manifest.json"), []byte("PRIVATE_CONTENT_MARKER"), 0600); err != nil {
			t.Fatal(err)
		}
		code, output := authoringCLIOutput(t, func() int {
			return runResume([]string{r.ID, "--root", filepath.Join(dir, ".jig"), "--on-recovery", "retry", "--discard-merge"})
		})
		if code != headless.ExitFailed || !strings.Contains(output, "authoring capture:") {
			t.Fatalf("reopen exit=%d output=%s", code, output)
		}
		if strings.Contains(output, "PRIVATE_CONTENT_MARKER") || strings.Contains(output, dir) {
			t.Fatalf("reopen disclosed capture: %s", output)
		}
		after, err := os.ReadFile(journalPath)
		if err != nil || !bytes.Equal(prefix, after) {
			t.Fatal("failed reopen changed journal")
		}
		held, err := engine.RunLockState(manager.RunDir(r.ID))
		if err != nil || held {
			t.Fatal("failed reopen leaked lease")
		}
		t.Log("strict CLI reopen: exit=1; safe reason; unchanged journal; no substituted worker or lease leak")
	})
}

type authoringCLIExecutor func(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error)

func (f authoringCLIExecutor) Execute(ctx context.Context, r engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	return f(ctx, r, rep)
}
