package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
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

func (f *sandboxFlags) deploymentCheck(policy *sandbox.Policy) func() error {
	if !f.endpoint.Explicit() {
		return nil
	}
	input := f.endpoint
	return func() error { _, err := sandbox.SelectEndpoint(input, policy); return err }
}
