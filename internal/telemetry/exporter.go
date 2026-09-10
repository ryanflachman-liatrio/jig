// Package telemetry — engine event → OTel measurement adapter (A18 Phase 3).
//
// EventExporter subscribes to a running engine.Manager and translates events
// to OTel measurements. It is read-only: the exporter never mutates engine
// state, never applies backpressure (both channels are drained with drop-on-
// full semantics matching the manager's own contract), and never emits step
// content in labels.
package telemetry

import (
	"context"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// EventExporter is a subscriber that translates engine events into OTel
// measurements. Attach it once per process (or once per Manager) — it is
// safe to attach multiple times, but each attach starts its own drain
// goroutine and consumes one subscription slot.
type EventExporter struct {
	provider *Provider
	metrics  *metrics

	mu sync.Mutex
	// workflows caches per-run workflow metadata so StepStatus events can be
	// enriched with backend/transport/model/step_type labels. RegisterWorkflow
	// populates it at run start; the exporter never queries the manager.
	workflows map[string]workflowLookup
	// stepStarts remembers the timestamp of the most recent StepStatus{To:
	// running} for a given (RunID, StepID, Attempt, Iteration, Generation)
	// tuple so terminal transitions can compute step duration without
	// touching a scheduler-owned timer.
	stepStarts map[stepKey]time.Time
	// runStarts remembers RunStarted timestamps for the run-duration histogram.
	runStarts map[string]time.Time
	// reviewStarts remembers when a ReviewRequest fired for each
	// (RunID, StepID, RoundID) so ReviewSubmitted can compute wait duration.
	reviewStarts map[reviewKey]time.Time
}

// workflowLookup is the exporter's read-only view of a running workflow.
type workflowLookup struct {
	workflow string
	steps    map[string]stepLabels
}

// stepLabels is the fixed label set derived from a workflow.Step. Every
// metric that names a step carries these labels (plus workflow / outcome).
type stepLabels struct {
	stepType  string
	backend   string
	transport string
	model     string
}

// stepKey uniquely identifies one step-attempt-iteration-generation tuple.
type stepKey struct {
	runID      string
	stepID     string
	attempt    int
	iteration  int
	generation int
}

type reviewKey struct {
	runID   string
	stepID  string
	roundID string
}

// NewEventExporter builds a subscriber bound to p. Passing a nil Provider
// returns an exporter whose Attach is a no-op — safe for callers that
// unconditionally construct one before deciding whether telemetry is on.
func NewEventExporter(p *Provider) (*EventExporter, error) {
	m, err := getMetrics(p)
	if err != nil {
		return nil, err
	}
	return &EventExporter{
		provider:     p,
		metrics:      m,
		workflows:    map[string]workflowLookup{},
		stepStarts:   map[stepKey]time.Time{},
		runStarts:    map[string]time.Time{},
		reviewStarts: map[reviewKey]time.Time{},
	}, nil
}

// RegisterWorkflow caches per-run workflow metadata so subsequent step-scoped
// events can be enriched with step_type / backend / transport / model labels
// without reaching into the manager. Called at run start by cmd/jig and the
// headless supervisor.
//
// Passing a nil workflow (or a workflow with no steps) is a no-op: the
// exporter still receives events, but step-scoped labels degrade to
// step_type="" and backend/transport/model="".
func (e *EventExporter) RegisterWorkflow(runID string, wf *workflow.Workflow) {
	if e == nil || runID == "" {
		return
	}
	lookup := workflowLookup{steps: map[string]stepLabels{}}
	if wf != nil {
		lookup.workflow = wf.Meta.Name
		for _, s := range wf.Steps {
			lookup.steps[s.ID] = stepLabels{
				stepType:  string(s.Type),
				backend:   s.Backend,
				transport: s.Transport,
				model:     s.Model,
			}
		}
	}
	e.mu.Lock()
	e.workflows[runID] = lookup
	e.mu.Unlock()
}

func (e *EventExporter) forgetRun(runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.workflows, runID)
	delete(e.runStarts, runID)
	for k := range e.stepStarts {
		if k.runID == runID {
			delete(e.stepStarts, k)
		}
	}
	for k := range e.reviewStarts {
		if k.runID == runID {
			delete(e.reviewStarts, k)
		}
	}
}

