// Package telemetry — Reporter and Mux wrapping (A18 Phase 4).
//
// The engine hands a Reporter to every step executor; the runner Mux is the
// single dispatch point every step passes through. Wrapping both here lets
// this package emit per-tool counters and per-step spans without touching
// engine/runner code. Both wrappers are strict pass-throughs when telemetry
// is off (nil Provider), so wiring them at cmd/jig level is unconditional.
package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/step"
)

// TelemetryReporter wraps an engine.Reporter and increments the per-tool
// counter (jig.tool.invoked) plus optional per-tool spans on every
// ToolCall(name, detail) observation.
//
// It preserves the underlying reporter's behavior for every other method —
// Output, Message, Question, Finding are pass-through. A nil TelemetryReporter
// itself is treated as a no-op wrapper: cmd/jig can construct one before
// deciding whether telemetry is on.
type TelemetryReporter struct {
	inner   engine.Reporter
	metrics *metrics
	tracer  trace.Tracer
	// stepSpan is the parent span opened by metricMux for this step. Every
	// per-tool span is a child of stepSpan when tracing is enabled.
	stepSpan  trace.Span
	stepAttrs []attribute.KeyValue
	toolAttrs attributeCache
	networkFn func() // optional wrap over StepRequest.NetworkRequest
	// dropCounter is called with a "kind" label when a downstream drop is
	// observed. Populated by the EventExporter that owns this wrapper.
	dropCounter func(ctx context.Context, kind string)
}

// attributeCache memoizes the (tool → attribute set) mapping so a repeated
// tool observation does not allocate a fresh KeyValue slice each time.
type attributeCache struct {
	cache map[string][]attribute.KeyValue
}

func (a *attributeCache) get(tool string, base []attribute.KeyValue) []attribute.KeyValue {
	if a.cache == nil {
		a.cache = map[string][]attribute.KeyValue{}
	}
	if v, ok := a.cache[tool]; ok {
		return v
	}
	out := make([]attribute.KeyValue, 0, len(base)+1)
	out = append(out, base...)
	out = append(out, attribute.String("tool", tool))
	a.cache[tool] = out
	return out
}

// NewTelemetryReporter returns a wrapper over rep that records per-tool
// activity on p's meter. When rep is nil (test scaffolding), reads pass
// through the same no-op path as when p is nil.
func NewTelemetryReporter(rep engine.Reporter, p *Provider, base []attribute.KeyValue) *TelemetryReporter {
	m, _ := getMetrics(p)
	var tr trace.Tracer
	if p != nil {
		tr = p.Tracer(instrumentationScope)
	}
	return &TelemetryReporter{
		inner:     rep,
		metrics:   m,
		tracer:    tr,
		stepAttrs: append([]attribute.KeyValue(nil), base...),
	}
}

// SetStepSpan attaches the parent step span to this reporter. metricMux
// calls this after opening the span so per-tool spans have a parent.
func (r *TelemetryReporter) SetStepSpan(sp trace.Span) {
	if r == nil {
		return
	}
	r.stepSpan = sp
}

// Output is a pass-through. Streaming text deltas are never recorded as
// measurements (would explode cardinality); we rely on the transcript file
// as the durable content record.
func (r *TelemetryReporter) Output(delta string) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.Output(delta)
}

// ToolCall increments jig.tool.invoked and (when tracing is on) opens/closes
// a short jig.tool child span. The tool name is the only tool-scoped label;
// detail is never emitted as an attribute (would leak content).
func (r *TelemetryReporter) ToolCall(tool, detail string) {
	if r == nil {
		return
	}
	if r.inner != nil {
		r.inner.ToolCall(tool, detail)
	}
	if r.metrics != nil {
		attrs := r.toolAttrs.get(tool, r.stepAttrs)
		r.metrics.toolInvoked.Add(context.Background(), 1, metric.WithAttributes(attrs...))
	}
	// Per-tool spans piggy-back on the step span so they carry the same
	// parent trace and terminate immediately. Duration on the observation
	// itself is zero (the runner does not tell us when the tool response
	// arrives); this is deliberate — an operator wants to count tool calls,
	// not measure them.
	if r.tracer != nil {
		ctx := trace.ContextWithSpan(context.Background(), r.stepSpan)
		_, sp := r.tracer.Start(ctx, "jig.tool", trace.WithAttributes(attribute.String("tool", tool)))
		sp.End()
	}
}

// Message pass-through — the transcript already carries the payload.
func (r *TelemetryReporter) Message(seq, iteration int) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.Message(seq, iteration)
}

// Question pass-through — the human gate is metricized via ReviewRequest /
// AgentQuestion events, not through the Reporter.
func (r *TelemetryReporter) Question(ctx context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	if r == nil || r.inner == nil {
		return interaction.QuestionResponse{}
	}
	return r.inner.Question(ctx, req)
}

// Finding pass-through — jig.security.finding is emitted from the event bus
// so both the recovery gate and the exporter see the same finding.
func (r *TelemetryReporter) Finding(sf engine.SecurityFinding) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.Finding(sf)
}

