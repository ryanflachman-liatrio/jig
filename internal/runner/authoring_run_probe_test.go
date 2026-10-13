//go:build darwin || linux

package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"golang.org/x/sys/unix"
)

// Fixture-specific preparation, not a replacement collector. Workflow's P3
// tests qualify the full path/budget policy. Here every file is directly below
// one pinned synthetic scripts directory; no discovery or links are allowed.
// The production Manager/runner still have no frozen-asset dispatch path.
func prepareAuthoringRunProbe(wf *workflow.Workflow, repo string) (map[string]string, error) {
	fd, err := unix.Open(filepath.Join(repo, "scripts"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	bindings := map[string]string{}
	for _, s := range wf.Steps {
		if s.Script == "" || strings.Contains(s.Script, "\n") {
			continue
		}
		if filepath.Dir(s.Script) != "scripts" {
			return nil, fmt.Errorf("probe accepts only direct fixture scripts")
		}
		source, err := unix.Openat(fd, filepath.Base(s.Script), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("probe capture missing or unsupported script")
		}
		f := os.NewFile(uintptr(source), "probe-script")
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("probe capture requires regular file")
		}
		data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		ce := f.Close()
		if err != nil || ce != nil || len(data) > 1<<20 {
			return nil, fmt.Errorf("probe capture read or limit failure")
		}
		bindings[s.ID] = string(data)
	}
	return bindings, nil
}

type authoringRunProbe struct {
	bindings map[string]string
	ready    chan struct{}
	release  <-chan struct{}
	workers  atomic.Int32
	commands atomic.Int32
	observed chan engine.StepRequest
	command  *CommandExecutor
}

func (e *authoringRunProbe) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	e.workers.Add(1)
	if req.Step.ID == "ready" {
		close(e.ready)
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	st := *req.Step
	if st.Script != "" {
		body, ok := e.bindings[st.ID]
		if !ok {
			return nil, fmt.Errorf("missing probe binding")
		}
		st.Script = ""
		st.Run = body // Exercise real shell/transcript consumption of frozen text.
	}
	req.Step = &st
	e.observed <- req
	e.commands.Add(1)
	return e.command.Execute(ctx, req, rep)
}
func startAuthoringRunProbe(wf *workflow.Workflow, repo string, e *authoringRunProbe) (*engine.Run, error) {
	bindings, err := prepareAuthoringRunProbe(wf, repo)
	if err != nil {
		return nil, err
	}
	e.bindings = bindings
	return engine.NewManager(e, filepath.Join(repo, ".jig")).Start(wf)
}
func authoringProbeWrite(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func authoringProbeFixture(t *testing.T) (string, *workflow.Workflow) {
	t.Helper()
	repo := t.TempDir()
	authoringProbeWrite(t, repo, ".gitignore", ".jig/\n")
	authoringProbeWrite(t, repo, "scripts/frozen.sh", "printf 'ORIGINAL\\n'\n")
	authoringProbeWrite(t, repo, ".agents/root.toml", "[workflow]\nname='run-probe'\nversion='1'\n[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='phase'\ntype='subworkflow'\ndepends_on=['ready']\nmodule='modules/outer.toml'\n")
	authoringProbeWrite(t, repo, ".agents/modules/outer.toml", "[module]\nschema_version=1\n[module.exports.result]\nref='@inner.result'\n[[step]]\nid='inner'\ntype='subworkflow'\nmodule='deep/inner.toml'\n")
	authoringProbeWrite(t, repo, ".agents/modules/deep/inner.toml", "[module]\nschema_version=1\n[module.exports.result]\nref='@worker'\n[[step]]\nid='worker'\ntype='command'\nscript='scripts/frozen.sh'\n")
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Probe"}, {"config", "user.email", "probe@example.invalid"}, {"add", "."}, {"-c", "commit.gpgsign=false", "commit", "-m", "synthetic fixture"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v: %v\n%s", args, err, out)
		}
	}
	wf, err := workflow.Load(filepath.Join(repo, ".agents/root.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.ModuleSources()) != 2 {
		t.Fatalf("module inventory=%d", len(wf.ModuleSources()))
	}
	return repo, wf
}
func TestAuthoringAssetProbeRun(t *testing.T) {
	t.Run("nested_capture_reaches_real_command", func(t *testing.T) {
		repo, wf := authoringProbeFixture(t)
		release := make(chan struct{})
		executor := &authoringRunProbe{ready: make(chan struct{}), release: release, observed: make(chan engine.StepRequest, 4), command: NewCommandExecutor(t.TempDir())}
		run, err := startAuthoringRunProbe(wf, repo, executor)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan engine.RunSnapshot, 1)
		go func() { done <- run.Wait() }()
		t.Cleanup(func() { run.Cancel(); run.Wait() })
		select {
		case <-executor.ready:
		case snap := <-done:
			t.Fatalf("run ended before barrier: %+v", snap)
		case <-time.After(10 * time.Second):
			t.Fatal("ready barrier timed out")
		}
		// The current checkout no longer contains any source this worker could read.
		if err := os.Remove(filepath.Join(repo, "scripts/frozen.sh")); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(repo, ".agents/modules")); err != nil {
			t.Fatal(err)
		}
		close(release)
		var snap engine.RunSnapshot
		select {
		case snap = <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("run completion timed out")
		}
		if !snap.Done || snap.Failed || executor.workers.Load() != 2 || executor.commands.Load() != 2 {
			t.Fatalf("snapshot=%+v workers=%d commands=%d", snap, executor.workers.Load(), executor.commands.Load())
		}
		var worker engine.StepRequest
		for i := 0; i < 2; i++ {
			req := <-executor.observed
			if req.Step.ID == "phase__inner__worker" {
				worker = req
			}
		}
		if worker.Step == nil || worker.ExecutionDir == "" || worker.TranscriptPath == "" {
			t.Fatal("missing expanded worker identity/execution view/transcript")
		}
		expected := "dbb1c6ac406e57ba5831d0aad46b6cf994c65f51d6df53fec8b6d1740a4b9c8d"
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(worker.Step.Run)))
		if got != expected {
			t.Fatalf("consumed digest=%s want=%s", got, expected)
		}
		reader, err := transcript.Open(worker.TranscriptPath)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := reader.Tail(10)
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for _, entry := range entries {
			for _, block := range entry.Blocks {
				output.WriteString(block.Text)
			}
		}
		if output.String() != "ORIGINAL\n" {
			t.Fatalf("authoritative transcript=%q", output.String())
		}
		t.Logf("expanded step=%s modules=2 workers=2 command calls=2 consumed_sha256=%s transcript=ORIGINAL", worker.Step.ID, got)
	})
	t.Run("late_asset_failure_prevents_ready_worker", func(t *testing.T) {
		repo, wf := authoringProbeFixture(t)
		if err := os.Remove(filepath.Join(repo, "scripts/frozen.sh")); err != nil {
			t.Fatal(err)
		}
		executor := &authoringRunProbe{}
		run, err := startAuthoringRunProbe(wf, repo, executor)
		if err == nil || run != nil || executor.workers.Load() != 0 || executor.commands.Load() != 0 {
			t.Fatalf("run=%v err=%v workers=%d commands=%d", run != nil, err, executor.workers.Load(), executor.commands.Load())
		}
		if _, err := os.Stat(filepath.Join(repo, ".jig")); !os.IsNotExist(err) {
			t.Fatalf("failed preflight constructed run storage: %v", err)
		}
		t.Log("late script removed after Load: capture refused; ready worker=0, command calls=0, no run storage")
	})
}
