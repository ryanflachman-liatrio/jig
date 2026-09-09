package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newProdMetricReaders and newProdSpanProcessors are the Phase 5 real
// implementations. Phase 1 leaves them empty so Init succeeds with a bare
// [sdkmetric.MeterProvider] / [tracenoop.TracerProvider].
//
// Placing the placeholders in their own file (with matching production
// bodies later) keeps the seam clean without introducing build tags.
func (p *Provider) newProdMetricReaders(ctx context.Context) ([]sdkmetric.Reader, []func(context.Context) error, error) {
	_ = ctx
	return nil, nil, nil
}

func (p *Provider) newProdSpanProcessors(ctx context.Context) ([]sdktrace.SpanProcessor, error) {
	_ = ctx
	return nil, nil
}
