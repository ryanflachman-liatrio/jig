package runner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type frozenExecution struct {
	command  *CommandExecutor
	ready    chan struct{}
	release  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	requests []engine.StepRequest
	outputs  []string
	route    bool
}
type frozenReporter struct {
	engine.Reporter
	text strings.Builder
}

func (r *frozenReporter) Output(s string) { r.text.WriteString(s); r.Reporter.Output(s) }
func (e *frozenExecution) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if req.Step.ID == "ready" {
		e.once.Do(func() { close(e.ready) })
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	req.Inputs = append(req.Inputs, engine.ResolvedInput{Ref: workflow.Input{As: "attempt", Artifact: "attempt"}, Value: fmt.Sprint(req.Attempt)})
	reporter := &frozenReporter{Reporter: rep}
	result, err := e.command.Execute(ctx, req, reporter)
	if e.route && req.Step.ID == "gate" && result != nil {
		result.Verdict = "ok"
		if req.Iteration == 0 {
			result.Verdict = "again"
		}
	}
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.outputs = append(e.outputs, reporter.text.String())
	e.mu.Unlock()
	return result, err
}
func frozenWait(t *testing.T, run *engine.Run) engine.RunSnapshot {
	t.Helper()
	done := make(chan engine.RunSnapshot, 1)
	go func() { done <- run.Wait() }()
	select {
	case snap := <-done:
		return snap
	case <-time.After(15 * time.Second):
		run.Cancel()
		t.Fatal("run did not settle")
		return engine.RunSnapshot{}
	}
}
func frozenReady(t *testing.T, e *frozenExecution) {
	t.Helper()
	select {
	case <-e.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("predecessor did not reach barrier")
	}
}
func TestFrozenAuthoringScripts(t *testing.T) {
	for _, mode := range []string{"persisted_delete", "memory_delete_retry", "persisted_edit_route", "absolute", "inline", "run_workspace", "empty"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cwd := t.TempDir()
			script := "printf 'ORIGINAL\\n'\n"
			if mode == "memory_delete_retry" {
				script += "test \"$JIG_INPUT_ATTEMPT\" != 0\n"
			}
			if mode == "empty" {
				script = ""
			}
			authoringProbeWrite(t, root, "source.sh", script)
			declaration := "script='source.sh'"
			if mode == "absolute" {
				declaration = "script='" + filepath.Join(root, "source.sh") + "'"
			}
			if mode == "inline" {
				declaration = "script='''printf 'INLINE\\n'\n'''"
			}
			if mode == "run_workspace" {
				declaration = "run='sh generated.sh'"
				authoringProbeWrite(t, cwd, "generated.sh", "echo OLD\n")
			}
			suffix := ""
			if mode == "memory_delete_retry" {
				suffix = "\non_failure='retry'\nmax_retries=1"
			}
			if mode == "persisted_edit_route" {
				suffix = "\n[[step]]\nid='gate'\ntype='command'\nrun='true'\ndepends_on=['worker']\noutput_type={enum=['again','ok']}\n[[step.route]]\nwhen=\"gate == 'again'\"\ngoto='worker'\nmax_iterations=2\n"
			}
			source := "[workflow]\nname='frozen'\nversion='1'\n[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='worker'\ntype='command'\ndepends_on=['ready']\n" + declaration + suffix + "\n"
			authoringProbeWrite(t, root, "workflow.toml", source)
			wf, err := workflow.Load(filepath.Join(root, "workflow.toml"))
			if err != nil {
				t.Fatal(err)
			}
			storage := ""
			if strings.HasPrefix(mode, "persisted") {
				storage = filepath.Join(root, ".jig")
			}
			e := &frozenExecution{command: NewCommandExecutor(cwd), ready: make(chan struct{}), release: make(chan struct{}), route: mode == "persisted_edit_route"}
			manager := engine.NewManager(e, storage)
			run, err := manager.Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			frozenReady(t, e)
			if strings.Contains(mode, "delete") {
				if err := os.Remove(filepath.Join(root, "source.sh")); err != nil {
					t.Fatal(err)
				}
			} else {
				authoringProbeWrite(t, root, "source.sh", "printf 'EDITED\\n'\n")
			}
			authoringProbeWrite(t, cwd, "generated.sh", "echo WORKSPACE\n")
			// Mutating caller-owned graph and a returned content copy cannot affect dispatch.
			wf.Steps[1].Script = "other.sh"
			close(e.release)
			snap := frozenWait(t, run)
			if snap.Failed || !snap.Done {
				t.Fatalf("run failed: %+v", snap)
			}
			want := "ORIGINAL\n"
			switch mode {
			case "inline":
				want = "INLINE\n"
			case "run_workspace":
				want = "WORKSPACE\n"
			case "empty":
				want = ""
			}
			count := 0
			for i, req := range e.requests {
				if req.Step.ID != "worker" {
					continue
				}
				count++
				if e.outputs[i] != want {
					t.Fatalf("attempt=%d iteration=%d output=%q want=%q", req.Attempt, req.Iteration, e.outputs[i], want)
				}
				if req.Step.Script != "" && !strings.Contains(req.Step.Script, "\n") {
					b, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
					if err != nil {
						t.Fatal(err)
					}
					if string(b) != script {
						t.Fatal("wrong captured bytes")
					}
					if len(b) > 0 {
						b[0] = 'X'
						again, _ := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
						if string(again) != script {
							t.Fatal("mutable alias")
						}
					}
					t.Logf("attempt=%d iteration=%d sha256=%x output=%q", req.Attempt, req.Iteration, sha256.Sum256([]byte(script)), want)
				}
				if storage == "" {
					if req.TranscriptPath != "" || req.ArtifactDir != "" {
						t.Fatal("memory run received persistence paths")
					}
				} else if want != "" {
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
							if block.Text == want {
								found = true
							}
						}
					}
					if !found {
						t.Fatal("authoritative transcript missing original output")
					}
				}
			}
			wantCount := 1
			if mode == "memory_delete_retry" || mode == "persisted_edit_route" {
				wantCount = 2
			}
			if count != wantCount {
				t.Fatalf("process starts=%d want=%d", count, wantCount)
			}
			if storage == "" {
				for _, dir := range []string{root, cwd} {
					if _, err := os.Stat(filepath.Join(dir, ".jig")); !os.IsNotExist(err) {
						t.Fatalf("unexpected relative persistence: %v", err)
					}
				}
			}
			if mode == "persisted_delete" {
				authoringProbeWrite(t, root, "source.sh", "printf 'EDITED\\n'\n")
				fresh, err := workflow.Load(filepath.Join(root, "workflow.toml"))
				if err != nil {
					t.Fatal(err)
				}
				next := &frozenExecution{command: NewCommandExecutor(cwd), ready: make(chan struct{}), release: make(chan struct{})}
				close(next.release)
				run, err := engine.NewManager(next, "").Start(fresh)
				if err != nil {
					t.Fatal(err)
				}
				if frozenWait(t, run).Failed {
					t.Fatal("fresh run failed")
				}
				if next.outputs[1] != "EDITED\n" {
					t.Fatalf("new run output=%q", next.outputs)
				}
			}
		})
	}
}
func TestFrozenAuthoringScriptsMissingBinding(t *testing.T) {
	root := t.TempDir()
	authoringProbeWrite(t, root, "source.sh", "echo SUBSTITUTE\n")
	result, err := NewCommandExecutor(root).Execute(t.Context(), engine.StepRequest{Step: &workflow.Step{ID: "worker", Script: "source.sh"}, RepoRoot: root}, &noopReporter{})
	if !errors.Is(err, workflow.CaptureMissing) || result.Status != step.StatusFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestFrozenAuthoringScriptsNestedModules(t *testing.T) {
	repo, wf := authoringProbeFixture(t)
	e := &frozenExecution{command: NewCommandExecutor(t.TempDir()), ready: make(chan struct{}), release: make(chan struct{})}
	run, err := engine.NewManager(e, filepath.Join(repo, ".jig")).Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Cancel(); run.Wait() })
	frozenReady(t, e)
	if err := os.Remove(filepath.Join(repo, "scripts/frozen.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".agents/modules")); err != nil {
		t.Fatal(err)
	}
	close(e.release)
	if frozenWait(t, run).Failed {
		t.Fatal("nested run failed")
	}
	if len(e.outputs) != 2 || e.outputs[1] != "ORIGINAL\n" {
		t.Fatalf("outputs=%q", e.outputs)
	}
}

