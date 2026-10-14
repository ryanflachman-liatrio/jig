package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func runnerModuleFixture(t *testing.T, backend, mode string) (string, *workflow.Workflow) {
	t.Helper()
	dir := t.TempDir()
	worker := "[[step]]\nid='worker'\ntype='agent'\nskill='skill'\noutput_template='template.md'\nschema_file='schema.json'\noutput='result.md'\n[step.validate]\noutput_schema='validation.json'\n"
	if mode == "retry" {
		worker = strings.Replace(worker, "[step.validate]", "on_failure='retry'\nmax_retries=1\n[step.validate]", 1)
	}
	if mode == "fanout" {
		worker = strings.Replace(worker, "output='result.md'\n[step.validate]\noutput_schema='validation.json'\n", "", 1)
		worker = "[[step]]\nid='discover'\ntype='agent'\nskill='skill'\n[step.schema]\nitems={list='text'}\n" + worker + "depends_on=['discover']\n[step.foreach]\nitems='@discover.items'\nas='item'\nmax_items=2\n"
	}
	suffix := ""
	export := "@worker"
	if mode == "route" {
		suffix = "[[step]]\nid='gate'\ntype='command'\nrun='true'\ndepends_on=['worker']\noutput_type={enum=['again','ok']}\n[[step.route]]\nwhen=\"gate == 'again'\"\ngoto='worker'\nmax_iterations=2\n"
		export = "@gate"
	}
	files := map[string]string{
		"workflow.toml":                "[workflow]\nname='module-consumer'\nversion='1'\n[defaults]\nmax_parallel=2\n[defaults.security]\ntier1_enabled=false\n[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='first'\ntype='subworkflow'\nmodule='modules/a.toml'\ndepends_on=['ready']\n[[step]]\nid='second'\ntype='subworkflow'\nmodule='modules/a.toml'\ndepends_on=['ready']\n",
		"modules/a.toml":               "[module]\nschema_version=1\n[module.exports.result]\nref='@nested.result'\n[[step]]\nid='nested'\ntype='subworkflow'\nmodule='deep/b.toml'\n",
		"modules/deep/b.toml":          "[module]\nschema_version=1\n[module.exports.result]\nref='" + export + "'\n[defaults]\nagent={backend='" + backend + "'}\n[defaults.security]\ntier1_enabled=false\n" + worker + suffix,
		"modules/deep/skill/SKILL.md":  "---\nname: fixture\ndescription: Synthetic\n---\nORIGINAL prompt {{skill_dir}}\n",
		"modules/deep/skill/helper.sh": "ORIGINAL helper\n",
		"modules/deep/template.md":     "# ORIGINAL template\n",
		"modules/deep/schema.json":     `{"type":"object","properties":{"original":{"type":"string"}}}`,
		"modules/deep/validation.json": `{"title":"ORIGINAL validation"}`,
	}
	for p, s := range files {
		authoringProbeWrite(t, dir, p, s)
	}
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, wf
}

type moduleObservation struct {
	req  engine.StepRequest
	spec harness.SessionSpec
}
type moduleExecutor struct {
	cwd, mode      string
	ready, release chan struct{}
	observed       chan moduleObservation
}

func (e *moduleExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if req.Step.ID == "ready" {
		close(e.ready)
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
	if req.Step.Type == workflow.StepCommand {
		verdict := "ok"
		if req.Iteration == 0 {
			verdict = "again"
		}
		return &step.Result{Status: step.StatusSucceeded, Verdict: verdict}, nil
	}
	req.ExecutionDir = filepath.Join(e.cwd, req.Step.ID)
	if err := os.MkdirAll(req.ExecutionDir, 0700); err != nil {
		return nil, err
	}
	ev := harness.Event{Type: harness.EventResult, SessionID: "synthetic-session", Structured: json.RawMessage(`{"original":"value","items":["one","two"],"status":"complete"}`)}
	if e.mode == "retry" && req.Attempt == 0 {
		ev.IsError = true
		ev.ErrText = "synthetic attempt failure"
	}
	h := &harness.FakeHarness{NameVal: req.Step.Backend, Caps: harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput, harness.CapSessionResume), Sess: harness.NewFakeSession([]harness.Event{ev})}
	a := NewAgentExecutor(func(backend string) (harness.Harness, error) {
		if backend != req.Step.Backend {
			return nil, fmt.Errorf("backend mismatch")
		}
		return h, nil
	})
	result, err := a.Execute(ctx, req, rep)
	e.observed <- moduleObservation{req, h.OpenSpec}
	return result, err
}

