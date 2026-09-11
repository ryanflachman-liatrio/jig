package main

import (
	"fmt"
	"os"
	"strings"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/runner"
	"jig/internal/telemetry"
	"jig/internal/workflow"
)

// newManager builds the production Manager used by both the TUI and
// `jig run`. Keeping registration in one place prevents the two clients from
// drifting on executor / harness / secret wiring.
//
// When a non-nil telemetryHandle is supplied, the runner Mux is wrapped in a
// telemetry.MetricMux so every step opens a jig.step span and reports a
// jig.step.duration histogram. The wrap is a zero-cost pass-through when the
// handle carries a noop Provider, so callers can invoke this unconditionally.
func newManager(root string, tel *telemetryHandle) (*engine.Manager, error) {
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, runner.NewCommandExecutor(""))
	mux.Register(workflow.StepCheck, runner.NewCheckExecutor(""))
	mux.Register(workflow.StepAgent, runner.NewAgentExecutor(harness.For))
	mux.Register(workflow.StepReview, runner.NewFakeExecutor(nil, runner.FakeOutcome{}))

	var exec engine.Executor = mux
	if tel != nil && tel.Provider != nil {
		exec = telemetry.NewMetricMux(mux, tel.Provider, tel.stepLabelsFor)
	}

	mgr := engine.NewManager(exec, root)
	mgr.SetIntegrationResolver(runner.NewIntegrationResolver(harness.For))
	mgr.SetSecretResolver(resolveNamedSecret)
	monitors, err := runner.BuiltinMonitors()
	if err != nil {
		return nil, fmt.Errorf("load built-in security monitors: %w", err)
	}
	mgr.SetMonitors(monitors)
	return mgr, nil
}

// resolveNamedSecret keeps secret values outside workflow TOML. The name is
// normalized the same way command execution exposes JIG_SECRET_<NAME>.
func resolveNamedSecret(name string) (string, error) {
	return resolveNamedSecretWithLookup(name, os.LookupEnv)
}

func resolveNamedSecretWithLookup(name string, lookup func(string) (string, bool)) (string, error) {
	key := "JIG_SECRET_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	value, ok := lookup(key)
	if !ok {
		return "", fmt.Errorf("%s is not set", key)
	}
	return value, nil
}
