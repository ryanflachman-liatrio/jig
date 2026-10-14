package runner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type parkedCaptureHarness struct{ caps harness.CapabilitySet }

func (h *parkedCaptureHarness) Name() string                        { return "synthetic" }
func (h *parkedCaptureHarness) Capabilities() harness.CapabilitySet { return h.caps }
func (h *parkedCaptureHarness) Open(context.Context, harness.SessionSpec) (harness.Session, error) {
	ch := make(chan harness.Event, 3)
	ch <- harness.Event{Type: harness.EventSessionID, SessionID: "synthetic-session"}
	ch <- harness.Event{Type: harness.EventText, Text: "PARKED"}
	ch <- harness.Event{Type: harness.EventAssistantEnd}
	return &parkedCaptureSession{ch: ch}, nil
}

type parkedCaptureSession struct{ ch chan harness.Event }

func (s *parkedCaptureSession) Messages() <-chan harness.Event               { return s.ch }
func (*parkedCaptureSession) Send(context.Context, harness.ToolResult) error { return nil }
func (*parkedCaptureSession) Close() error                                   { return nil }

type parkCaptureReporter struct {
	engine.Reporter
	notify func()
}

func (r *parkCaptureReporter) Message(seq, iteration int) {
	r.Reporter.Message(seq, iteration)
	if seq >= 1 {
		r.notify()
	}
}

type parkedCaptureExecutor struct {
	mode    string
	ready   chan struct{}
	once    sync.Once
	parked  atomic.Int32
	normal  *moduleExecutor
	command *CommandExecutor
}

func (e *parkedCaptureExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if e.mode == "reset" && req.Step.ID == "ready" {
		e.once.Do(func() { close(e.ready) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if req.Step.Type == workflow.StepCommand {
		return e.command.Execute(ctx, req, rep)
	}
	if e.mode == "fanout" && req.FanOutItem == nil {
		return e.normal.Execute(ctx, req, rep)
	}
	h := &parkedCaptureHarness{harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput, harness.CapSessionResume)}
	return NewAgentExecutorFixed(h).Execute(ctx, req, &parkCaptureReporter{Reporter: rep, notify: func() {
		if e.parked.Add(1) >= 2 {
			e.once.Do(func() { close(e.ready) })
		}
	}})
}

func TestFrozenAuthoringAssetsReopenHelper(t *testing.T) {
	if os.Getenv("JIG_TEST_AUTHORING_REOPEN") != "1" {
		t.Skip("subprocess fixture")
	}
	dir := os.Getenv("JIG_TEST_AUTHORING_PROJECT")
	mode := os.Getenv("JIG_TEST_AUTHORING_MODE")
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	normal := &moduleExecutor{cwd: dir, mode: mode, observed: make(chan moduleObservation, 16)}
	e := &parkedCaptureExecutor{mode: mode, ready: make(chan struct{}), normal: normal, command: NewCommandExecutor(dir)}
	run, err := engine.NewManager(e, filepath.Join(dir, ".jig")).Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.ready:
	case <-time.After(15 * time.Second):
		for _, st := range run.Snapshot().Steps {
			if st.Result != nil {
				t.Logf("%s: %+v", st.ID, *st.Result)
			} else {
				t.Logf("%s: %s", st.ID, st.Status)
			}
		}
		t.Fatal("park barrier")
	}
	fmt.Println("RUN_READY " + run.ID)
	var b [1]byte
	if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
		t.Fatal(err)
	}
	// Normal controller process exit releases the actual lease while retaining an
	// unfinished journal. Run.Cancel is intentionally a terminal operation.
	os.Exit(0)
}

type reopenedCaptureExecutor struct {
	normal  *moduleExecutor
	command *CommandExecutor
	shells  chan engine.StepRequest
	outputs chan string
}

