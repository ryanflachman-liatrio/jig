package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type sandboxFlags struct {
	policy   string
	seen     bool
	endpoint sandbox.EndpointInput
	xdg      string
}

func addSandboxFlags(fs *flag.FlagSet) *sandboxFlags {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	f := &sandboxFlags{endpoint: sandbox.EndpointInput{Cwd: cwd, Home: home, Project: cwd, EnvContext: os.Getenv("DOCKER_CONTEXT"), EnvHost: os.Getenv("DOCKER_HOST"), EnvConfig: os.Getenv("DOCKER_CONFIG")}, xdg: os.Getenv("XDG_CONFIG_HOME")}
	fs.Func("sandbox-policy", "trusted operator sandbox policy file", f.setPolicy)
	for _, item := range []struct {
		name   string
		target *string
	}{{"docker-context", &f.endpoint.Context}, {"docker-host", &f.endpoint.Host}, {"docker-config", &f.endpoint.Config}} {
		fs.Func(item.name, "operator Docker endpoint selection", func(value string) error {
			if *item.target != "" || strings.TrimSpace(value) == "" {
				return fmt.Errorf("--%s must occur once with a nonempty value", item.name)
			}
			if item.name == "docker-host" && f.endpoint.Context != "" || item.name == "docker-context" && f.endpoint.Host != "" {
				return fmt.Errorf("--docker-host and --docker-context are mutually exclusive")
			}
			if item.name == "docker-config" && !filepath.IsAbs(value) {
				value = filepath.Join(f.endpoint.Cwd, value)
			}
			*item.target = value
			return nil
		})
	}
	return f
}

func (f *sandboxFlags) setPolicy(value string) error {
	if f.seen || strings.TrimSpace(value) == "" {
		return fmt.Errorf("--sandbox-policy must occur once with a nonempty path")
	}
	f.seen = true
	path, err := filepath.Abs(value)
	if err != nil {
		return fmt.Errorf("resolve --sandbox-policy path")
	}
	f.policy = path
	return nil
}

func loadSandboxPolicy(f *sandboxFlags, root string) (*sandbox.Policy, error) {
	cwd, home := f.endpoint.Cwd, f.endpoint.Home
	if cwd == "" {
		return nil, fmt.Errorf("sandbox policy: process directory unavailable")
	}
	project := cwd
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("sandbox policy: project directory unavailable")
		}
		project = filepath.Dir(abs)
	}
	f.endpoint.Project = project
	return sandbox.Load(sandbox.Selection{ExplicitPath: f.policy, Cwd: cwd, Project: project, Home: home, XDGConfigHome: f.xdg})
}

// Bare TUI startup accepts only its operator flag here; subcommand flags are
// parsed by their own FlagSets so validate/status cannot inherit authority.
func parseStartupSandbox(args []string) (*sandboxFlags, error) {
	fs := flag.NewFlagSet("jig", flag.ContinueOnError)
	f := addSandboxFlags(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if len(fs.Args()) != 0 {
		return nil, fmt.Errorf("unexpected startup argument")
	}
	return f, nil
}

// epochLoader captures operator inputs once, then reloads trusted policy only
// at admission, reopen, or explicit recheck. It never rereads workflow files.
func (f *sandboxFlags) epochLoader(root string) engine.SandboxEpochLoader {
	input := f.endpoint
	project := input.Project
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			project = filepath.Dir(abs)
		}
	}
	selection := sandbox.Selection{ExplicitPath: f.policy, Cwd: input.Cwd, Project: project, Home: input.Home, XDGConfigHome: f.xdg}
	return func(ctx context.Context, wf *workflow.Workflow, locked *engine.SandboxPolicyState) (engine.SandboxAuthorizer, *engine.SandboxPolicyState, error) {
		source := sandbox.Source{}
		if locked != nil {
			source = sandbox.Source{Kind: locked.Source.Kind, Path: locked.Source.Path}
		}
		policy, err := sandbox.Reload(selection, source)
		if err != nil {
			return nil, nil, err
		}
		src := policy.Source()
		state := &engine.SandboxPolicyState{Source: engine.SandboxPolicySource{Kind: src.Kind, Path: src.Path}, Fingerprint: policy.Fingerprint(), Effective: map[string]engine.SandboxEffective{}}
		workspace := []string{project}
		if path, _ := wf.Source(); path != "" {
			workspace = append(workspace, filepath.Dir(path))
		}
		for _, m := range wf.ModuleSources() {
			workspace = append(workspace, filepath.Dir(m.Path))
		}
		authority := &lockedSandboxAuthority{policy: policy, values: map[string]sandbox.Effective{}}
		docker := false
		firstDockerID := ""
		for i := range wf.Steps {
			st := &wf.Steps[i]
			request := st.ResolvedSandbox()
			var previous *sandbox.Effective
			if locked != nil {
				e, ok := locked.Effective[st.ID]
				if !ok {
					return nil, nil, workflow.CaptureIntegrity
				}
				previous = &sandbox.Effective{Request: request, NanoCPUs: e.NanoCPUs, MemoryBytes: e.MemoryBytes, PIDs: e.PIDs, CPUSource: e.CPUSource, MemorySource: e.MemorySource, PIDsSource: e.PIDsSource}
			}
			e, err := policy.Evaluate(st, workspace, []string{root}, previous)
			if err != nil {
				return nil, nil, err
			}
			key, _ := json.Marshal(request)
			authority.values[string(key)] = e
			v := engine.SandboxEffective{NanoCPUs: e.NanoCPUs, MemoryBytes: e.MemoryBytes, PIDs: e.PIDs, CPUSource: e.CPUSource, MemorySource: e.MemorySource, PIDsSource: e.PIDsSource}
			if request.Profile != nil {
				docker = true
				if firstDockerID == "" {
					firstDockerID = st.ID
				}
				if locked != nil {
					v.Platform = locked.Effective[st.ID].Platform
				}
			}
			state.Effective[st.ID] = v
		}
		if docker {
			if locked == nil {
				return nil, nil, &engine.SandboxRuntimeError{StepID: firstDockerID}
			}
			if locked.Binding == nil {
				return nil, nil, workflow.CaptureIntegrity
			}
			b := locked.Binding
			// Bind the original canonical URI. Context/config edits never reselect it.
			boundInput := sandbox.EndpointInput{Cwd: input.Cwd, Home: input.Home, Project: project, Host: b.URI}
			endpoint, err := sandbox.SelectEndpoint(boundInput, policy)
			if err != nil {
				return nil, nil, err
			}
			expected := sandbox.BindingData{Endpoint: sandbox.EndpointSummary{URI: b.URI}, DaemonID: b.DaemonID, API: b.API, NativePlatform: b.NativePlatform}
			if _, err = sandbox.Inspect(ctx, endpoint, policy, wf.Steps, &expected); err != nil {
				return nil, nil, err
			}
			copied := *b
			state.Binding = &copied
		} else if input.Explicit() {
			if _, err = sandbox.SelectEndpoint(input, policy); err != nil {
				return nil, nil, err
			}
		}
		return authority, state, nil
	}
}

type lockedSandboxAuthority struct {
	policy *sandbox.Policy
	values map[string]sandbox.Effective
}

func (a *lockedSandboxAuthority) Authorize(st *workflow.Step, workspace, storage []string) error {
	key, _ := json.Marshal(st.ResolvedSandbox())
	e, ok := a.values[string(key)]
	if !ok {
		return workflow.CaptureIntegrity
	}
	_, err := a.policy.Evaluate(st, workspace, storage, &e)
	return err
}
