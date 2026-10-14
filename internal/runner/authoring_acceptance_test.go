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
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/headless"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// The generator really writes in a step worktree. The workspace consumer must
// see its squash-integrated contribution in a fresh reader view.
func acceptanceFixture(t *testing.T, mode string) (string, *workflow.Workflow) {
	t.Helper()
	dir := t.TempDir()
	script := "printf 'ORIGINAL\\n'\n"
	if mode == "retry" {
		script += "test \"$JIG_INPUT_ATTEMPT\" != 0\n"
	}
	authoringProbeWrite(t, dir, "authored.sh", script)
	authoringProbeWrite(t, dir, ".gitignore", ".jig/\n")
	suffix := ""
	if mode == "retry" {
		suffix = "on_failure='retry'\nmax_retries=1\n"
	}
	source := `[workflow]
name='acceptance'
version='1'
[[step]]
id='ready'
type='command'
run='true'
[[step]]
id='generate'
type='command'
depends_on=['ready']
isolation='worktree'
run="printf 'echo GENERATED\\n' > generated.sh"
[[step]]
id='worker'
type='command'
script='authored.sh'
depends_on=['generate']
` + suffix + `[[step]]
id='workspace'
type='command'
run='sh generated.sh'
depends_on=['worker']
`
	if mode == "route" {
		source += `[[step]]
id='gate'
type='command'
run='true'
depends_on=['workspace']
output_type={enum=['again','ok']}
[[step.route]]
when="gate == 'again'"
goto='worker'
max_iterations=2
`
	}
	authoringProbeWrite(t, dir, "workflow.toml", source)
	reopenGit(t, dir)
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, wf
}

