package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newMetricReaders builds the sdkmetric.Reader set matching cfg.Mode. It is
// populated by Phase 5. Phase 1 returns an empty slice, which yields a
// [sdkmetric.MeterProvider] with no readers — every measurement is discarded
// but Meter() still returns a functional (non-noop) meter, so tests can assert
// that instruments were registered without needing an exporter.
//
// The second return is a slice of extra shutdown closers (e.g. the Prometheus
// HTTP server). They are registered in LIFO order by installMeter.
func (p *Provider) newMetricReaders(ctx context.Context) ([]sdkmetric.Reader, []func(context.Context) error, error) {
	if p == nil {
		return nil, nil, nil
	}
	// Phase 5 wires OTLP, Prometheus, and stdout metric exporters here. Keeping
	// the seam explicit means metrics/reporter tests can construct a Provider
	// today and swap in a real exporter later without changing Init.
	return p.newProdMetricReaders(ctx)
}

// newSpanProcessors mirrors newMetricReaders for traces. Phase 1 returns nil
// so installTracer falls back to a no-op tracer; Phase 5 fills this in.
func (p *Provider) newSpanProcessors(ctx context.Context) ([]sdktrace.SpanProcessor, error) {
	if p == nil {
		return nil, nil
	}
	return p.newProdSpanProcessors(ctx)
}