func TestFrozenAuthoringScriptsCheckDelegation(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	script := `printf '{"schema_version":1,"outcome":"pass","findings":[]}' > findings.json` + "\n"
	authoringProbeWrite(t, dir, "check.sh", script)
	wf := &workflow.Workflow{Steps: []workflow.Step{{ID: "check", Type: workflow.StepCheck, Script: "check.sh", Findings: &workflow.CheckFindings{SchemaVersion: 1, File: "findings.json", RequiredTools: []string{"sh"}}}}}
	owned, assets, err := workflow.PrepareAuthoring(wf, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "check.sh")); err != nil {
		t.Fatal(err)
	}
	req := engine.StepRequest{Step: &owned.Steps[0], Assets: assets, AssetOwner: "check", ExecutionDir: cwd}
	result, err := NewCheckExecutor(cwd).Execute(t.Context(), req, &noopReporter{})
	if err != nil || result.Verdict != "pass" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	req.Assets = nil
	_, err = NewCheckExecutor(cwd).Execute(t.Context(), req, &noopReporter{})
	if !errors.Is(err, workflow.CaptureMissing) {
		t.Fatalf("check swallowed capture error: %v", err)
	}
}

type frozenFanoutExecutor struct {
	command *CommandExecutor
	ready   chan struct{}
	release chan struct{}
	calls   chan engine.StepRequest
}