func TestFrozenModuleAuthoringAssets(t *testing.T) {
	for _, scenario := range []string{"claude/retry", "claude/route", "claude/fanout", "cursor/retry", "cursor/route", "cursor/fanout", "codex/retry", "codex/route", "codex/fanout"} {
		t.Run(scenario, func(t *testing.T) {
			parts := strings.Split(scenario, "/")
			backend, mode := parts[0], parts[1]
			dir, wf := runnerModuleFixture(t, backend, mode)
			cwd := t.TempDir()
			authoringProbeWrite(t, cwd, "skill/SKILL.md", "DECOY")
			authoringProbeWrite(t, cwd, "schema.json", "DECOY")
			e := &moduleExecutor{cwd: cwd, mode: mode, ready: make(chan struct{}), release: make(chan struct{}), observed: make(chan moduleObservation, 16)}
			storage := filepath.Join(dir, ".jig")
			if mode == "fanout" {
				storage = ""
			}
			run, err := engine.NewManager(e, storage).Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			select {
			case <-e.ready:
			case <-time.After(5 * time.Second):
				t.Fatal("barrier")
			}
			if err := os.RemoveAll(filepath.Join(dir, "modules")); err != nil {
				t.Fatal(err)
			}
			for i := range wf.Steps {
				if wf.Steps[i].Schema != nil {
					wf.Steps[i].Schema.Fields[0].Name = "CALLER"
				}
				wf.Steps[i].SnapshotAgentPrompt = "CALLER"
			}
			close(e.release)
			settled := make(chan engine.RunSnapshot, 1)
			go func() { settled <- run.Wait() }()
			var snap engine.RunSnapshot
			select {
			case snap = <-settled:
			case <-time.After(10 * time.Second):
				for _, st := range run.Snapshot().Steps {
					if st.Result != nil {
						t.Logf("%s: %+v", st.ID, *st.Result)
					}
				}
				t.Fatalf("unsettled: %+v", run.Snapshot())
			}
			if snap.Failed {
				t.Fatalf("run failed: %+v", snap)
			}
			count := len(e.observed)
			want := 4
			if mode == "fanout" {
				want = 6
			}
			if count != want {
				t.Fatalf("opens=%d want=%d", count, want)
			}
			owners := map[string]int{}
			ids := map[string]bool{}
			var inventory int
			for i := 0; i < count; i++ {
				o := <-e.observed
				req, spec := o.req, o.spec
				if spec.Cwd != filepath.Join(cwd, req.Step.ID) || spec.Agent.Backend() != backend || !strings.Contains(spec.Prompt, "ORIGINAL prompt {{skill_dir}}") || strings.Contains(spec.Prompt, "CALLER") {
					t.Fatalf("host spec: %+v", spec)
				}
				if strings.HasSuffix(req.AssetOwner, "__worker") {
					props := spec.Schema["properties"].(map[string]any)
					if props["original"] == nil || props["CALLER"] != nil {
						t.Fatal("wrong producer schema")
					}
					if storage != "" && !strings.Contains(spec.Prompt, "# ORIGINAL template") {
						t.Fatal("wrong template")
					}
					raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetValidationSchema)
					if mode != "fanout" && (err != nil || string(raw) != `{"title":"ORIGINAL validation"}`) {
						t.Fatalf("validation=%s %v", raw, err)
					}
					owners[req.AssetOwner]++
					ids[req.Step.ID] = true
				}
				m := req.Assets.Manifest()
				if inventory != 0 && inventory != len(m.Bindings) {
					t.Fatal("runtime recapture")
				}
				inventory = len(m.Bindings)
				for _, b := range m.Bindings {
					if b.Owner == req.AssetOwner && b.Kind == workflow.AssetSkill && b.Relative == "helper.sh" {
						raw, ok := req.Assets.Blob(b.SHA256)
						if !ok || string(raw) != "ORIGINAL helper\n" {
							t.Fatal("lost bundle")
						}
						raw[0] = 'X'
						again, _ := req.Assets.Blob(b.SHA256)
						if string(again) != "ORIGINAL helper\n" {
							t.Fatal("mutable bundle")
						}
					}
				}
				compiled, err := json.Marshal(spec.Schema)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("owner=%s runtime=%s attempt=%d iteration=%d prompt_sha256=%x schema=%v bindings=%d schema_sha256=%x", req.AssetOwner, req.Step.ID, req.Attempt, req.Iteration, sha256.Sum256([]byte(spec.Prompt)), spec.Schema["properties"], inventory, sha256.Sum256(compiled))
			}
			if len(owners) != 2 || owners["first__nested__worker"] != 2 || owners["second__nested__worker"] != 2 {
				t.Fatalf("owners=%v", owners)
			}
			if mode == "fanout" && len(ids) != 4 {
				t.Fatal("children lost runtime identity")
			}
			if storage == "" {
				if _, err := os.Stat(filepath.Join(dir, ".jig")); !os.IsNotExist(err) {
					t.Fatal("memory persistence")
				}
			}
		})
	}
}

