package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04ReopenCLI(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "project", ".jig")
	os.MkdirAll(filepath.Dir(root), 0700)
	operator := filepath.Join(dir, "operator")
	os.Mkdir(operator, 0700)
	policy := filepath.Join(operator, "policy.toml")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(policy, "version=1\nallow_host=true\n")
	source := filepath.Join(filepath.Dir(root), "workflow.toml")
	script := filepath.Join(filepath.Dir(root), "script.sh")
	write(script, "printf ORIGINAL_LOCKED_WORK")
	write(source, "[workflow]\nname='policy-cli'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nscript='script.sh'\n")
	wf, err := workflow.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	m := engine.NewManager(authoringCLIExecutor(func(ctx context.Context, _ engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
		close(ready)
		<-ctx.Done()
		return nil, ctx.Err()
	}), root)
	f := &sandboxFlags{policy: policy}
	f.endpoint.Cwd = dir
	f.endpoint.Home = operator
	f.endpoint.Project = filepath.Dir(root)
	m.SetSandboxEpochLoader(f.epochLoader(root))
	run, err := m.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("start timeout")
	}
	journal := filepath.Join(m.RunDir(run.ID), "journal.jsonl")
	prefix, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	run.Cancel()
	run.Wait()
	write(script, "printf ALTERED_CHECKOUT_WORK")
	write(source, "broken current checkout")
	reset := func() {
		t.Helper()
		if err := os.WriteFile(journal, prefix, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"abort", "retry", "skip", "resume"} {
		t.Run(action, func(t *testing.T) {
			reset()
			write(policy, "version=1\nallow_host=false\n")
			code, out := authoringCLIOutput(t, func() int {
				return runResume([]string{run.ID, "--root", root, "--on-recovery", action, "--output=json"})
			})
			if code != 1 || !strings.Contains(out, "sandbox_policy_blocked") {
				t.Fatalf("exit=%d %s", code, out)
			}
			events, err := engine.ReplayJournalRaw(m.RunDir(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			blocks := 0
			for _, ev := range events {
				if _, ok := ev.(engine.SandboxPolicyBlocked); ok {
					blocks++
				}
			}
			want := 1
			if action == "retry" {
				want = 2
			}
			if blocks != want {
				t.Fatalf("policy rechecks: blocks=%d", blocks)
			}
			t.Logf("jig resume --on-recovery=%s: exit=1; block records=%d; no new worker attempt", action, blocks)
		})
	}
	t.Run("missing-selected-file", func(t *testing.T) {
		reset()
		os.Remove(policy)
		code, out := authoringCLIOutput(t, func() int { return runResume([]string{run.ID, "--root", root, "--on-recovery=retry", "--output=json"}) })
		if code != 1 || !strings.Contains(out, "sandbox_policy_unavailable") {
			t.Fatalf("missing file: %d %s", code, out)
		}
	})
	t.Run("explicit-reselection", func(t *testing.T) {
		reset()
		replacement := filepath.Join(operator, "replacement.toml")
		write(replacement, "version=1\nallow_host=true\n")
		code, out := authoringCLIOutput(t, func() int {
			return runResume([]string{run.ID, "--root", root, "--on-recovery=retry", "--sandbox-policy", replacement, "--output=jsonl"})
		})
		if strings.Contains(out, replacement) || strings.Contains(out, "fingerprint") || strings.Contains(out, "effective") {
			t.Fatal("JSONL disclosed local policy grant metadata")
		}
		if code != 0 {
			t.Fatalf("reselected: %d %s", code, out)
		}
		data, err := os.ReadFile(filepath.Join(m.RunDir(run.ID), "steps", "work", "transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "ORIGINAL_LOCKED_WORK") || strings.Contains(string(data), "ALTERED_CHECKOUT_WORK") {
			t.Fatal("reopen changed captured work")
		}
		t.Log("explicit trusted reselection: exit=0; original captured script ran despite changed checkout")
	})
	// Status is an offline fold: no current policy file is needed.
	reset()
	ev := engine.SandboxPolicyBlocked{RunID: run.ID, Reason: "sandbox_policy_denied", Epoch: 30}
	line, _ := engine.MarshalEnvelope(30, ev)
	file, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Write(append(line, '\n'))
	file.Close()
	for _, format := range []string{"text", "json"} {
		code, out := authoringCLIOutput(t, func() int { return runStatus([]string{run.ID, "--root", root, "--output", format}) })
		if code != 0 || !strings.Contains(out, "sandbox_policy_denied") {
			t.Fatalf("status %s: %d %s", format, code, out)
		}
		if format == "json" {
			var v any
			if json.Unmarshal([]byte(out), &v) != nil {
				t.Fatalf("invalid status JSON: %s", out)
			}
		}
	}
}

func TestD04ReopenPolicyWholeGraphPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.toml")
	raw := fmt.Sprintf(`version=1
allow_host=false
[docker]
allowed_endpoints=["unix:///synthetic/docker.sock"]
allowed_repositories=["alpine"]
allowed_images=[]
allowed_networks=["none"]
workspace_roots=[%q]
storage_roots=[%q]
authentication="none"
[docker.defaults]
cpus=1
memory="64MiB"
pids=64
[docker.ceilings]
cpus=2
memory="128MiB"
pids=128
`, dir, dir)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Decode("[workflow]\nname='precedence'\nversion='1'\n[[sandbox]]\nname='box'\nruntime='docker'\nimage='alpine'\n[[step]]\nid='docker_first'\ntype='command'\nrun='true'\nsandbox='box'\n[[step]]\nid='host_last'\ntype='command'\nrun='true'\nsandbox='host'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	flags := &sandboxFlags{policy: path}
	flags.endpoint.Cwd = dir
	flags.endpoint.Home = dir
	flags.endpoint.Project = dir
	_, _, err = flags.epochLoader("")(context.Background(), wf, nil)
	var denied *sandbox.Failure
	if !errors.As(err, &denied) || denied.Field != "allow_host" || denied.StepID != "host_last" {
		t.Fatalf("runtime support hid later policy denial: %v", err)
	}
}