func (e *reopenedCaptureExecutor) SupportsSessionResume(string) bool { return true }
func (e *reopenedCaptureExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if req.Step.Script != "" {
		r := &frozenReporter{Reporter: rep}
		result, err := e.command.Execute(ctx, req, r)
		e.shells <- req
		e.outputs <- r.text.String()
		return result, err
	}
	return e.normal.Execute(ctx, req, rep)
}
func reopenGit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"add", "."}, {"commit", "-qm", "synthetic fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
}

func TestFrozenAuthoringAssetsReopen(t *testing.T) {
	for _, mode := range []string{"resume", "route", "reset", "fanout"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "resume" || mode == "reset" {
				fixtureMode = ""
			}
			dir, _ := runnerModuleFixture(t, "codex", fixtureMode)
			bpath := filepath.Join(dir, "modules/deep/b.toml")
			source, err := os.ReadFile(bpath)
			if err != nil {
				t.Fatal(err)
			}
			dep := "worker"
			if mode == "route" {
				dep = "gate"
			}
			source = append(source, []byte("\n[[step]]\nid='shell'\ntype='command'\nscript='scripts/frozen.sh'\ndepends_on=['"+dep+"']\n")...)
			if err := os.WriteFile(bpath, source, 0600); err != nil {
				t.Fatal(err)
			}
			authoringProbeWrite(t, dir, "scripts/frozen.sh", "printf 'ORIGINAL\\n'\n")
			authoringProbeWrite(t, dir, ".gitignore", ".jig/\n")
			reopenGit(t, dir)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestFrozenAuthoringAssetsReopenHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "JIG_TEST_AUTHORING_REOPEN=1", "JIG_TEST_AUTHORING_PROJECT="+dir, "JIG_TEST_AUTHORING_MODE="+mode)
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
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			var stdoutLog strings.Builder
			ready := make(chan string, 1)
			go func() {
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					stdoutLog.WriteString(scan.Text() + "\n")
					if id, ok := strings.CutPrefix(scan.Text(), "RUN_READY "); ok {
						ready <- id
						return
					}
				}
				ready <- ""
			}()
			var id string
			select {
			case id = <-ready:
				if id == "" {
					stdin.Close()
					_ = cmd.Wait()
					t.Fatalf("helper: %s %s", stderr.String(), stdoutLog.String())
				}
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("helper timeout")
			}
			rd := filepath.Join(dir, ".jig/runs", id)
			if mode == "resume" {
				info, err := datastore.ReadSession(rd, "first__nested__worker")
				if err != nil || info.SessionID != "synthetic-session" {
					t.Fatalf("session checkpoint: %+v %v", info, err)
				}
			}
			if lock, err := engine.AcquireRunLease(rd); err == nil {
				lock.Close()
				t.Fatal("helper lost ownership")
			}
			if _, err := stdin.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatalf("normal exit: %v %s", err, stderr.String())
			}
			lease, err := engine.AcquireRunLease(rd)
			if err != nil {
				t.Fatal(err)
			}
			lease.Close()
			before, err := os.ReadFile(filepath.Join(rd, "authoring/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			journal, err := os.ReadFile(filepath.Join(rd, "journal.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "fanout" && !bytes.Contains(journal, []byte("fan_out_expanded")) {
				t.Fatal("family was not durably expanded")
			}
			if err := os.RemoveAll(filepath.Join(dir, "modules")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "workflow.toml")); err != nil {
				t.Fatal(err)
			}
			authoringProbeWrite(t, dir, "scripts/frozen.sh", "printf 'SUBSTITUTE\\n'\n")
			cwd := t.TempDir()
			normal := &moduleExecutor{cwd: cwd, mode: fixtureMode, ready: make(chan struct{}), release: make(chan struct{}), observed: make(chan moduleObservation, 16)}
			close(normal.release)
			e := &reopenedCaptureExecutor{normal: normal, command: NewCommandExecutor(cwd), shells: make(chan engine.StepRequest, 4), outputs: make(chan string, 4)}
			m := engine.NewManager(e, filepath.Join(dir, ".jig"))
			run, err := m.Resume(id)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			parks := run.Snapshot()
			if mode == "reset" {
				if _, err := run.Reset("ready"); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, st := range parks.Steps {
					if st.Status == step.StatusAwaitingRecovery {
						action := engine.RecoverRetry
						message := ""
						if mode == "resume" && st.ID == "first__nested__worker" {
							action = engine.RecoverResume
							message = "Operator answer"
						}
						run.Recover(st.ID, action, message)
					}
				}
			}
			snap := frozenWait(t, run)
			if snap.Failed || !snap.Done {
				t.Fatalf("reopen failed: %+v", snap)
			}
			if len(e.shells) != 2 {
				t.Fatalf("shells=%d", len(e.shells))
			}
			for i := 0; i < 2; i++ {
				req := <-e.shells
				output := <-e.outputs
				if output != "ORIGINAL\n" {
					t.Fatalf("substitution: %q", output)
				}
				raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
				if err != nil || string(raw) != "printf 'ORIGINAL\\n'\n" {
					t.Fatalf("bytes=%q %v", raw, err)
				}
				reader, err := transcript.Open(req.TranscriptPath)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := reader.Tail(20)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range entries {
					for _, block := range entry.Blocks {
						if block.Text == "ORIGINAL\n" {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("final transcript missing ORIGINAL")
				}
				t.Logf("mode=%s owner=%s generation=%d script_sha256=%x output=%q", mode, req.AssetOwner, req.Generation, sha256.Sum256(raw), output)
			}
			resumed := false
			children := map[string]bool{}
			for len(normal.observed) > 0 {
				o := <-normal.observed
				if o.req.ResumeSessionID != "" {
					resumed = true
					if o.spec.Resume != "synthetic-session" || o.spec.Prompt != o.req.Message || !strings.Contains(o.spec.Prompt, "Operator answer") {
						t.Fatal("conversation changed")
					}
				} else if !strings.Contains(o.spec.Prompt, "ORIGINAL prompt") {
					t.Fatal("prompt recaptured")
				}
				if strings.HasSuffix(o.req.AssetOwner, "__worker") {
					if o.spec.Schema["properties"].(map[string]any)["original"] == nil {
						t.Fatal("schema recaptured")
					}
					if mode != "fanout" && !strings.Contains(o.spec.Prompt, "ORIGINAL template") && o.req.ResumeSessionID == "" {
						t.Fatal("template recaptured")
					}
				}
				for _, b := range o.req.Assets.Manifest().Bindings {
					if b.Owner == o.req.AssetOwner && b.Relative == "helper.sh" {
						raw, _ := o.req.Assets.Blob(b.SHA256)
						if string(raw) != "ORIGINAL helper\n" {
							t.Fatal("bundle recaptured")
						}
					}
				}
				if o.req.FanOutItem != nil {
					children[o.req.Step.ID] = true
					if o.req.AssetOwner == o.req.Step.ID {
						t.Fatal("template identity lost")
					}
				}
			}
			if mode == "resume" && !resumed {
				t.Fatal("resume not exercised")
			}
			if mode == "fanout" && len(children) != 4 {
				t.Fatalf("restored children=%v", children)
			}
			if mode == "reset" {
				for _, st := range snap.Steps {
					if st.Generation != 1 {
						t.Fatalf("generation=%d", st.Generation)
					}
				}
			}
			after, err := os.ReadFile(filepath.Join(rd, "authoring/manifest.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("reset/reopen recaptured assets")
			}
			t.Logf("mode=%s: normal process exit released lease; strict reopen retained manifest sha256=%x; real shells=2", mode, sha256.Sum256(before))
		})
	}
}

func (e *parkedCaptureExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	return e.normal.Validate(ctx, req, rep)
}
func (e *reopenedCaptureExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	return e.normal.Validate(ctx, req, rep)
}
