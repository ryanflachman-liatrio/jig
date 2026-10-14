package runner

import (
	"bytes"
	"context"
	"fmt"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/processgroup"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/headless"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD02ValidationAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name, main, validator     string
		success, isolated, memory bool
	}{
		{"all predicates", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "true", true, true, false},
		{"command passes schema fails", `printf '%s' '{"ok":false,"label":"REQUIRED"}' > result.json`, "true", false, true, false},
		{"command passes content absent", `printf '%s' '{"ok":true,"label":"WRONG"}' > result.json`, "true", false, true, false},
		{"command passes file absent", "true", "true", false, true, false},
		{"validator fails", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "false", false, true, false},
		{"main failure skips validator", "exit 3", "touch validator-ran", false, true, false},
		{"allowed validator edit", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "echo ALLOWED > allowed.txt", true, true, false},
		{"forbidden validator edit", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "echo FORBIDDEN > forbidden.txt", false, true, false},
		{"validation failure retries", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "test -f validated.once || { touch validated.once; exit 1; }", true, true, false},
		{"reader does not integrate", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "echo ALLOWED > allowed.txt", true, false, false},
		{"persistence off fallback", `printf '%s' '{"ok":true,"label":"REQUIRED"}' > result.json`, "echo ALLOWED > allowed.txt", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			authoringProbeWrite(t, dir, ".gitignore", ".jig/\n")
			authoringProbeWrite(t, dir, "schema.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"ok":{"const":true}},"required":["ok"]}`)
			isolation := ""
			if tc.isolated {
				isolation = "isolation='worktree'\nmutation_paths=['result.json','allowed.txt','validator-ran','validated.once']\n"
			}
			if tc.name == "validation failure retries" {
				isolation += "on_failure='retry'\nmax_retries=1\n"
			}
			source := "[workflow]\nname='d02'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun=" + fmt.Sprintf("%q", tc.main) + "\noutput='result.json'\n" + isolation + "[step.validate]\ncommand=" + fmt.Sprintf("%q", tc.validator) + "\noutput_exists=true\noutput_contains='REQUIRED'\noutput_schema='schema.json'\n[[step]]\nid='dependent'\ntype='command'\nrun='true'\ndepends_on=['worker']\n"
			authoringProbeWrite(t, dir, "workflow.toml", source)
			reopenGit(t, dir)
			wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
			if err != nil {
				t.Fatal(err)
			}
			wf, _, err = workflow.PrepareAuthoring(wf, dir)
			if err != nil {
				t.Fatal(err)
			}
			// Runtime must consume the prepared schema, not this replacement.
			authoringProbeWrite(t, dir, "schema.json", `{"not":{}}`)
			root := filepath.Join(dir, ".jig")
			if tc.memory {
				root = ""
			}
			executor := &countingValidationExecutor{CommandExecutor: NewCommandExecutor(dir)}
			mgr := engine.NewManager(executor, root)
			var output, diagnostic bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			res := headless.Run(ctx, headless.Options{Workflow: wf, Manager: mgr, CI: true, DiscardMerge: true, Quiet: true, Stdout: &output, Stderr: &diagnostic})
			if (res.ExitCode == headless.ExitOK) != tc.success {
				t.Fatalf("exit=%d expected success=%v: %s %s snapshot=%+v", res.ExitCode, tc.success, output.String(), diagnostic.String(), mgr.Runs()[0].Snapshot())
			}
			if tc.name == "main failure skips validator" && executor.validations.Load() != 0 {
				t.Fatal("failed main executed success validator")
			}
			if tc.name == "validation failure retries" && executor.validations.Load() != 2 {
				t.Fatal("validation failure did not use bounded retry")
			}
			runs := mgr.Runs()
			snap := runs[0].Snapshot()
			for _, st := range snap.Steps {
				if st.ID == "dependent" && (st.Status == step.StatusSucceeded) != tc.success {
					t.Fatalf("dependent status=%s expected dispatch=%v", st.Status, tc.success)
				}
			}
			if tc.memory {
				data, err := os.ReadFile(filepath.Join(dir, "allowed.txt"))
				if err != nil || string(data) != "ALLOWED\n" {
					t.Fatal("fallback validator did not use configured CWD", err)
				}
				if _, err := os.Stat(filepath.Join(dir, ".jig")); !os.IsNotExist(err) {
					t.Fatal("persistence-off wrote .jig")
				}
				return
			}
			branch := "jig/d02/run-" + snap.ID
			read := func(path string) ([]byte, error) {
				cmd := exec.Command("git", "show", branch+":"+path)
				cmd.Dir = dir
				return cmd.CombinedOutput()
			}
			if tc.success && tc.isolated {
				if _, err := read("result.json"); err != nil {
					t.Fatal("accepted output missing from run branch", err)
				}
			}
			if !tc.success || !tc.isolated {
				for _, path := range []string{"result.json", "allowed.txt", "forbidden.txt", "validator-ran"} {
					if _, err := read(path); err == nil {
						t.Fatalf("rejected/reader candidate integrated %s", path)
					}
				}
			}
			if tc.name == "allowed validator edit" {
				if data, err := read("allowed.txt"); err != nil || string(data) != "ALLOWED\n" {
					t.Fatal("allowed validator edit absent", err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "result.json")); !os.IsNotExist(err) {
				t.Fatal("persisted execution changed host checkout")
			}
			t.Logf("exit=%d dependent=%v isolated=%v final candidate checked", res.ExitCode, tc.success, tc.isolated)
		})
	}
}