// recordNetwork increments jig.tool.network_request. Called by the wrapped
// NetworkRequest callback that metricMux plants into StepRequest.
func (r *TelemetryReporter) recordNetwork() {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.toolNetwork.Add(context.Background(), 1, metric.WithAttributes(r.stepAttrs...))
}

// MetricMux is an engine.Executor wrapper that records per-step spans and
// duration histograms around the inner mux's Execute. It also wraps the
// engine.Reporter so ToolCall observations produce per-tool metrics.
//
// MetricMux is safe to install unconditionally: when the Provider is nil (or
// telemetry is off), both the span open/close and the metric record are
// no-ops backed by OTel's noop provider — the inner executor runs unchanged.
type MetricMux struct {
	inner    engine.Executor
	provider *Provider
	tracer   trace.Tracer
	metrics  *metrics
	labels   func(runID, stepID string) []attribute.KeyValue
}

// NewMetricMux returns a wrapper around inner that opens a jig.step span for
// every Execute call and records duration. labels is a function that
// resolves the fixed step-scoped label set for the (runID, stepID) tuple —
// typically EventExporter.stepLabelsFor. Passing nil labels means all step
// spans carry only step / step_type-empty defaults.
func NewMetricMux(inner engine.Executor, p *Provider, labels func(runID, stepID string) []attribute.KeyValue) *MetricMux {
	m, _ := getMetrics(p)
	var tr trace.Tracer
	if p != nil {
		tr = p.Tracer(instrumentationScope)
	}
	if labels == nil {
		labels = func(_, stepID string) []attribute.KeyValue {
			return []attribute.KeyValue{
				attribute.String("workflow", ""),
				attribute.String("step", stepID),
				attribute.String("step_type", ""),
				attribute.String("backend", ""),
				attribute.String("transport", ""),
				attribute.String("model", ""),
			}
		}
	}
	return &MetricMux{inner: inner, provider: p, tracer: tr, metrics: m, labels: labels}
}

// Execute opens a jig.step span (when tracing is enabled), builds a
// TelemetryReporter that records per-tool activity into the same trace
// context, wraps StepRequest.NetworkRequest so outbound calls increment
// jig.tool.network_request, and delegates to the inner executor.
func (m *MetricMux) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	base := m.labels(req.RunID, stepID(req))

	var span trace.Span
	if m.tracer != nil {
		attrs := append([]attribute.KeyValue(nil), base...)
		attrs = append(attrs,
			attribute.String("run.id", req.RunID),
			attribute.Int("attempt", req.Attempt),
			attribute.Int("iteration", req.Iteration),
			attribute.Int("generation", req.Generation),
		)
		if req.FanOutItem != nil {
			attrs = append(attrs,
				attribute.String("foreach.family", req.FanOutItem.TemplateID),
				attribute.String("foreach.instance", req.FanOutItem.InstanceID),
				attribute.Int("foreach.index", req.FanOutItem.Index),
				attribute.Int("foreach.total", req.FanOutItem.Total),
			)
		}
		ctx, span = m.tracer.Start(ctx, "jig.step", trace.WithAttributes(attrs...))
		defer span.End()
	}

	// Wrap the reporter so ToolCall observations flow through the per-tool
	// counter. Even if telemetry is off, this is a cheap pass-through.
	wrapper := NewTelemetryReporter(rep, m.provider, base)
	wrapper.SetStepSpan(span)

	// Wrap NetworkRequest so outbound calls increment jig.tool.network_request.
	// Preserves the original callback so the engine's own accounting still runs.
	if req.NetworkRequest != nil {
		orig := req.NetworkRequest
		req.NetworkRequest = func() {
			orig()
			wrapper.recordNetwork()
		}
	} else if m.provider != nil && m.metrics != nil {
		req.NetworkRequest = wrapper.recordNetwork
	}

	start := time.Now()
	res, err := m.inner.Execute(ctx, req, wrapper)

	if span != nil {
		outcome := "unknown"
		if res != nil {
			outcome = string(res.Status)
		}
		span.SetAttributes(
			attribute.String("outcome", outcome),
			attribute.Float64("duration_ms", float64(time.Since(start).Milliseconds())),
		)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		} else if res != nil && res.Status == step.StatusFailed {
			span.SetStatus(codes.Error, res.Err)
		}
	}

	return res, err
}

// SupportsSessionResume forwards CapSessionResume probes to the inner
// executor when it implements engine.SessionResumeSupport. The metric mux is
// otherwise transparent to backend feature detection.
func (m *MetricMux) SupportsSessionResume(backend, transport string) bool {
	sr, ok := m.inner.(engine.SessionResumeSupport)
	if !ok {
		return false
	}
	return sr.SupportsSessionResume(backend, transport)
}

// stepID extracts the step id from a StepRequest without a nil dereference
// if the request was constructed with a nil Step (never happens in
// production but useful for tests).
func stepID(req engine.StepRequest) string {
	if req.Step == nil {
		return ""
	}
	return req.Step.ID
}
