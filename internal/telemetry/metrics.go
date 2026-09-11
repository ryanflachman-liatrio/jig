// Package telemetry — metric instrument definitions (A18 Phase 3).
//
// Every instrument is registered lazily on first use so tests that never touch
// a particular family do not force it into the export. The registry is
// deliberately narrow: only signals that jig already durably persists appear
// here, so nothing this exporter emits requires new engine plumbing.
package telemetry

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// instrumentationScope is the OTel meter/tracer name jig reports.
const instrumentationScope = "jig/internal/telemetry"

// metrics is the lazy-initialized instrument set for one Provider. Callers
// obtain it via Provider.metrics(); one Provider always yields the same set
// (memoized under mu) so counters and histograms share cumulative state.
//
// Field naming mirrors the metric name suffix exactly — every field on this
// struct corresponds to one row in the docs/observability.md catalog.
type metrics struct {
	meter metric.Meter

	// Run-scoped
	runStarted  metric.Int64Counter
	runFinished metric.Int64Counter
	runCost     metric.Float64Histogram
	runTokens   metric.Int64Histogram
	runDuration metric.Float64Histogram

	// Step-scoped
	stepStarted     metric.Int64Counter
	stepFinished    metric.Int64Counter
	stepDuration    metric.Float64Histogram
	stepCost        metric.Float64Histogram
	stepTokens      metric.Int64Histogram
	stepAttempts    metric.Int64Histogram
	stepIterations  metric.Int64Histogram
	stepGenerations metric.Int64Histogram

	// Gate / review / recovery
	gateResult          metric.Int64Counter
	reviewRequested     metric.Int64Counter
	reviewSubmitted     metric.Int64Counter
	reviewWaitDuration  metric.Float64Histogram
	recoveryRequested   metric.Int64Counter
	integrationConflict metric.Int64Counter
	finalMergeRequested metric.Int64Counter

	// Security / fan-out / reset / self-observability
	securityFinding metric.Int64Counter
	foreachExpanded metric.Int64Counter
	resetApplied    metric.Int64Counter
	toolInvoked     metric.Int64Counter
	toolNetwork     metric.Int64Counter
	exporterDropped metric.Int64Counter
}

// metricsMu / cachedMetrics memoize the instrument set per Provider so
// multiple attachers share the same counters. Held as package-level for
// simplicity — one process usually has one Provider and one exporter.
var (
	metricsMu     sync.Mutex
	cachedMetrics = map[*Provider]*metrics{}
)

// getMetrics returns the memoized instrument set for p, constructing it if
// needed. Safe to call from multiple goroutines. When p is nil, returns a
// discard set backed by a no-op meter so callers never need a nil check.
func getMetrics(p *Provider) (*metrics, error) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	if p == nil {
		return buildMetrics(nil, defaultMetricPrefix)
	}
	if m, ok := cachedMetrics[p]; ok {
		return m, nil
	}
	m, err := buildMetrics(p.Meter(instrumentationScope), p.cfg.MetricPrefix)
	if err != nil {
		return nil, err
	}
	cachedMetrics[p] = m
	return m, nil
}

// resetMetricsCacheForTest lets tests construct fresh Providers without
// leaking cached instruments between runs. Only intended for use inside
// _test.go files.
func resetMetricsCacheForTest() {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	cachedMetrics = map[*Provider]*metrics{}
}

