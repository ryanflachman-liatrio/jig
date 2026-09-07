package main

import (
	"fmt"
	"os"
	"strings"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/runner"
	"jig/internal/sentinel"
	"jig/internal/workflow"
)

// newManager builds the production Manager used by both the TUI and
// `jig run`. Keeping registration in one place prevents the two clients from
// drifting on executor / harness / secret wiring.
func newManager(root string) *engine.Manager {
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, runner.NewCommandExecutor(""))
	mux.Register(workflow.StepCheck, runner.NewCheckExecutor(""))
	mux.Register(workflow.StepAgent, runner.NewAgentExecutor(harness.For))
	mux.Register(workflow.StepReview, runner.NewFakeExecutor(nil, runner.FakeOutcome{}))
	mgr := engine.NewManager(mux, root)
	mgr.SetIntegrationResolver(runner.NewIntegrationResolver(harness.For))
	mgr.SetSecretResolver(resolveNamedSecret)
	if monitors := discoverMonitors("examples/agents/monitors"); len(monitors) > 0 {
		mgr.SetMonitors(monitors)
	}
	return mgr
}

// resolveNamedSecret keeps secret values outside workflow TOML. The name is
// normalized the same way command execution exposes JIG_SECRET_<NAME>.
func resolveNamedSecret(name string) (string, error) {
	key := "JIG_SECRET_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("%s is not set", key)
	}
	return value, nil
}

// discoverMonitors returns MonitorDef entries for every .md file found in dir.
// Each file's base name (without extension) becomes the monitor name. Files that
// fail to stat are silently skipped so the binary remains usable outside the repo.
func discoverMonitors(dir string) []sentinel.MonitorDef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	adapter := runner.NewMonitorAdapter()
	var defs []sentinel.MonitorDef
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 4 || e.Name()[len(e.Name())-3:] != ".md" {
			continue
		}
		name := e.Name()[:len(e.Name())-3] // strip .md
		defs = append(defs, sentinel.MonitorDef{
			File:       dir + "/" + e.Name(),
			Monitor:    name,
			Dispatcher: adapter,
		})
	}
	return defs
}