// Attach subscribes to mgr and starts a drain goroutine that translates
// events to measurements until ctx is done. Returns a stop function the
// caller may invoke to release the goroutine deterministically before ctx
// closes (useful in tests).
//
// The drain reads live and ctrl channels in a single select. Because both
// channels are drop-on-full on the producer side, the exporter never
// applies backpressure to the scheduler.
func (e *EventExporter) Attach(ctx context.Context, mgr *engine.Manager) (stop func()) {
	if e == nil || mgr == nil {
		return func() {}
	}
	drainCtx, cancel := context.WithCancel(ctx)
	live, ctrl := mgr.Subscribe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		e.drain(drainCtx, live, ctrl)
	}()

	return func() {
		cancel()
		<-done
	}
}

func (e *EventExporter) drain(ctx context.Context, live, ctrl <-chan engine.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ctrl:
			if !ok {
				return
			}
			e.handle(ctx, ev)
		case ev, ok := <-live:
			if !ok {
				return
			}
			e.handle(ctx, ev)
		}
	}
}

// handle dispatches one event to the matching instrument. Every case is
// straight-line and holds e.mu only for the map lookup / update; the OTel
// record calls are lock-free.
func (e *EventExporter) handle(ctx context.Context, ev engine.Event) {
	if e == nil || e.metrics == nil {
		return
	}
	switch v := ev.(type) {
	case engine.RunStarted:
		e.onRunStarted(ctx, v)
	case engine.RunFinished:
		e.onRunFinished(ctx, v)
	case engine.StepStatus:
		e.onStepStatus(ctx, v)
	case engine.GateResult:
		e.onGateResult(ctx, v)
	case engine.ReviewRequest:
		e.onReviewRequest(ctx, v)
	case engine.ReviewSubmitted:
		e.onReviewSubmitted(ctx, v)
	case engine.RecoveryRequest:
		e.onRecoveryRequest(ctx, v)
	case engine.IntegrationConflictRequest:
		e.onIntegrationConflict(ctx, v)
	case engine.FinalMergeRequest:
		e.onFinalMergeRequested(ctx, v)
	case engine.SecurityFinding:
		e.onSecurityFinding(ctx, v)
	case engine.FanOutExpanded:
		e.onFanOutExpanded(ctx, v)
	case engine.StepsReset:
		e.onStepsReset(ctx, v)
	}
}

func (e *EventExporter) onRunStarted(ctx context.Context, ev engine.RunStarted) {
	e.mu.Lock()
	e.runStarts[ev.RunID] = time.Now()
	// Ensure a lookup exists even if RegisterWorkflow was never called.
	if _, ok := e.workflows[ev.RunID]; !ok {
		e.workflows[ev.RunID] = workflowLookup{workflow: ev.Workflow, steps: map[string]stepLabels{}}
	} else if l := e.workflows[ev.RunID]; l.workflow == "" {
		l.workflow = ev.Workflow
		e.workflows[ev.RunID] = l
	}
	e.mu.Unlock()

	e.metrics.runStarted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("workflow", ev.Workflow),
	))
}

func (e *EventExporter) onRunFinished(ctx context.Context, ev engine.RunFinished) {
	e.mu.Lock()
	start, ok := e.runStarts[ev.RunID]
	wfName := ""
	if l, exists := e.workflows[ev.RunID]; exists {
		wfName = l.workflow
	}
	e.mu.Unlock()

	outcome := "succeeded"
	if ev.Failed {
		outcome = "failed"
	}
	labels := []attribute.KeyValue{
		attribute.String("workflow", wfName),
		attribute.String("outcome", outcome),
	}
	e.metrics.runFinished.Add(ctx, 1, metric.WithAttributes(labels...))
	if ok {
		e.metrics.runDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(labels...))
	}
	e.forgetRun(ev.RunID)
}