// buildMetrics constructs every instrument this exporter knows about. Every
// instrument follows OTel/Prom name conventions: dotted namespace with the
// configurable prefix. A nil meter falls back to a no-op Meter so calls to
// each instrument silently discard.
func buildMetrics(meter metric.Meter, prefix string) (*metrics, error) {
	if prefix == "" {
		prefix = defaultMetricPrefix
	}
	if meter == nil {
		meter = metricnoop.NewMeterProvider().Meter(instrumentationScope)
	}
	name := func(suffix string) string { return prefix + "." + suffix }

	var (
		m   metrics
		err error
	)
	m.meter = meter

	m.runStarted, err = meter.Int64Counter(name("run.started"),
		metric.WithDescription("Total runs started."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.runFinished, err = meter.Int64Counter(name("run.finished"),
		metric.WithDescription("Total runs finished."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.runCost, err = meter.Float64Histogram(name("run.cost_usd"),
		metric.WithDescription("Total USD cost of a run at RunFinished."),
		metric.WithUnit("USD"))
	if err != nil {
		return nil, wrap(err)
	}
	m.runTokens, err = meter.Int64Histogram(name("run.tokens"),
		metric.WithDescription("Total tokens processed by a run at RunFinished."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.runDuration, err = meter.Float64Histogram(name("run.duration"),
		metric.WithDescription("Wall-clock seconds from RunStarted to RunFinished."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, wrap(err)
	}

	m.stepStarted, err = meter.Int64Counter(name("step.started"),
		metric.WithDescription("Total step attempts started."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepFinished, err = meter.Int64Counter(name("step.finished"),
		metric.WithDescription("Total step attempts finished (any terminal transition)."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepDuration, err = meter.Float64Histogram(name("step.duration"),
		metric.WithDescription("Wall-clock seconds between running and terminal for one step attempt."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepCost, err = meter.Float64Histogram(name("step.cost_usd"),
		metric.WithDescription("SDK-reported USD cost on a step's terminal transition (recorded only when non-nil)."),
		metric.WithUnit("USD"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepTokens, err = meter.Int64Histogram(name("step.tokens"),
		metric.WithDescription("SDK-reported tokens on a step's terminal transition."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepAttempts, err = meter.Int64Histogram(name("step.attempts"),
		metric.WithDescription("attempt+1 on a step's terminal transition."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepIterations, err = meter.Int64Histogram(name("step.iterations"),
		metric.WithDescription("iteration+1 on a step's terminal transition."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.stepGenerations, err = meter.Int64Histogram(name("step.generations"),
		metric.WithDescription("generation on a step's terminal transition."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}

	m.gateResult, err = meter.Int64Counter(name("gate.result"),
		metric.WithDescription("[step.validate] gate results."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.reviewRequested, err = meter.Int64Counter(name("review.requested"),
		metric.WithDescription("Review gates requested."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.reviewSubmitted, err = meter.Int64Counter(name("review.submitted"),
		metric.WithDescription("Review gates submitted."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.reviewWaitDuration, err = meter.Float64Histogram(name("review.wait_duration"),
		metric.WithDescription("Seconds between ReviewRequest and matching ReviewSubmitted."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, wrap(err)
	}
	m.recoveryRequested, err = meter.Int64Counter(name("recovery.requested"),
		metric.WithDescription("RecoveryRequest events."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.integrationConflict, err = meter.Int64Counter(name("integration.conflict"),
		metric.WithDescription("IntegrationConflictRequest events."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.finalMergeRequested, err = meter.Int64Counter(name("final_merge.requested"),
		metric.WithDescription("FinalMergeRequest events."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}

	m.securityFinding, err = meter.Int64Counter(name("security.finding"),
		metric.WithDescription("SecurityFinding events by tier + severity."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.foreachExpanded, err = meter.Int64Counter(name("foreach.expanded"),
		metric.WithDescription("FanOutExpanded events by declared family step id."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.resetApplied, err = meter.Int64Counter(name("reset.applied"),
		metric.WithDescription("StepsReset events by target."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.toolInvoked, err = meter.Int64Counter(name("tool.invoked"),
		metric.WithDescription("Agent tool_use observations, labelled by tool name."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.toolNetwork, err = meter.Int64Counter(name("tool.network_request"),
		metric.WithDescription("Outbound network requests observed by the runner's NetworkRequest hook."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}
	m.exporterDropped, err = meter.Int64Counter(name("exporter.dropped"),
		metric.WithDescription("Events the telemetry exporter had to drop due to backpressure."),
		metric.WithUnit("1"))
	if err != nil {
		return nil, wrap(err)
	}

	return &m, nil
}

// wrap annotates instrument-creation errors so a bad prefix / duplicate name
// surfaces clearly in installMeter's log-and-downgrade path.
func wrap(err error) error {
	return fmt.Errorf("create instrument: %w", err)
}
