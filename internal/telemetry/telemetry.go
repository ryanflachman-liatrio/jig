// Package telemetry implements A18 — the optional OpenTelemetry / Prometheus /
// OTLP exporter for jig's already-durable run and step signals.
//
// The package is off by default: a zero Config or Mode == ModeOff builds a
// [Provider] that returns no-op OTel Meter/Tracer implementations, dials no
// endpoints, and binds no listeners. Existing installs behave byte-identically
// when nothing opts in.
//
// Boundaries kept deliberately narrow (docs/plans/a18-otel-prometheus-export.md
// "Architecture and ownership"):
//
//   - This is the only package that imports "go.opentelemetry.io/otel/**".
//   - It imports "jig/internal/engine" (event types), "jig/internal/workflow"
//     (Telemetry config type), and "jig/internal/step" (status enum). It does
//     not import "jig/internal/tui".
//   - It never mutates engine state. The exporter attaches as a subscriber
//     (Manager.Subscribe) and never applies backpressure.
//   - Persistence-off (root == "") remains valid: prefs writes no-op, metric
//     recording works, and no filesystem side effects occur.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.36.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// serviceName is the default service.name attribute used when neither
// OTEL_SERVICE_NAME nor Config.ServiceName is set. Operators can override it
// via env or Config.
const serviceName = "jig"

// Provider owns the OTel MeterProvider and TracerProvider selected for a run
// (or for the whole TUI process). Callers never construct one directly — use
// [Init]. Shutdown is idempotent so both TUI and headless entry points can
// defer it without coordinating ownership.
//
// Provider satisfies the "off by default" contract: if the resolved [Config]
// has Mode == ModeOff, [Init] returns a Provider whose Meter/Tracer are OTel
// no-op implementations and whose Shutdown is a no-op. Callers never have to
// nil-check.
type Provider struct {
	cfg    Config
	res    *resource.Resource
	meter  metric.MeterProvider
	tracer trace.TracerProvider

	// closeFns run in reverse order on Shutdown. They cover exporter Shutdown,
	// MeterProvider/TracerProvider Shutdown, and the Prometheus HTTP listener.
	// A single closer must be idempotent — Shutdown may be invoked more than
	// once (deferred in both TUI and headless paths).
	mu       sync.Mutex
	closed   bool
	closeFns []func(context.Context) error
}

// Mode selects the exporter surface.
//
// The zero value is ModeOff, which is what the "off by default" invariant
// relies on: an uninitialized Config or a Provider from [Init] with no env /
// workflow / prefs opt-in binds nothing and dials nothing.
type Mode string

const (
	ModeOff  Mode = "off"
	ModeProm Mode = "prom"
	ModeOTLP Mode = "otlp"
	ModeBoth Mode = "both"
)

// Init returns a Provider matching cfg. It never returns nil; a Provider with
// Mode == ModeOff hands out OTel no-op instruments.
//
// Init is intentionally forgiving: an exporter that fails to construct (e.g.
// OTLP dial-time errors, Prometheus listener bind failure) logs one warning
// via cfg.OnWarn (if set) and downgrades to a no-op provider. jig run exit
// codes are unaffected by exporter failures.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
	cfg = cfg.withDefaults()

	if cfg.Mode == ModeOff {
		return newNoopProvider(cfg), nil
	}

	cfg.warnIfIncomplete()

	res, err := buildResource(ctx, cfg)
	if err != nil {
		cfg.warn(fmt.Errorf("telemetry: build resource: %w", err))
		return newNoopProvider(cfg), nil
	}

	p := &Provider{cfg: cfg, res: res}

	if err := p.installMeter(ctx); err != nil {
		cfg.warn(fmt.Errorf("telemetry: install meter provider: %w", err))
		p.meter = metricnoop.NewMeterProvider()
	}
	if err := p.installTracer(ctx); err != nil {
		cfg.warn(fmt.Errorf("telemetry: install tracer provider: %w", err))
		p.tracer = tracenoop.NewTracerProvider()
	}

	return p, nil
}

// newNoopProvider builds a Provider whose Meter/Tracer are OTel no-ops. Used
// for Mode == ModeOff and as a safe downgrade when exporter installation
// fails.
func newNoopProvider(cfg Config) *Provider {
	return &Provider{
		cfg:    cfg,
		meter:  metricnoop.NewMeterProvider(),
		tracer: tracenoop.NewTracerProvider(),
	}
}