func TestFrozenAuthoringAcceptance(t *testing.T) {
	for _, mode := range []string{"persisted", "retry", "route", "memory"} {
		t.Run(mode, func(t *testing.T) {
			dir, wf := acceptanceFixture(t, mode)
			root := filepath.Join(dir, ".jig")
			if mode == "memory" {
				root = ""
			}
			e := &frozenExecution{command: NewCommandExecutor(dir), ready: make(chan struct{}), release: make(chan struct{}), route: mode == "route"}
			manager := engine.NewManager(e, root)
			var out, diagnostic discardWriter
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			done := make(chan headless.Result, 1)
			go func() {
				done <- headless.Run(ctx, headless.Options{Workflow: wf, Manager: manager, DiscardMerge: true, Stdout: out, Stderr: diagnostic, Quiet: true})
			}()
			frozenReady(t, e)
			authoringProbeWrite(t, dir, "authored.sh", "printf 'EDITED\\n'\n")
			close(e.release)
			result := <-done
			if result.ExitCode != headless.ExitOK {
				t.Fatalf("headless: %+v", result)
			}
			// Completion happens after all workers; their observations are now immutable.
			workerCount, workspaceCount := 0, 0
			for i, req := range e.requests {
				switch req.Step.ID {
				case "generate":
					if root != "" && (req.Worktree == "" || req.ExecutionDir == dir) {
						t.Fatal("generator did not execute in isolated worktree")
					}
				case "worker":
					workerCount++
					if e.outputs[i] != "ORIGINAL\n" {
						t.Fatalf("frozen output=%q", e.outputs[i])
					}
					raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
					if err != nil {
						t.Fatal(err)
					}
					expected := "printf 'ORIGINAL\\n'\n"
					if mode == "retry" {
						expected += "test \"$JIG_INPUT_ATTEMPT\" != 0\n"
					}
					if string(raw) != expected {
						t.Fatalf("captured bytes changed: %q", raw)
					}
					t.Logf("attempt=%d iteration=%d expected_sha256=%x consumed_sha256=%x frozen=%q", req.Attempt, req.Iteration, sha256.Sum256([]byte(expected)), sha256.Sum256(raw), e.outputs[i])
				case "workspace":
					workspaceCount++
					if e.outputs[i] != "GENERATED\n" {
						t.Fatalf("workspace output=%q", e.outputs[i])
					}
					if root != "" && req.ExecutionDir == dir {
						t.Fatal("consumer did not use integrated reader view")
					}
				}
				if req.Step.ID == "worker" || req.Step.ID == "workspace" {
					acceptanceTranscript(t, req, e.outputs[i])
				}
				if root == "" && (req.ArtifactDir != "" || req.TranscriptPath != "") {
					t.Fatal("memory run received persistence paths")
				}
			}
			want := 1
			if mode == "retry" || mode == "route" {
				want = 2
			}
			if workerCount != want {
				t.Fatalf("frozen starts=%d want=%d", workerCount, want)
			}
			wantWorkspace := 1
			if mode == "route" {
				wantWorkspace = 2
			}
			if workspaceCount != wantWorkspace {
				t.Fatalf("workspace starts=%d want=%d", workspaceCount, wantWorkspace)
			}
			if root != "" {
				// Discard final landing leaves the base checkout clean while retaining the
				// generator's contribution on the run branch. This also proves integration.
				if _, err := os.Stat(filepath.Join(dir, "generated.sh")); !os.IsNotExist(err) {
					t.Fatalf("unexpected base landing: %v", err)
				}
				cmd := exec.CommandContext(t.Context(), "git", "show", "jig/acceptance/run-"+result.Envelope.RunID+":generated.sh")
				cmd.Dir = dir
				data, err := cmd.CombinedOutput()
				if err != nil || string(data) != "echo GENERATED\n" {
					t.Fatalf("integrated file=%q %v", data, err)
				}
			} else if _, err := os.Stat(filepath.Join(dir, ".jig")); !os.IsNotExist(err) {
				t.Fatalf("relative persistence: %v", err)
			}
			// A fresh Load/Start observes the authored edit; no restored authority is used.
			fresh, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
			if err != nil {
				t.Fatal(err)
			}
			next := &frozenExecution{command: NewCommandExecutor(dir), ready: make(chan struct{}), release: make(chan struct{})}
			close(next.release)
			r, err := engine.NewManager(next, "").Start(fresh)
			if err != nil {
				t.Fatal(err)
			}
			if frozenWait(t, r).Failed {
				t.Fatal("new run failed")
			}
			found := false
			for i, req := range next.requests {
				if req.Step.ID == "worker" {
					found = true
					if next.outputs[i] != "EDITED\n" {
						t.Fatalf("new run=%q", next.outputs[i])
					}
				}
			}
			if !found {
				t.Fatal("new run never dispatched authored script")
			}
			t.Logf("%s: frozen starts=%d workspace starts=%d; workspace=GENERATED; new run=EDITED", mode, workerCount, workspaceCount)
		})
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestFrozenAuthoringAcceptanceController(t *testing.T) {
	if os.Getenv("JIG_TEST_ACCEPTANCE_CONTROLLER") != "1" {
		t.Skip("subprocess fixture")
	}
	dir := os.Getenv("JIG_TEST_ACCEPTANCE_DIR")
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	e := &frozenExecution{command: NewCommandExecutor(dir), ready: make(chan struct{}), release: make(chan struct{})}
	r, err := engine.NewManager(e, filepath.Join(dir, ".jig")).Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	frozenReady(t, e)
	fmt.Println("RUN_READY " + r.ID)
	var b [1]byte
	if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
		t.Fatal(err)
	}
	os.Exit(0) // Release the lease without converting interrupted work to cancellation.
}

func TestFrozenAuthoringAcceptanceReopen(t *testing.T) {
	dir, wf := acceptanceFixture(t, "reopen")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestFrozenAuthoringAcceptanceController$", "-test.v")
	cmd.Env = append(os.Environ(), "JIG_TEST_ACCEPTANCE_CONTROLLER=1", "JIG_TEST_ACCEPTANCE_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	scan := bufio.NewScanner(stdout)
	id := ""
	for scan.Scan() {
		if v, ok := strings.CutPrefix(scan.Text(), "RUN_READY "); ok {
			id = v
			break
		}
	}
	if id == "" {
		stdin.Close()
		_ = cmd.Wait()
		t.Fatalf("controller readiness: %v %s", scan.Err(), diagnostic.String())
	}
	if _, err := stdin.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("controller exit: %v %s", err, diagnostic.String())
	}
	authoringProbeWrite(t, dir, "authored.sh", "printf 'SUBSTITUTE\\n'\n")
	// Prove the root authoring source is unnecessary too.
	if err := os.Remove(filepath.Join(dir, "workflow.toml")); err != nil {
		t.Fatal(err)
	}
	e := &frozenExecution{command: NewCommandExecutor(t.TempDir()), ready: make(chan struct{}), release: make(chan struct{})}
	close(e.release)
	manager := engine.NewManager(e, filepath.Join(dir, ".jig"))
	live, ctrl := manager.Subscribe()
	r, err := manager.Resume(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Cancel(); r.Wait() })
	var out discardWriter
	result := headless.Supervise(ctx, headless.Options{Manager: manager, DiscardMerge: true, OnRecovery: headless.RecoveryRetry, Quiet: true, Stdout: out, Stderr: out}, wf, r, live, ctrl)
	if result.ExitCode != headless.ExitOK {
		t.Fatalf("reopen: %+v", result)
	}
	seen := map[string]bool{}
	for i, req := range e.requests {
		if req.Step.ID == "worker" || req.Step.ID == "workspace" {
			want := "ORIGINAL\n"
			if req.Step.ID == "workspace" {
				want = "GENERATED\n"
			}
			if e.outputs[i] != want {
				t.Fatalf("reopen %s output=%q want=%q", req.Step.ID, e.outputs[i], want)
			}
			acceptanceTranscript(t, req, want)
			if req.Step.ID == "worker" {
				raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
				expected := "printf 'ORIGINAL\\n'\n"
				if err != nil || string(raw) != expected {
					t.Fatalf("reopen capture=%q err=%v", raw, err)
				}
				t.Logf("reopen expected_sha256=%x consumed_sha256=%x", sha256.Sum256([]byte(expected)), sha256.Sum256(raw))
			}
			seen[req.Step.ID] = true
			t.Logf("reopen %s: output=%q execution view separate from unrelated executor CWD", req.Step.ID, want)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("missing resumed consumers: %v", seen)
	}
}

func acceptanceTranscript(t *testing.T, req engine.StepRequest, want string) {
	t.Helper()
	if req.TranscriptPath == "" {
		return
	}
	reader, err := transcript.Open(req.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reader.Tail(30)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		for _, block := range entry.Blocks {
			if block.Text == want {
				return
			}
		}
	}
	t.Fatalf("%s transcript missing executed output %q", req.Step.ID, want)
}
