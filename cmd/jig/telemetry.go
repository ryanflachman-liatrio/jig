package main

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"

	"jig/internal/engine"
	"jig/internal/telemetry"
	"jig/internal/workflow"
)

// stepLabelsFor returns the exporter's label set for (runID, stepID), or a
// safe empty set when telemetry is off. MetricMux consumes this to label
// per-step spans and duration histograms.
func (h *telemetryHandle) stepLabelsFor(runID, stepID string) []attribute.KeyValue {
	if h == nil || h.Exporter == nil {
		return []attribute.KeyValue{
			attribute.String("workflow", ""),
			attribute.String("step", stepID),
			attribute.String("step_type", ""),
			attribute.String("backend", ""),
			attribute.String("transport", ""),
			attribute.String("model", ""),
		}
	}
	return h.Exporter.StepLabelsFor(runID, stepID)
}

// telemetryHandle bundles the process-scoped telemetry surfaces. cmd/jig
// creates one at startup, wraps the runner Mux around it, attaches the
// EventExporter to the Manager, and defers Shutdown at exit. TUI and headless
// share the same handle: the same Provider serves the whole process.
type telemetryHandle struct {
	Provider *telemetry.Provider
	Exporter *telemetry.EventExporter
	stop     func()
}

// setupTelemetry resolves env + .jig/telemetry.json (workflow contribution is
// deferred to registerRun) and constructs the Provider and EventExporter. It
// returns a handle whose Shutdown is a no-op when telemetry is off, so both
// TUI and headless can defer it unconditionally.
//
// Failing to resolve a config is intentionally non-fatal: a malformed prefs
// file or an OTLP dial failure logs a warning and continues with a noop
// exporter, matching the plan's "exporter failures never change exit codes"
// contract.
func setupTelemetry(ctx context.Context, root string) *telemetryHandle {
	prefs := telemetry.LoadPrefs(root)
	cfg, err := telemetry.ResolveConfig(telemetry.OSEnv(), prefs, telemetry.TelemetryFields{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		return &telemetryHandle{stop: func() {}}
	}
	if cfg.OnWarn == nil {
		cfg.OnWarn = func(e error) { fmt.Fprintln(os.Stderr, e.Error()) }
	}
	p, err := telemetry.Init(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		return &telemetryHandle{stop: func() {}}
	}
	ex, err := telemetry.NewEventExporter(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: exporter: %v\n", err)
		_ = p.Shutdown(ctx)
		return &telemetryHandle{stop: func() {}}
	}
	return &telemetryHandle{Provider: p, Exporter: ex, stop: func() {}}
}

// attach wires the exporter to mgr. The returned stop function releases the
// drain goroutine; call it before Provider.Shutdown so no measurements are
// lost. When telemetry is off, Attach and Shutdown are no-ops.
func (h *telemetryHandle) attach(ctx context.Context, mgr *engine.Manager) {
	if h == nil || h.Exporter == nil {
		return
	}
	h.stop = h.Exporter.Attach(ctx, mgr)
}

// registerRun caches per-run workflow metadata with the exporter so
// StepStatus / GateResult / ToolCall events pick up step_type / backend /
// transport / model labels. Called at run start by both TUI and headless.
func (h *telemetryHandle) registerRun(runID string, wf *workflow.Workflow) {
	if h == nil || h.Exporter == nil {
		return
	}
	h.Exporter.RegisterWorkflow(runID, wf)
}

// shutdown drains any buffered measurements and closes the Prometheus
// listener. Safe to call multiple times.
func (h *telemetryHandle) shutdown(ctx context.Context) {
	if h == nil {
		return
	}
	if h.stop != nil {
		h.stop()
	}
	if h.Provider != nil {
		_ = h.Provider.Shutdown(ctx)
	}
}

// mode returns the resolved exporter mode as a human-readable string ("off",
// "prom", "otlp", "both"). Empty when telemetry was never initialised. The
// TUI status line and headless start-up message use it to badge the exporter
// posture without reaching into the Provider directly.
func (h *telemetryHandle) mode() string {
	if h == nil || h.Provider == nil {
		return string(telemetry.ModeOff)
	}
	return string(h.Provider.Mode())
}