func TestHostSkillExecutionPreserved(t *testing.T) {
	for _, backend := range []string{"claude", "cursor", "codex"} {
		t.Run(backend, func(t *testing.T) {
			for _, resume := range []bool{false, true} {
				dir, wf := runnerModuleFixture(t, backend, "")
				owned, assets, err := workflow.PrepareAuthoring(wf, "")
				if err != nil {
					t.Fatal(err)
				}
				st := &owned.Steps[1]
				if err := os.RemoveAll(filepath.Join(dir, "modules")); err != nil {
					t.Fatal(err)
				}
				cwd := t.TempDir()
				h := &harness.FakeHarness{NameVal: backend, Caps: harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput, harness.CapSessionResume), Sess: harness.NewFakeSession([]harness.Event{{Type: harness.EventResult, SessionID: "conversation"}})}
				selected := ""
				a := NewAgentExecutor(func(b string) (harness.Harness, error) { selected = b; return h, nil })
				req := engine.StepRequest{Step: st, Assets: assets, AssetOwner: st.ID, ExecutionDir: cwd}
				if resume {
					req.ResumeSessionID = "conversation"
					req.Message = "Operator reply"
				}
				result, err := a.Execute(t.Context(), req, &captureReporter{})
				if err != nil || result.Status != step.StatusSucceeded {
					t.Fatalf("%+v %v", result, err)
				}
				spec := h.OpenSpec
				if selected != backend || spec.Agent.Backend() != backend || spec.Cwd != cwd || result.SessionID != "conversation" {
					t.Fatalf("host spec=%+v result=%+v", spec, result)
				}
				if resume {
					if spec.Prompt != "Operator reply" || spec.Resume != "conversation" {
						t.Fatal("resume changed")
					}
				} else if !strings.Contains(spec.Prompt, "ORIGINAL prompt {{skill_dir}}") || spec.Resume != "" {
					t.Fatal("fresh prompt changed")
				}
				if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 1 || entries[0].Name() != "result.md" {
					t.Fatal("expected only materialized declared output, no helper root")
				}
				t.Logf("backend=%s resume=%v cwd=execution_dir prompt preserved; no helper roots", backend, resume)
				// Required capabilities still fail closed, independently of capture support.
				h.Caps = harness.NewCapabilitySet(harness.CapPermissionCallback)
				failed, err := a.Execute(t.Context(), req, &captureReporter{})
				if err != nil || failed.Status != step.StatusFailed {
					t.Fatal("capability refusal lost")
				}
				emptyWF := &workflow.Workflow{}
				_, emptyAssets, err := workflow.PrepareAuthoring(emptyWF, cwd)
				if err != nil {
					t.Fatal(err)
				}
				missingReq := req
				missingReq.Assets = emptyAssets
				if _, err := a.Execute(t.Context(), missingReq, &captureReporter{}); err != workflow.CaptureMissing {
					t.Fatalf("missing capture: %v", err)
				}
				st.SnapshotAgentPrompt = "SUBSTITUTE"
				_, err = a.Execute(t.Context(), req, &captureReporter{})
				if err != workflow.CaptureIntegrity {
					t.Fatalf("effective mismatch: %v", err)
				}
			}
		})
	}
}

func (e *moduleExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	req.ExecutionDir = filepath.Join(e.cwd, req.Step.ID)
	return validateAttempt(ctx, req, rep, e.cwd)
}