// Mode returns the resolved exporter mode. Callers (TUI status indicator,
// tests) use this to render a human-readable state without exposing OTel
// internals.
func (p *Provider) Mode() Mode {
	if p == nil {
		return ModeOff
	}
	return p.cfg.Mode
}

// PrometheusAddr returns the resolved Prometheus bind address ("" when the
// listener is disabled). Useful for the TUI status indicator and for tests
// that need to know which port the listener chose (":0" bind).
func (p *Provider) PrometheusAddr() string {
	if p == nil {
		return ""
	}
	return p.cfg.PrometheusAddr
}

// Meter returns the underlying MeterProvider's meter for the given
// instrumentation scope. Always safe to call — a Provider from Init with
// ModeOff returns a no-op meter that discards every measurement.
func (p *Provider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	if p == nil {
		return metricnoop.NewMeterProvider().Meter(name, opts...)
	}
	return p.meter.Meter(name, opts...)
}

// Tracer returns the underlying TracerProvider's tracer for the given scope.
// Always safe; a no-op Provider returns a discarding tracer.
func (p *Provider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	if p == nil {
		return tracenoop.NewTracerProvider().Tracer(name, opts...)
	}
	return p.tracer.Tracer(name, opts...)
}

// Resource returns the OTel resource used by this provider. Exposed for tests
// that need to assert on merged resource attributes.
func (p *Provider) Resource() *resource.Resource {
	if p == nil {
		return resource.Empty()
	}
	if p.res == nil {
		return resource.Empty()
	}
	return p.res
}

// Shutdown flushes any buffered measurements and stops every installed
// exporter and listener. It is safe to call more than once (idempotent) so
// both TUI and headless paths can defer it without coordinating ownership.
// A canceled or deadline-expired ctx surfaces the ctx error but does not
// suppress the shutdown attempts of remaining closers.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	fns := p.closeFns
	p.closeFns = nil
	p.mu.Unlock()

	var errs []error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := fns[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// installMeter is filled in during Phase 5 (exporters). Phase 1 keeps a
// safe fallback so an incomplete install never panics.
func (p *Provider) installMeter(ctx context.Context) error {
	// installMeter is populated by installMeterExporters in exporters.go once
	// Phase 5 wires OTLP / Prometheus / stdout metric exporters. Until then,
	// only the noop path can produce a meter.
	reader, closer, err := p.newMetricReaders(ctx)
	if err != nil {
		return err
	}

	opts := []sdkmetric.Option{sdkmetric.WithResource(p.res)}
	for _, r := range reader {
		opts = append(opts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(opts...)
	p.meter = mp
	p.addCloser(mp.Shutdown)
	for _, fn := range closer {
		p.addCloser(fn)
	}
	return nil
}

// installTracer is filled in during Phase 5. Same safe fallback pattern as
// installMeter.
func (p *Provider) installTracer(ctx context.Context) error {
	spanProcessors, err := p.newSpanProcessors(ctx)
	if err != nil {
		return err
	}

	if len(spanProcessors) == 0 {
		p.tracer = tracenoop.NewTracerProvider()
		return nil
	}
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(p.res)}
	for _, sp := range spanProcessors {
		opts = append(opts, sdktrace.WithSpanProcessor(sp))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	p.tracer = tp
	p.addCloser(tp.Shutdown)
	return nil
}

// addCloser registers a shutdown function to run on Provider.Shutdown. Order
// is LIFO so exporters flush before providers stop and providers stop before
// listeners close, matching OTel guidance.
func (p *Provider) addCloser(fn func(context.Context) error) {
	if fn == nil {
		return
	}
	p.mu.Lock()
	p.closeFns = append(p.closeFns, fn)
	p.mu.Unlock()
}

// buildResource merges the OTel default resource with jig-declared
// attributes and any operator-provided attributes.
func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	base := resource.Default()
	name := cfg.ServiceName
	if name == "" {
		name = serviceName
	}
	attrs := []attribute.KeyValue{semconv.ServiceName(name)}
	for k, v := range cfg.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}
	own := resource.NewSchemaless(attrs...)
	merged, err := resource.Merge(base, own)
	if err != nil {
		return base, err
	}
	_ = ctx // reserved for future detector plumbing
	return merged, nil
}
