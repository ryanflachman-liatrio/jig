package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestProviderWithReader returns a Provider whose MeterProvider is backed by
// the caller-supplied ManualReader. Tests use this to drive the exporter
// through a full workflow and then inspect ResourceMetrics directly, without
// needing OTLP or Prometheus wire formats to work in the test process.
//
// The returned Provider owns the reader's Shutdown call, so callers get the
// same idempotent shutdown story as production Init.
//
// This helper lives in a non-_test.go file so other packages (Phase 4 tests
// under internal/telemetry, and later integration tests in cmd/jig) can share
// the harness without duplicating the reader wiring.
func TestProviderWithReader(cfg Config, reader sdkmetric.Reader, sp ...sdktrace.SpanProcessor) *Provider {
	cfg = cfg.withDefaults()
	res, _ := buildResource(context.Background(), cfg)
	p := &Provider{cfg: cfg, res: res}

	opts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if reader != nil {
		opts = append(opts, sdkmetric.WithReader(reader))
	}
	mp := sdkmetric.NewMeterProvider(opts...)
	p.meter = mp
	p.addCloser(mp.Shutdown)

	if len(sp) > 0 {
		tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
		for _, s := range sp {
			tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(s))
		}
		tp := sdktrace.NewTracerProvider(tpOpts...)
		p.tracer = tp
		p.addCloser(tp.Shutdown)
	}

	// Fresh Provider means fresh instruments; without this, cachedMetrics
	// from a previous test's Provider would leak in.
	resetMetricsCacheForTest()

	return p
}