func TestD02ValidationOutputBounds(t *testing.T) {
	for _, size := range []int64{validationOutputLimit, validationOutputLimit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			f, err := os.Create(filepath.Join(dir, "result"))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(size); err != nil {
				t.Fatal(err)
			}
			f.Close()
			req := engine.StepRequest{Step: &workflow.Step{Output: "result", Validate: &workflow.Validate{Command: "true", OutputExists: true}}, ExecutionDir: dir}
			ok, detail, err := NewCommandExecutor("").Validate(t.Context(), req, &noopReporter{})
			if err != nil || ok != (size <= validationOutputLimit) {
				t.Fatalf("ok=%v detail=%s err=%v", ok, detail, err)
			}
		})
	}
}

func TestD02ManagedMainGroupEndsBeforeValidation(t *testing.T) {
	dir := t.TempDir()
	main := `sh -c 'echo $$ > child.pid; while :; do echo LIVE >> child-output; done' >/dev/null 2>&1 & while test ! -s child.pid; do :; done`
	req := engine.StepRequest{Step: &workflow.Step{ID: "worker", Type: workflow.StepCommand, Run: main, Validate: &workflow.Validate{Command: "test -s child.pid"}}, ExecutionDir: dir}
	e := NewCommandExecutor("")
	result, err := e.Execute(t.Context(), req, &noopReporter{})
	if err != nil || result.Status != step.StatusSucceeded || result.ExecutionUncertain {
		t.Fatalf("%+v %v", result, err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "child-output"))
	if err != nil {
		t.Fatal(err)
	}
	ok, detail, err := e.Validate(t.Context(), req, &noopReporter{})
	if !ok || err != nil {
		t.Fatalf("%s %v", detail, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "child-output"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("managed child continued writing during validator", err)
	}
}

func TestD02SchemaLocalReferencesOnly(t *testing.T) {
	for _, tc := range []struct {
		schema string
		valid  bool
	}{
		{`{"$defs":{"ok":{"const":true}},"$ref":"#/$defs/ok"}`, true},
		{`{"$ref":"https://example.invalid/PRIVATE_REFERENCE"}`, false},
		{`{"$ref":"file:///PRIVATE_REFERENCE"}`, false},
		{`{"$schema":"http://json-schema.org/draft-07/schema#"}`, false},
		{`{"type":"PRIVATE_SCHEMA_CONTENT"}`, false},
		{`{"$defs":{"legacy":{"$schema":"http://json-schema.org/draft-07/schema#"}}}`, false},
	} {
		schema, err := workflow.CompileValidationSchema([]byte(tc.schema))
		if (err == nil) != tc.valid {
			t.Fatalf("valid=%v err=%v", tc.valid, err)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("schema content leaked")
		}
		if tc.valid && schema.Validate(true) != nil {
			t.Fatal("local reference not enforced")
		}
	}
}

func TestD02AgentOutputMaterializedBeforeValidation(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprint(persisted), func(t *testing.T) {
			dir := t.TempDir()
			for _, prose := range []bool{false, true} {
				events := []harness.Event{}
				if prose {
					events = append(events, harness.Event{Type: harness.EventText, Text: `{"ok":true,"label":"REQUIRED"}`}, harness.Event{Type: harness.EventAssistantEnd})
				}
				events = append(events, harness.Event{Type: harness.EventResult, SessionID: "synthetic", Structured: []byte(`{"ok":true,"label":"REQUIRED"}`)})
				h := &harness.FakeHarness{Caps: harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput), Sess: harness.NewFakeSession(events)}
				req := engine.StepRequest{Step: &workflow.Step{ID: "worker", Type: workflow.StepAgent, Output: "result.json", Validate: &workflow.Validate{Command: "test -f result.json", OutputExists: true, OutputContains: "REQUIRED"}}, ExecutionDir: dir}
				if persisted {
					req.TranscriptPath = filepath.Join(dir, "steps", "worker", "transcript.jsonl")
					if err := os.MkdirAll(filepath.Dir(req.TranscriptPath), 0700); err != nil {
						t.Fatal(err)
					}
				}
				e := NewAgentExecutorFixed(h)
				result, err := e.Execute(t.Context(), req, &noopReporter{})
				if err != nil || result.Status != step.StatusSucceeded {
					t.Fatalf("%+v %v", result, err)
				}
				ok, detail, err := e.Validate(t.Context(), req, &noopReporter{})
				if !ok || err != nil {
					t.Fatalf("prose=%v %s %v", prose, detail, err)
				}
			}
		})
	}
}

