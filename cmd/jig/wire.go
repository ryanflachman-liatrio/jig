package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/notification"
	"jig/internal/runner"
	"jig/internal/telemetry"
	"jig/internal/workflow"
)

// Runtime bundles the process-wide state that owns cross-run behavior: the
// engine Manager, the notification Dispatcher, the DiagnosticStore, and the
// Lifecycle observer. One Runtime is constructed per jig process; both the
// TUI and headless entry paths use the same instance so a run started in the
// TUI shares its notification queue and diagnostic history with any
// subsequent `jig run` invocations from the same process.
type Runtime struct {
	Manager     *engine.Manager
	Dispatcher  *notification.Dispatcher
	Diagnostics *notification.DiagnosticStore
	Lifecycle   *notification.Lifecycle
	Senders     *notification.SenderRegistry

	root          string
	mu            sync.Mutex
	policies      map[string]workflow.NotificationPolicy // runID → frozen resolved policy
	bindingsByRun map[string][]notification.Binding
	closed        bool
}

// NewRuntime constructs the shared process runtime. It never contacts a
// receiver or reads operator configuration on its own until a run is
// registered; the observer resolves bindings once per RunRegistered call.
func NewRuntime(root string) (*Runtime, error) {
	return newRuntime(root, nil)
}

// newRuntime is [NewRuntime] with an optional telemetry handle. When tel is
// non-nil, the Manager's executor is wrapped in a telemetry.MetricMux so
// every step reports OTel spans/metrics alongside the notification runtime.
// cmd/jig's CLI entry points use this; tests use the exported NewRuntime.
func newRuntime(root string, tel *telemetryHandle) (*Runtime, error) {
	mgr, err := newManager(root, tel)
	if err != nil {
		return nil, err
	}
	diag := notification.NewDiagnosticStore()
	senders := notification.NewSenderRegistry(
		notification.NewDesktopSender(),
		&notification.HTTPSender{Slack: true, Client: notification.NewHTTPClient()},
		&notification.HTTPSender{Client: notification.NewHTTPClient()},
	)
	dispatcher := notification.NewDispatcher(senders, diag)
	rt := &Runtime{
		Manager:       mgr,
		Dispatcher:    dispatcher,
		Diagnostics:   diag,
		Senders:       senders,
		root:          root,
		policies:      make(map[string]workflow.NotificationPolicy),
		bindingsByRun: make(map[string][]notification.Binding),
	}
	rt.Lifecycle = notification.NewLifecycle(dispatcher, diag, rt.resolveBindings)
	dispatcher.WithResolver(rt.Lifecycle)
	mgr.RegisterObserver(rt.Lifecycle)
	return rt, nil
}

// resolveBindings is the bindings function the notification.Lifecycle calls
// once per RunRegistered. It reads the operator's local configuration and
// resolves current secrets. Missing config resolves to empty bindings
// silently, matching FR-05 (missing local config means disabled).
func (r *Runtime) resolveBindings(policy workflow.NotificationPolicy) []notification.Binding {
	cfg, err := notification.LoadLocalConfig(r.root, os.ReadFile)
	if err != nil {
		r.Diagnostics.RecordOverflow(err.Error())
		return nil
	}
	_, bindings := notification.ResolveBindings(policy, cfg, notification.Inspection{
		ResolveSecret: resolveNamedSecret,
		DesktopStatus: notification.LocalDesktopStatus,
	})
	return bindings
}

// PrepareRun records the run's frozen policy for later diagnostic display.
// The heavy lifting (bindings resolution) happens inside the observer at
// RunRegistered time so a race between Start and PrepareRun is impossible.
func (r *Runtime) PrepareRun(runID string, policy workflow.NotificationPolicy) {
	if runID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[runID] = policy
}

// Close releases the shared runtime: stops accepting new notifications,
// drains the dispatcher for its bounded window, and clears process-scoped
// lookups. It is safe to call more than once.
func (r *Runtime) Close(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	if r.Dispatcher != nil {
		r.Dispatcher.Shutdown(ctx)
	}
	notification.SetPolicyLookup(nil)
}

// DrainDiagnosticsTo writes the accumulated notification diagnostics as
// sanitized lines to w. Headless invokes it after run settlement so the JSON
// stdout envelope is byte-compatible even when delivery is disabled or
// failing. Persistence-off callers can pass the runtime output.
func (r *Runtime) DrainDiagnosticsTo(w interface{ Write(p []byte) (int, error) }) {
	if r == nil || r.Diagnostics == nil || r.Diagnostics.Empty() {
		return
	}
	_, _ = w.Write([]byte(r.Diagnostics.Render("notification: ")))
}

// ResolvedPolicyForRun is the accessor the TUI diagnostic surface uses to
// render the run's frozen policy alongside recent delivery outcomes.
func (r *Runtime) ResolvedPolicyForRun(runID string) workflow.NotificationPolicy {
	if r == nil {
		return workflow.NotificationPolicy{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policies[runID]
}

// ReleaseRun clears per-run notification state after a run settles. Callers
// invoke this when they are certain the dispatcher will not need the policy
// or bindings for any further notifications on that run.
func (r *Runtime) ReleaseRun(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.policies, runID)
	delete(r.bindingsByRun, runID)
}

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

// projectRoot returns the resolved project root (the directory that contains
// the .agents/ tree) so wire code can pass it to notification helpers that
// look up profile files. Callers pass the workflow file path; on error the
// helper returns the working directory as a best-effort fallback.
func projectRoot(workflowPath string) string {
	abs, err := filepath.Abs(workflowPath)
	if err != nil {
		return workflowPath
	}
	return filepath.Dir(abs)
}
