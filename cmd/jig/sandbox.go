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
	policy string
	seen   bool
}

func addSandboxFlags(fs *flag.FlagSet) *sandboxFlags {
	f := &sandboxFlags{}
	fs.Func("sandbox-policy", "trusted operator sandbox policy file", f.setPolicy)
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
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("sandbox policy: process directory unavailable")
	}
	home, _ := os.UserHomeDir()
	project := cwd
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("sandbox policy: project directory unavailable")
		}
		project = filepath.Dir(abs)
	}
	return sandbox.Load(sandbox.Selection{ExplicitPath: f.policy, Cwd: cwd, Project: project, Home: home, XDGConfigHome: os.Getenv("XDG_CONFIG_HOME")})
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