func (e *frozenFanoutExecutor) Execute(ctx context.Context, r engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if r.Step.ID == "discover" {
		close(e.ready)
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &step.Result{Status: step.StatusSucceeded, Structured: []byte(`{"items":["a","b"]}`)}, nil
	}
	e.calls <- r
	return e.command.Execute(ctx, r, rep)
}
func TestFrozenAuthoringScriptsFanout(t *testing.T) {
	dir := t.TempDir()
	authoringProbeWrite(t, dir, "skill/SKILL.md", "---\nname: fixture\ndescription: Synthetic fanout\n---\nDiscover fixtures.\n")
	authoringProbeWrite(t, dir, "script.sh", "echo ORIGINAL\n")
	authoringProbeWrite(t, dir, "workflow.toml", "[workflow]\nname='fanout-capture'\nversion='1'\n[[step]]\nid='discover'\ntype='agent'\nskill='skill'\n[step.schema]\nitems={list='text'}\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\ndepends_on=['discover']\n[step.foreach]\nitems='@discover.items'\nas='item'\nmax_items=2\n")
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	e := &frozenFanoutExecutor{command: NewCommandExecutor(t.TempDir()), ready: make(chan struct{}), release: make(chan struct{}), calls: make(chan engine.StepRequest, 2)}
	run, err := engine.NewManager(e, "").Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Cancel(); run.Wait() })
	select {
	case <-e.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("producer barrier")
	}
	if err := os.Remove(filepath.Join(dir, "script.sh")); err != nil {
		t.Fatal(err)
	}
	close(e.release)
	if frozenWait(t, run).Failed {
		t.Fatal("fanout failed")
	}
	if len(e.calls) != 2 {
		t.Fatalf("child calls=%d", len(e.calls))
	}
	for i := 0; i < 2; i++ {
		req := <-e.calls
		if req.AssetOwner != "worker" || req.FanOutItem == nil {
			t.Fatal("child lost template binding")
		}
		b, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetScript)
		if err != nil || string(b) != "echo ORIGINAL\n" {
			t.Fatalf("child capture=%q err=%v", b, err)
		}
	}
}