func (e *EventExporter) onStepStatus(ctx context.Context, ev engine.StepStatus) {
	base := e.stepLabelsFor(ev.RunID, ev.StepID)

	if ev.To == step.StatusRunning && ev.From != step.StatusRunning {
		e.mu.Lock()
		e.stepStarts[stepKey{ev.RunID, ev.StepID, ev.Attempt, ev.Iteration, ev.Generation}] = time.Now()
		e.mu.Unlock()
		e.metrics.stepStarted.Add(ctx, 1, metric.WithAttributes(base...))
		return
	}

	if !isTerminalStatus(ev.To) {
		return
	}

	terminal := append(base, attribute.String("outcome", string(ev.To)))
	terminalOpt := metric.WithAttributes(terminal...)

	e.metrics.stepFinished.Add(ctx, 1, terminalOpt)

	e.mu.Lock()
	key := stepKey{ev.RunID, ev.StepID, ev.Attempt, ev.Iteration, ev.Generation}
	start, ok := e.stepStarts[key]
	if ok {
		delete(e.stepStarts, key)
	}
	e.mu.Unlock()
	if ok {
		e.metrics.stepDuration.Record(ctx, time.Since(start).Seconds(), terminalOpt)
	}

	if ev.Cost != nil {
		e.metrics.stepCost.Record(ctx, *ev.Cost, terminalOpt)
	}
	if ev.Tokens > 0 {
		e.metrics.stepTokens.Record(ctx, int64(ev.Tokens), terminalOpt)
	}
	e.metrics.stepAttempts.Record(ctx, int64(ev.Attempt+1), terminalOpt)
	e.metrics.stepIterations.Record(ctx, int64(ev.Iteration+1), terminalOpt)
	e.metrics.stepGenerations.Record(ctx, int64(ev.Generation), terminalOpt)
}

func (e *EventExporter) onGateResult(ctx context.Context, ev engine.GateResult) {
	outcome := "passed"
	if !ev.Passed {
		outcome = "failed"
	}
	base := e.stepLabelsFor(ev.RunID, ev.StepID)
	extras := append(base, attribute.String("outcome", outcome))
	e.metrics.gateResult.Add(ctx, 1, metric.WithAttributes(extras...))
}

func (e *EventExporter) onReviewRequest(ctx context.Context, ev engine.ReviewRequest) {
	e.mu.Lock()
	e.reviewStarts[reviewKey{ev.RunID, ev.StepID, ev.RoundID}] = time.Now()
	e.mu.Unlock()
	e.metrics.reviewRequested.Add(ctx, 1, metric.WithAttributes(e.stepLabelsFor(ev.RunID, ev.StepID)...))
}

func (e *EventExporter) onReviewSubmitted(ctx context.Context, ev engine.ReviewSubmitted) {
	labels := e.stepLabelsFor(ev.RunID, ev.StepID)
	e.metrics.reviewSubmitted.Add(ctx, 1, metric.WithAttributes(labels...))

	e.mu.Lock()
	key := reviewKey{ev.RunID, ev.StepID, ev.RoundID}
	start, ok := e.reviewStarts[key]
	if ok {
		delete(e.reviewStarts, key)
	}
	e.mu.Unlock()
	if ok {
		e.metrics.reviewWaitDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(labels...))
	}
}

func (e *EventExporter) onRecoveryRequest(ctx context.Context, ev engine.RecoveryRequest) {
	base := e.stepLabelsFor(ev.RunID, ev.StepID)
	extras := append(base, attribute.Bool("can_resume", ev.CanResume))
	e.metrics.recoveryRequested.Add(ctx, 1, metric.WithAttributes(extras...))
}

func (e *EventExporter) onIntegrationConflict(ctx context.Context, ev engine.IntegrationConflictRequest) {
	e.metrics.integrationConflict.Add(ctx, 1, metric.WithAttributes(e.stepLabelsFor(ev.RunID, ev.StepID)...))
}