type validationReadyReporter struct {
	noopReporter
	ready chan struct{}
	once  sync.Once
}

func (r *validationReadyReporter) Output(delta string) {
	if strings.Contains(delta, "READY") {
		r.once.Do(func() { close(r.ready) })
	}
}

func TestD02RealValidatorCancellation(t *testing.T) {
	for _, mode := range []string{"stop", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			}
			defer cancel()
			rep := &validationReadyReporter{ready: make(chan struct{})}
			req := engine.StepRequest{Step: &workflow.Step{ID: "worker", Type: workflow.StepCommand, Validate: &workflow.Validate{Command: "echo READY; sleep 60 & wait"}}, ExecutionDir: t.TempDir()}
			done := make(chan error, 1)
			go func() {
				passed, _, err := NewCommandExecutor("").Validate(ctx, req, rep)
				if passed {
					done <- fmt.Errorf("cancelled validator passed")
				} else {
					done <- err
				}
			}()
			select {
			case <-rep.ready:
			case <-time.After(3 * time.Second):
				t.Fatal("validator never started")
			}
			if mode == "stop" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancellation did not propagate")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("validator descendants or stream failed to stop")
			}
		})
	}
}

type countingValidationExecutor struct {
	*CommandExecutor
	validations atomic.Int32
}

func (e *countingValidationExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	e.validations.Add(1)
	return e.CommandExecutor.Validate(ctx, req, rep)
}

func TestD02AgentUncertaintySurvivesOpenAndCloseFailures(t *testing.T) {
	for _, phase := range []string{"open", "close"} {
		t.Run(phase, func(t *testing.T) {
			session := harness.NewFakeSession([]harness.Event{{Type: harness.EventResult, SessionID: "synthetic"}})
			h := &harness.FakeHarness{Caps: harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapStructuredOutput), Sess: session}
			if phase == "open" {
				h.OpenErr = &processgroup.UncertainError{Cause: fmt.Errorf("synthetic termination failure")}
			} else {
				session.CloseErr = fmt.Errorf("synthetic termination failure")
			}
			req := engine.StepRequest{Step: &workflow.Step{ID: "worker", Type: workflow.StepAgent}, ExecutionDir: t.TempDir()}
			result, err := NewAgentExecutorFixed(h).Execute(t.Context(), req, &noopReporter{})
			if err != nil || result.Status != step.StatusFailed || !result.ExecutionUncertain {
				t.Fatalf("%s uncertainty lost: %+v %v", phase, result, err)
			}
		})
	}
}