func (e *EventExporter) onFinalMergeRequested(ctx context.Context, ev engine.FinalMergeRequest) {
	e.mu.Lock()
	wfName := ""
	if l, ok := e.workflows[ev.RunID]; ok {
		wfName = l.workflow
	}
	e.mu.Unlock()
	e.metrics.finalMergeRequested.Add(ctx, 1, metric.WithAttributes(
		attribute.String("workflow", wfName),
	))
}

func (e *EventExporter) onSecurityFinding(ctx context.Context, ev engine.SecurityFinding) {
	base := e.stepLabelsFor(ev.RunID, ev.StepID)
	extras := append(base,
		attribute.String("tier", ev.Tier),
		attribute.String("severity", ev.Severity),
		attribute.String("action", ev.Action),
	)
	e.metrics.securityFinding.Add(ctx, 1, metric.WithAttributes(extras...))
}

func (e *EventExporter) onFanOutExpanded(ctx context.Context, ev engine.FanOutExpanded) {
	e.mu.Lock()
	wfName := ""
	if l, ok := e.workflows[ev.RunID]; ok {
		wfName = l.workflow
	}
	e.mu.Unlock()
	e.metrics.foreachExpanded.Add(ctx, int64(len(ev.Instances)), metric.WithAttributes(
		attribute.String("workflow", wfName),
		attribute.String("family", ev.FamilyID),
		attribute.String("generation", strconv.Itoa(ev.Generation)),
	))
}

func (e *EventExporter) onStepsReset(ctx context.Context, ev engine.StepsReset) {
	e.mu.Lock()
	wfName := ""
	if l, ok := e.workflows[ev.RunID]; ok {
		wfName = l.workflow
	}
	e.mu.Unlock()
	e.metrics.resetApplied.Add(ctx, 1, metric.WithAttributes(
		attribute.String("workflow", wfName),
		attribute.String("target_step", ev.Target),
	))
}

// StepLabelsFor exposes stepLabelsFor for callers outside the package (e.g.
// cmd/jig wiring MetricMux around the runner Mux). Semantics match the
// private helper; see docs/observability.md for the label allow-list.
func (e *EventExporter) StepLabelsFor(runID, stepID string) []attribute.KeyValue {
	return e.stepLabelsFor(runID, stepID)
}

// stepLabelsFor returns the fixed step-scoped label set: workflow, step,
// step_type, backend, transport, model. Every attribute is recorded even
// when empty so downstream aggregation shapes are stable.
//
// The returned slice is a copy each call so append(base, ...) at call sites
// never mutates a cached value.
func (e *EventExporter) stepLabelsFor(runID, stepID string) []attribute.KeyValue {
	e.mu.Lock()
	l, ok := e.workflows[runID]
	labels := stepLabels{}
	if ok {
		if sl, has := l.steps[stepID]; has {
			labels = sl
		}
	}
	wfName := l.workflow
	e.mu.Unlock()

	return []attribute.KeyValue{
		attribute.String("workflow", wfName),
		attribute.String("step", stepID),
		attribute.String("step_type", labels.stepType),
		attribute.String("backend", labels.backend),
		attribute.String("transport", labels.transport),
		attribute.String("model", labels.model),
	}
}

// RecordDropped increments the self-observability counter. Exposed so the
// wrapping subscriber (or a wrapping Reporter) can report drops without
// re-implementing the metric registration.
func (e *EventExporter) RecordDropped(ctx context.Context, kind string) {
	if e == nil || e.metrics == nil {
		return
	}
	e.metrics.exporterDropped.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
}

// isTerminalStatus mirrors internal/step.Status terminal semantics. Kept
// local so this package does not need a compat shim if step.Status ever adds
// a "settled" helper method.
func isTerminalStatus(s step.Status) bool {
	switch s {
	case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		return true
	}
	return false
}
